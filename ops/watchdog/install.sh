#!/bin/bash
# 在本机安装/升级 bootimus watchdog（需要在有 docker 权限的机器上以 root 运行）
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
BIN_DIR=/opt/bootimus
UNIT_DIR=/etc/systemd/system
ENV_FILE=/etc/default/bootimus-watchdog

[ "$(id -u)" -eq 0 ] || { echo "请用 root 运行"; exit 1; }

echo "=== 1. 安装文件 ==="
install -d -m 755 "$BIN_DIR"
install -m 755 "$HERE/watchdog.sh"        "$BIN_DIR/watchdog.sh"
install -m 644 "$HERE/replace_image.py"   "$BIN_DIR/replace_image.py"
install -m 644 "$HERE/bootimus-watchdog.service" "$UNIT_DIR/bootimus-watchdog.service"
install -m 644 "$HERE/bootimus-watchdog.timer"   "$UNIT_DIR/bootimus-watchdog.timer"
if [ -f "$ENV_FILE" ]; then
    echo "  已存在 $ENV_FILE，保留不动"
else
    install -m 644 "$HERE/default.env" "$ENV_FILE"
    echo "  写入默认参数 $ENV_FILE"
fi

echo
echo "=== 2. 语法检查 ==="
bash -n "$BIN_DIR/watchdog.sh" && echo "  bash -n: OK"
python3 -c "import ast;ast.parse(open('$BIN_DIR/replace_image.py').read());print('  python: OK')"

echo
echo "=== 3. 启用 timer ==="
systemctl daemon-reload
systemctl enable --now bootimus-watchdog.timer
systemctl list-timers bootimus-watchdog.timer --no-pager

echo
echo "=== 4. 首跑 ==="
"$BIN_DIR/watchdog.sh" || echo "  退出码 $?（首次运行失败请翻 /var/log/bootimus-watchdog.log）"
