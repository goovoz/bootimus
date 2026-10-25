package auth

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestSecureCookieFollowsTLS(t *testing.T) {
	plain := &oidcClient{config: &OIDCConfig{RedirectURL: "http://bootimus.example.test/api/oidc/callback"}}
	req := httptest.NewRequest("GET", "/api/oidc/login", nil)
	if plain.secureCookie(req) {
		t.Fatal("plain HTTP with an http redirect URL must not set Secure")
	}
	req.TLS = &tls.ConnectionState{}
	if !plain.secureCookie(req) {
		t.Fatal("a TLS request must set Secure even with an http redirect URL")
	}
	https := &oidcClient{config: &OIDCConfig{RedirectURL: "https://bootimus.example.test/api/oidc/callback"}}
	if !https.secureCookie(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("an https redirect URL must set Secure")
	}
	if !https.secureCookie(nil) {
		t.Fatal("nil request with https redirect URL must set Secure")
	}
}
