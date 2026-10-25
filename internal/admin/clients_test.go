package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

func TestCreateClientStatus(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateClient(&models.Client{MACAddress: "aa:bb:cc:dd:ee:20"}); err != nil {
		t.Fatal(err)
	}
	unmigrated, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		store     storage.Storage
		body      string
		wantCode  int
		wantError string
	}{
		{"new MAC", store, `{"mac_address":"aa:bb:cc:dd:ee:21"}`, http.StatusCreated, ""},
		{"existing MAC", store, `{"mac_address":"AA-BB-CC-DD-EE-20"}`, http.StatusConflict, "A client with this MAC address already exists"},
		{"missing MAC", store, `{"name":"no mac"}`, http.StatusBadRequest, "MAC address is required"},
		{"blank MAC", store, `{"mac_address":"  "}`, http.StatusBadRequest, "MAC address is required"},
		{"storage failure", unmigrated, `{"mac_address":"aa:bb:cc:dd:ee:22"}`, http.StatusInternalServerError, "Failed to create client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{storage: tt.store}
			req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.CreateClient(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			var resp Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if tt.wantError != "" && resp.Error != tt.wantError {
				t.Errorf("error = %q, want %q", resp.Error, tt.wantError)
			}
		})
	}
}
