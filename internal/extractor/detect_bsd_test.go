package extractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractOpenBSDLayout(t *testing.T) {
	files := map[string]string{
		"/7.7/amd64/bsd.rd":       "openbsd-ramdisk-kernel",
		"/7.7/amd64/bsd":          "openbsd-kernel",
		"/7.7/amd64/pxeboot":      "openbsd-pxeboot",
		"/7.7/amd64/BOOTX64.EFI":  "openbsd-efi-x64",
		"/7.7/amd64/BOOTIA32.EFI": "openbsd-efi-ia32",
		"/7.7/amd64/base77.tgz":   "sets",
		"/etc/boot.conf":          "set image /7.7/amd64/bsd.rd\n",
	}

	bootFiles, dataDir := extractTestISO(t, "install77.iso", files)

	if bootFiles.Distro != "openbsd" {
		t.Fatalf("distro = %q, want openbsd", bootFiles.Distro)
	}
	if got := readFileString(t, bootFiles.Kernel); got != "openbsd-ramdisk-kernel" {
		t.Errorf("kernel content = %q", got)
	}
	if filepath.Base(bootFiles.Kernel) != "bsd.rd" {
		t.Errorf("kernel stored as %s, want bsd.rd", bootFiles.Kernel)
	}
	if bootFiles.Initrd != "" {
		t.Errorf("initrd = %q, want empty", bootFiles.Initrd)
	}
	bsdDir := filepath.Join(dataDir, "install77", "bsd")
	for name, want := range map[string]string{"pxeboot": "openbsd-pxeboot", "BOOTX64.EFI": "openbsd-efi-x64", "BOOTIA32.EFI": "openbsd-efi-ia32"} {
		if got := readFileString(t, filepath.Join(bsdDir, name)); got != want {
			t.Errorf("%s content = %q, want %q", name, got, want)
		}
	}
	if len(bootFiles.Loaders) != 3 {
		t.Errorf("loaders = %v, want three", bootFiles.Loaders)
	}
	conf := readFileString(t, filepath.Join(bsdDir, "etc", "boot.conf"))
	if !strings.Contains(conf, "set image bsd/install77/bsd.rd\n") {
		t.Errorf("boot.conf = %q", conf)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "install77", "iso", "7.7", "amd64", "base77.tgz")); err != nil {
		t.Errorf("full ISO tree should be extracted for the installer sets: %v", err)
	}
}

func TestExtractOpenBSDWithoutLoadersIsRejected(t *testing.T) {
	dataDir := t.TempDir()
	isoPath := filepath.Join(dataDir, "install77.iso")
	writeTestISO(t, isoPath, map[string]string{"/7.7/amd64/bsd.rd": "kernel"})
	ext, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ext.SetProgress(NewProgressReporter())
	if _, err := ext.Extract(isoPath); err == nil {
		t.Fatal("expected extraction to fail without pxeboot or an EFI loader")
	}
}

func TestExtractNetBSDLayout(t *testing.T) {
	files := map[string]string{
		"/amd64/binary/kernel/netbsd-INSTALL.gz":         "netbsd-install-kernel",
		"/amd64/binary/kernel/netbsd-GENERIC.gz":         "netbsd-generic-kernel",
		"/amd64/installation/misc/pxeboot_ia32.bin":      "netbsd-pxeboot",
		"/amd64/installation/misc/pxeboot_ia32_com0.bin": "netbsd-pxeboot-com0",
		"/EFI/boot/bootx64.efi":                          "netbsd-efi-x64",
		"/boot.cfg":                                      "menu=Install NetBSD:boot netbsd\n",
	}

	bootFiles, dataDir := extractTestISO(t, "NetBSD-10.1-amd64.iso", files)

	if bootFiles.Distro != "netbsd" {
		t.Fatalf("distro = %q, want netbsd", bootFiles.Distro)
	}
	if filepath.Base(bootFiles.Kernel) != "netbsd-INSTALL.gz" {
		t.Errorf("kernel stored as %s, want netbsd-INSTALL.gz", bootFiles.Kernel)
	}
	if got := readFileString(t, bootFiles.Kernel); got != "netbsd-install-kernel" {
		t.Errorf("kernel content = %q", got)
	}
	bsdDir := filepath.Join(dataDir, "NetBSD-10.1-amd64", "bsd")
	for name, want := range map[string]string{"pxeboot_ia32.bin": "netbsd-pxeboot", "pxeboot_ia32_com0.bin": "netbsd-pxeboot-com0", "bootx64.efi": "netbsd-efi-x64"} {
		if got := readFileString(t, filepath.Join(bsdDir, name)); got != want {
			t.Errorf("%s content = %q, want %q", name, got, want)
		}
	}
	cfg := readFileString(t, filepath.Join(bsdDir, "boot.cfg"))
	if !strings.Contains(cfg, "menu=Install NetBSD:boot tftp:bsd/NetBSD-10.1-amd64/netbsd-INSTALL.gz\n") {
		t.Errorf("boot.cfg = %q", cfg)
	}
}

func TestFreeBSDIsNotMistakenForOpenBSD(t *testing.T) {
	files := map[string]string{
		"/boot/kernel/kernel": "freebsd-kernel",
		"/boot/mfsroot.gz":    "freebsd-mfsroot",
	}
	bootFiles, _ := extractTestISO(t, "FreeBSD-14.2-RELEASE-amd64-disc1.iso", files)
	if bootFiles.Distro != "freebsd" {
		t.Fatalf("distro = %q, want freebsd", bootFiles.Distro)
	}
}
