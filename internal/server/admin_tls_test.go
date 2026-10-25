package server

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/tlscert"
)

func TestAdminTLSConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     AdminTLSConfig
		wantErr bool
	}{
		{"disabled", AdminTLSConfig{}, false},
		{"self-signed", AdminTLSConfig{Enabled: true}, false},
		{"cert and key", AdminTLSConfig{Enabled: true, CertFile: "a.crt", KeyFile: "a.key"}, false},
		{"cert only", AdminTLSConfig{Enabled: true, CertFile: "a.crt"}, true},
		{"key only", AdminTLSConfig{Enabled: true, KeyFile: "a.key"}, true},
		{"bad redirect port", AdminTLSConfig{Enabled: true, RedirectPort: 70000}, true},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestPrepareAdminTLSSelfSignedReportsFingerprint(t *testing.T) {
	srv := &Server{config: &Config{DataDir: t.TempDir(), ServerAddr: "10.0.0.1", AdminPort: 8443, AdminTLS: AdminTLSConfig{Enabled: true}}}
	if srv.AdminTLSEnabled() || !strings.HasPrefix(srv.AdminTLSStatus(), "Disabled") {
		t.Fatalf("status before prepare = %q", srv.AdminTLSStatus())
	}
	if err := srv.prepareAdminTLS(); err != nil {
		t.Fatal(err)
	}
	status := srv.AdminTLSStatus()
	if !strings.Contains(status, "self-signed") || !strings.Contains(status, "SHA-256") {
		t.Fatalf("status = %q", status)
	}
	fp := tlscert.Fingerprint(srv.adminTLS.Certificate())

	again := &Server{config: srv.config}
	if err := again.prepareAdminTLS(); err != nil {
		t.Fatal(err)
	}
	if tlscert.Fingerprint(again.adminTLS.Certificate()) != fp {
		t.Fatal("second start should reuse the persisted certificate")
	}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts.TLS = srv.adminTLS.Config()
	ts.StartTLS()
	defer ts.Close()
	leaf, err := x509.ParseCertificate(srv.adminTLS.Certificate().Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
	resp, err := client.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Fatal("connection was not TLS 1.2 or newer")
	}
}

func TestPrepareAdminTLSDisabledIsNoop(t *testing.T) {
	srv := &Server{config: &Config{DataDir: t.TempDir()}}
	if err := srv.prepareAdminTLS(); err != nil {
		t.Fatal(err)
	}
	if srv.adminTLS != nil {
		t.Fatal("TLS should stay off when not enabled")
	}
	if err := srv.ReloadTLS(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSRedirectHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://bootimus.example.test:8081/api/stats?x=1", nil)
	rec := httptest.NewRecorder()
	httpsRedirectHandler(8443).ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("code = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://bootimus.example.test:8443/api/stats?x=1" {
		t.Fatalf("location = %q", loc)
	}
}
