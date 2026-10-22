#!/usr/bin/env bash
# bootimus 自动更新 watchdog
#
# 逻辑：
#   1. 拉 ghcr 上的 latest，取它的 digest
#   2. 和 compose 里固定引用的 digest 比：一样就退出（不会重启容器）
#   3. 不一样 → 备份 compose → 改 digest → docker compose up -d → 健康检查
#   4. 健康检查失败 → 自动回滚到备份的 compose 并重启，记下这个 digest 不再重试
#
# 安全阀：
#   SKIP_IF_BUSY=1       装机流量（默认看 8080）上有活跃连接时跳过本轮，别打断正在装机的客户端
#   BUSY_PORTS=8080      判断「忙」的端口，逗号分隔。8081 是管理台，长期挂着的页面不算忙
#   KEEP_IMAGES=3        本地保留最近 3 个镜像，回滚随时可用
#   FORCE_FAIL_HEALTH=1  演练用：强制健康检查失败，验证回滚路径
#   CHECK_ONLY=1         只看有没有新版本，不动容器
#   HEALTH_TIMEOUT=60    等就绪的最长秒数
#
# 退出码：0 无变更/成功；2 回滚；3 拉取失败；4 锁被占用
set -uo pipefail

REPO=${REPO:-ghcr.io/goovoz/bootimus}
DIR=${DIR:-/opt/bootimus}
COMPOSE=${COMPOSE:-$DIR/docker-compose.yml}
HELPER=${HELPER:-$DIR/replace_image.py}
CONTAINER=${CONTAINER:-bootimus}
HEALTH_URL=${HEALTH_URL:-http://172.20.0.205:8081/}
STATE_DIR=${STATE_DIR:-/var/lib/bootimus-watchdog}
LOG=${LOG:-/var/log/bootimus-watchdog.log}
KEEP_IMAGES=${KEEP_IMAGES:-3}
SKIP_IF_BUSY=${SKIP_IF_BUSY:-1}
BUSY_PORTS=${BUSY_PORTS:-8080}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-60}
FORCE_FAIL_HEALTH=${FORCE_FAIL_HEALTH:-0}
CHECK_ONLY=${CHECK_ONLY:-0}

mkdir -p "$STATE_DIR"
touch "$LOG"

log() {
    printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG" >&2
}

# 日志超过 5MB 轮转
if [ "$(stat -c%s "$LOG" 2>/dev/null || echo 0)" -gt 5242880 ]; then
    mv -f "$LOG" "$LOG.1"
fi

# 防止两轮叠在一起
exec 9>"$STATE_DIR/lock"
if ! flock -n 9; then
    log "上一轮还没跑完，跳过"
    exit 4
fi

# 1. registry 上 latest 的 digest
if ! docker pull -q "$REPO:latest" >/dev/null 2>&1; then
    log "拉取 $REPO:latest 失败（网络或 registry 问题），本轮不改动"
    exit 3
fi
NEW=$(docker image inspect "$REPO:latest" \
    --format '{{range .RepoDigests}}{{println .}}{{end}}' 2>/dev/null \
    | grep -m1 "^${REPO}@" | cut -d@ -f2)
if [ -z "${NEW:-}" ]; then
    log "拿不到 latest 的 digest，本轮不改动"
    exit 3
fi

# 2. compose 里当前固定的引用
CUR=$(grep -E '^[[:space:]]+image:' "$COMPOSE" | head -1 \
    | sed -E 's/^[[:space:]]*image:[[:space:]]*//; s/[[:space:]]+$//; s/^"(.*)"$/\1/')
CUR_DIGEST=""
case "$CUR" in
    *@sha256:*) CUR_DIGEST="${CUR#*@}" ;;
esac

if [ "$CUR_DIGEST" = "$NEW" ]; then
    log "已是最新（${NEW:0:19}…），无需更新"
    exit 0
fi

log "发现新构建：当前 ${CUR_DIGEST:-$CUR} → 最新 $NEW"

if [ "$CHECK_ONLY" = 1 ]; then
    log "CHECK_ONLY=1，只报告不更新"
    exit 0
fi

