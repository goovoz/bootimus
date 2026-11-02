#!/usr/bin/env bash
# 汉化缺失审计（DOM 版）：起实例 → 扫真实 DOM → 交给 DSH 判断该不该翻
#
# 与 autofix.sh 的分工：
#   autofix.sh     只看源码字面量，轻、快、进 CI
#   autofix-dom.sh 看真实渲染结果，能抓到服务端返回的句子和运行时拼出来的文本
#                  （API 参考页的参数说明就是这么露出来的），重，每天跑一次
#
# 为什么这一步必须有人判断：DOM 里 200 多条「英文」绝大多数不该翻 —— 发行版名、
# 内核参数、文件大小、启动器文件名、语言选项。靠正则过滤会一直误伤。
# 原来靠 AI 网关逐条分类（实测准确率 15/16），现在把这个判断交给 DSH：
# 它能同时看到 DOM 文本、所在页面、调用点，比只给一串字符串判得准，
# 判完还能自己写词表、跑复查、提交。
#
# 本脚本只负责「起实例 + 扫 DOM + 去重 + 入队」，写完证据就退出，
# 不写 zh.json、不提交、不推送。
#
# 用法：
#   bash ops/i18n/autofix-dom.sh --dry-run    # 只扫描，不入队
#   bash ops/i18n/autofix-dom.sh              # 扫描 + 入队
set -uo pipefail

REPO=${REPO:-/root/work/bootimus}
PORT=${PORT:-18081}
HTTP_PORT=${HTTP_PORT:-18080}
TFTP_PORT=${TFTP_PORT:-1069}
DRY_RUN=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --no-push) : ;;   # 兼容旧参数：现在不写仓库，参数无意义
        *) echo "未知参数: $a"; exit 2 ;;
    esac
done

cd "$REPO" || { echo "进不去 $REPO" >&2; exit 1; }
. "$(dirname "$0")/_common.sh"

export TZ=${TZ:-Asia/Shanghai}
command -v jq >/dev/null || { log "ERROR: 缺 jq"; exit 1; }

# ---------- 1. 起临时实例 ----------
log "=== 起临时实例 ==="
# 清掉所有 bm- 开头的实例（前几轮的测试进程会占着 TFTP 端口）
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
    echo "$pids" | xargs -r kill 2>/dev/null; exit 1
fi
TOTAL=$(jq '.missing | length' /tmp/dom-out.json)
log "  扫到英文 $TOTAL 条"

# 过滤掉翻译表已有的；实例用完就关，别占着端口
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

pids=$(ps -eo pid=,comm= | awk '$2 ~ /^bm-/ {print $1}'); [ -n "$pids" ] && echo "$pids" | xargs -r kill 2>/dev/null

if [ "$NEW" -eq 0 ]; then log "没有新增，退出"; exit 0; fi

# ---------- 3. 交给 DSH ----------
if [ "$DRY_RUN" -eq 1 ]; then
    log "dry-run：只扫描，不入队（清单在 /tmp/dom-new.json）"
    exit 0
fi

EVID="${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}/evidence"
mkdir -p "$EVID" 2>/dev/null || true
DOM_EVID="$EVID/dom-new-latest.json"
cp -f /tmp/dom-new.json "$DOM_EVID" 2>/dev/null || DOM_EVID=""
[ -n "$DOM_EVID" ] && log "  清单已存：$DOM_EVID"

FP="i18n-gap/dom/$(md5sum /tmp/dom-new.json | cut -c1-12)"
TASK=$(jq -n --arg repo "$REPO" --argjson n "$NEW" --arg evid "$DOM_EVID" \
       --slurpfile items /tmp/dom-new.json '
  {kind:"i18n-gap", priority:4,
   source:{script:"ops/i18n/autofix-dom.sh", scope:"dom"},
   signal:{missing:$n, scope:"rendered-dom"},
   repo:{workdir:$repo, branch:"main"},
   evidence:{dom_list_file:$evid, dom_list:($items[0] | .[0:300])},
   playbook:"ops/dsh/HANDOFF-PROTOCOL.md#i18n-gap"}') \
  || { log "ERROR: 构造任务失败"; exit 1; }

log "=== 交给 DSH ==="
if ! dsh_enqueue i18n-gap "$FP" "$TASK"; then
    log "ERROR: 入队失败，本次清单没有交代出去"
    exit 1
fi
log "=== 完成（判断 + 翻译由 DSH 接手）==="
