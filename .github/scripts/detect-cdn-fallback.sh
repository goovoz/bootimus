#!/usr/bin/env bash
# 探测器：上游是否已经自己解决「远端清单在国内拉不动」的问题
#
# 本 fork 的 cdn-fallback 补丁把 raw.githubusercontent.com 换成
# 「主地址 + jsDelivr 镜像」轮询（internal/remotefetch）。上游一旦自己加了
# 镜像 / 重试 / 多源，这个补丁就该退役。
set -uo pipefail

detect() {
    local tree="$1"
    if [ ! -d "$tree" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=目录不存在：$tree"; return 0
    fi
    if grep -rqiE 'jsdelivr' "$tree"/internal/profiles "$tree"/internal/tools 2>/dev/null; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 profiles/tools 里已经出现 jsDelivr 镜像"
    elif grep -rqE 'RemoteProfilesURLs|RemoteToolsURLs|remotefetch' "$tree"/internal 2>/dev/null; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游已有多源清单获取（RemoteProfilesURLs/RemoteToolsURLs/remotefetch）"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游仍是单源 GET raw.githubusercontent.com，国内会挂到超时"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/profiles" "$base/b/internal/profiles" "$base/c/internal/tools"
    printf 'package profiles\nconst u = "https://raw.githubusercontent.com/x/y/main/z.json"\n' > "$base/a/internal/profiles/manager.go"
    printf 'package profiles\nvar RemoteProfilesURLs = []string{"a","b"}\n' > "$base/b/internal/profiles/manager.go"
    printf 'package tools\n// mirror via cdn.jsdelivr.net\n' > "$base/c/internal/tools/manager.go"
    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "c IMPLEMENTED"; do
        set -- $pair
        got=$(bash "$0" "$base/$1" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$2" ]; then echo "  PASS  $1 -> $got"; else echo "  FAIL  $1 -> $got（期望 $2）"; fail=1; fi
    done
    rm -rf "$base"
    [ "$fail" -eq 0 ] && echo "ALL PASS" || { echo "有用例失败"; return 1; }
}

if [ "$#" -gt 0 ] && [ "$1" = "--selftest" ]; then
    selftest
elif [ "$#" -ge 1 ]; then
    detect "$1"
else
    echo "用法: detect-cdn-fallback.sh <tree> | --selftest"; exit 2
fi
