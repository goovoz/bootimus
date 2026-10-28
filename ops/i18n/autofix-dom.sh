#!/usr/bin/env bash
# DOM 版汉化自动修复：起实例 → 扫真实 DOM → AI 判断该不该翻 → 翻译 → 写入 → 复查 → 提交
#
# 与 autofix.sh 的分工：
#   autofix.sh     只看源码字面量（index.html / app.js），轻、快、进 CI
#   autofix-dom.sh 看真实渲染结果，能抓到服务端返回的句子和运行时拼出来的文本
#                  （API 参考页的参数说明就是这么露出来的），重，每天跑一次
#
# 为什么必须让 AI 判断：DOM 里 200 多条"英文"绝大多数不该翻 —— 发行版名、内核参数、
# 文件大小、启动器文件名、语言选项。靠正则过滤会一直误伤，实测 LLM 分类准确率 15/16。
#
# 用法：
#   bash ops/i18n/autofix-dom.sh --dry-run    # 扫描 + 判断，不写不推
#   bash ops/i18n/autofix-dom.sh --no-push    # 写本地不推
#   bash ops/i18n/autofix-dom.sh              # 完整流程
set -uo pipefail

REPO=${REPO:-/root/work/bootimus}
PORT=${PORT:-18081}
HTTP_PORT=${HTTP_PORT:-18080}
TFTP_PORT=${TFTP_PORT:-1069}
BATCH=${BATCH:-40}
DRY_RUN=0
NO_PUSH=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --no-push) NO_PUSH=1 ;;
        *) echo "未知参数: $a"; exit 2 ;;
    esac
done

[ -r /root/ai_gateway.env ] && { set -a; . /root/ai_gateway.env; set +a; }
AI_MODEL=${AI_MODEL:-cn:deepseek-v4.1-flash}
AI_EFFORT=${AI_EFFORT:-max}
AI_TIMEOUT=${AI_TIMEOUT:-240}
GH_TOKEN=${GH_TOKEN:-$(cat /root/gh_pat.txt 2>/dev/null || true)}
export TZ=${TZ:-Asia/Shanghai}
LOG=${LOG:-/var/log/bootimus-i18n-autofix.log}
log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }

cd "$REPO" || { log "ERROR: 进不去 $REPO"; exit 1; }
[ -n "${AI_BASE_URL:-}" ] || { log "跳过：没配 AI_BASE_URL"; exit 0; }

# ---------- 1. 起临时实例 ----------
log "=== 起临时实例 ==="
# 清掉所有 bm- 开头的实例（之前几轮的测试进程会占着 TFTP 端口）
pids=$(ps -eo pid=,comm= | awk '$2 ~ /^bm-/ {print $1}')
if [ -n "$pids" ]; then echo "$pids" | xargs -r kill 2>/dev/null; sleep 2; fi
if ss -lun 2>/dev/null | grep -q ":$TFTP_PORT "; then
    log "ERROR: TFTP 端口 $TFTP_PORT 仍被占用，换 TFTP_PORT= 再跑"
    exit 1
fi

CGO_ENABLED=0 go build -o /tmp/bm-autofix-dom . || { log "ERROR: 编译失败"; exit 1; }
rm -rf /tmp/autofix-dom-data && mkdir -p /tmp/autofix-dom-data
BOOTIMUS_DATA_DIR=/tmp/autofix-dom-data BOOTIMUS_ADMIN_PORT="$PORT" BOOTIMUS_HTTP_PORT="$HTTP_PORT" \
BOOTIMUS_TFTP_PORT="$TFTP_PORT" BOOTIMUS_SERVER_ADDR=127.0.0.1 \
nohup /tmp/bm-autofix-dom serve > /tmp/bm-autofix-dom.log 2>&1 &
sleep 8
PW=$(grep -oP 'Password: \K\S+' /tmp/bm-autofix-dom.log | head -1)
[ -n "$PW" ] || { log "ERROR: 实例没起来"; tail -5 /tmp/bm-autofix-dom.log >> "$LOG"; exit 1; }

# 造点数据，否则空表格扫不出东西
TOK=$(curl -sS -X POST "http://127.0.0.1:$PORT/api/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" 2>/dev/null \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)
for i in 1 2 3; do
  curl -sS -X POST "http://127.0.0.1:$PORT/api/clients" -H "Authorization: Bearer $TOK" \
    -H 'Content-Type: application/json' -d "{\"mac\":\"aa:bb:cc:dd:ee:0$i\",\"hostname\":\"lab-0$i\"}" >/dev/null 2>&1
done
log "  实例就绪 :$PORT"

