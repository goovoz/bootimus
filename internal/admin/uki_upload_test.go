package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"bootimus/internal/storage"
	"bootimus/internal/uki/ukitest"
)

func uploadImage(t *testing.T, h *Handler, filename string, content []byte) int {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/images/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.UploadImage(rec, req)
	return rec.Code
}

func TestUploadUKI(t *testing.T) {
	dataDir := t.TempDir()
	isoDir := t.TempDir()
	store, err := storage.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{storage: store, isoDir: isoDir}

	src := filepath.Join(t.TempDir(), "fedora-uki.efi")
	ukitest.WriteUKI(t, src, "audit=0")
	ukiBytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	if code := uploadImage(t, h, "fedora-uki.efi", ukiBytes); code != http.StatusCreated {
		t.Fatalf("UKI upload returned %d, want 201", code)
	}
	img, err := store.GetImage("fedora-uki.efi")
	if err != nil {
		t.Fatal(err)
	}
	if img.BootMethod != "uki" || img.BootParams != "audit=0" || img.Distro != "" {
		t.Errorf("UKI image = method %q params %q distro %q, want uki/audit=0/no distro", img.BootMethod, img.BootParams, img.Distro)
	}

	if code := uploadImage(t, h, "grub.efi", []byte("not a PE file")); code != http.StatusBadRequest {
		t.Errorf("non-UKI .efi upload returned %d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(isoDir, "grub.efi")); !os.IsNotExist(err) {
		t.Error("rejected .efi upload must be removed from disk")
	}

	if code := uploadImage(t, h, "notes.txt", []byte("x")); code != http.StatusBadRequest {
		t.Errorf("unsupported file upload returned %d, want 400", code)
	}
}
