package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/storage"
	"bootimus/internal/webhook"
)

const installTestMAC = "aa:bb:cc:dd:ee:10"

func newInstallEnv(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.CreateClient(&models.Client{MACAddress: installTestMAC, Name: "bench-1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		config:          &Config{Storage: store, ServerAddr: "10.0.0.1", HTTPPort: 8080, DataDir: t.TempDir()},
		webhookNotifier: webhook.New(store),
	}
	return srv, store
}

func installTestImage(t *testing.T, store *storage.SQLiteStore, filename string, autoInstall bool) *models.Image {
	t.Helper()
	img := &models.Image{Filename: filename, Name: strings.TrimSuffix(filename, ".iso"), Enabled: true, BootMethod: "kernel", Distro: "rocky"}
	if autoInstall {
		img.AutoInstallEnabled = true
		img.AutoInstallScriptType = "kickstart"
		img.AutoInstallScript = "%post\ncurl -fsS \"{{CALLBACK_URL}}\"\necho {{INSTALL_TOKEN}} > /root/token\n%end\n"
	}
	if err := store.CreateImage(img); err != nil {
		t.Fatal(err)
	}
	return img
}

func formRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/install/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestImageBootRecordsInstallOnlyForAutoInstallImages(t *testing.T) {
	srv, store := newInstallEnv(t)
	plain := installTestImage(t, store, "plain.iso", false)
	unattended := installTestImage(t, store, "rocky.iso", true)
	client, _ := store.GetClient(installTestMAC)

	srv.noteImageBoot(installTestMAC, plain, client)
	if recs, _ := store.ListInstallRecords(installTestMAC, 10); len(recs) != 0 {
		t.Fatalf("plain image should not be tracked, got %+v", recs)
	}

	srv.noteImageBoot(installTestMAC, unattended, client)
	srv.noteImageBoot(installTestMAC, unattended, client)
	recs, _ := store.ListInstallRecords(installTestMAC, 10)
	if len(recs) != 1 || recs[0].State != models.InstallStateBooted || recs[0].Token == "" {
		t.Fatalf("expected one booted record with a token, got %+v", recs)
	}
	updated, _ := store.GetClient(installTestMAC)
	if updated.InstallState != models.InstallStateBooted || updated.InstallImage != "rocky" {
		t.Fatalf("client status not set: %+v", updated)
	}
}

func TestAutoInstallFetchIssuesTokenAndCallbackCompletes(t *testing.T) {
	srv, store := newInstallEnv(t)
	img := installTestImage(t, store, "rocky.iso", true)
	client, _ := store.GetClient(installTestMAC)
	srv.noteImageBoot(installTestMAC, img, client)

	rec := httptest.NewRecorder()
	srv.handleAutoInstallScript(rec, httptest.NewRequest(http.MethodGet, "/autoinstall/rocky.iso?mac=AA-BB-CC-DD-EE-10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("autoinstall fetch returned %d: %s", rec.Code, rec.Body.String())
	}
	open, err := store.LatestOpenInstallRecord(installTestMAC, "rocky.iso")
	if err != nil {
		t.Fatal(err)
	}
	if open.State != models.InstallStateInstalling || open.InstallingAt == nil {
		t.Fatalf("record should be installing after the file was fetched: %+v", open)
	}
	body := rec.Body.String()
	wantURL := "http://10.0.0.1:8080/install/callback?token=" + open.Token
	if !strings.Contains(body, wantURL) || !strings.Contains(body, "echo "+open.Token+" >") {
		t.Fatalf("placeholders not substituted:\n%s", body)
	}
	if strings.Contains(body, "{{") {
		t.Fatalf("unsubstituted placeholder left:\n%s", body)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/install/callback?token="+open.Token+"&status=completed&message=all+good", nil)
	req.RemoteAddr = "192.0.2.50:4444"
	srv.handleInstallCallback(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("callback returned %d: %s", rec.Code, rec.Body.String())
	}
	done, _ := store.GetInstallRecordByToken(open.Token)
	if done.State != models.InstallStateCompleted || done.FinishedAt == nil || done.Message != "all good" || done.ReporterIP != "192.0.2.50" {
		t.Fatalf("record not completed: %+v", done)
	}
	updated, _ := store.GetClient(installTestMAC)
	if updated.InstallState != models.InstallStateCompleted {
		t.Fatalf("client status = %q, want completed", updated.InstallState)
	}

	rec = httptest.NewRecorder()
	srv.handleInstallCallback(rec, formRequest("token="+open.Token+"&status=failed"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already recorded as completed") {
		t.Fatalf("second callback should be idempotent, got %d: %s", rec.Code, rec.Body.String())
	}
	again, _ := store.GetInstallRecordByToken(open.Token)
	if again.State != models.InstallStateCompleted {
		t.Fatalf("second callback changed the state to %q", again.State)
	}
}

func TestInstallCallbackRejectsBadInput(t *testing.T) {
	srv, store := newInstallEnv(t)
	img := installTestImage(t, store, "rocky.iso", true)
	token := srv.noteAutoInstallFetch(installTestMAC, img)
	if token == "" {
		t.Fatal("expected a token from a direct auto-install fetch")
	}

	cases := []struct {
		url  string
		code int
	}{
		{"/install/callback", http.StatusBadRequest},
		{"/install/callback?token=nope", http.StatusNotFound},
		{"/install/callback?token=" + token + "&status=sideways", http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.handleInstallCallback(rec, httptest.NewRequest(http.MethodGet, c.url, nil))
		if rec.Code != c.code {
			t.Errorf("%s: got %d, want %d", c.url, rec.Code, c.code)
		}
	}

	rec := httptest.NewRecorder()
	srv.handleInstallCallback(rec, formRequest("token="+token+"&status=failed&message=disk+too+small"))
	if rec.Code != http.StatusOK {
		t.Fatalf("failed report returned %d", rec.Code)
	}
	done, _ := store.GetInstallRecordByToken(token)
	if done.State != models.InstallStateFailed || done.Message != "disk too small" {
		t.Fatalf("failed report not recorded: %+v", done)
	}
}

func TestAutoInstallWithoutMACGetsEmptyToken(t *testing.T) {
	srv, store := newInstallEnv(t)
	installTestImage(t, store, "rocky.iso", true)
	rec := httptest.NewRecorder()
	srv.handleAutoInstallScript(rec, httptest.NewRequest(http.MethodGet, "/autoinstall/rocky.iso", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "/install/callback?token=\"") || strings.Contains(body, "{{") {
		t.Fatalf("anonymous fetch should render an empty token:\n%s", body)
	}
	if recs, _ := store.ListInstallRecords("", 10); len(recs) != 0 {
		t.Fatalf("anonymous fetch must not create records, got %+v", recs)
	}
}

func TestSweepMarksTimedOutInstallsFailed(t *testing.T) {
	srv, store := newInstallEnv(t)
	srv.config.InstallTimeoutHours = 2
	old := time.Now().Add(-3 * time.Hour)
	fresh := time.Now().Add(-30 * time.Minute)
	stale := &models.InstallRecord{MACAddress: installTestMAC, ImageFilename: "rocky.iso", ImageName: "rocky", Token: "stale", State: models.InstallStateInstalling, BootedAt: old, InstallingAt: &old}
	live := &models.InstallRecord{MACAddress: installTestMAC, ImageFilename: "rocky.iso", ImageName: "rocky", Token: "live", State: models.InstallStateBooted, BootedAt: fresh}
	for _, rec := range []*models.InstallRecord{stale, live} {
		if err := store.CreateInstallRecord(rec); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.sweepStaleInstalls(); n != 1 {
		t.Fatalf("swept %d records, want 1", n)
	}
	got, _ := store.GetInstallRecordByToken("stale")
	if got.State != models.InstallStateFailed || !strings.Contains(got.Message, "timed out after 2h") {
		t.Fatalf("stale record = %+v", got)
	}
	still, _ := store.GetInstallRecordByToken("live")
	if still.State != models.InstallStateBooted {
		t.Fatalf("fresh record should be untouched, got %+v", still)
	}
}

func TestInstallWebhookEvents(t *testing.T) {
	srv, store := newInstallEnv(t)
	var mu sync.Mutex
	var events []webhook.Event
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev webhook.Event
		json.Unmarshal(body, &ev)
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}))
	defer hook.Close()
	if err := store.UpdateWebhookConfig(&models.WebhookConfig{URL: hook.URL, Enabled: true, OnInstall: true}); err != nil {
		t.Fatal(err)
	}

	img := installTestImage(t, store, "rocky.iso", true)
	token := srv.noteAutoInstallFetch(installTestMAC, img)
	rec := httptest.NewRecorder()
	srv.handleInstallCallback(rec, httptest.NewRequest(http.MethodGet, "/install/callback?token="+token+"&status=failed&message=boom", nil))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected one webhook delivery, got %d", len(events))
	}
	ev := events[0]
	if ev.Event != webhook.EventInstallFailed || ev.MAC != installTestMAC || ev.ClientName != "bench-1" || ev.Image != "rocky" || ev.Metadata["message"] != "boom" {
		t.Fatalf("unexpected event %+v", ev)
	}

	if err := store.UpdateWebhookConfig(&models.WebhookConfig{URL: hook.URL, Enabled: true, OnInstall: false}); err != nil {
		t.Fatal(err)
	}
	token = srv.noteAutoInstallFetch(installTestMAC, img)
	rec = httptest.NewRecorder()
	srv.handleInstallCallback(rec, httptest.NewRequest(http.MethodGet, "/install/callback?token="+token, nil))
	time.Sleep(200 * time.Millisecond)
	if len(events) != 1 {
		t.Fatalf("install events should respect the on_install switch, got %d deliveries", len(events))
	}
}
