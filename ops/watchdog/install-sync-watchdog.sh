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

# ---- 可选的 AI 网关参数（不配就只走规则归因）----
if [ -z "${AI_BASE_URL:-}" ] && [ -r /root/ai_gateway.env ]; then
    AI_BASE_URL=$(sed -n 's/^AI_BASE_URL=//p' /root/ai_gateway.env | head -1 | tr -d '"'"'"' ')
    AI_API_KEY=$(sed -n 's/^AI_API_KEY=//p'  /root/ai_gateway.env | head -1 | tr -d '"'"'"' ')
fi
AI_MODEL=${AI_MODEL:-cn:deepseek-v4.1-flash}

echo "=== 安装计划 ==="
echo "  脚本   : $HERE/sync-watchdog.sh  ->  $BIN"
echo "  unit   : bootimus-sync-watch.{service,timer}  ->  $UNIT_DIR"
echo "  配置   : $CONF（含 GH_TOKEN，权限 600）"
echo "  状态   : $STATE_DIR/state.json"
echo "  节奏   : 每 30 分钟（OnBootSec=5min）"

if [ "$DRY" = "1" ]; then
    echo
    echo "（--dry-run，未做任何改动）"
    exit 0
fi

install -m 0755 "$HERE/sync-watchdog.sh" "$BIN"
install -m 0644 "$HERE/bootimus-sync-watch.service" "$UNIT_DIR/"
install -m 0644 "$HERE/bootimus-sync-watch.timer"   "$UNIT_DIR/"
mkdir -p "$STATE_DIR"

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

# 可选：用 LLM 做失败归因（只生成文案，不参与动作决策）。
# 不填 AI_BASE_URL 就退回纯规则归因，功能不受影响。
# 模型别用 cn:auto —— 会路由到任意模型；思考强度用 AI_EFFORT，
# deepseek-v4.1-flash 在 CN 面支持 low/high/max，max 实测思考量是 high 的 1.9 倍。
AI_BASE_URL=${AI_BASE_URL:-}
AI_MODEL=${AI_MODEL:-cn:deepseek-v4.1-flash}
AI_EFFORT=${AI_EFFORT:-max}
AI_API_KEY=${AI_API_KEY:-}
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
    if [ -n "${AI_BASE_URL:-}" ] && ! grep -q '^AI_BASE_URL=.\+' "$CONF"; then
        {
            echo ""
            echo "AI_BASE_URL=$AI_BASE_URL"
            echo "AI_MODEL=$AI_MODEL"
            echo "AI_API_KEY=$AI_API_KEY"
        } >> "$CONF"
        echo "已补写 AI 网关参数"
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
