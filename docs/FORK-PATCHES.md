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
   | `SUSPECT` | 出现相关痕迹但证据不足 / 能力部分重叠 | 照常重放，同时在总结里**告警**要求人工确认 |
   | `IMPLEMENTED` | 上游自己实现了 | **只摘掉该补丁的那组提交**：以上游为新基线，按原顺序 cherry-pick 其余提交，再写退役记录 |
   | `UNKNOWN` | 探测不了（上游结构大改 / 探测器崩） | **停手**：不 rebase、不推送，直接失败等人工 |

5. `go build ./...` + `go vet ./...` + `go test ./...` 通过后才推送
   （2026-10-25 起是全量测试；以前只跑 `./internal/admin/`，`internal/autoinstall` 的 13 个
   用例在 CI 里从来没被执行过）
6. rebase 冲突 → 不开天窗：把冲突原样提交到 `sync-conflict-*` 分支并开 PR 标 `[需人工]`，
   工作流故意失败提醒

> 用 `--force-with-lease` 推送：重放或退役都会改写补丁提交的 SHA，这是预期行为。
>
> **需要一个 `SYNC_TOKEN` 仓库 secret**（2026-10-25 补）：`GITHUB_TOKEN` 是 GitHub App
> 的 installation token，GitHub **禁止**它修改 `.github/workflows/**`（推冲突分支时上游的
> `ci.yml` 改动会一起带进去 → `remote rejected ... without 'workflows' permission`）。
> 解决方式是配一个带 **Workflows: Read and write** 权限的 fine-grained PAT 存成 secret：
> Settings → Secrets and variables → Actions → New repository secret，名字 `SYNC_TOKEN`。
> workflow 里写的是 `secrets.SYNC_TOKEN || secrets.GITHUB_TOKEN`，没配时会退回默认 token，
> 其余步骤照常。
>
> 同一个 secret 还解决**开 PR** 的问题：仓库默认不允许 `GITHUB_TOKEN` 创建 PR
> （实测 `gh pr create` → GraphQL `Resource not accessible by integration`），
> 用 PAT 就正常。不想用 PAT 的话，替代方案是在
> Settings → Actions → General → Workflow permissions 里勾上
> "Allow GitHub Actions to create and approve pull requests"。

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

退役前会先把当前 `main` 原样推到归档分支 `fork-archive/<补丁 id>-<时间戳>`，再从上游重建。
万一自动判定错了，一条命令就能拿回来：

```bash
git checkout -b restore-iso-url-no-suffix fork-archive/iso-url-no-suffix-YYYYMMDD-HHMMSS
# 或：git cherry-pick <归档分支上的补丁提交>
```

**退役不是 `git reset --hard <上游>`**（2026-10-25 修正）。这个仓库有 6 组补丁 + 20 多个
元数据提交，`reset --hard` 会把它们一次性全部丢掉——那不叫「让一个补丁让位」，叫清空 fork。
现在实现为：

1. 从上游 `<UP>` 建分支
2. `git rev-list --reverse <UP>..<旧 HEAD>` 按原顺序遍历超出的提交
3. 跳过 trailer 等于 `Fork-Patch: <目标 id>` 的提交，其余全部 `cherry-pick`
4. 任一步冲突 → `cherry-pick --abort`，**main 不动**，工作流失败等人工
5. 全部重放成功后才 `mark_retired.py` 写记录并提交（`Fork-Meta: retire-<id>`）

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


### 加固（2026-10-24）

- **口令不回显**：`GET /api/proxy-config` 只返回 `********`，把掩码原样提交回来表示
  「没改」，服务端沿用库里的旧口令；删空输入框才是清空。以前明文口令会出现在浏览器的
  网络面板里。
- **超时与连接回收**：以前 `http.Client{Timeout: 0}` + 每次下载新建 `Transport`
  （`IdleConnTimeout` 为 0）。源站接了连接却不发数据时下载会永久挂住，状态停在
  `downloading`，重试只会拿到 409；每次走代理还漏一条空闲连接。现在 Transport 按代理
  地址缓存复用，拨号 15s、响应头 30s、空闲 30s，结束调 `CloseIdleConnections()`。
- 直连时仍然尊重 `HTTP_PROXY`/`HTTPS_PROXY`（和默认 Transport 一致），只有显式勾选
  代理才覆盖。

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


### 加固（2026-10-24）

- **进度接口的数据竞争**：`Get`/`GetAll` 以前返回内部指针，接口层 `json.Marshal` 与
  下载协程的 `Update` 并发读写同一结构体，`-race` 判定为 DATA RACE。现在返回快照，
  `internal/admin/download_manager_test.go` 里有对应的并发用例。
- **卡死看门狗**：`http.Client` 没有读超时。超过 90s 收不到任何字节就关闭 body，让
  `Read` 立刻返回，半成品保留待下次续传，状态置 `error` 而不是永远 `downloading`。
