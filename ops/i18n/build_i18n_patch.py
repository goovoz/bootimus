#!/usr/bin/env python3
"""把翻译表注入 web/static/i18n.js，并把 applyTranslations 升级为「自动扫描文本节点」。

设计要点：
- 映射表的 key 必须是 DOM 里真实出现的文本，所以先 html.unescape 再压缩空白；
- SOURCE_TEXT 与 WeakMap 一起工作，切回英文时能还原原始英文；
- 用 MutationObserver + 30ms 防抖，覆盖 app.js 渲染出来的动态内容。
"""
import html
import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
I18N = REPO / 'web/static/i18n.js'
SRC = Path(__file__).resolve().parent / 'parts'

ANCHOR = '// [bootimus-fork L1] fork-patch: i18n-zh-full'
START = 'const _COMMON_BUTTON_MAP = {'


def norm(s: str) -> str:
    return re.sub(r'\s+', ' ', html.unescape(s)).strip()


def load_map() -> dict:
    merged = {}
    for name in ('zh_part1.json', 'zh_part2.json', 'zh_part3.json', 'zh_part4.json',
                 'zh_part5.json', 'zh_part6.json', 'zh_part7.json', 'zh_part9.json',
                 'zh_part10.json'):
        p = SRC / name
        if not p.exists():
            sys.exit(f'缺少 {p}')
        data = json.loads(p.read_text(encoding='utf-8'))
        for k, v in data.items():
            key = norm(k)
            if len(key) < 2:
                continue
            if key in merged and merged[key] != v:
                print(f'  ! key 冲突（保留先出现的）：{key}')
                continue
            merged[key] = v
    return merged


