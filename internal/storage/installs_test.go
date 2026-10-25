package storage

import (
	"testing"
	"time"

	"bootimus/internal/models"
)

func TestInstallRecordsLifecycle(t *testing.T) {
	store := newAuditTestStore(t)
	if err := store.CreateClient(&models.Client{MACAddress: "aa:bb:cc:dd:ee:01", Name: "lab", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	first := &models.InstallRecord{MACAddress: "aa:bb:cc:dd:ee:01", ImageFilename: "ubuntu.iso", ImageName: "Ubuntu", Token: "tok-1", State: models.InstallStateBooted, BootedAt: time.Now().Add(-10 * time.Hour)}
	second := &models.InstallRecord{MACAddress: "aa:bb:cc:dd:ee:01", ImageFilename: "ubuntu.iso", ImageName: "Ubuntu", Token: "tok-2", State: models.InstallStateBooted, BootedAt: time.Now()}
	other := &models.InstallRecord{MACAddress: "aa:bb:cc:dd:ee:02", ImageFilename: "debian.iso", ImageName: "Debian", Token: "tok-3", State: models.InstallStateCompleted, BootedAt: time.Now()}
	for _, rec := range []*models.InstallRecord{first, second, other} {
		if err := store.CreateInstallRecord(rec); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.GetInstallRecordByToken("tok-2")
	if err != nil || got.ID != second.ID {
		t.Fatalf("token lookup = %+v, %v", got, err)
	}
	if _, err := store.GetInstallRecordByToken("missing"); err == nil {
		t.Fatal("unknown token should not resolve")
	}

	latest, err := store.LatestOpenInstallRecord("aa:bb:cc:dd:ee:01", "ubuntu.iso")
	if err != nil || latest.ID != second.ID {
		t.Fatalf("latest open = %+v, %v", latest, err)
	}
	if _, err := store.LatestOpenInstallRecord("aa:bb:cc:dd:ee:02", ""); err == nil {
		t.Fatal("completed records are not open")
	}

	recs, err := store.ListInstallRecords("aa:bb:cc:dd:ee:01", 10)
	if err != nil || len(recs) != 2 || recs[0].ID != second.ID {
		t.Fatalf("list for mac = %+v, %v", recs, err)
	}
	all, err := store.ListInstallRecords("", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("list all = %d, %v", len(all), err)
	}

	stale, err := store.ListOpenInstallRecordsBefore(time.Now().Add(-6 * time.Hour))
	if err != nil || len(stale) != 1 || stale[0].ID != first.ID {
		t.Fatalf("stale = %+v, %v", stale, err)
	}

	now := time.Now()
	second.State = models.InstallStateCompleted
	second.FinishedAt = &now
	if err := store.UpdateInstallRecord(second); err != nil {
		t.Fatal(err)
	}
	if err := store.SetClientInstallStatus("aa:bb:cc:dd:ee:01", models.InstallStateCompleted, "Ubuntu", now); err != nil {
		t.Fatal(err)
	}
	client, err := store.GetClient("aa:bb:cc:dd:ee:01")
	if err != nil {
		t.Fatal(err)
	}
	if client.InstallState != models.InstallStateCompleted || client.InstallImage != "Ubuntu" || client.InstallUpdatedAt == nil {
		t.Fatalf("client status not updated: %+v", client)
	}
	latest, err = store.LatestOpenInstallRecord("aa:bb:cc:dd:ee:01", "ubuntu.iso")
	if err != nil || latest.ID != first.ID {
		t.Fatalf("after completion the older booted record should be the open one, got %+v, %v", latest, err)
	}
}
