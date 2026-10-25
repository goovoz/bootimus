package extractor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type WIMFile struct {
	Path string
	Size int64
}

func isWindowsDistro(distro string) bool {
	switch distro {
	case "windows", "windows7", "winpe":
		return true
	}
	return false
}

func findWIMFiles(reader FileSystemReader) []string {
	var found []string
	walkDir(reader, "/", 0, 4, func(path string, isDir bool) {
		if isDir {
			return
		}
		name := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
		if !strings.HasSuffix(name, ".wim") || name == "install.wim" {
			return
		}
		found = append(found, path)
	})
	sort.SliceStable(found, func(i, j int) bool {
		return wimPriority(found[i]) < wimPriority(found[j])
	})
	return found
}

func wimPriority(path string) int {
	lower := strings.ToLower(path)
	switch {
	case lower == "/sources/boot.wim":
		return 0
	case strings.Contains(lower, "boot"):
		return 1
	}
	return 2
}

func wimFilesWithSizes(extractedDir string, paths []string) []WIMFile {
	files := make([]WIMFile, 0, len(paths))
	for _, p := range paths {
		rel := strings.TrimPrefix(p, "/")
		entry := WIMFile{Path: rel}
		if info, err := os.Stat(filepath.Join(extractedDir, filepath.FromSlash(rel))); err == nil {
			entry.Size = info.Size()
		}
		files = append(files, entry)
	}
	return files
}

func (e *Extractor) cacheWindowsFiles(files *BootFiles, reader FileSystemReader, bootFilesDir string) error {
	extractedDir := filepath.Join(bootFilesDir, "iso")
	if err := os.MkdirAll(extractedDir, 0755); err != nil {
		return fmt.Errorf("failed to create extracted ISO directory: %w", err)
	}

	log.Printf("Extracting full Windows ISO contents to %s", extractedDir)
	if err := reader.ExtractAll(extractedDir); err != nil {
		return fmt.Errorf("failed to extract full ISO contents: %w", err)
	}

	files.ExtractedDir = extractedDir
	files.Kernel = filepath.Join(extractedDir, strings.TrimPrefix(files.Kernel, "/"))
	files.Initrd = filepath.Join(extractedDir, strings.TrimPrefix(files.Initrd, "/"))
	files.BootWim = filepath.Join(extractedDir, strings.TrimPrefix(files.BootWim, "/"))
	if files.InstallWim != "" {
		files.InstallWim = filepath.Join(extractedDir, strings.TrimPrefix(files.InstallWim, "/"))
	}

	var wimPaths []string
	for _, w := range files.WIMFiles {
		wimPaths = append(wimPaths, w.Path)
	}
	files.WIMFiles = wimFilesWithSizes(extractedDir, wimPaths)

	log.Printf("Windows ISO extraction complete (%s, %d WIM file(s))", files.Distro, len(files.WIMFiles))
	return nil
}
