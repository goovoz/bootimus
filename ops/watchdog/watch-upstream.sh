#!/usr/bin/env bash
# 上游哨兵：上游 main 一动，就催 Actions 去同步
#
# 为什么需要它：GitHub 的 schedule 不可靠。2026-11-05 实测 —— cron 定在 19:23Z，
# 实际 00:25Z 才跑（迟 5 小时 02 分）；前一天那次干脆没出现。同步挂在 cron 上等于靠运气。
#
# 这个脚本只做一件事：发现上游 SHA 变了 → POST 一次 workflow_dispatch。
# 不判断补丁、不合并、不推送、不碰仓库内容 —— 判断仍然是 sync-watchdog + DSH 的活。
#
# 判定顺序（任一成立就停手，输出 RESULT=SKIP:<原因>）：
#   1. 上游 SHA 与上次 dispatch 的一样        → already-dispatched
#   2. 我们的 main 已经包含这个上游 SHA       → already-contained（compare=identical/ahead）
#   3. 已有 queued / in_progress 的同步运行   → running
#   4. 其余                                   → dispatch，并把 SHA 记进状态
#
# 安全阀：
#   --dry-run   只打印判定，不 dispatch、不写状态
#   --selftest  用假数据跑三种判定，确认逻辑没写反
set -uo pipefail

REPO=${REPO:-goovoz/bootimus}
UPSTREAM=${UPSTREAM:-garybowers/bootimus}
UPSTREAM_BRANCH=${UPSTREAM_BRANCH:-main}
WORKFLOW=${WORKFLOW:-sync-upstream.yml}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-upstream-watch}
LOG=${LOG:-${STATE_DIR}/watch.log}
API=${API:-https://api.github.com}
GH_TOKEN=${GH_TOKEN:-}
DRY=0
SELFTEST=0

export TZ=${TZ:-Asia/Shanghai}

for a in "$@"; do
    case "$a" in
        --dry-run)  DRY=1 ;;
        --selftest) SELFTEST=1 ;;
        *) echo "未知参数：$a" >&2; exit 2 ;;
    esac
done

STATE="$STATE_DIR/state.json"
mkdir -p "$STATE_DIR" 2>/dev/null

# PAT：systemd 通过 EnvironmentFile(/etc/default/bootimus-upstream-watch) 注入；
# 手动跑时退回本机两个常见位置。
[ -z "$GH_TOKEN" ] && [ -r /etc/default/bootimus-upstream-watch ] &&
    GH_TOKEN=$(sed -n 's/^GH_TOKEN=//p' /etc/default/bootimus-upstream-watch | head -1 | tr -d '"'"'"' \r')
[ -z "$GH_TOKEN" ] && [ -r /root/gh_pat.txt ] && GH_TOKEN=$(tr -d '\n' < /root/gh_pat.txt)
[ -z "$GH_TOKEN" ] && [ -r /root/.github_pat ] && GH_TOKEN=$(tr -d '\r\n' < /root/.github_pat)

log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" >>"$LOG" 2>/dev/null; printf '%s\n' "$*"; }
say() { printf '%s\n' "$*"; }

api() {
    curl -sS --max-time 25 -H "Authorization: Bearer $GH_TOKEN" \
        -H 'Accept: application/vnd.github+json' "$@"
}

# ---------------------------------------------------------------- 采集（可注入假数据）
upstream_sha() {
    if [ -n "${UPW_FAKE_UPSTREAM_SHA:-}" ]; then say "$UPW_FAKE_UPSTREAM_SHA"; return 0; fi
    api "$API/repos/$UPSTREAM/commits/$UPSTREAM_BRANCH" | jq -r '.sha // ""'
}

main_sha() {
    if [ -n "${UPW_FAKE_MAIN_SHA:-}" ]; then say "$UPW_FAKE_MAIN_SHA"; return 0; fi
    api "$API/repos/$REPO/commits/main" | jq -r '.sha // ""'
}

compare_status() {   # <upstream sha>
    if [ -n "${UPW_FAKE_COMPARE_STATUS:-}" ]; then say "$UPW_FAKE_COMPARE_STATUS"; return 0; fi
    api "$API/repos/$REPO/compare/$1...main" | jq -r '.status // "unknown"'
}

active_runs() {
    if [ -n "${UPW_FAKE_ACTIVE_RUNS:-}" ]; then say "$UPW_FAKE_ACTIVE_RUNS"; return 0; fi
    local q p
    q=$(api "$API/repos/$REPO/actions/workflows/$WORKFLOW/runs?status=queued&per_page=1" | jq -r '.total_count // 0')
    p=$(api "$API/repos/$REPO/actions/workflows/$WORKFLOW/runs?status=in_progress&per_page=1" | jq -r '.total_count // 0')
    say "$(( ${q:-0} + ${p:-0} ))"
}

do_dispatch() {
    if [ -n "${UPW_FAKE_DISPATCH_FILE:-}" ]; then
        printf '%s\n' "$1" >> "$UPW_FAKE_DISPATCH_FILE"; return 0
    fi
    local code
    code=$(curl -sS --max-time 25 -o /dev/null -w '%{http_code}' -X POST \
        -H "Authorization: Bearer $GH_TOKEN" \
        -H 'Accept: application/vnd.github+json' \
        -H 'Content-Type: application/json' \
        -d '{"ref":"main"}' "$API/repos/$REPO/actions/workflows/$WORKFLOW/dispatches")
    case "$code" in
        204|201|200) return 0 ;;
        *) log "dispatch 失败：HTTP $code"; return 1 ;;
    esac
}

