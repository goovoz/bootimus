package imagescan

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"bootimus/internal/models"
	"bootimus/internal/uki"
)

const BootMethodUKI = "uki"

func Scan(root string) ([]models.SyncFile, error) {
	var files []models.SyncFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		var bootMethod, bootParams string
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".iso":
		case ".efi":
			if insideExtractionDir(root, path) {
				return nil
			}
			img, err := uki.Inspect(path)
			if err != nil {
				return nil
			}
			bootMethod, bootParams = BootMethodUKI, img.Cmdline
		default:
			return nil
		}

		info, err := d.Info()
		if err != nil {
			log.Printf("Warning: failed to get info for %s: %v", path, err)
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		groupPath := filepath.Dir(relPath)
		if groupPath == "." {
			groupPath = ""
		}

		files = append(files, models.SyncFile{
			Name:       strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
			Filename:   relPath,
			Size:       info.Size(),
			GroupPath:  groupPath,
			BootMethod: bootMethod,
			BootParams: bootParams,
		})
		return nil
	})
	return files, err
}

func IsSupportedFilename(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".iso", ".efi":
		return true
	}
	return false
}

func insideExtractionDir(root, path string) bool {
	root = filepath.Clean(root)
	for dir := filepath.Dir(path); dir != root && strings.HasPrefix(dir, root+string(os.PathSeparator)); dir = filepath.Dir(dir) {
		for _, ext := range []string{".iso", ".ISO"} {
			if _, err := os.Stat(dir + ext); err == nil {
				return true
			}
		}
	}
	return false
}
