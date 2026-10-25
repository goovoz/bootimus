package housekeeping

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/storage"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testStore struct {
	*storage.SQLiteStore
	raw *gorm.DB
}

func newStore(t *testing.T) *testStore {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	raw, err := gorm.Open(sqlite.Open(filepath.Join(dir, "bootimus.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &testStore{SQLiteStore: store, raw: raw}
}

func (s *testStore) DB() *gorm.DB { return s.raw }

func daysAgo(n int) time.Time {
	return time.Now().AddDate(0, 0, -n)
}

func seedBootLogs(t *testing.T, store *testStore, ages ...int) {
	t.Helper()
	for i, age := range ages {
		if err := store.LogBootAttempt("aa:bb:cc:dd:ee:00", "image", "10.0.0.1", true, ""); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().Model(&models.BootLog{}).Where("id = ?", i+1).Update("created_at", daysAgo(age)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(t *testing.T, store *testStore, model interface{}) int64 {
	t.Helper()
	var n int64
	if err := store.DB().Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneBootLogsRespectsRetention(t *testing.T) {
	store := newStore(t)
	seedBootLogs(t, store, 1, 50, 200, 400)

	settings := &models.HousekeepingSettings{BootLogDays: 90}
	affected, err := pruneBootLogs(store).Run(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	if n := countRows(t, store, &models.BootLog{}); n != 2 {
		t.Fatalf("remaining boot logs = %d, want 2", n)
	}
}

func TestPruneBootLogsDisabledWhenZero(t *testing.T) {
	store := newStore(t)
	seedBootLogs(t, store, 400)

	_, err := pruneBootLogs(store).Run(t.Context(), &models.HousekeepingSettings{})
	if err != ErrDisabled {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if n := countRows(t, store, &models.BootLog{}); n != 1 {
		t.Fatalf("boot logs = %d, want 1", n)
	}
}

func TestPruneInventoryHistoryKeepsLatestPerClient(t *testing.T) {
	store := newStore(t)
	for i, age := range []int{300, 200, 5} {
		inv := &models.HardwareInventory{MACAddress: "aa:bb:cc:dd:ee:01", Product: "box"}
		if err := store.SaveHardwareInventory(inv); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().Model(&models.HardwareInventory{}).Where("id = ?", i+1).Update("created_at", daysAgo(age)).Error; err != nil {
			t.Fatal(err)
		}
	}
	old := &models.HardwareInventory{MACAddress: "aa:bb:cc:dd:ee:02", Product: "lonely"}
	if err := store.SaveHardwareInventory(old); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&models.HardwareInventory{}).Where("id = ?", 4).Update("created_at", daysAgo(500)).Error; err != nil {
		t.Fatal(err)
	}

	affected, err := pruneInventoryHistory(store).Run(t.Context(), &models.HousekeepingSettings{InventoryHistoryDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	latest, err := store.GetLatestHardwareInventory("aa:bb:cc:dd:ee:02")
	if err != nil || latest.Product != "lonely" {
		t.Fatalf("latest inventory for the quiet client should survive, got %v %v", latest, err)
	}
	history, err := store.GetHardwareInventoryHistory("aa:bb:cc:dd:ee:01", 10)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %d entries (%v), want 1", len(history), err)
	}
}

func TestPruneDiscoveredClientsLeavesStaticAndRecent(t *testing.T) {
	store := newStore(t)
	stale := daysAgo(100)
	recent := daysAgo(2)
	clients := []*models.Client{
		{MACAddress: "aa:bb:cc:dd:ee:10", Static: false, Enabled: true, LastBoot: &stale},
		{MACAddress: "aa:bb:cc:dd:ee:11", Static: false, Enabled: true, LastBoot: &recent},
		{MACAddress: "aa:bb:cc:dd:ee:12", Static: true, Enabled: true, LastBoot: &stale},
		{MACAddress: "aa:bb:cc:dd:ee:13", Static: false, Enabled: true, EnrollmentState: models.EnrollmentStatePending, LastBoot: &stale},
		{MACAddress: "aa:bb:cc:dd:ee:14", Static: false, Enabled: true},
	}
	for _, c := range clients {
		if err := store.CreateClient(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DB().Model(&models.Client{}).Where("mac_address = ?", "aa:bb:cc:dd:ee:14").Update("created_at", daysAgo(120)).Error; err != nil {
		t.Fatal(err)
	}

	affected, err := pruneDiscoveredClients(store).Run(t.Context(), &models.HousekeepingSettings{DiscoveredClientDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	for _, mac := range []string{"aa:bb:cc:dd:ee:11", "aa:bb:cc:dd:ee:12", "aa:bb:cc:dd:ee:13"} {
		if _, err := store.GetClient(mac); err != nil {
			t.Errorf("%s should have been kept: %v", mac, err)
		}
	}
	for _, mac := range []string{"aa:bb:cc:dd:ee:10", "aa:bb:cc:dd:ee:14"} {
		if _, err := store.GetClient(mac); err == nil {
			t.Errorf("%s should have been pruned", mac)
		}
	}
}

func TestRunNowRecordsReport(t *testing.T) {
	store := newStore(t)
	seedBootLogs(t, store, 400)

	runner := New(store)
	report := runner.RunNow()
	if report.Status != StatusOK {
		t.Fatalf("status = %q, want ok", report.Status)
	}
	if len(report.Tasks) != 4 {
		t.Fatalf("tasks = %d, want 4", len(report.Tasks))
	}
	if report.Tasks[0].Name != "boot_logs" || report.Tasks[0].Affected != 1 {
		t.Fatalf("boot log task = %+v", report.Tasks[0])
	}
	if !report.Tasks[2].Skipped {
		t.Fatalf("discovered client task should be skipped by default: %+v", report.Tasks[2])
	}

	settings, err := store.GetHousekeepingSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.LastRun == nil || settings.LastStatus != StatusOK {
		t.Fatalf("last run not recorded: %+v", settings)
	}
	if ParseReport(settings.LastReport) == nil {
		t.Fatalf("stored report is not parseable: %q", settings.LastReport)
	}
	if settings.BootLogDays != storage.DefaultBootLogDays {
		t.Fatalf("defaults should survive a run: %+v", settings)
	}
}

func TestPruneAuditLogsRespectsRetention(t *testing.T) {
	store := newStore(t)
	for i, age := range []int{10, 400, 800} {
		if err := store.CreateAuditLog(&models.AuditLog{Username: "admin", Method: "PUT", Path: "/api/clients", Status: 200}); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().Model(&models.AuditLog{}).Where("id = ?", i+1).Update("created_at", daysAgo(age)).Error; err != nil {
			t.Fatal(err)
		}
	}

	affected, err := pruneAuditLogs(store).Run(t.Context(), &models.HousekeepingSettings{AuditLogDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	if n := countRows(t, store, &models.AuditLog{}); n != 1 {
		t.Fatalf("remaining audit logs = %d, want 1", n)
	}
}

func TestDataDirUsageSizesTopLevelEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "isos", "linux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "isos", "linux", "a.iso"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "isos", "b.iso"), make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootimus.db"), make([]byte, 20), 0o644); err != nil {
		t.Fatal(err)
	}

	InvalidateUsage()
	usage, err := DataDirUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if usage.TotalBytes != 1520 {
		t.Fatalf("total = %d, want 1520", usage.TotalBytes)
	}
	if len(usage.Entries) != 2 || usage.Entries[0].Name != "isos" || usage.Entries[0].Files != 2 || usage.Entries[0].Bytes != 1500 {
		t.Fatalf("entries = %+v", usage.Entries)
	}
}