def build_block(mapping: dict) -> str:
    body = json.dumps(mapping, ensure_ascii=False, indent=8)
    # json.dumps 用 8 空格缩进；去掉最外层花括号，保持与文件缩进一致
    inner = body[1:-1].strip('\n')
    return f'''{ANCHOR}
// 上游的 i18n 只覆盖静态 HTML 骨架（151 个 key + data-i18n 标记），而管理台大半文案是
// app.js 渲染时写死的英文，所以切到中文会中英混杂。本补丁不改 app.js：多挂一张
// 「英文原文 → 译文」表，并在每次 DOM 变更后自动翻译；原文记在 WeakMap 里，切回英文可还原。
const SOURCE_TEXT = {{
    'zh-CN': {{
{inner}
    }}
}};

// 带变量的文案（数字、端口）没法精确匹配，用正则兜住。
// 只在整串完全符合这个形态时才替换，避免误伤 "files" 这类普通单词。
const SOURCE_PATTERNS = {{
    'zh-CN': [
        [/^Enabled \\((\\d+) share\\(s\\) active, port (\\d+)\\)$/, '已启用（$1 个共享，端口 $2）'],
        [/^(\\d[\\d,]*)\\s+files?$/, '$1 个文件'],
        [/^(\\d[\\d,]*)\\s+images?$/, '$1 个镜像']
    ]
}};

const _SRC_ATTRS = ['title', 'placeholder', 'aria-label'];
const _srcTextMemo = new WeakMap();
const _srcAttrMemo = new WeakMap();
let _autoObserver = null;
let _autoTimer = null;
let _applying = false;
let _titleSrc = null;

function _srcDict() {{
    return SOURCE_TEXT[_currentLang] || null;
}}

function _normSpace(s) {{
    return String(s).replace(/\\s+/g, ' ').trim();
}}

function _patternTranslate(base) {{
    const list = SOURCE_PATTERNS[_currentLang];
    if (!list) return null;
    for (let i = 0; i < list.length; i++) {{
        if (list[i][0].test(base)) return base.replace(list[i][0], list[i][1]);
    }}
    return null;
}}

function _translateTextNodes(root) {{
    if (!root) return;
    const dict = _srcDict();
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {{
        acceptNode(node) {{
            const p = node.parentNode;
            if (!p) return NodeFilter.FILTER_REJECT;
            const tag = p.nodeName;
            if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'TEXTAREA' ||
                tag === 'CODE' || tag === 'PRE') {{
                return NodeFilter.FILTER_REJECT;
            }}
            return NodeFilter.FILTER_ACCEPT;
        }}
    }});
    const pending = [];
    let node;
    while ((node = walker.nextNode())) {{
        const raw = node.nodeValue;
        if (!raw || !raw.trim()) continue;
        const memo = _srcTextMemo.get(node);
        if (memo && raw === memo.out) {{
            if (!dict) pending.push([node, memo.src]);
            continue;
        }}
        if (memo) _srcTextMemo.delete(node);
        if (!dict) continue;
        let base = _normSpace(raw);
        // 可排序表头的文本是「Name ↕」这种：箭头由 sortIcon 直接拼在文本节点里，
        // 先把箭头剥掉再查表，命中后把箭头补回去
        let arrow = '';
        const am = base.match(/^(.+?)\\s*([\\u2195\\u2191\\u2193\\u21c5\\u25b2\\u25bc])$/);
        if (am && dict[am[1].trim()] !== undefined) {{
            base = am[1].trim();
            arrow = ' ' + am[2];
        }}
        let hit = dict[base];
        if (hit === undefined) hit = _patternTranslate(base);
        if (hit === undefined || hit === null) continue;
        hit += arrow;
        let lead = raw.match(/^\\s*/)[0];
        const trail = raw.match(/\\s*$/)[0];
        // 译文以中文标点开头时丢掉原文的前导空格，免得出现「 ，」
        if (/^[，。：；、！？）】」》]/.test(hit)) lead = '';
        const out = lead + hit + trail;
        _srcTextMemo.set(node, {{ src: raw, out: out }});
        if (raw !== out) pending.push([node, out]);
    }}
    for (const item of pending) item[0].nodeValue = item[1];
}}

function _translateAttrs(root) {{
    if (!root || !root.querySelectorAll) return;
    const dict = _srcDict();
    const writes = [];
    root.querySelectorAll('[title],[placeholder],[aria-label]').forEach(el => {{
        let memo = _srcAttrMemo.get(el);
        if (!memo) {{
            memo = {{}};
            _srcAttrMemo.set(el, memo);
        }}
        for (const attr of _SRC_ATTRS) {{
            if (!el.hasAttribute(attr)) continue;
            const raw = el.getAttribute(attr);
            if (raw === null) continue;
            const m = memo[attr];
            if (m && raw === m.out) {{
                if (!dict) writes.push([el, attr, m.src]);
                continue;
            }}
            if (m) delete memo[attr];
            if (!dict) continue;
            const hit = dict[_normSpace(raw)];
            if (hit === undefined) continue;
            memo[attr] = {{ src: raw, out: hit }};
            writes.push([el, attr, hit]);
        }}
    }});
    for (const w of writes) w[0].setAttribute(w[1], w[2]);
}}

function _translateTitle() {{
    if (_titleSrc === null) _titleSrc = document.title;
    const dict = _srcDict();
    if (!dict) {{
        if (document.title !== _titleSrc) document.title = _titleSrc;
        return;
    }}
    const hit = dict[_normSpace(_titleSrc)];
    if (hit !== undefined && document.title !== hit) document.title = hit;
}}

function _applyAll() {{
    if (_applying || !document.body) return;
    _applying = true;
    try {{
        applyTranslations(document);
        _translateTextNodes(document.body);
        _translateAttrs(document);
        _translateTitle();
    }} finally {{
        _applying = false;
        if (_autoObserver) _autoObserver.takeRecords();
    }}
}}

function _installAutoTranslate() {{
    if (_autoObserver || typeof MutationObserver === 'undefined' || !document.body) return;
    _autoObserver = new MutationObserver(() => {{
        if (_applying || _autoTimer) return;
        _autoTimer = setTimeout(() => {{
            _autoTimer = null;
            _applyAll();
        }}, 30);
    }});
    _autoObserver.observe(document.body, {{
        childList: true,
        subtree: true,
        characterData: true,
        attributes: true,
        attributeFilter: _SRC_ATTRS
    }});
}}

'''


