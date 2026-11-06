#!/usr/bin/env bash
# 冲突哨兵：只要还有「需要人工」的同步 PR 挂着，就把活交给 DSH
#
# 为什么需要它：sync-watchdog 的入队条件是「CI run 失败」。可 PR 是长期存在的对象 ——
# 那次失败的 run 早被清理了、上游又动了把 PR 变成 dirty、或者 PR 建好后一直没人管，
# 这些情况 watchdog 都看不见（2026-10-29～30 实测有三条 sync-conflict 分支推上去、
# PR 也开了，然后静默躺在那里）。
#
# 判定（任一成立即视为「需要 DSH」）：
#   1. open PR 标题含「需人工」
#   2. open PR 的 mergeable_state = dirty（真冲突）
#   3. 没有 PR，但远端存在 sync-conflict/* 分支，且其 head commit 带 Fork-Meta: conflict
#
# 幂等：fingerprint = 该分支 head sha，同一个 head 只入队一次（由 dsh-handoff 保证）。
# 只读 + 入队：不改仓库、不 dispatch、不推送、不调用任何模型。
#
# 安全阀：
#   --dry-run   只打印判定与任务 JSON，不入队
#   --selftest  用假数据跑五种判定
set -uo pipefail

REPO=${REPO:-goovoz/bootimus}
UPSTREAM=${UPSTREAM:-garybowers/bootimus}
API=${API:-https://api.github.com}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-conflict-watch}
LOG=${LOG:-${STATE_DIR}/watch.log}
DSH_HANDOFF=${DSH_HANDOFF:-/root/work/bootimus/ops/dsh/dsh-handoff.sh}
DSH_HANDOFF_HOME=${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}
SYNC_STATE=${SYNC_STATE:-/var/lib/bootimus-sync-watch/state.json}
SYNC_LOG=${SYNC_LOG:-/var/log/bootimus-sync-watch.log}
BRANCH_FRESH_DAYS=${BRANCH_FRESH_DAYS:-3}   # 没有 PR 的冲突分支只认这么新的，更老的交给 prune
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

mkdir -p "$STATE_DIR" 2>/dev/null

[ -z "$GH_TOKEN" ] && [ -r /etc/default/bootimus-conflict-watch ] &&
    GH_TOKEN=$(sed -n 's/^GH_TOKEN=//p' /etc/default/bootimus-conflict-watch | head -1 | tr -d '"'"'"' \r')
[ -z "$GH_TOKEN" ] && [ -r /root/gh_pat.txt ] && GH_TOKEN=$(tr -d '\n' < /root/gh_pat.txt)
[ -z "$GH_TOKEN" ] && [ -r /root/.github_pat ] && GH_TOKEN=$(tr -d '\r\n' < /root/.github_pat)

log() { printf '%s  %s\n' "$(date '+%F %T')" "$*" >>"$LOG" 2>/dev/null; printf '%s\n' "$*"; }
say() { printf '%s\n' "$*"; }

api() {
    curl -sS --max-time 25 -H "Authorization: Bearer $GH_TOKEN" \
        -H 'Accept: application/vnd.github+json' "$@"
}

# ---------------------------------------------------------------- 采集（可注入假数据）
pulls_json() {
    if [ -n "${CFT_FAKE_FILE:-}" ]; then
        jq -c '.pulls // []' "$CFT_FAKE_FILE"; return 0
    fi
    api "$API/repos/$REPO/pulls?state=open&per_page=50" | jq -c '.' 2>/dev/null || echo '[]'
}

pr_detail() {   # <number> → 单条 PR JSON（含 mergeable_state）
    local n="$1"
    if [ -n "${CFT_FAKE_FILE:-}" ]; then
        jq -c --argjson n "$n" '[.pulls[] | select(.number == $n)][0] // {}' "$CFT_FAKE_FILE"; return 0
    fi
    api "$API/repos/$REPO/pulls/$n"
}

branches_json() {
    if [ -n "${CFT_FAKE_FILE:-}" ]; then
        jq -c '.branches // []' "$CFT_FAKE_FILE"; return 0
    fi
    api "$API/repos/$REPO/branches?per_page=100" | jq -c '.' 2>/dev/null || echo '[]'
}

branch_detail() {   # <branch name> → 单条分支 JSON（含 head commit message）
    local ref="$1"
    if [ -n "${CFT_FAKE_FILE:-}" ]; then
        jq -c --arg b "$ref" '[.branches[] | select(.name == $b)][0] // {}' "$CFT_FAKE_FILE"; return 0
    fi
    api "$API/repos/$REPO/branches/$ref"
}

# ---------------------------------------------------------------- 入队
enqueue_task() {   # <branch> <head sha> <pr number|""> <pr url> <pr title> <mergeable_state> <reason> <head message>
    local br="$1" sha="$2" pn="$3" purl="$4" ptitle="$5" pstate="$6" reason="$7" hmsg="$8"
    local fp="sync-conflict/${sha:0:12}"
    local behind
    behind=$( [ -r "$SYNC_STATE" ] && jq -r '.behind_upstream // 0' "$SYNC_STATE" 2>/dev/null || echo 0 )

    local payload
    payload=$(jq -n \
        --arg kind "sync-failure" --arg fp "$fp" --arg reason "$reason" \
        --arg br "$br" --arg sha "$sha" --arg hmsg "$hmsg" \
        --arg pn "$pn" --arg purl "$purl" --arg ptitle "$ptitle" --arg pstate "$pstate" \
        --arg behind "$behind" --arg syncstate "$SYNC_STATE" --arg synclog "$SYNC_LOG" \
        '{kind:$kind, fingerprint:$fp, priority:1,
          signal:{health:"FAILED", failure_kind:"CONFLICT", step:"PR 待人工", detail:$reason,
                  source:"pr-state", behind_upstream:($behind|tonumber? // null)},
          pr:{number:($pn|tonumber? // null), url:$purl, title:$ptitle,
              mergeable_state:$pstate, head_ref:$br},
          conflict:{branch:$br, head_sha:$sha, head_message:$hmsg},
          repo:{workdir:"/root/work/bootimus", repo:"goovoz/bootimus",
                upstream:"garybowers/bootimus", branch:"main"},
          evidence:{state_json:$syncstate, watchdog_log:$synclog},
          playbook:"ops/dsh/HANDOFF-PROTOCOL.md#sync-failure",
          hint:"冲突分支已在远端（别重跑 sync-upstream）：auto-resolve-sync.sh --collect 出证据 → 逐处判断上游意图、保留 Fork-Patch trailer 与 [bootimus-fork L1] 锚点 → --verify → --publish；禁止 -X ours/-X theirs 一把梭。收尾 report 带 commit sha。"}') || { say "RESULT=ERROR:payload"; return 3; }

    if [ "$DRY" = "1" ]; then
        log "（dry-run）需要 DSH：$reason（分支 $br，head ${sha:0:8}，PR ${pn:-无}）"
        printf '%s\n' "$payload"
        say "RESULT=DRY"; return 0
    fi

    [ -r "$DSH_HANDOFF" ] || { say "RESULT=ERROR:no-handoff"; return 3; }
    local out
    out=$(printf '%s' "$payload" | DSH_HANDOFF_HOME="$DSH_HANDOFF_HOME" bash "$DSH_HANDOFF" enqueue) \
        || { say "RESULT=ERROR:enqueue-failed"; return 3; }
    case "$out" in
        SKIP\ *) log "已有同指纹任务（$reason，head ${sha:0:8}）：${out#SKIP }"; say "RESULT=SKIP:${out#SKIP }" ;;
        "")       say "RESULT=ERROR:enqueue-empty" ;;
        *)        log "已交给 DSH：$out（$reason，分支 $br，head ${sha:0:8}）"; say "RESULT=ENQUEUE:$out" ;;
    esac
}

# ---------------------------------------------------------------- 主判定
scan() {
    local pulls n i title ref sha mstate url pn reasons found=0
    pulls=$(pulls_json)

    local count
    count=$(printf '%s' "$pulls" | jq 'length' 2>/dev/null || echo 0)
    i=0
    while [ "${i:-0}" -lt "${count:-0}" ] 2>/dev/null; do
        row=$(printf '%s' "$pulls" | jq -r --argjson i "$i" '.[$i] | [
                (.number // 0 | tostring),
                (.title // ""),
                (.head.ref // ""),
                (.html_url // ""),
                (.head.sha // ""),
                (.mergeable_state // "")
              ] | @tsv')
        IFS=$'\t' read -r n title ref url sha mstate <<<"$row"

        reasons=""
        case "$title" in *需人工*) reasons="PR 标题含[需人工]" ;; esac
        case "$ref"  in sync-conflict/*) reasons="${reasons:+$reasons；}分支是 sync-conflict/*" ;; esac

        # list 接口不一定带 mergeable_state，候选 PR 再取一次详情
        if [ -n "$reasons" ] && [ -z "$mstate" ] && [ -z "${CFT_FAKE_FILE:-}" ]; then
            detail=$(pr_detail "$n")
            mstate=$(printf '%s' "$detail" | jq -r '.mergeable_state // ""')
            [ -n "$sha" ] || sha=$(printf '%s' "$detail" | jq -r '.head.sha // ""')
        fi
        [ "$mstate" = "dirty" ] && reasons="${reasons:+$reasons；}mergeable_state=dirty"

        if [ -n "$reasons" ]; then
            found=1
            enqueue_task "$ref" "$sha" "$n" "$url" "$title" "$mstate" "$reasons" ""
            return $?
        fi
        i=$((i + 1))
    done

    # 没有 PR 也要看分支：PR 可能因权限没建起来（gh pr create 403），分支却推上去了
    local brs bcnt name bsha bconflict bsubject bdate age_days bepoch
    brs=$(branches_json)
    bcnt=$(printf '%s' "$brs" | jq 'length' 2>/dev/null || echo 0)
    i=0
    while [ "${i:-0}" -lt "${bcnt:-0}" ] 2>/dev/null; do
        name=$(printf '%s' "$brs" | jq -r --argjson i "$i" '.[$i].name // ""')
        case "$name" in
            sync-conflict/*)
                row=$(printf '%s' "$brs" | jq -r --argjson i "$i" '.[$i] | [
                        (.commit.sha // ""),
                        (((.commit.commit.message // "") | test("Fork-Meta: conflict|合并产生冲突|冲突标记已原样提交") | tostring)),
                        ((.commit.commit.message // "") | split("\n")[0]),
                        (.commit.commit.committer.date // "")
                      ] | @tsv')
                IFS=$'\t' read -r bsha bconflict bsubject bdate <<<"$row"
                if [ -z "$bconflict" ] && [ -z "${CFT_FAKE_FILE:-}" ]; then
                    detail=$(branch_detail "$name")
                    row=$(printf '%s' "$detail" | jq -r '[
                            (.commit.sha // ""),
                            (((.commit.commit.message // "") | test("Fork-Meta: conflict|合并产生冲突|冲突标记已原样提交") | tostring)),
                            ((.commit.commit.message // "") | split("\n")[0]),
                            (.commit.commit.committer.date // "")
                          ] | @tsv')
                    IFS=$'\t' read -r bsha bconflict bsubject bdate <<<"$row"
                fi
                if [ "$bconflict" = "true" ]; then
                    # 老残留分支不该叫醒 DSH：超过 BRANCH_FRESH_DAYS 天交给 prune
                    age_days=999
                    if [ -n "$bdate" ]; then
                        bepoch=$(date -d "$bdate" +%s 2>/dev/null || echo 0)
                        if [ "${bepoch:-0}" -gt 0 ] 2>/dev/null; then
                            age_days=$(( ( $(date +%s) - bepoch ) / 86400 ))
                        fi
                    fi
                    if [ "${age_days:-999}" -gt "$BRANCH_FRESH_DAYS" ] 2>/dev/null; then
                        log "跳过旧冲突分支 $name（${age_days} 天前，超过 ${BRANCH_FRESH_DAYS} 天）"
                    else
                        found=1
                        enqueue_task "$name" "$bsha" "" "" "" "" \
                            "sync-conflict 分支的 head 是冲突提交，且没有对应 PR" "$bsubject"
                        return $?
                    fi
                fi ;;
        esac
        i=$((i + 1))
    done

    [ "$found" = "1" ] || { say "RESULT=CLEAN"; return 0; }
    return 0
}

# ---------------------------------------------------------------- 自检
selftest() {
    local base fail=0
    base=$(mktemp -d)
    mkdir -p "$base/q"
    cat > "$base/clean.json" <<'JSON'
{"pulls":[],"branches":[]}
JSON
    cat > "$base/human.json" <<'JSON'
{"pulls":[{"number":7,"title":"[需人工] chore(sync): 同步上游 abc","head":{"ref":"sync-conflict/20261105-0102","sha":"deadbeefcafe1111"},"mergeable_state":"unknown","html_url":"https://example/pull/7"}],"branches":[]}
JSON
    cat > "$base/dirty.json" <<'JSON'
{"pulls":[{"number":8,"title":"chore(sync): 同步上游 def","head":{"ref":"sync/upstream","sha":"cafebabe2222"},"mergeable_state":"dirty","html_url":"https://example/pull/8"}],"branches":[]}
JSON
    # 冲突分支夹具用动态日期，免得固定日期过几天就被当成「旧分支」而跳过
    day1=$(date -u -d '-1 day' '+%Y-%m-%dT%H:%M:%SZ')
    day10=$(date -u -d '-10 day' '+%Y-%m-%dT%H:%M:%SZ')
    printf '%s\n' "{\"pulls\":[],\"branches\":[{\"name\":\"sync-conflict/20261106-0008\",\"commit\":{\"sha\":\"feedface3333\",\"commit\":{\"message\":\"wip: 冲突标记已原样提交\\n\\nFork-Meta: conflict\",\"committer\":{\"date\":\"$day1\"}}}}]}" > "$base/branch-fresh.json"
    printf '%s\n' "{\"pulls\":[],\"branches\":[{\"name\":\"sync-conflict/20261020-0008\",\"commit\":{\"sha\":\"feedface4444\",\"commit\":{\"message\":\"wip: 冲突标记已原样提交\\n\\nFork-Meta: conflict\",\"committer\":{\"date\":\"$day10\"}}}}]}" > "$base/branch-old.json"

    run_case() {  # <名字> <期望前缀> <fixture> [第二次跑]
        local name="$1" want="$2" fx="$3" second="${4:-0}"
        local out got
        local home="$base/q"
        out=$(env STATE_DIR="$base/s-$name" LOG="$base/l-$name" \
                   DSH_HANDOFF_HOME="$home" DSH_HANDOFF=/root/work/bootimus/ops/dsh/dsh-handoff.sh \
                   CFT_FAKE_FILE="$fx" bash "$0" 2>&1)
        if [ "$second" = "1" ]; then
            out=$(env STATE_DIR="$base/s-$name" LOG="$base/l-$name" \
                       DSH_HANDOFF_HOME="$home" DSH_HANDOFF=/root/work/bootimus/ops/dsh/dsh-handoff.sh \
                       CFT_FAKE_FILE="$fx" bash "$0" 2>&1)
        fi
        got=$(printf '%s' "$out" | sed -n 's/^RESULT=//p' | head -1)
        case "$got" in
            "$want"*) printf '  PASS  %-22s -> %s\n' "$name" "$got" ;;
            *) printf '  FAIL  %-22s -> %s（期望 %s*）\n' "$name" "${got:-空}" "$want"
               printf '%s\n' "$out" | sed 's/^/        /'; fail=1 ;;
        esac
    }

    echo "=== 冲突哨兵自检 ==="
    run_case clean       CLEAN            "$base/clean.json"
    run_case human       ENQUEUE          "$base/human.json"
    run_case dirty       ENQUEUE          "$base/dirty.json"
    run_case branch-fresh ENQUEUE         "$base/branch-fresh.json"
    run_case branch-old   CLEAN           "$base/branch-old.json"
    run_case idempotent  SKIP             "$base/human.json" 1
    rm -rf "$base"
    if [ "$fail" = "0" ]; then echo "ALL PASS"; else echo "有用例失败"; return 1; fi
}

if [ "$SELFTEST" = "1" ]; then selftest; exit $?; fi
command -v jq >/dev/null || { echo "缺少 jq" >&2; exit 3; }
if [ -z "$GH_TOKEN" ] && [ -z "${CFT_FAKE_FILE:-}" ]; then
    echo "没有 GH_TOKEN：配 /etc/default/bootimus-conflict-watch，或放 /root/gh_pat.txt" >&2; exit 3
fi
scan
