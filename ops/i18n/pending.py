#!/usr/bin/env python3
"""看上游这波更新新增了哪些英文界面文案、哪些还没翻。

    python3 ops/i18n/pending.py              # 上次同步点 → upstream/main
    python3 ops/i18n/pending.py <base>       # 指定基线，与 upstream/main 比
    python3 ops/i18n/pending.py <base> <head>

只读 web/static 下的 index.html / app.js / fork-ui.js，从 diff 的 + 行里挑出
字符串字面量，减去 zh.json 已有的键。翻译表是「英文原文 → 中文」，所以比对
用的是原文而不是 i18n.js 里的 app.title 那种 key。
"""
import html
import json
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MAP_FILE = Path(__file__).resolve().parent / 'zh.json'
SCAN = ['web/static/index.html', 'web/static/app.js', 'web/static/fork-ui.js']

LIT = re.compile(r"""'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)\"""")


def norm(s):
    return re.sub(r'\s+', ' ', html.unescape(s)).strip()


def unescape_js(s):
    """把 JS 字符串字面量的转义还原，否则 client\\'s 这种键永远对不上 DOM 里的文本。"""
    def one(m):
        c = m.group(1)
        return {'n': '\n', 't': '\t', 'r': '\r', '0': ''}.get(c, c)
    return re.sub(r'\\(.)', one, s)


def run(*args):
    r = subprocess.run(['git', '-C', str(REPO)] + list(args),
                       capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f'git {" ".join(args)} 失败：{r.stderr.strip()}')
    return r.stdout


def resolve(ref):
    # 不能用 run()：--quiet 失败时 stderr 是空的，报错会变成一句没有内容的「失败」
    r = subprocess.run(
        ['git', '-C', str(REPO), 'rev-parse', '--verify', '--quiet', f'{ref}^{{commit}}'],
        capture_output=True, text=True)
    out = r.stdout.strip()
    if r.returncode != 0 or not out:
        sys.exit(f'找不到 ref：{ref}（上游引用要先 git fetch upstream）')
    return out


def looks_like_ui(text):
    """判断一个字符串字面量像不像界面文案。宁可漏，不要刷一屏噪音。"""
    t = norm(text)
    if len(t) < 3 or len(t) > 200:
        return False
    if '://' in t or t.startswith('/') or t.startswith('.'):
        return False
    if any(c in t for c in '{};=<>'):
        return False
    # 纯标识符 / CSS 类名 / 事件名
    if re.fullmatch(r'[a-z][a-zA-Z0-9_.:-]*', t):
        return False
    if re.fullmatch(r'[A-Za-z0-9_./-]+', t) and ' ' not in t:
        return False
    letters = sum(c.isalpha() or c.isspace() for c in t)
    if letters / len(t) < 0.75:
        return False
    # 一串小写 token（btn btn-sm、flex items-center）是类名，不是文案：
    # 真文案的句子不会整个全小写
    if re.fullmatch(r'[a-z0-9-]+( +[a-z0-9-]+)+', t):
        return False
    # 字体栈（'JetBrains Mono', ui-monospace, …）
    if 'monospace' in t or 'sans-serif' in t or 'serif' in t:
        return False
    # UI 文案通常有空格，或是首字母大写的短语
    return (' ' in t) or t[0].isupper()


def pending_strings(base, head):
    diff = run('diff', '--unified=0', base, head, '--', *SCAN)
    found = set()
    for line in diff.splitlines():
        if not line.startswith('+') or line.startswith('+++'):
            continue
        for m in LIT.finditer(line[1:]):
            s = m.group(1) if m.group(1) is not None else m.group(2)
            if s and looks_like_ui(s):
                found.add(norm(unescape_js(s)))
    return found


def main():
    if not MAP_FILE.exists():
        sys.exit(f'缺少 {MAP_FILE}')
    known = {norm(k): v for k, v in
             json.loads(MAP_FILE.read_text(encoding='utf-8')).items()}

    args = sys.argv[1:]
    if len(args) >= 2:
        base, head = args[0], args[1]
    elif len(args) == 1:
        base, head = args[0], 'upstream/main'
    else:
        # 默认从「上次同步到的上游提交」算起。fork 的 main 是 rebase 在上游之上的，
        # 所以 HEAD 与 upstream/main 的公共祖先就是上一次同步的那个点。
        base = subprocess.run(
            ['git', '-C', str(REPO), 'merge-base', 'HEAD', 'upstream/main'],
            capture_output=True, text=True).stdout.strip()
        if not base:
            sys.exit('算不出 HEAD 与 upstream/main 的公共祖先（先 git fetch upstream）')
        head = 'upstream/main'

    b, h = resolve(base), resolve(head)
    if b == h:
        print(f'上游已跟到最新（{h[:8]}），没有新增文案。')
        return 0

    cand = pending_strings(b, h)
    todo = sorted(cand - set(known))

    print(f'基线 {base} ({b[:8]}) → {head} ({h[:8]})')
    print(f'新增候选文案 {len(cand)} 条，zh.json 已有 {len(cand) - len(todo)} 条，'
          f'待翻译 {len(todo)} 条')
    if todo:
        print()
        print('往 ops/i18n/zh.json 里追加下面这些（键是英文原文）：')
        for s in todo:
            print(f'  {json.dumps(s, ensure_ascii=False)}: "",')
        print()
        print('填好译文后跑 bash ops/i18n/rebuild.sh 重新生成 i18n.js。')
    return 0


if __name__ == '__main__':
    sys.exit(main())
