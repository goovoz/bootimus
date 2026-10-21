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
