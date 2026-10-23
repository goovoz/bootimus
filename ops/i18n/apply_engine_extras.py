#!/usr/bin/env python3
"""在生成的 i18n.js 上追加「引擎能力」补丁（幂等）。

build_i18n_patch.py 只负责把映射表灌进去；下面四项是渲染引擎的增强，单独放在这里，
避免改那个 f-string 模板（里面全是双重大括号，手改极易出错）：

1. 状态列前缀图标：「⚠ Kernel boot needs extraction」是同一个文本节点，先剥图标再查表，
   命中后把图标补回去；
2. 冒号前缀：「Download failed: timeout」= 映射键 "Download failed:" + 具体错误；
3. 带变量的整句：`ubuntu prefers kernel boot, currently sanboot`、`Deleted foo.iso` 等；
4. 原生 confirm()：浏览器自己画的对话框，DOM 扫描覆盖不到，改成在调用前翻译文案。
"""
import sys
from pathlib import Path

I18N = Path('/root/bootimus/web/static/i18n.js')
src = I18N.read_text(encoding='utf-8')

# ---------- 1. 带变量的整句规则 ----------
OLD_PATTERNS = """// 带变量的文案（数字、端口）没法精确匹配，用正则兜住。
// 只在整串完全符合这个形态时才替换，避免误伤 "files" 这类普通单词。
const SOURCE_PATTERNS = {
    'zh-CN': [
        [/^Enabled \\((\\d+) share\\(s\\) active, port (\\d+)\\)$/, '已启用（$1 个共享，端口 $2）'],
        [/^(\\d[\\d,]*)\\s+files?$/, '$1 个文件'],
        [/^(\\d[\\d,]*)\\s+images?$/, '$1 个镜像']
    ]
};"""

NEW_PATTERNS = """// 带变量的文案（数字、端口、发行版名）没法精确匹配，用正则兜住。
// 只在整串完全符合这个形态时才替换，避免误伤 "files" 这类普通单词。
const BOOT_METHOD_ZH = { kernel: '内核', sanboot: 'SAN', nbd: 'NBD', nfs: 'NFS', wimboot: 'WIM', http: 'HTTP' };

const SOURCE_PATTERNS = {
    'zh-CN': [
        [/^Enabled \\((\\d+) share\\(s\\) active, port (\\d+)\\)$/, '已启用（$1 个共享，端口 $2）'],
        [/^(\\d[\\d,]*)\\s+files?$/, '$1 个文件'],
        [/^(\\d[\\d,]*)\\s+images?$/, '$1 个镜像'],
        // 「ubuntu prefers kernel boot, currently sanboot」：发行版名与两个引导方式都是变量
        [/^(.+?) prefers (.+) boot, currently (.+)$/, (m, distro, want, got) =>
            distro + ' 建议用' + (BOOT_METHOD_ZH[want] || want) + '引导，当前是 ' + (BOOT_METHOD_ZH[got] || got)],
        [/^Wake-on-LAN packet sent to (.+)$/, (m, target) => '已向 ' + target + ' 发送网络唤醒数据包'],
        [/^Failed to download (.+?): (.+)$/, (m, name, err) => '下载 ' + name + ' 失败：' + err],
        [/^Deleted (.+)$/, (m, name) => '已删除 ' + name]
    ]
};"""

# ---------- 2. 冒号前缀 + 原生对话框 ----------
OLD_PATTERN_TRANSLATE = """function _patternTranslate(base) {
    const list = SOURCE_PATTERNS[_currentLang];
    if (!list) return null;
    for (let i = 0; i < list.length; i++) {
        if (list[i][0].test(base)) return base.replace(list[i][0], list[i][1]);
    }
    return null;
}"""

