#!/usr/bin/env bash
# 装「DOM 汉化审计」定时器：每天扫一次真实渲染，缺口自动入队 i18n-gap。
# 和 bootimus-i18n-autofix.timer（05:00，静态层）配成一对。
set -euo pipefail

REPO=${REPO:-/root/work/bootimus}
UNITS=/etc/systemd/system

install -m 0644 "$REPO/ops/i18n/systemd/bootimus-i18n-dom-audit.service" "$UNITS/"
install -m 0644 "$REPO/ops/i18n/systemd/bootimus-i18n-dom-audit.timer" "$UNITS/"

systemctl daemon-reload
systemctl enable --now bootimus-i18n-dom-audit.timer >/dev/null
echo "--- timer ---"
systemctl list-timers bootimus-i18n-dom-audit.timer --no-pager | sed 's/^/  /'
