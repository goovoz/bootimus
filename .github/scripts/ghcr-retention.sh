#!/usr/bin/env bash
# [bootimus-fork L0] GHCR 保留策略：**每个版本号只留最新一个 digest**。
#
#   0.1.77 → 保留该版本号下最新构建的 index（+ 它的两个平台子清单）
#   0.1.76 → 同样保留它自己最新那个，依此类推
#   latest → 固定指向「版本号最高的那个 keeper」
#
# 为什么按版本号而不是「全库只留一个」：跨版本回滚时至少还留着一个验证过的旧版本可以临时回滚；
# 同版本号内的中间构建（每次小改动一个 digest）没有保留价值。
#
# 保留/删除的边界：
#   * 每个版本号 → updated_at 最新的那个 index；
#   * 该 index 的**平台子清单**（linux/amd64、linux/arm64）也必须留——它们在 API 里是
#     独立「版本」，删掉 index 就坏了（Packages 页面显示的 untagged 就是它们）；
#   * 其余全部删除（同版本号的旧构建、旧构建的子清单）。
#
# 安全阀：
#   * 一个 keeper 都解析不出来就中止，绝不在「不知道留谁」的情况下删东西；
#   * 15 分钟内更新过的版本跳过（避免和正在进行的构建抢）；
#   * DRY_RUN=true 只打印计划；
#   * VERSIONS_FROM=<file> 用本地清单只跑分组逻辑（无网络，用于自测）。
set -uo pipefail

OWNER=${OWNER:-goovoz}
PACKAGE=${PACKAGE:-bootimus}
IMAGE=${IMAGE:-ghcr.io/${OWNER}/${PACKAGE}}
GH_TOKEN=${GH_TOKEN:-}
KEEP_DIGEST=${KEEP_DIGEST:-}
DRY_RUN=${DRY_RUN:-false}
GRACE_MINUTES=${GRACE_MINUTES:-15}
FIX_LATEST=${FIX_LATEST:-true}
VERSIONS_FROM=${VERSIONS_FROM:-}

ACC="application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"

log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v jq >/dev/null || die "缺少 jq"

