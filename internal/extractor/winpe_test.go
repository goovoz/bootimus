package extractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractWinPERescueMediaLayout(t *testing.T) {
	files := map[string]string{
		"/bootmgr":                  "bootmgr",
		"/boot/bcd":                 "bcd",
		"/boot/boot.sdi":            "sdi",
		"/SSTR/strelec10x64Eng.wim": "pe-wim-english",
		"/SSTR/strelec10x64Rus.wim": "pe-wim-russian",
		"/efi/boot/bootx64.efi":     "efi",
	}
	bootFiles, dataDir := extractTestISO(t, "WinPE11_Sergei_Strelec_x64_2025.iso", files)

	if bootFiles.Distro != "winpe" {
		t.Fatalf("distro = %q, want winpe", bootFiles.Distro)
	}
	if len(bootFiles.WIMFiles) != 2 {
		t.Fatalf("WIM files = %+v, want 2", bootFiles.WIMFiles)
	}
	if !strings.EqualFold(bootFiles.WIMFiles[0].Path, "SSTR/strelec10x64Eng.wim") || bootFiles.WIMFiles[0].Size != int64(len("pe-wim-english")) {
		t.Fatalf("first WIM = %+v", bootFiles.WIMFiles[0])
	}
	if !strings.EqualFold(filepath.Base(bootFiles.BootWim), "strelec10x64Eng.wim") {
		t.Fatalf("boot WIM = %q", bootFiles.BootWim)
	}
	if _, err := os.Stat(bootFiles.BootWim); err != nil {
		t.Fatalf("boot WIM not extracted: %v", err)
	}
	if bootFiles.ExtractedDir != filepath.Join(dataDir, "WinPE11_Sergei_Strelec_x64_2025", "iso") {
		t.Fatalf("extracted dir = %q", bootFiles.ExtractedDir)
	}
}

func TestExtractWindowsInstallerStaysWindows(t *testing.T) {
	files := map[string]string{
		"/bootmgr":              "bootmgr",
		"/boot/bcd":             "bcd",
		"/boot/boot.sdi":        "sdi",
		"/sources/boot.wim":     "boot-wim",
		"/sources/install.wim":  "install-wim",
		"/boot/extra/other.wim": "other",
	}
	bootFiles, _ := extractTestISO(t, "Win11_25H2_English_x64.iso", files)

	if bootFiles.Distro != "windows" {
		t.Fatalf("distro = %q, want windows", bootFiles.Distro)
	}
	if filepath.ToSlash(bootFiles.BootWim) == "" || filepath.Base(bootFiles.BootWim) != "boot.wim" {
		t.Fatalf("boot WIM = %q", bootFiles.BootWim)
	}
	if len(bootFiles.WIMFiles) != 2 || !strings.EqualFold(bootFiles.WIMFiles[0].Path, "sources/boot.wim") {
		t.Fatalf("WIM files = %+v", bootFiles.WIMFiles)
	}
	if _, err := os.Stat(bootFiles.InstallWim); err != nil {
		t.Fatalf("install.wim not extracted: %v", err)
	}
}

func TestExtractBCDWithoutWIMIsNotWindows(t *testing.T) {
	dataDir := t.TempDir()
	isoPath := filepath.Join(dataDir, "odd.iso")
	writeTestISO(t, isoPath, map[string]string{
		"/boot/bcd":      "bcd",
		"/boot/boot.sdi": "sdi",
		"/readme.txt":    "nothing to boot",
	})
	ext, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ext.SetProgress(NewProgressReporter())
	if _, err := ext.Extract(isoPath); err == nil {
		t.Fatal("expected extraction to fail without a WIM or kernel")
	}
}
