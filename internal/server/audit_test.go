package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"bootimus/internal/auth"
	"bootimus/internal/models"
	"bootimus/internal/storage"
)

type auditEnv struct {
	srv     *Server
	handler http.Handler
	store   *storage.SQLiteStore
	admin   string
}

func newAuditEnv(t *testing.T) *auditEnv {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	mgr, err := auth.NewManager(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	alice := &models.User{Username: "alice", Enabled: true, IsAdmin: true, Source: models.UserSourceLocal}
	if err := alice.SetPassword("alice-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	srv := &Server{config: &Config{Storage: store, Auth: mgr, ServerAddr: "10.0.0.1", HTTPPort: 8080, DataDir: t.TempDir()}}
	mux := http.NewServeMux()
	srv.setupAdminInterface(mux)
	tok, _ := mgr.GenerateToken("alice", true)
	return &auditEnv{srv: srv, handler: mux, store: store, admin: tok}
}

func (e *auditEnv) do(method, path, body string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.admin)
	req.RemoteAddr = "192.0.2.10:54321"
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	e.srv.audit.wait()
	return rec.Code
}

func (e *auditEnv) entries(t *testing.T) []models.AuditLog {
	t.Helper()
	entries, _, err := e.store.ListAuditLogs(storage.AuditLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestAuditRecordsMutatingAdminRequests(t *testing.T) {
	env := newAuditEnv(t)
	code := env.do(http.MethodPut, "/api/theme", `{"title":"Lab menu"}`)
	entries := env.entries(t)
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Username != "alice" || got.Method != http.MethodPut || got.Path != "/api/theme" || got.Status != code || got.RemoteIP != "192.0.2.10" {
		t.Errorf("unexpected entry %+v (status %d)", got, code)
	}
	if got.Summary != "Updated theme" {
		t.Errorf("summary = %q", got.Summary)
	}
}

func TestAuditIgnoresReadsAndLogin(t *testing.T) {
	env := newAuditEnv(t)
	env.do(http.MethodGet, "/api/theme", "")
	env.do(http.MethodGet, "/api/clients", "")
	env.do(http.MethodPost, "/api/login", `{"username":"alice","password":"nope"}`)
	unauth := httptest.NewRequest(http.MethodPut, "/api/theme", strings.NewReader(`{"title":"x"}`))
	unauth.Header.Set("Authorization", "Bearer not-a-token")
	env.handler.ServeHTTP(httptest.NewRecorder(), unauth)
	env.srv.audit.wait()
	if entries := env.entries(t); len(entries) != 0 {
		t.Errorf("want no audit entries, got %+v", entries)
	}
}

func TestAuditRedactsSensitiveQueryValues(t *testing.T) {
	env := newAuditEnv(t)
	env.do(http.MethodPost, "/api/users/reset-password?id=1&password=hunter2&api_token=abc", `{}`)
	entries := env.entries(t)
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(entries))
	}
	q, err := url.ParseQuery(entries[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("id") != "1" || q.Get("password") != "[redacted]" || q.Get("api_token") != "[redacted]" {
		t.Errorf("query not redacted: %q", entries[0].Query)
	}
	if strings.Contains(entries[0].Query, "hunter2") {
		t.Errorf("password leaked into audit query: %q", entries[0].Query)
	}
	if entries[0].Summary != "Reset password user 1" {
		t.Errorf("summary = %q", entries[0].Summary)
	}
}

func TestAuditListEndpoint(t *testing.T) {
	env := newAuditEnv(t)
	env.do(http.MethodPut, "/api/theme", `{"title":"a"}`)
	env.do(http.MethodDelete, "/api/clients?mac=aa:bb:cc:dd:ee:ff", "")

	req := httptest.NewRequest(http.MethodGet, "/api/audit?path=clients&limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+env.admin)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/audit: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, "Deleted client aa:bb:cc:dd:ee:ff") || strings.Contains(body, "/api/theme") {
		t.Errorf("unexpected audit listing: %s", body)
	}
	if env.entries(t)[0].Path == "/api/audit" {
		t.Errorf("listing the audit log must not itself be audited")
	}
}

func TestAuditSummary(t *testing.T) {
	cases := []struct {
		method, path, query, want string
	}{
		{http.MethodPost, "/api/clients", "", "Created client"},
		{http.MethodPost, "/api/clients/wake", "mac=aa:bb", "Wake client aa:bb"},
		{http.MethodDelete, "/api/images", "filename=x.iso", "Deleted image x.iso"},
		{http.MethodPost, "/api/client-groups/next-boot", "id=3", "Next boot client group 3"},
		{http.MethodPut, "/api/profiles/update", "", "Update profile"},
	}
	for _, c := range cases {
		values, _ := url.ParseQuery(c.query)
		if got := auditSummary(c.method, c.path, values); got != c.want {
			t.Errorf("%s %s?%s = %q, want %q", c.method, c.path, c.query, got, c.want)
		}
	}
}
