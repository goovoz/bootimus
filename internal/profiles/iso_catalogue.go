package profiles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RemoteProfilesURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch remote catalogue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote catalogue returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read remote catalogue: %w", err)
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
