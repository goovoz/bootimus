package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDownloadTestServer(t *testing.T, body []byte, sumsPath string) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(body)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/test.iso"):
			w.Write(body)
		case sumsPath != "" && r.URL.Path == sumsPath:
			w.Write([]byte(hex.EncodeToString(sum[:]) + "  test.iso\n"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func waitForDownload(t *testing.T, filename string) *DownloadProgress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := downloadMgr.Get(filename); p != nil && p.Status != "downloading" {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download did not finish")
	return nil
}

func TestDownloadRemovesFileOnChecksumMismatch(t *testing.T) {
	body := []byte("iso payload")
	srv := newDownloadTestServer(t, body, "")
	defer srv.Close()
	h := &Handler{isoDir: t.TempDir()}
	dest := filepath.Join(h.isoDir, "test.iso")
	downloadMgr.TryBegin(srv.URL+"/pub/test.iso", "test.iso")
	h.downloadISO(srv.URL+"/pub/test.iso", "test.iso", dest, downloadOptions{SHA256: strings.Repeat("0", 64)})
	p := waitForDownload(t, "test.iso")
	if p.Status != "error" || p.Verification != "mismatch" || !strings.Contains(p.Error, "mismatch") {
		t.Fatalf("progress = %+v", p)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("mismatched download was not removed")
	}
}

func TestDownloadVerifiesAgainstDiscoveredChecksumFile(t *testing.T) {
	body := []byte("another iso payload")
	srv := newDownloadTestServer(t, body, "/pub/SHA256SUMS")
	defer srv.Close()
	h := &Handler{isoDir: t.TempDir()}
	dest := filepath.Join(h.isoDir, "test.iso")
	downloadMgr.TryBegin(srv.URL+"/pub/test.iso", "test.iso")
	h.downloadISO(srv.URL+"/pub/test.iso", "test.iso", dest, downloadOptions{CatalogueID: "demo"})
	p := waitForDownload(t, "test.iso")
	if p.Status != "completed" || p.Verification != "verified" {
		t.Fatalf("progress = %+v", p)
	}
	sum := sha256.Sum256(body)
	if p.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum = %q", p.Checksum)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != string(body) {
		t.Fatalf("file content wrong: %v", err)
	}
}

func TestDownloadWithoutChecksumIsUnverified(t *testing.T) {
	srv := newDownloadTestServer(t, []byte("plain"), "")
	defer srv.Close()
	h := &Handler{isoDir: t.TempDir()}
	dest := filepath.Join(h.isoDir, "test.iso")
	downloadMgr.TryBegin(srv.URL+"/plain/test.iso", "test.iso")
	h.downloadISO(srv.URL+"/plain/test.iso", "test.iso", dest, downloadOptions{})
	p := waitForDownload(t, "test.iso")
	if p.Status != "completed" || p.Verification != "unverified" {
		t.Fatalf("progress = %+v", p)
	}
}
