#!/usr/bin/env bash
# bootimus 上游同步 watchdog
#
# 背景：sync-upstream 失败时，信号只在 GitHub 页面上，没人看就没人知道。
# 2026-10-27 就吃过一次：8 个提交漏了 Fork-Patch trailer，同步在「校验提交标记」
# 这一步停手，一直卡在「落后上游 18 个提交」，直到人工发现。
#
# 逻辑：
#   1. 拉最近几次 sync-upstream 运行，判定 OK / RUNNING / STALE / FAILED
#   2. FAILED 时按失败步骤归因（trailer / 冲突 / 探测器 / 权限 / 测试 / 瞬时）
#   3. 只有「瞬时」类会自动重新 dispatch，其余只收集证据等 AI/人
#   4. 状态写进 $STATE_DIR/state.json —— AI 巡检直接读这个文件
#
# 为什么自愈这么保守：改提交历史、解冲突、动探测器都要判断，脚本硬干容易把
# 小故障放成大故障。脚本负责「发现 + 归因 + 通知 + 重试瞬时失败」，
# 判断类的事留给 AI/人。
#
# 安全阀：
#   CHECK_ONLY=1           只看不动作（演练用）
#   MAX_RETRY_PER_DAY=2    最多自动重试几次
#   STALE_HOURS=36         多久没成功算「停滞」
#   SC_SENDKEY=...         Server酱 SendKey，设了才推送
#
# 退出码：0 正常/进行中；1 需要 AI 或人工；2 已自动重试；3 自身出错
set -uo pipefail

REPO=${REPO:-goovoz/bootimus}
UPSTREAM=${UPSTREAM:-garybowers/bootimus}
API=${API:-https://api.github.com}
WORKFLOW=${WORKFLOW:-sync-upstream.yml}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-sync-watch}
LOG=${LOG:-/var/log/bootimus-sync-watch.log}
STATE=$STATE_DIR/state.json
STALE_HOURS=${STALE_HOURS:-36}
MAX_RETRY_PER_DAY=${MAX_RETRY_PER_DAY:-2}
CHECK_ONLY=${CHECK_ONLY:-0}
SC_SENDKEY=${SC_SENDKEY:-}
SC_API=${SC_API:-https://sctapi.ftqq.com}
GH_TOKEN=${GH_TOKEN:-}

export TZ=${TZ:-Asia/Shanghai}

# ---- 配置：systemd 走 EnvironmentFile，手动执行时补一次（只补空的）----
if [ -r /etc/default/bootimus-sync-watch ]; then
    while IFS='=' read -r k v; do
        case "$k" in ''|\#*) continue ;; esac
        k=$(printf '%s' "$k" | tr -d ' \t')
        v=$(printf '%s' "$v" | sed 's/^"//; s/"$//; s/[[:space:]]*$//')
        [ -n "$v" ] || continue
        cur=""
        eval "cur=\${$k:-}"
        [ -n "$cur" ] && continue
        case "$k" in
            GH_TOKEN|SC_SENDKEY|REPO|UPSTREAM|STALE_HOURS|MAX_RETRY_PER_DAY|STATE_DIR|LOG|\
            AI_BASE_URL|AI_MODEL|AI_API_KEY|AI_EFFORT|AI_TIMEOUT|AI_MAX_TOKENS|AI_MAX_LOG_BYTES)
                printf -v "$k" '%s' "$v" 2>/dev/null || eval "$k=\$v" ;;
        esac
    done < /etc/default/bootimus-sync-watch
fi
[ -z "$GH_TOKEN" ] && [ -r /root/gh_pat.txt ] && GH_TOKEN=$(tr -d '\n' < /root/gh_pat.txt)

mkdir -p "$STATE_DIR" 2>/dev/null
log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" >>"$LOG" 2>/dev/null; [ "${SYNC_QUIET:-0}" = "1" ] || printf '%s\n' "$*"; }
die() { log "ERROR: $*"; exit 3; }

[ -n "$GH_TOKEN" ] || die "没有 GH_TOKEN（配 /etc/default/bootimus-sync-watch 或放 /root/gh_pat.txt）"
command -v jq >/dev/null   || die "缺少 jq"
command -v curl >/dev/null || die "缺少 curl"