- **进度写回节流**：每 200ms 或 256KB 才写一次（以前每 32KB 一次，1GB 要加解锁四千多次）；
  速度按「本次实际传输的字节」算，续传不会一上来就显示几个 G/s。
- **记录回收**：完成的记录 24 小时后从内存里丢掉；残留 `.part` 清理除了启动时执行，
  还每 6 小时跑一次（`StartDownloadJanitor`）——长期不重启的机器以前永远不清。

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


### 值必须先转义再进模板（2026-10-24）

`text/template` 不做任何转义，而四种格式的注入面完全不同，所以 `sanitize.go` 在渲染
前统一处理：

| 格式 | 处理 |
|---|---|
| autounattend | XML 转义（`&` `<` `>` `"` `'`）。以前口令里有一个 `&` 就会得到「XML syntax error … invalid character entity」，合法口令被拒 |
| autoinstall | 字符串标量加引号并做 YAML 转义 |
| preseed / kickstart | 没有引号机制，只能约束成「单行 + 允许的字符」。以前主机名里塞一个换行就能凭空多出一条 `d-i` 指令 |

同时补上以前会静默产出坏文件的校验：

- `image_index` / `disk_id` 是 `number` 字段（`<DiskID>first</DiskID>` 以前能渲染成功，
  装到分区阶段才失败）
- 包名不允许 shell 元字符；包列表按格式落地（preseed 空格分隔、kickstart 一行一个、
  autoinstall 是 YAML 列表），不再把 textarea 原样塞进去
- 公钥必须是 `ssh-ed25519 AAAA…` 这种形状，且**按行展开成多条指令**，不再把一个带
  换行的字符串塞进单条命令
- 没声明 Placeholder 的字段里出现 `{{TOKEN}}` 直接报错，而不是原样写进答案文件

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

- Windows：`DESKTOP-` + 7 位大写字母数字（Windows 自己生成的计算机名就是这个形态）。注意默认值是 `*`，交给 Windows 自己生成；Random 按钮生成的是**固定**名字，会写死在这份文件里供所有客户端共用；
- Linux：`<发行版家族>-` + 5 位小写字母数字，例如 `ubuntu-k3f9a`。

勾了「使用客户端自身取值」时按钮不生效（此时主机名来自 `{{HOSTNAME}}` 占位符）。

### 引导模式必须与固件一致（GPT / MBR）

`autounattend.xml` 里的分区表不是随便写的：**Legacy BIOS 只能装到 MBR 盘，UEFI 只能装到 GPT 盘**。
不匹配时 Setup 的原话是「Windows 无法安装到这个磁盘。选中的磁盘采用 GPT 分区形式」，
点确定就取消安装——不写日志、不解释，一轮试装二十分钟白费。

所以模板有 `firmware` 变量，默认 `uefi`：

| 取值 | 分区 | 安装目标 |
|---|---|---|
| `uefi` | `EFI` 300MB(FAT32) + `MSR` 16MB + `Primary` 剩余(NTFS, C:) | PartitionID 3 |
| `bios` | `Primary` 500MB(NTFS、活动) + `Primary` 剩余(NTFS, C:) | PartitionID 2 |

判断客户端是哪种，看服务端日志就够了：

```
proxyDHCP: DISCOVER -> bc:24:11:dc:91:c7 arch=0 bootfile=undionly.kpxe   # Legacy BIOS
proxyDHCP: DISCOVER -> bc:24:11:dc:91:c7 arch=7 bootfile=ipxe.efi        # UEFI
```

`arch=0` + `undionly.kpxe` 就是 Legacy BIOS，此时 wimboot 起的 WinPE 也是 BIOS 模式，
答案文件必须选 `bios`。（wimboot 会按 iPXE 的启动模式自动选 `bootmgr` 或 `bootmgfw.efi`，
所以固件模式是客户端 PXE 时就定死的，服务端改不了。）

### Windows 的计算机名不能用占位符

Windows 走 SMB：`patchWindowsBootWim()` 把**库里的文件原文**写到 `Z:\AutoUnattend.xml`，
`startnet.cmd` 再拷到 `X:\` 交给 `setup.exe /unattend:X:\AutoUnattend.xml`。这一层是 **image 级**、
不是 per-client，所以 `{{HOSTNAME}}` 不会被替换，只会原样变成计算机名（早期版本正是这么错的）。

现在的做法：

- 默认值 `*` —— Windows 自己生成 `DESKTOP-XXXXXXX`，一份文件供整支舰队用也不会重名；
- 填具体值时先按 Windows 规则清洗：`\ / : * ? " < > |`、空格、句点等换成 `-`，
  截断到 15 字符，纯数字或清空后为空则退回 `*`。
  这类值只会在 specialize 阶段（装完重启后）才失败，是最难查的一类错误，所以在渲染时就拦住。

