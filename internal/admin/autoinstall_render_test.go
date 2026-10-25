package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"bootimus/internal/autoinstall"
	"bootimus/internal/models"
	"bootimus/internal/storage"
)

func newAutoInstallHandler(t *testing.T) (*Handler, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	lib, err := autoinstall.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{storage: store, autoInstallLib: lib, serverAddr: "10.0.0.1", httpPort: 8080}, store
}

type renderResult struct {
	Content    string   `json:"content"`
	Unresolved []string `json:"unresolved"`
}

func TestRenderAutoInstallFile(t *testing.T) {
	h, store := newAutoInstallHandler(t)
	group := &models.ClientGroup{Name: "lab-3", Enabled: true, Variables: models.StringMap{"role": "worker", "rack": "3"}}
	if err := store.CreateClientGroup(group); err != nil {
		t.Fatal(err)
	}
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:30", Name: "bench-1", Enabled: true, ClientGroupID: &group.ID, Variables: models.StringMap{"role": "control-plane"}}
	if err := store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImage(&models.Image{Filename: "ubuntu-24.04.iso", Name: "Ubuntu 24.04", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	content := "host={{HOSTNAME}} group={{CLIENT_GROUP}} role={{VAR.role}} rack={{VAR.rack}} iso={{IMAGE_URL}} server={{SERVER_URL}} cb={{CALLBACK_URL}} {{MISSING}}"
	if err := h.autoInstallLib.Write("ubuntu", "default.yaml", content); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		query          string
		wantCode       int
		wantContent    string
		wantUnresolved []string
		wantError      string
	}{
		{
			name:           "no client or image",
			query:          "distro=ubuntu&filename=default.yaml",
			wantCode:       http.StatusOK,
			wantContent:    "host= group= role={{VAR.role}} rack={{VAR.rack}} iso={{IMAGE_URL}} server=http://10.0.0.1:8080 cb=http://10.0.0.1:8080/install/callback?token=preview {{MISSING}}",
			wantUnresolved: []string{"VAR.role", "VAR.rack", "IMAGE_URL", "MISSING"},
		},
		{
			name:           "client with group and image",
			query:          "distro=ubuntu&filename=default.yaml&mac=AA-BB-CC-DD-EE-30&image=ubuntu-24.04.iso",
			wantCode:       http.StatusOK,
			wantContent:    "host=bench-1 group=lab-3 role=control-plane rack=3 iso=http://10.0.0.1:8080/isos/ubuntu-24.04.iso server=http://10.0.0.1:8080 cb=http://10.0.0.1:8080/install/callback?token=preview {{MISSING}}",
			wantUnresolved: []string{"MISSING"},
		},
		{
			name:      "unknown client",
			query:     "distro=ubuntu&filename=default.yaml&mac=aa:bb:cc:dd:ee:99",
			wantCode:  http.StatusNotFound,
			wantError: "Client not found",
		},
		{
			name:      "unknown image",
			query:     "distro=ubuntu&filename=default.yaml&image=nope.iso",
			wantCode:  http.StatusNotFound,
			wantError: "Image not found",
		},
		{
			name:      "unknown file",
			query:     "distro=ubuntu&filename=other.yaml",
			wantCode:  http.StatusNotFound,
			wantError: "File not found",
		},
		{
			name:     "traversal rejected",
			query:    "distro=..&filename=default.yaml",
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/autoinstall-files/render?"+tt.query, nil)
			rec := httptest.NewRecorder()
			h.RenderAutoInstallFile(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			var resp struct {
				Success bool         `json:"success"`
				Error   string       `json:"error"`
				Data    renderResult `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tt.wantError != "" && resp.Error != tt.wantError {
				t.Errorf("error = %q, want %q", resp.Error, tt.wantError)
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			if resp.Data.Content != tt.wantContent {
				t.Errorf("content = %q, want %q", resp.Data.Content, tt.wantContent)
			}
			if !reflect.DeepEqual(resp.Data.Unresolved, tt.wantUnresolved) {
				t.Errorf("unresolved = %v, want %v", resp.Data.Unresolved, tt.wantUnresolved)
			}
		})
	}
}

func TestRenderAutoInstallFileReturnsEmptyUnresolvedList(t *testing.T) {
	h, _ := newAutoInstallHandler(t)
	if err := h.autoInstallLib.Write("ubuntu", "plain.yaml", "server: {{SERVER_ADDR}}\n"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/autoinstall-files/render?distro=ubuntu&filename=plain.yaml", nil)
	rec := httptest.NewRecorder()
	h.RenderAutoInstallFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unresolved":[]`) {
		t.Errorf("unresolved should be an empty array, body: %s", rec.Body.String())
	}
}

func TestSaveAutoInstallFileValidation(t *testing.T) {
	h, _ := newAutoInstallHandler(t)

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantFlag bool
		wantSave bool
	}{
		{"valid yaml", `{"distro":"ubuntu","filename":"ok.yaml","content":"hostname: {{HOSTNAME}}\n"}`, http.StatusOK, false, true},
		{"invalid yaml", `{"distro":"ubuntu","filename":"bad.yaml","content":"a:\n  b: 1\n c: 2\n"}`, http.StatusBadRequest, true, false},
		{"invalid yaml forced", `{"distro":"ubuntu","filename":"forced.yaml","content":"a:\n  b: 1\n c: 2\n","force":true}`, http.StatusOK, false, true},
		{"invalid xml", `{"distro":"windows","filename":"bad.xml","content":"<a><b></a>"}`, http.StatusBadRequest, true, false},
		{"unchecked extension", `{"distro":"debian","filename":"any.cfg","content":"{{ ::"}`, http.StatusOK, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/autoinstall-files/save", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.SaveAutoInstallFile(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			var resp struct {
				Error string          `json:"error"`
				Data  map[string]bool `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Data["validation_failed"] != tt.wantFlag {
				t.Errorf("validation_failed = %v, want %v", resp.Data["validation_failed"], tt.wantFlag)
			}
			if tt.wantFlag && !strings.HasPrefix(resp.Error, "invalid ") {
				t.Errorf("error = %q, want a validation message", resp.Error)
			}
			var req2 struct {
				Distro, Filename string
			}
			json.Unmarshal([]byte(tt.body), &req2)
			_, err := h.autoInstallLib.Read(req2.Distro, req2.Filename)
			if (err == nil) != tt.wantSave {
				t.Errorf("file saved = %v, want %v (err %v)", err == nil, tt.wantSave, err)
			}
		})
	}
}

func TestClientVariablesViaAPI(t *testing.T) {
	h, store := newAutoInstallHandler(t)
	mac := "aa:bb:cc:dd:ee:31"

	create := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"mac_address":"`+mac+`","variables":{"role":"worker"}}`))
	rec := httptest.NewRecorder()
	h.CreateClient(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d (body %s)", rec.Code, rec.Body.String())
	}

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantVars models.StringMap
	}{
		{"replace variables", `{"variables":{"role":"control-plane","rack":"7"}}`, http.StatusOK, models.StringMap{"role": "control-plane", "rack": "7"}},
		{"untouched when absent", `{"name":"bench-2"}`, http.StatusOK, models.StringMap{"role": "control-plane", "rack": "7"}},
		{"invalid name rejected", `{"variables":{"bad name":"x"}}`, http.StatusBadRequest, models.StringMap{"role": "control-plane", "rack": "7"}},
		{"non-string value rejected", `{"variables":{"rack":7}}`, http.StatusBadRequest, models.StringMap{"role": "control-plane", "rack": "7"}},
		{"null clears", `{"variables":null}`, http.StatusOK, models.StringMap{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/clients?mac="+mac, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.UpdateClient(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			stored, err := store.GetClient(mac)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored.Variables, tt.wantVars) {
				t.Errorf("stored variables = %v, want %v", stored.Variables, tt.wantVars)
			}
		})
	}

	badCreate := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"mac_address":"aa:bb:cc:dd:ee:32","variables":{"a.b":"x"}}`))
	rec = httptest.NewRecorder()
	h.CreateClient(rec, badCreate)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with invalid variable name status = %d, want 400", rec.Code)
	}
}

func TestClientGroupVariablesViaAPI(t *testing.T) {
	h, store := newAutoInstallHandler(t)

	rec := httptest.NewRecorder()
	h.CreateClientGroup(rec, httptest.NewRequest(http.MethodPost, "/api/client-groups", strings.NewReader(`{"name":"lab","variables":{"role":"worker"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d (body %s)", rec.Code, rec.Body.String())
	}
	group, err := store.GetClientGroupByName("lab")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(group.Variables, models.StringMap{"role": "worker"}) {
		t.Errorf("created variables = %v", group.Variables)
	}

	rec = httptest.NewRecorder()
	h.UpdateClientGroup(rec, httptest.NewRequest(http.MethodPut, "/api/client-groups/update?id="+itoa(group.ID), strings.NewReader(`{"name":"lab","variables":{"rack":"3"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d (body %s)", rec.Code, rec.Body.String())
	}
	group, _ = store.GetClientGroup(group.ID)
	if !reflect.DeepEqual(group.Variables, models.StringMap{"rack": "3"}) {
		t.Errorf("updated variables = %v", group.Variables)
	}

	rec = httptest.NewRecorder()
	h.UpdateClientGroup(rec, httptest.NewRequest(http.MethodPut, "/api/client-groups/update?id="+itoa(group.ID), strings.NewReader(`{"name":"lab","variables":{"not valid":"x"}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("update with invalid variable name status = %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.CreateClientGroup(rec, httptest.NewRequest(http.MethodPost, "/api/client-groups", strings.NewReader(`{"name":"lab2","variables":{"a-b":"x"}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with invalid variable name status = %d, want 400", rec.Code)
	}
}

func itoa(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
