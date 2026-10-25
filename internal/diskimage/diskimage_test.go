package diskimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bootimus/internal/models"
	"bootimus/internal/storage"
	"bootimus/internal/tools"
)

type fakeTools struct{ enabled []tools.EnabledTool }

func (f fakeTools) GetEnabledTools(string) []tools.EnabledTool { return f.enabled }

func clonezillaTool() tools.EnabledTool {
	return tools.EnabledTool{
		Name:       ToolName,
		KernelURL:  "http://10.0.0.1:8080/tools/clonezilla/live/vmlinuz",
		InitrdURL:  "http://10.0.0.1:8080/tools/clonezilla/live/initrd.img",
		BootParams: "boot=live config components union=overlay username=user noswap noeject vga=788 fetch=http://10.0.0.1:8080/tools/clonezilla/live/filesystem.squashfs",
		BootMethod: "kernel",
	}
}

func newManager(t *testing.T, nfsEnabled bool, enabled ...tools.EnabledTool) (*Manager, *storage.SQLiteStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.CreateClient(&models.Client{MACAddress: "aa:bb:cc:dd:ee:01", Name: "lab-01", Enabled: true, Static: true}); err != nil {
		t.Fatal(err)
	}
	m := New(store, Config{DataDir: dir, ServerAddr: "10.0.0.1", HTTPPort: 8080, NFSPort: 2049, NFSEnabled: nfsEnabled}, fakeTools{enabled})
	return m, store, dir
}

