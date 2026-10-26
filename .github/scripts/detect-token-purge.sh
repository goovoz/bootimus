#!/bin/bash
# 探测器：上游是否已自行支持「删除已吊销的令牌」
set -uo pipefail

detect() {
    local tree="$1"
    local st="$tree/internal/storage"
    local admin="$tree/internal/admin"

    if [ ! -d "$st" ] || [ ! -d "$admin" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/storage 或 internal/admin"; return 0
    fi

    # 指纹 1：存储层出现了删令牌的方法
    if grep -rqs 'func.*DeleteAPIToken\|func deleteAPIToken' "$st"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游存储层已出现删除令牌的方法（DeleteAPIToken）"
        return 0
    fi

    # 指纹 2：admin 的 DELETE /api/tokens 认 purge 参数
    if [ -f "$admin/tokens.go" ] && grep -q 'purge' "$admin/tokens.go"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 /api/tokens 的 DELETE 已出现 purge 语义"
        return 0
    fi

    # 指纹 3（弱）：前端在 revoked 分支里给了按钮，而不是直接留空
    local app="$tree/web/static/app.js"
    if [ -f "$app" ] && grep -q "tok.revoked[^}]*DeleteAPIToken\|tok.revoked[^}]*purgeAPIToken" "$app"; then
        echo "RESULT=SUSPECT"
        echo "REASON=上游前端在已吊销行里出现了按钮，但后端没看到删除能力，需人工确认"
        return 0
    fi

    echo "RESULT=NEEDED"
    echo "REASON=上游吊销后仍是终态，删除已吊销令牌仍需本补丁"
}

selftest() {
    local base fail=0
    base=$(mktemp -d)
    mkdir -p "$base/a/internal/storage" "$base/a/internal/admin" "$base/a/web/static"
    mkdir -p "$base/b/internal/storage" "$base/b/internal/admin" "$base/b/web/static"
    mkdir -p "$base/missing"

    # a：上游原样（只有吊销）
    printf 'package storage\nfunc revokeAPIToken() {}\n' > "$base/a/internal/storage/tokens.go"
    printf 'package admin\nfunc (h *Handler) APITokens() {}\n' > "$base/a/internal/admin/tokens.go"
    printf "const x = tok.revoked ? '' : 'Revoke';\n" > "$base/a/web/static/app.js"

    # b：上游自己加了删除
    printf 'package storage\nfunc (s *SQLiteStore) DeleteAPIToken(id uint) error { return nil }\n' > "$base/b/internal/storage/tokens.go"
    printf 'package admin\nfunc (h *Handler) APITokens() {}\n' > "$base/b/internal/admin/tokens.go"
    printf "const x = '';\n" > "$base/b/web/static/app.js"

    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "missing UNKNOWN"; do
        set -- $pair
        got=$(bash "$0" "$base/$1" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$2" ]; then
            echo "  PASS  $1 -> $got"
        else
            echo "  FAIL  $1 -> $got（期望 $2）"; fail=1
        fi
    done
    rm -rf "$base"
    if [ "$fail" -eq 0 ]; then echo "ALL PASS"; else echo "有用例失败"; return 1; fi
}

if [ "${1:-}" = "--selftest" ]; then
    selftest
else
    detect "${1:?用法: detect-token-purge.sh <tree> | --selftest}"
fi
