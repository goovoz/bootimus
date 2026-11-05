#!/usr/bin/env bash
# 探测器：上游的 proxyDHCP 是否已经从「请求进来的那块网卡」回包
#
# 背景：proxyDHCP 的 OFFER/ACK 发往 255.255.255.255:68。socket 绑在 0.0.0.0:67、
# 没开 IP_PKTINFO 时，内核按路由表选出口 —— 有多块网卡（docker 网桥 / 管理口 / VPN）
# 的机器上广播会从默认路由那块出去，客户端根本收不到，日志里却显示「已回复」。
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/proxydhcp/proxydhcp.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/proxydhcp/proxydhcp.go：$tree"; return 0
    fi
    if grep -rq "IP_PKTINFO" "$tree/internal" 2>/dev/null; then
        echo "RESULT=IMPLEMENTED"; echo "REASON=上游已经用 IP_PKTINFO 决定回包出口"
    elif grep -q "WriteToUDP" "$f"; then
        echo "RESULT=NEEDED"; echo "REASON=上游仍直接 WriteToUDP 到 255.255.255.255，多网卡主机会从默认路由发出去"
    else
        echo "RESULT=UNKNOWN"; echo "REASON=proxyDHCP 的回包路径可能已重写，找不到 WriteToUDP"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/proxydhcp" "$base/a/internal/netbind" \
             "$base/b/internal/proxydhcp" "$base/b/internal/netbind" \
             "$base/c/internal/proxydhcp"
    printf 'conn.WriteToUDP(b, dst)\n' > "$base/a/internal/proxydhcp/proxydhcp.go"
    printf 'package netbind\n' > "$base/a/internal/netbind/x.go"
    printf 'conn.WriteToUDP(b, dst)\n' > "$base/b/internal/proxydhcp/proxydhcp.go"
    printf 'unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_PKTINFO, 1)\n' > "$base/b/internal/netbind/x.go"
    printf 'package proxydhcp\n' > "$base/c/internal/proxydhcp/proxydhcp.go"
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
    echo "用法: detect-proxydhcp-reply-via-ingress.sh <tree> | --selftest"; exit 2
fi
