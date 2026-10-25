package catalogue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bootimus/internal/housekeeping"
	"bootimus/internal/models"
	"bootimus/internal/profiles"
	"bootimus/internal/tools"
)

func TestParseChecksumsHandlesGNUAndBSDFormats(t *testing.T) {
	input := strings.Join([]string{
		"# comment",
		"e7b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90  ubuntu-24.04.4-desktop-amd64.iso",
		"f7b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f91 *ubuntu-24.04.4-live-server-amd64.iso",
		"SHA256 (debian-13.5.0-amd64-netinst.iso) = A7B2C3D4E5F60718293A4B5C6D7E8F90A1B2C3D4E5F60718293A4B5C6D7E8F92",
		"not a checksum line",
		"abcd  short.iso",
	}, "\n")
	sums := ParseChecksums(strings.NewReader(input))
	if len(sums) != 3 {
		t.Fatalf("parsed %d entries, want 3: %v", len(sums), sums)
	}
	if sums["ubuntu-24.04.4-live-server-amd64.iso"] != "f7b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f91" {
		t.Fatalf("binary-mode entry not parsed: %v", sums)
	}
	if sums["debian-13.5.0-amd64-netinst.iso"] != "a7b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f92" {
		t.Fatalf("BSD entry not parsed or not lower-cased: %v", sums)
	}
	if _, ok := LookupChecksum(sums, "UBUNTU-24.04.4-DESKTOP-AMD64.ISO"); !ok {
		t.Fatal("lookup should be case-insensitive")
	}
}

