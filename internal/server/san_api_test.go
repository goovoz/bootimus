package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/models"
)

func (e *accessEnv) doJSON(method, path, token string, body interface{}) (int, string) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestSANTargetsAPIAndViewerRedaction(t *testing.T) {
	env := newAccessEnv(t)

	code, body := env.doJSON(http.MethodPost, "/api/san-targets", env.admin, map[string]string{
		"name": "lab-disk", "uri": "iscsi:10.0.0.5::::iqn.2024-01.lan:disk0",
		"chap_username": "boot", "chap_password": "chap-secret",
		"reverse_chap_username": "srv", "reverse_chap_password": "reverse-secret",
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Data models.SANTarget `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}

	if code, body := env.doJSON(http.MethodPost, "/api/san-targets", env.admin, map[string]string{"name": "bad", "uri": "nfs:x"}); code != http.StatusBadRequest {
		t.Errorf("bad scheme: want 400, got %d %s", code, body)
	}
	if code, body := env.doJSON(http.MethodPost, "/api/san-targets", env.admin, map[string]string{"name": "lab-disk", "uri": "aoe:e0.0"}); code != http.StatusConflict {
		t.Errorf("duplicate name: want 409, got %d %s", code, body)
	}

	code, body = env.do(http.MethodGet, "/api/san-targets", env.viewer)
	if code != http.StatusOK {
		t.Fatalf("viewer list: %d %s", code, body)
	}
	for _, secret := range []string{"chap-secret", "reverse-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("viewer response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "lab-disk") || !strings.Contains(body, `"client_count":0`) {
		t.Errorf("viewer lost non-secret fields: %s", body)
	}
	if _, body := env.do(http.MethodGet, "/api/san-targets", env.admin); !strings.Contains(body, "chap-secret") {
		t.Errorf("admin should see CHAP secrets for the edit form: %s", body)
	}
	if code, _ := env.doJSON(http.MethodPost, "/api/san-targets", env.viewer, map[string]string{"name": "v", "uri": "aoe:e0.1"}); code != http.StatusForbidden {
		t.Errorf("viewer create: want 403, got %d", code)
	}

	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:02", Name: "san-01", Enabled: true, Static: true}
	if err := env.store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	code, body = env.doJSON(http.MethodPut, "/api/clients?mac="+client.MACAddress, env.admin, map[string]interface{}{"san_target_id": created.Data.ID, "boot_from_san": true})
	if code != http.StatusOK {
		t.Fatalf("assign target: %d %s", code, body)
	}
	data := previewResponse(t, env, "/api/clients/menu-preview?mac="+client.MACAddress)
	if !strings.Contains(data.Script, "item san Boot from SAN: lab-disk") || !strings.Contains(data.Script, "choose --default san") {
		t.Errorf("preview should default to the SAN entry:\n%s", data.Script)
	}
	if !strings.Contains(data.Script, "set password chap-secret") {
		t.Errorf("preview should carry CHAP credentials:\n%s", data.Script)
	}

	code, body = env.doJSON(http.MethodPost, "/api/clients/next-boot", env.admin, map[string]string{"mac_address": client.MACAddress, "image_filename": created.Data.NextBootValue()})
	if code != http.StatusOK {
		t.Fatalf("next boot san: %d %s", code, body)
	}
	data = previewResponse(t, env, "/api/clients/menu-preview?mac="+client.MACAddress)
	if data.NextBootImage != "SAN: lab-disk" || !strings.HasPrefix(data.Script, "#!ipxe\n\ngoto san\n\n") {
		t.Errorf("one-shot SAN boot not reflected: next=%q\n%s", data.NextBootImage, data.Script)
	}
	if code, body := env.doJSON(http.MethodPost, "/api/clients/next-boot", env.admin, map[string]string{"mac_address": client.MACAddress, "image_filename": "san:999"}); code != http.StatusBadRequest {
		t.Errorf("unknown san next boot: want 400, got %d %s", code, body)
	}

	if code, body := env.doJSON(http.MethodPut, "/api/clients?mac="+client.MACAddress, env.admin, map[string]interface{}{"san_target_id": 999}); code != http.StatusBadRequest {
		t.Errorf("unknown target on client: want 400, got %d %s", code, body)
	}

	code, body = env.doJSON(http.MethodDelete, fmt.Sprintf("/api/san-targets?id=%d", created.Data.ID), env.admin, nil)
	if code != http.StatusOK {
		t.Fatalf("delete: %d %s", code, body)
	}
	data = previewResponse(t, env, "/api/clients/menu-preview?mac="+client.MACAddress)
	if strings.Contains(data.Script, ":san") {
		t.Errorf("deleted target still in menu:\n%s", data.Script)
	}
}

func TestGroupSANTargetInherited(t *testing.T) {
	env := newAccessEnv(t)
	target := &models.SANTarget{Name: "shared", URI: "aoe:e0.0"}
	if err := env.store.CreateSANTarget(target); err != nil {
		t.Fatal(err)
	}
	group := &models.ClientGroup{Name: "rack", SANTargetID: &target.ID, BootFromSAN: true}
	if err := env.store.CreateClientGroup(group); err != nil {
		t.Fatal(err)
	}
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:03", Enabled: true, Static: true, ClientGroupID: &group.ID}
	if err := env.store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	data := previewResponse(t, env, "/api/clients/menu-preview?mac="+client.MACAddress)
	if !strings.Contains(data.Script, "sanboot --no-describe --drive 0x80 aoe:e0.0") || !strings.Contains(data.Script, "choose --default san") {
		t.Errorf("group target not inherited:\n%s", data.Script)
	}
}
