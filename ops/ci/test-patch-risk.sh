#!/usr/bin/env bash
# patch-risk.py 的自测：用临时 git 仓库造三种场景，断言判定正确。
# 这类审计器最怕「永远输出无冲突」——所以必须证明它能抓到真冲突。
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
AUDIT="$REPO_ROOT/ops/ci/patch-risk.py"
[ -f "$AUDIT" ] || { echo "FAIL: 找不到 $AUDIT"; exit 1; }
command -v git >/dev/null || { echo "FAIL: 没有 git"; exit 1; }

pass=0; fail=0
ok()   { echo "  PASS  $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $*"; fail=$((fail+1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# 造一个基准仓库：app.go 有 10 行，hello() 在 2-4 行
make_base() {
    local dir="$1"
    mkdir -p "$dir"
    ( cd "$dir"
      git init -q -b main .
      git config user.email t@t; git config user.name t
      printf 'package main\n\nfunc hello() {\n\tprintln("hi")\n}\n\nfunc other() {\n\tprintln("x")\n}\n' > app.go
      mkdir -p .github
      printf 'README.md\n' > .github/fork-paths.txt
      git add -A && git commit -q -m base
    )
}

echo "=== 场景 1：我方与上游改同一行（应判定冲突）==="
d="$WORK/same-line"; make_base "$d"
( cd "$d"
  git checkout -q -b fork
  sed -i 's/\tprintln("hi")/\tprintln("hi from fork")/' app.go
  printf 'README.md\n' > .github/fork-paths.txt
  printf '{"patches":[{"id":"same-line"}]}\n' > .github/fork-patches.json
  git add -A && git commit -q -m "feat: fork 改了 hello 里的那行

Fork-Patch: same-line"
  BASE=$(git rev-parse main)
  git checkout -q main
  git checkout -q -b upstream
  sed -i 's/\tprintln("hi")/\tprintln("hi from upstream")/' app.go
  git commit -qam "upstream 也改了同一行"
  git checkout -q fork
  out=$(python3 "$AUDIT" --base "$BASE" --upstream upstream 2>&1)
  echo "$out" | sed 's/^/    /'
)
out=$( cd "$d" && BASE=$(git rev-parse main) && git checkout -q fork && python3 "$AUDIT" --base "$BASE" --upstream upstream 2>&1 )
if echo "$out" | grep -q 'app.go.*1 处'; then ok "抓到了 app.go 的 1 处冲突"; else bad "没抓到冲突"; fi
if echo "$out" | grep -q 'same-line'; then ok "归属到了补丁 same-line"; else bad "没归属到补丁"; fi

echo
echo "=== 场景 2：我方改函数体、上游改另一函数（应判定无冲突）==="
d2="$WORK/diff-line"; make_base "$d2"
( cd "$d2"
  git checkout -q -b fork
  sed -i 's/\tprintln("hi")/\tprintln("hi from fork")/' app.go
  printf 'README.md\n' > .github/fork-paths.txt
  printf '{"patches":[{"id":"diff-line"}]}\n' > .github/fork-patches.json
  git add -A && git commit -q -m "feat: fork 改 hello

Fork-Patch: diff-line"
  git checkout -q main
  git checkout -q -b upstream
  sed -i 's/\tprintln("x")/\tprintln("x from upstream")/' app.go
  git commit -qam "upstream 改 other"
  git checkout -q fork
)
out2=$( cd "$d2" && BASE=$(git rev-parse main) && python3 "$AUDIT" --base "$BASE" --upstream upstream 2>&1 )
echo "$out2" | sed -n '/A\. 确定性冲突/,/B\./p' | sed 's/^/    /'
if echo "$out2" | sed -n '/A\. 确定性冲突/,/B\./p' | grep -q '无'; then
    ok "正确判定为无冲突"
else
    bad "误报冲突"
fi

echo
echo "=== 场景 3：我方只新增文件（L0），上游改老文件（应判定无冲突）==="
d3="$WORK/new-file"; make_base "$d3"
( cd "$d3"
  git checkout -q -b fork
  printf 'package main\n\nfunc forked() {}\n' > fork_only.go
  printf 'fork_only.go\n' > .github/fork-paths.txt
  printf '{"patches":[{"id":"new-file"}]}\n' > .github/fork-patches.json
  git add -A && git commit -q -m "feat: 新增 fork 自有文件

Fork-Patch: new-file"
  git checkout -q main
  git checkout -q -b upstream
  sed -i 's/\tprintln("hi")/\tprintln("hi v2")/' app.go
  git commit -qam "upstream 改了 app.go"
  git checkout -q fork
)
out3=$( cd "$d3" && BASE=$(git rev-parse main) && python3 "$AUDIT" --base "$BASE" --upstream upstream 2>&1 )
echo "$out3" | sed -n '/A\. 确定性冲突/,/B\./p' | sed 's/^/    /'
if echo "$out3" | sed -n '/A\. 确定性冲突/,/B\./p' | grep -q '无'; then
    ok "新增文件不产生冲突"
else
    bad "把 L0 新文件误判成冲突"
fi

echo
echo "=== 场景 4：--strict 在有冲突时退出码非 0 ==="
d4="$WORK/strict"; cp -r "$WORK/same-line" "$d4"
cd "$d4"
BASE=$(git rev-parse main)
git checkout -q fork
python3 "$AUDIT" --base "$BASE" --upstream upstream --strict >/dev/null 2>&1
rc=$?
[ "$rc" = "1" ] && ok "--strict 退出码 1" || bad "--strict 退出码是 $rc（应为 1）"

echo
echo "==================== 结果：PASS=$pass FAIL=$fail ===================="
[ "$fail" -eq 0 ] || exit 1
