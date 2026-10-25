package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/diskimage"
	"bootimus/internal/models"
	"bootimus/internal/tools"
	"bootimus/internal/webhook"
)

func newDiskImagingServer(t *testing.T) (*Server, *diskimage.Manager, *models.Client) {
	t.Helper()
	env := newAccessEnv(t)
	dir := t.TempDir()
	tm := tools.NewManager(env.store, dir)
	if err := env.store.SaveBootTool(&models.BootTool{Name: "clonezilla", DisplayName: "Clonezilla Live", Enabled: true, Downloaded: true}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		config:          &Config{Storage: env.store, ServerAddr: "10.0.0.1", HTTPPort: 8080, NFSPort: 2049, NFSEnabled: true, DataDir: dir},
		toolsManager:    tm,
		webhookNotifier: webhook.New(env.store),
		logBroadcaster:  NewLogBroadcaster(),
	}
	srv.diskImages = diskimage.New(env.store, diskimage.Config{DataDir: dir, ServerAddr: "10.0.0.1", HTTPPort: 8080, NFSPort: 2049, NFSEnabled: true}, tm)
	srv.diskImages.OnEvent = srv.fireDiskImagingEvent
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:10", Name: "lab-10", Enabled: true, Static: true, ShowPublicImages: true}
	if err := env.store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	return srv, srv.diskImages, client
}

func TestMenuBootsPendingDiskJobAndCallbackCompletesIt(t *testing.T) {
	srv, mgr, client := newDiskImagingServer(t)

	before := srv.renderBootMenu(client.MACAddress, "10.0.0.5:1234", false)
	if strings.Contains(before.Script, ":diskimaging") || before.Kind != "menu" {
		t.Fatalf("menu should be ordinary before a job exists:\n%s", before.Script)
	}

	job, err := mgr.Capture(diskimage.CaptureRequest{MAC: client.MACAddress, Name: "lab-gold", Disk: "sda"})
	if err != nil {
		t.Fatal(err)
	}

	preview := srv.renderBootMenu(client.MACAddress, "10.0.0.5:1234", false)
	if preview.Kind != "imaging" || !strings.Contains(preview.DiskJob, "capture lab-gold") {
		t.Fatalf("preview = %+v", preview)
	}
	if !strings.HasPrefix(preview.Script, "#!ipxe\n\ngoto diskimaging\n") {
		t.Fatalf("menu should jump straight to the imaging section:\n%s", preview.Script)
	}
	for _, want := range []string{
		":diskimaging\n",
		"kernel http://10.0.0.1:8080/tools/clonezilla/live/vmlinuz initrd=initrd boot=live",
		"ocs_prerun=\"wget -q -O /tmp/bootimus-job.sh http://10.0.0.1:8080/diskimage/job?token=" + job.Token + "\"",
		"initrd --name initrd http://10.0.0.1:8080/tools/clonezilla/live/initrd.img",
	} {
		if !strings.Contains(preview.Script, want) {
			t.Errorf("menu missing %q:\n%s", want, preview.Script)
		}
	}

	rec := httptest.NewRecorder()
	srv.handleDiskImageJob(rec, httptest.NewRequest(http.MethodGet, "/diskimage/job?token="+job.Token, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "savedisk lab-gold") {
		t.Fatalf("job script: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handleDiskImageCallback(rec, httptest.NewRequest(http.MethodGet, "/diskimage/callback?token="+job.Token+"&status=failed&message=ocs-sr_exit_2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	failed, err := srv.config.Storage.GetDiskJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != models.DiskJobStateFailed || failed.Message != "ocs-sr_exit_2" {
		t.Fatalf("job after callback = %+v", failed)
	}

	after := srv.renderBootMenu(client.MACAddress, "10.0.0.5:1234", false)
	if strings.Contains(after.Script, ":diskimaging") {
		t.Fatal("finished job must not be served again")
	}

	rec = httptest.NewRecorder()
	srv.handleDiskImageCallback(rec, httptest.NewRequest(http.MethodGet, "/diskimage/callback?token=bogus&status=done", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bogus token: want 404, got %d", rec.Code)
	}
}

func TestMenuFallsBackWhenClonezillaMissing(t *testing.T) {
	srv, mgr, client := newDiskImagingServer(t)
	job, err := mgr.Capture(diskimage.CaptureRequest{MAC: client.MACAddress, Name: "lab-gold"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.config.Storage.DeleteBootTool("clonezilla"); err != nil {
		t.Fatal(err)
	}
	rendered := srv.renderBootMenu(client.MACAddress, "10.0.0.5:1234", false)
	if strings.Contains(rendered.Script, "goto diskimaging") {
		t.Fatal("menu must not jump to an unbootable imaging section")
	}
	if active := mgr.ActiveJob(client.MACAddress); active == nil || active.ID != job.ID {
		t.Fatal("job should stay queued until the tool is available")
	}
}
