#!/usr/bin/env bash
# 探测器：上游是否已自行支持下载走代理
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go"; return 0
    fi
    if grep -q 'http\.ProxyURL\|ProxyConfig' "$f"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游下载链路已出现代理支持（http.ProxyURL / ProxyConfig）"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游下载仍是直连，代理能力仍需要本补丁"
    fi
}

selftest() {
    local base; base=$(mktemp -d); fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin"
    printf 'package admin\nfunc f(){ client.Get(u) }\n' > "$base/a/internal/admin/handlers.go"
    printf 'package admin\nfunc f(){ tr := &http.Transport{Proxy: http.ProxyURL(pu)} }\n' > "$base/b/internal/admin/handlers.go"
    mkdir -p "$base/missing"
    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "missing UNKNOWN"; do
        set -- $pair
        got=$(bash "$0" "$base/$1" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$2" ]; then echo "  PASS  $1 -> $got"; else echo "  FAIL  $1 -> $got（期望 $2）"; fail=1; fi
    done
    rm -rf "$base"
    [ "$fail" -eq 0 ] && echo "ALL PASS" || { echo "有用例失败"; return 1; }
}

if [ "${1:-}" = "--selftest" ]; then selftest; else detect "${1:?用法: detect-download-proxy.sh <tree> | --selftest}"; fi
