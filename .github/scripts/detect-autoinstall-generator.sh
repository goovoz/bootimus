#!/usr/bin/env bash
# 探测器：上游是否已自带「自动安装文件生成」能力
# 用法：detect-autoinstall-generator.sh <上游树> | --selftest
#
# 三态语义（2026-10-25 调整）：
#   IMPLEMENTED 上游连「模板目录 + 渲染接口」都照我们的形态做了 —— 可以直接退役
#   SUSPECT     上游实现了「变量渲染 / 校验 / 预览」，但没有内置模板库
#               （2026-10-24 的 PR #201/#202 就是这种：render.go + validate.go +
#                /api/autoinstall-files/render + 前端变量编辑器）。
#               能力互补，但同包同名符号会冲突，必须人工合并 —— 所以不再报 NEEDED。
#   NEEDED      上游只有存取，没有任何渲染/校验
set -uo pipefail

detect() {
    local tree="$1"
    local f="$tree/internal/admin/handlers.go"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 internal/admin/handlers.go"; return 0
    fi

    # 强信号：上游照搬了我们的接口形态（或直接有了 generate.go）
    # 注意 RenderAutoInstallFile 不算强信号 —— 那是上游自己的「用变量渲染用户自带文件」，
    # 与我们的「内置模板生成」不是同一件事，只说明能力重叠（走下面的 SUSPECT）。
    if grep -qE '"/api/autoinstall-templates"|RenderAutoInstallTemplate' "$f"; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游已提供模板目录/模板渲染接口（autoinstall-templates / RenderAutoInstallTemplate）"
        return 0
    fi
    if [ -f "$tree/internal/autoinstall/generate.go" ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 internal/autoinstall 里已有 generate.go"
        return 0
    fi

    # 弱信号：上游有渲染/校验/预览，但没有内置模板库
    local render=0 validate=0 preview=0
    [ -f "$tree/internal/autoinstall/render.go" ] && render=1
    [ -f "$tree/internal/autoinstall/validate.go" ] && validate=1
    grep -qE 'autoinstall-files/render|RenderAutoInstallFile' "$tree/internal/server/server.go" "$f" 2>/dev/null && preview=1
    if [ "$render" = 1 ] || [ "$validate" = 1 ] || [ "$preview" = 1 ]; then
        echo "RESULT=SUSPECT"
        echo "REASON=上游已有变量渲染/校验/预览（render=${render} validate=${validate} preview=${preview}）但没有内置模板库：能力互补，需人工合并并按上游命名改掉同包同名符号"
        return 0
    fi

    echo "RESULT=NEEDED"
    echo "REASON=上游只有自动安装文件的存取，没有模板生成也没有变量渲染"
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/internal/admin" "$base/b/internal/admin" "$base/missing" \
             "$base/c/internal/admin" "$base/c/internal/autoinstall" "$base/c/internal/server"
    printf 'package admin\n// ListAutoInstallFiles only\n' > "$base/a/internal/admin/handlers.go"
    printf 'package admin\n// mux.HandleFunc("/api/autoinstall-templates")\n' > "$base/b/internal/admin/handlers.go"
    printf 'package admin\n// mux.HandleFunc("/api/autoinstall-files/render")\n' > "$base/c/internal/admin/handlers.go"
    printf 'package autoinstall\nfunc Render(content string, vars map[string]string) (string, []string) { return content, nil }\n' \
        > "$base/c/internal/autoinstall/render.go"
    printf 'package autoinstall\nfunc Validate(filename, content string) error { return nil }\n' \
        > "$base/c/internal/autoinstall/validate.go"
    printf 'package server\n// mux.HandleFunc("/api/autoinstall-files/render", h)\n' > "$base/c/internal/server/server.go"

    echo "=== 探测器自检 ==="
    for pair in "a NEEDED" "b IMPLEMENTED" "c SUSPECT" "missing UNKNOWN"; do
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
