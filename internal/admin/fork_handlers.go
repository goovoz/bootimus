// [bootimus-fork L0] fork 自有的 handlers。
//
// 这里的函数上游没有。放在本文件是为了让 internal/admin/handlers.go 的 diff 只剩
// 「改上游函数的那几行」——之前 39 个 hunk 里大半是整段新增，上游一动就冲突。
// 同 package 内整块移动，语义不变。
package admin

import (
	"bootimus/internal/autoinstall"
	_ "bootimus/internal/bmc/amt"
	_ "bootimus/internal/bmc/redfish"
	"bootimus/internal/models"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prune drops finished entries nobody is polling any more; without it the map
// grows for the lifetime of the process.
func (dm *DownloadManager) Prune(olderThan time.Duration) int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for name, p := range dm.downloads {
		if p.Status == "downloading" || p.FinishedAt.IsZero() {
			continue
		}
		if p.FinishedAt.Before(cutoff) {
			delete(dm.downloads, name)
			removed++
		}
	}
	return removed
}

// resolveDownloadFilename picks the on-disk file name for a download.
//
// isoDownloadFilename intentionally stays strict (a URL has to end in .iso),
// but plenty of legitimate sources hand out opaque links: Buzzheavier-style
// file hosts, signed S3/CloudFront URLs, one-time tokens. Those have no usable
// suffix, so before rejecting the request we ask the origin what it is about to
// send. Only an ISO-ish Content-Type gets through, which keeps typos and
// mistyped page URLs failing fast.
func resolveDownloadFilename(client *http.Client, rawURL, requested string) (string, error) {
	filename, err := isoDownloadFilename(rawURL, requested)
	if err == nil {
		return filename, nil
	}
	if requested != "" {
		// The caller named the file explicitly - never guess over their input.
		return "", err
	}

	name, probeErr := probeRemoteISOFilename(client, rawURL)
	if probeErr != nil {
		return "", fmt.Errorf("%w. The server did not identify the content as a disc image either (%v); set the file name explicitly to override", err, probeErr)
	}
	return name, nil
}

// probeRemoteISOFilename issues a 2-byte ranged GET (servers that ignore Range
// simply get their body closed early) and derives a .iso file name from the
// response headers.
//
// client 由调用方传入：它必须和随后真正下载用的是同一个（含代理配置与超时），
// 否则「探测直连、下载走代理」会出现探测失败而下载本来能成的情况。
func probeRemoteISOFilename(client *http.Client, rawURL string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Range", "bytes=0-1")
	req.Header.Set("User-Agent", isoUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !isoContentTypes[ct] {
		return "", fmt.Errorf("unexpected content type %q", ct)
	}

	name := filenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = filepath.Base(resp.Request.URL.Path)
	}
	if name == "" || name == "." || name == "/" || name == ".." {
		return "", fmt.Errorf("server did not report a file name")
	}

	name = filepath.Base(name)
	if !strings.HasSuffix(strings.ToLower(name), ".iso") {
		name += ".iso"
	}
	// Re-run the strict validation so path traversal / odd names are still rejected.
	return isoDownloadFilename(rawURL, name)
}

// filenameFromContentDisposition extracts the file name from a
// Content-Disposition header, including RFC 2231 encoded (filename*) values.
func filenameFromContentDisposition(cd string) string {
	if strings.TrimSpace(cd) == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(cd); err == nil {
		if v := strings.TrimSpace(params["filename"]); v != "" {
			return v
		}
	}
	return ""
}

