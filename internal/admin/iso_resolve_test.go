package admin

// [bootimus-fork L1] fork-patch: iso-catalog-resolve 的回归测试

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionPatternOnlyMatchesSameFamily(t *testing.T) {
	re := versionPattern("debian-13.5.0-amd64-netinst.iso")
	ok := []string{
		"debian-13.7.0-amd64-netinst.iso",
		"debian-13.5.0-amd64-netinst.iso",
	}
	bad := []string{
		"debian-edu-13.7.0-amd64-netinst.iso",
		"debian-13.7.0-amd64-DVD-1.iso",
		"debian-mac-13.7.0-amd64-netinst.iso",
		"debian-13.7.0-arm64-netinst.iso",
	}
	for _, n := range ok {
		if !re.MatchString(n) {
			t.Errorf("expected %q to match", n)
		}
	}
	for _, n := range bad {
		if re.MatchString(n) {
			t.Errorf("expected %q NOT to match", n)
		}
	}
}

func TestPickNewestISO(t *testing.T) {
	names := []string{
		"kali-linux-2024.4-live-amd64.iso",
		"kali-linux-2026.3-live-amd64.iso",
		"kali-linux-2026.2-live-amd64.iso",
		"kali-linux-2026.3-installer-amd64.iso",
	}
	re := versionPattern("kali-linux-2024.4-live-amd64.iso")
	if got := pickNewestISO(names, re); got != "kali-linux-2026.3-live-amd64.iso" {
		t.Fatalf("pickNewestISO = %q", got)
	}
}

// fakeMirror 起一个带目录列表的假镜像站；old 文件故意 404。
func fakeMirror(t *testing.T) *httptest.Server {
	t.Helper()
	listing := `<html><body>
	<a href="debian-13.5.0-amd64-netinst.iso">debian-13.5.0-amd64-netinst.iso</a>
	<a href="debian-edu-13.7.0-amd64-netinst.iso">edu</a>
	<a href="debian-13.7.0-amd64-netinst.iso">debian-13.7.0-amd64-netinst.iso</a>
	<a href="debian-13.10.0-amd64-netinst.iso">debian-13.10.0-amd64-netinst.iso</a>
	<a href="debian-13.7.0-amd64-netinst.iso.sha256">checksum</a>
	</body></html>`
	mux := http.NewServeMux()
	mux.HandleFunc("/iso-cd/", func(w http.ResponseWriter, r *http.Request) {
		// Go 的 mux 把 "/iso-cd/" 当成子树，这里只让真正的目录路径返回列表
		if r.URL.Path != "/iso-cd/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(listing))
	})
	serveFile := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "4")
			if r.Method == http.MethodHead {
				return
			}
			w.Write([]byte("iso!"))
		}
	}
	mux.HandleFunc("/iso-cd/debian-13.7.0-amd64-netinst.iso", serveFile("13.7.0"))
	mux.HandleFunc("/iso-cd/debian-13.10.0-amd64-netinst.iso", serveFile("13.10"))
	return httptest.NewServer(mux)
}

func TestResolveLatestISOURLPicksNewest(t *testing.T) {
	srv := fakeMirror(t)
	defer srv.Close()

	if _, _, err := resolveLatestISOURL(srv.URL + "/iso-cd/missing-1.0-amd64-netinst.iso"); err == nil {
		t.Fatal("expected an error when nothing matches")
	}

	got, changed, err := resolveLatestISOURL(srv.URL + "/iso-cd/debian-13.5.0-amd64-netinst.iso")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !changed {
		t.Fatal("expected the URL to be rewritten")
	}
	// 13.10.0 比 13.7.0 新：必须按数字比较，不能按字符串
	if !strings.HasSuffix(got, "/debian-13.10.0-amd64-netinst.iso") {
		t.Fatalf("resolved to %q, want the 13.10.0 build", got)
	}
}

func TestResolveLatestISOURLKeepsWorkingURL(t *testing.T) {
	srv := fakeMirror(t)
	defer srv.Close()

	raw := srv.URL + "/iso-cd/debian-13.7.0-amd64-netinst.iso"
	got, changed, err := resolveLatestISOURL(raw)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if changed || got != raw {
		t.Fatalf("working URL should be left alone, got %q (changed=%v)", got, changed)
	}
}

func TestSplitURL(t *testing.T) {
	dir, name, err := splitURL("https://example.com/a/b/debian-1.0.iso")
	if err != nil || dir != "https://example.com/a/b" || name != "debian-1.0.iso" {
		t.Fatalf("splitURL = %q, %q, %v", dir, name, err)
	}
	if _, _, err := splitURL("ftp://example.com/x.iso"); err == nil {
		t.Fatal("ftp should be rejected")
	}
}

func TestNumericOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"x-13.10.0.iso", "x-13.7.0.iso", true},
		{"x-13.7.0.iso", "x-13.10.0.iso", false},
		{"x-2026.3.iso", "x-2024.4.iso", true},
	}
	for _, c := range cases {
		if got := newerTuple(numericTuples(c.a), numericTuples(c.b)); got != c.want {
			t.Errorf("newerTuple(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
