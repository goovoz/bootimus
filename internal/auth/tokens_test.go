package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bootimus/internal/models"
)

type fakeTokenStore struct {
	fakeUserStore
	tokens  map[string]*models.APIToken
	touched int
}

func (f *fakeTokenStore) GetAPITokenByHash(hash string) (*models.APIToken, error) {
	t, ok := f.tokens[hash]
	if !ok {
		return nil, http.ErrNoLocation
	}
	c := *t
	return &c, nil
}

func (f *fakeTokenStore) TouchAPIToken(id uint, at time.Time) error {
	f.touched++
	return nil
}

func newTokenManager(t *testing.T) (*Manager, *fakeTokenStore) {
	t.Helper()
	store := &fakeTokenStore{
		fakeUserStore: fakeUserStore{users: map[string]*models.User{}},
		tokens:        map[string]*models.APIToken{},
	}
	return &Manager{userStore: store, jwtSecret: []byte("test-secret-0123456789")}, store
}

func issue(t *testing.T, store *fakeTokenStore, name, scope string, expires *time.Time, revoked bool) string {
	t.Helper()
	secret, err := GenerateAPISecret()
	if err != nil {
		t.Fatal(err)
	}
	store.tokens[HashAPISecret(secret)] = &models.APIToken{ID: uint(len(store.tokens) + 1), Name: name, Scope: scope, Hash: HashAPISecret(secret), ExpiresAt: expires, Revoked: revoked}
	return secret
}

func callWith(m *Manager, wrap func(http.HandlerFunc) http.HandlerFunc, method, bearer string) (int, string) {
	var seen string
	h := wrap(func(w http.ResponseWriter, r *http.Request) {
		seen = UsernameFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(method, "/api/clients", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code, seen
}

func TestGenerateAPISecretShape(t *testing.T) {
	secret, err := GenerateAPISecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, APITokenPrefix) || len(secret) != len(APITokenPrefix)+apiTokenSecretLength {
		t.Fatalf("unexpected secret %q", secret)
	}
	if !IsAPISecret(secret) || IsAPISecret("eyJhbGciOi") {
		t.Fatal("IsAPISecret misclassifies")
	}
	if APISecretPrefix(secret) != secret[:8] {
		t.Fatal("prefix should be the first eight characters")
	}
	other, _ := GenerateAPISecret()
	if other == secret {
		t.Fatal("secrets must be unique")
	}
}

func TestAPITokenScopes(t *testing.T) {
	m, store := newTokenManager(t)
	readSecret := issue(t, store, "ci-reader", models.APITokenScopeRead, nil, false)
	adminSecret := issue(t, store, "deployer", models.APITokenScopeAdmin, nil, false)

	if code, user := callWith(m, m.ReadMiddleware, http.MethodGet, readSecret); code != http.StatusOK || user != "token:ci-reader" {
		t.Fatalf("read token GET: %d as %q", code, user)
	}
	if code, _ := callWith(m, m.ReadMiddleware, http.MethodPut, readSecret); code != http.StatusForbidden {
		t.Fatalf("read token PUT: want 403, got %d", code)
	}
	if code, _ := callWith(m, m.AdminMiddleware, http.MethodPost, readSecret); code != http.StatusForbidden {
		t.Fatalf("read token on admin route: want 403, got %d", code)
	}
	if code, user := callWith(m, m.AdminMiddleware, http.MethodPost, adminSecret); code != http.StatusOK || user != "token:deployer" {
		t.Fatalf("admin token: %d as %q", code, user)
	}
	if store.touched != 2 {
		t.Fatalf("expected one touch per token on first use, got %d", store.touched)
	}
	callWith(m, m.AdminMiddleware, http.MethodPost, adminSecret)
	if store.touched != 2 {
		t.Fatalf("second use within a minute must not touch again, got %d", store.touched)
	}
}

func TestAPITokenRejectedWhenRevokedExpiredOrUnknown(t *testing.T) {
	m, store := newTokenManager(t)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	revoked := issue(t, store, "old", models.APITokenScopeAdmin, nil, true)
	expired := issue(t, store, "stale", models.APITokenScopeAdmin, &past, false)
	live := issue(t, store, "fresh", models.APITokenScopeAdmin, &future, false)

	for name, secret := range map[string]string{"revoked": revoked, "expired": expired, "unknown": APITokenPrefix + strings.Repeat("x", apiTokenSecretLength)} {
		if code, _ := callWith(m, m.AdminMiddleware, http.MethodGet, secret); code != http.StatusUnauthorized {
			t.Errorf("%s token: want 401, got %d", name, code)
		}
	}
	if code, _ := callWith(m, m.AdminMiddleware, http.MethodGet, live); code != http.StatusOK {
		t.Errorf("live token with future expiry: want 200, got %d", code)
	}
}

func TestAPITokenIgnoredWithoutTokenStore(t *testing.T) {
	m := &Manager{userStore: &fakeUserStore{users: map[string]*models.User{}}, jwtSecret: []byte("test-secret-0123456789")}
	if code, _ := callWith(m, m.AdminMiddleware, http.MethodGet, APITokenPrefix+"anything"); code != http.StatusUnauthorized {
		t.Fatalf("want 401 when the store cannot look tokens up, got %d", code)
	}
}
