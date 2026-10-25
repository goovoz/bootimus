package extractor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bootimus/internal/bsdboot"
)

var openBSDVersionDir = regexp.MustCompile(`^\d+\.\d+$`)

var openBSDArchPreference = []string{"amd64", "arm64", "i386"}

var openBSDLoaderNames = []string{
	bsdboot.OpenBSDPXEBoot,
	bsdboot.OpenBSDEFIX64,
	bsdboot.OpenBSDEFIIA32,
	bsdboot.OpenBSDEFIARM64,
}

var netBSDKernelCandidates = []string{
	"/amd64/binary/kernel/netbsd-INSTALL.gz",
	"/i386/binary/kernel/netbsd-INSTALL.gz",
	"/amd64/binary/kernel/netbsd-INSTALL",
	"/i386/binary/kernel/netbsd-INSTALL",
	"/netbsd",
}

var netBSDLoaderCandidates = []string{
	"/amd64/installation/misc/pxeboot_ia32.bin",
	"/i386/installation/misc/pxeboot_ia32.bin",
	"/amd64/installation/misc/pxeboot_ia32_com0.bin",
	"/i386/installation/misc/pxeboot_ia32_com0.bin",
	"/amd64/installation/misc/bootx64.efi",
	"/amd64/installation/misc/bootia32.efi",
	"/EFI/boot/bootx64.efi",
	"/EFI/BOOT/BOOTX64.EFI",
	"/EFI/boot/bootia32.efi",
	"/EFI/BOOT/BOOTIA32.EFI",
}

func (e *Extractor) detectOpenBSDUnified(reader FileSystemReader) (*BootFiles, error) {
	rootEntries, err := reader.ListDirectory("/")
	if err != nil {
		return nil, fmt.Errorf("not OpenBSD")
	}
	for _, entry := range rootEntries {
		if !entry.IsDir || !openBSDVersionDir.MatchString(entry.Name) {
			continue
		}
		archEntries, err := reader.ListDirectory("/" + entry.Name)
		if err != nil {
			continue
		}
		for _, arch := range openBSDArchPreference {
			if !containsDir(archEntries, arch) {
				continue
			}
			archDir := "/" + entry.Name + "/" + arch
			kernel := archDir + "/" + bsdboot.OpenBSDKernel
			if !reader.FileExists(kernel) {
				continue
			}
			files := &BootFiles{
				Kernel: kernel,
				Distro: bsdboot.DistroOpenBSD,
			}
			entries, _ := reader.ListDirectory(archDir)
			for _, name := range openBSDLoaderNames {
				if actual := entryNamed(entries, name); actual != "" {
					files.Loaders = append(files.Loaders, archDir+"/"+actual)
				}
			}
			if len(files.Loaders) == 0 {
				return nil, fmt.Errorf("OpenBSD layout found but no pxeboot or EFI loader in %s", archDir)
			}
			return files, nil
		}
	}
	return nil, fmt.Errorf("not OpenBSD")
}

func (e *Extractor) detectNetBSDUnified(reader FileSystemReader) (*BootFiles, error) {
	kernel := ""
	for _, candidate := range netBSDKernelCandidates {
		if reader.FileExists(candidate) {
			kernel = candidate
			break
		}
	}
	if kernel == "" {
		return nil, fmt.Errorf("not NetBSD")
	}
	if kernel == "/netbsd" && !reader.FileExists("/boot.cfg") && !reader.FileExists("/amd64/binary/sets") && !reader.FileExists("/i386/binary/sets") {
		return nil, fmt.Errorf("not NetBSD")
	}
	files := &BootFiles{
		Kernel: kernel,
		Distro: bsdboot.DistroNetBSD,
	}
	seen := map[string]bool{}
	for _, candidate := range netBSDLoaderCandidates {
		if !reader.FileExists(candidate) {
			continue
		}
		name := bsdboot.LoaderName(bsdboot.DistroNetBSD, candidate)
		if seen[name] {
			continue
		}
		seen[name] = true
		files.Loaders = append(files.Loaders, candidate)
	}
	if len(files.Loaders) == 0 {
		return nil, fmt.Errorf("NetBSD layout found but no pxeboot_ia32.bin or EFI loader on the ISO")
	}
	return files, nil
}

func containsDir(entries []DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.IsDir && entry.Name == name {
			return true
		}
	}
	return false
}

func entryNamed(entries []DirEntry, name string) string {
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		if entry.Name == name {
			return entry.Name
		}
	}
	for _, entry := range entries {
		if !entry.IsDir && strings.EqualFold(entry.Name, name) {
			return entry.Name
		}
	}
	return ""
}

func (e *Extractor) cacheBSDFiles(files *BootFiles, reader FileSystemReader, bootFilesDir string) error {
	bsdDir := filepath.Join(bootFilesDir, bsdboot.CacheSubdir)
	if err := os.MkdirAll(filepath.Join(bsdDir, "etc"), 0755); err != nil {
		return fmt.Errorf("failed to create BSD boot directory: %w", err)
	}

	kernelName := bsdboot.KernelName(files.Distro, files.Kernel)
	kernelDest := filepath.Join(bsdDir, kernelName)
	if err := reader.ExtractFile(files.Kernel, kernelDest); err != nil {
		return fmt.Errorf("failed to extract %s kernel: %w", bsdboot.DisplayName(files.Distro), err)
	}
	files.Kernel = kernelDest
	files.Initrd = ""

	var copied []string
	for _, loader := range files.Loaders {
		name := bsdboot.LoaderName(files.Distro, loader)
		dest := filepath.Join(bsdDir, name)
		if err := reader.ExtractFile(loader, dest); err != nil {
			log.Printf("Warning: failed to extract %s loader %s: %v", bsdboot.DisplayName(files.Distro), loader, err)
			continue
		}
		copied = append(copied, dest)
	}
	files.Loaders = copied

	configPath := filepath.Join(bsdDir, filepath.FromSlash(bsdboot.ConfigName(files.Distro)))
	cacheRel := filepath.ToSlash(filepath.Base(bootFilesDir))
	if rel, err := filepath.Rel(e.dataDir, bootFilesDir); err == nil && !strings.HasPrefix(rel, "..") {
		cacheRel = filepath.ToSlash(rel)
	}
	config := bsdboot.BootConfig(files.Distro, bsdboot.TFTPPrefix+cacheRel+"/"+kernelName, files.BootParams)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write %s boot configuration: %w", bsdboot.DisplayName(files.Distro), err)
	}

	extractedDir := filepath.Join(bootFilesDir, "iso")
	if err := os.MkdirAll(extractedDir, 0755); err != nil {
		return fmt.Errorf("failed to create extracted ISO directory: %w", err)
	}
	log.Printf("Extracting full %s ISO contents to %s", bsdboot.DisplayName(files.Distro), extractedDir)
	if err := reader.ExtractAll(extractedDir); err != nil {
		return fmt.Errorf("failed to extract full ISO contents: %w", err)
	}
	files.ExtractedDir = extractedDir
	return nil
}
