#!/usr/bin/env bash
# 汉化缺失审计（静态）→ 交给 DSH 补
#
# 这一步只看 web/static 里的字面量（index.html / app.js / fork-ui.js，以及
# generate.go 里模板字段的 Label 与 Help）。服务端返回的句子和模板拼出来的文本
# 静态扫不到，归 autofix-dom.sh。
#
# 2026-11 起不再在这里调 AI 网关：翻译只给一串字面量是做不好的（看不到调用点、
# 看不到已有术语怎么用），而且原来的流程把「翻译」和「写入/复查/推送」绑死在
# 一个 systemd oneshot 里，出错只有一个退出码。现在这一步只做审计，然后把
# 缺失清单交给 ops/dsh 的队列，由 DSH 领走 —— 它能读仓库、能对照语气、
# 写完自己跑复查，再把结果写回任务。
#
# 用法：
#   bash ops/i18n/autofix.sh              # 审计 + 入队
#   bash ops/i18n/autofix.sh --dry-run    # 只审计，不入队
#   bash ops/i18n/autofix.sh --no-push    # 兼容旧参数，等价于不加（现在不写文件）
set -uo pipefail

REPO=${REPO:-/root/work/bootimus}
MAX_BATCH=${MAX_BATCH:-40}
DRY_RUN=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --no-push) : ;;   # 入队流程不写仓库，参数保留只为兼容旧的调用方式
        *) echo "未知参数: $a"; exit 2 ;;
    esac
done

cd "$REPO" || { echo "进不去 $REPO" >&2; exit 1; }
. "$(dirname "$0")/_common.sh"

export TZ=${TZ:-Asia/Shanghai}
command -v jq >/dev/null || { log "ERROR: 缺 jq"; exit 1; }

# ---------- 1. 审计 ----------
log "=== 审计（静态）==="
AUDIT_JSON=$(python3 ops/i18n/audit.py --json 2>/dev/null) || true
MISSING=$(printf '%s' "$AUDIT_JSON" | jq -r '.missing[]?' 2>/dev/null)
COUNT=$(printf '%s\n' "$MISSING" | grep -c . || true)
log "缺失 $COUNT 条"

if [ "$COUNT" -eq 0 ]; then
    log "没有缺失，退出"
    exit 0
fi
if [ "$COUNT" -gt "$MAX_BATCH" ]; then
    log "超出单轮上限 $MAX_BATCH，本轮只交前 $MAX_BATCH 条"
    MISSING=$(printf '%s\n' "$MISSING" | head -n "$MAX_BATCH")
    COUNT=$MAX_BATCH
fi
printf '%s\n' "$MISSING" | sed 's/^/  - /' >> "$LOG"

# ---------- 2. 交给 DSH ----------
if [ "$DRY_RUN" -eq 1 ]; then
    log "dry-run：只审计，不入队"
    exit 0
fi

FP="i18n-gap/static/$(printf '%s' "$MISSING" | md5sum | cut -c1-12)"
TASK=$(jq -n --arg repo "$REPO" --argjson n "$COUNT" --arg missing "$MISSING" '
  {kind:"i18n-gap", priority:3,
   source:{script:"ops/i18n/autofix.sh", scope:"static"},
   signal:{missing:$n, scope:"web/static"},
   repo:{workdir:$repo, branch:"main"},
   evidence:{missing_list:($missing | split("\n") | map(select(length > 0)))},
   playbook:"ops/dsh/HANDOFF-PROTOCOL.md#i18n-gap"}') || { log "ERROR: 构造任务失败"; exit 1; }

log "=== 交给 DSH ==="
if ! dsh_enqueue i18n-gap "$FP" "$TASK"; then
    log "ERROR: 入队失败，本次缺失没有交代出去"
    exit 1
fi
log "=== 完成（翻译由 DSH 接手）==="
