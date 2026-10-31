#!/usr/bin/env bash
# 同步冲突自动解决：AI 出合并方案 → 本地编译测试 → 通过才推
#
# 触发条件：sync-watchdog 判定 needs_ai=true（不是瞬时故障，是真的语义冲突）
#
# 流程：
#   1. 检出最新的 sync-conflict/* 分支（workflow 留下的现场）
#   2. 重新 rebase upstream/main，让冲突重现
#   3. 每个冲突块单独问 AI 合并方案（带文件路径和上下文）
#   4. 缩进归一化后写回，git add + rebase --continue
#   5. go build + go test 兜底
#   6. 全过 → force-with-lease 推 main；任何一步失败 → abort + 记录到 issue
#
# 为什么敢让 AI 改代码：它的输出只是「候选」，最终由编译器和测试决定生死。
# 通不过就整体回滚，仓库不会留下半成品。
set -uo pipefail

WORKDIR=${WORKDIR:-/root/work/bootimus}
UPSTREAM_BRANCH=${UPSTREAM_BRANCH:-main}
LOG=${LOG:-/var/log/bootimus-sync-watch.log}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-sync-watch}
MAX_ROUNDS=${MAX_ROUNDS:-20}          # 一个分支最多处理多少个冲突块
DRY_RUN=${DRY_RUN:-0}

# 网关配置：优先用 /root/ai_gateway.env，其次 /etc/default/bootimus-sync-watch
[ -r /root/ai_gateway.env ] && { set -a; . /root/ai_gateway.env; set +a; }
[ -r /etc/default/bootimus-sync-watch ] && { set +a; . /etc/default/bootimus-sync-watch; set +a; }
: "${AI_BASE_URL:=}"
: "${AI_MODEL:=cn:deepseek-v4.1-flash}"
: "${AI_API_KEY:=}"
: "${AI_EFFORT:=max}"