### 软件源镜像只列实测可用的站

模板里的 `mirror` 下拉不是抄来的清单：**每一家都在部署机（.205）上真的取过仓库索引**才算可用，
用的还是安装器自己的 UA 和路径。

| 发行版 | 判据 | 通过的站 |
|---|---|---|
| Debian | `/{path}/dists/stable/Release` 含 `Suite:` | 阿里云、华为云、腾讯云、中科大、清华 TUNA、网易、南大、华中科大 |
| Ubuntu | `/{path}/dists/noble/Release` 含 `Suite:` | 同上 8 家（另外北外、上交大也是通的，没进下拉） |
| Rocky 9 | `/{path}/9/BaseOS/x86_64/os/repodata/repomd.xml` 含 `<repomd` | 阿里云 `/rockylinux`、华为云 `/rockylinux`、腾讯云 `/rocky`、南大 `/rocky` |
| AlmaLinux 9 | 同上 | 阿里云、华为云、腾讯云、南大（都是 `/almalinux`） |

所以 kickstart 的下拉只有 4 家加「官方」：清华 TUNA、网易、中科大、华中科大**没有** Rocky/Alma 的仓库，
放进去就是必然 404/首页 HTML。**路径也要按站分别记**（`/rockylinux` 与 `/rocky` 是两家不同的布局），
代码里存的是 `family -> path` 的表，不是拼 host 猜路径。

三套模板的语义不同，别混：

- **preseed**：写 `d-i mirror/http/hostname` + `directory`，装机全程从镜像取包；
- **autoinstall**：写 `apt.primary.uri`，subiquity 装包时走镜像；选「官方」就不写这一段，
  交回 geoip 默认（通常是 archive.ubuntu.com）；
- **kickstart**：安装源是 bootimus 自己提供的 ISO 树（`inst.repo=`），镜像只在 `%post` 里改
  `dnf` 的 repo 文件 —— 而且只替换 host 一段，保留发行版自带的 `$releasever` / `$contentdir`，
  这样换小版本、仓库布局变了也不会失效。

### 为什么 Ubuntu 同时有 preseed 和 autoinstall

官方文档写「Ubuntu 使用 cloud-init」，指的是 **Ubuntu 20.04 及以后**（Subiquity/autoinstall）。
preseed 是更早的 debian-installer 路线：Ubuntu 到 20.04 为止用它，Debian 至今仍在用。
两个模板的名字因此改成带版本范围的写法，免得看起来像重复功能：

- `Debian / Ubuntu up to 20.04 (preseed)`
- `Ubuntu Server 20.04+ (autoinstall / cloud-init)`


### 加固（2026-10-24）

- **保留查询串**：以前用字符串拼接重建目录地址，签名/带 token 的链接改写后丢 query
  直接 403；现在一路用 `*url.URL`。
- **兼容单引号与绝对地址**：`href` 以前只认双引号，Apache 风格的目录页一律解析失败。
- **失败短缓存（2 分钟）**：解析失败时以前每次下载前都打三个请求（HEAD + 列目录 + HEAD）。
- **HEAD 405/501 不再当成「文件没了」**：这类服务器直接按原样放行，不做改写。

## 加固（2026-10-25）：同步机制 + 四处代码

### 同步机制（这三条是当天的头号问题）

- **`git log ... | head -30` 会杀死整个 job**：`head` 读满 30 行关管道 → `git log` 收 SIGPIPE
  → 退出 141 → 配 `set -euo pipefail` 直接整步失败。2026-10-24 上游一次推了 115 个提交，
  同步就此停摆，fork 静默落后 126 个提交，而每天的定时任务只会重复这个失败。
  现在用 `git log --oneline --max-count=30`（不产生管道），并把总条数打进日志。
- **退役不再 `reset --hard`**：见上文「退役是安全的」。顺带把写死的 `iso-url-no-suffix`
  改成从 `PATCH_ID` 变量取，为后续多补丁退役留出口子。
- **`UNKNOWN` 停手**：以前 `result != IMPLEMENTED` 就 rebase，探测器崩了也照推。现在三态
  之外的一切结果都走「判定失败 → 停手」步骤。
- `编译与测试` 从 `go test ./internal/admin/` 改成 `go build ./... && go vet ./... && go test ./...`。

### `autoinstall-generator`

- **符号改名避让上游**：上游 2026-10-24 的 PR #201 新增了
  `internal/autoinstall/render.go`（`Render(content, vars)`）与 `validate.go`（`Validate(filename, content)`）。
  同一个包里同名会直接编译失败，而 `git rebase` 是行级的、不会报冲突。现在改成
  `RenderTemplate(id, values)` / `ValidateAnswerFile(format, content)`，并已用「把上游三个文件
  塞进本包再编译」的方式预演验证：不再有 `redeclared`。
