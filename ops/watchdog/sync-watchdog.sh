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
#   3. 只有「瞬时」类会自动重新 dispatch，其余把证据打包交给 DSH
#   4. 状态写进 $STATE_DIR/state.json，交接任务进 $DSH_HANDOFF_HOME
#
# 为什么自愈这么保守：改提交历史、解冲突、动探测器都要判断，脚本硬干容易把
# 小故障放成大故障。脚本负责「发现 + 归因 + 通知 + 重试瞬时失败」，
# 判断和动手交给 DSH（见 ops/dsh/）：DSH 在本机跑着，能读整个仓库、能 ssh 到
# 这台机器上跑构建、能提交推送，比一次性的网关问答强得多。
#
# 这个脚本**不调用任何模型**。原来的 llm_advise 走 AI 网关只产出三段文案，没人看
# 就等于没做；现在改成入队，由 DSH 真正去处理（读日志、改仓库、验证、推送）。
#
# 安全阀：
#   CHECK_ONLY=1           只看不动作（演练用，也不入队）
#   MAX_RETRY_PER_DAY=2    最多自动重试几次
#   STALE_HOURS=36         多久没成功算「停滞」
#   SC_SENDKEY=...         Server酱 SendKey，设了才推送
#
# 退出码：0 正常/进行中；1 需要 DSH 或人工；2 已自动重试；3 自身出错
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
            DSH_HANDOFF|DSH_HANDOFF_HOME)
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

# ---------------------------------------------------------------- DSH 交接
# 判断不归脚本管，但证据要替 DSH 打齐：它得知道「现在有事、事是什么、现场在哪」。
# 所以这里只做三件事 —— 把 CI 全量日志落成证据文件、enqueue 一个任务、记住任务号。
# 同指纹（同一次 run）不重复入队，30 分钟一轮不会堆出几十份同样的任务。
#
# 为什么把整份日志落盘而不是截断：早先给模型喂 3 行片段时它只能复述片段，
# 给 468 行全文时它自己找到了具体提交号、数出 8 个违规提交。DSH 同理，而且
# 它还能沿着任务里的仓库路径自己 ssh 过去翻更多东西。
DSH_HANDOFF=${DSH_HANDOFF:-/root/work/bootimus/ops/dsh/dsh-handoff.sh}
DSH_HANDOFF_HOME=${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}
HANDOFF_TASK=""

# handoff_enqueue <kind> <fingerprint> <full_log>
# 成功时把任务 id 写进 HANDOFF_TASK；失败只记日志，不打断原有通知链路。
handoff_enqueue() {
    local kind="$1" fp="$2" full_log="$3"
    [ -r "$DSH_HANDOFF" ] || { log "没有 $DSH_HANDOFF，跳过交班（照旧通知人工）"; return 0; }

    local evid="$DSH_HANDOFF_HOME/evidence"
    mkdir -p "$evid" 2>/dev/null || true
    local logfile=""
    if [ -n "$full_log" ]; then
        logfile="$evid/ci-$RUN_ID.log"
        printf '%s\n' "$full_log" > "$logfile" 2>/dev/null || logfile=""
    fi

    local payload
    payload=$(jq -n \
        --arg kind "$kind" --arg fp "$fp" \
        --arg health "$HEALTH" --arg failkind "$KIND" --arg step "$STEP" \
        --arg detail "$DETAIL" --arg behind "$BEHIND" --arg run_id "$RUN_ID" \
        --arg run_url "$RUN_URL" --arg sha "$HEAD_SHA" --arg last_ok "$LAST_OK_AT" \
        --arg offenders "$TRAILER_BAD" --arg logfile "$logfile" \
        --arg state "$STATE" --arg log "$LOG" \
        '{kind:$kind, fingerprint:$fp, priority:1,
          signal:{health:$health, failure_kind:$failkind, step:$step, detail:$detail,
                  behind_upstream:($behind|tonumber? // null),
                  trailer_offenders:$offenders,
                  last_success_at:$last_ok},
          run:{id:$run_id, sha:$sha, url:$run_url},
          repo:{workdir:"/root/work/bootimus", repo:"goovoz/bootimus",
                upstream:"garybowers/bootimus", branch:"main"},
          evidence:{ci_log:$logfile, state_json:$state, watchdog_log:$log},
          playbook:"ops/dsh/HANDOFF-PROTOCOL.md#sync-failure"}') \
        || { log "构造交班任务失败"; return 0; }

    local out
    out=$(printf '%s' "$payload" | DSH_HANDOFF_HOME="$DSH_HANDOFF_HOME" bash "$DSH_HANDOFF" enqueue) \
        || { log "入队失败：$out"; return 0; }

    case "$out" in
        SKIP\ *) HANDOFF_TASK=${out#SKIP }; log "交班任务已存在：$HANDOFF_TASK" ;;
        "")      log "入队无输出" ;;
        *)       HANDOFF_TASK=$out;         log "已交给 DSH：$HANDOFF_TASK" ;;
    esac
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
        # 顺手去掉时间戳前缀和 ANSI 转义，证据文件给 DSH 看时是干净文本
        FULL_LOG=$(api -L "$API/repos/$REPO/actions/jobs/$JOB_ID/logs" 2>/dev/null \
                   | sed 's/^[0-9T:.Z-]* //' | sed 's/\x1b\[[0-9;]*m//g')
        LOG_TAIL=$(printf '%s\n' "$FULL_LOG" | tail -60)
        # 归因要的是「失败那一段」，不是日志末尾 —— 末尾往往是 cleanup 这类成功步骤。
        # 早先直接 tail -40，看到的全是正常输出，只能瞎猜。
        eline=$(printf '%s\n' "$FULL_LOG" | grep -n '::error::' | head -1 | cut -d: -f1)
        if [ -n "${eline:-}" ]; then
            ERR_SNIPPET=$(printf '%s\n' "$FULL_LOG" | sed -n "$(( eline > 25 ? eline - 25 : 1 )),$(( eline + 5 ))p")
        else
            ERR_SNIPPET=$(printf '%s\n' "$FULL_LOG" | tail -40)
        fi
    fi
    ERRLINE=$(printf '%s\n' "$LOG_TAIL" | grep -o '::error::.*' | tail -2 | tr '\n' ' ')

    # 认证类失败必须先于步骤名判定：Checkout 阶段的 401 / 缺凭据不是瞬时故障，
    # 盲重试多少次结论都一样（2026-11-03 SYNC_TOKEN 失效，白跑了两轮 dispatch）。
    # 注意要扫 FULL_LOG 而不是 LOG_TAIL：checkout 失败时日志尾部是 cleanup，
    # 真正的 401 在中间（实测 tail -60 抓不到）。
    if printf '%s\n' "$FULL_LOG" | grep -qE 'could not read Username|Bad credentials|[Aa]uthentication failed|remote: Invalid username or password|401 Unauthorized'; then
        KIND=PERMISSION
        DETAIL="GitHub 认证失败（检查 secret SYNC_TOKEN 是否有效、是否有 workflows 写权限）"
    else
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
    fi
    [ -n "$ERRLINE" ] && DETAIL="$DETAIL ｜ $ERRLINE"