# ---------- 2. 扫描 ----------
log "=== DOM 扫描 ==="
BASE="http://127.0.0.1:$PORT" ADMIN_PW="$PW" \
PW_MODULE="$REPO/ui-tests/node_modules/playwright" \
node ops/i18n/audit-dom.js > /tmp/dom-out.json 2>/tmp/dom-out.err
if [ ! -s /tmp/dom-out.json ]; then
    log "ERROR: 扫描无输出"; head -3 /tmp/dom-out.err >> "$LOG"
    echo "$pids" | xargs -r kill; exit 1
fi
TOTAL=$(jq '.missing | length' /tmp/dom-out.json)
log "  扫到英文 $TOTAL 条"

# 过滤掉翻译表已有的（用 Python 做差集，比 jq 的 slurpfile + process substitution 稳）
python3 - <<'PY'
import json
from pathlib import Path
dom = json.load(open('/tmp/dom-out.json', encoding='utf-8'))
zh = json.loads(Path('ops/i18n/zh.json').read_text(encoding='utf-8'))
new = [t for t in dom.get('missing', []) if t not in zh]
json.dump(new, open('/tmp/dom-new.json', 'w', encoding='utf-8'), ensure_ascii=False)
print('  其中翻译表没有的 %d 条' % len(new))
PY
NEW=$(jq 'length' /tmp/dom-new.json)
log "  待判断 $NEW 条"

# 实例用完就关，别占着端口
pids=$(ps -eo pid=,comm= | awk '$2 ~ /^bm-/ {print $1}'); [ -n "$pids" ] && echo "$pids" | xargs -r kill 2>/dev/null

if [ "$NEW" -eq 0 ]; then log "没有新增，退出"; exit 0; fi

# ---------- 3. AI 判断 + 翻译 ----------
log "=== AI 判断（分 $(( (NEW + BATCH - 1) / BATCH )) 批，每批 $BATCH 条）==="
SYS='你在审查一个 PXE 网络启动管理台界面上残留的英文。逐条判断要不要翻成中文。

该翻（translate）：给人看的说明、提示、参数解释、按钮、表头、状态。
不该翻（skip）：
- 发行版/系统名：AlmaLinux、Debian、Arch Linux、openSUSE、WinPE
- 发行版标识或匹配模式：alma, almalinux / debian, truenas / redhat
- 内核命令行参数：initrd=initrd ... / root=live / modules=loop,squashfs
- 文件名：ipxe.efi、undionly.kpxe、manifest.json、bootimus.efi
- 文件大小/数字+单位：1 MB、387.85 KB、223 B、8s
- 版本号：v0.1.79、dev、vdev
- 语言选项（带国旗）：🇬🇧 English (UK)
- 系统信息：ubuntu 24.04、x86_64
- 技术标识：PXE、iPXE、UEFI、SMB、NFS、GET、PUT、HTTP、API
- 中英混排且中文部分已足够：BMC：/ iPXE SAN URI：

术语统一：image→镜像，client→客户端，boot→启动，bootloader→引导加载器，group→分组，token→令牌，audit→审计，disk image→磁盘镜像。

严格输出 JSON 数组，每项 {"text":"原文","action":"translate"|"skip","zh":"译文（skip 时为空字符串）"}，不要解释、不要代码块。'

> /dev/null 2>&1
echo '[]' > /tmp/dom-translated.json
rm -f /tmp/dom-translated.json.part
TOTAL_BATCH=$(( (NEW + BATCH - 1) / BATCH ))
for b in $(seq 0 $((TOTAL_BATCH - 1))); do
    CHUNK=$(jq -c ".[$((b*BATCH)):$(( (b+1)*BATCH ))]" /tmp/dom-new.json)
    N=$(printf '%s' "$CHUNK" | jq 'length')
    body=$(jq -n --arg m "$AI_MODEL" --arg s "$SYS" --argjson u "$CHUNK" --arg e "$AI_EFFORT" \
      '{model:$m,temperature:0,max_tokens:8000,reasoning_effort:$e,
        messages:[{role:"system",content:$s},{role:"user",content:($u|tojson)}]}')
    resp=$(curl -sS --max-time "$AI_TIMEOUT" -X POST "${AI_BASE_URL%/}/chat/completions" \
      -H "Authorization: Bearer ${AI_API_KEY:-placeholder}" -H 'Content-Type: application/json' -d "$body" 2>/dev/null)
    content=$(printf '%s' "$resp" | jq -r '.choices[0].message.content // empty' 2>/dev/null)
    if [ -z "$content" ]; then
        log "  第 $((b+1)) 批无输出，跳过"
        continue
    fi
    printf '%s' "$resp" | jq -r '"  第 \('"$((b+1))"') 批 usage: 输入=\(.usage.prompt_tokens) 思考=\(.usage.completion_thinking_tokens // 0) credit=\(.usage.credit // 0)"' >> "$LOG" 2>/dev/null
    printf '%s' "$content" | python3 -c '
