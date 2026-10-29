#!/usr/bin/env python3
"""汉化审计：找出 web/static 里还没进翻译表的英文，以及翻译表里已经失效的词条。

为什么要它：之前的做法是「用户截图 → 逐条补」，所以 app.js 里 34 个 <th>
从来没被覆盖过，报一个补一个。这个脚本把「漏了什么」变成可枚举的清单。

两类输出：
  missing  源码里有、翻译表没有 —— 必须补
  stale    翻译表里有、源码里再也找不到 —— 上游可能改了原文，需要跟着改

用法：
  python3 ops/i18n/audit.py            # 报告，有 missing 时退出码 1
  python3 ops/i18n/audit.py --json     # 机器可读
  python3 ops/i18n/audit.py --strict   # stale 也算失败（发版前用）

注意：这里只覆盖**静态文本**（<th>、HTML 里的字面量）。服务端返回的句子和
模板拼出来的文本（`${a} of ${b}`）静态扫不到，那部分靠 ops/i18n/audit-dom.js
（Playwright 扫真实 DOM）。
"""
import argparse
import html as htmllib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ZH = ROOT / 'ops/i18n/zh.json'
APP_JS = ROOT / 'web/static/app.js'
INDEX = ROOT / 'web/static/index.html'
FORK_UI = ROOT / 'web/static/fork-ui.js'
GEN_GO = ROOT / 'internal/autoinstall/generate.go'

# 不该翻的：技术标识、发行版名、语言名、协议名、品牌名
ALLOW = {
    'IP', 'URI', 'SAN', 'CPU', 'MAC', 'UUID', 'DHCP', 'PXE', 'NFS', 'SMB', 'TFTP',
    'HTTP', 'HTTPS', 'API', 'URL', 'JSON', 'HTML', 'CSS', 'ID', 'OK', 'ISO',
    'UEFI', 'BIOS', 'ARM64', 'x86_64', 'Kubernetes', 'Webhook', 'netboot',
    # 品牌 / 产品名 / 外链：界面上保持原样更清楚
    'Bootimus', 'bootimus', 'Docker Hub', 'GitHub', 'Website', 'Report Issues',
    'Redfish', 'Intel AMT', 'Memdisk', 'Licence',
    'PXE/HTTP Boot Server', 'PXE/HTTP Boot Server Management Interface',
    'UEFI x86', 'UEFI ARM64',
    # 文件系统 / 分区技术名：界面上保持原文更准确
    'LVM', 'ZFS', 'Btrfs', 'ext4', 'XFS',
    # 发行版名（模板下拉里出现）
    'AlmaLinux', 'Rocky Linux', 'CentOS', 'Fedora', 'Debian', 'Ubuntu', 'openSUSE',
    'Agama (openSUSE Leap 16 / Tumbleweed)',
    # 这句在 DOM 里被 <code> 切成两段（"Optional." + "is replaced with ..."），
    # 两个碎片都已经配了词条；整串永远匹配不到，别当缺失报。
    "Optional. is replaced with the client's MAC without colons. Leave blank to use iPXE's default.",
}
# 看起来就不是 UI 文案的（模板变量、实体、残缺句、URL、进度占位、版本占位）
NOISE = re.compile(
    r'^(\$\{|&[a-zA-Z]+;|https?://|/|=|--|#|[A-Z_]{4,}$|\s*$'   # 模板变量/实体/URL/变量名
    r'|\s*[.,;:]\s'                                             # 以标点开头 = 被标签切碎的残句
    r'|\d+%\s*-\s*'                                             # 进度占位 0% - 0 B/s
    r'|v\.\.\.$'                                                # 版本占位
    r'|🚀'                                                       # 品牌标题
    r'|[A-Za-z0-9.-]+\.(com|org|io|net)(/|$))'                   # 裸域名
)
HAS_LATIN = re.compile(r'[A-Za-z]')
HAS_CJK = re.compile(r'[\u4e00-\u9fa5]')
# 中间被 <code>/<a> 之类的行内标签挖空后剩下的残句，例如
# "iPXE SAN URI: , or ." —— 这类不能当整句翻，要按片段处理，单列出来
FRAGMENTED = re.compile(r'\s[,.;:]\s|,\s*\.\s*$|:\s*,\s')


def strip_tags(s):
    # 必须解码实体：DOM 里看到的是 —— / <filename>，源码里写的是 &mdash; / &lt;。
    # 不解码的话，上游把字面字符改成实体（或反过来）就会被误判成「新增文案」，
    # 实际上翻译表里早就有对应 key。
    s = htmllib.unescape(s)
    return re.sub(r'\s+', ' ', re.sub(r'<[^>]+>', '', s)).strip()


def collect_th(path):
    """<th> 里的文本（这是最容易漏的一类）"""
    if not path.exists():
        return {}
    src = path.read_text(encoding='utf-8')
    out = {}
    for m in re.finditer(r'<th[^>]*>(.*?)</th>', src, re.S):
        # 去掉 ${sortIcon(...)} 这类模板片段，只留文字
        raw = re.sub(r'\$\{[^}]*\}', '', m.group(1))
        t = strip_tags(raw)
        if t:
            out.setdefault(t, path.name)
    return out


