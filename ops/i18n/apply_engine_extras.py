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

REPO = Path(__file__).resolve().parents[2]
I18N = REPO / 'web/static/i18n.js'
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
        // API 参考页的摘要后面会拼上 Body: / Multipart: / Optional:（后面再接 <code>），
        // 整串查表永远命中不了。拆两段：摘要查表，后缀单独映射。
        [/^(.+?)\\s+(Body|Multipart|Optional):$/,
            (m, sum, kind) => {
                const d = (typeof SOURCE_TEXT !== 'undefined' && SOURCE_TEXT['zh-CN']) || {};
                const zh = d[sum] || sum;
                return zh + ' ' + ({ Body: '请求体', Multipart: '表单', Optional: '可选参数' }[kind] || kind) + '：';
            }],
        // 审计日志的「动作」列：服务端拼的是「Created client bc:24:11:dc:91:c7」，
        // 动词和资源名都是固定的，末尾那段是标识符（MAC、名字），原样保留
        [/^(Created|Deleted|Updated) ((?:disk image)|(?:san target)|(?:deployment profile)|(?:enrolment rule)|(?:auto-install file)|(?:public file)|(?:install image)|(?:audit log)|client|user|image|group|token|file|tool|schedule|webhook|cluster|job|bootloader|profile|rule|target)(?:\\s+(.+))?$/,
            (m, verb, res, id) => {
                const v = { Created: '创建了', Deleted: '删除了', Updated: '更新了' }[verb] || verb;
                const r = { client: '客户端', user: '用户', image: '镜像', group: '分组', token: '令牌',
                   file: '文件', tool: '工具', schedule: '计划任务', webhook: 'Webhook',
                   cluster: '集群', job: '任务', bootloader: '引导加载器', profile: '配置',
                   rule: '规则', target: '目标', 'disk image': '磁盘镜像', 'san target': 'SAN 目标',
                   'deployment profile': '部署档案', 'enrolment rule': '纳管规则',
                   'auto-install file': '自动安装文件', 'public file': '公开文件',
                   'install image': '安装镜像', 'audit log': '审计日志' }[res] || res;
                // 资源名以英文开头（SAN、Webhook）时补个空格，中文里挨着太挤
                return v + (/^[A-Za-z]/.test(r) ? ' ' : '') + r + (id ? ' ' + id : '');
            }],
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
        [/^Delete (.+?)\\/(.+?)\\?$/, (m, distro, file) => '确定删除 ' + distro + '/' + file + '？'],
        [/^Revoke the token "(.+?)"\\? Anything using it will stop working immediately\\.$/,
            (m, name) => '吊销令牌「' + name + '」？正在使用它的地方会立即失效。'],
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

# ---------- 5. 只翻译变过的子树（下载页每秒刷新进度，全量扫描扛不住）----------
OLD_APPLY = 'function _applyAll() {\n    if (_applying || !document.body) return;\n    _applying = true;\n    try {\n        applyTranslations(document);\n        _translateTextNodes(document.body);\n        _translateAttrs(document);\n        _translateTitle();\n    } finally {\n        _applying = false;\n        if (_autoObserver) _autoObserver.takeRecords();\n    }\n}\n\n'

NEW_APPLY = '// 每批 DOM 变动只翻译「变过的那几棵子树」。以前每次变动都从 document.body 全量\n// 走一遍 TreeWalker，而下载页每秒刷新进度 —— 等于每秒扫一遍全表。\nlet _dirtyRoots = new Set();\nconst _DIRTY_FULL_THRESHOLD = 40;\n\nfunction _markDirty(node) {\n    if (!node) return;\n    const el = node.nodeType === Node.TEXT_NODE ? node.parentNode : node;\n    if (el && el.nodeType === Node.ELEMENT_NODE) _dirtyRoots.add(el);\n}\n\nfunction _applyAll() {\n    if (_applying || !document.body) return;\n    _dirtyRoots = new Set();\n    _applying = true;\n    try {\n        applyTranslations(document);\n        _translateTextNodes(document.body);\n        _translateAttrs(document);\n        _translateTitle();\n    } finally {\n        _applying = false;\n        if (_autoObserver) _autoObserver.takeRecords();\n    }\n}\n\nfunction _applyDirty() {\n    if (_applying || !document.body) return;\n    const roots = _dirtyRoots;\n    _dirtyRoots = new Set();\n    // 整表重绘时逐个子树反而更贵，超过阈值就退回全量\n    if (roots.size === 0 || roots.size > _DIRTY_FULL_THRESHOLD) {\n        _applyAll();\n        return;\n    }\n    _applying = true;\n    try {\n        roots.forEach(root => {\n            const scope = root.parentElement || root;\n            if (!scope || !scope.isConnected) return;\n            applyTranslations(scope);\n            _translateTextNodes(scope);\n            _translateAttrs(scope);\n        });\n        _translateTitle();\n    } finally {\n        _applying = false;\n        if (_autoObserver) _autoObserver.takeRecords();\n    }\n}\n\n'

