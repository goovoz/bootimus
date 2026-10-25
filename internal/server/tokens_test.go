package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/models"
)

func createToken(t *testing.T, env *auditEnv, bearer, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	env.srv.audit.wait()
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func doAs(env *auditEnv, method, path, bearer string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	env.srv.audit.wait()
	return rec.Code
}

func TestAPITokenLifecycleThroughRoutes(t *testing.T) {
	env := newAuditEnv(t)

	code, resp := createToken(t, env, env.admin, `{"name":"ci","scope":"read"}`)
	if code != http.StatusCreated {
		t.Fatalf("create read token: %d %+v", code, resp)
	}
	data := resp["data"].(map[string]interface{})
	readSecret := data["secret"].(string)
	if !strings.HasPrefix(readSecret, "btk_") {
		t.Fatalf("secret %q lacks the btk_ prefix", readSecret)
	}
	if _, present := data["token"].(map[string]interface{})["hash"]; present {
		t.Fatal("token hash must never be returned")
	}

	code, resp = createToken(t, env, env.admin, `{"name":"deploy","scope":"admin","expires_in_days":30}`)
	if code != http.StatusCreated {
		t.Fatalf("create admin token: %d %+v", code, resp)
	}
	adminSecret := resp["data"].(map[string]interface{})["secret"].(string)
	adminID := resp["data"].(map[string]interface{})["token"].(map[string]interface{})["id"].(float64)

	if code := doAs(env, http.MethodGet, "/api/clients", readSecret); code != http.StatusOK {
		t.Errorf("read token GET /api/clients: %d", code)
	}
	if code := doAs(env, http.MethodGet, "/api/users", readSecret); code != http.StatusForbidden {
		t.Errorf("read token GET /api/users: want 403, got %d", code)
	}
	if code := doAs(env, http.MethodPut, "/api/theme", readSecret); code != http.StatusForbidden {
		t.Errorf("read token PUT /api/theme: want 403, got %d", code)
	}
	if code := doAs(env, http.MethodPut, "/api/theme", adminSecret); code != http.StatusOK {
		t.Errorf("admin token PUT /api/theme: want 200, got %d", code)
	}
	if code, _ := createToken(t, env, adminSecret, `{"name":"escalate","scope":"admin"}`); code != http.StatusForbidden {
		t.Errorf("admin token creating a token: want 403, got %d", code)
	}

	entries := env.entries(t)
	var seen bool
	for _, e := range entries {
		if e.Path == "/api/theme" && e.Username == "token:deploy" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("audit log should attribute the change to token:deploy, got %+v", entries)
	}

	if code := doAs(env, http.MethodDelete, "/api/tokens?id="+strings.TrimSuffix(strings.TrimSuffix(jsonNumber(adminID), ".0"), ".000000"), env.admin); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if code := doAs(env, http.MethodPut, "/api/theme", adminSecret); code != http.StatusUnauthorized {
		t.Errorf("revoked token: want 401, got %d", code)
	}
	tokens, err := env.store.ListAPITokens()
	if err != nil || len(tokens) != 2 {
		t.Fatalf("ListAPITokens: %v %d", err, len(tokens))
	}
	for _, tok := range tokens {
		if tok.Name == "deploy" && (!tok.Revoked || tok.ExpiresAt == nil || tok.LastUsedAt == nil) {
			t.Errorf("revoked admin token state wrong: %+v", tok)
		}
		if tok.Scope != models.APITokenScopeRead && tok.Scope != models.APITokenScopeAdmin {
			t.Errorf("bad scope %+v", tok)
		}
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