NEW_PATTERN_TRANSLATE = OLD_PATTERN_TRANSLATE + """

// 「Download failed: timeout」「Failed to upload file: xxx」这类是「固定前缀 + 具体错误」。
// 映射表里以冒号结尾的键天然就是前缀，命中后把剩余部分原样接上。
let _prefixCache = null;

function _prefixTranslate(base) {
    const dict = _srcDict();
    if (!dict) return null;
    if (_prefixCache === null || _prefixCache.lang !== _currentLang) {
        const keys = [];
        for (const key in dict) {
            if (key.length >= 6 && key.charAt(key.length - 1) === ':') keys.push(key);
        }
        keys.sort((a, b) => b.length - a.length);
        _prefixCache = { lang: _currentLang, keys: keys, dict: dict };
    }
    const cache = _prefixCache;
    for (let i = 0; i < cache.keys.length; i++) {
        const key = cache.keys[i];
        if (base.length > key.length && base.indexOf(key) === 0) {
            return cache.dict[key] + base.slice(key.length).replace(/^\\s+/, '');
        }
    }
    return null;
}

// 原生 confirm()/alert() 由浏览器绘制，TreeWalker 覆盖不到；在调用前把已知模板换成中文。
// 只换文案、不改 app.js：其他未覆盖的调用原样透传。
const NATIVE_TEXT_PATTERNS = {
    'zh-CN': [
        [/^Delete (\\d+) images?\\?\\s*WARNING: This will permanently delete the ISO files from disk\\.$/,
            (m, n) => '确定删除 ' + n + ' 个镜像？\\n\\n警告：这会从磁盘上永久删除对应的 ISO 文件。'],
        [/^Delete image (.+?)\\?\\s*WARNING: This will permanently delete the ISO file from disk and remove it from the database\\.$/,
            (m, name) => '确定删除镜像 ' + name + '？\\n\\n警告：这会从磁盘上永久删除该 ISO 文件，并从数据库中移除。'],
        [/^Extract kernel and initrd from (.+?)\\?\\s*This will mount the ISO and extract boot files for direct kernel booting\\.$/,
            (m, name) => '从 ' + name + ' 中解压内核与 initrd？\\n\\n这会挂载该 ISO 并解压启动文件，用于直接内核引导。'],
        [/^Download netboot files for (.+?)\\?\\s*This will download and extract the proper network boot files required for Debian\\/Ubuntu network installation\\.$/,
            (m, name) => '为 ' + name + ' 下载网络引导文件？\\n\\n这会下载并解压 Debian/Ubuntu 网络安装所需的网络引导文件。'],
        [/^Are you sure you want to delete "(.+?)"\\?\\s*This will permanently delete the file from the server\\.$/,
            (m, name) => '确定要删除「' + name + '」吗？\\n\\n这会从服务器上永久删除该文件。'],
        [/^Are you sure you want to delete user "(.+?)"\\?$/, (m, name) => '确定要删除用户「' + name + '」吗？'],
        [/^Are you sure you want to delete (.+?)\\?$/, (m, what) => '确定要删除 ' + what + ' 吗？'],
        [/^Delete custom profile "(.+?)"\\?$/, (m, name) => '确定删除自定义配置「' + name + '」？'],
        [/^Delete downloaded files for (.+?)\\? You can re-download later\\.$/, (m, name) => '确定删除 ' + name + ' 的已下载文件？之后可以重新下载。'],
        [/^Delete custom tool "(.+?)"\\? This removes the tool and all its files\\.$/, (m, name) => '确定删除自定义工具「' + name + '」？该工具及其所有文件都会被移除。'],
        [/^Delete the entire "(.+?)" bootloader set and all its files\\?$/, (m, name) => '确定删除整个引导程序集「' + name + '」及其所有文件？'],
        [/^Delete "(.+?)" from set "(.+?)"\\?$/, (m, file, set) => '确定从引导程序集「' + set + '」中删除「' + file + '」？'],
        [/^Delete ISO file "(.+?)"\\? This will remove the ISO file but keep the extracted boot folder\\.$/, (m, name) => '确定删除 ISO 文件「' + name + '」？这会删除 ISO 文件，但保留已解压的引导目录。'],
        [/^Delete boot folder "(.+?)"\\? This will remove all extracted files but keep the ISO\\.$/, (m, name) => '确定删除引导目录「' + name + '」？这会删除所有已解压文件，但保留 ISO。'],
        [/^Delete group "(.+?)"\\? This will unassign all images from this group\\.$/, (m, name) => '确定删除组「' + name + '」？该组下的所有镜像会被解除分组。'],
        [/^Delete client group "(.+?)"\\? Members will be detached \\(not deleted\\)\\.$/, (m, name) => '确定删除客户端组「' + name + '」？组内成员会被解除关联（不会被删除）。'],
        [/^Delete cluster "(.+?)"\\? Its nodes will be detached and become unmanaged\\.$/, (m, name) => '确定删除集群「' + name + '」？其节点会解除关联并变为未托管状态。'],
        [/^Delete node image "(.+?)"\\?$/, (m, name) => '确定删除节点镜像「' + name + '」？'],
        [/^Reinstall (.+?)\\? The node will be wiped and reprovisioned on next PXE boot\\.$/, (m, mac) => '确定重装 ' + mac + '？该节点会被清空，并在下次 PXE 启动时重新部署。'],
        [/^Remove (.+?) from its cluster\\? The node will become unmanaged\\.$/, (m, mac) => '确定把 ' + mac + ' 从集群中移除？该节点会变为未托管状态。'],
        [/^Delete client (.+?)\\?$/, (m, mac) => '确定删除客户端 ' + mac + '？'],
        [/^Delete (\\d+) clients?\\?$/, (m, n) => '确定删除 ' + n + ' 个客户端？'],
        [/^Delete this schedule\\?$/, () => '确定删除这个计划？'],
        [/^Delete (.+?)\\/(.+?)\\?$/, (m, distro, file) => '确定删除 ' + distro + '/' + file + '？']
    ]
};

(function () {
    const nativeConfirm = window.confirm;
    const nativeAlert = window.alert;

    function _translateNative(message) {
        const raw = String(message === null || message === undefined ? '' : message);
        const base = _normSpace(raw);
        const dict = _srcDict();
        if (dict && dict[base] !== undefined) return dict[base];
        const list = NATIVE_TEXT_PATTERNS[_currentLang];
        if (list) {
            for (let i = 0; i < list.length; i++) {
                if (list[i][0].test(base)) return base.replace(list[i][0], list[i][1]);
            }
        }
        return raw;
    }

    window.confirm = function (message) {
        return nativeConfirm.call(window, _translateNative(message));
    };
    window.alert = function (message) {
        return nativeAlert.call(window, _translateNative(message));
    };
})();"""

