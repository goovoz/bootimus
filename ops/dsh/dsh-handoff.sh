#!/usr/bin/env bash
# bootimus ⇄ DSH 交接客户端（跑在 101 上；本机 DSH 通过 ssh 调它）
#
# 链路：
#   101 的定时任务（sync-watchdog / i18n autofix）发现「需要判断」的事
#     → enqueue 一个任务
#     → 本机 DSH 领走（claim）、在 101 上动手（ssh）、回来 report
#
# 这个脚本是队列唯一入口：**不发任何网络请求、不调用任何模型**。
# 换掉原来的 AI 网关正是为了这点 —— 判断由 DSH 做，脚本只负责记录和交付证据。
#
# 目录（DSH_HANDOFF_HOME，默认 /var/lib/bootimus-dsh）：
#   tasks/<id>.json       任务本体，status 字段即生命周期
#   evidence/<id>.*       证据包（完整 CI 日志、冲突现场、待翻文本…）
#   .lock                 flock，串行化写入
#
# 生命周期：
#   queued ──claim──> claimed ──report done──> done
#                          └────report failed──> failed
#   claimed 带租约 lease_until（默认 90 分钟）。DSH 半路挂掉后租约过期，
#   任务会被 list --stale 列为可重领，不会永久卡死。
#
# 幂等：同 fingerprint 已有 queued/claimed 任务时 enqueue 直接返回 SKIP。
# 定时任务每 30 分钟跑一次，不会堆出几十份同样的任务。
#
# 用法见 ops/dsh/README.md 与 ops/dsh/HANDOFF-PROTOCOL.md。
set -uo pipefail

DSH_HANDOFF_HOME=${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}
TASKS=$DSH_HANDOFF_HOME/tasks
EVID=$DSH_HANDOFF_HOME/evidence
LOCK=$DSH_HANDOFF_HOME/.lock
TTL_DEFAULT=${TTL_DEFAULT:-5400}

die() { echo "dsh-handoff: $*" >&2; exit 3; }
ensure_dirs() { mkdir -p "$TASKS" "$EVID" || die "无法创建 $DSH_HANDOFF_HOME"; }
now_utc() { date -u '+%F %T'; }
task_path() { printf '%s/%s.json' "$TASKS" "$1"; }

# with_lock <cmd...>：所有写操作串行化，避免 watchdog 与 i18n 定时器撞车
with_lock() {
    ensure_dirs
    exec 9>"$LOCK" || die "打不开锁文件"
    flock 9 || die "拿不到锁"
    "$@"
}