OLD_OBSERVER = '    _autoObserver = new MutationObserver(() => {\n        if (_applying || _autoTimer) return;\n        _autoTimer = setTimeout(() => {\n            _autoTimer = null;\n            _applyAll();\n        }, 30);\n    });\n'

NEW_OBSERVER = '    _autoObserver = new MutationObserver((records) => {\n        for (const r of records) _markDirty(r.target);\n        if (_applying || _autoTimer) return;\n        _autoTimer = setTimeout(() => {\n            _autoTimer = null;\n            _applyDirty();\n        }, 30);\n    });\n'


# ---------- 1b. 启动菜单预览的摘要行 ----------
# 三个字段全是变量（MAC 会变），没法进映射表，只能走 SOURCE_PATTERNS。
MENU_PATTERNS_INNER = """
        // 启动菜单预览的摘要行：MAC、客户端名、类型都是变量拼出来的，整串查表不可能命中。
        // 分隔符在源码里是 `&nbsp;·&nbsp;`，落到 DOM 是 U+00A0，用 \\s* 兜住。
        [/^MAC: (.*?)\\s*·\\s*Client: not registered \\(anonymous menu\\)\\s*·\\s*Kind: boot menu$/,
            (m, mac) => 'MAC：' + mac.replace('unknown', '未知') + ' · 客户端：未注册（匿名菜单）· 类型：启动菜单'],
        [/^MAC: (.*?)\\s*·\\s*Client: (.*?)\\s*·\\s*Kind: boot menu$/,
            (m, mac, client) => 'MAC：' + mac.replace('unknown', '未知') + ' · 客户端：' +
                client.replace('(unnamed)', '（未命名）') + ' · 类型：启动菜单'],
        [/^MAC: (.*?)\\s*·\\s*Client: (.*?)\\s*·\\s*Kind: cluster install boot$/,
            (m, mac, client) => 'MAC：' + mac.replace('unknown', '未知') + ' · 客户端：' +
                client.replace('(unnamed)', '（未命名）') + ' · 类型：集群安装启动'],
        [/^MAC: (.*?)\\s*·\\s*Client: (.*?)\\s*·\\s*Kind: (.*?)\\s*·\\s*Next boot: (.*)$/,
            (m, mac, client, kind, nb) => 'MAC：' + mac.replace('unknown', '未知') + ' · 客户端：' +
                client.replace('(unnamed)', '（未命名）') + ' · 类型：' +
                (kind === 'cluster install boot' ? '集群安装启动' : '启动菜单') + ' · 下次启动：' + nb],
        // 磁盘镜像页顶部的红字：problems 是服务端返回的数组，join(' ') 之后拼进同一个文本节点，
        // 条数随环境变（没启 NFS / 没装 Clonezilla / 两个都没有），所以按组合各写一条。
        [/^the NFS server is disabled; start Bootimus with --nfs-enabled\\s*the Clonezilla tool is not downloaded and enabled\\s*Enable the Clonezilla tool under$/,
            'NFS 服务已停用，请用 --nfs-enabled 启动 Bootimus；Clonezilla 工具未下载并启用。在'],
        [/^the NFS server is disabled; start Bootimus with --nfs-enabled\\s*Enable the Clonezilla tool under$/,
            'NFS 服务已停用，请用 --nfs-enabled 启动 Bootimus。在'],
        [/^the Clonezilla tool is not downloaded and enabled\\s*Enable the Clonezilla tool under$/,
            'Clonezilla 工具未下载并启用。在'],
"""

NEW_PATTERNS_MENU = NEW_PATTERNS.replace(
    "        [/^Deleted (.+)$/, (m, name) => '已删除 ' + name]",
    "        [/^Deleted (.+)$/, (m, name) => '已删除 ' + name]," + MENU_PATTERNS_INNER,
)
assert NEW_PATTERNS_MENU != NEW_PATTERNS, '菜单正则没插进 SOURCE_PATTERNS'

steps = [
    ('带变量的整句规则', OLD_PATTERNS, NEW_PATTERNS),
    ('冒号前缀 + 原生对话框', OLD_PATTERN_TRANSLATE, NEW_PATTERN_TRANSLATE),
    ('状态列前缀图标', OLD_ARROW, NEW_ARROW),
    ('查表接上前缀规则', OLD_LOOKUP, NEW_LOOKUP),
    ('只翻译变过的子树', OLD_APPLY, NEW_APPLY),
    ('观察器改记脏根', OLD_OBSERVER, NEW_OBSERVER),
    ('启动菜单预览摘要行', NEW_PATTERNS, NEW_PATTERNS_MENU),
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
