package storage

import (
	"testing"
	"time"

	"bootimus/internal/models"
)

func newAuditTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestAuditLogListFilters(t *testing.T) {
	store := newAuditTestStore(t)
	entries := []models.AuditLog{
		{Username: "alice", Method: "PUT", Path: "/api/clients", Query: "mac=aa", Status: 200, Summary: "Updated client aa"},
		{Username: "bob", Method: "DELETE", Path: "/api/images", Status: 200, Summary: "Deleted image"},
		{Username: "alice", Method: "POST", Path: "/api/users", Status: 400, Summary: "Created user"},
	}
	for i := range entries {
		if err := store.CreateAuditLog(&entries[i]); err != nil {
			t.Fatal(err)
		}
	}

	got, total, err := store.ListAuditLogs(AuditLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("want 3 entries, got %d (total %d)", len(got), total)
	}
	if got[0].Path != "/api/users" {
		t.Errorf("newest entry first, got %s", got[0].Path)
	}

	got, total, err = store.ListAuditLogs(AuditLogFilter{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("username filter: want 2, got %d (total %d)", len(got), total)
	}

	got, total, err = store.ListAuditLogs(AuditLogFilter{Path: "images"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "bob" {
		t.Errorf("path filter: want bob's entry, got %+v", got)
	}

	got, total, err = store.ListAuditLogs(AuditLogFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 1 || got[0].Path != "/api/images" {
		t.Errorf("pagination: want the middle entry, got %+v (total %d)", got, total)
	}

	got, _, err = store.ListAuditLogs(AuditLogFilter{Since: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("since filter in the future should return nothing, got %d", len(got))
	}
}

func TestDeleteAuditLogsBefore(t *testing.T) {
	store := newAuditTestStore(t)
	old := &models.AuditLog{Username: "alice", Method: "PUT", Path: "/api/clients", Status: 200}
	if err := store.CreateAuditLog(old); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(old).Update("created_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAuditLog(&models.AuditLog{Username: "bob", Method: "POST", Path: "/api/users", Status: 200}); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteAuditLogsBefore(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("want 1 deleted, got %d", deleted)
	}
	got, total, err := store.ListAuditLogs(AuditLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "bob" {
		t.Errorf("want only bob's entry left, got %+v", got)
	}
}
