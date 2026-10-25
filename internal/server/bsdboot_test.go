package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

type bsdReadFrom struct {
	tftpCapture
	ip string
}

func (b *bsdReadFrom) RemoteAddr() net.UDPAddr {
	if b.ip == "" {
		b.ip = "192.0.2.50"
	}
	return net.UDPAddr{IP: net.ParseIP(b.ip), Port: 4000}
}

func newBSDTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	isoDir := t.TempDir()
	bsdDir := filepath.Join(isoDir, "lab", "install77", "bsd")
	if err := os.MkdirAll(filepath.Join(bsdDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"pxeboot": "pxe", "BOOTX64.EFI": "efi", "bsd.rd": "ramdisk"} {
		if err := os.WriteFile(filepath.Join(bsdDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateImage(&models.Image{Name: "OpenBSD 7.7", Filename: "lab/install77.iso", Enabled: true, Extracted: true, Distro: "openbsd", BootMethod: "kernel", BootParams: "stty com0 115200;set tty com0"}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		config:        &Config{Storage: store, ISODir: isoDir, ServerAddr: "10.0.0.1", HTTPPort: 8080, TFTPPort: 69, DataDir: t.TempDir()},
		bsdSelections: newBSDSelections(),
		bootLogDedup:  map[string]time.Time{},
	}
	return srv, isoDir
}

func TestBSDTFTPServesPrefixedLoader(t *testing.T) {
	srv, _ := newBSDTestServer(t)
	capture := &bsdReadFrom{}
	if err := srv.serveTFTPFile("bsd/lab/install77/pxeboot", capture); err != nil {
		t.Fatalf("serveTFTPFile: %v", err)
	}
	if capture.String() != "pxe" || capture.size != 3 {
		t.Fatalf("payload = %q size %d", capture.String(), capture.size)
	}
}

func TestBSDTFTPRejectsTraversalAndUnknownCache(t *testing.T) {
	srv, _ := newBSDTestServer(t)
	for _, path := range []string{"bsd/lab/install77/../../../etc/passwd", "bsd/nothere/pxeboot", "bsd/lab/install77"} {
		if err := srv.serveTFTPFile(path, &bsdReadFrom{}); err == nil {
			t.Errorf("%s should be refused", path)
		}
	}
}

func TestBSDTFTPRootFilesFollowClientSelection(t *testing.T) {
	srv, _ := newBSDTestServer(t)
	capture := &bsdReadFrom{}
	if err := srv.serveTFTPFile("etc/boot.conf", capture); err == nil {
		t.Fatal("etc/boot.conf must not resolve before the client selects a BSD image")
	}

	req := httptest.NewRequest(http.MethodGet, "/bsd/lab/install77/boot.ipxe?mac=aa-bb-cc-dd-ee-ff&platform=pcbios", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	rec := httptest.NewRecorder()
	srv.handleBSDBoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hook status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chain tftp://10.0.0.1/bsd/lab/install77/pxeboot || goto bsdfail\n") {
		t.Fatalf("hook script = %q", rec.Body.String())
	}

	capture = &bsdReadFrom{}
	if err := srv.serveTFTPFile("etc/boot.conf", capture); err != nil {
		t.Fatalf("etc/boot.conf after selection: %v", err)
	}
	want := "set image bsd/lab/install77/bsd.rd\nstty com0 115200\nset tty com0\n"
	if capture.String() != want {
		t.Fatalf("boot.conf = %q, want %q", capture.String(), want)
	}

	capture = &bsdReadFrom{}
	if err := srv.serveTFTPFile("bsd", capture); err != nil || capture.String() != "ramdisk" {
		t.Fatalf("bsd -> bsd.rd: err=%v payload=%q", err, capture.String())
	}

	capture = &bsdReadFrom{}
	if err := srv.serveTFTPFile("etc/random.seed", capture); err != nil || capture.size != 512 {
		t.Fatalf("random seed: err=%v size=%d", err, capture.size)
	}

	other := &bsdReadFrom{}
	srv.bsdSelections.now = func() time.Time { return time.Now().Add(time.Hour) }
	if err := srv.serveTFTPFile("etc/boot.conf", other); err == nil {
		t.Fatal("selection should expire")
	}
}

func TestBSDHookReportsMissingFirmwareLoader(t *testing.T) {
	srv, isoDir := newBSDTestServer(t)
	if err := os.Remove(filepath.Join(isoDir, "lab", "install77", "bsd", "BOOTX64.EFI")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/bsd/lab/install77/boot.ipxe?mac=aa:bb:cc:dd:ee:ff&platform=efi&buildarch=x86_64", nil)
	req.RemoteAddr = "192.0.2.51:4321"
	rec := httptest.NewRecorder()
	srv.handleBSDBoot(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "OpenBSD UEFI loader BOOTX64.EFI was not found") || strings.Contains(body, "chain tftp") {
		t.Fatalf("missing loader script = %q", body)
	}
	if _, ok := srv.bsdSelections.lookup("192.0.2.51"); ok {
		t.Fatal("a failed hook must not record a selection")
	}
}

func TestBSDHookRejectsBadPaths(t *testing.T) {
	srv, _ := newBSDTestServer(t)
	for _, path := range []string{"/bsd/../etc/boot.ipxe", "/bsd/lab/install77/pxeboot", "/bsd/missing/boot.ipxe"} {
		rec := httptest.NewRecorder()
		srv.handleBSDBoot(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s returned 200", path)
		}
	}
}

func TestBSDTFTPHostIncludesNonDefaultPort(t *testing.T) {
	srv, _ := newBSDTestServer(t)
	srv.config.TFTPPort = 6969
	if got := srv.bsdTFTPHost(); got != "10.0.0.1:6969" {
		t.Fatalf("host = %q", got)
	}
}
