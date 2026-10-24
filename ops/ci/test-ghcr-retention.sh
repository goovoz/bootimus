#!/bin/bash
# 保留策略的分组逻辑自测（不联网、不改任何东西）。
#   bash ops/ci/test-ghcr-retention.sh
# 用 VERSIONS_FROM 把合成清单喂给 ghcr-retention.sh，断言「每个版本号只留最新一个」。
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/../../.github/scripts/ghcr-retention.sh"
FIX_VERSION="$HERE/retention-fixture-version.txt"
FIX_ORDER="$HERE/retention-fixture-order.txt"
fail=0

check() { # check <描述> <期望出现的字符串> <输出>
    local desc=$1 want=$2 out=$3
    if printf '%s' "$out" | grep -qF -- "$want"; then
        echo "PASS $desc"
    else
        echo "FAIL $desc （缺: $want）"
        fail=$((fail + 1))
    fi
}

echo "== 用例 1：两个版本号，各留最新 =="
OUT=$(VERSIONS_FROM="$FIX_VERSION" DRY_RUN=true bash "$S" 2>&1)
check "0.1.77 保留最新构建" "0.1.77  →  sha256:77new" "$OUT"
check "0.1.76 保留最新构建" "0.1.76  →  sha256:76new" "$OUT"
check "latest 应指向 0.1.77 的最新构建" "最新版本号：0.1.77 → latest 应指向 sha256:77new" "$OUT"
check "同版本旧构建被删" "would-rm 0.1.77   sha256:77old" "$OUT"
check "旧版本号旧构建被删" "would-rm 0.1.76   sha256:76old" "$OUT"
check "未打 tag 的子清单也进删除计划" "would-rm untagged sha256:76old-amd64" "$OUT"
check "保留 2 个版本号" "保留 2 个（2 个版本号）" "$OUT"
check "删除 9 个" "删除 9 个" "$OUT"

echo
echo "== 用例 2：0.1.9 vs 0.1.10 必须按数字序 =="
OUT=$(VERSIONS_FROM="$FIX_ORDER" DRY_RUN=true bash "$S" 2>&1)
check "最新版本号是 0.1.10" "最新版本号：0.1.10" "$OUT"
check "0.1.9 也保留自己最新的" "0.1.9  →  sha256:v9" "$OUT"

echo
echo "== 用例 3：清单里没有版本号时必须中止 =="
TMP=$(mktemp)
printf '301\tsha256:only-untagged\t-\t2026-10-24T03:00:00Z\n' > "$TMP"
OUT=$(VERSIONS_FROM="$TMP" DRY_RUN=true bash "$S" 2>&1)
rm -f "$TMP"
if printf '%s' "$OUT" | grep -q "一个版本号都没解析出来"; then
    echo "PASS 空清单中止"
else
    echo "FAIL 空清单没有中止"
    fail=$((fail + 1))
fi

echo
if [ "$fail" -eq 0 ]; then echo "ALL PASS"; exit 0; fi
echo "$fail FAILED"
exit 1
