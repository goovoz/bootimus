package catalogue

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"bootimus/internal/models"
	"bootimus/internal/profiles"
	"bootimus/internal/tools"
)

var digitRun = regexp.MustCompile(`\d+`)

type Update struct {
	Version  string
	Filename string
	URL      string
	Release  string
}

func VersionKey(filename string) string {
	return digitRun.ReplaceAllString(strings.ToLower(filename), "#")
}

func VersionOf(filename string) []int {
	var out []int
	for _, run := range digitRun.FindAllString(filename, -1) {
		n, err := strconv.Atoi(run)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func VersionLabel(filename string) string {
	name := strings.TrimSuffix(filename, path.Ext(filename))
	m := regexp.MustCompile(`\d+(?:\.\d+)+`).FindString(name)
	if m == "" {
		m = digitRun.FindString(name)
	}
	return m
}

func Newer(candidate, current []int) bool {
	for i := 0; i < len(candidate) && i < len(current); i++ {
		if candidate[i] != current[i] {
			return candidate[i] > current[i]
		}
	}
	return len(candidate) > len(current)
}

func releaseURL(entry profiles.ISOEntry, rel profiles.ISORelease) string {
	if len(entry.Mirrors) == 0 {
		return rel.Path
	}
	return strings.TrimRight(entry.Mirrors[0].Base, "/") + rel.Path
}

func FindUpdate(img *models.Image, cat *profiles.ISOCatalogue) (*Update, bool) {
	if img == nil || cat == nil {
		return nil, false
	}
	installed := VersionOf(img.Filename)
	key := VersionKey(img.Filename)
	var best *Update
	var bestVersion []int
	consider := func(entry profiles.ISOEntry, rel profiles.ISORelease) {
		filename := path.Base(rel.Path)
		if filename == img.Filename {
			return
		}
		version := VersionOf(filename)
		if !Newer(version, installed) {
			return
		}
		if best != nil && !Newer(version, bestVersion) {
			return
		}
		best = &Update{Version: VersionLabel(filename), Filename: filename, URL: releaseURL(entry, rel), Release: rel.Label}
		bestVersion = version
	}
	for _, entry := range cat.Distros {
		if img.CatalogueID != "" && entry.ID == img.CatalogueID && img.CatalogueRelease != "" {
			for _, rel := range entry.Releases {
				if rel.Label == img.CatalogueRelease {
					consider(entry, rel)
				}
			}
		}
	}
	if best != nil {
		return best, true
	}
	for _, entry := range cat.Distros {
		if img.CatalogueID != "" && entry.ID != img.CatalogueID {
			continue
		}
		for _, rel := range entry.Releases {
			if VersionKey(path.Base(rel.Path)) == key {
				consider(entry, rel)
			}
		}
	}
	return best, best != nil
}

func ToolUpdate(tool *models.BootTool, def *tools.ToolDefinition) (string, bool) {
	if tool == nil || def == nil || tool.Custom || !tool.Downloaded || tool.DownloadedURL == "" {
		return "", false
	}
	if def.DownloadURL == "" || def.DownloadURL == tool.DownloadedURL {
		return "", false
	}
	return def.DownloadURL, true
}