api() { curl -sS --max-time 25 -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/vnd.github+json' "$@"; }

# ---- 可选：用 LLM 做失败归因（OpenAI 兼容网关，不配就走纯规则）----
# 安全边界：LLM 只产出「文案」，不参与任何动作决策。脚本该重跑还是重跑、
# 该交给 AI 还是交，全由上面的规则决定 —— 不把 LLM 输出接给 eval，也不据此改仓库。
#
# 参数怎么定的（踩过弯，记下来省得下次再试）：
#   * 模型 cn:deepseek-v4.1-flash：网关 /v1/models 声明 context_length=1000000、
#     max_output_tokens=128000、only_reasoning=true、reasoning_default_effort=high。
#   * 思考强度用 reasoning_effort，取值 low/high/max（CN 面三档；国际面只有 high，
#     往国际面发 low/max 会被上游判 400）。thinking.budget_tokens 这类字段会被网关
#     静默丢弃，别浪费时间试。实测同一份日志同一提示词：low 思考 278 / high 324 /
#     max 612 tokens，max 的成本只贵 0.02 credit，所以默认拉满。
#   * 上下文 1M 是模型固有属性，不用传参 —— 它的用途是把【完整日志】喂进去而不是截几十行。
#     截片段会漏掉真正的证据：给 3 行片段时模型只能复述我给它的那条，
#     给 468 行全文时它自己找到了具体提交号、还数出了 8 个违规提交。
#   * 提示词强制分步推理（列假设 → 找证据 → 排除 → 收敛），比单纯调档位更能提质量。
AI_BASE_URL=${AI_BASE_URL:-}
AI_MODEL=${AI_MODEL:-cn:deepseek-v4.1-flash}
AI_API_KEY=${AI_API_KEY:-}
AI_EFFORT=${AI_EFFORT:-max}
AI_TIMEOUT=${AI_TIMEOUT:-180}
AI_MAX_TOKENS=${AI_MAX_TOKENS:-16000}
AI_MAX_LOG_BYTES=${AI_MAX_LOG_BYTES:-400000}   # 约 100k tokens，1M 窗口留足余量

LLM_USAGE=""
llm_advise() {
    local diary="$1"
    [ -n "$AI_BASE_URL" ] || return 0
    [ -n "$diary" ] || return 0
    # 日志过大时保留头尾（关键信息通常在前面的 setup 和最后的报错里）
    if [ "$(printf '%s' "$diary" | wc -c)" -gt "$AI_MAX_LOG_BYTES" ]; then
        diary=$(printf '%s\n' "$diary" | { head -n 4000; echo "……（中间省略）……"; tail -n 4000; })
    fi
    local sys payload resp content
    sys='你在诊断一个 CI 失败。严格按下面步骤推理，不要跳过：
1. 先列出所有可能导致失败的原因，至少 8 个，包括看起来不太可能的；
2. 对每一个原因，回到日志里找支持它的证据、以及反对它的证据，引用日志原文；
3. 明确排除证据不成立的，说明为什么排除；
4. 只保留证据最强的一条作为根因；
5. 最后才给结论。
输出固定三行，每行不超过 120 字：
CAUSE: <根因，必须引用日志里的原文片段作为依据>
FIX: <一句话可执行的修复动作，带上具体文件/命令/提交号>
HUMAN: <yes 或 no，是否需要人工确认>'
    payload=$(jq -n --arg m "$AI_MODEL" --arg s "$sys" --arg d "$diary" \
        --arg e "$AI_EFFORT" --argjson mt "$AI_MAX_TOKENS" '{
        model: $m, temperature: 0, max_tokens: $mt, reasoning_effort: $e,
        messages: [
          {role: "system", content: $s},
          {role: "user", content: ("以下是任务的完整日志：\n" + $d)}
        ]}') || return 0
    resp=$(curl -sS --max-time "$AI_TIMEOUT" -X POST "${AI_BASE_URL%/}/chat/completions" \
        -H "Authorization: Bearer $AI_API_KEY" -H 'Content-Type: application/json' \
        -d "$payload" 2>/dev/null) || return 0
    content=$(printf '%s' "$resp" | jq -r '.choices[0].message.content // empty' 2>/dev/null)
    LLM_USAGE=$(printf '%s' "$resp" | jq -c '.usage // {}' 2>/dev/null)
    [ -n "$LLM_USAGE" ] && log "LLM 用量：$(printf '%s' "$LLM_USAGE" | jq -r '"输入=\(.prompt_tokens) 思考=\(.completion_thinking_tokens // 0) 输出=\(.completion_tokens) credit=\(.credit // 0)"' 2>/dev/null)"
    printf '%s' "$content"
}

notify() {
    local title="$1" body="$2"
    if [ -n "$SC_SENDKEY" ]; then
        if curl -sS --max-time 15 -X POST "$SC_API/$SC_SENDKEY.send" \
                --data-urlencode "title=$title" --data-urlencode "desp=$body" >/dev/null; then
            log "已推送：$title"
        else
            log "推送失败：$title"
        fi
    else
        # 没配 Server 酱 key 时走 issue —— 否则告警只进日志，等于没告警。
        log "（未配 SC_SENDKEY，改走 GitHub Issue）$title"
        issue_notify "$title" "$body"
    fi
}

# 开或更新一条 sync-failure issue。已有未关闭的就追加评论，不重复开单。
issue_notify() {
    local title="$1" body="$2"
    [ -n "${GH_TOKEN:-}" ] || { log "无 GH_TOKEN，跳过 issue 告警"; return 1; }

    local num
    num=$(api "$API/repos/$REPO/issues?labels=sync-failure&state=open&per_page=1" 2>/dev/null \
        | jq -r '.[0].number // empty' 2>/dev/null || true)

    if [ -n "$num" ]; then
        if api -X POST "$API/repos/$REPO/issues/$num/comments" \
                -H 'Content-Type: application/json' \
                -d "$(jq -n --arg b "$body" '{body:$b}')" >/dev/null; then
            log "  已追加到 issue #$num"
        else
            log "  追加 issue 评论失败"
        fi
    else
        num=$(api -X POST "$API/repos/$REPO/issues" \
                -H 'Content-Type: application/json' \
                -d "$(jq -n --arg t "【自动告警】$title" --arg b "$body" \
                    '{title:$t, body:$b, labels:["sync-failure"]}')" 2>/dev/null \
            | jq -r '.number // empty' 2>/dev/null || true)
        if [ -n "$num" ]; then
            log "  已开 issue #$num"
        else
            log "  开 issue 失败（检查 GH_TOKEN 有没有 issues:write 权限）"
        fi
    fi
}

# 同步恢复正常时关掉遗留的告警单，别让它们堆着。
issue_close_if_any() {
    [ -n "${GH_TOKEN:-}" ] || return 0
    local nums
    nums=$(api "$API/repos/$REPO/issues?labels=sync-failure&state=open&per_page=10" 2>/dev/null \
        | jq -r '.[].number' 2>/dev/null || true)
    [ -n "$nums" ] || return 0
    for n in $nums; do
        if api -X PATCH "$API/repos/$REPO/issues/$n" \
                -H 'Content-Type: application/json' -d '{"state":"closed"}' >/dev/null; then
            log "  已关闭 issue #$n（同步已恢复）"
        fi
    done
}

# ---------------------------------------------------------------- 1. 拉运行记录
RUNS=$(api "$API/repos/$REPO/actions/workflows/$WORKFLOW/runs?per_page=10") || die "拉取运行记录失败"
echo "$RUNS" | jq -e '.workflow_runs' >/dev/null 2>&1 \
    || { log "响应异常：$(printf '%s' "$RUNS" | head -c 200)"; die "运行记录格式不对"; }

RUN_ID=$(echo "$RUNS" | jq -r '.workflow_runs[0].id // empty')
if [ -z "$RUN_ID" ]; then log "还没有任何 sync-upstream 运行记录"; exit 0; fi

# 演练用：拿历史某次 run 走一遍归因逻辑（只看不动作）
if [ -n "${RUN_ID_OVERRIDE:-}" ]; then
    OV=$(api "$API/repos/$REPO/actions/runs/$RUN_ID_OVERRIDE")
    if echo "$OV" | jq -e '.id' >/dev/null 2>&1; then
        RUN_ID=$RUN_ID_OVERRIDE
        RUNS=$(jq -c --argjson ov "$OV" '.workflow_runs = [$ov]' <<<"$RUNS")
        log "演练模式：改用 run $RUN_ID_OVERRIDE"
    else
        log "RUN_ID_OVERRIDE=$RUN_ID_OVERRIDE 取不到，忽略"
    fi
fi

CONCL=$(echo "$RUNS"   | jq -r '.workflow_runs[0].conclusion // .workflow_runs[0].status')
CREATED=$(echo "$RUNS" | jq -r '.workflow_runs[0].created_at')
HEAD_SHA=$(echo "$RUNS" | jq -r '.workflow_runs[0].head_sha' | cut -c1-7)
RUN_URL=$(echo "$RUNS" | jq -r '.workflow_runs[0].html_url')
LAST_OK_AT=$(echo "$RUNS" | jq -r '[.workflow_runs[] | select(.conclusion=="success")][0].updated_at // empty')

# ---------------------------------------------------------------- 2. 健康度
now=$(date +%s); stale=0
if [ -n "$LAST_OK_AT" ]; then
    ok_ts=$(date -d "$LAST_OK_AT" +%s 2>/dev/null || echo 0)
    [ "$ok_ts" -gt 0 ] && [ $(( (now - ok_ts) / 3600 )) -ge "$STALE_HOURS" ] && stale=1
else
    stale=1
fi

case "$CONCL" in
    success) if [ "$stale" = 1 ]; then HEALTH=STALE; else HEALTH=OK; fi ;;
    failure) HEALTH=FAILED ;;
    *)       if [ "$stale" = 1 ]; then HEALTH=STALE; else HEALTH=RUNNING; fi ;;
