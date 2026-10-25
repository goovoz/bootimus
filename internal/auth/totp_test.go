package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bootimus/internal/models"

	"github.com/pquerna/otp/totp"
)

func newTOTPManager(t *testing.T) (*Manager, *fakeUserStore) {
	t.Helper()
	alice := &models.User{Username: "alice", Enabled: true, IsAdmin: true, Source: models.UserSourceLocal}
	if err := alice.SetPassword("alice-password"); err != nil {
		t.Fatal(err)
	}
	store := &fakeUserStore{users: map[string]*models.User{"alice": alice}}
	return &Manager{userStore: store, jwtSecret: []byte("test-secret-0123456789")}, store
}

func postJSON(t *testing.T, h http.HandlerFunc, path, bearer string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.RemoteAddr = "198.51.100.7:4000"
	rec := httptest.NewRecorder()
	h(rec, req)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func enrol(t *testing.T, m *Manager, store *fakeUserStore, jwt string) []string {
	t.Helper()
	code, resp := postJSON(t, m.HandleTOTPSetup, "/api/me/totp/setup", jwt, nil)
	if code != http.StatusOK {
		t.Fatalf("setup: %d %+v", code, resp)
	}
	data := resp["data"].(map[string]interface{})
	if !strings.HasPrefix(data["qr_code"].(string), "data:image/png;base64,") || !strings.HasPrefix(data["otpauth_url"].(string), "otpauth://totp/Bootimus:alice") {
		t.Fatalf("setup payload: %+v", data)
	}
	if store.users["alice"].TOTPEnabled {
		t.Fatal("setup must not enable TOTP before confirmation")
	}
	if code, resp := postJSON(t, m.HandleTOTPEnable, "/api/me/totp/enable", jwt, map[string]string{"code": "000000"}); code != http.StatusBadRequest {
		t.Fatalf("enable with a wrong code: want 400, got %d %+v", code, resp)
	}
	valid, err := totp.GenerateCode(store.users["alice"].TOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, resp = postJSON(t, m.HandleTOTPEnable, "/api/me/totp/enable", jwt, map[string]string{"code": valid})
	if code != http.StatusOK {
		t.Fatalf("enable: %d %+v", code, resp)
	}
	raw := resp["data"].(map[string]interface{})["recovery_codes"].([]interface{})
	codes := make([]string, 0, len(raw))
	for _, c := range raw {
		codes = append(codes, c.(string))
	}
	if len(codes) != recoveryCodeCount || !store.users["alice"].TOTPEnabled {
		t.Fatalf("enable result: %d codes, enabled=%v", len(codes), store.users["alice"].TOTPEnabled)
	}
	return codes
}

func TestTOTPEnrolmentAndLogin(t *testing.T) {
	m, store := newTOTPManager(t)
	jwt, _ := m.GenerateToken("alice", true)
	codes := enrol(t, m, store, jwt)

	login := func(body map[string]string) (int, map[string]interface{}) {
		return postJSON(t, m.HandleLogin, "/api/login", "", body)
	}
	code, resp := login(map[string]string{"username": "alice", "password": "alice-password"})
	if code != http.StatusUnauthorized || resp["totp_required"] != true || resp["error"] != "totp_required" {
		t.Fatalf("password only: want totp_required, got %d %+v", code, resp)
	}
	if code, resp := login(map[string]string{"username": "alice", "password": "alice-password", "code": "123456"}); code != http.StatusUnauthorized || resp["error"] != "Invalid two-factor code" {
		t.Fatalf("wrong code: %d %+v", code, resp)
	}
	if code, _ := login(map[string]string{"username": "alice", "password": "wrong", "code": "123456"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password with a code: want 401, got %d", code)
	}
	valid, _ := totp.GenerateCode(store.users["alice"].TOTPSecret, time.Now())
	if code, resp := login(map[string]string{"username": "alice", "password": "alice-password", "code": valid}); code != http.StatusOK || resp["success"] != true {
		t.Fatalf("valid code: %d %+v", code, resp)
	}
	if code, resp := login(map[string]string{"username": "alice", "password": "alice-password", "code": codes[0]}); code != http.StatusOK {
		t.Fatalf("recovery code: %d %+v", code, resp)
	}
	if code, _ := login(map[string]string{"username": "alice", "password": "alice-password", "code": codes[0]}); code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code: want 401, got %d", code)
	}
	if got := RemainingRecoveryCodes(store.users["alice"]); got != recoveryCodeCount-1 {
		t.Fatalf("remaining recovery codes = %d", got)
	}
	if code, _ := login(map[string]string{"username": "alice", "password": "alice-password", "code": strings.ToUpper(codes[1])}); code != http.StatusOK {
		t.Fatalf("recovery codes should be case-insensitive")
	}
}

func TestTOTPWrongCodesCountTowardsLockout(t *testing.T) {
	m, store := newTOTPManager(t)
	jwt, _ := m.GenerateToken("alice", true)
	enrol(t, m, store, jwt)
	for i := 0; i < userFailureThreshold; i++ {
		postJSON(t, m.HandleLogin, "/api/login", "", map[string]string{"username": "alice", "password": "alice-password", "code": "000000"})
	}
	valid, _ := totp.GenerateCode(store.users["alice"].TOTPSecret, time.Now())
	if code, _ := postJSON(t, m.HandleLogin, "/api/login", "", map[string]string{"username": "alice", "password": "alice-password", "code": valid}); code != http.StatusTooManyRequests {
		t.Fatalf("after repeated bad codes: want 429, got %d", code)
	}
}

func TestTOTPDisableAndGuards(t *testing.T) {
	m, store := newTOTPManager(t)
	jwt, _ := m.GenerateToken("alice", true)
	enrol(t, m, store, jwt)

	if code, _ := postJSON(t, m.HandleTOTPSetup, "/api/me/totp/setup", jwt, nil); code != http.StatusBadRequest {
		t.Fatalf("setup while enabled: want 400, got %d", code)
	}
	if code, _ := postJSON(t, m.HandleTOTPDisable, "/api/me/totp/disable", jwt, map[string]string{"password": "nope"}); code != http.StatusForbidden {
		t.Fatalf("disable with wrong password: want 403, got %d", code)
	}
	if code, _ := postJSON(t, m.HandleTOTPDisable, "/api/me/totp/disable", jwt, map[string]string{"password": "alice-password"}); code != http.StatusOK {
		t.Fatalf("disable: want 200, got %d", code)
	}
	u := store.users["alice"]
	if u.TOTPEnabled || u.TOTPSecret != "" || u.RecoveryCodes != "" {
		t.Fatalf("disable left state behind: %+v", u)
	}
	if code, _ := postJSON(t, m.HandleLogin, "/api/login", "", map[string]string{"username": "alice", "password": "alice-password"}); code != http.StatusOK {
		t.Fatalf("login after disable: want 200, got %d", code)
	}

	bob := &models.User{Username: "bob", Enabled: true, Source: models.UserSourceLDAP}
	store.users["bob"] = bob
	bobJWT, _ := m.GenerateToken("bob", false)
	if code, _ := postJSON(t, m.HandleTOTPSetup, "/api/me/totp/setup", bobJWT, nil); code != http.StatusBadRequest {
		t.Fatalf("LDAP user setup: want 400, got %d", code)
	}
	if code, _ := postJSON(t, m.HandleTOTPSetup, "/api/me/totp/setup", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous setup: want 401, got %d", code)
	}
}

func TestClearTOTPResetsEverything(t *testing.T) {
	u := &models.User{TOTPEnabled: true, TOTPSecret: "x", RecoveryCodes: `["a"]`}
	ClearTOTP(u)
	if u.TOTPEnabled || u.TOTPSecret != "" || u.RecoveryCodes != "" {
		t.Fatalf("%+v", u)
	}
	if ok, _ := VerifyTOTP(u, "123456"); ok {
		t.Fatal("verification must fail once cleared")
	}
}
