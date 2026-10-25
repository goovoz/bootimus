package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bootimus/internal/models"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter() (*loginLimiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter()
	l.now = clock.now
	return l, clock
}

func TestLimiterLocksUserAfterThreshold(t *testing.T) {
	l, clock := newTestLimiter()
	for i := 0; i < userFailureThreshold-1; i++ {
		l.fail("alice", "10.0.0.1")
		if _, locked := l.check("alice", "10.0.0.1"); locked {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	l.fail("alice", "10.0.0.1")
	wait, locked := l.check("alice", "10.0.0.1")
	if !locked || wait != initialLockout {
		t.Fatalf("want lock of %s, got locked=%v wait=%s", initialLockout, locked, wait)
	}
	if _, locked := l.check("bob", "10.0.0.1"); locked {
		t.Fatal("other user on the same address should not be locked")
	}
	clock.advance(initialLockout)
	if _, locked := l.check("alice", "10.0.0.1"); locked {
		t.Fatal("lock should expire")
	}
}

func TestLimiterEscalatesAndCaps(t *testing.T) {
	l, clock := newTestLimiter()
	for i := 0; i < userFailureThreshold; i++ {
		l.fail("alice", "10.0.0.1")
	}
	want := initialLockout
	for i := 0; i < 8; i++ {
		wait, locked := l.check("alice", "10.0.0.1")
		if !locked || wait != want {
			t.Fatalf("round %d: want lock %s, got locked=%v wait=%s", i, want, locked, wait)
		}
		clock.advance(wait)
		l.fail("alice", "10.0.0.1")
		want *= 2
		if want > maxLockout {
			want = maxLockout
		}
	}
	if wait, _ := l.check("alice", "10.0.0.1"); wait != maxLockout {
		t.Fatalf("want cap of %s, got %s", maxLockout, wait)
	}
}

func TestLimiterSuccessClearsUser(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < userFailureThreshold; i++ {
		l.fail("alice", "10.0.0.1")
	}
	l.succeed("alice")
	if _, locked := l.check("alice", "10.0.0.1"); locked {
		t.Fatal("successful login should clear the lock")
	}
	for i := 0; i < userFailureThreshold-1; i++ {
		l.fail("alice", "10.0.0.1")
	}
	if _, locked := l.check("alice", "10.0.0.1"); locked {
		t.Fatal("failure count should restart after success")
	}
}

func TestLimiterAddressThresholdIsIndependent(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < addrFailureThreshold-1; i++ {
		l.fail("user"+string(rune('a'+i)), "10.0.0.9")
	}
	if _, locked := l.check("someone", "10.0.0.9"); locked {
		t.Fatal("address locked before its threshold")
	}
	l.fail("another", "10.0.0.9")
	if _, locked := l.check("someone", "10.0.0.9"); !locked {
		t.Fatal("address should be locked after its threshold")
	}
	if _, locked := l.check("someone", "10.0.0.10"); locked {
		t.Fatal("other address should not be locked")
	}
}

func TestLimiterFailuresExpireWithWindow(t *testing.T) {
	l, clock := newTestLimiter()
	for i := 0; i < userFailureThreshold-1; i++ {
		l.fail("alice", "10.0.0.1")
	}
	clock.advance(lockoutWindow + time.Second)
	l.fail("alice", "10.0.0.1")
	if _, locked := l.check("alice", "10.0.0.1"); locked {
		t.Fatal("old failures should not count towards the lock")
	}
	if _, ok := l.users["bob"]; ok {
		t.Fatal("unexpected record")
	}
}

func postLogin(m *Manager, username, password, addr string) *httptest.ResponseRecorder {
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	m.HandleLogin(rec, req)
	return rec
}

func TestHandleLoginLocksOutAndRecovers(t *testing.T) {
	user := &models.User{Username: "alice", Enabled: true, Source: models.UserSourceLocal}
	if err := user.SetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(map[string]*models.User{"alice": user})
	l, clock := newTestLimiter()
	m.loginLimit = l

	for i := 0; i < userFailureThreshold; i++ {
		if rec := postLogin(m, "Alice", "wrong", "192.0.2.5:4567"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d", i+1, rec.Code)
		}
	}

	rec := postLogin(m, "alice", "correct horse", "192.0.2.5:4567")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 while locked, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("want Retry-After 60, got %q", rec.Header().Get("Retry-After"))
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if msg, _ := resp["error"].(string); !strings.Contains(msg, "Too many failed login attempts") {
		t.Fatalf("unexpected error message %q", msg)
	}

	clock.advance(initialLockout)
	rec = postLogin(m, "alice", "correct horse", "192.0.2.5:4567")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 after lock expiry, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, locked := l.check("alice", "192.0.2.5"); locked {
		t.Fatal("successful login should clear the user record")
	}
}

func TestHandleLoginIgnoresMalformedBodies(t *testing.T) {
	m, _ := newTestManager(map[string]*models.User{})
	l, _ := newTestLimiter()
	m.loginLimit = l
	for i := 0; i < userFailureThreshold*2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("{not json"))
		req.RemoteAddr = "192.0.2.7:1000"
		rec := httptest.NewRecorder()
		m.HandleLogin(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rec.Code)
		}
	}
	if len(l.addrs) != 0 || len(l.users) != 0 {
		t.Fatal("malformed bodies must not be counted")
	}
}
