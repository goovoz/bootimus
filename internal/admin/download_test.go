package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestISODownloadFilename(t *testing.T) {
	cases := []struct {
		url, requested, want string
		wantErr              bool
	}{
		{url: "https://example.com/pub/ubuntu.iso", want: "ubuntu.iso"},
		{url: "https://example.com/pub/ubuntu.iso", requested: "renamed.ISO", want: "renamed.ISO"},
		{url: "https://example.com/pub/readme.txt", wantErr: true},
		{url: "https://github.com/rgl/hello-world-debian-uki/releases/download/v0.0.1/hello-world.efi", want: "hello-world.efi"},
		{url: "https://example.com/pub/ubuntu.iso", requested: "hello.efi", want: "hello.efi"},
		{url: "https://example.com/pub/ubuntu.iso", requested: "renamed.img", wantErr: true},
		{url: "https://example.com/pub/ubuntu.iso", requested: "../../etc/evil.iso", wantErr: true},
		{url: "https://example.com/pub/ubuntu.iso", requested: "sub/dir.iso", wantErr: true},
		{url: "https://example.com/pub/ubuntu.iso", requested: `..\evil.iso`, wantErr: true},
		{url: "https://example.com/pub/ubuntu.iso", requested: "..", wantErr: true},
	}
	for _, c := range cases {
		got, err := isoDownloadFilename(c.url, c.requested)
		if c.wantErr {
			if err == nil {
				t.Errorf("isoDownloadFilename(%q, %q) = %q, want error", c.url, c.requested, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("isoDownloadFilename(%q, %q) = %q, %v; want %q", c.url, c.requested, got, err, c.want)
		}
	}
}

// [bootimus-fork L1] fork-patch: iso-url-no-suffix —— 补丁行为的回归测试，
// 与实现同生共死；上游接管实现后这两个测试一并删除。
func TestResolveDownloadFilenameExplicitAndSuffixed(t *testing.T) {
	// An explicit name is used verbatim (and never probed).
	got, err := resolveDownloadFilename("https://example.com/d/abc123?v=tok", "custom.iso")
	if err != nil || got != "custom.iso" {
		t.Errorf("explicit name = %q, %v; want \"custom.iso\", nil", got, err)
	}

	// A .iso suffix needs no network round trip - example.com is unreachable
	// in the test sandbox, so a probe here would fail.
	got, err = resolveDownloadFilename("https://example.com/pub/ubuntu-24.04.iso", "")
	if err != nil || got != "ubuntu-24.04.iso" {
		t.Errorf("suffixed url = %q, %v; want \"ubuntu-24.04.iso\", nil", got, err)
	}

	// Unsafe explicit names are still rejected instead of being guessed around.
	if _, err := resolveDownloadFilename("https://example.com/x.iso", "../evil.iso"); err == nil {
		t.Error("expected path traversal in filename to be rejected")
	}
}

func TestResolveDownloadFilenameProbesOpaqueLinks(t *testing.T) {
	var sawRange bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-1" {
			sawRange = true
		}
		switch r.URL.Path {
		case "/d/iso9660":
			w.Header().Set("Content-Type", "application/x-iso9660-image")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0, 1})
		case "/d/disposition":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="ubuntu-24.04-live-server-amd64.iso"`)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0, 1})
		case "/d/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>please sign in</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Opaque URL, ISO content type -> name derived from the final path.
	got, err := resolveDownloadFilename(srv.URL+"/d/iso9660", "")
	if err != nil {
		t.Fatalf("iso9660 probe returned error: %v", err)
	}
	if got != "iso9660.iso" {
		t.Errorf("iso9660 probe = %q; want \"iso9660.iso\"", got)
	}
	if !sawRange {
		t.Error("expected the probe to send a ranged request")
	}

	// Content-Disposition wins over the path when the server provides it.
	got, err = resolveDownloadFilename(srv.URL+"/d/disposition", "")
	if err != nil {
		t.Fatalf("disposition probe returned error: %v", err)
	}
	if got != "ubuntu-24.04-live-server-amd64.iso" {
		t.Errorf("disposition probe = %q; want the Content-Disposition name", got)
	}

	// An HTML page is not an ISO and must be refused before downloading.
	if _, err := resolveDownloadFilename(srv.URL+"/d/login", ""); err == nil {
		t.Error("expected an HTML response to be rejected")
	}
}
