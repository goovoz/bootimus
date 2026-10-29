#!/usr/bin/env bash
# 汉化自动修复（DOM 版）：起实例 → 扫真实 DOM → AI 判断该不该翻 → 翻译 → 写入 → 复查 → 提交
#
# 与 autofix.sh 的分工：
#   autofix.sh     只看源码字面量，轻、快、进 CI
#   autofix-dom.sh 看真实渲染结果，能抓到服务端返回的句子和运行时拼出来的文本
#                  （API 参考页的参数说明就是这么露出来的），重，每天跑一次
#
# 为什么必须让 AI 判断：DOM 里 200 多条「英文」绝大多数不该翻 —— 发行版名、内核参数、
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

cd "$REPO" || { echo "进不去 $REPO" >&2; exit 1; }
. "$(dirname "$0")/_common.sh"

export TZ=${TZ:-Asia/Shanghai}
load_ai_env || exit 0

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

# ---------- 3. AI 判断 + 翻译（分批）----------
BATCHES=$(( (NEW + BATCH - 1) / BATCH ))
log "=== AI 判断（分 $BATCHES 批，每批 $BATCH 条）==="
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

第三种判定 variable：句子里的数字、MAC、文件名、主机名等是**运行时才填进去的**，
不能当固定文案翻（写进词表也永远匹配不到）。例如：
- Showing 4 of 4              → variable
- 3 removed                    → variable
- Enabled (0 share(s) active, port 445) → variable
- Ignored: vendor class "HUAWEI:android:NCO" ... → variable
但下面这种数字是**固定默认值**，照常 translate：
- (Maximum entries, default 100).  → translate

严格输出 JSON 数组，每项 {"text":"原文","action":"translate"|"skip"|"variable","zh":"译文（非 translate 时为空字符串）"}，不要解释、不要代码块。'

rm -f /tmp/dom-batch-*.json
for b in $(seq 0 $((BATCHES - 1))); do
    CHUNK=$(jq -c ".[$((b*BATCH)):$(( (b+1)*BATCH ))]" /tmp/dom-new.json)
    RAW=$(llm_call "$SYS" "$CHUNK" 8000)
    if [ -z "$RAW" ]; then
        log "  第 $((b+1)) 批无输出，跳过"
        continue
    fi
    json_extract "$RAW" > "/tmp/dom-batch-$b.json"
    echo "  第 $((b+1)) 批解析完成" >> "$LOG"
done

# 合并各批 + 统计
python3 - <<'PY'
import glob, json
merged = []
for f in sorted(glob.glob('/tmp/dom-batch-*.json')):
    try:
        arr = json.load(open(f, encoding='utf-8'))
        merged.extend([x for x in arr if isinstance(x, dict)])
    except Exception:
        pass
open('/tmp/dom-translated.json', 'w', encoding='utf-8').write(json.dumps(merged, ensure_ascii=False))

tr = [x for x in merged if x.get('action') == 'translate' and x.get('zh')]
var = [x for x in merged if x.get('action') == 'variable']
skip = len(merged) - len(tr) - len(var)
print('  AI 判断：共 %d 条 → 该翻 %d / 变量句 %d / 跳过 %d' % (len(merged), len(tr), len(var), skip))
if var:
    print('  变量句（需要人工写正则，不会自动写词条）：')
    for x in var[:20]:
        print('    ! %s' % (x.get('text') or '')[:100])

# 只留 translate 的，转成 {原文: 译文}
pairs = {(x.get('text') or '').strip(): (x.get('zh') or '').strip()
         for x in tr if (x.get('text') or '').strip()}
json.dump(pairs, open('/tmp/dom-translations.json', 'w', encoding='utf-8'), ensure_ascii=False)
PY

# ---------- 4. 写入（只新增）----------
apply_translations /tmp/dom-translations.json "$DRY_RUN"
case $? in
    0) ;;
    2) log "无可新增，退出"; exit 0 ;;
    *) log "ERROR: 写入失败，不推送"; exit 1 ;;
esac
[ "$DRY_RUN" -eq 1 ] && { log "dry-run 结束"; exit 0; }

# ---------- 5. 复查 + 提交 ----------
verify_all || exit 1
if [ "$NO_PUSH" -eq 1 ]; then
    commit_and_build "i18n: 按 DOM 扫描结果自动补一批（AI 判断 + 翻译）" --no-push || exit 1
else
    commit_and_build "i18n: 按 DOM 扫描结果自动补一批（AI 判断 + 翻译）" || exit 1
fi
log "=== 完成 ==="
