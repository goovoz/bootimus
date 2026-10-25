package autoinstall

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestLibraryWriteReadListDelete(t *testing.T) {
	lib := newTestLibrary(t)

	if err := lib.Write("ubuntu", "default.yaml", "hostname: {{HOSTNAME}}\n"); err != nil {
		t.Fatal(err)
	}
	if err := lib.Write("windows", "kiosk.xml", "<unattend/>"); err != nil {
		t.Fatal(err)
	}

	content, err := lib.Read("ubuntu", "default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if content != "hostname: {{HOSTNAME}}\n" {
		t.Errorf("Read = %q", content)
	}

	files, err := lib.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{Distro: "ubuntu", Filename: "default.yaml", Path: "ubuntu/default.yaml", Type: "autoinstall", Size: 23},
		{Distro: "windows", Filename: "kiosk.xml", Path: "windows/kiosk.xml", Type: "autounattend", Size: 11},
	}
	if len(files) != len(want) {
		t.Fatalf("List returned %d files, want %d", len(files), len(want))
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("List[%d] = %+v, want %+v", i, files[i], want[i])
		}
	}

	if err := lib.Delete("ubuntu", "default.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Read("ubuntu", "default.yaml"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read after Delete error = %v, want ErrNotFound", err)
	}
	if err := lib.Delete("ubuntu", "default.yaml"); err != nil {
		t.Errorf("Delete of a missing file = %v, want nil", err)
	}
}

func TestLibraryReadPath(t *testing.T) {
	lib := newTestLibrary(t)
	if err := lib.Write("debian", "server.cfg", "d-i\n"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		rel     string
		want    string
		wantErr error
	}{
		{"existing", "debian/server.cfg", "d-i\n", nil},
		{"surrounding whitespace", "  debian/server.cfg ", "d-i\n", nil},
		{"empty", "", "", ErrNotFound},
		{"missing distro segment", "server.cfg", "", ErrInvalidName},
		{"missing file", "debian/other.cfg", "", ErrNotFound},
		{"traversal", "../debian/server.cfg", "", ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lib.ReadPath(tt.rel)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLibraryRejectsTraversal(t *testing.T) {
	lib := newTestLibrary(t)
	outside := filepath.Join(filepath.Dir(lib.Root()), "escaped.txt")

	tests := []struct {
		name     string
		distro   string
		filename string
	}{
		{"dot dot distro", "..", "escaped.txt"},
		{"dot distro", ".", "escaped.txt"},
		{"slash in distro", "../x", "escaped.txt"},
		{"slash in filename", "ubuntu", "../escaped.txt"},
		{"backslash in filename", "ubuntu", `..\escaped.txt`},
		{"empty distro", "", "escaped.txt"},
		{"empty filename", "ubuntu", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := lib.Write(tt.distro, tt.filename, "x"); !errors.Is(err, ErrInvalidName) {
				t.Errorf("Write error = %v, want ErrInvalidName", err)
			}
			if _, err := lib.Read(tt.distro, tt.filename); !errors.Is(err, ErrInvalidName) {
				t.Errorf("Read error = %v, want ErrInvalidName", err)
			}
			if err := lib.Delete(tt.distro, tt.filename); !errors.Is(err, ErrInvalidName) {
				t.Errorf("Delete error = %v, want ErrInvalidName", err)
			}
		})
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("file escaped the library root: %v", err)
	}
}
