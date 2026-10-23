#!/usr/bin/env bash
# 探测器：上游是否已自行支持 ISO 下载的断点续传（Range）与残留清理
# 用法：detect-download-resume.sh <上游树> | --selftest
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go"; return 0
    fi
    # 注意：上游本来就有「bytes=0-1」这种固定探测用的 Range（probeRemoteISOFilename），
    # 不能拿 Range 本身当信号，必须认「按偏移量续传」的写法。
    if grep -qE 'bytes=%d-|If-Range' "$f"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游下载链路已出现按偏移量续传（bytes=%d- / If-Range）"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游下载仍是「GET 全量 + os.Create 截断」，续传与残留清理仍需要本补丁"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin" "$base/missing"
    # a：像上游那样只有固定 2 字节探测，没有续传
    printf 'package admin\nfunc f(){ req.Header.Set("Range", "bytes=0-1"); resp, _ := client.Get(u); os.Create(p) }\n' \
        > "$base/a/internal/admin/handlers.go"
    # b：按偏移量续传
    printf 'package admin\nfunc f(){ req.Header.Set("Range", fmt.Sprintf("bytes=%%d-", offset)); req.Header.Set("If-Range", etag) }\n' \
        > "$base/b/internal/admin/handlers.go"
    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "missing UNKNOWN"; do
        set -- $pair
        got=$(bash "$0" "$base/$1" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$2" ]; then echo "  PASS  $1 -> $got"; else echo "  FAIL  $1 -> $got（期望 $2）"; fail=1; fi
    done
    rm -rf "$base"
    [ "$fail" -eq 0 ] && echo "ALL PASS" || { echo "有用例失败"; return 1; }
}

if [ "${1:-}" = "--selftest" ]; then
    selftest
else
    detect "${1:?用法: detect-download-resume.sh <tree> | --selftest}"
fi