esac

# ---------------------------------------------------------------- 3. 落后多少
# 用「上游最新 SHA 作 base、我们 main 作 head」来比：
#   behind_by = base(上游) 独有 = 我们落后的提交数  ← 要的就是这个
#   ahead_by  = head(我们) 独有 = 我们的补丁数
# 别用 compare/main...garybowers:main 这种跨 fork 的 owner:branch 写法 —— 实测会
# 返回 404（fork 网络重建后解析不到），而用上游 SHA 是稳的。
BEHIND=""
UPSHA=$(api "$API/repos/$UPSTREAM/commits/main" 2>/dev/null | jq -r '.sha // empty' 2>/dev/null)
if [ -n "$UPSHA" ]; then
    CMP=$(api "$API/repos/$REPO/compare/$UPSHA...main" 2>/dev/null)
    echo "$CMP" | jq -e '.behind_by' >/dev/null 2>&1 && BEHIND=$(echo "$CMP" | jq -r '.behind_by')
fi

# ---------------------------------------------------------------- 4. 失败归因
KIND=""; STEP=""; DETAIL=""; TRAILER_BAD=""
if [ "$HEALTH" = "FAILED" ]; then
    JOBS=$(api "$API/repos/$REPO/actions/runs/$RUN_ID/jobs")
    JOB_ID=$(echo "$JOBS" | jq -r '.jobs[0].id // empty')
    STEP=$(echo "$JOBS" | jq -r '[.jobs[].steps[] | select(.conclusion=="failure")][0].name // empty')

    LOG_TAIL=""; ERR_SNIPPET=""; FULL_LOG=""
    if [ -n "$JOB_ID" ]; then
        # 顺手去掉时间戳前缀和 ANSI 转义，喂给 LLM 的是干净文本
        FULL_LOG=$(api -L "$API/repos/$REPO/actions/jobs/$JOB_ID/logs" 2>/dev/null \
                   | sed 's/^[0-9T:.Z-]* //' | sed 's/\x1b\[[0-9;]*m//g')
        LOG_TAIL=$(printf '%s\n' "$FULL_LOG" | tail -60)
        # 归因要的是「失败那一段」，不是日志末尾 —— 末尾往往是 cleanup 这类成功步骤。
        # 早先直接 tail -40 喂给 LLM，它看到全是正常输出，只能编。
        eline=$(printf '%s\n' "$FULL_LOG" | grep -n '::error::' | head -1 | cut -d: -f1)
        if [ -n "${eline:-}" ]; then
            ERR_SNIPPET=$(printf '%s\n' "$FULL_LOG" | sed -n "$(( eline > 25 ? eline - 25 : 1 )),$(( eline + 5 ))p")
        else
            ERR_SNIPPET=$(printf '%s\n' "$FULL_LOG" | tail -40)
        fi
    fi
    ERRLINE=$(printf '%s\n' "$LOG_TAIL" | grep -o '::error::.*' | tail -2 | tr '\n' ' ')

    case "$STEP" in
        校验提交标记*)
            KIND=TRAILER
            TRAILER_BAD=$(printf '%s\n' "$LOG_TAIL" | grep -o '::error::提交 [0-9a-f]* [^￼]*' | head -10 | sed 's/::error:://' | tr '\n' ';')
            DETAIL="有提交没带 Fork-Patch / Fork-Meta 标记，同步在校验阶段停手"
            ;;
        上游已实现*|上游未实现*|重放*)  KIND=CONFLICT;       DETAIL="补丁重放冲突，需人工解决" ;;
        判定失败*)                      KIND=DETECT_UNKNOWN; DETAIL="探测器给不出判定，可能上游结构大改" ;;
        推送*)                          KIND=PERMISSION;     DETAIL="推送被拒，通常是 SYNC_TOKEN 缺失或权限不足" ;;
        编译与测试)                     KIND=TEST_FAIL;      DETAIL="重放后编译或测试不通过" ;;
        "")                             KIND=TRANSIENT;      DETAIL="看不出失败步骤，按瞬时故障处理" ;;
        *)                              KIND=TRANSIENT;      DETAIL="失败步骤：$STEP" ;;
    esac
    [ -n "$ERRLINE" ] && DETAIL="$DETAIL ｜ $ERRLINE"