# 3. 之前失败过的 digest 不反复重试
if [ -f "$STATE_DIR/failed-digests" ] && grep -qx "$NEW" "$STATE_DIR/failed-digests"; then
    log "该 digest 之前更新失败过（健康检查未通过），本轮跳过；确认无误后清掉 $STATE_DIR/failed-digests"
    exit 0
fi

# 4. 有人正在装机就等下一轮
#    只认 8080（装机文件服务）；8081 是管理台，长期挂着的看板不算忙
if [ "$SKIP_IF_BUSY" = 1 ]; then
    BUSY=0
    for p in ${BUSY_PORTS//,/ }; do
        c=$(ss -Htn state established sport = :"$p" 2>/dev/null | wc -l)
        BUSY=$((BUSY + c))
    done
    if [ "${BUSY:-0}" -gt 0 ]; then
        log "检测到 ${BUSY} 个活跃连接（端口 ${BUSY_PORTS}，通常是正在装机），本轮跳过，避免打断"
        exit 0
    fi
fi

# 5. 备份 → 改引用 → 重建
cp -a "$COMPOSE" "$STATE_DIR/rollback.yml"
cp -a "$COMPOSE" "$STATE_DIR/compose-$(date +%Y%m%d-%H%M%S).yml"
if ! python3 "$HELPER" --digest "$NEW"; then
    log "写 compose 失败，未做任何改动"
    cp -a "$STATE_DIR/rollback.yml" "$COMPOSE"
    exit 2
fi

log "docker compose up -d"
up_out=$(docker compose -f "$COMPOSE" up -d 2>&1)
up_rc=$?
printf '%s\n' "$up_out" | tail -3 | while read -r l; do log "  $l"; done
if [ "$up_rc" -ne 0 ]; then
    log "compose up 失败，回滚"
    cp -a "$STATE_DIR/rollback.yml" "$COMPOSE"
    docker compose -f "$COMPOSE" up -d >/dev/null 2>&1
    echo "$NEW" >> "$STATE_DIR/failed-digests"
    exit 2
fi

# 6. 健康检查
ok=0
waited=0
while [ "$waited" -lt "$HEALTH_TIMEOUT" ]; do
    sleep 3
    waited=$((waited + 3))
    if [ "$FORCE_FAIL_HEALTH" = 1 ]; then
        log "  [演练] 强制健康检查失败"
        ok=0
        break
    fi
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$HEALTH_URL" 2>/dev/null || echo 000)
    if [ "$code" = "200" ]; then
        ok=1
        break
    fi
    log "  等待就绪 ${waited}s（HTTP $code）"
done

if [ "$ok" -ne 1 ]; then
    log "健康检查失败，自动回滚到上一个构建"
    cp -a "$STATE_DIR/rollback.yml" "$COMPOSE"
    if docker compose -f "$COMPOSE" up -d >/dev/null 2>&1; then
        sleep 8
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$HEALTH_URL" 2>/dev/null || echo 000)
        log "回滚完成，回滚后 HTTP $code"
    else
        log "回滚也失败了，需要人工介入！"
    fi
    echo "$NEW" >> "$STATE_DIR/failed-digests"
    exit 2
fi

# 7. 成功：把版本串写回注释，并解除该 digest 的失败标记
VER=$(docker exec "$CONTAINER" /bootimus version 2>/dev/null | head -1 | tr -d '\r')
if [ -n "${VER:-}" ]; then
    python3 "$HELPER" --digest "$NEW" --version "$VER" >/dev/null 2>&1
fi
if [ -f "$STATE_DIR/failed-digests" ]; then
    sed -i "\|^${NEW}\$|d" "$STATE_DIR/failed-digests"
fi
log "更新成功：$NEW（版本 ${VER:-未知}）"

# 8. 清理旧镜像（保留最近 KEEP_IMAGES 个）
RUNNING=$(docker inspect "$CONTAINER" --format '{{.Image}}' 2>/dev/null | sed 's/^sha256://')
docker images "$REPO" --format '{{.CreatedAt}}	{{.ID}}' | sort -r | cut -f2 \
    | tail -n +$((KEEP_IMAGES + 1)) | while read -r id; do
    case "$RUNNING" in
        "$id"*) continue ;;
    esac
    docker image rm "$id" >/dev/null 2>&1 && log "  已清理旧镜像 ${id:0:12}"
done

log "本轮结束"
exit 0
