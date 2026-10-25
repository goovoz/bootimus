package imagescan

import (
	"os"
	"path/filepath"
	"testing"

	"bootimus/internal/models"
	"bootimus/internal/uki/ukitest"
)

func TestScanFindsISOsAndUKIs(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	write("ubuntu.iso", "iso")
	write("lab/Rocky.ISO", "iso")
	ukitest.WriteUKI(t, filepath.Join(root, "hello-world.efi"), "audit=0")
	os.MkdirAll(filepath.Join(root, "lab"), 0755)
	ukitest.WriteUKI(t, filepath.Join(root, "lab", "fedora-uki.efi"), "")
	ukitest.Write(t, write("grub.efi", ""), ukitest.Section{Name: ".text", Data: []byte("code")})
	write("ubuntu/iso/EFI/BOOT/BOOTX64.EFI", "shim")
	os.MkdirAll(filepath.Join(root, "ubuntu", "iso", "EFI", "Linux"), 0755)
	ukitest.WriteUKI(t, filepath.Join(root, "ubuntu", "iso", "EFI", "Linux", "inside.efi"), "quiet")
	write("notes.txt", "x")

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]models.SyncFile{}
	for _, f := range files {
		got[f.Filename] = f
	}

	want := map[string]models.SyncFile{
		"ubuntu.iso":                           {Name: "ubuntu", Filename: "ubuntu.iso"},
		filepath.Join("lab", "Rocky.ISO"):      {Name: "Rocky", Filename: filepath.Join("lab", "Rocky.ISO"), GroupPath: "lab"},
		"hello-world.efi":                      {Name: "hello-world", Filename: "hello-world.efi", BootMethod: BootMethodUKI, BootParams: "audit=0"},
		filepath.Join("lab", "fedora-uki.efi"): {Name: "fedora-uki", Filename: filepath.Join("lab", "fedora-uki.efi"), GroupPath: "lab", BootMethod: BootMethodUKI},
	}
	if len(got) != len(want) {
		t.Errorf("found %d files, want %d: %+v", len(got), len(want), files)
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		g.Size = 0
		if g != w {
			t.Errorf("%s = %+v, want %+v", name, g, w)
		}
	}
}

func TestIsSupportedFilename(t *testing.T) {
	for name, want := range map[string]bool{
		"ubuntu.iso": true, "UBUNTU.ISO": true, "hello.efi": true, "HELLO.EFI": true,
		"readme.txt": false, "image.img": false,
	} {
		if got := IsSupportedFilename(name); got != want {
			t.Errorf("IsSupportedFilename(%q) = %v, want %v", name, got, want)
		}
	}
}
