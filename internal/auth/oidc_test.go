package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"bootimus/internal/models"

	"github.com/golang-jwt/jwt/v5"
)

type fakeProvider struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	nonce       string
	idClaims    map[string]interface{}
	userInfo    map[string]interface{}
	gotVerifier string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key, idClaims: map[string]interface{}{}, userInfo: map[string]interface{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"issuer":                                p.server.URL,
			"authorization_endpoint":                p.server.URL + "/auth",
			"token_endpoint":                        p.server.URL + "/token",
			"jwks_uri":                              p.server.URL + "/keys",
			"userinfo_endpoint":                     p.server.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"keys": []map[string]string{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": "test",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "invalid_grant"})
			return
		}
		p.gotVerifier = r.Form.Get("code_verifier")
		claims := jwt.MapClaims{
			"iss":   p.server.URL,
			"aud":   "bootimus",
			"sub":   "subject-1",
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
			"nonce": p.nonce,
		}
		for k, v := range p.idClaims {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test"
		signed, err := tok.SignedString(key)
		if err != nil {
			t.Error(err)
		}
		writeJSON(w, map[string]interface{}{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": signed})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]interface{}{"sub": "subject-1"}
		for k, v := range p.userInfo {
			info[k] = v
		}
		writeJSON(w, info)
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func newOIDCTestManager(p *fakeProvider, cfg OIDCConfig) (*Manager, *fakeUserStore) {
	cfg.Issuer = p.server.URL
	cfg.ClientID = "bootimus"
	cfg.ClientSecret = "secret"
	cfg.RedirectURL = "https://bootimus.example.com/api/oidc/callback"
	m, store := newTestManager(map[string]*models.User{
		"admin": {Username: "admin", Enabled: true, IsAdmin: true},
	})
	m.oidc = newOIDCClient(&cfg)
	return m, store
}

type flowResult struct {
	token string
	err   string
}

func runOIDCFlow(t *testing.T, m *Manager, p *fakeProvider, tamper func(q url.Values, cookies []*http.Cookie) []*http.Cookie) flowResult {
	t.Helper()
	rec := httptest.NewRecorder()
	m.HandleOIDCLogin(rec, httptest.NewRequest(http.MethodGet, "/api/oidc/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: want 302, got %d", rec.Code)
	}
	authURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	aq := authURL.Query()
	if aq.Get("code_challenge") == "" || aq.Get("code_challenge_method") != "S256" {
		t.Fatalf("login: PKCE challenge missing from %s", authURL)
	}
	if aq.Get("state") == "" || aq.Get("nonce") == "" {
		t.Fatalf("login: state or nonce missing from %s", authURL)
	}
	p.nonce = aq.Get("nonce")

	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == oidcStateCookie && (!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode) {
			t.Fatalf("login: state cookie attributes wrong: %+v", c)
		}
	}

	q := url.Values{"code": {"good-code"}, "state": {aq.Get("state")}}
	if tamper != nil {
		cookies = tamper(q, cookies)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/oidc/callback?"+q.Encode(), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	m.HandleOIDCCallback(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: want 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	frag, err := url.ParseQuery(strings.TrimPrefix(loc, "/#"))
	if err != nil {
		t.Fatalf("callback: bad redirect %q", loc)
	}
	return flowResult{token: frag.Get("oidc_token"), err: frag.Get("oidc_error")}
}

func TestOIDCLoginCreatesAdminFromGroups(t *testing.T) {
	p := newFakeProvider(t)
	p.idClaims = map[string]interface{}{"preferred_username": "Alice", "groups": []string{"staff", "Bootimus Admins"}}
	m, store := newOIDCTestManager(p, OIDCConfig{AdminGroups: []string{"bootimus admins"}})

	res := runOIDCFlow(t, m, p, nil)
	if res.token == "" {
		t.Fatalf("want token, got error %q", res.err)
	}
	if p.gotVerifier == "" {
		t.Fatal("token exchange did not send a PKCE verifier")
	}
	claims, err := m.ValidateToken(res.token)
	if err != nil || claims.Username != "alice" || !claims.IsAdmin {
		t.Fatalf("unexpected token claims %+v (%v)", claims, err)
	}
	user := store.users["alice"]
	if user == nil || user.Source != models.UserSourceOIDC || !user.IsAdmin {
		t.Fatalf("user not provisioned correctly: %+v", user)
	}
	if code := callAdmin(m, "Bearer "+res.token); code != http.StatusOK {
		t.Fatalf("OIDC admin token: want 200, got %d", code)
	}
}

func TestOIDCAllowedGroups(t *testing.T) {
	p := newFakeProvider(t)
	m, store := newOIDCTestManager(p, OIDCConfig{AdminGroups: []string{"admins"}, AllowedGroups: []string{"operators"}})

	p.idClaims = map[string]interface{}{"preferred_username": "bob", "groups": []string{"operators"}}
	res := runOIDCFlow(t, m, p, nil)
	if res.token == "" {
		t.Fatalf("allowed user: want token, got error %q", res.err)
	}
	if store.users["bob"].IsAdmin {
		t.Fatal("allowed user should not be admin")
	}

	p.idClaims = map[string]interface{}{"preferred_username": "mallory", "groups": []string{"contractors"}}
	res = runOIDCFlow(t, m, p, nil)
	if res.token != "" || res.err == "" {
		t.Fatalf("user outside allowed groups: want error, got %+v", res)
	}
	if _, ok := store.users["mallory"]; ok {
		t.Fatal("user outside allowed groups was provisioned")
	}
}

func TestOIDCClaimsFromUserInfo(t *testing.T) {
	p := newFakeProvider(t)
	p.userInfo = map[string]interface{}{"preferred_username": "carol", "groups": "admins"}
	m, store := newOIDCTestManager(p, OIDCConfig{AdminGroups: []string{"admins"}})

	res := runOIDCFlow(t, m, p, nil)
	if res.token == "" {
		t.Fatalf("want token, got error %q", res.err)
	}
	if u := store.users["carol"]; u == nil || !u.IsAdmin {
		t.Fatalf("userinfo claims not used: %+v", u)
	}
}

func TestOIDCRejectsBadCallbacks(t *testing.T) {
	p := newFakeProvider(t)
	p.idClaims = map[string]interface{}{"preferred_username": "admin", "groups": []string{"admins"}}
	m, _ := newOIDCTestManager(p, OIDCConfig{AdminGroups: []string{"admins"}})

	if res := runOIDCFlow(t, m, p, nil); res.token != "" {
		t.Fatal("OIDC user took over the local admin account")
	}

	p.idClaims["preferred_username"] = "dave"
	cases := map[string]func(q url.Values, cookies []*http.Cookie) []*http.Cookie{
		"wrong state": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			q.Set("state", "forged")
			return c
		},
		"missing cookie": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			return nil
		},
		"session token as state": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			tok, _ := m.GenerateToken("admin", true)
			return []*http.Cookie{{Name: oidcStateCookie, Value: tok}}
		},
		"bad code": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			q.Set("code", "bad-code")
			return c
		},
		"provider error": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			q.Set("error", "access_denied")
			return c
		},
		"wrong nonce": func(q url.Values, c []*http.Cookie) []*http.Cookie {
			p.nonce = "replayed"
			return c
		},
	}
	for name, tamper := range cases {
		res := runOIDCFlow(t, m, p, tamper)
		if res.token != "" || res.err == "" {
			t.Errorf("%s: want error redirect, got %+v", name, res)
		}
	}
}