state_get() {
    [ -r "$STATE" ] || { say ""; return 0; }
    jq -r --arg k "$1" '.[$k] // "" | tostring' "$STATE" 2>/dev/null || say ""
}

state_put() {   # <upstream sha> <dispatched:yes|no> <reason>
    [ "$DRY" = "1" ] && return 0
    local prev_last prev_count
    prev_last=$(state_get last_dispatched_sha)
    prev_count=$( [ -r "$STATE" ] && jq -r '.dispatch_count // 0' "$STATE" 2>/dev/null || echo 0 )
    [ -n "$prev_count" ] || prev_count=0
    local new_last="$prev_last" new_count="$prev_count"
    if [ "$2" = "yes" ]; then
        new_last="$1"; new_count=$(( prev_count + 1 ))
    fi
    jq -n --arg up "$1" --arg last "$new_last" --arg why "$3" \
          --arg now "$(date -u '+%F %T')" --argjson n "$new_count" \
        '{last_upstream_sha:$up, last_dispatched_sha:$last, last_check_at:$now,
          last_reason:$why, dispatch_count:$n}' > "$STATE.tmp" \
        && mv "$STATE.tmp" "$STATE"
}

# ---------------------------------------------------------------- 主判定
decide() {
    local up main cmp running last
    up=$(upstream_sha); [ -n "$up" ] || { say "RESULT=ERROR:no-upstream-sha"; return 3; }
    main=$(main_sha)
    last=$(state_get last_dispatched_sha)

    if [ "$up" = "$last" ]; then
        log "上游 ${up:0:8} 已经催过了，跳过（main=${main:0:8}）"
        say "RESULT=SKIP:already-dispatched"; return 0
    fi

    cmp=$(compare_status "$up")
    case "$cmp" in
        identical|ahead)
            log "main 已包含上游 ${up:0:8}（compare=$cmp），无需同步"
            state_put "$up" no already-contained
            say "RESULT=SKIP:already-contained"; return 0 ;;
    esac

    running=$(active_runs)
    if [ "${running:-0}" -gt 0 ] 2>/dev/null; then
        log "已有 $running 个同步运行在排队/进行中（上游 ${up:0:8}），本轮不打扰"
        say "RESULT=SKIP:running"; return 0
    fi

    if [ "$DRY" = "1" ]; then
        log "（dry-run）本该催同步：上游 ${up:0:8}，main ${main:0:8}，compare=$cmp"
        say "RESULT=DISPATCH"; return 0
    fi

    if do_dispatch "$up"; then
        state_put "$up" yes dispatched
        log "已催同步：上游 ${up:0:8}（main 原来 ${main:0:8}，compare=$cmp）"
        say "RESULT=DISPATCH"; return 0
    fi
    say "RESULT=ERROR:dispatch-failed"; return 3
}

# ---------------------------------------------------------------- 自检
selftest() {
    local base fail=0
    base=$(mktemp -d)
    run_case() {  # <名字> <期望 RESULT> <额外 env...>
        local name="$1" want="$2"; shift 2
        local out got
        out=$(env STATE_DIR="$base/s-$name" LOG="$base/l-$name" \
                   UPW_FAKE_DISPATCH_FILE="$base/d-$name" "$@" \
                   bash "$0" 2>&1)
        got=$(printf '%s' "$out" | sed -n 's/^RESULT=//p' | head -1)
        if [ "$got" = "$want" ]; then
            printf '  PASS  %-24s -> %s\n' "$name" "$got"
        else
            printf '  FAIL  %-24s -> %s（期望 %s）\n' "$name" "${got:-空}" "$want"
            printf '%s\n' "$out" | sed 's/^/        /'
            fail=1
        fi
    }
    echo "=== 上游哨兵自检 ==="
    run_case fresh     DISPATCH               UPW_FAKE_UPSTREAM_SHA=aaa111 UPW_FAKE_MAIN_SHA=bbb222 UPW_FAKE_COMPARE_STATUS=diverged UPW_FAKE_ACTIVE_RUNS=0
    run_case contained SKIP:already-contained UPW_FAKE_UPSTREAM_SHA=aaa111 UPW_FAKE_MAIN_SHA=bbb222 UPW_FAKE_COMPARE_STATUS=ahead    UPW_FAKE_ACTIVE_RUNS=0
    run_case running   SKIP:running           UPW_FAKE_UPSTREAM_SHA=aaa111 UPW_FAKE_MAIN_SHA=bbb222 UPW_FAKE_COMPARE_STATUS=diverged UPW_FAKE_ACTIVE_RUNS=1
    rm -rf "$base"
    if [ "$fail" = "0" ]; then echo "ALL PASS"; else echo "有用例失败"; return 1; fi
}

if [ "$SELFTEST" = "1" ]; then selftest; exit $?; fi
command -v jq >/dev/null || { echo "缺少 jq" >&2; exit 3; }
if [ -z "$GH_TOKEN" ] && [ -z "${UPW_FAKE_UPSTREAM_SHA:-}" ]; then
    echo "没有 GH_TOKEN：配 /etc/default/bootimus-upstream-watch，或放 /root/gh_pat.txt" >&2; exit 3
fi
decide
