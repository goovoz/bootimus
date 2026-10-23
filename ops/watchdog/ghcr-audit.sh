#!/usr/bin/env bash
# ghcr 标签巡检（只读：不删除、不推送任何镜像）
#
# 目的：把「GitHub 网页显示异常」与「镜像真的被删」区分开。
#   - 每次抓一遍 registry 的 tag → digest，存成快照（去重排序）
#   - 与上次快照比对：标签消失 / 标签拿不到 digest / 线上钉住的 digest 404 → 推送告警
#   - 其余情况只写日志，不打扰
#
# 退出码：0 正常；1 发现回归（已告警）；2 自身执行失败（网络/API 异常，不做判定）
set -uo pipefail
export TZ=${TZ:-Asia/Shanghai}

REPO_SLUG=${REPO_SLUG:-goovoz/bootimus}
COMPOSE=${COMPOSE:-/opt/bootimus/docker-compose.yml}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-watchdog}
LOG=${LOG:-/var/log/ghcr-audit.log}
SC_API=${SC_API:-https://sctapi.ftqq.com}
SNAP="$STATE_DIR/ghcr-tags.txt"
HIST="$STATE_DIR/ghcr-changes.log"

[ -f /etc/default/bootimus-ghcr-audit ] && . /etc/default/bootimus-ghcr-audit
mkdir -p "$STATE_DIR"
touch "$LOG"

log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG" >&2; }

# 单实例：systemd 定时与手动触发撞在一起会抢同一个临时文件，造出假告警
exec 9>"$STATE_DIR/ghcr-audit.lock"
if ! flock -n 9; then
    log "已有巡检实例在跑，本轮跳过"
    exit 0
fi

notify() {
    local title="$1" body="$2"
    [ -n "${SC_SENDKEY:-}" ] || { log "未配置 SC_SENDKEY，跳过推送"; return 0; }
    local resp code
    resp=$(curl -s --max-time 20 -X POST "${SC_API}/${SC_SENDKEY}.send" \
        --data-urlencode "title=${title}" \
        --data-urlencode "desp=${body}" 2>/dev/null)
    code=$(printf '%s' "$resp" | jq -r '.code // empty' 2>/dev/null)
    if [ "$code" = "0" ]; then
        log "已推送通知：${title}"
    else
        log "推送失败：${resp:-<无响应>}"
    fi
}

PIN_REF=$( { [ -f "$COMPOSE" ] && grep -oE 'ghcr\.io/[^ ]*@sha256:[a-f0-9]{64}' "$COMPOSE" | head -1; } || true )
HOST_LABEL="$(hostname)（${PIN_REF:-未取到镜像引用}）"
ACC="application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"

NEW=$(mktemp "$STATE_DIR/ghcr-tags.XXXXXX")
trap 'rm -f "$NEW"' EXIT

# 1. 匿名 token（包是 public，不需要 PAT）
TOKEN=$(curl -s --max-time 25 "https://ghcr.io/token?scope=repository:${REPO_SLUG}:pull&service=ghcr.io" | jq -r '.token // empty' 2>/dev/null)
if [ -z "$TOKEN" ]; then
    log "拿不到匿名 token（网络或 ghcr 异常），本轮不做判定"
    exit 2
fi

# 2. tag 列表
TAGS=$(curl -s --max-time 25 -H "Authorization: Bearer $TOKEN" \
    "https://ghcr.io/v2/${REPO_SLUG}/tags/list" | jq -r '.tags[]?' 2>/dev/null | sort -u)
COUNT=$(printf '%s\n' "$TAGS" | sed '/^$/d' | wc -l)
if [ "${COUNT:-0}" -eq 0 ]; then
    log "tag 列表为空（registry 返回异常或包真被清空）"
    notify "bootimus ghcr 巡检：tag 列表为空" "$(printf '**主机**：%s\n\n**仓库**：`%s`\n\nregistry 返回的 tag 列表为空，可能是 API 异常，也可能是包真的被清空。\n\n**时间**：%s\n' "$HOST_LABEL" "$REPO_SLUG" "$(date '+%F %T %Z')")"
    exit 2
fi

# 3. 逐 tag 取 digest
MISSING=""
while IFS= read -r t; do
    [ -z "$t" ] && continue
    d=$(curl -s -I --max-time 20 -H "Authorization: Bearer $TOKEN" -H "Accept: $ACC" \
        "https://ghcr.io/v2/${REPO_SLUG}/manifests/${t}" \
        | tr -d '\r' | awk 'tolower($1)=="docker-content-digest:"{print $2; exit}')
    if [ -z "$d" ]; then
        MISSING="${MISSING} ${t}"
        d="MISSING"
    fi
    printf '%s %s\n' "$t" "$d" >> "$NEW"
done < <(printf '%s\n' "$TAGS")
sort -u -o "$NEW" "$NEW"

if [ ! -s "$NEW" ]; then
    log "抓取结果为空，本轮不做判定（避免误报）"
    exit 2
fi

# 4. 线上钉住的 digest 是否还在
PIN_DIGEST=""
[ -n "$PIN_REF" ] && PIN_DIGEST="${PIN_REF#*@}"
PIN_CODE="skip"
if [ -n "$PIN_DIGEST" ]; then
    PIN_CODE=$(curl -s -o /dev/null -w '%{http_code}' -I --max-time 20 \
        -H "Authorization: Bearer $TOKEN" -H "Accept: $ACC" \
        "https://ghcr.io/v2/${REPO_SLUG}/manifests/${PIN_DIGEST}")
fi

# 5. 与上次快照比对（集合比较，不看顺序/重复）
REMOVED=""
if [ -f "$SNAP" ]; then
    REMOVED=$(comm -23 <(awk '{print $1}' "$SNAP" | sort -u) <(awk '{print $1}' "$NEW" | sort -u) | tr '\n' ' ')
fi

log "tag 数 ${COUNT}；缺失 digest:${MISSING:- 无}；线上钉住 digest: ${PIN_DIGEST:-未找到} → HTTP ${PIN_CODE}"

# 6. 判定
REGRESS=0
REASON=""
if [ -n "$REMOVED" ]; then
    REGRESS=1
    REASON="${REASON}标签消失：${REMOVED}\n"
fi
if [ -n "$MISSING" ]; then
    REGRESS=1
    REASON="${REASON}标签取不到 digest：${MISSING}\n"
fi
if [ -n "$PIN_DIGEST" ] && [ "$PIN_CODE" != "200" ]; then
    REGRESS=1
    REASON="${REASON}线上钉住的 digest 返回 HTTP ${PIN_CODE}\n"
fi

cp -a "$NEW" "$SNAP"

if [ "$REGRESS" -eq 1 ]; then
    log "发现回归：$REASON"
    printf '%s\t%s\n' "$(date -Is)" "$(printf '%b' "$REASON" | tr '\n' ';')" >> "$HIST"
    notify "bootimus ghcr 巡检：标签有变化" "$(printf '**主机**：%s\n\n**仓库**：`%s`\n\n%b\n**当前 tag 数**：%s\n\n**时间**：%s\n' \
        "$HOST_LABEL" "$REPO_SLUG" "$REASON" "$COUNT" "$(date '+%F %T %Z')")"
    exit 1
fi

log "无变化（tag 数 ${COUNT}，全部可解析）"
exit 0
