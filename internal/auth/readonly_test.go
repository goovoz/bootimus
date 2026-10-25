package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/models"
)

func newRoleManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, _ := newTestManager(map[string]*models.User{
		"alice": {Username: "alice", Enabled: true, IsAdmin: true},
		"vic":   {Username: "vic", Enabled: true},
	})
	admin, err := m.GenerateToken("alice", true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := m.GenerateToken("vic", true)
	if err != nil {
		t.Fatal(err)
	}
	return m, admin, viewer
}

func serve(h http.HandlerFunc, method, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/clients", strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

const clientJSON = `{"success":true,"data":[{"mac_address":"aa:bb","ipmi_host":"10.0.0.9","ipmi_password":"hunter2","config_patch":"secret: x","size":9007199254740993,"client_group":{"name":"rack","ipmi_password":"grouppw","clients":[{"ipmi_password":"nested"}]}}]}`

func TestReadMiddlewareMethods(t *testing.T) {
	m, admin, viewer := newRoleManager(t)
	called := 0
	h := m.ReadMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		method, token string
		want          int
	}{
		{http.MethodGet, "", http.StatusUnauthorized},
		{http.MethodGet, viewer, http.StatusOK},
		{http.MethodHead, viewer, http.StatusOK},
		{http.MethodPost, viewer, http.StatusForbidden},
		{http.MethodPut, viewer, http.StatusForbidden},
		{http.MethodDelete, viewer, http.StatusForbidden},
		{http.MethodGet, admin, http.StatusOK},
		{http.MethodPost, admin, http.StatusOK},
		{http.MethodDelete, admin, http.StatusOK},
	}
	for _, tc := range cases {
		before := called
		rec := serve(h, tc.method, tc.token)
		if rec.Code != tc.want {
			t.Errorf("%s (token %v): want %d, got %d", tc.method, tc.token != "", tc.want, rec.Code)
		}
		if tc.want != http.StatusOK && called != before {
			t.Errorf("%s: handler ran for a refused request", tc.method)
		}
	}
}

func TestReadMiddlewareRedactsForViewers(t *testing.T) {
	m, admin, viewer := newRoleManager(t)
	h := m.ReadMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(clientJSON))
	})

	rec := serve(h, http.MethodGet, viewer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status not preserved: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, secret := range []string{"hunter2", "grouppw", "nested", "secret: x", "ipmi_password", "config_patch"} {
		if strings.Contains(body, secret) {
			t.Errorf("viewer response still contains %q: %s", secret, body)
		}
	}
	for _, kept := range []string{`"ipmi_host":"10.0.0.9"`, `"size":9007199254740993`, `"name":"rack"`} {
		if !strings.Contains(body, kept) {
			t.Errorf("viewer response lost %s: %s", kept, body)
		}
	}
	if rec.Header().Get("Content-Length") == "999" {
		t.Error("stale Content-Length kept")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("redacted body is not valid JSON: %v", err)
	}

	rec = serve(h, http.MethodGet, admin)
	if !strings.Contains(rec.Body.String(), "hunter2") {
		t.Error("admin response was redacted")
	}
}

func TestReadMiddlewarePassesThroughNonJSON(t *testing.T) {
	m, _, viewer := newRoleManager(t)
	h := m.ReadMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(`{"ipmi_password":"raw bytes"}`))
	})
	rec := serve(h, http.MethodGet, viewer)
	if rec.Body.String() != `{"ipmi_password":"raw bytes"}` {
		t.Fatalf("binary body altered: %q", rec.Body.String())
	}
}

func TestReadMiddlewareFailsClosedOnBadJSON(t *testing.T) {
	m, _, viewer := newRoleManager(t)
	h := m.ReadMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ipmi_password":"hunter2"`))
	})
	rec := serve(h, http.MethodGet, viewer)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("want 500 without the secret, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleMe(t *testing.T) {
	m, admin, viewer := newRoleManager(t)
	for token, want := range map[string]string{admin: "admin", viewer: "read-only"} {
		rec := serve(m.HandleMe, http.MethodGet, token)
		var resp struct {
			Data struct {
				Role    string `json:"role"`
				IsAdmin bool   `json:"is_admin"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Data.Role != want || resp.Data.IsAdmin != (want == "admin") {
			t.Errorf("want role %s, got %+v", want, resp.Data)
		}
	}
}
