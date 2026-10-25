package uki

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"bootimus/internal/uki/ukitest"
)

func TestInspectReadsCmdline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hello-world.efi")
	ukitest.WriteUKI(t, path, "audit=0  console=ttyS0\n")

	img, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if img.Cmdline != "audit=0 console=ttyS0" {
		t.Errorf("Cmdline = %q, want %q", img.Cmdline, "audit=0 console=ttyS0")
	}
}

func TestInspectWithoutCmdline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bare.efi")
	ukitest.WriteUKI(t, path, "")

	img, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if img.Cmdline != "" {
		t.Errorf("Cmdline = %q, want empty", img.Cmdline)
	}
}

func TestInspectRejectsOtherEFIApplications(t *testing.T) {
	dir := t.TempDir()

	shim := filepath.Join(dir, "BOOTX64.EFI")
	ukitest.Write(t, shim, ukitest.Section{Name: ".text", Data: []byte("code")})
	if _, err := Inspect(shim); !errors.Is(err, ErrNotUKI) {
		t.Errorf("Inspect(shim) error = %v, want ErrNotUKI", err)
	}

	junk := filepath.Join(dir, "junk.efi")
	if err := os.WriteFile(junk, []byte("not a PE file"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsUKI(junk) {
		t.Error("IsUKI(junk) = true, want false")
	}
}
