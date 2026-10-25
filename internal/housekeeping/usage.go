package housekeeping

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const usageCacheTTL = 2 * time.Minute

type EntryUsage struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

type Usage struct {
	DataDir    string       `json:"data_dir"`
	TotalBytes int64        `json:"total_bytes"`
	Entries    []EntryUsage `json:"entries"`
	MeasuredAt time.Time    `json:"measured_at"`
}

var usageCache struct {
	mu      sync.Mutex
	entries map[string]*Usage
}

func DataDirUsage(dataDir string) (*Usage, error) {
	usageCache.mu.Lock()
	defer usageCache.mu.Unlock()
	if cached, ok := usageCache.entries[dataDir]; ok && time.Since(cached.MeasuredAt) < usageCacheTTL {
		return cached, nil
	}
	usage, err := measureDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	if usageCache.entries == nil {
		usageCache.entries = make(map[string]*Usage)
	}
	usageCache.entries[dataDir] = usage
	return usage, nil
}

func InvalidateUsage() {
	usageCache.mu.Lock()
	usageCache.entries = nil
	usageCache.mu.Unlock()
}

func measureDataDir(dataDir string) (*Usage, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	usage := &Usage{DataDir: dataDir, MeasuredAt: time.Now()}
	for _, entry := range entries {
		item := EntryUsage{Name: entry.Name(), Dir: entry.IsDir()}
		full := filepath.Join(dataDir, entry.Name())
		if entry.IsDir() {
			filepath.WalkDir(full, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil || d.IsDir() {
					return nil
				}
				if info, infoErr := d.Info(); infoErr == nil {
					item.Bytes += info.Size()
					item.Files++
				}
				return nil
			})
		} else if info, infoErr := entry.Info(); infoErr == nil {
			item.Bytes = info.Size()
			item.Files = 1
		}
		usage.TotalBytes += item.Bytes
		usage.Entries = append(usage.Entries, item)
	}
	sort.Slice(usage.Entries, func(i, j int) bool {
		if usage.Entries[i].Bytes != usage.Entries[j].Bytes {
			return usage.Entries[i].Bytes > usage.Entries[j].Bytes
		}
		return usage.Entries[i].Name < usage.Entries[j].Name
	})
	return usage, nil
}