import json, re, sys
raw = sys.stdin.read().strip()
if raw.startswith("```"):
    raw = re.sub(r"^```[a-z]*\n?", "", raw); raw = re.sub(r"\n?```$", "", raw)
try:
    arr = json.loads(raw)
    print(json.dumps([x for x in arr if isinstance(x, dict)], ensure_ascii=False))
except Exception:
    print("[]")
' >> /tmp/dom-translated.json.part
done

# 合并各批结果
python3 - <<'PY'
import json, os
merged = []
p = '/tmp/dom-translated.json.part'
if os.path.exists(p):
    for line in open(p, encoding='utf-8'):
        line = line.strip()
        if not line: continue
        try: merged.extend(json.loads(line))
        except Exception: pass
open('/tmp/dom-translated.json', 'w', encoding='utf-8').write(json.dumps(merged, ensure_ascii=False))
tr = [x for x in merged if x.get('action') == 'translate' and x.get('zh')]
print('  AI 判断：共 %d 条，该翻 %d 条，跳过 %d 条' % (len(merged), len(tr), len(merged) - len(tr)))
PY

# ---------- 4. 写入（只新增）----------
DRY=$DRY_RUN python3 - <<'PY'
import json, os, sys
from pathlib import Path

tr = json.load(open('/tmp/dom-translated.json', encoding='utf-8'))
zh_path = Path('ops/i18n/zh.json')
zh = json.loads(zh_path.read_text(encoding='utf-8'))

added, skipped = {}, []
for it in tr:
    if it.get('action') != 'translate':
        continue
    k, v = (it.get('text') or '').strip(), (it.get('zh') or '').strip()
    if not k or not v or v == k:
        continue
    if k in zh:
        skipped.append(k); continue
    added[k] = v

print('  新增 %d 条，翻译表已有 %d 条' % (len(added), len(skipped)))
for k, v in list(added.items())[:40]:
    print('    %-52s -> %s' % (k[:52], v))

if os.environ.get('DRY') == '1':
    print('  （--dry-run，未写入）')
    sys.exit(0)
if not added:
    print('  没有可新增的')
    sys.exit(2)

zh.update(added)
zh_path.write_text(json.dumps(zh, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('  ✓ 写入，共 %d 条' % len(zh))
PY
WRITE_RC=$?
[ "$WRITE_RC" -eq 2 ] && { log "无可新增，退出"; exit 0; }
[ "$WRITE_RC" -ne 0 ] && { log "ERROR: 写入失败"; exit 1; }
[ "$DRY_RUN" -eq 1 ] && { log "dry-run 结束"; exit 0; }

# ---------- 5. 复查 ----------
log "=== 复查 ==="
bash ops/i18n/rebuild.sh >/tmp/dom-rebuild.log 2>&1 || { log "ERROR: rebuild 失败"; exit 1; }
node --check web/static/i18n.js || { log "ERROR: 语法错"; exit 1; }
python3 ops/i18n/audit.py >/tmp/dom-audit.log 2>&1
grep -q '缺失 0 条' /tmp/dom-audit.log || { log "ERROR: 静态审计仍有缺失"; exit 1; }
CGO_ENABLED=0 go build ./... >/tmp/dom-build.log 2>&1 || { log "ERROR: go build 失败"; exit 1; }
log "  ✓ 审计归零 + 构建通过"

# ---------- 6. 提交 ----------
if [ "$NO_PUSH" -eq 1 ]; then log "--no-push，改动留在工作区"; exit 0; fi
git diff --quiet -- ops/i18n/zh.json web/static/i18n.js && { log "无变化"; exit 0; }

git add ops/i18n/zh.json web/static/i18n.js
git commit -q -F - <<'MSG'
i18n: 按 DOM 扫描结果自动补一批（AI 判断 + 翻译）

由 ops/i18n/autofix-dom.sh 生成：起临时实例 → Playwright 扫真实 DOM →
LLM 逐条判断该不该翻（实测分类准确率 15/16）→ 只写 translate 的 → 复查通过后提交。

静态审计看不到的文本靠这一步兜：服务端返回的句子、API 参考页的参数说明
（(Maximum entries, default 100). 这类）、运行时拼出来的状态文本。

Fork-Patch: i18n-zh-full
MSG
SHA=$(git rev-parse --short HEAD)
log "  已提交 $SHA"

if git push -q origin HEAD:main; then
    log "  已推送 $SHA"
    [ -n "$GH_TOKEN" ] && curl -sS -o /dev/null -w '' -X POST \
      -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/vnd.github+json' \
      -d '{"ref":"main"}' \
      "https://api.github.com/repos/goovoz/bootimus/actions/workflows/fork-release.yml/dispatches" \
      && log "  已触发构建"
else
    log "ERROR: 推送失败"
    exit 1
fi
log "=== 完成 ==="
