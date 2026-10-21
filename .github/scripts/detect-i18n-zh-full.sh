#!/usr/bin/env bash
# detect-i18n-zh-full.sh — 判定上游是否已自行补齐管理台的中文覆盖
#
# 用法：
#   detect-i18n-zh-full.sh <upstream-tree>   # 对【上游代码】跑，输出 RESULT=/REASON=
#   detect-i18n-zh-full.sh --selftest        # 离线自检三种状态
#
# 判定口径：
#   IMPLEMENTED  zh-CN 字典 >= 300 条（上游把界面文案都收进字典了），
#                或上游已出现「英文原文 → 译文」的自动翻译机制（SOURCE_TEXT / MutationObserver）
#   SUSPECT      字典在 200~300 之间，疑似补了一半
#   NEEDED       仍只覆盖静态骨架（<= 200 条），fork 补丁继续保留
#   UNKNOWN      上游 tree 里找不到 web/static/i18n.js（路径可能被重构）
set -uo pipefail

ZH_FILE="web/static/i18n.js"
MIN_FULL=${MIN_FULL:-300}
MIN_HALF=${MIN_HALF:-200}

count_zh_keys() {
    # 只数 'zh-CN': { ... } 这一段里的 key
    sed -n "/'zh-CN': {/,/^    }/p" "$1" 2>/dev/null \
        | grep -cE "^[[:space:]]+'[A-Za-z0-9_.-]+':"
}

detect() {
    local tree="${1:-}"
    if [ -z "$tree" ]; then
        echo "RESULT=UNKNOWN"
        echo "REASON=没有传入 upstream tree 路径"
        return 0
    fi
    local file="$tree/$ZH_FILE"
    if [ ! -f "$file" ]; then
        echo "RESULT=UNKNOWN"
        echo "REASON=upstream tree 中找不到 $ZH_FILE（路径可能被上游重构）"
        return 0
    fi

    local n
    n=$(count_zh_keys "$file")
    n=${n:-0}

    local auto=0
    if grep -qE "SOURCE_TEXT|MutationObserver" "$file"; then
        auto=1
    fi

    if [ "$auto" -eq 1 ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 i18n.js 已出现原文→译文的自动翻译机制（SOURCE_TEXT / MutationObserver），zh-CN 字典 ${n} 条"
    elif [ "$n" -ge "$MIN_FULL" ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游 zh-CN 字典已达 ${n} 条（阈值 ${MIN_FULL}），说明界面文案已整体收进字典"
    elif [ "$n" -ge "$MIN_HALF" ]; then
        echo "RESULT=SUSPECT"
        echo "REASON=上游 zh-CN 字典 ${n} 条，处于 ${MIN_HALF}~${MIN_FULL} 之间，疑似补了一半，需人工确认"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游 zh-CN 字典仅 ${n} 条（阈值 ${MIN_HALF}），仍只覆盖静态骨架，补丁继续保留"
    fi
}

selftest() {
    local tmp rc=0
    tmp=$(mktemp -d)
    mkdir -p "$tmp/full/web/static" "$tmp/half/web/static" "$tmp/none/web/static" "$tmp/auto/web/static"

    # NEEDED：只有 3 条
    {
        echo "const TRANSLATIONS = {"
        echo "    'en-GB': {"
        echo "        'nav.a': 'A',"
        echo "    },"
        echo "    'zh-CN': {"
        echo "        'nav.a': '甲',"
        echo "        'nav.b': '乙',"
        echo "        'nav.c': '丙',"
        echo "    },"
        echo "};"
    } > "$tmp/none/web/static/i18n.js"

    # SUSPECT：250 条
    {
        echo "const TRANSLATIONS = {"
        echo "    'zh-CN': {"
        for i in $(seq 1 250); do echo "        'k.$i': '值$i',"; done
        echo "    },"
        echo "};"
    } > "$tmp/half/web/static/i18n.js"

    # IMPLEMENTED（靠条数）：350 条
    {
        echo "const TRANSLATIONS = {"
        echo "    'zh-CN': {"
        for i in $(seq 1 350); do echo "        'k.$i': '值$i',"; done
        echo "    },"
        echo "};"
    } > "$tmp/full/web/static/i18n.js"

    # IMPLEMENTED（靠机制）：只有 10 条但有自动翻译
    {
        echo "const SOURCE_TEXT = { 'zh-CN': {} };"
        echo "const TRANSLATIONS = {"
        echo "    'zh-CN': {"
        for i in $(seq 1 10); do echo "        'k.$i': '值$i',"; done
        echo "    },"
        echo "};"
        echo "new MutationObserver(() => {});"
    } > "$tmp/auto/web/static/i18n.js"

    check() {
        local label="$1" tree="$2" want="$3"
        local got
        got=$(detect "$tree" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$want" ]; then
            echo "  PASS  $label -> $got"
        else
            echo "  FAIL  $label -> 期望 $want，实际 $got"
            rc=1
        fi
    }

    echo "=== 自检 ==="
    check "只有骨架字典"       "$tmp/none" NEEDED
    check "补了一半(250)"      "$tmp/half" SUSPECT
    check "字典够全(350)"      "$tmp/full" IMPLEMENTED
    check "有自动翻译机制"     "$tmp/auto" IMPLEMENTED
    check "路径不存在"         "$tmp/missing" UNKNOWN
    rm -rf "$tmp"
    if [ "$rc" -eq 0 ]; then echo "ALL PASS"; else echo "有失败项"; fi
    return "$rc"
}

case "${1:-}" in
    --selftest) selftest ;;
    *) detect "${1:-}" ;;
esac