// [bootimus-fork L1] fork-patch: iso-catalog-resolve
// 目录里带版本号的 ISO 文件名会随上游点版本变化（debian-13.5.0 → 13.7.0），
// 前端在下载前问一次：这个 URL 还在吗？不在就按目录里最新的同类文件改掉。
// 解析失败不报错，调用方按原 URL 继续（最多是下载时报 404）。
func (h *Handler) ResolveISOCatalogURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	var req struct {
		URL string `json:"url"`
		// [bootimus-fork L1] fork-patch: iso-catalog-resolve
		// 解析请求要和随后的下载用同一条链路（含代理），否则直连不通的用户拿到的
		// 永远是「解析失败 → 原 URL → 404」。
		UseProxy bool `json:"use_proxy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid body"})
		return
	}
	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "URL is required"})
		return
	}
	resolved, changed, err := resolveLatestISOURL(h.resolveClient(req.UseProxy), raw)
	data := map[string]any{"url": resolved, "changed": changed}
	if err != nil {
		data["url"] = raw
		data["changed"] = false
		data["note"] = err.Error()
	}
	h.sendJSON(w, http.StatusOK, Response{Success: true, Data: data})
}

// [bootimus-fork L1] fork-patch: download-proxy
// normalizeProxyURL accepts "host:port" or a full proxy URL and attaches credentials.
func normalizeProxyURL(addr, user, pass string) (*url.URL, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("proxy address is empty")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid proxy address: missing host")
	}
	if user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	return u, nil
}

// 下载用到的超时。整体 Timeout 不能用（ISO 动辄几个 GB），要的是「每个阶段不许拖死」：
// 连不上 15s 放弃、给了请求 30s 不出响应头放弃、90s 收不到任何字节由看门狗收尾。
const (
	downloadDialTimeout     = 15 * time.Second
	downloadHeaderTimeout   = 30 * time.Second
	downloadIdleConnTimeout = 30 * time.Second
	downloadStallAfter      = 90 * time.Second
	downloadBufferSize      = 256 * 1024
)

func newDownloadTransport(proxy *url.URL) *http.Transport {
	// 直连时保留对环境变量代理（HTTP_PROXY/HTTPS_PROXY）的支持，和默认 Transport 一致；
	// 只有显式选了代理才覆盖它。
	proxyFn := http.ProxyFromEnvironment
	if proxy != nil {
		proxyFn = http.ProxyURL(proxy)
	}
	return &http.Transport{
		Proxy:                 proxyFn,
		DialContext:           (&net.Dialer{Timeout: downloadDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   downloadDialTimeout,
		ResponseHeaderTimeout: downloadHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       downloadIdleConnTimeout,
		MaxIdleConnsPerHost:   2,
	}
}

// 空闲连接要能被复用，否则每次走代理的下载都会漏一条 TCP 连接：以前每次都新建
// Transport，而 IdleConnTimeout 为 0 意味着这些连接永不回收。
var (
	directDownloadOnce sync.Once
	directDownloadTr   *http.Transport

	proxyDownloadMu  sync.Mutex
	proxyDownloadTrs = map[string]*http.Transport{}
)

// downloadClient returns the HTTP client used for ISO downloads. When useProxy is
// set and a proxy is configured, the request is routed through it; otherwise the
// download goes direct (and the returned note explains why, if it was requested).
func (h *Handler) downloadClient(useProxy bool) (*http.Client, string) {
	if !useProxy {
		directDownloadOnce.Do(func() {
			directDownloadTr = newDownloadTransport(nil)
		})
		return &http.Client{Transport: directDownloadTr}, ""
	}
	if h.storage == nil {
		return h.downloadClient(false)
	}
	cfg, err := h.storage.GetProxyConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.Address) == "" {
		client, _ := h.downloadClient(false)
		return client, "proxy requested but not configured in Settings; downloading directly"
	}
	pu, err := normalizeProxyURL(cfg.Address, cfg.Username, cfg.Password)
	if err != nil {
		client, _ := h.downloadClient(false)
		return client, fmt.Sprintf("proxy config unusable (%v); downloading directly", err)
	}

	key := pu.String()
	proxyDownloadMu.Lock()
	tr, ok := proxyDownloadTrs[key]
	if !ok {
		tr = newDownloadTransport(pu)
		proxyDownloadTrs[key] = tr
	}
	proxyDownloadMu.Unlock()
	return &http.Client{Transport: tr}, "using proxy " + pu.Redacted()
}

// resolveClient is the client used by the "is this URL still alive?" helpers
// (ISO catalogue resolution and the no-.iso-suffix filename probe).
//
// 它复用下载链路的 Transport（因而也复用代理设置），但额外加一个整体超时：
// 这两个请求是用户在弹窗里等着的交互，不能因为没有 Timeout 而挂到天荒地老。
// Client 是值类型，浅拷贝一份再改 Timeout 不会影响真正下载用的那个。
func (h *Handler) resolveClient(useProxy bool) *http.Client {
	base, _ := h.downloadClient(useProxy)
	c := *base
	c.Timeout = isoResolveTimeout
	return &c
}

func downloadPartialPath(destPath string) string {
	return destPath + ".part"
}

func downloadMetaPath(destPath string) string {
	return destPath + ".part.json"
}

// downloadPartMeta remembers enough about the origin entity to make a resume
// safe: if the file behind the URL changed, If-Range turns the response into a
// plain 200 and the download restarts instead of splicing two versions.
type downloadPartMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Total        int64     `json:"total,omitempty"`
	Updated      time.Time `json:"updated"`
}

func readDownloadMeta(destPath string) *downloadPartMeta {
	data, err := os.ReadFile(downloadMetaPath(destPath))
	if err != nil {
		return nil
	}
	var m downloadPartMeta
	if err := json.Unmarshal(data, &m); err != nil || strings.TrimSpace(m.URL) == "" {
		return nil
	}
	return &m
}

func writeDownloadMeta(destPath string, m *downloadPartMeta) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.WriteFile(downloadMetaPath(destPath), data, 0o644); err != nil {
		log.Printf("Failed to persist resume metadata for %s: %v", filepath.Base(destPath), err)
	}
}

// parseContentRange understands "bytes 100-199/1000" as well as the "bytes */1000"
// form a 416 carries.
func parseContentRange(value string) (start, end, total int64, ok bool) {
	value = strings.TrimSpace(value)
	if len(value) < 7 || !strings.EqualFold(value[:6], "bytes ") {
		return 0, 0, 0, false
	}
	value = strings.TrimSpace(value[6:])
	slash := strings.Index(value, "/")
	if slash < 0 {
		return 0, 0, 0, false
	}
	span, totalStr := strings.TrimSpace(value[:slash]), strings.TrimSpace(value[slash+1:])
	if totalStr != "*" {
		n, err := strconv.ParseInt(totalStr, 10, 64)
		if err != nil {
			return 0, 0, 0, false
		}
		total = n
	}
	if span == "*" {
		return 0, 0, total, true
	}
	dash := strings.Index(span, "-")
	if dash < 0 {
		return 0, 0, 0, false
	}
	s, err1 := strconv.ParseInt(strings.TrimSpace(span[:dash]), 10, 64)
	e, err2 := strconv.ParseInt(strings.TrimSpace(span[dash+1:]), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, 0, false
	}
	return s, e, total, true
}

// CleanupStaleDownloads drops partial downloads nothing is writing to any more,
// so leftovers cannot pile up. Recent ones are kept on purpose: they are what the
// next attempt resumes from.
func (h *Handler) CleanupStaleDownloads() {
	h.cleanupStaleDownloads(downloadStaleAfter)
}

func (h *Handler) cleanupStaleDownloads(maxAge time.Duration) {
	entries, err := os.ReadDir(h.isoDir)
	if err != nil {
		log.Printf("Cannot inspect %s for partial downloads: %v", h.isoDir, err)
		return
	}

	knownMeta := make(map[string]bool, len(entries))
	removed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".iso.part") {
			continue
		}
		knownMeta[name+".json"] = true

		target := strings.TrimSuffix(name, ".part")
		if p := downloadMgr.Get(target); p != nil && p.Status == "downloading" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if time.Since(info.ModTime()) < maxAge {
			log.Printf("Keeping partial download %s (%d bytes) for resume", name, info.Size())
			continue
		}

		partFile := filepath.Join(h.isoDir, name)
		if err := os.Remove(partFile); err != nil {
			log.Printf("Failed to remove stale partial %s: %v", name, err)
			continue
		}
		os.Remove(partFile + ".json")
		removed++
		log.Printf("Removed stale partial download %s (%d bytes)", name, info.Size())
	}

	// Metadata whose data file is already gone.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".iso.part.json") || knownMeta[name] {
			continue
		}
		os.Remove(filepath.Join(h.isoDir, name))
	}

	if removed > 0 {
		log.Printf("Cleaned up %d stale partial download(s) in %s", removed, h.isoDir)
	}

	// 已完成的下载记录没人再看就丢掉，否则 map 会随进程一直变大
	if pruned := downloadMgr.Prune(24 * time.Hour); pruned > 0 {
		log.Printf("Dropped %d finished download record(s)", pruned)
	}
}

// StartDownloadJanitor re-runs the partial-download cleanup on a timer. Doing it
// only at startup is not enough: a host that stays up for months would never
// clean up again.
func (h *Handler) StartDownloadJanitor(interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			h.CleanupStaleDownloads()
		}
	}()
}

// [bootimus-fork L1] fork-patch: autoinstall-generator
// 模板目录：只返回元数据（变量定义 + 默认文件名），正文由 /api/autoinstall/render 现渲染。
func (h *Handler) ListAutoInstallTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	h.sendJSON(w, http.StatusOK, Response{
		Success: true,
		Data:    map[string]any{"templates": autoinstall.Templates()},
	})
}

// RenderAutoInstallTemplate 把表单值灌进内置模板，做格式校验后返回正文。
// 保存仍走 /api/autoinstall-files/save，因此「附加到镜像 / 按客户端覆盖」逻辑不变。
func (h *Handler) RenderAutoInstallTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	var req struct {
		Template string            `json:"template"`
		Values   map[string]string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid body"})
		return
	}
	tpl, ok := autoinstall.TemplateByID(req.Template)
	if !ok {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Unknown template: " + req.Template})
		return
	}
	content, err := autoinstall.RenderTemplate(req.Template, req.Values)
	if err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: err.Error()})
		return
	}
	h.sendJSON(w, http.StatusOK, Response{
		Success: true,
		Data: map[string]any{
			"content":  content,
			"filename": tpl.Filename,
			"format":   tpl.Format,
			"distro":   tpl.Distro,
		},
	})
}

func maskProxyConfig(cfg *models.ProxyConfig) *models.ProxyConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	if out.Password != "" {
		out.Password = proxyPasswordMask
	}
	return &out
}

func (h *Handler) GetProxyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	cfg, err := h.storage.GetProxyConfig()
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	h.sendJSON(w, http.StatusOK, Response{Success: true, Data: maskProxyConfig(cfg)})
}

func (h *Handler) UpdateProxyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	var cfg models.ProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid request"})
		return
	}
	cfg.Address = strings.TrimSpace(cfg.Address)
	cfg.Username = strings.TrimSpace(cfg.Username)

	// 前端把掩码原样提交回来，表示「这条口令没改」
	if cfg.Password == proxyPasswordMask {
		if existing, err := h.storage.GetProxyConfig(); err == nil && existing != nil {
			cfg.Password = existing.Password
		}
	}

	if cfg.Address != "" {
		if _, err := normalizeProxyURL(cfg.Address, cfg.Username, cfg.Password); err != nil {
			h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: err.Error()})
			return
		}
	}
	if err := h.storage.UpdateProxyConfig(&cfg); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Proxy settings saved", Data: maskProxyConfig(&cfg)})
}