NEW_APPLY = '''function applyTranslations(root) {
    root = root || document;

    root.querySelectorAll('button').forEach(b => {
        if (b.hasAttribute('data-i18n')) return;
        const key = _COMMON_BUTTON_MAP[b.textContent.trim()];
        if (key) b.setAttribute('data-i18n', key);
    });

    root.querySelectorAll('[data-i18n]').forEach(el => {
        const key = el.getAttribute('data-i18n');
        const text = t(key);
        let lastText = null;
        for (let i = el.childNodes.length - 1; i >= 0; i--) {
            const n = el.childNodes[i];
            if (n.nodeType === Node.TEXT_NODE && n.nodeValue.trim()) {
                lastText = n;
                break;
            }
        }
        if (lastText) {
            const next = lastText.nodeValue.replace(/\\S.*\\S|\\S/, text);
            if (lastText.nodeValue !== next) lastText.nodeValue = next;
        } else if (el.textContent !== text) {
            el.textContent = text;
        }
    });
    root.querySelectorAll('[data-i18n-title]').forEach(el => {
        const next = t(el.getAttribute('data-i18n-title'));
        if (el.title !== next) el.title = next;
    });
    root.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
        const next = t(el.getAttribute('data-i18n-placeholder'));
        if (el.placeholder !== next) el.placeholder = next;
    });
    root.querySelectorAll('[data-i18n-aria-label]').forEach(el => {
        const next = t(el.getAttribute('data-i18n-aria-label'));
        if (el.getAttribute('aria-label') !== next) el.setAttribute('aria-label', next);
    });
}'''


def replace_function(src: str, name: str, new_body: str) -> str:
    """从 'function name(' 起，匹配到同层的结尾 '}'（行首零缩进）。"""
    start = src.find(f'function {name}(')
    if start < 0:
        sys.exit(f'找不到 function {name}')
    end = src.find('\n}\n', start)
    if end < 0:
        sys.exit(f'找不到 {name} 的结尾')
    return src[:start] + new_body + src[end + 2:]


def main() -> None:
    mapping = load_map()
    print(f'翻译表条目：{len(mapping)}')

    src = I18N.read_text(encoding='utf-8')
    if ANCHOR in src:
        sys.exit('i18n.js 里已存在补丁，先还原再跑（避免重复注入）')

    block = build_block(mapping)
    idx = src.find(START)
    if idx < 0:
        sys.exit(f'找不到锚点：{START}')
    src = src[:idx] + block + src[idx:]

    src = replace_function(src, 'applyTranslations', NEW_APPLY)

    # setLanguage / _initI18n 里的入口改成 _applyAll
    src = src.replace("""    ['lang-select', 'login-lang-select'].forEach(id => {
        const s = document.getElementById(id);
        if (s && s.value !== lang) s.value = lang;
    });
    applyTranslations();""", """    ['lang-select', 'login-lang-select'].forEach(id => {
        const s = document.getElementById(id);
        if (s && s.value !== lang) s.value = lang;
    });
    _applyAll();""")

    src = src.replace("""    _populateLangSelect(document.getElementById('lang-select'));
    _populateLangSelect(document.getElementById('login-lang-select'));

    applyTranslations();""", """    _populateLangSelect(document.getElementById('lang-select'));
    _populateLangSelect(document.getElementById('login-lang-select'));

    _applyAll();
    _installAutoTranslate();""")

    I18N.write_text(src, encoding='utf-8')
    print(f'已写入 {I18N}（{len(src)} 字符）')

    # 覆盖率核对
    extracted = Path('/tmp/strings_exact.txt')
    if extracted.exists():
        known = {norm(x) for x in extracted.read_text(encoding='utf-8').splitlines() if x.strip()}
        missing = sorted(known - set(mapping))
        print(f'\n提取文案 {len(known)} 条，已覆盖 {len(known & set(mapping))}，未覆盖 {len(missing)}')
        for m in missing:
            print('  未覆盖:', m[:110])


if __name__ == '__main__':
    main()
