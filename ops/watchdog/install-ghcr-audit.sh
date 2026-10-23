#!/usr/bin/env bash
# 在部署主机上安装 ghcr 巡检（只读，不碰镜像、不改 compose）
set -euo pipefail

SRC_DIR=${SRC_DIR:-/tmp/wd}
DEST=/opt/bootimus
UNITS=/etc/systemd/system
ENVF=/etc/default/bootimus-ghcr-audit

install -m 0755 "$SRC_DIR/ghcr-audit.sh" "$DEST/ghcr-audit.sh"
install -m 0644 "$SRC_DIR/bootimus-ghcr-audit.service" "$UNITS/"
install -m 0644 "$SRC_DIR/bootimus-ghcr-audit.timer" "$UNITS/"

# 复用 watchdog 的 Server酱 sendkey（不重复保存明文，也不进仓库）
if [ ! -f "$ENVF" ]; then
    KEY=$( [ -f /etc/default/bootimus-watchdog ] && grep -oE 'SC_SENDKEY=.*' /etc/default/bootimus-watchdog | head -1 | cut -d= -f2- || true )
    if [ -n "${KEY:-}" ]; then
        umask 077
        printf 'SC_SENDKEY=%s\n' "$KEY" > "$ENVF"
        chmod 600 "$ENVF"
        echo "已生成 $ENVF（复用 watchdog 的 sendkey）"
    else
        echo "未在 /etc/default/bootimus-watchdog 里找到 sendkey，巡检不会推送（仅写日志）"
    fi
fi

systemctl daemon-reload
systemctl enable --now bootimus-ghcr-audit.timer >/dev/null
echo "--- timer ---"
systemctl list-timers bootimus-ghcr-audit.timer --no-pager | sed 's/^/  /'
