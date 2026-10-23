#!/usr/bin/env bash
# 从上游 pristine 的 i18n.js 重新生成补丁文件（唯一入口，别直接跑 build_i18n_patch.py）
#
#   bash ops/i18n/rebuild.sh [上游 ref]
#
# 步骤：取上游 i18n.js → 灌入 parts/*.json 的映射 → 追加引擎能力 → 语法检查
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
REF=${1:-cb5efed}

cd "$REPO"

if ! git rev-parse --verify --quiet "$REF^{commit}" >/dev/null; then
    echo "找不到上游 ref: $REF" >&2
    exit 1
fi

echo "== 1. 取上游 pristine i18n.js（$REF）=="
PRISTINE="${TMPDIR:-/tmp}/i18n.pristine.js"
git show "$REF:web/static/i18n.js" > "$PRISTINE"
cp "$PRISTINE" web/static/i18n.js

echo "== 2. 灌入映射表 =="
python3 "$HERE/build_i18n_patch.py"

echo "== 3. 追加引擎能力（前缀图标 / 冒号前缀 / 原生对话框）=="
python3 "$HERE/apply_engine_extras.py"

echo "== 4. 语法检查 =="
node --check web/static/i18n.js && echo "node --check OK"
