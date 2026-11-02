#!/usr/bin/env bash
# 同步冲突处理工具箱 —— 现在由 DSH 驱动，脚本只当「手」，不当下判断的「脑」
#
# 以前这个文件叫 auto-resolve-sync.sh：自己检出冲突分支 → 逐冲突块问 AI 网关
# → 把答案写回文件 → 编译测试 → 通过就推。问题是问答式模型看不到仓库上下文，
# 一个块一个块地猜，猜错就得整体回滚；而且它把「怎么改」和「改了能不能过」
# 绑在一起，出问题时只剩一条日志。
#
# 现在拆成四个确定性动作，由 DSH 串起来（判断、怎么合并、要不要放弃都在 DSH 那边）：
#
#   --collect        把冲突现场导成证据（不解决、不留 rebase 残局），给 DSH 看
#   --fix-trailers   补齐缺 Fork-Patch / Fork-Meta 的提交（纯机械，可回滚）
#   --verify         gofmt / go build / go test / i18n 审计 / 标记复查 —— 推送闸门
#   --publish        验证通过后 force-with-lease 推 main
#
# 典型用法（DSH 在 101 上通过 ssh 执行）：
#   bash ops/watchdog/auto-resolve-sync.sh --collect
#   # …DSH 自己 rebase、改文件…
#   bash ops/watchdog/auto-resolve-sync.sh --fix-trailers
#   bash ops/watchdog/auto-resolve-sync.sh --verify
#   bash ops/watchdog/auto-resolve-sync.sh --publish
#
# 退出码：0 成功；1 动作失败（原因在 stdout/日志）；2 参数不对；3 环境不对
set -uo pipefail

WORKDIR=${WORKDIR:-/root/work/bootimus}
UPSTREAM_BRANCH=${UPSTREAM_BRANCH:-main}
LOG=${LOG:-/var/log/bootimus-sync-watch.log}
EVID_DIR=${EVID_DIR:-/var/lib/bootimus-dsh/evidence}
DRY_RUN=${DRY_RUN:-0}

