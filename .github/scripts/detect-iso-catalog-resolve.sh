#!/usr/bin/env bash
# 探测器：上游是否已自带「ISO 目录里挑最新文件」的能力
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go"; return 0
    fi
    if grep -qE 'iso-catalog/resolve|resolveLatestISOURL|ResolveISOCatalogURL' "$f"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游已提供 ISO URL 解析（iso-catalog/resolve）"
    elif grep -q 'pickNewestISO\|versionPattern' "$tree"/internal/admin/*.go 2>/dev/null; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 internal/admin 里已有版本号解析实现"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游下载 URL 仍是写死版本号，点版本一升级就 404"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin" "$base/missing"
    printf 'package admin\n// DownloadISO only\n' > "$base/a/internal/admin/handlers.go"
    printf 'package admin\n// mux.HandleFunc("/api/iso-catalog/resolve", ...)\n' > "$base/b/internal/admin/handlers.go"
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
    detect "${1:?用法: detect-iso-catalog-resolve.sh <tree> | --selftest}"
fi
