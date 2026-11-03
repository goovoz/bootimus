// Package remotefetch 负责从上游取「单文件清单」（发行版配置、工具清单）。
//
// 这些文件放在 raw.githubusercontent.com 上，国内网络经常连不通或一直挂着，
// 表现出来就是 30 秒后的一句 "context deadline exceeded (Client.Timeout
// exceeded while awaiting headers)"。jsDelivr 可以直接代理 GitHub 仓库里的文件，
// 同一个路径换几个边缘节点轮着试，比死等一个源强得多。
package remotefetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// perAttempt 是单个源的超时。raw.githubusercontent.com 被墙时通常是一直挂着不回，
// 不把单源超时压短，第一个源就能吃光整个预算。
const perAttempt = 12 * time.Second

// maxBody 兜住异常大的响应（正常清单只有几十 KB）。
const maxBody = 8 << 20

// jsDelivrHosts 是 jsDelivr 的可用边缘节点，顺序即优先级。
var jsDelivrHosts = []string{"cdn.jsdelivr.net", "fastly.jsdelivr.net", "gcore.jsdelivr.net"}

// Mirrors 把 https://raw.githubusercontent.com/<owner>/<repo>/<ref>/<path>
// 展开成 jsDelivr 地址。解析不了（host 不对、段数不够）就返回 nil，
// 调用方继续只用主地址。
func Mirrors(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "raw.githubusercontent.com" {
		return nil
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) < 4 {
		return nil
	}
	owner, repo, ref := segs[0], segs[1], segs[2]
	path := strings.Join(segs[3:], "/")
	out := make([]string, 0, len(jsDelivrHosts))
	for _, h := range jsDelivrHosts {
		out = append(out, fmt.Sprintf("https://%s/gh/%s/%s@%s/%s", h, owner, repo, ref, path))
	}
	return out
}

// WithMirrors 返回「主地址 + 全部镜像」，主地址永远排第一。
func WithMirrors(primary string) []string {
	return append([]string{primary}, Mirrors(primary)...)
}

// Get 按顺序尝试每个地址，返回第一个 200 的正文与实际使用的地址。
// 全部失败时把每个源的简短原因拼进错误，方便一眼分辨「超时」还是「404」。
func Get(ctx context.Context, client *http.Client, urls []string) ([]byte, string, error) {
	if len(urls) == 0 {
		return nil, "", fmt.Errorf("没有配置远端地址")
	}
	if client == nil {
		client = &http.Client{Timeout: perAttempt}
	}
	var reasons []string
	for _, u := range urls {
		actx, cancel := context.WithTimeout(ctx, perAttempt)
		req, err := http.NewRequestWithContext(actx, http.MethodGet, u, nil)
		if err != nil {
			cancel()
			reasons = append(reasons, fmt.Sprintf("%s: %v", hostOf(u), err))
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			reasons = append(reasons, fmt.Sprintf("%s: %s", hostOf(u), brief(err)))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %s", hostOf(u), brief(readErr)))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			reasons = append(reasons, fmt.Sprintf("%s: HTTP %d", hostOf(u), resp.StatusCode))
			continue
		}
		return body, u, nil
	}
	return nil, "", fmt.Errorf("所有源都失败（%s）", strings.Join(reasons, "；"))
}

func hostOf(u string) string {
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		return pu.Host
	}
	return u
}

// brief 把 Go 的网络错误压成一句话，太长的原文会挤爆飘窗。
func brief(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "Client.Timeout"), strings.Contains(s, "context deadline exceeded"):
		return "请求超时"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(s, "connection reset"):
		return "连接被重置"
	case strings.Contains(s, "no such host"):
		return "域名解析失败"
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