func TestValidNameAndDisk(t *testing.T) {
	for _, ok := range []string{"win11-lab", "a", "img.2026_10", "0abc"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "Has Space", "UPPER", "../x", strings.Repeat("a", 65)} {
		if err := ValidName(bad); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", bad)
		}
	}
	cases := map[string]string{"": "sda", "/dev/nvme0n1": "nvme0n1", "auto": "auto", " sdb ": "sdb"}
	for in, want := range cases {
		got, err := NormaliseDisk(in)
		if err != nil || got != want {
			t.Errorf("NormaliseDisk(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormaliseDisk("sda; rm -rf /"); err == nil {
		t.Error("NormaliseDisk accepted shell metacharacters")
	}
}

func TestStatusReportsProblems(t *testing.T) {
	m, _, _ := newManager(t, false)
	st := m.Status()
	if st.Ready || len(st.Problems) != 2 {
		t.Fatalf("status = %+v, want two problems", st)
	}
	if _, err := m.Capture(CaptureRequest{MAC: "aa:bb:cc:dd:ee:01", Name: "x"}); err == nil {
		t.Fatal("capture should be refused when not ready")
	}
	m2, _, _ := newManager(t, true, clonezillaTool())
	if st := m2.Status(); !st.Ready {
		t.Fatalf("status = %+v, want ready", st)
	}
}

func TestCaptureBootScriptAndCompletion(t *testing.T) {
	m, store, dir := newManager(t, true, clonezillaTool())
	var events []string
	m.OnEvent = func(event string, _ *models.DiskJob, _ *models.DiskImage) { events = append(events, event) }

	job, err := m.Capture(CaptureRequest{MAC: "AA-BB-CC-DD-EE-01", Name: "win11-lab", Description: "Lab build", Disk: "/dev/sda"})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != models.DiskJobStatePending || job.ClientMAC != "aa:bb:cc:dd:ee:01" || job.Disk != "sda" || len(job.Token) != 32 {
		t.Fatalf("job = %+v", job)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, StagingDir, job.Token)); err != nil {
		t.Fatalf("staging directory missing: %v", err)
	}
	if _, err := m.Capture(CaptureRequest{MAC: "aa:bb:cc:dd:ee:01", Name: "other"}); err != ErrJobActive {
		t.Fatalf("second capture err = %v, want ErrJobActive", err)
	}
	if active := m.ActiveJob("aa:bb:cc:dd:ee:01"); active == nil || active.ID != job.ID {
		t.Fatalf("ActiveJob = %+v", active)
	}

	boot, err := m.Boot(job, []tools.EnabledTool{clonezillaTool()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`fetch=http://10.0.0.1:8080/tools/clonezilla/live/filesystem.squashfs`,
		`ocs_prerun="wget -q -O /tmp/bootimus-job.sh http://10.0.0.1:8080/diskimage/job?token=` + job.Token + `"`,
		`ocs_live_run="bash /tmp/bootimus-job.sh"`,
		`ocs_live_batch="yes"`,
	} {
		if !strings.Contains(boot.Params, want) {
			t.Errorf("boot params missing %q:\n%s", want, boot.Params)
		}
	}
	if _, err := m.Boot(job, nil); err != ErrToolUnavailable {
		t.Fatalf("Boot without tool err = %v", err)
	}

	script, err := m.JobScript(job.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"mount -t nfs -o nolock,vers=3,tcp,port=2049,mountport=2049 10.0.0.1:/disk-images/.staging/" + job.Token + " /home/partimag",
		`ocs-sr -q2 -j2 -z1p -i 4096 -sc -p true savedisk win11-lab "$DISK"`,
		"CALLBACK='http://10.0.0.1:8080/diskimage/callback?token=" + job.Token + "'",
		"DISK='sda'",
		"poweroff",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	running, _ := store.GetDiskJob(job.ID)
	if running.State != models.DiskJobStateRunning || running.StartedAt == nil {
		t.Fatalf("job after script fetch = %+v", running)
	}
	if _, err := m.JobScript("nope"); err != ErrUnknownToken {
		t.Fatalf("unknown token err = %v", err)
	}

	captured := filepath.Join(dir, DirName, StagingDir, job.Token, "win11-lab")
	if err := os.MkdirAll(captured, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captured, "sda1.ntfs-ptcl-img.gz.aa"), make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Report(job.Token, "done", "sda", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, "win11-lab", "sda1.ntfs-ptcl-img.gz.aa")); err != nil {
		t.Fatalf("image not moved into place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, StagingDir, job.Token)); !os.IsNotExist(err) {
		t.Fatalf("staging directory should be removed, stat err = %v", err)
	}
	image, _ := store.GetDiskImageByName("win11-lab")
	if image.State != models.DiskImageStateReady || image.SizeBytes != 1234 || image.SourceClientName != "lab-01" {
		t.Fatalf("image = %+v", image)
	}
	done, _ := store.GetDiskJob(job.ID)
	if done.State != models.DiskJobStateCompleted || done.FinishedAt == nil {
		t.Fatalf("job = %+v", done)
	}
	if len(events) != 1 || events[0] != EventCaptureCompleted {
		t.Fatalf("events = %v", events)
	}
	if err := m.Report(job.Token, "done", "", ""); err != ErrJobNotActive {
		t.Fatalf("second report err = %v", err)
	}
	if m.ActiveJob("aa:bb:cc:dd:ee:01") != nil {
		t.Fatal("no job should be active after completion")
	}
}

func TestCaptureFailureAndCancel(t *testing.T) {
	m, store, dir := newManager(t, true, clonezillaTool())
	job, err := m.Capture(CaptureRequest{MAC: "aa:bb:cc:dd:ee:01", Name: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Report(job.Token, "failed", "", "ocs-sr_exit_1"); err != nil {
		t.Fatal(err)
	}
	image, _ := store.GetDiskImage(job.DiskImageID)
	if image.State != models.DiskImageStateFailed {
		t.Fatalf("image state = %q", image.State)
	}
	failed, _ := store.GetDiskJob(job.ID)
	if failed.State != models.DiskJobStateFailed || failed.Message != "ocs-sr_exit_1" {
		t.Fatalf("job = %+v", failed)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, StagingDir, job.Token)); !os.IsNotExist(err) {
		t.Fatal("staging directory should be removed after failure")
	}

	job2, err := m.Capture(CaptureRequest{MAC: "aa:bb:cc:dd:ee:01", Name: "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(job2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDiskImageByName("cancelled"); err == nil {
		t.Fatal("unfinished image should be deleted on cancel")
	}
	if err := m.Cancel(job2.ID); err != ErrJobNotActive {
		t.Fatalf("second cancel err = %v", err)
	}
}

func TestDeployRequiresReadyImageAndMountsRootReadOnly(t *testing.T) {
	m, store, dir := newManager(t, true, clonezillaTool())
	image := &models.DiskImage{Name: "gold", State: models.DiskImageStateCapturing}
	if err := store.CreateDiskImage(image); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Deploy(DeployRequest{MAC: "aa:bb:cc:dd:ee:01", ImageID: image.ID}); err != ErrImageNotReady {
		t.Fatalf("deploy err = %v, want ErrImageNotReady", err)
	}
	image.State = models.DiskImageStateReady
	if err := store.UpdateDiskImage(image); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DirName, "gold"), 0o755); err != nil {
		t.Fatal(err)
	}
	job, err := m.Deploy(DeployRequest{MAC: "aa:bb:cc:dd:ee:01", ImageID: image.ID, Disk: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	script, err := m.JobScript(job.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"10.0.0.1:/disk-images /home/partimag",
		`ocs-sr -g auto -e1 auto -e2 -r -j2 -scr -p true restoredisk gold "$DISK"`,
		"DISK='auto'",
		"lsblk -dno NAME,TYPE,RM,RO",
		"reboot",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("deploy script missing %q:\n%s", want, script)
		}
	}
	if err := m.Report(job.Token, "running", "nvme0n1", "started"); err != nil {
		t.Fatal(err)
	}
	if err := m.Report(job.Token, "done", "nvme0n1", ""); err != nil {
		t.Fatal(err)
	}
	done, _ := store.GetDiskJob(job.ID)
	if done.State != models.DiskJobStateCompleted || done.Disk != "nvme0n1" {
		t.Fatalf("job = %+v", done)
	}
	if err := m.DeleteImage(image.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, "gold")); !os.IsNotExist(err) {
		t.Fatal("image directory should be removed")
	}
}

func TestDeleteImageRefusedWhileJobActive(t *testing.T) {
	m, store, _ := newManager(t, true, clonezillaTool())
	image := &models.DiskImage{Name: "busy", State: models.DiskImageStateReady}
	if err := store.CreateDiskImage(image); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Deploy(DeployRequest{MAC: "aa:bb:cc:dd:ee:01", ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteImage(image.ID); err != storage.ErrDiskImageInUse {
		t.Fatalf("delete err = %v, want ErrDiskImageInUse", err)
	}
}
