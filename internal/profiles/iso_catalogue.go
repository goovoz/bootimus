package profiles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"bootimus/internal/remotefetch"
)

type ISOCatalogue struct {
	Version string     `json:"version"`
	Distros []ISOEntry `json:"distros"`
}

type ISOEntry struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Mirrors  []ISOMirror  `json:"mirrors"`
	Releases []ISORelease `json:"releases"`
}

type ISOMirror struct {
	Region string `json:"region"`
	Base   string `json:"base"`
}

type ISORelease struct {
	Label        string `json:"label"`
	Path         string `json:"path"`
	SizeHint     string `json:"size_hint,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	ChecksumPath string `json:"checksum_path,omitempty"`
}

func LoadISOCatalogue() (*ISOCatalogue, error) {
	data, err := embeddedProfiles.ReadFile("distro-profiles.json")
	if err != nil {
		return nil, fmt.Errorf("read distro-profiles: %w", err)
	}
	return ParseISOCatalogue(data)
}

func FetchRemoteISOCatalogue(ctx context.Context) (*ISOCatalogue, error) {
	data, _, err := remotefetch.Get(ctx, &http.Client{Timeout: 30 * time.Second}, RemoteProfilesURLs)
	if err != nil {
		return nil, fmt.Errorf("fetch remote catalogue: %w", err)
	}
	return ParseISOCatalogue(data)
}

func ParseISOCatalogue(data []byte) (*ISOCatalogue, error) {
	var pf ProfileFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse distro-profiles: %w", err)
	}
	var distros []ISOEntry
	for _, p := range pf.Profiles {
		if len(p.Releases) == 0 {
			continue
		}
		distros = append(distros, ISOEntry{
			ID:       p.ID,
			Name:     p.DisplayName,
			Mirrors:  p.Mirrors,
			Releases: p.Releases,
		})
	}
	return &ISOCatalogue{Version: pf.Version, Distros: distros}, nil
}
