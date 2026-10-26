#!/usr/bin/env python3
"""补丁冲突风险评估。

回答一个现有机制答不出的问题：**我们改的那几行，上游是不是也在改**。
6 个 detect-*.sh 只判断「上游是否已实现该功能（能不能退役）」，
从来没人检查「我们的补丁是不是站在上游要动的那一行上」——那才是每次同步失败的根因。

原理：git 的三方合并用 base 当基准。若我方与上游相对 base 改了**同一行**，
合并/rebase 必然冲突。所以把两边相对 base 的改动都折算成 base 行号，求交集即可。
行号都以 base 为基准，可以直接比较，不用处理版本漂移。

两段输出：
  A. 确定性冲突：交集非空的行 —— 下次 rebase 一定会撞
  B. 热点文件：我方 hunk × 上游最近提交改动次数 —— 上游还没动，但迟早会动

用法：
  python3 ops/ci/patch-risk.py                     # 用默认 upstream/main 做对比
  python3 ops/ci/patch-risk.py --base HEAD~5       # 指定基准
  python3 ops/ci/patch-risk.py --strict             # A 段非空时退出码 1（给 CI 当闸门）
  python3 ops/ci/patch-risk.py --format json        # 机器可读
"""
import argparse
import json
import re
import subprocess
import sys

L0_FILE = '.github/fork-paths.txt'
PATCHES_FILE = '.github/fork-patches.json'
UPSTREAM_HOT_WINDOW = 100  # 上游最近多少个提交算「近期」


def git(*args, check=True):
    out = subprocess.run(['git', *args], capture_output=True, text=True)
    if check and out.returncode != 0:
        raise SystemExit('git %s 失败：%s' % (' '.join(args), out.stderr.strip()))
    return out.stdout


def load_l0():
    """fork 自有文件清单：这些文件上游根本没有，不可能冲突。"""
    try:
        lines = open(L0_FILE, encoding='utf-8').read().splitlines()
    except OSError:
        return set()
    return {l.strip() for l in lines if l.strip() and not l.startswith('#')}


def parse_diff_base_ranges(diff_text):
    """解析 git diff -U0，返回 {文件: [(start, end, is_insert)]}，行号是 **base 侧**。

    is_insert=True 表示该侧在这处只插入（old_count=0），start==end 是插入点。
    """
    result = {}
    cur = None
    for line in diff_text.splitlines():
        if line.startswith('+++ b/'):
            cur = line[6:]
            result.setdefault(cur, [])
        elif line.startswith('@@') and cur:
            m = re.match(r'@@ -(\d+)(?:,(\d+))? \+\d+(?:,(\d+))? @@', line)
            if not m:
                continue
            start = int(m.group(1))
            count = int(m.group(2)) if m.group(2) is not None else 1
            if count == 0:
                result[cur].append((start, start, True))
            else:
                result[cur].append((start, start + count - 1, False))
    return {f: v for f, v in result.items() if v}


def count_changed_lines(diff_text):
    """每个文件 fork 侧改了多少行（增+删）：hunk 数目不变但内容变短时这个数会动。"""
    per = {}
    cur = None
    for line in diff_text.splitlines():
        if line.startswith('+++ b/'):
            cur = line[6:]
            per.setdefault(cur, 0)
        elif cur and (line.startswith('+') or line.startswith('-')) and not line.startswith(('+++', '---')):
            per[cur] += 1
    return per


def overlaps(a, b):
    """两个 base 侧区间是否相交（含插入点贴边的情况）。"""
    for s1, e1, ins1 in a:
        for s2, e2, ins2 in b:
            if ins1 and ins2:
                if abs(s1 - s2) <= 0:
                    return True
                continue
            if ins1:
                if s2 - 1 <= s1 <= e2 + 1:
                    return True
                continue
            if ins2:
                if s1 - 1 <= s2 <= e1 + 1:
                    return True
                continue
            if max(s1, s2) <= min(e1, e2):
                return True
    return False


def patch_files():
    """{patch_id: set(上游侧文件)}，用 trailer 归属。"""
    try:
        meta = json.load(open(PATCHES_FILE, encoding='utf-8'))
    except (OSError, json.JSONDecodeError):
        return {}
    items = meta if isinstance(meta, list) else meta.get('patches') or []
    l0 = load_l0()
    out = {}
    for p in items:
        pid = p['id'] if isinstance(p, dict) else p
        shas = git('log', '--format=%H', '--extended-regexp',
                   '--grep', '^Fork-Patch: %s$' % re.escape(pid)).split()
        files = set()
        for c in shas:
            files.update(f for f in git('show', '--name-only', '--format=', c).split() if f)
        alive = set(git('ls-tree', '-r', '--name-only', 'HEAD').split())
        out[pid] = {f for f in files if f not in l0 and f in alive}
    return out


def upstream_touch_counts(files, base, ref):
    """每个文件在 base..ref 之间被多少个上游提交改过。"""
    counts = {}
    for f in files:
        out = git('log', '--format=%H', '%s..%s' % (base, ref), '--', f, check=False)
        counts[f] = len([x for x in out.split() if x])
    return counts


