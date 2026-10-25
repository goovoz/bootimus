package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"bootimus/internal/models"
)

func previewResponse(t *testing.T, env *accessEnv, path string) bootMenuRender {
	t.Helper()
	code, body := env.do(http.MethodGet, path, env.admin)
	if code != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d %s", path, code, body)
	}
	var resp struct {
		Success bool           `json:"success"`
		Data    bootMenuRender `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if !resp.Success {
		t.Fatalf("GET %s: success=false", path)
	}
	return resp.Data
}

func TestMenuPreviewDoesNotConsumeNextBoot(t *testing.T) {
	env := newAccessEnv(t)
	img := &models.Image{Name: "Ubuntu", Filename: "ubuntu.iso", Enabled: true, Public: true}
	if err := env.store.CreateImage(img); err != nil {
		t.Fatal(err)
	}
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:01", Name: "lab-01", Enabled: true, ShowPublicImages: true, Static: true}
	if err := env.store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetNextBootImage(client.MACAddress, img.Filename); err != nil {
		t.Fatal(err)
	}

	data := previewResponse(t, env, "/api/clients/menu-preview?mac=AA-BB-CC-DD-EE-01")
	if data.MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("mac not normalised: %q", data.MAC)
	}
	if !data.KnownClient || data.ClientName != "lab-01" {
		t.Errorf("expected known client lab-01, got known=%v name=%q", data.KnownClient, data.ClientName)
	}
	if data.Kind != "menu" {
		t.Errorf("kind = %q, want menu", data.Kind)
	}
	if data.NextBootImage != "Ubuntu" {
		t.Errorf("next boot image = %q, want Ubuntu", data.NextBootImage)
	}
	if !strings.HasPrefix(data.Script, "#!ipxe") {
		t.Errorf("script does not look like iPXE:\n%s", data.Script)
	}

	stored, err := env.store.GetClient(client.MACAddress)
	if err != nil {
		t.Fatal(err)
	}
	if stored.NextBootImage != img.Filename {
		t.Errorf("preview cleared the next boot image: %q", stored.NextBootImage)
	}
}

func TestMenuPreviewUnknownClient(t *testing.T) {
	env := newAccessEnv(t)
	data := previewResponse(t, env, "/api/clients/menu-preview")
	if data.MAC != "unknown" {
		t.Errorf("mac = %q, want unknown", data.MAC)
	}
	if data.KnownClient || data.ClientName != "" || data.NextBootImage != "" {
		t.Errorf("unexpected client details for anonymous preview: %+v", data)
	}
	if data.Kind != "menu" || !strings.Contains(data.Script, "#!ipxe") {
		t.Errorf("unexpected preview for anonymous client: %+v", data)
	}
	if code, _ := env.do(http.MethodPost, "/api/clients/menu-preview", env.admin); code != http.StatusMethodNotAllowed {
		t.Errorf("POST menu-preview: want 405, got %d", code)
	}
}
