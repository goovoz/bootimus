package admin

// [bootimus-fork L1] fork-patch: iso-catalog-resolve
//
// 目录里带版本号的文件名（debian-13.5.0-amd64-netinst.iso、kali-linux-2024.4-live-amd64.iso …）
// 会随上游点版本变化，写死在 distro-profiles.json 里迟早 404。
// 这里提供一个「按目录里最新的同类文件解析」的兜底：只在该 URL 已经失效时才访问目录，
// 结果按 URL 缓存，正常情况零额外请求。

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	isoResolveTTL        = 15 * time.Minute
	isoResolveMaxBodyLen = 1 << 20
	isoResolveTimeout    = 25 * time.Second
	// 部分镜像站会拦 Go 默认 UA（和 ISO 下载那次的坑一样）
	isoResolveUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

var (
	isoResolveCache sync.Map // raw url -> isoResolveEntry
	digitRun        = regexp.MustCompile(`\d+`)
	hrefISO         = regexp.MustCompile(`href="([^"?]+\.iso)"`)
)

type isoResolveEntry struct {
	url string
	at  time.Time
}

func isoResolveClient() *http.Client {
	return &http.Client{Timeout: isoResolveTimeout}
}

func headStatus(client *http.Client, raw string) (int, error) {
	req, err := http.NewRequest(http.MethodHead, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", isoResolveUA)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

func splitURL(raw string) (dir, name string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", errors.New("only http(s) URLs can be resolved")
	}
	path := u.Path
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "", "", errors.New("URL has no directory part")
	}
	u.Path = path[:idx]
	return u.String(), path[idx+1:], nil
}

// versionPattern turns "debian-13.5.0-amd64-netinst.iso" into
// ^debian-\d+\.\d+\.\d+-amd64-netinst\.iso$ so only the same family matches
// (debian-edu-… or a different architecture is rejected).
func versionPattern(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	q = digitRun.ReplaceAllString(q, `\d+`)
	return regexp.MustCompile("^" + q + "$")
}

func numericTuples(name string) []int {
	parts := digitRun.FindAllString(name, -1)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func newerTuple(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

func pickNewestISO(names []string, pattern *regexp.Regexp) string {
	var best string
	for _, n := range names {
		if !pattern.MatchString(n) {
			continue
		}
		if best == "" || newerTuple(numericTuples(n), numericTuples(best)) {
			best = n
		}
	}
	return best
}

func listDirectoryISO(client *http.Client, dir string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, dir+"/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", isoResolveUA)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("directory listing returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, isoResolveMaxBodyLen))
	if err != nil {
		return nil, err
	}
	html := string(body)
	seen := map[string]bool{}
	var names []string
	for _, m := range hrefISO.FindAllStringSubmatch(html, -1) {
		name := m[1]
		if strings.Contains(name, "/") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// resolveLatestISOURL returns the newest sibling of raw when raw itself is gone.
// changed reports whether the returned URL differs from the input.
func resolveLatestISOURL(raw string) (resolved string, changed bool, err error) {
	if e, ok := isoResolveCache.Load(raw); ok {
		entry := e.(isoResolveEntry)
		if time.Since(entry.at) < isoResolveTTL {
			return entry.url, entry.url != raw, nil
		}
		isoResolveCache.Delete(raw)
	}

	client := isoResolveClient()
	if code, headErr := headStatus(client, raw); headErr == nil && code == http.StatusOK {
		isoResolveCache.Store(raw, isoResolveEntry{url: raw, at: time.Now()})
		return raw, false, nil
	}

	dir, name, err := splitURL(raw)
	if err != nil {
		return raw, false, err
	}
	names, err := listDirectoryISO(client, dir)
	if err != nil {
		return raw, false, fmt.Errorf("cannot list %s: %w", dir, err)
	}
	best := pickNewestISO(names, versionPattern(name))
	if best == "" || best == name {
		return raw, false, fmt.Errorf("no newer file matching %q in %s", name, dir)
	}
	candidate := dir + "/" + best
	if code, err := headStatus(client, candidate); err != nil || code != http.StatusOK {
		return raw, false, fmt.Errorf("newest candidate %s is not downloadable (HTTP %d)", best, code)
	}
	isoResolveCache.Store(raw, isoResolveEntry{url: candidate, at: time.Now()})
	return candidate, true, nil
}