fi

# 可选：让 LLM 补一句归因（只用于展示，不参与动作决策）
AI_ADVICE=""
if [ "$HEALTH" = "FAILED" ] && [ -n "$AI_BASE_URL" ]; then
    AI_ADVICE=$(llm_advise "$FULL_LOG")
    if [ -n "$AI_ADVICE" ]; then
        log "LLM 归因：$(printf '%s' "$AI_ADVICE" | tr '\n' ' ')"
    else
        log "LLM 归因：无输出（网关不可达或模型名不对，不影响规则归因）"
    fi
fi

# ---------------------------------------------------------------- 5. 保守自愈
ACTION=none; RETRY_COUNT=0; TODAY=$(date +%F)
PREV='{}'; [ -s "$STATE" ] && PREV=$(cat "$STATE" 2>/dev/null) 
echo "$PREV" | jq -e . >/dev/null 2>&1 || PREV='{}'
if [ "$(echo "$PREV" | jq -r '.action.retry_date // ""')" = "$TODAY" ]; then
    RETRY_COUNT=$(echo "$PREV" | jq -r '.action.retry_count // 0')
fi

if [ "$HEALTH" = "FAILED" ]; then
    if [ "$CHECK_ONLY" = "1" ]; then
        [ "$KIND" = "TRANSIENT" ] && ACTION=would_retry || ACTION=needs_ai
    elif [ "$KIND" = "TRANSIENT" ] && [ "$RETRY_COUNT" -lt "$MAX_RETRY_PER_DAY" ]; then
        code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 20 -X POST \
            -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/vnd.github+json' \
            -d '{"ref":"main"}' "$API/repos/$REPO/actions/workflows/$WORKFLOW/dispatches")
        if [ "$code" = "204" ]; then
            ACTION=redispatched; RETRY_COUNT=$((RETRY_COUNT + 1))
            log "瞬时失败，已重新触发（今日第 $RETRY_COUNT 次）"
        else
            ACTION=redispatch_failed
            log "重新触发失败，HTTP $code"
        fi
    else
        ACTION=needs_ai
    fi