# ------------------------------------------------------------------ 版本号提取
# tag 形态：0.1.77（漂移）/ 0.1.77-ga2327e1（本次构建）/ sha-a2327e1 / latest
# 只认前两种；平台子清单没有 tag，自然落空（它们靠 keeper 的子清单关系保留）
base_version() {
    local tags=$1 t
    for t in $tags; do
        if [[ "$t" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then printf '%s' "$t"; return 0; fi
    done
    for t in $tags; do
        if [[ "$t" =~ ^([0-9]+\.[0-9]+\.[0-9]+)-g ]]; then printf '%s' "${BASH_REMATCH[1]}"; return 0; fi
    done
    return 0
}

# --------------------------------------------------------------- 1. 版本清单
fetch_versions() {
    if [ -n "$VERSIONS_FROM" ]; then
        cat "$VERSIONS_FROM"
        return 0
    fi
    local out
    out=$(gh api --paginate "users/${OWNER}/packages/container/${PACKAGE}/versions?per_page=100" --jq \
        '.[] | [.id, .name, ((.metadata.container.tags // []) | join(" ") | if . == "" then "-" else . end), .updated_at] | @tsv' 2>/dev/null)
    if [ -z "$out" ]; then
        out=$(gh api --paginate "orgs/${OWNER}/packages/container/${PACKAGE}/versions?per_page=100" --jq \
            '.[] | [.id, .name, ((.metadata.container.tags // []) | join(" ") | if . == "" then "-" else . end), .updated_at] | @tsv' 2>/dev/null)
    fi
    printf '%s' "$out"
}

VERSIONS=$(fetch_versions)
[ -n "$VERSIONS" ] || die "取不到版本列表（令牌权限或包名不对），中止"
total=$(printf '%s\n' "$VERSIONS" | sed '/^$/d' | wc -l)
log "包内共 ${total} 个版本"

# --------------------------------------------------- 2. 每个版本号挑最新那个
declare -A BEST_DIGEST=()
declare -A BEST_TIME=()
declare -A BEST_TAGS=()

while IFS=$'\t' read -r id digest tags updated; do
    [ -z "${id:-}" ] && continue
    base=$(base_version "$tags")
    [ -n "$base" ] || continue
    prev=${BEST_TIME[$base]:-}
    if [ -z "$prev" ] || [[ "$updated" > "$prev" ]]; then
        BEST_TIME[$base]=$updated
        BEST_DIGEST[$base]=$digest
        BEST_TAGS[$base]=$tags
    fi
done <<< "$VERSIONS"

[ "${#BEST_DIGEST[@]}" -gt 0 ] || die "一个版本号都没解析出来，中止（不删任何东西）"

log ""
log "每个版本号保留最新构建："
for base in $(printf '%s\n' "${!BEST_DIGEST[@]}" | sort -V); do
    log "  ${base}  →  ${BEST_DIGEST[$base]}  [${BEST_TAGS[$base]}]  (${BEST_TIME[$base]})"
done

NEWEST_BASE=$(printf '%s\n' "${!BEST_DIGEST[@]}" | sort -V | tail -1)
NEWEST_DIGEST=${BEST_DIGEST[$NEWEST_BASE]}
log ""
log "最新版本号：${NEWEST_BASE} → latest 应指向 ${NEWEST_DIGEST}"

# ------------------------------------------------- 3. keeper 集合（含平台子清单）
KEEP_SET=$(mktemp)
SUMMARY=$(mktemp)
trap 'rm -f "$KEEP_SET" "$SUMMARY"' EXIT

TOKEN=""
if [ -z "$VERSIONS_FROM" ]; then
    TOKEN=$(curl -fsS --max-time 25 \
        "https://ghcr.io/token?scope=repository:${OWNER}/${PACKAGE}:pull&service=ghcr.io" \
        | jq -r '.token // empty') || true
fi

for base in "${!BEST_DIGEST[@]}"; do
    printf '%s\n' "${BEST_DIGEST[$base]}" >> "$KEEP_SET"
    if [ -n "$TOKEN" ]; then
        curl -fsS --max-time 25 -H "Authorization: Bearer ${TOKEN}" -H "Accept: ${ACC}" \
            "https://ghcr.io/v2/${OWNER}/${PACKAGE}/manifests/${BEST_DIGEST[$base]}" \
            | jq -r '.manifests[]?.digest // empty' >> "$KEEP_SET" 2>/dev/null || true
    fi
done
[ -n "$KEEP_DIGEST" ] && printf '%s\n' "$KEEP_DIGEST" >> "$KEEP_SET"
sort -u -o "$KEEP_SET" "$KEEP_SET"
log "需要保留的 digest（含平台子清单）共 $(wc -l < "$KEEP_SET") 个"

# ------------------------------------------------------------ 4. latest 校准
if [ -n "$TOKEN" ]; then
    LATEST_DIGEST=$(curl -fsSI --max-time 25 -H "Authorization: Bearer ${TOKEN}" -H "Accept: ${ACC}" \
        "https://ghcr.io/v2/${OWNER}/${PACKAGE}/manifests/latest" \
        | tr -d '\r' | awk 'tolower($1)=="docker-content-digest:"{print $2; exit}')
    log "latest 当前指向：${LATEST_DIGEST:-<取不到>}"
    if [ -z "${LATEST_DIGEST:-}" ]; then
        log "WARN: 取不到 latest 的 digest，本轮不校准（不影响下面的清理判定）"
    elif [ "$LATEST_DIGEST" != "$NEWEST_DIGEST" ]; then
        log "latest 不等于「最高版本号的最新构建」，需指到 ${NEWEST_DIGEST}"
        if [ "$DRY_RUN" = "true" ] || [ "$FIX_LATEST" != "true" ]; then
            log "  （dry_run 或 FIX_LATEST=false，跳过重打 tag）"
        elif docker buildx imagetools create -t "${IMAGE}:latest" "${IMAGE}@${NEWEST_DIGEST}" >/dev/null 2>&1; then
            log "  已把 latest 指到 ${NEWEST_DIGEST}"
        else
            log "  WARN: 重打 latest 失败（确认已 docker login ghcr.io），本轮跳过"
        fi
    fi
fi

# --------------------------------------------------------------- 5. 删除其余
cutoff=$(date -u -d "${GRACE_MINUTES} minutes ago" +%Y-%m-%dT%H:%M:%SZ)

deleted=0
kept=0
kept_platform=0
skipped_recent=0
failed=0

while IFS=$'\t' read -r id digest tags updated; do
    [ -z "${id:-}" ] && continue
    base=$(base_version "$tags")
    label=${base:-untagged}
    if grep -qx "$digest" "$KEEP_SET"; then
        kept=$((kept + 1))
        note=""
        if [ -z "$base" ]; then
            # 没有 tag 的版本就是 index 的平台清单（amd64 / arm64），删了镜像就拉不下来
            kept_platform=$((kept_platform + 1))
            note="（平台清单，正常）"
        fi
        printf '  keep    %-8s %-71s %s%s\n' "$label" "$digest" "${tags}" "$note" >> "$SUMMARY"
        continue
    fi
    if [[ "$updated" > "$cutoff" ]]; then
        skipped_recent=$((skipped_recent + 1))
        printf '  fresh   %-8s %-71s %s（%sm 内更新，跳过）\n' "$label" "$digest" "${tags}" "$GRACE_MINUTES" >> "$SUMMARY"
        continue
    fi
    if [ "$DRY_RUN" = "true" ]; then
        printf '  would-rm %-8s %-71s %s\n' "$label" "$digest" "${tags}" >> "$SUMMARY"
        deleted=$((deleted + 1))
        continue
    fi
    if gh api -X DELETE "users/${OWNER}/packages/container/${PACKAGE}/versions/${id}" >/dev/null 2>&1 \
        || gh api -X DELETE "orgs/${OWNER}/packages/container/${PACKAGE}/versions/${id}" >/dev/null 2>&1; then
        printf '  delete  %-8s %-71s %s\n' "$label" "$digest" "${tags}" >> "$SUMMARY"
        deleted=$((deleted + 1))
    else
        printf '  FAILED  %-8s %-71s %s\n' "$label" "$digest" "${tags}" >> "$SUMMARY"
        failed=$((failed + 1))
    fi
done <<< "$VERSIONS"

log ""
cat "$SUMMARY"
log ""
log "保留 ${kept} 个（${#BEST_DIGEST[@]} 个版本号 + ${kept_platform} 个平台清单），删除 ${deleted} 个，近期跳过 ${skipped_recent} 个，失败 ${failed} 个（dry_run=${DRY_RUN}）"
log "说明：Packages 页面上的 untagged 就是这些平台清单（每个版本号 2 个：amd64 / arm64），它们是镜像本体，删掉 docker pull 会失败。"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "## GHCR 保留策略（每个版本号留最新一个）"
        echo ""
        echo "| 版本号 | 保留 digest | 构建 tag |"
        echo "|---|---|---|"
        for base in $(printf '%s\n' "${!BEST_DIGEST[@]}" | sort -V); do
            echo "| ${base} | \`${BEST_DIGEST[$base]}\` | ${BEST_TAGS[$base]} |"
        done
        echo ""
        echo "保留 ${kept} 个（含 ${kept_platform} 个平台清单）/ 删除 ${deleted} 个 / 跳过 ${skipped_recent} 个 / 失败 ${failed} 个（dry_run=${DRY_RUN}）"
        echo ""
        echo "> Packages 页面显示的 **untagged** 就是这些平台清单：多架构镜像由一个 index 指向"
        echo "> amd64 / arm64 两份 manifest，buildx 只给 index 打 tag，所以子清单永远显示 untagged。"
        echo "> 每个保留的版本号固定 2 个，删掉会导致 \`docker pull\` 失败。"
        echo ""
        echo '```'
        cat "$SUMMARY"
        echo '```'
    } >> "$GITHUB_STEP_SUMMARY"
fi

[ "$failed" -eq 0 ] || exit 1
exit 0
