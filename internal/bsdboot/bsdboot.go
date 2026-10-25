package bsdboot

import (
	"path"
	"strconv"
	"strings"
)

const (
	DistroOpenBSD = "openbsd"
	DistroNetBSD  = "netbsd"

	CacheSubdir = "bsd"
	TFTPPrefix  = "bsd/"

	OpenBSDKernel     = "bsd.rd"
	OpenBSDBootConf   = "etc/boot.conf"
	OpenBSDRandomSeed = "etc/random.seed"
	OpenBSDPXEBoot    = "pxeboot"
	OpenBSDEFIX64     = "BOOTX64.EFI"
	OpenBSDEFIIA32    = "BOOTIA32.EFI"
	OpenBSDEFIARM64   = "BOOTAA64.EFI"

	NetBSDKernel       = "netbsd-INSTALL.gz"
	NetBSDKernelPlain  = "netbsd"
	NetBSDBootCfg      = "boot.cfg"
	NetBSDPXEBoot      = "pxeboot_ia32.bin"
	NetBSDPXEBootCom0  = "pxeboot_ia32_com0.bin"
	NetBSDEFIX64       = "bootx64.efi"
	NetBSDEFIIA32      = "bootia32.efi"
	NetBSDEFIBootCfg   = "EFI/NetBSD/boot.cfg"
	randomSeedBytes    = 512
	bootCfgMenuTimeout = 10
)

func IsBSD(distro string) bool {
	return distro == DistroOpenBSD || distro == DistroNetBSD
}

func DisplayName(distro string) string {
	switch distro {
	case DistroOpenBSD:
		return "OpenBSD"
	case DistroNetBSD:
		return "NetBSD"
	}
	return distro
}

func KernelName(distro string, source string) string {
	switch distro {
	case DistroOpenBSD:
		return OpenBSDKernel
	case DistroNetBSD:
		if strings.HasSuffix(strings.ToLower(source), ".gz") {
			return NetBSDKernel
		}
		return NetBSDKernelPlain
	}
	return path.Base(source)
}

func LoaderName(distro string, source string) string {
	base := path.Base(source)
	switch distro {
	case DistroOpenBSD:
		if strings.HasSuffix(strings.ToLower(base), ".efi") {
			return strings.ToUpper(base)
		}
		return strings.ToLower(base)
	case DistroNetBSD:
		return strings.ToLower(base)
	}
	return base
}

func BIOSLoader(distro string) string {
	switch distro {
	case DistroOpenBSD:
		return OpenBSDPXEBoot
	case DistroNetBSD:
		return NetBSDPXEBoot
	}
	return ""
}

func EFILoader(distro, buildarch string) string {
	switch distro {
	case DistroOpenBSD:
		switch buildarch {
		case "arm64":
			return OpenBSDEFIARM64
		case "i386":
			return OpenBSDEFIIA32
		}
		return OpenBSDEFIX64
	case DistroNetBSD:
		if buildarch == "i386" {
			return NetBSDEFIIA32
		}
		return NetBSDEFIX64
	}
	return ""
}

func ConfigName(distro string) string {
	switch distro {
	case DistroOpenBSD:
		return OpenBSDBootConf
	case DistroNetBSD:
		return NetBSDBootCfg
	}
	return ""
}

func RootFiles(distro string) []string {
	switch distro {
	case DistroOpenBSD:
		return []string{OpenBSDBootConf, OpenBSDRandomSeed, OpenBSDKernel, "bsd"}
	case DistroNetBSD:
		return []string{NetBSDBootCfg, NetBSDEFIBootCfg, NetBSDKernel, NetBSDKernelPlain, "netbsd-INSTALL"}
	}
	return nil
}

func RootFileTarget(distro, name string) string {
	switch distro {
	case DistroOpenBSD:
		if name == "bsd" {
			return OpenBSDKernel
		}
	case DistroNetBSD:
		switch name {
		case NetBSDKernelPlain, "netbsd-INSTALL":
			return NetBSDKernel
		case NetBSDEFIBootCfg:
			return NetBSDBootCfg
		}
	}
	return name
}

func IsConfig(distro, name string) bool {
	return name == ConfigName(distro) || (distro == DistroNetBSD && name == NetBSDEFIBootCfg)
}

func ExtraLines(bootParams string) []string {
	var lines []string
	for _, chunk := range strings.FieldsFunc(bootParams, func(r rune) bool { return r == ';' || r == '\n' }) {
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			lines = append(lines, chunk)
		}
	}
	return lines
}

func BootConfig(distro, kernelTFTPPath, bootParams string) string {
	switch distro {
	case DistroOpenBSD:
		return openBSDBootConf(kernelTFTPPath, ExtraLines(bootParams))
	case DistroNetBSD:
		return netBSDBootCfg(kernelTFTPPath, ExtraLines(bootParams))
	}
	return ""
}

func openBSDBootConf(kernel string, extra []string) string {
	var sb strings.Builder
	sb.WriteString("set image " + kernel + "\n")
	for _, line := range extra {
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func netBSDBootCfg(kernel string, extra []string) string {
	var sb strings.Builder
	sb.WriteString("banner=NetBSD network install served by Bootimus\n")
	sb.WriteString("banner=\n")
	sb.WriteString("banner=If the loader reports it cannot open boot.cfg or the kernel over NFS,\n")
	sb.WriteString("banner=type: boot tftp:" + kernel + "\n")
	sb.WriteString("menu=Install NetBSD:boot tftp:" + kernel + "\n")
	sb.WriteString("menu=Install NetBSD (serial console com0):consdev com0;boot tftp:" + kernel + "\n")
	sb.WriteString("menu=Drop to boot prompt:prompt\n")
	sb.WriteString("timeout=" + strconv.Itoa(bootCfgMenuTimeout) + "\n")
	sb.WriteString("default=1\n")
	for _, line := range extra {
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func RandomSeedSize() int {
	return randomSeedBytes
}
