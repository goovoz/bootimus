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
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	isoResolveTTL     = 15 * time.Minute
	isoResolveFailTTL = 2 * time.Minute
	isoResolveMaxBody = 1 << 20
	isoResolveTimeout = 25 * time.Second
	// 部分镜像站会拦 Go 默认 UA（和 ISO 下载那次的坑一样）。
	// 同一个 UA 也给 probeRemoteISOFilename 用：解析和探测打的是同一批站，UA 不一致会
	// 出现「HEAD 能过、带 Range 的探测被 403」这种只在部分站上复现的怪现象。
	isoUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

var (
	isoResolveCache sync.Map // raw url -> isoResolveEntry
	digitRun        = regexp.MustCompile(`\d+`)
	// 目录页的引号风格不统一（nginx 用双引号、Apache 常见单引号），
	// 也允许 href 里带 query；取出来的值统一再交给 url.Parse 处理。
	hrefISO = regexp.MustCompile(`href=["']([^"']*\.iso[^"']*)["']`)
)

type isoResolveEntry struct {
	url    string
	at     time.Time
	errMsg string // 非空 = 这次解析失败；短时间内不必再用三个请求重试
}

func headStatus(client *http.Client, raw string) (int, error) {
	req, err := http.NewRequest(http.MethodHead, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", isoUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// splitURL keeps the parsed URL (query string included — signed links break if it
// is dropped) instead of rebuilding the directory by string concatenation.
func splitURL(raw string) (dir *url.URL, name string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", errors.New("only http(s) URLs can be resolved")
	}
	idx := strings.LastIndex(u.Path, "/")
	if idx < 0 {
		return nil, "", errors.New("URL has no directory part")
	}
	d := *u
	d.Path = u.Path[:idx]
	d.RawPath = ""
	return &d, u.Path[idx+1:], nil
}

// siblingURL builds "<dir>/<name>" while keeping scheme, host and query.
func siblingURL(dir *url.URL, name string) string {
	u := *dir
	base := strings.TrimSuffix(dir.Path, "/")
	if name == "" {
		u.Path = base + "/"
	} else {
		u.Path = base + "/" + name
	}
	u.RawPath = ""
	return u.String()
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

func listDirectoryISO(client *http.Client, dir *url.URL) ([]string, error) {
	listing := siblingURL(dir, "")
	req, err := http.NewRequest(http.MethodGet, listing, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", isoUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("directory listing returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, isoResolveMaxBody))
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var names []string
	for _, m := range hrefISO.FindAllStringSubmatch(string(body), -1) {
		ref := m[1]
		// 目录可能给出绝对地址或带 query 的地址，先取路径再取最后一段
		if u, perr := url.Parse(ref); perr == nil && u.Path != "" {
			ref = u.Path
		}
		name := path.Base(ref)
		if name == "" || name == "." || name == "/" || name == ".." || seen[name] {
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
// client is supplied by the caller so this path honours the same proxy setting as
// the download it is about to trigger — a user who needs a proxy to reach the
// mirror needs it for the HEAD/listing requests too.
func resolveLatestISOURL(client *http.Client, raw string) (resolved string, changed bool, err error) {
	if client == nil {
		client = &http.Client{Timeout: isoResolveTimeout}
	}
	if e, ok := isoResolveCache.Load(raw); ok {
		entry := e.(isoResolveEntry)
		ttl := isoResolveTTL
		if entry.errMsg != "" {
			ttl = isoResolveFailTTL
		}
		if time.Since(entry.at) < ttl {
			if entry.errMsg != "" {
				return raw, false, errors.New(entry.errMsg)
			}
			return entry.url, entry.url != raw, nil
		}
		isoResolveCache.Delete(raw)
	}

	fail := func(err error) (string, bool, error) {
		isoResolveCache.Store(raw, isoResolveEntry{url: raw, at: time.Now(), errMsg: err.Error()})
		return raw, false, err
	}

	code, headErr := headStatus(client, raw)
	if headErr == nil {
		if code == http.StatusOK {
			isoResolveCache.Store(raw, isoResolveEntry{url: raw, at: time.Now()})
			return raw, false, nil
		}
		// HEAD 不被支持时无法判断文件在不在，按原样交给下载链路，不做改写
		if code == http.StatusMethodNotAllowed || code == http.StatusNotImplemented {
			return raw, false, nil
		}
	}

	dir, name, err := splitURL(raw)
	if err != nil {
		return fail(err)
	}
	names, err := listDirectoryISO(client, dir)
	if err != nil {
		return fail(fmt.Errorf("cannot list %s: %w", siblingURL(dir, ""), err))
	}
	best := pickNewestISO(names, versionPattern(name))
	if best == "" || best == name {
		return fail(fmt.Errorf("no newer file matching %q in %s", name, siblingURL(dir, "")))
	}
	candidate := siblingURL(dir, best)
	if code, err := headStatus(client, candidate); err != nil || code != http.StatusOK {
		return fail(fmt.Errorf("newest candidate %s is not downloadable (HTTP %d)", best, code))
	}
	isoResolveCache.Store(raw, isoResolveEntry{url: candidate, at: time.Now()})
	return candidate, true, nil
}
