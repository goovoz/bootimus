#!/usr/bin/env bash
# 探测器：上游是否已自带「自动安装文件生成」能力
# 用法：detect-autoinstall-generator.sh <上游树> | --selftest
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go"; return 0
    fi
    if grep -qE 'autoinstall-templates|RenderAutoInstallTemplate' "$f"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游已提供模板目录/渲染接口（autoinstall-templates / RenderAutoInstallTemplate）"
    elif [ -f "$tree/internal/autoinstall/generate.go" ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 internal/autoinstall 里已有 generate.go"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游只有自动安装文件的存取，没有模板生成能力"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin" "$base/missing"
    printf 'package admin\n// ListAutoInstallFiles only\n' > "$base/a/internal/admin/handlers.go"
    printf 'package admin\n// mux.HandleFunc("/api/autoinstall-templates")\n' > "$base/b/internal/admin/handlers.go"
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
    detect "${1:?用法: detect-autoinstall-generator.sh <tree> | --selftest}"
fi
