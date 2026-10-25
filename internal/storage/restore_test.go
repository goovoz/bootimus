package storage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name     string
	body     string
	typeflag byte
}

func buildArchive(t *testing.T, entries []archiveEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o600, Typeflag: flag}
		if flag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if flag == tar.TypeSymlink {
			hdr.Linkname = e.body
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestStageRestoreAcceptsSQLiteBackup(t *testing.T) {
	dataDir := t.TempDir()
	archive := buildArchive(t, []archiveEntry{
		{name: "bootimus.db", body: "new-db"},
		{name: "bootloaders/", typeflag: tar.TypeDir},
		{name: "bootloaders/custom/ipxe.efi", body: "efi"},
	})
	summary, err := StageRestore(dataDir, archive)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Database != "sqlite" || summary.FileCount != 2 {
		t.Fatalf("unexpected summary %+v", summary)
	}
	pending, err := PendingRestore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Database != "sqlite" {
		t.Fatalf("pending summary %+v", pending)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".restore-pending", "bootloaders", "custom", "ipxe.efi")); err != nil {
		t.Fatal(err)
	}
}

func TestStageRestoreRejectsBadArchives(t *testing.T) {
	cases := map[string]struct {
		entries []archiveEntry
		want    string
	}{
		"no database": {
			entries: []archiveEntry{{name: "bootloaders/x", body: "x"}},
			want:    "does not contain bootimus.db",
		},
		"traversal": {
			entries: []archiveEntry{{name: "bootimus.db", body: "db"}, {name: "../etc/passwd", body: "x"}},
			want:    "unsafe path",
		},
		"absolute": {
			entries: []archiveEntry{{name: "/bootimus.db", body: "db"}},
			want:    "absolute path",
		},
		"symlink": {
			entries: []archiveEntry{{name: "bootimus.db", body: "db"}, {name: "link", body: "/etc", typeflag: tar.TypeSymlink}},
			want:    "unsupported entry",
		},
		"nested database only": {
			entries: []archiveEntry{{name: "sub/bootimus.db", body: "db"}},
			want:    "does not contain bootimus.db",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			_, err := StageRestore(dataDir, buildArchive(t, tc.entries))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if _, err := PendingRestore(dataDir); !errors.Is(err, ErrNoPendingRestore) {
				t.Fatalf("rejected archive left a pending restore: %v", err)
			}
			entries, _ := os.ReadDir(dataDir)
			if len(entries) != 0 {
				t.Fatalf("rejected archive left files behind: %v", entries)
			}
		})
	}
}

func TestStageRestoreRejectsNonGzip(t *testing.T) {
	_, err := StageRestore(t.TempDir(), strings.NewReader("not an archive"))
	if err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("want gzip error, got %v", err)
	}
}

func TestApplyPendingRestoreSQLite(t *testing.T) {
	dataDir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("bootimus.db", "old-db")
	mustWrite("bootimus.db-wal", "wal")
	mustWrite("bootimus.db.pre-restore", "ancient")
	mustWrite("files/keep.txt", "keep")
	mustWrite("files/replace.txt", "old")

	archive := buildArchive(t, []archiveEntry{
		{name: "./bootimus.db", body: "new-db"},
		{name: "files/replace.txt", body: "new"},
		{name: "bootloaders/custom/ipxe.efi", body: "efi"},
	})
	if _, err := StageRestore(dataDir, archive); err != nil {
		t.Fatal(err)
	}

	summary, err := ApplyPendingRestore(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Database != "sqlite" {
		t.Fatalf("summary %+v", summary)
	}
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(dataDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if got := read("bootimus.db"); got != "new-db" {
		t.Fatalf("database not replaced: %q", got)
	}
	if got := read("bootimus.db.pre-restore"); got != "old-db" {
		t.Fatalf("previous database not kept: %q", got)
	}
	if got := read("files/replace.txt"); got != "new" {
		t.Fatalf("file not replaced: %q", got)
	}
	if got := read("files/keep.txt"); got != "keep" {
		t.Fatalf("unrelated file touched: %q", got)
	}
	if got := read("bootloaders/custom/ipxe.efi"); got != "efi" {
		t.Fatalf("new file missing: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "bootimus.db-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wal file not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".restore-pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory not removed: %v", err)
	}
	if _, err := ApplyPendingRestore(dataDir, nil); !errors.Is(err, ErrNoPendingRestore) {
		t.Fatalf("second apply: %v", err)
	}
}

func TestApplyPendingRestoreRefusesMismatchedDatabase(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := StageRestore(dataDir, buildArchive(t, []archiveEntry{{name: "bootimus.sql", body: "select 1;"}})); err != nil {
		t.Fatal(err)
	}
	_, err := ApplyPendingRestore(dataDir, nil)
	if err == nil || !strings.Contains(err.Error(), "PostgreSQL dump") {
		t.Fatalf("want mismatch error, got %v", err)
	}
	if _, err := PendingRestore(dataDir); err != nil {
		t.Fatalf("failed apply should leave the staging directory: %v", err)
	}
	if err := CancelRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := PendingRestore(dataDir); !errors.Is(err, ErrNoPendingRestore) {
		t.Fatalf("cancel left a pending restore: %v", err)
	}
}
