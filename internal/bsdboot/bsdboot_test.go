package bsdboot

import (
	"strings"
	"testing"
)

func TestOpenBSDBootConfAppendsExtraLines(t *testing.T) {
	conf := BootConfig(DistroOpenBSD, "bsd/install77/bsd.rd", "stty com0 115200; set tty com0")
	want := "set image bsd/install77/bsd.rd\nstty com0 115200\nset tty com0\n"
	if conf != want {
		t.Fatalf("boot.conf = %q, want %q", conf, want)
	}
}

func TestNetBSDBootCfgUsesTFTPPaths(t *testing.T) {
	cfg := BootConfig(DistroNetBSD, "bsd/netbsd/netbsd-INSTALL.gz", "consdev=com0")
	for _, want := range []string{
		"menu=Install NetBSD:boot tftp:bsd/netbsd/netbsd-INSTALL.gz\n",
		"menu=Drop to boot prompt:prompt\n",
		"timeout=10\n",
		"default=1\n",
		"consdev=com0\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("boot.cfg missing %q:\n%s", want, cfg)
		}
	}
}

func TestLoaderAndKernelNames(t *testing.T) {
	cases := []struct{ distro, source, want string }{
		{DistroOpenBSD, "/7.7/amd64/bootx64.efi", "BOOTX64.EFI"},
		{DistroOpenBSD, "/7.7/amd64/PXEBOOT", "pxeboot"},
		{DistroNetBSD, "/EFI/BOOT/BOOTX64.EFI", "bootx64.efi"},
	}
	for _, c := range cases {
		if got := LoaderName(c.distro, c.source); got != c.want {
			t.Errorf("LoaderName(%s, %s) = %q, want %q", c.distro, c.source, got, c.want)
		}
	}
	if got := KernelName(DistroNetBSD, "/netbsd"); got != "netbsd" {
		t.Errorf("plain NetBSD kernel name = %q", got)
	}
	if got := KernelName(DistroNetBSD, "/amd64/binary/kernel/netbsd-INSTALL.gz"); got != "netbsd-INSTALL.gz" {
		t.Errorf("compressed NetBSD kernel name = %q", got)
	}
	if got := EFILoader(DistroOpenBSD, "arm64"); got != "BOOTAA64.EFI" {
		t.Errorf("arm64 loader = %q", got)
	}
}

func TestRootFileTargets(t *testing.T) {
	if got := RootFileTarget(DistroOpenBSD, "bsd"); got != "bsd.rd" {
		t.Errorf("bsd maps to %q", got)
	}
	if got := RootFileTarget(DistroNetBSD, "netbsd"); got != "netbsd-INSTALL.gz" {
		t.Errorf("netbsd maps to %q", got)
	}
	if got := RootFileTarget(DistroNetBSD, "EFI/NetBSD/boot.cfg"); got != "boot.cfg" {
		t.Errorf("EFI boot.cfg maps to %q", got)
	}
	if !IsConfig(DistroNetBSD, "EFI/NetBSD/boot.cfg") || !IsConfig(DistroOpenBSD, "etc/boot.conf") || IsConfig(DistroOpenBSD, "bsd.rd") {
		t.Error("IsConfig misclassifies boot configuration names")
	}
}