# ---------- 3. 状态列的前缀图标 ----------
OLD_ARROW = """        let base = _normSpace(raw);
        // 可排序表头的文本是「Name ↕」这种：箭头由 sortIcon 直接拼在文本节点里，"""

NEW_ARROW = """        let base = _normSpace(raw);
        // 状态列是「⚠ Kernel boot needs extraction」「✓ Ready」这种：图标和文字在同一个
        // 文本节点里，先剥掉前缀图标再查表，命中后把图标补回去
        let mark = '';
        const mm = base.match(/^([\\u26a0\\u2713\\u2714\\u2717\\u2718\\u26d4\\u2139\\u25cf\\u2022\\u25b2\\u25bc\\u2192\\u2190!]+\\uFE0F?\\s+)(.+)$/);
        if (mm) {
            const rest = mm[2].trim();
            if (dict[rest] !== undefined || _patternTranslate(rest) !== null || _prefixTranslate(rest) !== null) {
                mark = mm[1];
                base = rest;
            }
        }
        // 可排序表头的文本是「Name ↕」这种：箭头由 sortIcon 直接拼在文本节点里，"""

# ---------- 4. 查表时接上前缀规则 ----------
OLD_LOOKUP = """        let hit = dict[base];
        if (hit === undefined) hit = _patternTranslate(base);
        if (hit === undefined || hit === null) continue;
        hit += arrow;"""

NEW_LOOKUP = """        let hit = dict[base];
        if (hit === undefined) hit = _patternTranslate(base);
        if (hit === undefined || hit === null) hit = _prefixTranslate(base);
        if (hit === undefined || hit === null) continue;
        hit = mark + hit + arrow;"""

steps = [
    ('带变量的整句规则', OLD_PATTERNS, NEW_PATTERNS),
    ('冒号前缀 + 原生对话框', OLD_PATTERN_TRANSLATE, NEW_PATTERN_TRANSLATE),
    ('状态列前缀图标', OLD_ARROW, NEW_ARROW),
    ('查表接上前缀规则', OLD_LOOKUP, NEW_LOOKUP),
]

for name, old, new in steps:
    if new in src:
        print('  跳过（已应用）:', name)
        continue
    if old not in src:
        sys.exit('找不到锚点：' + name)
    src = src.replace(old, new, 1)
    print('  已应用:', name)

I18N.write_text(src, encoding='utf-8')
print('i18n.js 已更新（%d 字符）' % len(src))
