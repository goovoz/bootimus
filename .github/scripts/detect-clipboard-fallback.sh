#!/usr/bin/env bash
# 探测器：上游是否已经自己处理了「非安全上下文里 navigator.clipboard 不存在」
#
# 本 fork 的 clipboard-fallback 补丁把管理台的复制动作统一收进 copyTextToClipboard()：
# 安全上下文走 Clipboard API，否则退回 document.execCommand('copy')，再不行弹框让人手工复制。
# 上游一旦自己加了兜底，这个补丁就该退役。
set -uo pipefail

detect() {
    local tree="$1"
    local app="$tree/web/static/app.js"
    if [ ! -f "$app" ]; then
        echo "RESULT=UNKNOWN"; echo "REASON=找不到 web/static/app.js：$tree"; return 0
    fi
    if grep -q "execCommand('copy')\|execCommand(\"copy\")" "$app"; then
        echo "RESULT=IMPLEMENTED"; echo "REASON=上游 app.js 里已经有 execCommand 兜底"
    elif grep -q "navigator.clipboard" "$app"; then
        echo "RESULT=NEEDED"; echo "REASON=上游仍直接调用 navigator.clipboard，http 内网访问时按钮点了没反应"
    else
        echo "RESULT=UNKNOWN"; echo "REASON=app.js 里既没有 clipboard 调用也没有兜底，结构可能已大改"
    fi
}

selftest() {
    local base; base=$(mktemp -d); local fail=0
    mkdir -p "$base/a/web/static" "$base/b/web/static" "$base/c/web/static"
    printf 'navigator.clipboard.writeText(url);\n' > "$base/a/web/static/app.js"
    printf 'if (navigator.clipboard) {} else { document.execCommand("copy"); }\n' > "$base/b/web/static/app.js"
    printf 'console.log(1);\n' > "$base/c/web/static/app.js"
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
    echo "用法: detect-clipboard-fallback.sh <tree> | --selftest"; exit 2
fi
