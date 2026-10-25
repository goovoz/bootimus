package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

func newWinPEHandler(t *testing.T) (*Handler, *storage.SQLiteStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	isoDir := filepath.Join(dataDir, "isos")
	if err := os.MkdirAll(isoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &Handler{storage: store, dataDir: dataDir, isoDir: isoDir}, store, isoDir
}

func putWinPE(t *testing.T, h *Handler, filename, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/images/winpe?filename="+filename, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetWinPEWIM(rec, req)
	return rec.Code, rec.Body.String()
}

func TestSetWinPEWIMSelectsKnownWIM(t *testing.T) {
	h, store, _ := newWinPEHandler(t)
	img := &models.Image{
		Name: "Strelec", Filename: "strelec.iso", Extracted: true, Distro: "winpe", BootMethod: "kernel",
		WIMPath:  "SSTR/eng.wim",
		WIMFiles: []models.WIMFile{{Path: "SSTR/eng.wim", Size: 10}, {Path: "SSTR/rus.wim", Size: 11}},
	}
	if err := store.CreateImage(img); err != nil {
		t.Fatal(err)
	}

	code, body := putWinPE(t, h, "strelec.iso", `{"wim_path":"SSTR/rus.wim","wim_index":2}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	saved, err := store.GetImage("strelec.iso")
	if err != nil {
		t.Fatal(err)
	}
	if saved.WIMPath != "SSTR/rus.wim" || saved.WIMIndex != 2 {
		t.Fatalf("saved WIM = %q index %d", saved.WIMPath, saved.WIMIndex)
	}
	if len(saved.WIMFiles) != 2 {
		t.Fatalf("WIM list lost on save: %+v", saved.WIMFiles)
	}
}

func TestSetWinPEWIMRejectsUnknownAndNonWindows(t *testing.T) {
	h, store, isoDir := newWinPEHandler(t)
	if err := store.CreateImage(&models.Image{Name: "Strelec", Filename: "strelec.iso", Extracted: true, Distro: "winpe", BootMethod: "kernel"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImage(&models.Image{Name: "Ubuntu", Filename: "ubuntu.iso", Extracted: true, Distro: "ubuntu", BootMethod: "kernel"}); err != nil {
		t.Fatal(err)
	}

	if code, _ := putWinPE(t, h, "strelec.iso", `{"wim_path":"missing/x.wim"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown WIM: status %d, want 400", code)
	}
	if code, _ := putWinPE(t, h, "strelec.iso", `{"wim_path":"../../etc/passwd.wim"}`); code != http.StatusBadRequest {
		t.Fatalf("traversal: status %d, want 400", code)
	}
	if code, _ := putWinPE(t, h, "ubuntu.iso", `{"wim_path":"boot.wim"}`); code != http.StatusBadRequest {
		t.Fatalf("non-Windows image: status %d, want 400", code)
	}
	if code, _ := putWinPE(t, h, "nope.iso", `{"wim_path":"boot.wim"}`); code != http.StatusNotFound {
		t.Fatalf("missing image: status %d, want 404", code)
	}

	wimDir := filepath.Join(isoDir, "strelec", "iso", "extra")
	if err := os.MkdirAll(wimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wimDir, "late.wim"), []byte("wim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := putWinPE(t, h, "strelec.iso", `{"wim_path":"extra/late.wim"}`); code != http.StatusOK {
		t.Fatalf("WIM present on disk but not listed: status %d: %s", code, body)
	}
}
