package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/autoinstall"
	"bootimus/internal/models"
)

const proxmoxTestISO = "proxmox-ve_9.1-1.iso"

func newProxmoxEnv(t *testing.T) *Server {
	t.Helper()
	srv, store := newInstallEnv(t)
	lib, err := autoinstall.New(srv.config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	srv.autoInstallLib = lib
	for name, content := range map[string]string{
		"default.toml": "[global]\nfqdn = \"default-{{MAC}}.lan\"\n",
		"bench.toml":   "[global]\nfqdn = \"{{HOSTNAME}}.lan\"\nmac = \"{{MAC}}\"\n",
	} {
		if err := lib.Write("proxmox", name, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateClient(&models.Client{MACAddress: "aa:bb:cc:dd:ee:20", Name: "pve-bench", Enabled: true, AutoInstallFile: "proxmox/bench.toml"}); err != nil {
		t.Fatal(err)
	}
	img := &models.Image{Filename: proxmoxTestISO, Name: "Proxmox VE", Enabled: true, BootMethod: "kernel", Distro: "proxmox", AutoInstallFile: "proxmox/default.toml"}
	if err := store.CreateImage(img); err != nil {
		t.Fatal(err)
	}
	return srv
}

func postProxmoxSystemInfo(srv *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/autoinstall/"+proxmoxTestISO, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	srv.handleAutoInstallScript(rec, req)
	return rec
}

func TestAutoInstallProxmoxPostResolvesClient(t *testing.T) {
	cases := []struct {
		name            string
		interfaces      string
		wantBody        string
		wantMissingBody string
	}{
		{
			name:       "known mac on second interface wins",
			interfaces: `[{"link":"eno1","mac":"AA:BB:CC:DD:EE:99"},{"link":"eno2","mac":"AA:BB:CC:DD:EE:20"}]`,
			wantBody:   "fqdn = \"pve-bench.lan\"\nmac = \"aa:bb:cc:dd:ee:20\"\n",
		},
		{
			name:       "unknown macs fall back to the image default with the first mac",
			interfaces: `[{"link":"eno1","mac":"aa:bb:cc:dd:ee:98"},{"link":"eno2","mac":"aa:bb:cc:dd:ee:97"}]`,
			wantBody:   "fqdn = \"default-aa:bb:cc:dd:ee:98.lan\"\n",
		},
	}
	for _, c := range cases {
		srv := newProxmoxEnv(t)
		rec := postProxmoxSystemInfo(srv, `{"$schema":{"version":"1.0"},"product":{"fullname":"Proxmox VE","product":"pve","enable_btrfs":true},"network_interfaces":`+c.interfaces+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d %s", c.name, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/toml; charset=utf-8" {
			t.Errorf("%s: content type = %q", c.name, ct)
		}
		if !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s: body = %q, want it to contain %q", c.name, rec.Body.String(), c.wantBody)
		}
	}
}

func TestAutoInstallProxmoxPostRejectsBodiesWithoutInterfaces(t *testing.T) {
	srv := newProxmoxEnv(t)
	for _, body := range []string{"", "not json", `{"network_interfaces":[]}`} {
		if rec := postProxmoxSystemInfo(srv, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestAutoInstallGetStillResolvesByQueryMAC(t *testing.T) {
	srv := newProxmoxEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/autoinstall/"+proxmoxTestISO+"?mac=aa-bb-cc-dd-ee-20", nil)
	rec := httptest.NewRecorder()
	srv.handleAutoInstallScript(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pve-bench.lan") {
		t.Errorf("expected the per-client file, got %q", rec.Body.String())
	}
}
