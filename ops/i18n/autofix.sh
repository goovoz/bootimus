#!/usr/bin/env bash
# 汉化自动修复：审计发现缺失 → LLM 翻译 → 写入词表 → 复查 → 提交推送 → 触发构建
#
# 设计原则：
#   · 只新增，不覆盖已有词条 —— LLM 不能改坏已经人工校对过的译文
#   · 任何一步失败都不推送（宁可什么都不做，也不留半成品）
#   · 推送前必须复查：审计归零 + i18n.js 可复现 + go build 通过
#   · 每轮有条数上限，防止一次灌进几百条没人看得过来
#
# 用法：
#   bash ops/i18n/autofix.sh              # 完整流程
#   bash ops/i18n/autofix.sh --dry-run    # 只翻译不写入
#   bash ops/i18n/autofix.sh --no-push    # 写本地，不提交
#
# 依赖：AI_BASE_URL / AI_API_KEY / AI_MODEL / AI_EFFORT，以及推送用的 GH_TOKEN
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

# 配置：AI 网关 + PAT
[ -r /root/ai_gateway.env ] && { set -a; . /root/ai_gateway.env; set +a; }
AI_MODEL=${AI_MODEL:-cn:deepseek-v4.1-flash}
AI_EFFORT=${AI_EFFORT:-max}
AI_TIMEOUT=${AI_TIMEOUT:-180}
GH_TOKEN=${GH_TOKEN:-$(cat /root/gh_pat.txt 2>/dev/null || true)}
SC_SENDKEY=${SC_SENDKEY:-}
export TZ=${TZ:-Asia/Shanghai}

LOG=${LOG:-/var/log/bootimus-i18n-autofix.log}
log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }

cd "$REPO" || { log "ERROR: 进不去 $REPO"; exit 1; }

[ -n "${AI_BASE_URL:-}" ] || { log "跳过：没配 AI_BASE_URL"; exit 0; }
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
INPUT_JSON=$(printf '%s\n' "$MISSING" | jq -R . | jq -sc .)
SYS='你在给一个 PXE 网络启动管理台做中文翻译。规则：
1. 只翻译 UI 文案；技术标识保持原文：PXE、iPXE、UEFI、BIOS、NFS、SMB、TFTP、HTTP、API、JSON、MAC、IP、URL、ISO、DHCP、Kubernetes、Webhook、netboot、Clonezilla、memdisk、initrd
2. 产品名不翻：Bootimus、Docker Hub、GitHub、Redfish、Intel AMT
3. 术语统一：image→镜像，client→客户端，boot→启动，bootloader→引导加载器，profile→配置，group→分组，token→令牌，audit→审计，housekeeping→日常维护，disk image→磁盘镜像，enrollment→纳管
4. 保留原有标点和语气，末尾句号保留；不要出现「管理管理」这种重复
5. 严格输出 JSON 对象，键是原文，值是译文，不要解释、不要 markdown 代码块'

BODY=$(jq -n --arg m "$AI_MODEL" --arg s "$SYS" --argjson u "$INPUT_JSON" \
  --arg e "$AI_EFFORT" '{model:$m,temperature:0,max_tokens:8000,reasoning_effort:$e,
    messages:[{role:"system",content:$s},{role:"user",content:($u|tojson)}]}')

RESP=$(curl -sS --max-time "$AI_TIMEOUT" -X POST "${AI_BASE_URL%/}/chat/completions" \
  -H "Authorization: Bearer ${AI_API_KEY:-placeholder}" -H 'Content-Type: application/json' \
  -d "$BODY" 2>/dev/null)

CONTENT=$(printf '%s' "$RESP" | jq -r '.choices[0].message.content // empty' 2>/dev/null)
if [ -z "$CONTENT" ]; then
    log "ERROR: LLM 无输出，退出（不写任何东西）"
    printf '%s' "$RESP" | head -c 300 >> "$LOG"; echo >> "$LOG"
    exit 1
fi

printf '%s' "$RESP" | jq -r '"  用量: 输入=\(.usage.prompt_tokens) 思考=\(.usage.completion_thinking_tokens // 0) 输出=\(.usage.completion_tokens) credit=\(.usage.credit // 0)"' >> "$LOG" 2>/dev/null

