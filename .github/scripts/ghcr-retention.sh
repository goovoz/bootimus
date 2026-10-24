#!/usr/bin/env bash
# [bootimus-fork L0] GHCR 保留策略：每个包只留「latest 指向的那一个镜像」。
#
# 为什么需要它：每次小改动都会构建一个新 digest，tag 是 <base>-g<sha> / sha-<sha>，
# 基线版本（0.1.77）长期不动，于是同版本号下堆几十个 digest。部署只认 latest / 钉住的
# digest，旧构建留着没有价值，只会让 Packages 页面难以辨认。
#
# 保留哪些：
#   * 目标 digest（默认 latest 指向的那个）本身；
#   * 它的多架构子清单（linux/amd64、linux/arm64）——这些也是独立「版本」，删掉会毁掉镜像。
#
# 删除哪些：其余全部（旧构建的 4 个 tag + 它们未打 tag 的子清单）。
#
# 安全阀：
#   * 目标 digest 取不到就整体中止，绝不在「不知道留谁」的情况下删东西；
#   * 15 分钟内更新过的版本跳过（避免和正在进行的构建抢）；
#   * DRY_RUN=true 只打印计划。
set -uo pipefail

OWNER=${OWNER:-goovoz}
PACKAGE=${PACKAGE:-bootimus}
GH_TOKEN=${GH_TOKEN:-}
KEEP_DIGEST=${KEEP_DIGEST:-}
DRY_RUN=${DRY_RUN:-false}
GRACE_MINUTES=${GRACE_MINUTES:-15}

ACC="application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"

log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ -n "$GH_TOKEN" ] || die "缺少 GH_TOKEN"
command -v jq >/dev/null || die "缺少 jq"

# ---------------------------------------------------------------- 1. 目标 digest
if [ -z "$KEEP_DIGEST" ]; then
    TOKEN=$(curl -fsS --max-time 25 \
        "https://ghcr.io/token?scope=repository:${OWNER}/${PACKAGE}:pull&service=ghcr.io" \
        | jq -r '.token // empty') || true
    [ -n "${TOKEN:-}" ] || die "拿不到 registry token，无法解析 latest"
    KEEP_DIGEST=$(curl -fsSI --max-time 25 \
        -H "Authorization: Bearer ${TOKEN}" -H "Accept: ${ACC}" \
        "https://ghcr.io/v2/${OWNER}/${PACKAGE}/manifests/latest" \
        | tr -d '\r' | awk 'tolower($1)=="docker-content-digest:"{print $2; exit}')
    [ -n "${KEEP_DIGEST:-}" ] || die "latest 没有返回 digest，中止（不删任何东西）"
fi
log "保留 digest: ${KEEP_DIGEST}"

# 子清单：多架构 index 的平台清单也是独立版本，必须一起留
KEEP_SET=$(mktemp)
trap 'rm -f "$KEEP_SET"' EXIT
printf '%s\n' "$KEEP_DIGEST" > "$KEEP_SET"
if [ -n "${TOKEN:-}" ]; then
    curl -fsS --max-time 25 -H "Authorization: Bearer ${TOKEN}" -H "Accept: ${ACC}" \
        "https://ghcr.io/v2/${OWNER}/${PACKAGE}/manifests/${KEEP_DIGEST}" \
        | jq -r '.manifests[]?.digest // empty' >> "$KEEP_SET" 2>/dev/null || true
fi
log "连同平台子清单，需要保留的 digest："
sed 's/^/  /' "$KEEP_SET"

# ------------------------------------------------------------- 2. 版本列表 + 删除
# 用户拥有的包用 users/<owner>/...；组织包退回 orgs/<owner>/...。
list_versions() {
    local path=$1
    gh api --paginate "${path}?per_page=100" --jq \
        '.[] | [.id, .name, ((.metadata.container.tags // []) | join(" ")), .updated_at] | @tsv' 2>/dev/null
}

VERSIONS=$(list_versions "users/${OWNER}/packages/container/${PACKAGE}/versions")
if [ -z "$VERSIONS" ]; then
    VERSIONS=$(list_versions "orgs/${OWNER}/packages/container/${PACKAGE}/versions")
fi
if [ -z "$VERSIONS" ]; then
    VERSIONS=$(list_versions "user/packages/container/${PACKAGE}/versions")
fi
[ -n "$VERSIONS" ] || die "取不到版本列表（令牌权限或包名不对），中止"

total=$(printf '%s\n' "$VERSIONS" | sed '/^$/d' | wc -l)
log "包内共 ${total} 个版本"

# GNU date -d 在 ubuntu-latest 上可用
cutoff=$(date -u -d "${GRACE_MINUTES} minutes ago" +%Y-%m-%dT%H:%M:%SZ)

deleted=0
kept=0
skipped_recent=0
failed=0
SUMMARY=$(mktemp)
trap 'rm -f "$KEEP_SET" "$SUMMARY"' EXIT

while IFS=$'\t' read -r id digest tags updated; do
    [ -z "${id:-}" ] && continue
    if grep -qx "$digest" "$KEEP_SET"; then
        kept=$((kept + 1))
        printf '  keep  %-71s %s\n' "$digest" "${tags:-<untagged>}" >> "$SUMMARY"
        continue
    fi
    if [[ "$updated" > "$cutoff" ]]; then
        skipped_recent=$((skipped_recent + 1))
        printf '  fresh %-71s %s（%s 内更新过，跳过）\n' "$digest" "${tags:-<untagged>}" "${GRACE_MINUTES}m" >> "$SUMMARY"
        continue
    fi
    if [ "$DRY_RUN" = "true" ]; then
        printf '  would-delete %s %s\n' "$digest" "${tags:-<untagged>}" >> "$SUMMARY"
        deleted=$((deleted + 1))
        continue
    fi
    if gh api -X DELETE "users/${OWNER}/packages/container/${PACKAGE}/versions/${id}" >/dev/null 2>&1 \
        || gh api -X DELETE "orgs/${OWNER}/packages/container/${PACKAGE}/versions/${id}" >/dev/null 2>&1; then
        printf '  delete %-71s %s\n' "$digest" "${tags:-<untagged>}" >> "$SUMMARY"
        deleted=$((deleted + 1))
    else
        printf '  FAILED %-71s %s\n' "$digest" "${tags:-<untagged>}" >> "$SUMMARY"
        failed=$((failed + 1))
    fi
done <<< "$VERSIONS"

log ""
cat "$SUMMARY"
log ""
log "保留 ${kept} 个，删除 ${deleted} 个，近期更新跳过 ${skipped_recent} 个，失败 ${failed} 个（dry_run=${DRY_RUN}）"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "## GHCR 保留策略"
        echo ""
        echo "- 保留 digest：\`${KEEP_DIGEST}\`（含平台子清单）"
        echo "- 保留 ${kept} 个 / 删除 ${deleted} 个 / 跳过 ${skipped_recent} 个 / 失败 ${failed} 个"
        echo "- dry_run：${DRY_RUN}"
        echo ""
        echo '```'
        cat "$SUMMARY"
        echo '```'
    } >> "$GITHUB_STEP_SUMMARY"
fi

[ "$failed" -eq 0 ] || exit 1
exit 0
