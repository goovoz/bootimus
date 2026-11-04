#!/usr/bin/env bash
# 探测器：上游的 WinPE startnet 脚本是否也会在断网时打印网卡清单
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go：$tree"; return 0
    fi
    if grep -q "Adapters WinPE can see" "$f"; then
        echo "RESULT=IMPLEMENTED"; echo "REASON=上游 netfail 分支已经会打印网卡清单"
    elif grep -q "Could not reach" "$f"; then
        echo "RESULT=NEEDED"; echo "REASON=上游 netfail 只打印一句 ERROR，没有网卡诊断"
    else
        echo "RESULT=UNKNOWN"; echo "REASON=handlers.go 里找不到 netfail 逻辑，脚本可能已重写"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin" "$base/c/internal/admin"
    printf 'echo ERROR: Could not reach {{ .ServerAddr }}\n' > "$base/a/internal/admin/handlers.go"
    printf 'echo Adapters WinPE can see\nipconfig /all\n' > "$base/b/internal/admin/handlers.go"
    printf 'package admin\n' > "$base/c/internal/admin/handlers.go"
    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "c UNKNOWN"; do
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
    echo "用法: detect-winpe-net-diagnostics.sh <tree> | --selftest"; exit 2
fi