def collect_html_text(path):
    """index.html 里标签之间的文本（跳过 script/style/code/pre）"""
    if not path.exists():
        return {}
    src = path.read_text(encoding='utf-8')
    src = re.sub(r'<(script|style|pre|code)\b.*?</\1>', ' ', src, flags=re.S | re.I)
    out = {}
    for m in re.finditer(r'>([^<>{}]{3,200})<', src):
        t = strip_tags(m.group(1))
        if t:
            out.setdefault(t, path.name)
    return out


def collect_options(path):
    """<option> 的文本"""
    if not path.exists():
        return {}
    src = path.read_text(encoding='utf-8')
    out = {}
    for m in re.finditer(r'<option[^>]*>(.*?)</option>', src, re.S):
        t = strip_tags(m.group(1))
        if t:
            out.setdefault(t, path.name)
    return out


def collect_go_labels(path):
    """Go 里模板字段的 Label / Help / 选项文案。

    这是之前的盲区：autoinstall 模板的表单是按 generate.go 里的 Var 定义动态渲染的，
    文案全在 Go 源码里，扫 web/static 根本看不到。加字段不补词条就会漏成一片英文。
    只认这几个构造函数的第 2 个参数（Label 位），外加 opt() 的 label，误报可控。
    """
    if not path.exists():
        return {}
    src = path.read_text(encoding='utf-8')
    out = {}
    pats = [
        # text("name", "Label", ...) / numberVar / passwordVar / selectVar / selectVarHelp
        r'(?:text|numberVar|passwordVar|selectVar|selectVarHelp)\(\s*"[^"]*"\s*,\s*"((?:[^"\\]|\\.)*)"',
        # opt("value", "Label")
        r'opt\(\s*"[^"]*"\s*,\s*"((?:[^"\\]|\\.)*)"',
        # 裸结构体 {Name: "x", Label: "..."}
        r'Label:\s*"((?:[^"\\]|\\.)*)"',
        # Help: "..."
        r'Help:\s*"((?:[^"\\]|\\.)*)"',
    ]
    for p in pats:
        for m in re.finditer(p, src):
            t = m.group(1).replace('\\"', '"').strip()
            if t:
                out.setdefault(t, path.name)
    return out


def is_candidate(text):
    if not HAS_LATIN.search(text):
        return False
    if HAS_CJK.search(text):
        return False
    if text in ALLOW:
        return False
    if NOISE.match(text):
        return False
    if len(text) > 120:
        return False
    return True


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--json', action='store_true')
    ap.add_argument('--strict', action='store_true', help='stale 也算失败')
    args = ap.parse_args()

    zh = json.loads(ZH.read_text(encoding='utf-8'))

    found = {}
    for path in (APP_JS, FORK_UI, INDEX):
        for t, src in collect_th(path).items():
            found.setdefault(t, src)
    for path in (INDEX,):
        for t, src in collect_html_text(path).items():
            found.setdefault(t, src)
        for t, src in collect_options(path).items():
            found.setdefault(t, src)
    # 模板字段文案（generate.go 里的 Var 定义）
    for t, src in collect_go_labels(GEN_GO).items():
        found.setdefault(t, src)

    missing, fragmented = [], []
    for t in found:
        if not is_candidate(t) or t in zh:
            continue
        (fragmented if FRAGMENTED.search(t) else missing).append(t)
    missing.sort()
    fragmented.sort()

    # stale：翻译表里有、源码里完全找不到的。动态文本本来就不在源码里，
    # 所以这里只对「像静态文案的 key」做判断，避免一片红。
    src_all = ''
    for p in (APP_JS, FORK_UI, INDEX):
        if p.exists():
            src_all += p.read_text(encoding='utf-8')
    stale = []
    for k in zh:
        if not is_candidate(k):
            continue
        if k not in src_all:
            stale.append(k)

    report = {
        'missing_count': len(missing),
        'fragmented_count': len(fragmented),
        'stale_count': len(stale),
        'missing': missing,
        'fragmented': fragmented,
        'stale': stale[:200],
        'checked': len(found),
    }

    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print('=== 汉化审计 ===')
        print('  扫描到静态文本 %d 条，翻译表 %d 条' % (len(found), len(zh)))
        print('  缺失 %d 条%s' % (len(missing), '' if not missing else '：'))
        for t in missing:
            print('    - %s  (来自 %s)' % (t, found[t]))
        if fragmented:
            print('  被标签切碎 %d 条（要按片段配词条，不能当整句）:' % len(fragmented))
            for t in fragmented:
                print('    ~ %s' % t)
        print('  失效 %d 条（源码里找不到，可能上游改了原文）' % len(stale))
        for t in stale[:30]:
            print('    ~ %s' % t)
        if len(stale) > 30:
            print('    … 还有 %d 条' % (len(stale) - 30))

    if missing:
        print('\n提示：缺失项直接加进 ops/i18n/zh.json，然后 bash ops/i18n/rebuild.sh')
        return 1
    if args.strict and stale:
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
