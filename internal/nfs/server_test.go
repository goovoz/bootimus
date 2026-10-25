package nfs

import (
	"os"
	"path/filepath"
	"testing"

	billy "github.com/go-git/go-billy/v5"
	gonfs "github.com/willscott/go-nfs"
)

func newTestHandler(t *testing.T) (*bootHandler, string) {
	t.Helper()
	isos := t.TempDir()
	images := t.TempDir()
	if err := os.WriteFile(filepath.Join(isos, "ubuntu.iso"), []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(images, DiskImageStaging, "abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(images, "gold"), 0o755); err != nil {
		t.Fatal(err)
	}
	return newHandler(isos, images), images
}

func mount(t *testing.T, h *bootHandler, path string) (gonfs.MountStatus, billy.Filesystem) {
	t.Helper()
	status, fs, _ := h.Mount(t.Context(), nil, gonfs.MountRequest{Dirpath: []byte(path)})
	return status, fs
}

func TestBootExportIsReadOnly(t *testing.T) {
	h, _ := newTestHandler(t)
	status, fs := mount(t, h, "/")
	if status != gonfs.MountStatusOk {
		t.Fatalf("mount status = %v", status)
	}
	if _, err := fs.Stat("ubuntu.iso"); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if _, err := fs.Create("new.iso"); err == nil {
		t.Fatal("create should be refused on the boot export")
	}
	if err := fs.MkdirAll("dir", 0o755); err == nil {
		t.Fatal("mkdir should be refused on the boot export")
	}
	if h.Change(fs) != nil {
		t.Fatal("boot export must not offer attribute changes")
	}
	if !billy.CapabilityCheck(fs, billy.ReadCapability) || billy.CapabilityCheck(fs, billy.WriteCapability) {
		t.Fatal("boot export capabilities should be read-only")
	}
}

func TestCaptureStagingIsWritable(t *testing.T) {
	h, images := newTestHandler(t)
	status, fs := mount(t, h, "/disk-images/.staging/abc123")
	if status != gonfs.MountStatusOk {
		t.Fatalf("mount status = %v", status)
	}
	if err := fs.MkdirAll("win11", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Create("win11/sda1.img")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("data"))
	f.Close()
	if _, err := os.Stat(filepath.Join(images, DiskImageStaging, "abc123", "win11", "sda1.img")); err != nil {
		t.Fatalf("file not written through: %v", err)
	}
	if h.Change(fs) == nil {
		t.Fatal("staging export should allow attribute changes")
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		t.Fatal("staging export should advertise write capability")
	}
}

func TestDiskImageRootIsReadOnlyAndUnknownPathsRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	status, fs := mount(t, h, "/disk-images")
	if status != gonfs.MountStatusOk {
		t.Fatalf("mount status = %v", status)
	}
	if _, err := fs.Stat("gold"); err != nil {
		t.Fatalf("image directory not visible: %v", err)
	}
	if _, err := fs.Create("gold/x"); err == nil {
		t.Fatal("deploy export must be read-only")
	}
	for _, path := range []string{"/disk-images/gold", "/disk-images/.staging", "/disk-images/.staging/missing", "/disk-images/.staging/../gold"} {
		if status, _ := mount(t, h, path); status == gonfs.MountStatusOk {
			t.Errorf("mount of %s should be rejected", path)
		}
	}
}

func TestDiskImageExportAbsentWhenNotConfigured(t *testing.T) {
	h := newHandler(t.TempDir(), "")
	if status, _ := mount(t, h, "/disk-images"); status == gonfs.MountStatusOk {
		t.Fatal("disk image export should not exist without a directory")
	}
}