fi

# 归因到这里就结束了 —— 不再问模型。真正需要看懂日志的是 DSH，见 6b 的交班。

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
  --argjson needs_ai "$NEEDS_AI" --arg offenders "$TRAILER_BAD" \
  '{checked_at:$checked_at, health:$health,
    run:{id:$run_id, conclusion:$concl, created_at:$created, sha:$sha, url:$url},
    last_success_at:$last_ok, behind_upstream:($behind|tonumber? // null),
    failure:{kind:$kind, step:$step, detail:$detail, trailer_offenders:$offenders},
    action:{taken:$action, retry_count:$retry, retry_date:$retry_date},
    needs_ai:$needs_ai,
    dsh_task:null,
    last_notified:($prev|try .last_notified catch null)}' \
  --argjson prev "$PREV" > "$STATE.tmp" && mv "$STATE.tmp" "$STATE"

# ---------------------------------------------------------------- 6b. 需要判断时交给 DSH
# 判定 NEEDS_AI 说明不是瞬时故障（重试没用），是得有人看懂日志、动仓库的事。
# 以前这里 exec /usr/local/sbin/bootimus-auto-resolve，由它逐冲突块问 AI 网关；
# 那个方案是一次一问答、看不到仓库上下文，改错一点就只能整体回滚。
# 现在只入队：DSH 领走后在 /root/work/bootimus 上自己 rebase、自己改、自己验证、
# 自己推。脚本不替它判断，也不等它 —— 下一轮定时器看到变绿（或看到 DSH 写回的
# 失败结论）就够了。
if [ "$NEEDS_AI" = "true" ]; then
    if [ "$CHECK_ONLY" = "1" ]; then
        log "需要判断：CHECK_ONLY，不派遣"
    else
        _kind="sync-failure"
        _fp="sync-failure/$RUN_ID"
        if [ "$HEALTH" = "STALE" ]; then
            _kind="sync-stale"
            _fp="sync-stale/${LAST_OK_AT:-unknown}"
        fi
        handoff_enqueue "$_kind" "$_fp" "$FULL_LOG"
        if [ -n "$HANDOFF_TASK" ]; then
            ACTION="handed_to_dsh"
            jq --arg t "$HANDOFF_TASK" '.dsh_task=$t | .action.taken="handed_to_dsh"' \
                "$STATE" > "$STATE.tmp" 2>/dev/null \
                && mv "$STATE.tmp" "$STATE" || true
        else
            ACTION="handoff_failed"
        fi
    fi
fi

log "健康=$HEALTH 类型=${KIND:-无} 落后=${BEHIND:-?} 动作=$ACTION 需DSH=$NEEDS_AI${HANDOFF_TASK:+ 任务=$HANDOFF_TASK}"

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
    if [ -n "$HANDOFF_TASK" ]; then
        BODY="$BODY

**已交给 DSH**：\`$HANDOFF_TASK\`
DSH 会在这个任务上读日志、改仓库、验证后推送，结论写回任务。
查看：\`bash ops/dsh/dsh-handoff.sh show $HANDOFF_TASK\`"
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
