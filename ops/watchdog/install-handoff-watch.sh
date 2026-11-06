#!/usr/bin/env bash
# 安装两块新的巡检：上游哨兵（A）+ 冲突哨兵（B）
#
#   bash ops/watchdog/install-handoff-watch.sh             # 安装并启用
#   bash ops/watchdog/install-handoff-watch.sh --dry-run   # 只打印计划
#   bash ops/watchdog/install-handoff-watch.sh --uninstall # 卸载
#
# 为什么要这两块：GitHub 的 schedule 会迟到甚至丢（2026-11-05 实测迟 5 小时），而
# 「PR 挂着没人管」watchdog 看不见（它只认 CI run 失败）。两个哨兵都只读 + 入队，
# 不改仓库、不 dispatch（除了 A 催同步）、不调用任何模型。
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
BIN_DIR=/usr/local/sbin
UNIT_DIR=/etc/systemd/system
STATE_LIB=/var/lib
DRY=0

for a in "$@"; do
    case "$a" in
        --dry-run) DRY=1 ;;
        --uninstall)
            systemctl disable --now bootimus-upstream-watch.timer bootimus-conflict-watch.timer 2>/dev/null
            rm -f "$UNIT_DIR/bootimus-upstream-watch.service" "$UNIT_DIR/bootimus-upstream-watch.timer" \
                  "$UNIT_DIR/bootimus-conflict-watch.service" "$UNIT_DIR/bootimus-conflict-watch.timer" \
                  "$BIN_DIR/bootimus-watch-upstream" "$BIN_DIR/bootimus-conflict-watch"
            systemctl daemon-reload
            echo "已卸载（/etc/default/bootimus-*-watch 与 /var/lib/bootimus-*-watch 保留，需要的话手工删）"
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
if [ -z "$TOKEN" ] && [ -r /etc/default/bootimus-sync-watch ]; then
    TOKEN=$(sed -n 's/^GH_TOKEN=//p' /etc/default/bootimus-sync-watch | head -1 | tr -d '"'"'"' \r')
fi
if [ -z "$TOKEN" ] && [ -r /root/.github_pat ]; then
    TOKEN=$(tr -d '\r\n' < /root/.github_pat)
fi
[ -n "$TOKEN" ] || { echo "找不到 GitHub PAT：设 GH_TOKEN，或放 /root/gh_pat.txt"; exit 1; }

echo "=== 安装计划 ==="
echo "  A 上游哨兵 : $HERE/watch-upstream.sh        -> $BIN_DIR/bootimus-watch-upstream   （每 5 分钟）"
echo "  B 冲突哨兵 : $HERE/sync-conflict-watch.sh    -> $BIN_DIR/bootimus-conflict-watch   （每 15 分钟）"
echo "  unit       : bootimus-upstream-watch.{service,timer}、bootimus-conflict-watch.{service,timer}"
echo "  配置       : /etc/default/bootimus-upstream-watch、/etc/default/bootimus-conflict-watch（含 GH_TOKEN，600）"
echo "  状态       : $STATE_LIB/bootimus-upstream-watch/、$STATE_LIB/bootimus-conflict-watch/"
echo "  队列       : /var/lib/bootimus-dsh（B 只入队，不修东西）"

if [ "$DRY" = "1" ]; then echo; echo "（--dry-run，未做任何改动）"; exit 0; fi

install -m 0755 "$HERE/watch-upstream.sh"     "$BIN_DIR/bootimus-watch-upstream"
install -m 0755 "$HERE/sync-conflict-watch.sh" "$BIN_DIR/bootimus-conflict-watch"

for pair in "upstream:/etc/default/bootimus-upstream-watch" "conflict:/etc/default/bootimus-conflict-watch"; do
    name="${pair%%:*}"
    conf="${pair#*:}"
    if [ -r "$conf" ] && grep -q '^GH_TOKEN=.' "$conf"; then
        echo "保留已有配置：$conf"
    else
        umask 077
        printf '# bootimus %s 哨兵（由 install-handoff-watch.sh 生成）\nGH_TOKEN=%s\n' "$name" "$TOKEN" > "$conf"
        chmod 600 "$conf"
        echo "已写配置：$conf"
    fi
done

mkdir -p "$STATE_LIB/bootimus-upstream-watch" "$STATE_LIB/bootimus-conflict-watch"

install -m 0644 "$HERE/bootimus-upstream-watch.service" "$UNIT_DIR/"
install -m 0644 "$HERE/bootimus-upstream-watch.timer"   "$UNIT_DIR/"
install -m 0644 "$HERE/bootimus-conflict-watch.service" "$UNIT_DIR/"
install -m 0644 "$HERE/bootimus-conflict-watch.timer"   "$UNIT_DIR/"
systemctl daemon-reload
systemctl enable --now bootimus-upstream-watch.timer bootimus-conflict-watch.timer

echo
echo "已安装。验证："
echo "  systemctl list-timers 'bootimus-*-watch.timer'"
echo "  $BIN_DIR/bootimus-watch-upstream --dry-run"
echo "  $BIN_DIR/bootimus-conflict-watch --dry-run"