# ---------- 3. 校验并写入（只新增）----------
LOG_FILE=$LOG DRY=$DRY_RUN python3 - "$CONTENT" <<'PY'
import json, os, re, sys
from pathlib import Path

raw = sys.argv[1].strip()
if raw.startswith('```'):
    raw = re.sub(r'^```[a-z]*\n?', '', raw)
    raw = re.sub(r'\n?```$', '', raw)
try:
    tr = json.loads(raw)
except Exception as e:
    print('  ✗ 译文不是合法 JSON: %s' % e)
    sys.exit(1)

map_path = Path('ops/i18n/zh.json')
zh = json.loads(map_path.read_text(encoding='utf-8'))

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

if os.environ.get('DRY') == '1':
    print('  （--dry-run，未写入）')
    sys.exit(0)

if not added:
    print('  没有需要新增的，退出')
    sys.exit(2)

zh.update(added)
map_path.write_text(json.dumps(zh, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('  ✓ 写入 zh.json，共 %d 条' % len(zh))
PY
WRITE_RC=$?
[ "$WRITE_RC" -eq 2 ] && { log "无可新增，退出"; exit 0; }
[ "$WRITE_RC" -ne 0 ] && { log "ERROR: 写入失败（rc=$WRITE_RC），不推送"; exit 1; }
[ "$DRY_RUN" -eq 1 ] && { log "dry-run 结束"; exit 0; }

# ---------- 4. 复查 ----------
log "=== 复查 ==="
bash ops/i18n/rebuild.sh >/tmp/autofix-rebuild.log 2>&1 || { log "ERROR: rebuild 失败"; exit 1; }
node --check web/static/i18n.js || { log "ERROR: i18n.js 语法错"; exit 1; }

python3 ops/i18n/audit.py >/tmp/autofix-audit.log 2>&1
if ! grep -q '缺失 0 条' /tmp/autofix-audit.log; then
    log "ERROR: 复查仍有缺失，放弃本轮"
    grep -A5 '缺失' /tmp/autofix-audit.log >> "$LOG"
    exit 1
fi
log "  ✓ 审计归零"

CGO_ENABLED=0 go build ./... >/tmp/autofix-build.log 2>&1 || { log "ERROR: go build 失败"; exit 1; }
log "  ✓ go build 通过"

# ---------- 5. 提交推送 ----------
if [ "$NO_PUSH" -eq 1 ]; then
    log "--no-push：改动留在工作区"
    git --no-pager diff --stat | tail -3 >> "$LOG"
    exit 0
fi

if git diff --quiet -- ops/i18n/zh.json web/static/i18n.js; then
    log "工作区无变化，退出"
    exit 0
fi

ADDED_N=$(git --no-pager diff --numstat -- ops/i18n/zh.json | awk '{print $1}')
git add ops/i18n/zh.json web/static/i18n.js
git commit -q -F - <<MSG
i18n: 自动补 $(printf '%s' "$COUNT") 条界面文案

由 ops/i18n/autofix.sh 自动生成：审计发现缺失 → LLM 翻译 → 只新增不覆盖 →
复查（审计归零 + i18n.js 可复现 + go build）通过后提交。

Fork-Patch: i18n-zh-full
MSG
SHA=$(git rev-parse --short HEAD)
log "  已提交 $SHA"

if git push -q origin HEAD:main; then
    log "  已推送 $SHA"
    if [ -n "$GH_TOKEN" ]; then
        code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
          -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/vnd.github+json' \
          -d '{"ref":"main"}' \
          "https://api.github.com/repos/goovoz/bootimus/actions/workflows/fork-release.yml/dispatches")
        log "  触发构建 HTTP $code"
    fi
else
    log "ERROR: 推送失败（改动已在本地 $SHA）"
    exit 1
fi

# ---------- 6. 通知 ----------
if [ -n "$SC_SENDKEY" ]; then
    curl -sS --max-time 15 -X POST "https://sctapi.ftqq.com/$SC_SENDKEY.send" \
      --data-urlencode "title=bootimus 自动补翻译 $COUNT 条" \
      --data-urlencode "desp=提交 \`$SHA\`，已触发构建。详情见 /var/log/bootimus-i18n-autofix.log" \
      >/dev/null && log "  已推送通知"
fi

log "=== 完成 ==="