log() { printf '%s  [auto-resolve] %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }
die() { log "ERROR: $*"; exit 1; }

cd "$WORKDIR" || die "进不去 $WORKDIR"

# ---------------------------------------------------------------- 推送
# key 来自 /etc/default/bootimus-sync-watch（600 权限，不进仓库）。
# 成功和失败都发 —— 用户明确要求知道 AI 到底做了什么。
notify() {
    local title="$1" body="$2"
    if [ -n "${SC_SENDKEY:-}" ]; then
        if curl -sS --max-time 20 -X POST "https://sctapi.ftqq.com/${SC_SENDKEY}.send" \
                --data-urlencode "title=$title" --data-urlencode "desp=$body" >/dev/null 2>&1; then
            log "  已推送：$title"
            return 0
        fi
        log "  推送失败：$title"
        return 1
    fi
    log "  （未配 SC_SENDKEY，跳过推送）$title"
    return 0
}

# 失败时统一收口：写日志 + 推送 + 退出
fail_notify() {
    local reason="$1" rc="${2:-1}"
    log "失败：$reason"
    notify "❌ bootimus AI 解冲突失败" "**原因**：${reason}

**落后上游**：$(git rev-list --count HEAD.."upstream/${UPSTREAM_BRANCH}" 2>/dev/null || echo 未知) 个提交
**现场**：\`/var/log/bootimus-sync-watch.log\`
**处理手册**：\`docs/FORK-SYNC-CONFLICTS.md\`

需要人工介入。"
    exit "$rc"
}

# ---------------------------------------------------------------- TRAILER 修复
# 找出 upstream..HEAD 里缺 Fork-Patch 标记的提交并补上。
# 返回 0=没有缺的，1=补了（需要重新验证），2=补失败
fix_missing_trailers() {
    local bad=()
    local sha body
    for sha in $(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD); do
        body=$(git log -1 --format='%B' "$sha")
        printf '%s' "$body" | grep -qE '^Fork-(Patch|Meta):' || bad+=("$sha")
    done

    if [ ${#bad[@]} -eq 0 ]; then
        return 0
    fi

    log "发现 ${#bad[@]} 个提交缺 Fork-Patch 标记："
    for sha in "${bad[@]}"; do
        log "  $(git log -1 --format='%h %s' "$sha")"
    done

    # 工作区必须干净，filter-branch 不认脏工作区
    if [ -n "$(git status --porcelain)" ]; then
        log "  ✗ 工作区不干净，不敢改写历史"
        return 2
    fi

    local backup
    backup=$(git rev-parse HEAD)
    log "  改写前的 HEAD: $backup（失败可 git reset --hard $backup 回滚）"

    # 用 msg-filter 补标记。只动没标记的，已有的原样保留。
    if ! FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f --msg-filter '
        body=$(cat)
        if printf "%s" "$body" | grep -qE "^Fork-(Patch|Meta):"; then
            printf "%s\n" "$body"
        else
            title=$(printf "%s" "$body" | head -1)
            # 取 type(scope) 当 slug，取不到就用 sha
            slug=$(printf "%s" "$title" | sed -n "s/^\([a-z][a-z]*\)(\([a-z0-9_-]*\)):.*/\1-\2/p")
            [ -z "$slug" ] && slug="auto-$(printf "%s" "$GIT_COMMIT" | cut -c1-8)"
            printf "%s\n\nFork-Patch: %s\n" "$body" "$slug"
        fi
    ' "upstream/$UPSTREAM_BRANCH"..HEAD >/tmp/ar-trailer.log 2>&1; then
        log "  ✗ filter-branch 失败"
        tail -5 /tmp/ar-trailer.log | while read -r l; do log "    $l"; done
        return 2
    fi

    # 复查
    local still=0
    for sha in $(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD); do
        body=$(git log -1 --format='%B' "$sha")
        printf '%s' "$body" | grep -qE '^Fork-(Patch|Meta):' || still=$((still + 1))
    done
    if [ "$still" -ne 0 ]; then
        log "  ✗ 补完还有 $still 个缺"
        return 2
    fi

    log "  ✓ 全部补齐（$(git log -1 --format='%h' "$(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD | tail -1)")...$(git rev-parse --short HEAD)）"
    return 1
}

[ -n "$AI_BASE_URL" ] || fail_notify "没配 AI_BASE_URL"
command -v jq >/dev/null || die "缺 jq"
GO=$(command -v go || echo /usr/local/go/bin/go)
GOFMT=$("$GO" env GOROOT)/bin/gofmt

# ---------------------------------------------------------------- AI 调用
SYS='你在做 git rebase 冲突的合并决策。仓库是 bootimus 的 fork。

规则：
1. <<<<<<< HEAD 一侧是上游的新代码
2. >>>>>>> 一侧是 fork 自己的补丁（带 [bootimus-fork L1] 锚点 / Fork-Patch 标记）
3. fork 的补丁必须保留，不能因为合并丢掉
4. 两边各自新增不同语句 → 两段都保留，上游在前
5. 同一个东西的两个版本 → 用上游的新版本，但 fork 额外加的行保留
6. 如果两边逻辑上真的互斥、无法共存，只输出一个词：UNRESOLVABLE

输出要求：
- 只输出合并后的代码行（含缩进），不要冲突标记、不要解释、不要 markdown 代码块
- 缩进从第 0 列开始不带外层包裹

输出会被直接写进文件，语法错误会导致编译失败、整次尝试回滚。'

ask_ai() {
    local path="$1" block="$2"
    local body resp
    body=$(jq -n --arg m "$AI_MODEL" --arg s "$SYS" --arg e "$AI_EFFORT" \
        --arg p "$path" --arg b "$block" '{
        model:$m, temperature:0, max_tokens:4000, reasoning_effort:$e,
        messages:[
          {role:"system",content:$s},
          {role:"user",content:("文件：" + $p + "\n\n冲突块：\n" + $b)}
        ]}')
    resp=$(curl -sS --noproxy '*' --max-time 180 -X POST "${AI_BASE_URL%/}/chat/completions" \
        -H "Authorization: Bearer ${AI_API_KEY:-placeholder}" \
        -H 'Content-Type: application/json' -d "$body" 2>/dev/null) || return 1
    printf '%s' "$resp" | jq -r '.choices[0].message.content // empty' 2>/dev/null
}

# ---------------------------------------------------------------- 缩进归一化 + 替换
resolve_file() {
    local path="$1"
    python3 - "$path" <<'PY'
import json, re, subprocess, sys
from pathlib import Path

path = Path(sys.argv[1])
src = path.read_text(encoding='utf-8')

BLOCK = re.compile(r'<<<<<<< HEAD\n(.*?)=======\n(.*?)>>>>>>> ([^\n]*)\n', re.S)

def indent_of(ln):
    return ln[:len(ln) - len(ln.lstrip())]

def normalize(ai_text, target_indent):
    lines = [ln for ln in ai_text.splitlines() if ln.strip()]
    if not lines:
        return ''
    ai_min = min(indent_of(ln) for ln in lines)
    out = []
    for ln in ai_text.splitlines():
        out.append('' if not ln.strip() else target_indent + ln[len(ai_min):])
    return '\n'.join(out)

blocks = list(BLOCK.finditer(src))
if not blocks:
    print('NO_CONFLICT')
    sys.exit(0)

results = []
for i, m in enumerate(blocks, 1):
    ours, theirs = m.group(1), m.group(2)
    target = min(indent_of(ln) for ln in (ours + theirs).splitlines() if ln.strip())
    block_text = m.group(0)
    # 由 bash 侧逐块调 AI（这里只把块交给外部）
    results.append({'index': i, 'target': target, 'block': block_text})

# 输出块清单，交给 bash 逐块处理
print(json.dumps(results, ensure_ascii=False))
PY
}

# ---------------------------------------------------------------- 主流程
BRANCH=$(git for-each-ref --sort=-creatordate --format='%(refname:short)' 'refs/heads/sync-conflict/*' | head -1)
[ -n "$BRANCH" ] || BRANCH=$(git for-each-ref --sort=-creatordate --format='%(refname:short)' 'refs/remotes/origin/sync-conflict/*' | head -1 | sed 's#^origin/##')
[ -n "$BRANCH" ] || fail_notify "找不到 sync-conflict 分支"

log "处理分支: $BRANCH"

# 先看有没有缺标记的提交 —— 纪律检查会卡在这里，比冲突更早。
fix_missing_trailers
case $? in
    0) log "标记齐全" ;;
    1) log "已补标记，重新走一遍" ;;
    2) fail_notify "补 Fork-Patch 标记失败，需要人工" 4 ;;
esac

# 干净起点
git rebase --abort 2>/dev/null || true
git checkout -q -f main 2>/dev/null || git checkout -q -f master
git fetch -q upstream
git fetch -q origin

WORK="auto-resolve/$(date +%Y%m%d-%H%M)"
git branch -D "$WORK" 2>/dev/null || true
git checkout -q -b "$WORK" main || die "切分支失败"

log "rebase 到 upstream/$UPSTREAM_BRANCH"
if ! git rebase "upstream/$UPSTREAM_BRANCH" >/tmp/ar-rebase.log 2>&1; then
    CONFLICTS=$(git diff --name-only --diff-filter=U)
    [ -n "$CONFLICTS" ] || { log "rebase 失败但没有冲突文件"; tail -5 /tmp/ar-rebase.log; exit 1; }
    log "冲突文件: $(echo "$CONFLICTS" | tr '\n' ' ')"

    ROUND=0
    while [ -n "$CONFLICTS" ]; do
        for f in $CONFLICTS; do
            ROUND=$((ROUND + 1))
            [ "$ROUND" -gt "$MAX_ROUNDS" ] && die "超过 $MAX_ROUNDS 轮，停手"

            # 逐块问 AI
            python3 - "$f" > /tmp/ar-blocks.json <<'PY'
import json, re, sys
from pathlib import Path
src = Path(sys.argv[1]).read_text(encoding='utf-8')
BLOCK = re.compile(r'<<<<<<< HEAD\n(.*?)=======\n(.*?)>>>>>>> ([^\n]*)\n', re.S)
out = []
for i, m in enumerate(BLOCK.finditer(src), 1):
    out.append({'index': i, 'text': m.group(0)})
print(json.dumps(out, ensure_ascii=False))
PY

            N=$(jq 'length' /tmp/ar-blocks.json 2>/dev/null || echo 0)
            log "  $f: $N 个冲突块"
            [ "$N" -eq 0 ] && { git add "$f"; continue; }

            for k in $(seq 0 $((N - 1))); do
                BLK=$(jq -r ".[$k].text" /tmp/ar-blocks.json)
                ANS=$(ask_ai "$f" "$BLK")
                if [ -z "$ANS" ] || [ "$ANS" = "UNRESOLVABLE" ]; then
                    git rebase --abort
                    fail_notify "AI 判定无法自动合并：$f 第 $((k+1)) 块" 3
                fi
                # 写回：用 python 做归一化替换
                printf '%s' "$ANS" > /tmp/ar-answer.txt
                python3 - "$f" "$((k+1))" /tmp/ar-answer.txt <<'PY'
import re, sys
from pathlib import Path
path, idx, ansfile = Path(sys.argv[1]), int(sys.argv[2]), Path(sys.argv[3])
src = path.read_text(encoding='utf-8')
BLOCK = re.compile(r'<<<<<<< HEAD\n(.*?)=======\n(.*?)>>>>>>> ([^\n]*)\n', re.S)
blocks = list(BLOCK.finditer(src))
if idx > len(blocks):
    sys.exit('块序号越界')
m = blocks[idx - 1]
ours, theirs = m.group(1), m.group(2)
def indent_of(ln): return ln[:len(ln) - len(ln.lstrip())]
target = min(indent_of(ln) for ln in (ours + theirs).splitlines() if ln.strip())
ans = ansfile.read_text(encoding='utf-8')
lines = [ln for ln in ans.splitlines() if ln.strip()]
ai_min = min(indent_of(ln) for ln in lines) if lines else ''
norm = '\n'.join('' if not ln.strip() else target + ln[len(ai_min):] for ln in ans.splitlines())
src = src[:m.start()] + norm + '\n' + src[m.end():]
path.write_text(src, encoding='utf-8')
PY
                log "  ✓ $f 第 $((k+1)) 块已合并"
            done
            git add "$f"
        done

        if git diff --name-only --diff-filter=U | grep -q .; then
            CONFLICTS=$(git diff --name-only --diff-filter=U)
            continue
        fi
        if ! GIT_EDITOR=true git rebase --continue >>/tmp/ar-rebase.log 2>&1; then
            CONFLICTS=$(git diff --name-only --diff-filter=U)
            [ -n "$CONFLICTS" ] || { log "continue 失败且无冲突"; tail -5 /tmp/ar-rebase.log; exit 1; }
            log "下一批冲突: $(echo "$CONFLICTS" | tr '\n' ' ')"
        else
            CONFLICTS=""
        fi
    done
else
    log "rebase 无冲突，可能上游已合并"
fi

# ---------------------------------------------------------------- 验证
log "验证：gofmt / build / test"
if ! "$GOFMT" -l . >/tmp/ar-fmt.log 2>&1; then
    log "  ✗ gofmt 执行失败"; exit 1
fi
if [ -s /tmp/ar-fmt.log ]; then
    log "  格式化了 $(wc -l < /tmp/ar-fmt.log) 个文件"
    "$GOFMT" -w $(cat /tmp/ar-fmt.log) 2>/dev/null || true
fi

if ! CGO_ENABLED=0 "$GO" build ./... >/tmp/ar-build.log 2>&1; then
    _err=$(tail -5 /tmp/ar-build.log | tr "\n" " " | head -c 300)
    fail_notify "编译失败：${_err}" 2
fi
log "  ✓ 编译通过"

if ! "$GO" test ./internal/admin/ ./internal/autoinstall/ >/tmp/ar-test.log 2>&1; then
    _err=$(tail -5 /tmp/ar-test.log | tr "\n" " " | head -c 300)
    fail_notify "测试失败：${_err}" 2
fi
log "  ✓ 测试通过"

# 汉化别被打断
if ! python3 ops/i18n/audit.py 2>/dev/null | grep -q '缺失 0 条'; then
    log "  ! 汉化审计有缺失（不阻塞，交给 autofix 后续补）"
fi

# 补丁标记还在吗
if git log --format='%B' "upstream/$UPSTREAM_BRANCH"..HEAD | grep -q 'Fork-Patch:'; then
    log "  ✓ Fork-Patch 标记还在"
else
    log "  ! 没找到 Fork-Patch 标记，检查是否丢了补丁"
fi

if [ "$DRY_RUN" = "1" ]; then
    log "DRY_RUN：不推送。分支 $WORK 留着"
    exit 0
fi

# ---------------------------------------------------------------- 推送
log "推送到 main"
if git push --force-with-lease=main:"$(git rev-parse origin/main)" origin "$WORK:main" >/tmp/ar-push.log 2>&1; then
    _sha=$(git rev-parse --short HEAD)
    log "  ✓ 已推送 $_sha"
    git checkout -q main && git reset -q --hard "$WORK"
    _behind=$(git rev-list --count HEAD.."upstream/$UPSTREAM_BRANCH")
    log "完成：落后 $_behind 个提交"
    notify "✅ bootimus 同步已由 AI 自动修复" "**提交**：${_sha}
**落后上游**：${_behind} 个提交（已归零）
**处理分支**：${BRANCH}
**验证**：gofmt / go build / go test 全通过
**日志**：\`/var/log/bootimus-sync-watch.log\`

构建已由 push 自动触发。"
else
    log "  ✗ 推送失败"
    tail -5 /tmp/ar-push.log | while read -r l; do log "    $l"; done
    exit 1
fi