fi

# ---------------------------------------------------------------- 6. 落盘
NEEDS_AI=false
case "$HEALTH/$KIND" in
    FAILED/TRANSIENT) ;;
    FAILED/*) NEEDS_AI=true ;;
esac
[ "$HEALTH" = "STALE" ] && NEEDS_AI=true
case "$ACTION" in needs_ai|redispatch_failed) NEEDS_AI=true ;; esac

jq -n \
  --arg checked_at "$(date '+%F %T')" --arg health "$HEALTH" \
  --arg kind "$KIND" --arg step "$STEP" --arg detail "$DETAIL" \
  --arg run_id "$RUN_ID" --arg concl "$CONCL" --arg created "$CREATED" \
  --arg sha "$HEAD_SHA" --arg url "$RUN_URL" --arg last_ok "$LAST_OK_AT" \
  --arg behind "$BEHIND" --arg action "$ACTION" \
  --argjson retry "$RETRY_COUNT" --arg retry_date "$TODAY" \
  --argjson needs_ai "$NEEDS_AI" --arg offenders "$TRAILER_BAD" --arg advice "$AI_ADVICE" \
  --arg usage "${LLM_USAGE:-{\}}" --arg model "$AI_MODEL" \
  '{checked_at:$checked_at, health:$health,
    run:{id:$run_id, conclusion:$concl, created_at:$created, sha:$sha, url:$url},
    last_success_at:$last_ok, behind_upstream:($behind|tonumber? // null),
    failure:{kind:$kind, step:$step, detail:$detail, trailer_offenders:$offenders,
             llm_advice:$advice, llm_model:$model, llm_usage:($usage|fromjson?)},
    action:{taken:$action, retry_count:$retry, retry_date:$retry_date},
    needs_ai:$needs_ai,
    last_notified:($prev|try .last_notified catch null)}' \
  --argjson prev "$PREV" > "$STATE.tmp" && mv "$STATE.tmp" "$STATE"

# ---------------------------------------------------------------- 6b. 需要 AI 时先让它自己试
# 判定 NEEDS_AI 说明不是瞬时故障（重试没用），是真正的语义冲突。
# 交给 auto-resolve-sync：AI 出合并方案 → 本地编译测试 → 通过才推。
# 它失败就退回原来的「转人工」路径，不会让事情更糟。
if [ "$NEEDS_AI" = "true" ] && [ -x /usr/local/sbin/bootimus-auto-resolve ]; then
    _today=$(date '+%Y-%m-%d')
    _tries=$(printf '%s' "$PREV" | jq -r --arg d "$_today" 'if .auto_resolve_date == $d then (.auto_resolve_count // 0) else 0 end' 2>/dev/null || echo 0)
    if [ "${_tries:-0}" -lt "${AUTO_RESOLVE_PER_DAY:-2}" ]; then
        log "需要 AI：交给 auto-resolve 尝试（今日第 $((_tries + 1)) 次）"
        if /usr/local/sbin/bootimus-auto-resolve >> "$LOG" 2>&1; then
            log "  auto-resolve 成功，重新检查"
            ACTION="auto_resolved"
            NEEDS_AI=false
            exec "$0"   # 重新跑一遍，让状态变绿
        else
            log "  auto-resolve 失败（rc=$?），转人工"
            ACTION="needs_human"
        fi
        jq --arg d "$_today" --argjson n "$((_tries + 1))" \
            '.auto_resolve_date=$d | .auto_resolve_count=$n' "$STATE" > "$STATE.tmp" 2>/dev/null \
            && mv "$STATE.tmp" "$STATE" || true
    else
        log "需要 AI：今日已试 ${_tries} 次，不再重试，等人工"
        ACTION="needs_human"
    fi
fi

log "健康=$HEALTH 类型=${KIND:-无} 落后=${BEHIND:-?} 动作=$ACTION 需AI=$NEEDS_AI"

# ---------------------------------------------------------------- 7. 通知（同一故障只发一次）
FP="$HEALTH/$KIND/$RUN_ID"
PREV_FP=$(echo "$PREV" | jq -r '.last_notified // ""')
if [ "$HEALTH" != "OK" ] && [ "$HEALTH" != "RUNNING" ] && [ "$FP" != "$PREV_FP" ]; then
    BODY="**状态**：$HEALTH${KIND:+（$KIND）}
**失败步骤**：${STEP:-无}
**说明**：${DETAIL:-无}
**落后上游**：${BEHIND:-未知} 个提交
**运行**：$RUN_URL
**自动动作**：$ACTION（今日已重试 $RETRY_COUNT 次）"
    if [ -n "$AI_ADVICE" ]; then
        BODY="$BODY

**AI 归因**：
$(printf '%s\n' "$AI_ADVICE" | sed 's/^/  /')"
    fi
    if [ -n "$TRAILER_BAD" ]; then
        BODY="$BODY

**缺标记的提交**：
\`\`\`
$(printf '%s' "$TRAILER_BAD" | tr ';' '\n')
\`\`\`
修法：补 \`Fork-Patch:\` / \`Fork-Meta:\` trailer 后 \`push --force-with-lease\`"
    fi
    notify "bootimus 同步${HEALTH}" "$BODY"
    jq --arg fp "$FP" '.last_notified=$fp' "$STATE" > "$STATE.tmp" && mv "$STATE.tmp" "$STATE"
fi

# 恢复了就把告警单关掉，别留一堆僵尸 issue
[ "$HEALTH" = "OK" ] && issue_close_if_any

case "$HEALTH" in OK|RUNNING) exit 0 ;; esac
[ "$ACTION" = "redispatched" ] && exit 2
exit 1
