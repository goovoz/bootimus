# Fork 补丁与上游同步

本仓库是 [garybowers/bootimus](https://github.com/garybowers/bootimus) 的 fork，在 `main` 上
叠了少量**不想提交给上游**的功能补丁。机制目标：**持续吃上游更新，同时保证补丁不被覆盖；
一旦上游自己实现了同一件事，补丁自动让位。**

## 一句话模型

```
main = 上游 main + 一串「带 Fork-Patch trailer 的补丁提交」
```

- 补丁提交的 message 里必须有 `Fork-Patch: <id>`
- 纯元数据提交（改本目录文档 / 清单）用 `Fork-Meta:` 标记
- 每个补丁的改动处都有 `[bootimus-fork L1]` 锚点注释，rebase 冲突时一眼能认出

## 每日同步做什么

`.github/workflows/sync-upstream.yml`（每天 03:23 北京时间 + 手动触发）：

1. 拉上游 `main`；**SHA 没变就直接退出**（省 CI 分钟数）
2. 校验 `main` 上超出的提交都带 `Fork-Patch:` / `Fork-Meta:` 标记
   —— 出现未标记提交就报错停手（自动同步只对「已知形态」负责）
3. 把上游代码导出到临时目录，跑对应补丁的探测器（`.github/scripts/detect-*.sh`）
4. 按判定结果处置：

   | 判定 | 含义 | 自动动作 |
   |---|---|---|
   | `NEEDED` | 上游仍未实现 | `git rebase upstream/main` 重放补丁 |
   | `SUSPECT` | 出现相关痕迹但证据不足 | 照常重放，同时在总结里**告警**要求人工确认 |
   | `IMPLEMENTED` | 上游自己实现了 | `reset` 到上游、删除补丁、写退役记录（上游实现直接顶替我们的） |

5. `go build ./...` + `go test ./internal/admin/` 通过后才推送
6. rebase 冲突 → 不开天窗：把冲突原样提交到 `sync-conflict-*` 分支并开 PR 标 `[需人工]`，
   工作流故意失败提醒

> 用 `--force-with-lease` 推送：重放或退役都会改写补丁提交的 SHA，这是预期行为。

## 补丁清单

| id | 状态 | 做什么 | 触碰的上游文件 |
|---|---|---|---|
| `iso-url-no-suffix` | active | 允许下载不带 `.iso` 后缀的链接：2 字节 Range 探测 Content-Type，从 Content-Disposition / 最终路径取名，UI 可显式指定文件名 | `internal/admin/handlers.go`、`internal/admin/download_test.go`、`web/static/app.js`、`web/static/index.html` |
| `i18n-zh-full` | active | 把管理台剩余文案补成中文：`i18n.js` 增加「英文原文 → 中文」映射（当前 830 条），并在每次 DOM 变更后自动翻译（覆盖 app.js 动态渲染的内容，不改 app.js） | `web/static/i18n.js` |
| `download-proxy` | active | ISO 下载可走网络代理：设置页存代理地址与可选凭据，「从 URL 下载」与「获取镜像」各有一个「使用代理下载」开关 | `internal/admin/handlers.go`、`internal/models/models.go`、`internal/storage/*.go`、`internal/server/server.go`、`web/static/{index.html,app.js,i18n.js}` |
| `download-resume` | active | ISO 下载断点续传 + 残留清理：数据先写 `.part`（带 ETag 的 meta），再下用 `Range` 续传、`If-Range` 防串版本，完成才 rename；启动清理 7 天前的残留 | `internal/admin/handlers.go`、`internal/admin/download_resume_test.go`、`internal/server/server.go` |
| `autoinstall-generator` | active | 自动安装文件从模板生成：4 套模板 + 变量表单 + 预览 + 格式校验，产物写入现有文件库 | `internal/autoinstall/generate.go`、`internal/autoinstall/templates/*.tmpl`、`internal/admin/handlers.go`、`internal/server/server.go`、`web/static/{index.html,app.js,i18n.js}` |
| `iso-catalog-resolve` | active | 「获取镜像」下载前解析真实文件名：URL 失效时按目录里最新的同类文件改写，15 分钟缓存 | `internal/admin/iso_resolve.go`、`internal/admin/handlers.go`、`internal/server/server.go`、`web/static/app.js`、`distro-profiles.json` |

清单的机器可读版本在 `.github/fork-patches.json`（含探测器路径、退役策略、触碰文件）。

## 退役是怎么判定的

`iso-url-no-suffix` 的探测器看四个信号（对**上游代码**跑，不跑自己的）：

| 信号 | 怎么取 | 说明 |
|---|---|---|
| `strict_gone` | `URL must point to an .iso file` 这句拒绝消失 | 上游放宽了 URL 后缀校验 |
| `call_gone` | `DownloadISO` 不再直接调用 `isoDownloadFilename(req.URL, req.Filename)` | 上游加了中间层 |
| `probe_added` | 代码出现 `bytes=0-1` 或 `Header.Set("Range"` | **本能力的指纹**：只有为了猜远端文件名才会对下载地址发 Range 请求 |
| `ui_added` | 前端出现 `name="filename"` 输入框（`filename_patterns` 不算） | 上游给了用户显式命名入口 |

判定：

| 组合 | 结果 |
|---|---|
| `strict_gone`，或 `probe_added`，或（`call_gone` 且 `ui_added`） | **IMPLEMENTED** → 退役 |
| 只有 `call_gone` 或只有 `ui_added`（证据不足） | **SUSPECT** → 重放补丁 + 告警人工复核 |
| 四者皆无 | **NEEDED** → 重放补丁 |
| 找不到 `internal/admin/handlers.go` | **UNKNOWN** → 停手 + 人工 |

> **为什么不用 `Content-Disposition` 当信号**：上游 `handlers.go` 本来就有 3 处
> `Content-Disposition`（下载响应头），拿它判定会天天误报 IMPLEMENTED。
> 同理 `name="filename"` 必须精确匹配，不能匹配到 `filename_patterns`。

探测器自带离线自检：`bash .github/scripts/detect-iso-url-no-suffix.sh --selftest`

### 退役是安全的

退役前会先把补丁提交原样推到归档分支 `fork-archive/iso-url-no-suffix-<日期>`，
再 `reset` 到上游。万一自动判定错了，一条命令就能拿回来：

```bash
git checkout -b restore-iso-url-no-suffix fork-archive/iso-url-no-suffix-YYYYMMDD
# 或：git cherry-pick <归档分支上的补丁提交>
```

同时 `docs/FORK-PATCHES.md` 的退役记录会写清「哪天、上游哪个 commit、为什么退役」。

## 中文覆盖补丁（`i18n-zh-full`）为什么这么做

上游的 i18n 是「静态骨架级」的：`i18n.js` 里 8 种语言各 151 个 key，HTML 上用
`data-i18n` 标记的元素由 `applyTranslations()` 渲染；而管理台真正的文案大头在
`app.js` —— 表格列名、按钮、提示语都是渲染时拼出来的英文字面量，共有 400 多条，
且 `applyTranslations()` 只在 `DOMContentLoaded` 跑一次，动态渲染的内容永远不会被翻译。
所以在中文界面下会中英混杂。

补丁的取舍：**不改 `app.js`**（7290 行、几百处字面量，改完上游一动就冲突），
而是在 `i18n.js` 里加一层「英文原文 → 译文」的映射，并接管渲染后的翻译：

- `SOURCE_TEXT`：830 条原文到中文的映射（专有名词与示例值除外）
- `_translateTextNodes()`：逐文本节点精确匹配，跳过 `script/style/code/pre/textarea`
- `_translateAttrs()`：`title` / `placeholder` / `aria-label` 同样翻译
- `_installAutoTranslate()`：`MutationObserver` + 30ms 防抖，动态渲染后自动补译
- 原文记在 `WeakMap` 里，切回英文能完整还原，不会把英文吃掉

判定「上游是否已自行补齐」用 `.github/scripts/detect-i18n-zh-full.sh`：

| 上游状态 | 结果 | 处置 |
|---|---|---|
| zh-CN 字典 >= 300 条，或已出现 `SOURCE_TEXT` / `MutationObserver` | **IMPLEMENTED** | 仅告警，人工确认后再删补丁 |
| 字典 200~300 条 | **SUSPECT** | 告警，补丁保留 |
| 字典 <= 200 条（当前上游 151） | **NEEDED** | 补丁继续生效 |
| 找不到 `web/static/i18n.js` | **UNKNOWN** | 停手转人工 |

**碎片必须整句配套**：一段话被 `<strong>` / `<code>` 切开后，浏览器里是多个独立文本节点，
翻译层逐个节点查表。只补其中一部分会造出「还没有自动安装文件，点击 新建文件 or 上传文件 to add one.」
这种中英混杂——比全英文更难看。所以切片时要么整句配套补齐，要么全部不译（API 文档里
`Add ?mac={mac} for one.` 就是故意不译的例子）。

**为什么不自动退役**：这个补丁一删就是 684 条译文；上游只要「部分实现」就会让判定变绿，
自动删除的代价远大于人工确认的成本。

### 三类容易漏掉的文案

| 来源 | 例子 | 处理方式 |
|---|---|---|
| HTML / app.js 字面量 | `Disk cloning and imaging tool`（工具说明） | 精确映射 |
| 运行时数据（`tools-profiles.json`、`distro-profiles.json` 的 `description` / `region` / `label`） | `24.04 LTS Desktop (amd64)` | 精确映射；扫源码扫不到，要看数据文件 |
| Go 后端拼出来的状态串（`handlers.go`、`kubernetes/flatcar|talos.go`） | `Enabled (1 share(s) active, port 445)` | 带变量的用 **模式规则**（见下） |

### 模式规则（`SOURCE_PATTERNS`）

数字/端口插在文案中间时，精确匹配永远对不上，所以补了一层正则：

```js
const SOURCE_PATTERNS = {
    'zh-CN': [
        [/^Enabled \((\d+) share\(s\) active, port (\d+)\)$/, '已启用（$1 个共享，端口 $2）'],
        [/^(\d[\d,]*)\s+files?$/, '$1 个文件'],
        [/^(\d[\d,]*)\s+images?$/, '$1 个镜像']
    ]
};
```

规则是**整串锚定**的，所以孤立的 `Files`、`No files yet...` 不会被误伤；`files` 只在「数字 + files」这种计数形态下才翻。

### 标识符不硬翻

`current`、`stable`、`default`、`secureboot-official` 这些是**接口取值或目录名**，直接换成中文会让用户填错值、
或让路径对不上。做法是保留原文再补中文注释：`current` → `current（当前版本）`、
`secureboot-official` → `secureboot-official（官方安全启动）`。它们大多出现在输入框 placeholder 上，
翻译只改显示、不改 `value`，提交给后端的内容不受影响。


### 译文与生成器放在仓库里（`ops/i18n/`）

`i18n.js` 是生成物：上游 `i18n.js` + `ops/i18n/parts/*.json` 的映射 + `apply_engine_extras.py`
追加的引擎能力。重新生成只需一条命令（默认以上游 `cb5efed` 为底，可传别的 ref）：

```bash
bash ops/i18n/rebuild.sh            # 或 bash ops/i18n/rebuild.sh <upstream-ref>
```

> 早先这两步的脚本只存在于临时目录，`/tmp` 被清空后 770 条译文就无法重建了，
> 所以源文件与生成器一并入库。

### 三类新增的渲染规则

| 形态 | 例子 | 处理 |
|---|---|---|
| 前缀图标 + 文案（同一文本节点） | `⚠ Kernel boot needs extraction`、`✓ Ready` | 先剥掉 `⚠ ✓ →` 等前缀，查表命中后再补回 |
| 固定前缀 + 具体错误 | `Download failed: timeout` | 映射表里以 `:` 结尾的键当前缀，最长优先匹配，剩余部分原样接上 |
| 整句带变量 | `ubuntu prefers kernel boot, currently sanboot`、`Deleted foo.iso` | 走 `SOURCE_PATTERNS` 正则 + 函数式替换 |

### 原生 `confirm()` 也要翻译

浏览器自己绘制的对话框不在 DOM 里，`TreeWalker` 覆盖不到。`i18n.js` 在加载时包一层
`window.confirm` / `window.alert`：命中 `NATIVE_TEXT_PATTERNS`（解压、删除镜像/客户端/组/
集群等 24 条模板）就先翻译再交给原生弹窗，**不改 `app.js`**；未覆盖的调用原样透传。

## 下载代理补丁（`download-proxy`）

国内镜像站对服务端程序化下载普遍有拦截（四家常用站实测：跳转目标不可达 / 418 WAF / JS 验证页 /
客户端指纹拦截），内置镜像列表这条路收益太低，改成让用户自备代理。

### 用法

1. 「设置 → 网络代理」填代理地址（`host:port`，也接受带 scheme 的完整 URL），用户名/密码可留空；
2. 「从 URL 下载」弹窗勾选 **Use proxy for this download**，或在「获取镜像」弹窗勾选
   **Use proxy for these downloads**（对该弹窗内所有下载生效）；
3. 勾了但设置里没配代理时，会在日志里记一条 `proxy requested but not configured`，然后**直连**下载，
   不会凭空失败。

### 实现要点

- `DownloadISO` 请求体新增 `use_proxy`；`downloadISO` 按需构建 `http.Transport{Proxy: http.ProxyURL(...)}`，
  未勾选时仍是原来的直连 client（行为不变）；
- 代理地址为空 = 不启用；用户名有、密码空时用 `url.User`；
- 配置存在 `proxy_configs` 表（id 固定为 1），与 webhook 配置同一套 GORM 写法，sqlite 与 postgres 各一份实现；
- `/api/proxy-config` GET 读取、POST/PUT 保存，保存前会用 `normalizeProxyURL` 校验地址格式；
- **凭据以明文存于本地 SQLite**（与该库其它配置一致），接口仅管理员可访问；返回值也是明文，
  所以别把管理台暴露到公网。

探测器：`bash .github/scripts/detect-download-proxy.sh --selftest`。

## 断点续传与残留清理补丁（`download-resume`）

上游下载是「`client.Get` 拉全量 + `os.Create` 截断」，中断后既不能续传，也不能直接重下：
半成品就落在 `<name>.iso` 上，下次被 `File already exists` 挡回去，只能手动删；
更糟的是启动时 `scanISOs` + `SyncImages` 会把这个半成品登记成一张「镜像」，界面上看不出它是坏的。

### 做法

- 数据先写 `<name>.iso.part`，全部到齐才 `rename` 成 `<name>.iso` —— 半成品不再冒充镜像
- 再次发起同一 URL 时，若已有 `.part`，用 `Range: bytes=<已有>-` 接着下
- 同时写 `<name>.iso.part.json`（URL / ETag / Last-Modified / 总长），续传带 `If-Range`：
  远端文件换了就退化成 200 整下，避免把两个版本拼在一起
- 远端忽略 `Range`（回 200）、`Content-Range` 起点不符、或 `416` 但长度不对 → 截断重下（最多回退一次）
- `416` 且 `offset == total` → 认为已经下完，直接收尾入库
- 网络中断**不再删除**半成品（留给下次续传）；HTTP 状态错误才放弃
- 启动时 `CleanupStaleDownloads()` 删除 7 天没动的 `.part` 与孤儿 meta，新的保留待续传

### 行为变化

| 场景 | 之前 | 现在 |
|---|---|---|
| 下载中断（容器重启） | 半成品占着 `<name>.iso`，再下 409 | `.part` 保留，再下自动续传 |
| 半成品出现在镜像列表 | 会（启动扫描会登记） | 不会（`.part` 不被扫描） |
| 网络中断 | 删掉半成品，只能重下 | 保留半成品，进度从断点继续 |

探测器：`bash .github/scripts/detect-download-resume.sh --selftest`。只告警不自动退役 ——
续传涉及数据正确性，必须人工确认上游实现等价（尤其 `If-Range`）后再让位。

## 自动安装文件生成器（`autoinstall-generator`）

上游的「Auto-Install Files」只有存取：上传、编辑、删、按镜像/客户端下发，用户得自己从零写
autounattend.xml / kickstart / preseed / autoinstall。本补丁补中间那层「模板 + 变量 → 渲染」。

### 结构

| 文件 | 作用 |
|---|---|
| `internal/autoinstall/generate.go` | 模板目录（变量定义 + 默认文件名）、`Render()`、`Validate()` |
| `internal/autoinstall/templates/*.tmpl` | 4 套内置模板，`go:embed` 进二进制 |
| `GET /api/autoinstall-templates` | 返回模板元数据（前端据此建表单） |
| `POST /api/autoinstall/render` | `{template, values}` → 正文，渲染后立刻做格式校验 |

产物不直接落库：前端拿到正文后调既有的 `/api/autoinstall-files/save`，
所以**「附加到镜像 / 按客户端覆盖」的逻辑一行没改**。

> 文件库里的文件**不会自动对外公开**：要真正下发给安装器，仍要在「镜像」或「客户端」上把它附加（既有逻辑），
> 公开地址形如 `/autoinstall/<镜像文件名>?mac=<mac>`；`/autoinstall/<库内路径>` 是查不到东西的。

### 四套模板与校验

| 模板 | 格式 | 校验 |
|---|---|---|
| Debian / Ubuntu preseed | `preseed` | 至少 5 条 `d-i` 指令 |
| RHEL / Rocky / Alma / Fedora | `kickstart` | 含 `%packages`、`%end`、`rootpw` |
| Ubuntu Server 20.04+ | `autoinstall` | YAML 可解析且含顶层 `autoinstall:` 键 |
| Windows 10 / 11 | `autounattend` | XML 良构 |

Ubuntu 的 `identity.password` 必须是哈希，所以服务端用 `bcrypt` 现算，明文不落盘；
preseed / kickstart / autounattend 按各自安装器的要求写明文（文件本身在 HTTP 上可读，
这一条写进了表单提示与文档）。

### 按客户端取值是模板里的显式开关

变量可以声明 `placeholder`（`{{HOSTNAME}}`、`{{MAC}}` 这些是 bootimus 在下发时替换的 token）。
表单上勾了「使用客户端自身取值」，渲染出来的文件里就保留 token，而不是把某个固定值烧进去——
同一份文件挂到多个客户端，每台机器拿到自己的名字。

实现细节：token 不能直接塞进 `text/template`（`{{HOSTNAME}}` 会被当成函数调用），所以渲染时
先写一个哨兵（`\x00pc:hostname\x00`），执行完再换回 token；`autoinstall.yaml` 里的 hostname
因此要加引号（`hostname: "{{HOSTNAME}}"`），否则占位符会被 YAML 当成流式映射。
未声明 `placeholder` 的字段即使填了 `{{...}}` 也原样输出（有测试盯着）。

探测器：`bash .github/scripts/detect-autoinstall-generator.sh --selftest`。只告警不自动退役。

## 镜像目录里的版本号（`iso-catalog-resolve`）

`distro-profiles.json` 里每条 release 只写了 `path`，前端拼成 `base + path` 直接用。
问题在于目录里的文件名本身带点版本号：Debian `current/` 里就是 `debian-13.7.0-amd64-netinst.iso`，
点版本一升（13.5.0 → 13.7.0）所有写死的路径同时失效，用户看到的是 `HTTP 404`。

### 做法

下载前先问一次 `POST /api/iso-catalog/resolve {url}`：

1. HEAD 原 URL，200 就直接返回（正常情况零额外请求，结果缓存 15 分钟）；
2. 失效则取同目录的 HTML 索引，把原文件名里的**每一段数字**换成 `\d+` 得到模式：
   `debian-13.5.0-amd64-netinst.iso` → `^debian-\d+\.\d+\.\d+-amd64-netinst\.iso$`
   （`debian-edu-…`、换架构的变体因此不会误命中）；
3. 候选按**数字序**比较取最大（`13.10.0` > `13.7.0`，字符串比较会选错）；
4. 对新候选再 HEAD 一次，200 才改写；解析失败就保留原 URL，不阻断下载。

前端命中改写时会把输入框里的 URL 一起更新，并提示「下载链接已更新为当前版本」。

### 顺带修掉的数据

审计 26 个下载点时发现 5 条失效（Debian 2 条、Kali 2 条、Fedora Workstation 1 条命名改了），
已一并更新；以后靠解析兜底。

探测器：`bash .github/scripts/detect-iso-catalog-resolve.sh --selftest`。

### 与官方文档（bootimus.com/docs/auto-install）的对齐

写过一版之后对照官方文档复查，改了三处：

| 项 | 之前 | 现在 | 为什么 |
|---|---|---|---|
| kickstart 默认文件名 | `ks.cfg` | `kickstart.ks` | 服务端按扩展名判类型，`.cfg` 会被当成 **preseed**；文档明确 `.ks` 才是 kickstart |
| 口令写法 | preseed/kickstart 写明文，autoinstall 用 bcrypt | 三处统一 `$6$`（sha512-crypt） | 文档示例即 `$6$rounds=…`；`identity.password` 要求 crypt(3) 哈希，bcrypt 在部分镜像上不被认可 |
| Windows 账户 | 只有内置 Administrator 口令 | 额外生成本地管理员账户（用户名/密码） | OOBE 不再要求手工建号，装完直接能登录 |

`$6$` 的实现用 `github.com/GehirnInc/crypt/sha512_crypt`（纯 Go、无传递依赖），
并用 `openssl passwd -6` 生成的 5 组向量做回归 —— 哈希算错的表现是「装完登录不了」，
比报错难查得多。root 口令留空时不再回退成用户口令：preseed 不写 root 行、kickstart 走 `rootpw --lock`。

Windows 的 `autounattend.xml` 仍用明文口令（unattend 的 `PlainText` 语义），这是格式限制，不是遗漏。

### 主机名

表单上的 **Random** 按钮按各自系统的默认命名规则生成：

- Windows：`DESKTOP-` + 7 位大写字母数字（Windows 自己生成的计算机名就是这个形态）；
- Linux：`<发行版家族>-` + 5 位小写字母数字，例如 `ubuntu-k3f9a`。

勾了「使用客户端自身取值」时按钮不生效（此时主机名来自 `{{HOSTNAME}}` 占位符）。

## 新增一个补丁的步骤

1. 在 `main` 上正常写代码，**一个补丁尽量压成一个提交**，message 末尾加 `Fork-Patch: <id>`
2. 改动处加 `[bootimus-fork L1] fork-patch: <id>` 锚点注释
3. 写探测器 `.github/scripts/detect-<id>.sh`（输出 `RESULT=` / `REASON=`，支持 `--selftest`）
4. 在 `.github/fork-patches.json` 登记，并更新本文件的两个表格
5. 把新文件写进 `.github/fork-paths.txt`（自有文件清单）

## 手动操作

- 立刻检查/同步：**Actions → Sync Upstream → Run workflow**（`mode=direct` 直接更新 main，`mode=pr` 只开 PR）
- 只想知道上游有没有变化：看最近一次运行的总结，里面列了新增提交和判定结果

## 退役记录

| 日期 | 补丁 | 上游实现点 | 说明 |
|---|---|---|---|
<!-- 退役记录由 workflow 自动追加 -->
