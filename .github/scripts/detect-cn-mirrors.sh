#!/usr/bin/env bash
# 探测器：上游是否已自行加入中国大陆镜像站（补丁 cn-mirrors 是否还必要）
#
# 用法：detect-cn-mirrors.sh <upstream-tree>  |  detect-cn-mirrors.sh --selftest
# 输出：RESULT=NEEDED|SUSPECT|IMPLEMENTED|UNKNOWN + REASON=...
#
# 判定依据（只看上游的 distro-profiles.json）：
#   - 含有 ≥4 个中国大陆镜像站地区的条目 → IMPLEMENTED（覆盖已足够，让位）
#   - 有 1~3 个                          → SUSPECT（可能只做了一部分）
#   - 一个都没有                          → NEEDED（补丁仍需要）
set -uo pipefail

MARKERS='Alibaba|Aliyun|Huawei|USTC|Tsinghua|TUNA|China|CN \('

detect() {
    local tree="$1"
    local f="$tree/distro-profiles.json"
    if [ ! -f "$f" ]; then
        echo "RESULT=UNKNOWN"
        echo "REASON=上游树里找不到 distro-profiles.json（路径可能被上游重构）"
        return 0
    fi
    local n
    n=$(python3 - "$f" <<'PY'
import json, re, sys
try:
    d = json.load(open(sys.argv[1], encoding='utf-8'))
except Exception:
    print(-1); sys.exit(0)
pat = re.compile(r'(Alibaba|Aliyun|Huawei|USTC|Tsinghua|TUNA|China|^CN\b)', re.I)
seen = set()
for p in d.get('profiles', []):
    for m in p.get('mirrors') or []:
        r = str(m.get('region', ''))
        if pat.search(r):
            seen.add(r)
print(len(seen))
PY
)
    if [ "$n" = "-1" ]; then
        echo "RESULT=UNKNOWN"
        echo "REASON=distro-profiles.json 解析失败"
        return 0
    fi
    if [ "$n" -ge 4 ]; then
        echo "RESULT=IMPLEMENTED"
        echo "REASON=上游已加入 ${n} 个中国大陆镜像站地区"
    elif [ "$n" -ge 1 ]; then
        echo "RESULT=SUSPECT"
        echo "REASON=上游只加了 ${n} 个中国大陆镜像站地区，可能不完整，保留补丁"
    else
        echo "RESULT=NEEDED"
        echo "REASON=上游没有任何中国大陆镜像站，补丁仍需要"
    fi
}

selftest() {
    local base; base=$(mktemp -d)
    local fail=0

    mk() { # mk <名称> <region 数组的 python 表达式>
        local dir="$base/$1"; mkdir -p "$dir"
        python3 - "$dir/distro-profiles.json" "$2" <<'PY'
import json, sys
out, expr = sys.argv[1], sys.argv[2]
regions = eval(expr)
d = {"version": "test", "profiles": [{"id": "ubuntu", "display_name": "Ubuntu",
     "mirrors": [{"region": r, "base": "https://example.invalid/" + str(i)} for i, r in enumerate(regions)]}]}
json.dump(d, open(out, "w", encoding="utf-8"), ensure_ascii=False)
PY
    }

    mk needed "['Global (Canonical)', 'UK (Bytemark)', 'DE (GSA)']"
    mk suspect "['Global (Canonical)', 'CN (Alibaba Cloud)']"
    mk implemented "['Global (Canonical)', 'CN (Alibaba Cloud)', 'CN (Huawei Cloud)', 'CN (USTC)', 'CN (Tsinghua TUNA)']"
    mk nobroken "['Global (Canonical)']"
    mkdir -p "$base/missing"

    check() {
        local name="$1" want="$2" got
        got=$(bash "$0" "$base/$name" | sed -n 's/^RESULT=//p')
        if [ "$got" = "$want" ]; then
            echo "  PASS  $name -> $got"
        else
            echo "  FAIL  $name -> $got（期望 $want）"
            fail=1
        fi
    }

    echo "=== 探测器自检 ==="
    check needed NEEDED
    check suspect SUSPECT
    check implemented IMPLEMENTED
    check nobroken NEEDED
    check missing UNKNOWN
    rm -rf "$base"
    [ "$fail" -eq 0 ] && echo "ALL PASS" || { echo "有用例失败"; return 1; }
}

if [ "${1:-}" = "--selftest" ]; then
    selftest
else
    detect "${1:?用法: detect-cn-mirrors.sh <upstream-tree> | --selftest}"
fi