func TestClaimStrings(t *testing.T) {
	claims := map[string]interface{}{
		"list":   []interface{}{"a", 1, "b"},
		"single": "c",
	}
	if got := claimStrings(claims, "list"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("list: got %v", got)
	}
	if got := claimStrings(claims, "single"); len(got) != 1 || got[0] != "c" {
		t.Errorf("single: got %v", got)
	}
	if got := claimStrings(claims, "missing"); got != nil {
		t.Errorf("missing: got %v", got)
	}
}

func TestUserMatches(t *testing.T) {
	entries := []string{"Alice@Example.com", "@ops.example.com", " "}
	cases := map[string]bool{
		"alice@example.com":         true,
		"bob@example.com":           false,
		"carol@ops.example.com":     true,
		"dave@evil-ops.example.com": false,
		"erin@ops.example.com.evil": false,
		"ops.example.com":           false,
	}
	for username, want := range cases {
		if got := userMatches(username, entries); got != want {
			t.Errorf("%s: want %v, got %v", username, want, got)
		}
	}
}

func TestOIDCUserListsWithoutGroups(t *testing.T) {
	p := newFakeProvider(t)
	m, store := newOIDCTestManager(p, OIDCConfig{
		UsernameClaim: "email",
		AdminUsers:    []string{"alice@example.com"},
		AllowedUsers:  []string{"@example.com"},
	})

	p.idClaims = map[string]interface{}{"email": "Alice@example.com", "email_verified": true}
	if res := runOIDCFlow(t, m, p, nil); res.token == "" {
		t.Fatalf("admin user: %q", res.err)
	}
	if u := store.users["alice@example.com"]; u == nil || !u.IsAdmin {
		t.Fatalf("admin user not made admin: %+v", u)
	}

	p.idClaims = map[string]interface{}{"email": "bob@example.com", "email_verified": true}
	if res := runOIDCFlow(t, m, p, nil); res.token == "" {
		t.Fatalf("domain user: %q", res.err)
	}
	if store.users["bob@example.com"].IsAdmin {
		t.Fatal("domain user should be read-only")
	}

	p.idClaims = map[string]interface{}{"email": "mallory@gmail.com", "email_verified": true}
	if res := runOIDCFlow(t, m, p, nil); res.token != "" {
		t.Fatal("user outside the allowed domain signed in")
	}

	p.idClaims = map[string]interface{}{"email": "carol@example.com", "email_verified": false}
	if res := runOIDCFlow(t, m, p, nil); res.token != "" {
		t.Fatal("unverified email signed in")
	}
	p.idClaims = map[string]interface{}{"email": "dave@example.com"}
	if res := runOIDCFlow(t, m, p, nil); res.token != "" {
		t.Fatal("email without email_verified signed in")
	}
}
