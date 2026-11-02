#!/usr/bin/env bash
# 安装 bootimus 上游同步 watchdog（systemd timer）
#
# 用法：
#   bash ops/watchdog/install-sync-watchdog.sh              # 安装并启用
#   bash ops/watchdog/install-sync-watchdog.sh --dry-run    # 只打印要做什么
#   bash ops/watchdog/install-sync-watchdog.sh --uninstall  # 卸载
#
# PAT 来源优先级：$GH_TOKEN > /root/gh_pat.txt > 已有配置文件
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
BIN=/usr/local/sbin/bootimus-sync-watch
UNIT_DIR=/etc/systemd/system
CONF=/etc/default/bootimus-sync-watch
STATE_DIR=/var/lib/bootimus-sync-watch
DRY=0

for a in "$@"; do
    case "$a" in
        --dry-run)   DRY=1 ;;
        --uninstall)
            systemctl disable --now bootimus-sync-watch.timer 2>/dev/null
            rm -f "$UNIT_DIR/bootimus-sync-watch.service" "$UNIT_DIR/bootimus-sync-watch.timer" "$BIN"
            systemctl daemon-reload
            echo "已卸载（配置 $CONF 与状态 $STATE_DIR 保留，需要的话手工删）"
            exit 0 ;;
        *) echo "未知参数：$a"; exit 2 ;;
    esac
done

[ "$(id -u)" = "0" ] || { echo "需要 root"; exit 1; }

# ---- PAT ----
TOKEN="${GH_TOKEN:-}"
if [ -z "$TOKEN" ] && [ -r /root/gh_pat.txt ]; then
    TOKEN=$(tr -d '\n' < /root/gh_pat.txt)
fi
if [ -z "$TOKEN" ] && [ -r "$CONF" ]; then
    TOKEN=$(sed -n 's/^GH_TOKEN=//p' "$CONF" | head -1 | tr -d '"'"'"' ')
fi
[ -n "$TOKEN" ] || { echo "找不到 GitHub PAT：设 GH_TOKEN 环境变量，或放到 /root/gh_pat.txt"; exit 1; }

# ---- 交班对象（DSH）----
# 2026-11 起不再调用 AI 网关：需要判断的事交给本机的 DSH（见 ops/dsh/）。
# 这里只需要把队列客户端和队列目录定下来。
DSH_HANDOFF=${DSH_HANDOFF:-$HERE/../dsh/dsh-handoff.sh}
DSH_HANDOFF_HOME=${DSH_HANDOFF_HOME:-/var/lib/bootimus-dsh}
DSH_HANDOFF=$(cd "$(dirname "$DSH_HANDOFF")" && pwd)/$(basename "$DSH_HANDOFF")

echo "=== 安装计划 ==="
echo "  脚本   : $HERE/sync-watchdog.sh  ->  $BIN"
echo "  工具箱 : $HERE/auto-resolve-sync.sh  ->  $(dirname "$BIN")/bootimus-sync-tool"
echo "  队列   : $HERE/../dsh/dsh-handoff.sh  ->  $(dirname "$BIN")/bootimus-dsh-handoff"
echo "  unit   : bootimus-sync-watch.{service,timer}  ->  $UNIT_DIR"
echo "  配置   : $CONF（含 GH_TOKEN，权限 600）"
echo "  状态   : $STATE_DIR/state.json"
echo "  队列目录: $DSH_HANDOFF_HOME（tasks/ + evidence/）"
echo "  节奏   : 每 30 分钟（OnBootSec=5min）"
echo "  说明   : 不调用任何模型；需要判断的任务入队等 DSH 领走"

if [ "$DRY" = "1" ]; then
    echo
    echo "（--dry-run，未做任何改动）"
    exit 0
fi

install -m 0755 "$HERE/sync-watchdog.sh" "$BIN"