# 遍历任务文件，对每个调用 fn <路径>；fn 的 stdout 直接透传
each_task() {
    local f
    for f in "$TASKS"/*.json; do
        [ -e "$f" ] || continue
        "$@" "$f"
    done
}

# ---------------------------------------------------------------- enqueue
# enqueue --kind K --fingerprint FP [--priority N] [--issue N] [--task-file F|-]
#
# 从 stdin / --task-file 读一份「半成品」任务 JSON，补齐 id/created_at/status 后落盘。
# stdout：任务 id，或 "SKIP <已有id>"。
cmd_enqueue() {
    local kind="" fp="" priority="" issue="" src="-"
    while [ $# -gt 0 ]; do
        case "$1" in
            --kind)        kind=$2; shift 2 ;;
            --fingerprint) fp=$2; shift 2 ;;
            --priority)    priority=$2; shift 2 ;;
            --issue)       issue=$2; shift 2 ;;
            --task-file)   src=$2; shift 2 ;;
            -)             src="-"; shift ;;
            *) die "enqueue 未知参数：$1" ;;
        esac
    done

    local raw
    if [ "$src" = "-" ]; then raw=$(cat); else [ -r "$src" ] || die "读不到 $src"; raw=$(cat "$src"); fi
    printf '%s' "$raw" | jq -e . >/dev/null 2>&1 || die "任务不是合法 JSON"

    [ -n "$kind" ]     && raw=$(printf '%s' "$raw" | jq --arg k "$kind" '.kind=$k')
    [ -n "$fp" ]       && raw=$(printf '%s' "$raw" | jq --arg f "$fp" '.fingerprint=$f')
    [ -n "$priority" ] && raw=$(printf '%s' "$raw" | jq --argjson p "$priority" '.priority=$p')
    [ -n "$issue" ]    && raw=$(printf '%s' "$raw" | jq --argjson i "$issue" '.issue=$i')

    kind=$(printf '%s' "$raw" | jq -r '.kind // ""')
    fp=$(printf '%s' "$raw" | jq -r '.fingerprint // ""')
    [ -n "$kind" ] || die "缺 kind"
    [ -n "$fp" ]   || die "缺 fingerprint"

    # 去重：同指纹还没结束的就不再入队
    local existing=""
    local f s k
    for f in "$TASKS"/*.json; do
        [ -e "$f" ] || continue
        k=$(jq -r '.fingerprint // ""' "$f" 2>/dev/null) || continue
        [ "$k" = "$fp" ] || continue
        s=$(jq -r '.status // ""' "$f" 2>/dev/null)
        case "$s" in
            queued|claimed) existing=$(basename "$f" .json); break ;;
        esac
    done
    if [ -n "$existing" ]; then echo "SKIP $existing"; return 0; fi

    local id
    id=$(printf '%s-%s-%s' "$(date -u '+%Y%m%dT%H%M%S')" \
            "$(printf '%s' "$kind" | tr -c 'a-zA-Z0-9._-' '-')" \
            "$(printf '%s' "$fp" | md5sum | cut -c1-8)")

    local out; out=$(task_path "$id")
    printf '%s' "$raw" | jq \
        --arg id "$id" --arg now "$(now_utc)" '
        .id=$id
        | .created_at=$now
        | .status="queued"
        | .attempts=0
        | .priority=(.priority // 5)
        | .claimed_by=null | .claimed_at=null | .lease_until=null
        | .result=null' > "$out.tmp" || die "写任务失败"
    mv "$out.tmp" "$out"
    echo "$id"
}

# ---------------------------------------------------------------- list / show
status_match() {  # status_match <实际状态> <想要的逗号列表>
    local s="$1" want="$2"
    [ -z "$want" ] && return 0
    case ",$want," in *",$s,"*) return 0 ;; esac
    return 1
}

cmd_list() {
    local want="" stale_only=0 json=0
    while [ $# -gt 0 ]; do
        case "$1" in
            --status) want=$2; shift 2 ;;
            --stale)  stale_only=1; shift ;;
            --json)   json=1; shift ;;
            *) die "list 未知参数：$1" ;;
        esac
    done
    ensure_dirs
    local now; now=$(date +%s)
    local rows; rows=$(mktemp)
    local f s id created kind prio fp lease
    for f in "$TASKS"/*.json; do
        [ -e "$f" ] || continue
        s=$(jq -r '.status // "?"' "$f" 2>/dev/null) || continue
        status_match "$s" "$want" || continue
        if [ "$stale_only" = "1" ]; then
            [ "$s" = "claimed" ] || continue
            lease=$(jq -r '.lease_until // 0' "$f")
            [ "${lease:-0}" -lt "$now" ] || continue
        fi
        id=$(jq -r '.id // ""' "$f")
        created=$(jq -r '.created_at // ""' "$f")
        kind=$(jq -r '.kind // ""' "$f")
        prio=$(jq -r '.priority // 5' "$f")
        fp=$(jq -r '.fingerprint // ""' "$f")
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$s" "$kind" "$prio" "$created" "$fp" >> "$rows"
    done

    if [ "$json" = "1" ]; then
        jq -R -s 'split("\n") | map(select(length > 0) | split("\t"))
            | map({id:.[0], status:.[1], kind:.[2],
                   priority:(.[3] | tonumber?), created_at:.[4], fingerprint:.[5]})' "$rows"
        rm -f "$rows"; return 0
    fi
    if [ ! -s "$rows" ]; then echo "（队列为空）"; rm -f "$rows"; return 0; fi
    printf '%-38s %-8s %-14s %-4s %-20s %s\n' ID STATUS KIND PRIO CREATED_UTC FINGERPRINT
    while IFS=$'\t' read -r id s kind prio created fp; do
        printf '%-38s %-8s %-14s %-4s %-20s %s\n' "$id" "$s" "$kind" "$prio" "$created" "$fp"
    done < "$rows"
    rm -f "$rows"
}

cmd_show() {
    local id=${1:-}; [ -n "$id" ] || die "show 需要任务 id"
    [ -r "$(task_path "$id")" ] || die "没有这个任务：$id"
    jq . "$(task_path "$id")"
}

# ---------------------------------------------------------------- claim
# claim [--kind K] [--ttl 秒] [--id ID] [--by 名字]
# stdout：任务 JSON；没有可领的任务时退出码 4。
cmd_claim() {
    local kind="" ttl=$TTL_DEFAULT id="" by="dsh"
    while [ $# -gt 0 ]; do
        case "$1" in
            --kind) kind=$2; shift 2 ;;
            --ttl)  ttl=$2; shift 2 ;;
            --id)   id=$2; shift 2 ;;
            --by)   by=$2; shift 2 ;;
            *) die "claim 未知参数：$1" ;;
        esac
    done
    ensure_dirs
    local now; now=$(date +%s)
    local pick="" f s k lease
    if [ -n "$id" ]; then
        [ -r "$(task_path "$id")" ] || die "没有这个任务：$id"
        pick=$id
    else
        for f in "$TASKS"/*.json; do
            [ -e "$f" ] || continue
            s=$(jq -r '.status // ""' "$f" 2>/dev/null) || continue
            k=$(jq -r '.kind // ""' "$f" 2>/dev/null)
            if [ -n "$kind" ] && [ "$k" != "$kind" ]; then continue; fi
            case "$s" in
                queued) pick=$(basename "$f" .json); break ;;
                claimed)
                    lease=$(jq -r '.lease_until // 0' "$f")
                    if [ "${lease:-0}" -lt "$now" ]; then pick=$(basename "$f" .json); break; fi
                    ;;
            esac
        done
    fi
    if [ -z "$pick" ]; then echo "没有可领的任务${kind:+（kind=$kind）}" >&2; exit 4; fi

    local p; p=$(task_path "$pick")
    jq --arg by "$by" --arg now "$(now_utc)" --argjson ttl "$ttl" --argjson nowe "$now" '
        .status="claimed" | .claimed_by=$by | .claimed_at=$now
        | .attempts=((.attempts // 0) + 1)
        | .lease_until=($nowe + $ttl)
        | .lease_until_iso=(($nowe + $ttl) | todate)' "$p" > "$p.tmp" && mv "$p.tmp" "$p" \
        || die "领取失败"
    jq . "$p"
}

# ---------------------------------------------------------------- report
# report <id> --status done|failed [--message 文案] [--commit SHA] [--branch B] [--detail-file F]
cmd_report() {
    local id=${1:-}; [ $# -gt 0 ] && shift
    local st="" msg="" commit="" branch="" detail=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --status)      st=$2; shift 2 ;;
            --message)     msg=$2; shift 2 ;;
            --commit)      commit=$2; shift 2 ;;
            --branch)      branch=$2; shift 2 ;;
            --detail-file) detail=$2; shift 2 ;;
            *) die "report 未知参数：$1" ;;
        esac
    done
    [ -n "$id" ] || die "report 需要任务 id"
    case "$st" in done|failed) ;; *) die "--status 只能是 done 或 failed" ;; esac
    local p; p=$(task_path "$id")
    [ -r "$p" ] || die "没有这个任务：$id"

    local dtext=""
    [ -n "$detail" ] && [ -r "$detail" ] && dtext=$(cat "$detail")

    jq --arg st "$st" --arg msg "$msg" --arg commit "$commit" --arg branch "$branch" \
       --arg now "$(now_utc)" --arg dt "$dtext" '
        .status=$st
        | .finished_at=$now
        | .lease_until=null
        | .result={status:$st, message:$msg, commit:$commit, branch:$branch,
                   detail:(if $dt=="" then null else $dt end), at:$now}' \
        "$p" > "$p.tmp" && mv "$p.tmp" "$p" || die "写结果失败"
    echo "已记录 $id → $st"
}

# ---------------------------------------------------------------- 其它
cmd_status() {
    ensure_dirs
    local q=0 c=0 d=0 fl=0
    local f s
    for f in "$TASKS"/*.json; do
        [ -e "$f" ] || continue
        s=$(jq -r '.status // ""' "$f" 2>/dev/null)
        case "$s" in
            queued)  q=$((q + 1)) ;;
            claimed) c=$((c + 1)) ;;
            done)    d=$((d + 1)) ;;
            failed)  fl=$((fl + 1)) ;;
        esac
    done
    printf 'queued=%s claimed=%s done=%s failed=%s\n' "$q" "$c" "$d" "$fl"
}

cmd_gc() {
    local keep=14
    while [ $# -gt 0 ]; do
        case "$1" in --keep-days) keep=$2; shift 2 ;; *) die "gc 未知参数：$1" ;; esac
    done
    ensure_dirs
    local cutoff; cutoff=$(( $(date +%s) - keep * 86400 ))
    local f s ts e n=0
    for f in "$TASKS"/*.json; do
        [ -e "$f" ] || continue
        s=$(jq -r '.status // ""' "$f")
        case "$s" in done|failed) ;; *) continue ;; esac
        ts=$(jq -r '.finished_at // .created_at // ""' "$f")
        [ -n "$ts" ] || continue
        e=$(date -u -d "$ts" +%s 2>/dev/null || echo 0)
        if [ "$e" -gt 0 ] && [ "$e" -lt "$cutoff" ]; then rm -f "$f"; n=$((n + 1)); fi
    done
    echo "清理 $n 个过期任务（保留 ${keep} 天）"
}

usage() {
    cat <<'EOF'
dsh-handoff —— 101 与 DSH 之间的任务队列

  enqueue --kind K --fingerprint FP [--priority N] [--issue N] [--task-file F|-]
  list [--status S[,S…]] [--stale] [--json]
  show <id>
  claim [--kind K] [--ttl 秒] [--id ID] [--by 名字]
  report <id> --status done|failed [--message 文案] [--commit SHA] [--branch 分支] [--detail-file F]
  status
  gc [--keep-days 14]

环境：
  DSH_HANDOFF_HOME   队列根目录，默认 /var/lib/bootimus-dsh
  TTL_DEFAULT        claim 默认租约秒数，默认 5400（90 分钟）

退出码：0 成功；3 自身出错；4 claim 没有可领任务
EOF
}

command -v jq >/dev/null || die "缺少 jq"
cmd=${1:-}; [ $# -gt 0 ] && shift
case "$cmd" in
    enqueue) with_lock cmd_enqueue "$@" ;;
    list)    cmd_list "$@" ;;
    show)    cmd_show "$@" ;;
    claim)   with_lock cmd_claim "$@" ;;
    report)  with_lock cmd_report "$@" ;;
    status)  cmd_status ;;
    gc)      with_lock cmd_gc "$@" ;;
    ""|-h|--help|help) usage ;;
    *) usage; die "未知子命令：$cmd" ;;
esac
