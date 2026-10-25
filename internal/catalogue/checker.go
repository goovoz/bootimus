package catalogue

import (
	"context"
	"log"
	"sync"
	"time"

	"bootimus/internal/housekeeping"
	"bootimus/internal/models"
	"bootimus/internal/profiles"
	"bootimus/internal/tools"
)

const TaskName = "catalogue_check"

type Store interface {
	ListImages() ([]*models.Image, error)
	UpdateImage(filename string, image *models.Image) error
	ListBootTools() ([]*models.BootTool, error)
	SaveBootTool(tool *models.BootTool) error
}

type ImageUpdate struct {
	Filename string `json:"filename"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	URL      string `json:"url"`
}

type ToolUpdateInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	URL         string `json:"url"`
}

type Report struct {
	CheckedAt        time.Time        `json:"checked_at"`
	CatalogueVersion string           `json:"catalogue_version"`
	Remote           bool             `json:"remote"`
	Images           []ImageUpdate    `json:"images"`
	Tools            []ToolUpdateInfo `json:"tools"`
	Error            string           `json:"error,omitempty"`
}

type Checker struct {
	store         Store
	disableRemote bool
	loader        func(ctx context.Context) (*profiles.ISOCatalogue, bool)
	mu            sync.Mutex
	last          *Report
}

func NewChecker(store Store, disableRemote bool) *Checker {
	c := &Checker{store: store, disableRemote: disableRemote}
	c.loader = c.loadCatalogue
	return c
}

func (c *Checker) RemoteDisabled() bool {
	return c.disableRemote
}

func (c *Checker) LastReport() *Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *Checker) Name() string {
	return TaskName
}

func (c *Checker) Run(ctx context.Context, settings *models.HousekeepingSettings) (int64, error) {
	if settings != nil && settings.CatalogueCheckOff {
		return 0, housekeeping.ErrDisabled
	}
	report, err := c.Check(ctx)
	if err != nil {
		return 0, err
	}
	return int64(len(report.Images) + len(report.Tools)), nil
}

func (c *Checker) loadCatalogue(ctx context.Context) (*profiles.ISOCatalogue, bool) {
	if !c.disableRemote {
		if cat, err := profiles.FetchRemoteISOCatalogue(ctx); err == nil {
			return cat, true
		} else {
			log.Printf("Catalogue: remote fetch failed, using embedded catalogue: %v", err)
		}
	}
	cat, err := profiles.LoadISOCatalogue()
	if err != nil {
		return &profiles.ISOCatalogue{}, false
	}
	return cat, false
}

func (c *Checker) Check(ctx context.Context) (*Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cat, remote := c.loader(ctx)
	report := &Report{CheckedAt: time.Now(), CatalogueVersion: cat.Version, Remote: remote, Images: []ImageUpdate{}, Tools: []ToolUpdateInfo{}}

	images, err := c.store.ListImages()
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		update, found := FindUpdate(img, cat)
		changed := false
		if found {
			if !img.UpdateAvailable || img.UpdateURL != update.URL || img.UpdateVersion != update.Version {
				img.UpdateAvailable = true
				img.UpdateVersion = update.Version
				img.UpdateFilename = update.Filename
				img.UpdateURL = update.URL
				changed = true
			}
			report.Images = append(report.Images, ImageUpdate{Filename: img.Filename, Name: img.Name, Version: update.Version, URL: update.URL})
		} else if img.UpdateAvailable {
			img.UpdateAvailable = false
			img.UpdateVersion = ""
			img.UpdateFilename = ""
			img.UpdateURL = ""
			changed = true
		}
		if changed {
			if err := c.store.UpdateImage(img.Filename, img); err != nil {
				log.Printf("Catalogue: failed to record update state for %s: %v", img.Filename, err)
			}
		}
	}

	toolList, err := c.store.ListBootTools()
	if err != nil {
		return nil, err
	}
	for _, tool := range toolList {
		url, found := ToolUpdate(tool, tools.GetDefinition(tool.Name))
		changed := false
		if found {
			if !tool.UpdateAvailable || tool.UpdateURL != url {
				tool.UpdateAvailable = true
				tool.UpdateURL = url
				changed = true
			}
			report.Tools = append(report.Tools, ToolUpdateInfo{Name: tool.Name, DisplayName: tool.DisplayName, URL: url})
		} else if tool.UpdateAvailable {
			tool.UpdateAvailable = false
			tool.UpdateURL = ""
			changed = true
		}
		if changed {
			if err := c.store.SaveBootTool(tool); err != nil {
				log.Printf("Catalogue: failed to record update state for tool %s: %v", tool.Name, err)
			}
		}
	}
	c.last = report
	return report, nil
}