# auto-resolve 现在只是「工具箱」（--collect / --verify / --publish），不再自己
# 问模型；名字也跟着改，免得以后有人以为它还会自动解决冲突。
install -m 0755 "$HERE/auto-resolve-sync.sh" "$(dirname "$BIN")/bootimus-sync-tool"
install -m 0755 "$HERE/../dsh/dsh-handoff.sh" "$(dirname "$BIN")/bootimus-dsh-handoff"
# 老的 /usr/local/sbin/bootimus-auto-resolve 里还留着调 AI 网关的代码，已经没人调它了，
# 留着只会让人以为「自动解冲突」还在 —— 直接删掉。
if [ -f "$(dirname "$BIN")/bootimus-auto-resolve" ]; then
    rm -f "$(dirname "$BIN")/bootimus-auto-resolve"
    echo "已删除过时的 /usr/local/sbin/bootimus-auto-resolve（原 AI 解冲突器）"
fi
chmod 0755 "$DSH_HANDOFF" 2>/dev/null || true
install -m 0644 "$HERE/bootimus-sync-watch.service" "$UNIT_DIR/"
install -m 0644 "$HERE/bootimus-sync-watch.timer"   "$UNIT_DIR/"
mkdir -p "$STATE_DIR" "$DSH_HANDOFF_HOME/tasks" "$DSH_HANDOFF_HOME/evidence"

if [ ! -f "$CONF" ]; then
    cat > "$CONF" <<EOF
# bootimus 上游同步 watchdog 配置（改完下一轮自动生效，不用 restart）
# 这个文件里有密钥，权限必须是 600

GH_TOKEN=$TOKEN

# 仓库与上游
REPO=goovoz/bootimus
UPSTREAM=garybowers/bootimus

# 多久没成功算「停滞」（小时）
STALE_HOURS=36

# 瞬时故障每天最多自动重试几次
MAX_RETRY_PER_DAY=2

# Server酱 SendKey（SCT 开头）。设了才会推送，不设只写日志。
# SC_SENDKEY=SCTxxxxxxxxxxxxxxxxxxxxxxxxxx

# 交给谁处理：DSH 的交接队列（见 ops/dsh/）。本脚本不调用任何模型。
# 队列客户端由 DSH 通过 ssh 调用（claim / report），两边共用这一份。
DSH_HANDOFF=$DSH_HANDOFF
DSH_HANDOFF_HOME=$DSH_HANDOFF_HOME
EOF
    chmod 600 "$CONF"
    echo "已生成 $CONF"
else
    # 已存在就只补缺的项
    if ! grep -q '^GH_TOKEN=.\+' "$CONF"; then
        sed -i "s|^GH_TOKEN=.*|GH_TOKEN=$TOKEN|" "$CONF"
        echo "已补写 $CONF 里的 GH_TOKEN"
    else
        echo "$CONF 已有 GH_TOKEN，保持不动"
    fi
    if grep -q '^AI_' "$CONF"; then
        # 老版本留下的网关参数，现在没人读了；改成注释，留个痕方便回溯
        sed -i 's|^AI_|# [2026-11 已停用，改由 DSH 接管] AI_|' "$CONF"
        echo "已把 $CONF 里的 AI_* 网关参数注释掉（不再调用网关）"
    fi
    if ! grep -q '^DSH_HANDOFF=.\+' "$CONF"; then
        {
            echo ""
            echo "# 交给谁处理：DSH 的交接队列（见 ops/dsh/）"
            echo "DSH_HANDOFF=$DSH_HANDOFF"
            echo "DSH_HANDOFF_HOME=$DSH_HANDOFF_HOME"
        } >> "$CONF"
        echo "已补写 DSH 交接参数"
    fi
    chmod 600 "$CONF"
fi

systemctl daemon-reload
systemctl enable --now bootimus-sync-watch.timer

echo
echo "=== 装好了，立即跑一次看结果 ==="
systemctl start bootimus-sync-watch.service
sleep 3
tail -5 /var/log/bootimus-sync-watch.log 2>/dev/null | sed 's/^/  /'
echo
echo "  状态文件: $STATE_DIR/state.json"
echo "  看下次触发: systemctl list-timers bootimus-sync-watch.timer --no-pager"
