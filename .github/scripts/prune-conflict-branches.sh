#!/usr/bin/env bash
# [bootimus-fork L0] 清理过期的 sync-conflict/* 分支。
#
# 冲突分支只是「给人工看现场」的临时载体：解决完之后它既没并入 main、也不该长期占着。
# 这里按两把锁清理：
#   1. 最后提交超过 KEEP_DAYS 天（默认 7）
#   2. 没有开着的 PR 指向它（开着说明还在人工处理，不能删）
# 只碰 sync-conflict/*，绝不动 fork-archive/*（那是退役补丁的归档，永久保留）。
#
# 环境变量：GH_TOKEN / GITHUB_TOKEN（需要 contents:write + pull-requests:read）
#          KEEP_DAYS（默认 7）
set -uo pipefail

KEEP_DAYS=${KEEP_DAYS:-7}
PATTERN=${PATTERN:-sync-conflict/*}
TOKEN=${GH_TOKEN:-${GITHUB_TOKEN:-}}
REPO=${GITHUB_REPOSITORY:-goovoz/bootimus}

[ -n "$TOKEN" ] || { echo "::error::缺少 GH_TOKEN"; exit 1; }

echo "=== 过期冲突分支清理（保留 ${KEEP_DAYS} 天，无 open PR 才删）==="

git fetch -q --prune origin || true
BRANCHES=$(git ls-remote --heads origin "$PATTERN" | awk '{print $2}' | sed 's|refs/heads/||')
if [ -z "$BRANCHES" ]; then
    echo "  没有 $PATTERN 分支"
    exit 0
fi

now=$(date +%s)
deleted=0
for br in $BRANCHES; do
    git fetch -q origin "$br:refs/remotes/origin/$br" 2>/dev/null || true
    ts=$(git log -1 --format=%ct "origin/$br" 2>/dev/null || echo "$now")
    age=$(( (now - ts) / 86400 ))

    if [ "$age" -lt "$KEEP_DAYS" ]; then
        echo "  保留 $br（${age} 天前，未到 ${KEEP_DAYS} 天）"
        continue
    fi

    # 有开着 PR 说明还在人工处理
    open_pr=$(curl -sS -H "Authorization: Bearer $TOKEN" -H 'Accept: application/vnd.github+json' \
        "https://api.github.com/repos/${REPO}/pulls?state=open&head=${REPO%%/*}:${br}&per_page=1" \
        | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo "1")
    if [ "${open_pr:-1}" != "0" ]; then
        echo "  保留 $br（有开着的 PR）"
        continue
    fi

    if git push -q origin --delete "$br" 2>/dev/null; then
        echo "  已删除 $br（${age} 天前）"
        deleted=$((deleted + 1))
    else
        echo "  ::warning::删除 $br 失败"
    fi
done

echo "  本轮删除 ${deleted} 个分支"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "### 冲突分支清理"
        echo ""
        echo "- 保留天数：${KEEP_DAYS}"
        echo "- 本轮删除：${deleted} 个"
    } >> "$GITHUB_STEP_SUMMARY"
fi
