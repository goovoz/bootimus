package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"bootimus/internal/models"
)

func newTestManager(users map[string]*models.User) (*Manager, *fakeUserStore) {
	store := &fakeUserStore{users: users}
	return &Manager{userStore: store, jwtSecret: []byte("test-secret-0123456789")}, store
}

func callAdmin(m *Manager, authHeader string) int {
	h := m.AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.Header.Set("Authorization", authHeader)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code
}

func TestProvisionExternalUserCreatesAndRefreshes(t *testing.T) {
	m, store := newTestManager(map[string]*models.User{})

	user, err := m.provisionExternalUser("carol", models.UserSourceOIDC, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !user.IsAdmin || !user.Enabled || user.Source != models.UserSourceOIDC {
		t.Fatalf("create: unexpected user %+v", user)
	}
	stored := store.users["carol"]
	if stored == nil || stored.Password == "" {
		t.Fatalf("create: user not stored with a password: %+v", stored)
	}

	user, err = m.provisionExternalUser("carol", models.UserSourceOIDC, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if user.IsAdmin || store.users["carol"].IsAdmin {
		t.Fatalf("refresh: admin rights not revoked")
	}
}

func TestProvisionExternalUserRefusesOtherSources(t *testing.T) {
	m, _ := newTestManager(map[string]*models.User{
		"admin":  {Username: "admin", Enabled: true, IsAdmin: true},
		"dave":   {Username: "dave", Enabled: true, Source: models.UserSourceLDAP},
		"erin":   {Username: "erin", Enabled: false, Source: models.UserSourceOIDC},
		"legacy": {Username: "legacy", Enabled: true, Source: models.UserSourceLocal},
	})

	cases := []struct {
		username string
		want     error
	}{
		{"admin", errSourceConflict},
		{"legacy", errSourceConflict},
		{"dave", errSourceConflict},
		{"erin", errUserDisabled},
		{"bad'name", errInvalidUsername},
		{"", errInvalidUsername},
	}
	for _, tc := range cases {
		if _, err := m.provisionExternalUser(tc.username, models.UserSourceOIDC, true); !errors.Is(err, tc.want) {
			t.Errorf("%q: want %v, got %v", tc.username, tc.want, err)
		}
	}
}

func TestLocalLoginRejectsExternalUsers(t *testing.T) {
	m, store := newTestManager(map[string]*models.User{})
	if _, err := m.provisionExternalUser("frank", models.UserSourceLDAP, false); err != nil {
		t.Fatal(err)
	}
	if err := store.users["frank"].SetPassword("known"); err != nil {
		t.Fatal(err)
	}
	if m.ValidateCredentials("frank", "known") {
		t.Fatal("local login accepted for an LDAP user")
	}
}

func TestExternalUserPassesAuthentication(t *testing.T) {
	m, _ := newTestManager(map[string]*models.User{})
	if _, err := m.provisionExternalUser("grace", models.UserSourceLDAP, true); err != nil {
		t.Fatal(err)
	}
	tok, err := m.GenerateToken("grace", true)
	if err != nil {
		t.Fatal(err)
	}
	if code := callAdmin(m, "Bearer "+tok); code != 200 {
		t.Fatalf("provisioned LDAP admin: want 200, got %d", code)
	}
}
