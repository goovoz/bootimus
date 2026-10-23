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
| `i18n-zh-full` | active | 把管理台剩余文案补成中文：`i18n.js` 增加「英文原文 → 中文」映射（当前 684 条），并在每次 DOM 变更后自动翻译（覆盖 app.js 动态渲染的内容，不改 app.js） | `web/static/i18n.js` |
| `cn-mirrors` | active | 在「获取镜像」的镜像站下拉里加入阿里云 / 华为云 / 中科大 / 清华 TUNA，按实测可用性登记 33 条 | `distro-profiles.json`、`internal/profiles/distro-profiles.json` |

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

- `SOURCE_TEXT`：684 条原文到中文的映射（专有名词与示例值除外）
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


## 国内镜像站补丁（`cn-mirrors`）

「管理 → 镜像 → 获取镜像」的镜像站下拉原来只有 Global / UK / DE。补丁在 `distro-profiles.json` 的
`mirrors` 里加国内镜像，**插入位置是每个发行版第一个镜像之后**（`mirrors[1:1]`）——前端用
`mirrors[0].base` 当默认 URL（`app.js` 的 `defaultBase`），这样默认行为不变、国内镜像排第二项。

### 教训：站存在 ≠ 能下载

第一版凭「目录存在」就登记了 33 条（阿里云 / 华为云 / 中科大 / 清华），结果在管理台点下载全部 403。
逐条查下来，是四家站在**服务端程序化访问**上的拦截，不是版本或路径问题：

| 站 | 真实返回 | 结论 |
|---|---|---|
| 阿里云 | 302 → `iso-osm.mirrors.aliyuncs.com`，该域名在部署机 80/443 全部 connection refused | 跳转目标不可达，用不了 |
| 华为云 | 418 + 3KB 拦截页（浏览器 UA 也一样） | WAF 挑战，用不了 |
| 中科大 | 200 但正文是 `Verifying - USTC Mirrors` | JS 人机验证页，用不了 |
| 清华 TUNA | 403「您访问使用的软件带有非常用软件的特征」 | 客户端指纹拦截，用不了 |

关键点：**浏览器能打开这些页面，不代表 bootimus 能下载**。判断标准只有一个——在部署机上真的取到
ISO 字节（`Range: bytes=0-1023`，且首字节不是 `<`、Content-Type 不是 HTML）。

### 现在的登记范围（逐条实测）

在部署机（`172.20.0.205`）上按「官方能提供的版本，候选必须全覆盖」筛，最后只有两家通过，且只覆盖
两个发行版：

| 发行版 | 镜像 | 结果 |
|---|---|---|
| ubuntu | `CN (NetEase)` = `mirrors.163.com/ubuntu-releases` | 4/4 版本可下 |
| ubuntu | `CN (HUST)` = `mirrors.hust.edu.cn/ubuntu-releases` | 4/4 版本可下 |
| arch | `CN (NetEase)` = `mirrors.163.com/archlinux/iso/latest` | 1/1 可下 |
| arch | `CN (HUST)` = `mirrors.hust.edu.cn/archlinux/iso/latest` | 1/1 可下 |

两家在 **Go 默认 UA**（`Go-http-client/1.1`，也就是 bootimus 实际用的）下同样返回 206，因此不需要
改客户端代码。像腾讯云、北外、南大、上交大等站也测过，分别是不支持该路径 / 403 / JS 验证页。

其余 8 个发行版目前没有可用的国内镜像，下拉里就只有官方与欧洲镜像——**宁可不显示，也不显示一个必然失败的选项**。

### 如果以后要加回来

1. 先确认那家站放行了程序化下载（换网络、加白名单、或它调整了 WAF），不能只看浏览器能打开
2. 用 `Range: bytes=0-1023` 在**部署机**上跑一遍，首字节不能是 `<`
3. 该镜像必须覆盖官方能提供的**全部**版本，否则那一行仍然会 404
4. 地区名写英文 `CN (NetEase)`，中文放 `i18n-zh-full` 的映射表（`中国（网易）`），不把中文写进数据文件

探测器：`bash .github/scripts/detect-cn-mirrors.sh --selftest`。上游一旦自己加了 4 个以上中国大陆
镜像站，每日同步会告警但不自动退役——镜像可用性必须在部署机上人工复测。

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
