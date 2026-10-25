package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSelfSignedIsCreatedOnceAndReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	first, created, err := LoadOrCreateSelfSigned(dir, []string{"10.0.0.5", "pxe.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first load should create the certificate")
	}
	second, created, err := LoadOrCreateSelfSigned(dir, []string{"10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second load should reuse the certificate")
	}
	if Fingerprint(first) != Fingerprint(second) {
		t.Fatalf("fingerprint changed between loads: %s vs %s", Fingerprint(first), Fingerprint(second))
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{CertFileName, KeyFileName} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
			}
		}
	}
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("10.0.0.5"); err != nil {
		t.Fatalf("IP SAN missing: %v", err)
	}
	if err := leaf.VerifyHostname("pxe.example.test"); err != nil {
		t.Fatalf("DNS SAN missing: %v", err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Fatalf("localhost SAN missing: %v", err)
	}
}

func TestFingerprintFormat(t *testing.T) {
	cert, _, err := LoadOrCreateSelfSigned(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(cert)
	if len(fp) != 95 {
		t.Fatalf("fingerprint %q has length %d, want 95", fp, len(fp))
	}
}

func TestReloaderServesAndReloads(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	certA, _, err := LoadOrCreateSelfSigned(dirA, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	certB, _, err := LoadOrCreateSelfSigned(dirB, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	current := dirA
	reloader, err := NewReloader(func() (tls.Certificate, error) {
		return Load(filepath.Join(current, CertFileName), filepath.Join(current, KeyFileName))
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secure")
	}))
	srv.TLS = reloader.Config()
	srv.StartTLS()
	defer srv.Close()

	get := func(trust tls.Certificate) (int, error) {
		pool := x509.NewCertPool()
		leaf, err := x509.ParseCertificate(trust.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		pool.AddCert(leaf)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}}}
		resp, err := client.Get(srv.URL)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}

	if code, err := get(certA); err != nil || code != http.StatusOK {
		t.Fatalf("request with certificate A: code=%d err=%v", code, err)
	}
	if _, err := get(certB); err == nil {
		t.Fatal("certificate B should not be trusted before reload")
	}

	current = dirB
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if code, err := get(certB); err != nil || code != http.StatusOK {
		t.Fatalf("request after reload: code=%d err=%v", code, err)
	}
	if Fingerprint(reloader.Certificate()) != Fingerprint(certB) {
		t.Fatal("reloader did not switch to certificate B")
	}
}

func TestReloadKeepsOldCertificateOnFailure(t *testing.T) {
	dir := t.TempDir()
	cert, _, err := LoadOrCreateSelfSigned(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	broken := false
	reloader, err := NewReloader(func() (tls.Certificate, error) {
		if broken {
			return Load(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"))
		}
		return Load(filepath.Join(dir, CertFileName), filepath.Join(dir, KeyFileName))
	})
	if err != nil {
		t.Fatal(err)
	}
	broken = true
	if err := reloader.Reload(); err == nil {
		t.Fatal("reload with missing files should fail")
	}
	if Fingerprint(reloader.Certificate()) != Fingerprint(cert) {
		t.Fatal("failed reload must keep the previous certificate")
	}
}
