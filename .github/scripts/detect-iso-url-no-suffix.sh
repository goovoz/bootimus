#!/usr/bin/env bash
# [bootimus-fork L0] 判定上游是否已自己实现「允许下载不带 .iso 后缀的 ISO 链接」。
#
# 用法：detect-iso-url-no-suffix.sh <upstream-source-tree>
# 输出：KEY=VALUE 形式，供 workflow 解析
#   RESULT=IMPLEMENTED  上游已实现 → 本补丁应当退役（让上游实现在位）
#   RESULT=SUSPECT      出现相关痕迹但证据不足 → 保留补丁 + 人工复核
#   RESULT=NEEDED       上游仍未实现 → 保留补丁
#   RESULT=UNKNOWN      上游结构变化过大，判不了 → 保留补丁 + 人工
# 也可用 --selftest 在无网络情况下自检判定逻辑。
#
# 信号说明（都是对“上游代码”跑，不跑自己的实现）：
#   strict_gone  「URL must point to an .iso file」这句拒绝消失 → 上游放宽了后缀校验
#   call_gone    DownloadISO 不再直接调用严格的 isoDownloadFilename
#   probe_added  出现 Range 探测（bytes=0-1 / Header.Set("Range"）—— 这是本能力的指纹
#   ui_added     前端出现 name="filename" 输入项
#
# 注意：不能用 Content-Disposition 当信号 —— 上游本来就有 3 处（下载响应头），会误判。
set -uo pipefail

STUB_STRICT='URL must point to an .iso file'
STUB_CALL='isoDownloadFilename(req.URL, req.Filename)'

have_strict_reject() {
    grep -qF "$STUB_STRICT" "$1/internal/admin/handlers.go" 2>/dev/null
}

have_direct_call() {
    grep -qF "$STUB_CALL" "$1/internal/admin/handlers.go" 2>/dev/null
}

# 探测远端文件名的指纹：只有为了“猜文件名”才会对下载地址发 Range 请求
have_probe_capability() {
    local h="$1/internal/admin/handlers.go"
    grep -qF 'bytes=0-1' "$h" 2>/dev/null && return 0
    grep -qF 'Header.Set("Range"' "$h" 2>/dev/null && return 0
    return 1
}

# 前端是否新增了「文件名字段」（上游当前没有；注意 filename_patterns 不算）
have_ui_filename_field() {
    grep -qE 'name="filename"[[:space:]>]' "$1/web/static/index.html" 2>/dev/null
}

detect() {
    local tree="$1"
    if [ ! -f "$tree/internal/admin/handlers.go" ]; then
        echo "RESULT=UNKNOWN"
        echo "REASON=upstream tree 中找不到 internal/admin/handlers.go（路径可能被上游重构）"
        return 0
    fi

    local strict=1 call=1 probe=0 ui=0
    have_strict_reject "$tree" || strict=0
    have_direct_call "$tree" || call=0
    have_probe_capability "$tree" && probe=1
    have_ui_filename_field "$tree" && ui=1

    local ev="strict=${strict} call=${call} probe=${probe} ui=${ui}"

    if [ "$strict" -eq 0 ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游不再用「URL 必须以 .iso 结尾」拒绝请求（$ev）"
    elif [ "$probe" -eq 1 ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游代码出现 Range 文件名探测（$ev）"
    elif [ "$call" -eq 0 ] && [ "$ui" -eq 1 ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游调用点已换掉严格函数，且前端新增了文件名字段（$ev）"
    elif [ "$call" -eq 0 ] || [ "$ui" -eq 1 ]; then
        echo "RESULT=SUSPECT"
        echo "REASON=上游出现部分痕迹但证据不足（$ev），保留补丁并请人工确认"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游仍要求 URL 以 .iso 结尾，本补丁仍然必要（$ev）"
    fi
}

selftest() {
    local tmp rc=0
    tmp=$(mktemp -d)
    mkdir -p "$tmp"/{needed,suspect,implemented}/{internal/admin,web/static}

    # 1) 上游当前的样子：严格校验 + 直接调用，无探测、无前端字段
    cat > "$tmp/needed/internal/admin/handlers.go" <<'EOF'
func isoDownloadFilename(rawURL, requested string) (string, error) {
	if requested == "" {
		filename := filepath.Base(rawURL)
		if !strings.HasSuffix(strings.ToLower(filename), ".iso") {
			return "", fmt.Errorf("URL must point to an .iso file")
		}
		return filename, nil
	}
}
func (h *Handler) DownloadISO(w http.ResponseWriter, r *http.Request) {
	filename, err := isoDownloadFilename(req.URL, req.Filename)
}
EOF
    echo '<input type="url" name="url" required>' > "$tmp/needed/web/static/index.html"

    # 2) 调用点被重构但没加探测能力（证据不足）
    sed 's|isoDownloadFilename(req.URL, req.Filename)|isoDownloadFilename(req.URL, strings.TrimSpace(req.Filename))|' \
        "$tmp/needed/internal/admin/handlers.go" > "$tmp/suspect/internal/admin/handlers.go"
    echo '<input type="url" name="url" required>' > "$tmp/suspect/web/static/index.html"

    # 3) 上游已实现：出现 Range 探测（严格校验可以还在，也算实现）
    cp "$tmp/needed/internal/admin/handlers.go" "$tmp/implemented/internal/admin/handlers.go"
    cat >> "$tmp/implemented/internal/admin/handlers.go" <<'EOF'
func probeRemoteISOFilename(rawURL string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("Range", "bytes=0-1")
	return filepath.Base(req.URL.Path), nil
}
EOF
    echo '<input type="url" name="url" required>' > "$tmp/implemented/web/static/index.html"

    for c in needed suspect implemented; do
        local got want
        got=$(detect "$tmp/$c" | sed -n 's/^RESULT=//p')
        case "$c" in
            needed) want=NEEDED ;;
            suspect) want=SUSPECT ;;
            implemented) want=IMPLEMENTED ;;
        esac
        if [ "$got" = "$want" ]; then
            echo "  ok   $c -> $got"
        else
            echo "  FAIL $c -> got=$got want=$want"
            rc=1
        fi
    done
    rm -rf "$tmp"
    [ $rc -eq 0 ] && echo "SELFTEST: PASS" || echo "SELFTEST: FAIL"
    return $rc
}

case "${1:-}" in
    --selftest) selftest ;;
    "") echo "usage: $0 <upstream-source-tree> | --selftest" >&2; exit 2 ;;
    *) detect "$1" ;;
esac
