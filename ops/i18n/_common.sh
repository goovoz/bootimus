#!/usr/bin/env bash
# autofix 系列的公共件：日志、交接给 DSH、写词表、复查、提交推送。
#
# autofix.sh（扫源码字面量）和 autofix-dom.sh（扫真实 DOM）只在
# 「怎么拿到待翻文本」这一步不同，后面几步完全一样。以前这些各写一份，
# 加判定规则时要改两处 —— 已经因为漏改一边出过问题。
#
# 2026-11 起「翻什么、怎么翻」不再交给 AI 网关：翻译单看一串字面量是做不好的，
# 得看调用点、看 DOM 语境、看术语在别处怎么翻。这些都在 DSH 手里。
# 于是这里只剩三类函数：
#   * 交给 DSH —— dsh_enqueue
#   * 落盘规则 —— apply_translations（只新增不覆盖，DSH 也得守这条）
#   * 推送闸门 —— verify_all / commit_and_build（DSH 干完活回来调）
#
# 用法：在脚本里 `. "$(dirname "$0")/_common.sh"`，然后调下面的函数。
# 本文件不设 set -e，由调用方决定严格程度。

: "${LOG:=/var/log/bootimus-i18n-autofix.log}"

log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }

# ---------------------------------------------------------------- 交给 DSH
# dsh_enqueue <kind> <fingerprint> <任务 JSON>
# 退出码：0 已入队（含「队列里已有同指纹」）；1 入队失败
dsh_enqueue() {
    local kind="$1" fp="$2" payload="$3"
    local tool=${DSH_HANDOFF:-${REPO:-.}/ops/dsh/dsh-handoff.sh}
    [ -r "$tool" ] || { log "  没有 $tool，跳过交班"; return 1; }
    local out
    out=$(printf '%s' "$payload" | DSH_HANDOFF_HOME="${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}" \
            bash "$tool" enqueue --kind "$kind" --fingerprint "$fp") \
        || { log "  入队失败：$out"; return 1; }
    case "$out" in
        SKIP\ *) log "  队列里已有同指纹任务：${out#SKIP }" ;;
        "")      log "  入队无输出"; return 1 ;;
        *)       log "  已交给 DSH（任务 ${out}）" ;;
    esac
    return 0
}

# ---------------------------------------------------------------- 落盘规则
# apply_translations <json_file> [dry]
#
# json 形如 {"原文":"译文"}。只新增，不覆盖已有词条 —— 人工校对过的译文
# 不许被自动流程改坏，DSH 也一样要守这条。
# 空译文、译文与原文相同的一律丢弃。
# 退出码：0 已写入 / 2 没有可新增的 / 1 失败
apply_translations() {
    local file="$1" dry="${2:-0}"
    BOOTIMUS_DRY="$dry" python3 - "$file" <<'PY'
import json, os, sys
from pathlib import Path

src = Path(sys.argv[1])
try:
    tr = json.loads(src.read_text(encoding='utf-8'))
except Exception as e:
    print('  ✗ 译文不是合法 JSON: %s' % e)
    sys.exit(1)

zh_path = Path('ops/i18n/zh.json')
zh = json.loads(zh_path.read_text(encoding='utf-8'))

added, skipped, bad = {}, [], []
for k, v in tr.items():
    if not isinstance(v, str) or not v.strip():
        bad.append((k, '空译文')); continue
    if v.strip() == k.strip():
        bad.append((k, '译文与原文相同')); continue
    if k in zh:
        skipped.append(k); continue
    added[k] = v.strip()

print('  译出 %d 条，新增 %d，跳过已有 %d，丢弃 %d' % (len(tr), len(added), len(skipped), len(bad)))
for k, why in bad[:10]:
    print('    丢弃 %s（%s）' % (k[:50], why))
for k, v in list(added.items())[:60]:
    print('    %-46s -> %s' % (k[:46], v))

if os.environ.get('BOOTIMUS_DRY') == '1':
    print('  （--dry-run，未写入）')
    sys.exit(0)
if not added:
    print('  没有可新增的')
    sys.exit(2)

zh.update(added)
zh_path.write_text(json.dumps(zh, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('  ✓ 写入 zh.json，共 %d 条' % len(zh))
PY
}

# ---------------------------------------------------------------- 推送闸门
# verify_all 推送前的三道关：i18n.js 能重建、审计归零、Go 还能编译。
verify_all() {
    log "=== 复查 ==="
    bash ops/i18n/rebuild.sh >/tmp/autofix-rebuild.log 2>&1 || { log "ERROR: rebuild 失败"; return 1; }
    node --check web/static/i18n.js || { log "ERROR: i18n.js 语法错"; return 1; }
    python3 ops/i18n/audit.py >/tmp/autofix-audit.log 2>&1
    if ! grep -q '缺失 0 条' /tmp/autofix-audit.log; then
        log "ERROR: 复查仍有缺失，放弃本轮"
        grep -A5 '缺失' /tmp/autofix-audit.log >> "$LOG"
        return 1
    fi
    CGO_ENABLED=0 go build ./... >/tmp/autofix-build.log 2>&1 || { log "ERROR: go build 失败"; return 1; }
    log "  ✓ 审计归零 + 构建通过"
}

# commit_and_build <标题> [--no-push]
#
# 提交、推送、触发构建。任何一个失败都不静默 —— 改动留在本地等人工看。
commit_and_build() {
    local title="$1" nopush="${2:-}"

    if [ "$nopush" = "--no-push" ]; then
        log "--no-push：改动留在工作区"
        git --no-pager diff --stat | tail -3 >> "$LOG"
        return 0
    fi
    if git diff --quiet -- ops/i18n/zh.json web/static/i18n.js; then
        log "工作区无变化"
        return 0
    fi

    git add ops/i18n/zh.json web/static/i18n.js
    git commit -q -F - <<MSG
$title

由 DSH 按 ops/dsh 的交接流程生成：审计发现缺失 → DSH 翻译（带仓库上下文和
术语表）→ 只新增不覆盖 → 复查（审计归零 + i18n.js 可复现 + go build）通过后提交。

Fork-Patch: i18n-zh-full
MSG
    local sha
    sha=$(git rev-parse --short HEAD)
    log "  已提交 $sha"

    if git push -q origin HEAD:main; then
        # push 到 main 会自动触发构建（fork-release 监听了 branches: main），
        # 这里不再手动 dispatch —— 两处都发会构建两遍。
        log "  已推送 $sha（构建由 push 自动触发）"
    else
        log "ERROR: 推送失败（改动已在本地 $sha）"
        return 1
    fi
}
