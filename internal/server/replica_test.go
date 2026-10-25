package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bootimus/internal/auth"
	"bootimus/internal/models"
	"bootimus/internal/replica"
	"bootimus/internal/storage"
)

func newReplicaEnv(t *testing.T) (*accessEnv, *Server) {
	t.Helper()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"success":false,"error":"not found"}`))
	}))
	t.Cleanup(primary.Close)

	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr, err := auth.NewManager(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	viewer := &models.User{Username: "viewer", Enabled: true, Source: models.UserSourceLocal}
	if err := viewer.SetPassword("viewer-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(viewer); err != nil {
		t.Fatal(err)
	}
	cfg := &replica.Config{PrimaryURL: primary.URL, Token: "btk_test", Interval: time.Minute, SyncISOs: false}
	srv := &Server{config: &Config{Storage: store, Auth: mgr, ServerAddr: "10.0.0.1", HTTPPort: 8080, DataDir: t.TempDir(), Replica: cfg}}
	mux := http.NewServeMux()
	srv.setupAdminInterface(mux)
	t.Cleanup(func() {
		if srv.replica != nil {
			srv.replica.Stop()
		}
	})
	adminTok, _ := mgr.GenerateToken("admin", true)
	viewerTok, _ := mgr.GenerateToken("viewer", false)
	return &accessEnv{handler: mux, store: store, admin: adminTok, viewer: viewerTok}, srv
}

func TestReplicaRefusesChangesExceptAllowedRoutes(t *testing.T) {
	env, _ := newReplicaEnv(t)
	for _, route := range []string{"/api/clients", "/api/images/extract", "/api/theme", "/api/san-targets"} {
		code, body := env.do(http.MethodPost, route, env.admin)
		if code != http.StatusConflict || !strings.Contains(body, "replica of") {
			t.Errorf("POST %s on a replica: want 409 with a replica message, got %d %s", route, code, body)
		}
	}
	if code, _ := env.do(http.MethodGet, "/api/clients", env.admin); code != http.StatusOK {
		t.Errorf("GET /api/clients on a replica: want 200, got %d", code)
	}
	if code, _ := env.do(http.MethodPost, "/api/replica/sync", env.admin); code == http.StatusConflict {
		t.Error("POST /api/replica/sync must not be blocked by the replica guard")
	}
	if code, _ := env.do(http.MethodPost, "/api/tokens", env.admin); code == http.StatusConflict {
		t.Error("POST /api/tokens must stay available on a replica")
	}
	if code, _ := env.do(http.MethodPost, "/api/clients", env.viewer); code != http.StatusForbidden {
		t.Errorf("viewer POST on a replica: want 403 from auth, got %d", code)
	}
}

func TestReplicaStatusAndMe(t *testing.T) {
	env, srv := newReplicaEnv(t)
	if srv.replica == nil {
		t.Fatal("replica syncer not started")
	}
	code, body := env.do(http.MethodGet, "/api/replica/status", env.viewer)
	if code != http.StatusOK {
		t.Fatalf("status: %d %s", code, body)
	}
	var resp struct {
		Data struct {
			Enabled bool           `json:"enabled"`
			Status  replica.Status `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Data.Enabled || resp.Data.Status.PrimaryURL == "" {
		t.Fatalf("unexpected status payload: %s", body)
	}

	code, body = env.do(http.MethodGet, "/api/me", env.admin)
	if code != http.StatusOK || !strings.Contains(body, `"replica"`) || !strings.Contains(body, `"primary_url"`) {
		t.Fatalf("/api/me should carry replica info: %d %s", code, body)
	}
}

func TestNoReplicaStatusWhenNotConfigured(t *testing.T) {
	env := newAccessEnv(t)
	code, body := env.do(http.MethodGet, "/api/replica/status", env.admin)
	if code != http.StatusOK || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("status without replica config: %d %s", code, body)
	}
	if code, _ := env.do(http.MethodPost, "/api/clients", env.admin); code == http.StatusConflict {
		t.Fatal("the replica guard must be inert when replica mode is off")
	}
}

func TestMenuUsesPrimaryForRemoteImages(t *testing.T) {
	mb := &MenuBuilder{serverAddr: "10.0.0.2", httpPort: 8080, remoteBase: "http://10.0.0.1:8080", remoteImages: map[uint]bool{5: true}}
	if got := mb.assetBase(&models.Image{ID: 5}); got != "http://10.0.0.1:8080" {
		t.Fatalf("remote image base = %q", got)
	}
	if got := mb.assetBase(&models.Image{ID: 6}); got != "http://10.0.0.2:8080" {
		t.Fatalf("local image base = %q", got)
	}
}
