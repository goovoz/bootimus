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
| `i18n-zh-full` | active | 把管理台剩余文案补成中文：`i18n.js` 增加「英文原文 → 中文」映射（当前 696 条），并在每次 DOM 变更后自动翻译（覆盖 app.js 动态渲染的内容，不改 app.js） | `web/static/i18n.js` |
| `download-proxy` | active | ISO 下载可走网络代理：设置页存代理地址与可选凭据，「从 URL 下载」与「获取镜像」各有一个「使用代理下载」开关 | `internal/admin/handlers.go`、`internal/models/models.go`、`internal/storage/*.go`、`internal/server/server.go`、`web/static/{index.html,app.js,i18n.js}` |
| `download-resume` | active | ISO 下载断点续传 + 残留清理：数据先写 `.part`（带 ETag 的 meta），再下用 `Range` 续传、`If-Range` 防串版本，完成才 rename；启动清理 7 天前的残留 | `internal/admin/handlers.go`、`internal/admin/download_resume_test.go`、`internal/server/server.go` |

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

- `SOURCE_TEXT`：696 条原文到中文的映射（专有名词与示例值除外）
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