- **`select` 字段开始校验白名单**：以前 `Type: "select"` 的字段完全不校验，一个手写请求就能把
  任意单行文本写进答案文件（实测 `recipe="<script>alert(1)</script>"` → 200 且写入
  `d-i partman-auto/choose_recipe select <script>alert(1)</script>`）。现在必须命中
  `Var.Options` 里的某个 value，否则 400。
- 探测器改为**三态 + SUSPECT**：上游有 `render.go` / `validate.go` / `autoinstall-files/render`
  但**没有内置模板库**时返回 `SUSPECT`（能力互补、需人工合并），不再误报 `NEEDED`。

### `iso-url-no-suffix` / `iso-catalog-resolve` / `download-resume`

- **解析与探测走同一条链路**：`resolveLatestISOURL` 和 `probeRemoteISOFilename` 以前各自新建
  裸 `http.Client`，忽略代理配置——用户正是因为直连不到才配代理，结果下载前的两次解析仍直连，
  表现成「解析失败 → 原 URL → 404」。现在两者都接收调用方传入的 client
  （`h.resolveClient(useProxy)`，复用下载 Transport + 25s 整体超时），`/api/iso-catalog/resolve`
  也支持 `use_proxy` 字段。
- **统一 UA**：探测原本用 `bootimus/iso-download-probe`，解析用 Chrome UA。镜像站按 UA 拦人是
  已知坑，现在共用 `isoUserAgent`。
- **完成前校验字节数**：读到 `io.EOF` 就 rename 的旧逻辑在 chunked 响应（`ContentLength = -1`）
  下挡不住截断——中间代理把半条响应「正常」收尾，残缺文件就会被当成 ISO 入库。现在
  `meta.Total > 0` 时必须 `written == meta.Total`，否则保留 `.part` 并报错。

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

## Fork 新增端点在「API 参考」页的登记

上游已把这一页改成**从 `/api/openapi.json` 动态生成**：真源是 `internal/admin/openapi.go` 的
`APISections`，`web/static/app.js` 里的静态 `API_REFERENCE` 数组已被上游删除。fork 原先往静态数组里
补的四条随之失效，改为登记在 `APISections`：

| 端点 | 分类 | 说明 |
|---|---|---|
| `POST /api/iso-catalog/resolve` | Images | 下载前把带版本号的目录地址解析成当前文件名 |
| `GET /api/autoinstall-templates` | Auto-Install Files | 内置模板与变量定义 |
| `POST /api/autoinstall/render` | Auto-Install Files | 渲染并校验答案文件 |
| `GET` / `PUT` / `POST /api/proxy-config` | Settings | 下载代理配置（口令只回掩码） |

上游的 `internal/server/openapi_test.go` 会断言「`server.go` 里注册的每条路由都必须出现在
`APISections`」，所以漏登记会直接测试失败 —— 比原来靠人记得改 JSON 数组可靠，也不会再出现
「接口能用但页面上查不到」。

## 同步记录：2026-10-25 首次真同步（上游一次 +126 提交）

上游在 10-24 一次性推了 126 个提交（autoinstall 模板/格式、multi-site、wimboot、UKI、
housekeeping、安装记录、副本同步等），fork 的补丁在这个基线上全部重放。

**整条跳过的死补丁**：国内镜像站那条线（`增加四家国内镜像站` → `恢复紧凑格式` → `只保留能下载的`
→ `回退国内镜像站改动`）四个提交净变化为零，全部跳过，不占历史。

**冲突处理原则**（按“谁负责这块”划分，不做机械覆盖）：

| 区域 | 处理 |
|---|---|
| 下载链路 | 以 fork 为准（代理、`.part` 续传、停滞看门狗、字节数收口），**并叠加上游新加的 SHA-256 校验**：校验值在整份文件上算（续传时先把已有 `.part` 过一遍哈希），比对放在 `rename` 之前，不一致就删掉半成品 |
| 自动安装 | 以上游为主；fork 的内置模板生成器改名避让（`Render`→`RenderTemplate`、`Validate`→`ValidateAnswerFile`），两套能力并存（上游是“自带文件+变量渲染”，fork 是“内置四套模板生成”） |
| 存储与路由 | 上游新增的一律保留（housekeeping、install record、replica、SAN、审计日志…），fork 的 `proxy_config` 表与 `/api/proxy-config` 端点并排加上 |
| API 参考页 | 采用上游的 openapi 动态实现，fork 四个端点登记进 `APISections`（见上一节） |

**验收**：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；`node --check` 通过
（`app.js` / `i18n.js`）；YAML 全部可解析；6 个探测器自测全 PASS；
`upstream/main` 已是 `main` 的祖先（下次上游提交即可正常 rebase）。
