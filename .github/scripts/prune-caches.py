#!/usr/bin/env python3
"""按「预算 + 家族保留」清理 GitHub Actions 缓存。

为什么需要它：镜像构建用 buildkit 的 gha 缓存，每次多架构构建都会新增几十条
`buildkit-blob-*` 层缓存（每条几十到几百 MB）。仓库缓存配额是 10 GB，一旦顶到上限，
GitHub 就开始随机淘汰 —— 下一个构建可能刚好丢掉自己要用的层，白跑十几分钟。
所以要么在构建之前腾空间，要么每天定时扫一遍。

策略（不按“年龄”，因为构建机每天把每条都刷新成「刚刚访问过」）：
  1. 每个家族先护住最新的 N 条（buildkit-blob 120 / index 10 / setup-go 4 / 其它 3）
  2. 其余按「最久没访问」从老到新删，删到总量回到预算以内就停
  3. 总量本来就在预算内 → 一条都不删（避免无谓地打掉热缓存）

环境变量：
  GH_TOKEN / GITHUB_TOKEN   必需，需要 actions: write
  GITHUB_REPOSITORY         默认取自 Actions 环境
  BUDGET_GB                 预算，默认 7（GB）
  DRY_RUN                   true 时只打印计划
"""
import json
import os
import sys
import urllib.error
import urllib.request

REPO = os.environ.get("GITHUB_REPOSITORY", "goovoz/bootimus")
TOKEN = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
BUDGET_GB = float(os.environ.get("BUDGET_GB", "7"))
BUDGET = BUDGET_GB * 1024 ** 3
DRY_RUN = os.environ.get("DRY_RUN", "").lower() in ("1", "true", "yes")

# 每个家族护住最新的条数
KEEP_NEWEST = {"buildkit-blob": 120, "index": 10, "setup-go": 4}
KEEP_DEFAULT = 3
# 第一轮（只删不受保护的）删完仍然超预算时，允许从保护集里继续删，
# 但不能低于这个下限 —— 兜底保住「最近一两次构建」的层缓存，否则下次构建等于冷启动
KEEP_FLOOR = {"buildkit-blob": 60, "index": 5, "setup-go": 4}
FLOOR_DEFAULT = 2

if not TOKEN:
    print("::error::缺少 GH_TOKEN / GITHUB_TOKEN", file=sys.stderr)
    sys.exit(1)


def api(method, path):
    req = urllib.request.Request(
        "https://api.github.com/repos/%s%s" % (REPO, path),
        headers={
            "Authorization": "Bearer " + TOKEN,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        method=method,
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else {}


def human(n):
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024:
            return "%.1f%s" % (n, unit)
        n /= 1024
    return "%.1fTB" % n


def family(key):
    if key.startswith("buildkit-blob"):
        return "buildkit-blob"
    if key.startswith("index"):
        return "index"
    if key.startswith("setup-go"):
        return "setup-go"
    return "other"


def fetch_all():
    items, page = [], 1
    while True:
        data = api("GET", "/actions/caches?per_page=100&page=%d" % page)
        chunk = data.get("actions_caches", [])
        items.extend(chunk)
        if len(chunk) < 100:
            return items
        page += 1


def delete_one(cache_id):
    """删一条缓存；已经不存在（404）也算删成功。"""
    try:
        api("DELETE", "/actions/caches/%s" % cache_id)
        return True
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return True
        print("  删除失败 %s: HTTP %s" % (cache_id, e.code))
        return False


def main():
    items = fetch_all()
    total = sum(c["size_in_bytes"] for c in items)
    print("缓存：%d 条 / %s，预算 %s%s" % (
        len(items), human(total), human(BUDGET), "（dry-run）" if DRY_RUN else ""))

    by_fam = {}
    for c in items:
        by_fam.setdefault(family(c["key"]), []).append(c)
    for lst in by_fam.values():
        lst.sort(key=lambda c: c["last_accessed_at"], reverse=True)

    protected = set()
    for fam, lst in by_fam.items():
        for c in lst[:KEEP_NEWEST.get(fam, KEEP_DEFAULT)]:
            protected.add(c["id"])

    if total <= BUDGET:
        print("在预算内，不删任何缓存（护住 %d 条最新）" % len(protected))
        _summary(len(items), total)
        return 0

    deleted = freed = 0

    def sweep(candidates, tag):
        nonlocal total, deleted, freed
        candidates = sorted(candidates, key=lambda c: c["last_accessed_at"])
        for c in candidates:
            if total <= BUDGET:
                return
            if DRY_RUN:
                deleted += 1
                freed += c["size_in_bytes"]
                total -= c["size_in_bytes"]
                if deleted <= 10:
                    print("  将删除[%s] %-12s %-9s %s" % (
                        tag, c["id"], human(c["size_in_bytes"]), c["key"][:52]))
                continue
            if delete_one(c["id"]):
                deleted += 1
                freed += c["size_in_bytes"]
                total -= c["size_in_bytes"]

    # 第一轮：只动不受保护的
    sweep([c for c in items if c["id"] not in protected], "非保护")

    # 第二轮：还超预算就动保护集，但守住下限
    if total > BUDGET:
        print("::warning::删完非保护缓存仍超预算（%s > %s），从保护集里继续删到下限" % (
            human(total), human(BUDGET)))
        extra = []
        for fam, lst in by_fam.items():
            floor = KEEP_FLOOR.get(fam, FLOOR_DEFAULT)
            extra.extend(lst[floor:])  # 下标 >= floor 的都是可牺牲的
        sweep(extra, "跌破保护线")

    print("%s %d 条，释放 %s" % (
        "计划删除" if DRY_RUN else "已删除", deleted, human(freed)))
    remaining = total if DRY_RUN else sum(c["size_in_bytes"] for c in fetch_all())
    print("%s剩余约 %s" % ("（预估）" if DRY_RUN else "", human(remaining)))
    if remaining > BUDGET:
        print("::warning::仍未回到预算内（保护下限挡着），下次构建后继续清")
    _summary(len(items) - (0 if DRY_RUN else deleted), remaining)
    return 0


def _summary(count, size):
    out = os.environ.get("GITHUB_STEP_SUMMARY")
    if not out:
        return
    try:
        with open(out, "a", encoding="utf-8") as fh:
            fh.write("| 项 | 值 |\n|---|---|\n")
            fh.write("| 缓存条数 | %d |\n" % count)
            fh.write("| 占用 | %s |\n" % human(size))
            fh.write("| 预算 | %s |\n" % human(BUDGET))
            fh.write("| 模式 | %s |\n" % ("dry-run" if DRY_RUN else "实际删除"))
    except OSError:
        pass


if __name__ == "__main__":
    sys.exit(main())