log() { printf '%s  [sync-tool] %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }
die() { local rc="${2:-1}"; log "ERROR: $*"; exit "$rc"; }

[ -d "$WORKDIR" ] || die "进不去 $WORKDIR" 3
cd "$WORKDIR" || die "进不去 $WORKDIR" 3
command -v git >/dev/null || die "缺 git" 3
command -v jq  >/dev/null || die "缺 jq" 3

GO=$(command -v go || echo /usr/local/go/bin/go)
GOFMT=$("$GO" env GOROOT 2>/dev/null)/bin/gofmt

# ---------------------------------------------------------------- --collect
# 让冲突重现一次，逐文件导出冲突块，然后干净地 abort。
# 不留在 rebase 中间态：DSH 要自己在终端里一步步来，残局只会碍事。
do_collect() {
    local branch
    branch=$(git for-each-ref --sort=-creatordate --format='%(refname:short)' 'refs/heads/sync-conflict/*' | head -1)
    [ -n "$branch" ] || branch=$(git for-each-ref --sort=-creatordate --format='%(refname:short)' \
        'refs/remotes/origin/sync-conflict/*' | head -1 | sed 's#^origin/##')

    git rebase --abort >/dev/null 2>&1 || true
    git checkout -q -f main 2>/dev/null || git checkout -q -f master 2>/dev/null \
        || die "切不回 main" 3
    git fetch -q upstream || die "fetch upstream 失败"
    git fetch -q origin   || die "fetch origin 失败"

    local sha behind dirty
    sha=$(git rev-parse --short HEAD)
    behind=$(git rev-list --count "HEAD..upstream/$UPSTREAM_BRANCH" 2>/dev/null || echo '?')
    dirty=$(git status --porcelain | wc -l)

    mkdir -p "$EVID_DIR" || die "建不了 $EVID_DIR" 3
    local stamp; stamp=$(date -u '+%Y%m%dT%H%M%S')
    local out="$EVID_DIR/conflict-$stamp"
    mkdir -p "$out"

    log "冲突分支：${branch:-（无）}  落后上游：$behind  HEAD=$sha  脏文件：$dirty"

    # 先把上游新增的提交和 fork 自己的补丁各留一份 —— 判断「上游为什么这么改」靠它
    git log --oneline -20 "HEAD..upstream/$UPSTREAM_BRANCH" > "$out/upstream-new.log" 2>/dev/null || true
    git log --format='%H %s%n%b' -20 "upstream/$UPSTREAM_BRANCH..HEAD" > "$out/fork-patches.log" 2>/dev/null || true

    # 真去 rebase 一次，只为把冲突逼出来
    local rebase_ok=0
    GIT_EDITOR=true git rebase "upstream/$UPSTREAM_BRANCH" > "$out/rebase.log" 2>&1 || rebase_ok=1

    if [ "$rebase_ok" = "0" ]; then
        log "rebase 无冲突（上游可能已经把补丁合并了）"
        git rebase --abort >/dev/null 2>&1 || true
    else
        local files; files=$(git diff --name-only --diff-filter=U)
        if [ -z "$files" ]; then
            log "rebase 失败但没有冲突文件，看 $out/rebase.log"
        else
            log "冲突文件：$(echo "$files" | tr '\n' ' ')"
            local f n=0
            for f in $files; do
                n=$((n + 1))
                {
                    printf '# 冲突文件：%s\n' "$f"
                    printf '# 冲突块数：%s\n\n' "$(grep -c '^<<<<<<<' "$f" 2>/dev/null || echo 0)"
                    cat "$f"
                } > "$out/$(printf '%02d' "$n")-$(echo "$f" | tr '/' '_').conflict" 2>/dev/null
                # 三方对照（base/ours/theirs）比两方对照好判断得多
                git show ":1:$f" > "$out/$(printf '%02d' "$n")-$(echo "$f" | tr '/' '_').base" 2>/dev/null || true
            done
            git diff --name-only --diff-filter=U > "$out/conflicted-files.txt"
        fi
        git rebase --abort >/dev/null 2>&1 || true
    fi

    git checkout -q main 2>/dev/null || true

    jq -n --arg dir "$out" --arg branch "${branch:-}" --arg sha "$sha" \
          --arg behind "$behind" \
          --argjson ok "$([ "$rebase_ok" = 0 ] && echo true || echo false)" \
          --arg files "$(cat "$out/conflicted-files.txt" 2>/dev/null | tr '\n' ' ')" \
        '{evidence_dir:$dir, conflict_branch:$branch, head_sha:$sha,
          behind_upstream:($behind|tonumber? // null), rebase_clean:$ok,
          conflicted_files:($files|split(" ")|map(select(length>0)))}' > "$out/summary.json"

    log "证据目录：$out"
    cat "$out/summary.json"
    if [ "$rebase_ok" = "0" ]; then return 0; else return 1; fi
}

# ---------------------------------------------------------------- --fix-trailers
# 给 upstream..HEAD 里缺 Fork-Patch / Fork-Meta 的提交补标记。
# 纯机械改写，不需要判断，但确实改写历史 —— 所以工作区脏就拒绝动手。
do_fix_trailers() {
    local bad=() sha body
    for sha in $(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD); do
        body=$(git log -1 --format='%B' "$sha")
        printf '%s' "$body" | grep -qE '^Fork-(Patch|Meta):' || bad+=("$sha")
    done

    if [ ${#bad[@]} -eq 0 ]; then log "标记齐全，无需补"; return 0; fi

    log "缺标记的提交 ${#bad[@]} 个："
    for sha in "${bad[@]}"; do log "  $(git log -1 --format='%h %s' "$sha")"; done

    [ -n "$(git status --porcelain)" ] && die "工作区不干净，不敢改写历史" 1

    local backup; backup=$(git rev-parse HEAD)
    log "改写前 HEAD=$backup（失败用 git reset --hard $backup 回滚）"

    FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f --msg-filter '
        body=$(cat)
        if printf "%s" "$body" | grep -qE "^Fork-(Patch|Meta):"; then
            printf "%s\n" "$body"
        else
            title=$(printf "%s" "$body" | head -1)
            slug=$(printf "%s" "$title" | sed -n "s/^\([a-z][a-z]*\)(\([a-z0-9_-]*\)):.*/\1-\2/p")
            [ -z "$slug" ] && slug="auto-$(printf "%s" "$GIT_COMMIT" | cut -c1-8)"
            printf "%s\n\nFork-Patch: %s\n" "$body" "$slug"
        fi
    ' "upstream/$UPSTREAM_BRANCH"..HEAD > /tmp/synctool-trailer.log 2>&1 \
        || { tail -5 /tmp/synctool-trailer.log | while read -r l; do log "    $l"; done
             die "filter-branch 失败" 1; }

    local still=0
    for sha in $(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD); do
        body=$(git log -1 --format='%B' "$sha")
        printf '%s' "$body" | grep -qE '^Fork-(Patch|Meta):' || still=$((still + 1))
    done
    [ "$still" -ne 0 ] && die "补完还有 $still 个缺标记" 1

    log "✓ 标记已补齐：$backup → $(git rev-parse --short HEAD)"
    return 0
}

# ---------------------------------------------------------------- --verify
# 推送前唯一闸门，全绿才允许 --publish。
do_verify() {
    local fail=0

    log "=== 1/5 gofmt ==="
    if [ -x "$GOFMT" ]; then
        if ! "$GOFMT" -l . > /tmp/synctool-fmt.log 2>&1; then
            log "  ✗ gofmt 执行失败"; fail=1
        elif [ -s /tmp/synctool-fmt.log ]; then
            log "  ! 有未格式化文件，自动格式化：$(wc -l < /tmp/synctool-fmt.log) 个"
            # shellcheck disable=SC2046
            "$GOFMT" -w $(cat /tmp/synctool-fmt.log) 2>/dev/null || true
        else
            log "  ✓ 格式一致"
        fi
    else
        log "  ! 没有 gofmt，跳过"
    fi

    log "=== 2/5 go build ==="
    if CGO_ENABLED=0 "$GO" build ./... > /tmp/synctool-build.log 2>&1; then
        log "  ✓ 编译通过"
    else
        log "  ✗ 编译失败：$(tail -5 /tmp/synctool-build.log | tr '\n' ' ' | head -c 300)"
        fail=1
    fi

    log "=== 3/5 go test ==="
    if "$GO" test ./internal/admin/ ./internal/autoinstall/ > /tmp/synctool-test.log 2>&1; then
        log "  ✓ 测试通过"
    else
        log "  ✗ 测试失败：$(tail -5 /tmp/synctool-test.log | tr '\n' ' ' | head -c 300)"
        fail=1
    fi

    log "=== 4/5 汉化审计 ==="
    if python3 ops/i18n/audit.py > /tmp/synctool-i18n.log 2>&1; then
        if grep -q '缺失 0 条' /tmp/synctool-i18n.log; then
            log "  ✓ 汉化无缺失"
        else
            log "  ! 汉化有缺失（不阻塞同步，会另开 i18n-gap 任务）"
            grep '缺失' /tmp/synctool-i18n.log | head -1 | sed 's/^/    /'
        fi
    else
        log "  ! 审计脚本跑不起来，跳过"
    fi

    log "=== 5/5 Fork-Patch 标记 ==="
    local missing=0 sha body
    for sha in $(git rev-list "upstream/$UPSTREAM_BRANCH"..HEAD); do
        body=$(git log -1 --format='%B' "$sha")
        if ! printf '%s' "$body" | grep -qE '^Fork-(Patch|Meta):'; then
            missing=$((missing + 1))
            log "    $(git log -1 --format='%h %s' "$sha")"
        fi
    done
    if [ "$missing" -eq 0 ]; then
        log "  ✓ 标记齐全"
    else
        log "  ✗ $missing 个提交缺标记（先跑 --fix-trailers）"; fail=1
    fi

    if [ "$fail" -eq 0 ]; then log "=== 验证全绿 ==="; return 0; fi
    log "=== 验证未通过 ==="
    return 1
}

# ---------------------------------------------------------------- --publish
do_publish() {
    git rev-parse --verify HEAD >/dev/null 2>&1 || die "没有可推的提交" 1
    local want; want=$(git rev-parse origin/main 2>/dev/null || echo "")
    [ -n "$want" ] || die "取不到 origin/main" 1
    local sha; sha=$(git rev-parse --short HEAD)

    if [ "$DRY_RUN" = "1" ]; then
        log "DRY_RUN：本应把 $sha 推成 main（origin/main=$want）"
        return 0
    fi

    log "推送到 main（force-with-lease，基线 $want）"
    if git push --force-with-lease=main:"$want" origin HEAD:main > /tmp/synctool-push.log 2>&1; then
        log "  ✓ 已推送 $sha"
        log "  落后上游：$(git rev-list --count "HEAD..upstream/$UPSTREAM_BRANCH" 2>/dev/null || echo '?') 个提交"
        return 0
    fi
    tail -5 /tmp/synctool-push.log | while read -r l; do log "    $l"; done
    die "推送失败（改动还在本地 $sha）" 1
}

# ---------------------------------------------------------------- 入口
MODE=${1:-}
case "$MODE" in
    --collect)      do_collect ;;
    --fix-trailers) do_fix_trailers ;;
    --verify)       do_verify ;;
    --publish)      do_publish ;;
    --auto)
        # 一条命令跑全套机械动作（不再问模型）
        do_fix_trailers || exit 1
        do_verify       || exit 1
        do_publish      || exit 1
        ;;
    ""|-h|--help)
        sed -n '1,26p' "$0" | sed 's/^# \{0,1\}//'
        ;;
    *) die "未知参数：$MODE（--collect / --fix-trailers / --verify / --publish）" 2 ;;
esac
