#!/usr/bin/env bash
# 汉化自动修复（静态）：审计源码字面量 → LLM 翻译 → 写入词表 → 复查 → 提交推送
#
# 这一步只看 web/static 里的字面量（index.html / app.js / fork-ui.js，以及
# generate.go 里模板字段的 Label 与 Help）。服务端返回的句子和模板拼出来的文本
# 静态扫不到，归 autofix-dom.sh。
#
# 用法：
#   bash ops/i18n/autofix.sh              # 完整流程
#   bash ops/i18n/autofix.sh --dry-run    # 只翻译不写入
#   bash ops/i18n/autofix.sh --no-push    # 写入但不提交
set -uo pipefail

REPO=${REPO:-/root/work/bootimus}
MAX_BATCH=${MAX_BATCH:-40}
DRY_RUN=0
NO_PUSH=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --no-push) NO_PUSH=1 ;;
        *) echo "未知参数: $a"; exit 2 ;;
    esac
done

cd "$REPO" || { echo "进不去 $REPO" >&2; exit 1; }
. "$(dirname "$0")/_common.sh"

export TZ=${TZ:-Asia/Shanghai}
load_ai_env || exit 0
command -v jq >/dev/null || { log "ERROR: 缺 jq"; exit 1; }

# ---------- 1. 审计 ----------
log "=== 审计 ==="
AUDIT_JSON=$(python3 ops/i18n/audit.py --json 2>/dev/null) || true
MISSING=$(printf '%s' "$AUDIT_JSON" | jq -r '.missing[]?' 2>/dev/null)
COUNT=$(printf '%s\n' "$MISSING" | grep -c . || true)
log "缺失 $COUNT 条"

if [ "$COUNT" -eq 0 ]; then
    log "没有缺失，退出"
    exit 0
fi
if [ "$COUNT" -gt "$MAX_BATCH" ]; then
    log "超出单轮上限 $MAX_BATCH，本轮只处理前 $MAX_BATCH 条"
    MISSING=$(printf '%s\n' "$MISSING" | head -n "$MAX_BATCH")
    COUNT=$MAX_BATCH
fi
printf '%s\n' "$MISSING" | sed 's/^/  - /' >> "$LOG"

# ---------- 2. 翻译 ----------
log "=== 调 LLM 翻译（$AI_MODEL / effort=$AI_EFFORT）==="
SYS='你在给一个 PXE 网络启动管理台做中文翻译。规则：
1. 只翻译 UI 文案；技术标识保持原文：PXE、iPXE、UEFI、BIOS、NFS、SMB、TFTP、HTTP、API、JSON、MAC、IP、URL、ISO、DHCP、Kubernetes、Webhook、netboot、Clonezilla、memdisk、initrd
2. 产品名不翻：Bootimus、Docker Hub、GitHub、Redfish、Intel AMT
3. 术语统一：image→镜像，client→客户端，boot→启动，bootloader→引导加载器，profile→配置，group→分组，token→令牌，audit→审计，housekeeping→日常维护，disk image→磁盘镜像，enrollment→纳管
4. 保留原有标点和语气，末尾句号保留；不要出现「管理管理」这种重复
5. 严格输出 JSON 对象，键是原文，值是译文，不要解释、不要 markdown 代码块'

INPUT_JSON=$(printf '%s\n' "$MISSING" | jq -R . | jq -sc .)
RAW=$(llm_call "$SYS" "$INPUT_JSON" 8000)
if [ -z "$RAW" ]; then
    log "ERROR: LLM 无输出，退出（不写任何东西）"
    exit 1
fi

# ---------- 3. 写入（只新增）----------
json_extract "$RAW" > /tmp/autofix-translations.json
apply_translations /tmp/autofix-translations.json "$DRY_RUN"
case $? in
    0) ;;
    2) log "无可新增，退出"; exit 0 ;;
    *) log "ERROR: 写入失败，不推送"; exit 1 ;;
esac
[ "$DRY_RUN" -eq 1 ] && { log "dry-run 结束"; exit 0; }

# ---------- 4. 复查 + 提交 ----------
verify_all || exit 1
if [ "$NO_PUSH" -eq 1 ]; then
    commit_and_build "i18n: 自动补 $COUNT 条界面文案" --no-push || exit 1
else
    commit_and_build "i18n: 自动补 $COUNT 条界面文案" || exit 1
fi

# ---------- 5. 通知 ----------
if [ -n "${SC_SENDKEY:-}" ]; then
    curl -sS --max-time 15 -X POST "https://sctapi.ftqq.com/$SC_SENDKEY.send" \
        --data-urlencode "title=bootimus 自动补翻译 $COUNT 条" \
        --data-urlencode "desp=已触发构建。详情见 /var/log/bootimus-i18n-autofix.log" \
        >/dev/null && log "  已推送通知"
fi
log "=== 完成 ==="
