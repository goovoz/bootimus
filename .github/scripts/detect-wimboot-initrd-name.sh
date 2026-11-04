#!/usr/bin/env bash
# 探测器：上游给 Windows 应答文件那条 initrd 是否已经用 --name 压短名字
#
# 背景：wimboot 枚举 iPXE 暴露的 EFI 文件系统时用固定 31 字符的名字缓冲
# （VDISK_NAME_LEN），而 iPXE 给镜像取名取的是 URL 的 basename、忽略行尾那个名字参数。
# bootimus 的应答文件 URL 是 /autoinstall/<ISO 文件名>，basename 58 字符，
# 于是 wimboot 直接 die："Could not read root directory: 0x8000000000000005"。
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/server/menu.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/server/menu.go：$tree"; return 0
    fi
    if grep -q "initrd --name AutoUnattend.xml" "$f"; then
        echo "RESULT=IMPLEMENTED"; echo "REASON=上游已经用 --name 给 AutoUnattend.xml 压短名字"
    elif grep -q "AutoUnattend.xml" "$f"; then
        echo "RESULT=NEEDED"; echo "REASON=上游仍把名字写在 URL 后面（iPXE 会忽略它，改用 URL basename）"
    else
        echo "RESULT=UNKNOWN"; echo "REASON=menu.go 里找不到 AutoUnattend.xml，Windows 引导逻辑可能已重写"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/server" "$base/b/internal/server" "$base/c/internal/server"
    printf 'initrd %%s/autoinstall/%%s?mac=%%s AutoUnattend.xml\n' > "$base/a/internal/server/menu.go"
    printf 'initrd --name AutoUnattend.xml %%s/autoinstall/%%s?mac=%%s\n' > "$base/b/internal/server/menu.go"
    printf 'package server\n' > "$base/c/internal/server/menu.go"
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
    echo "用法: detect-wimboot-initrd-name.sh <tree> | --selftest"; exit 2
fi
