package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/autoinstall"
	"bootimus/internal/models"
)

func TestHandleAutoInstallScriptServesAutounattend(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, srv *Server, img *models.Image)
	}{
		{
			name: "library file",
			setup: func(t *testing.T, srv *Server, img *models.Image) {
				lib, err := autoinstall.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if err := lib.Write("windows", "kiosk.xml", "<unattend><ComputerName>{{HOSTNAME}}</ComputerName><Mac>{{MAC}}</Mac></unattend>"); err != nil {
					t.Fatal(err)
				}
				srv.autoInstallLib = lib
				img.AutoInstallFile = "windows/kiosk.xml"
			},
		},
		{
			name: "inline legacy script",
			setup: func(t *testing.T, srv *Server, img *models.Image) {
				img.AutoInstallEnabled = true
				img.AutoInstallScriptType = "autounattend"
				img.AutoInstallScript = "<unattend><ComputerName>{{HOSTNAME}}</ComputerName><Mac>{{MAC}}</Mac></unattend>"
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, store := newInstallEnv(t)
			img := &models.Image{Filename: "Win11.iso", Name: "Windows 11", Enabled: true, BootMethod: "kernel", Distro: "windows"}
			c.setup(t, srv, img)
			if err := store.CreateImage(img); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			srv.handleAutoInstallScript(rec, httptest.NewRequest(http.MethodGet, "/autoinstall/Win11.iso?mac="+installTestMAC, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
				t.Errorf("Content-Type = %q, want application/xml", ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "<ComputerName>bench-1</ComputerName>") {
				t.Errorf("expected {{HOSTNAME}} substituted with the client name:\n%s", body)
			}
			if !strings.Contains(body, "<Mac>"+installTestMAC+"</Mac>") {
				t.Errorf("expected {{MAC}} substituted:\n%s", body)
			}
			if strings.Contains(body, "{{") {
				t.Errorf("unsubstituted placeholder left in body:\n%s", body)
			}
		})
	}
}