func TestNormaliseSHA256RejectsBadInput(t *testing.T) {
	if _, err := NormaliseSHA256("abc"); err == nil {
		t.Fatal("short checksum accepted")
	}
	if _, err := NormaliseSHA256(strings.Repeat("g", 64)); err == nil {
		t.Fatal("non-hex checksum accepted")
	}
	got, err := NormaliseSHA256("  " + strings.Repeat("AB", 32) + "\n")
	if err != nil || got != strings.Repeat("ab", 32) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestChecksumURLFor(t *testing.T) {
	iso := "https://releases.ubuntu.com/24.04.4/ubuntu-24.04.4-desktop-amd64.iso?x=1"
	cases := map[string]string{
		"":                             "",
		"SHA256SUMS":                   "https://releases.ubuntu.com/24.04.4/SHA256SUMS",
		"/other/SHA256SUMS":            "https://releases.ubuntu.com/other/SHA256SUMS",
		"https://example.org/sums.txt": "https://example.org/sums.txt",
	}
	for in, want := range cases {
		if got := ChecksumURLFor(iso, in); got != want {
			t.Errorf("ChecksumURLFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscoverChecksumTriesCandidatesInOrder(t *testing.T) {
	sum := strings.Repeat("1a", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl/sha256sum.txt":
			w.Write([]byte(sum + "  thing-1.0.iso\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, source, err := DiscoverChecksum(context.Background(), srv.URL+"/dl/thing-1.0.iso", "thing-1.0.iso")
	if err != nil || got != sum {
		t.Fatalf("got %q from %q, err %v", got, source, err)
	}
	if !strings.HasSuffix(source, "/dl/sha256sum.txt") {
		t.Fatalf("unexpected source %q", source)
	}
	if _, _, err := DiscoverChecksum(context.Background(), srv.URL+"/dl/other.iso", "other.iso"); !errors.Is(err, ErrChecksumNotFound) {
		t.Fatalf("expected ErrChecksumNotFound, got %v", err)
	}
}

func TestVersionHelpers(t *testing.T) {
	if VersionKey("ubuntu-24.04.3-desktop-amd64.iso") != VersionKey("ubuntu-24.04.4-desktop-amd64.iso") {
		t.Fatal("point releases should share a key")
	}
	if VersionKey("ubuntu-24.04.3-desktop-amd64.iso") == VersionKey("ubuntu-26.04-desktop-amd64.iso") {
		t.Fatal("different version shapes should not share a key")
	}
	if !Newer(VersionOf("debian-13.5.0-amd64-netinst.iso"), VersionOf("debian-13.4.0-amd64-netinst.iso")) {
		t.Fatal("13.5.0 should be newer than 13.4.0")
	}
	if Newer(VersionOf("debian-13.4.0-amd64-netinst.iso"), VersionOf("debian-13.5.0-amd64-netinst.iso")) {
		t.Fatal("13.4.0 should not be newer than 13.5.0")
	}
	if VersionLabel("linuxmint-22.3-cinnamon-64bit.iso") != "22.3" {
		t.Fatalf("label = %q", VersionLabel("linuxmint-22.3-cinnamon-64bit.iso"))
	}
}

func syntheticCatalogue() *profiles.ISOCatalogue {
	return &profiles.ISOCatalogue{Version: "test", Distros: []profiles.ISOEntry{
		{ID: "ubuntu", Name: "Ubuntu", Mirrors: []profiles.ISOMirror{{Region: "Global", Base: "https://releases.ubuntu.com/"}}, Releases: []profiles.ISORelease{
			{Label: "24.04 LTS Desktop (amd64)", Path: "/24.04.4/ubuntu-24.04.4-desktop-amd64.iso"},
			{Label: "26.04 LTS Desktop (amd64)", Path: "/26.04/ubuntu-26.04-desktop-amd64.iso"},
		}},
		{ID: "debian", Name: "Debian", Mirrors: []profiles.ISOMirror{{Region: "Global", Base: "https://cdimage.debian.org/debian-cd/current/amd64"}}, Releases: []profiles.ISORelease{
			{Label: "13 (Trixie) Netinst (amd64)", Path: "/iso-cd/debian-13.5.0-amd64-netinst.iso"},
		}},
	}}
}

func TestFindUpdateByCatalogueRelease(t *testing.T) {
	img := &models.Image{Filename: "ubuntu-24.04.2-desktop-amd64.iso", CatalogueID: "ubuntu", CatalogueRelease: "24.04 LTS Desktop (amd64)"}
	update, ok := FindUpdate(img, syntheticCatalogue())
	if !ok {
		t.Fatal("expected an update")
	}
	if update.Filename != "ubuntu-24.04.4-desktop-amd64.iso" || update.Version != "24.04.4" {
		t.Fatalf("update = %+v", update)
	}
	if update.URL != "https://releases.ubuntu.com/24.04.4/ubuntu-24.04.4-desktop-amd64.iso" {
		t.Fatalf("url = %q", update.URL)
	}
}

func TestFindUpdateByFilenameShapeForUntrackedImages(t *testing.T) {
	img := &models.Image{Filename: "debian-13.4.0-amd64-netinst.iso"}
	update, ok := FindUpdate(img, syntheticCatalogue())
	if !ok || update.Version != "13.5.0" {
		t.Fatalf("update = %+v, ok %v", update, ok)
	}
	current := &models.Image{Filename: "ubuntu-24.04.4-desktop-amd64.iso"}
	if _, ok := FindUpdate(current, syntheticCatalogue()); ok {
		t.Fatal("current release must not be flagged, and 26.04 is a different series")
	}
	unknown := &models.Image{Filename: "custom-rescue.iso"}
	if _, ok := FindUpdate(unknown, syntheticCatalogue()); ok {
		t.Fatal("unknown images must not be flagged")
	}
}

func TestToolUpdate(t *testing.T) {
	def := &tools.ToolDefinition{Name: "gparted", DownloadURL: "https://example.org/gparted-2.zip"}
	cases := []struct {
		tool *models.BootTool
		want bool
	}{
		{&models.BootTool{Name: "gparted", Downloaded: true, DownloadedURL: "https://example.org/gparted-1.zip"}, true},
		{&models.BootTool{Name: "gparted", Downloaded: true, DownloadedURL: "https://example.org/gparted-2.zip"}, false},
		{&models.BootTool{Name: "gparted", Downloaded: true}, false},
		{&models.BootTool{Name: "gparted", Downloaded: false, DownloadedURL: "https://example.org/gparted-1.zip"}, false},
		{&models.BootTool{Name: "gparted", Custom: true, Downloaded: true, DownloadedURL: "x"}, false},
	}
	for i, c := range cases {
		if _, got := ToolUpdate(c.tool, def); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}

type fakeStore struct {
	images []*models.Image
	tools  []*models.BootTool
}

func (f *fakeStore) ListImages() ([]*models.Image, error) { return f.images, nil }
func (f *fakeStore) UpdateImage(filename string, image *models.Image) error {
	for i := range f.images {
		if f.images[i].Filename == filename {
			f.images[i] = image
		}
	}
	return nil
}
func (f *fakeStore) ListBootTools() ([]*models.BootTool, error) { return f.tools, nil }
func (f *fakeStore) SaveBootTool(tool *models.BootTool) error   { return nil }

func TestCheckerFlagsAndClearsUpdates(t *testing.T) {
	store := &fakeStore{images: []*models.Image{
		{Filename: "ubuntu-24.04.2-desktop-amd64.iso", Name: "Ubuntu", CatalogueID: "ubuntu", CatalogueRelease: "24.04 LTS Desktop (amd64)"},
		{Filename: "stale.iso", Name: "Stale", UpdateAvailable: true, UpdateURL: "gone"},
	}}
	checker := NewChecker(store, true)
	checker.loader = func(context.Context) (*profiles.ISOCatalogue, bool) { return syntheticCatalogue(), false }

	report, err := checker.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Images) != 1 || report.Images[0].Version != "24.04.4" {
		t.Fatalf("report = %+v", report.Images)
	}
	if !store.images[0].UpdateAvailable || store.images[0].UpdateFilename != "ubuntu-24.04.4-desktop-amd64.iso" {
		t.Fatalf("image not flagged: %+v", store.images[0])
	}
	if store.images[1].UpdateAvailable || store.images[1].UpdateURL != "" {
		t.Fatalf("stale flag not cleared: %+v", store.images[1])
	}
	if checker.LastReport() != report {
		t.Fatal("last report not retained")
	}
}

func TestCheckerTaskSkipsWhenDisabled(t *testing.T) {
	checker := NewChecker(&fakeStore{}, true)
	checker.loader = func(context.Context) (*profiles.ISOCatalogue, bool) { return syntheticCatalogue(), false }
	if _, err := checker.Run(context.Background(), &models.HousekeepingSettings{CatalogueCheckOff: true}); !errors.Is(err, housekeeping.ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	n, err := checker.Run(context.Background(), &models.HousekeepingSettings{})
	if err != nil || n != 0 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if checker.Name() != TaskName {
		t.Fatalf("name = %q", checker.Name())
	}
}

func TestVerifierHashesFileAndReportsMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.iso")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	const helloSum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	v := NewVerifier()
	if !v.Begin("x.iso", helloSum) {
		t.Fatal("begin refused")
	}
	var gotActual string
	var gotVerified bool
	v.Run("x.iso", path, helloSum, func(actual string, verified bool) { gotActual, gotVerified = actual, verified })
	if gotActual != helloSum || !gotVerified {
		t.Fatalf("actual %q verified %v", gotActual, gotVerified)
	}
	p := v.Get("x.iso")
	if p == nil || p.Status != "completed" || !p.Verified || p.Percent != 100 {
		t.Fatalf("progress = %+v", p)
	}
	v.Begin("x.iso", strings.Repeat("0", 64))
	v.Run("x.iso", path, strings.Repeat("0", 64), nil)
	if p := v.Get("x.iso"); p.Verified || p.Error == "" {
		t.Fatalf("mismatch not reported: %+v", p)
	}
}