def patch_own_hunks(pmap):
    """每个补丁**自己**改了多少 hunk（按它自己的提交算，避免共享文件被重复计入）。"""
    out = {}
    for pid, files in pmap.items():
        shas = git('log', '--format=%H', '--extended-regexp',
                   '--grep', '^Fork-Patch: %s$' % re.escape(pid)).split()
        per_file = {}
        for c in shas:
            cur = None
            diff = git('show', '-U0', '--no-color', '--format=', c)
            for line in diff.splitlines():
                if line.startswith('+++ b/'):
                    cur = line[6:]
                elif line.startswith('@@') and cur:
                    per_file[cur] = per_file.get(cur, 0) + 1
        out[pid] = {f: n for f, n in per_file.items() if f in files}
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--base', default=None, help='基准提交，默认 merge-base(HEAD, upstream/main)')
    ap.add_argument('--upstream', default='upstream/main')
    ap.add_argument('--strict', action='store_true', help='A 段非空时退出码 1')
    ap.add_argument('--format', choices=['text', 'json'], default='text')
    args = ap.parse_args()

    base = args.base or git('merge-base', 'HEAD', args.upstream).strip()
    l0 = load_l0()

    fork_ranges = parse_diff_base_ranges(git('diff', '-U0', '--no-color', '%s..HEAD' % base))
    up_ranges = parse_diff_base_ranges(git('diff', '-U0', '--no-color', '%s..%s' % (base, args.upstream)))

    # A. 确定性冲突：两边都动过的行（L0 文件上游没有，天然排除）
    conflicts = []
    for f, ranges in fork_ranges.items():
        if f in l0 or f not in up_ranges:
            continue
        if overlaps(ranges, up_ranges[f]):
            hit = sum(1 for r in ranges if overlaps([r], up_ranges[f]))
            conflicts.append({'file': f, 'hunks': hit, 'our_ranges': ranges, 'up_ranges': up_ranges[f]})

    # B. 热点：我方 hunk 数 × 上游近期改动次数
    pmap = patch_files()
    file_to_patches = {}
    for pid, files in pmap.items():
        for f in files:
            file_to_patches.setdefault(f, []).append(pid)

    cand = [f for f in fork_ranges if f not in l0]
    recent = git('log', '--format=%H', '-n', str(UPSTREAM_HOT_WINDOW), args.upstream, check=False).split()
    hot_base = recent[-1] if recent else base
    touch = upstream_touch_counts(cand, hot_base, args.upstream)
    changed = count_changed_lines(git('diff', '-U0', '--no-color', '%s..HEAD' % base))

    hot = []
    for f in cand:
        hunks = len(fork_ranges[f])
        t = touch.get(f, 0)
        if hunks and t:
            hot.append({'file': f, 'hunks': hunks, 'lines': changed.get(f, 0), 'upstream_touches': t,
                        'score': hunks * t, 'patches': sorted(file_to_patches.get(f, []))})
    hot.sort(key=lambda x: -x['score'])

    summary = {}
    own = patch_own_hunks(pmap)
    for pid in pmap:
        relevant = [h for h in hot if pid in h['patches']]
        summary[pid] = {
            'files': len(pmap[pid]),
            'hot_files': len(relevant),
            'hunks': sum(own.get(pid, {}).values()),
            'max_score': max([h['score'] for h in relevant], default=0),
            'own_shared': sorted(set(own.get(pid, {})) & {h['file'] for h in hot}),
        }

    if args.format == 'json':
        print(json.dumps({'base': base, 'conflicts': conflicts, 'hot': hot, 'patches': summary},
                         ensure_ascii=False, indent=2))
        return 1 if (args.strict and conflicts) else 0

    print('基准 base = %s（%s）' % (base[:9], args.upstream))
    print('我方改动文件 %d 个，上游改动文件 %d 个，其中双方都动过的 %d 个'
          % (len(fork_ranges), len(up_ranges), len([f for f in fork_ranges if f in up_ranges])))
    print()

    print('A. 确定性冲突（两边改同一行，下次 rebase 必撞）')
    if not conflicts:
        print('   无')
    else:
        for c in conflicts:
            who = ','.join(sorted(file_to_patches.get(c['file'], []))) or '（未归属到补丁）'
            print('   %-42s %d 处  ← %s' % (c['file'], c['hunks'], who))
    print()

    print('B. 热点文件（我方 hunk × 上游近 %d 个提交的改动次数）' % UPSTREAM_HOT_WINDOW)
    if not hot:
        print('   无')
    else:
        print('   %-40s %5s %7s %6s %6s  %s' % ('文件', 'hunk', '改动行', '上游改', '分数', '相关补丁'))
        for h in hot[:12]:
            print('   %-40s %5d %7d %6d %6d  %s'
                  % (h['file'], h['hunks'], h['lines'], h['upstream_touches'], h['score'], ','.join(h['patches'])))
    print()

    print('C. 按补丁汇总')
    for pid, s in sorted(summary.items(), key=lambda kv: -kv[1]['max_score']):
        print('   %-24s 上游文件 %2d 个   hunk %2d 个   命中热点 %2d 个   最高分 %d'
              % (pid, s['files'], s['hunks'], s['hot_files'], s['max_score']))

    if conflicts and args.strict:
        print('\n::error::存在确定性冲突（--strict）')
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
