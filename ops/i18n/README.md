# 汉化审计：三层，缺一层就会漏

> 起因见文末「2026-11-05 漏了什么」。一句话：**静态扫描说「缺失 0 条」的时候，界面仍然是英文。**

## 三层各管一段

| 层 | 脚本 | 能看到什么 | 看不到什么 | 什么时候跑 |
|---|---|---|---|---|
| 1 静态 | `audit.py` | HTML 字面量、`<option>`、`<th>`、Go 里的表单字段文案、**后端枚举常量** | 服务端拼出来的句子、模板拼的文本 | 每次 push（CI 的 `i18n-audit` job）+ 每天 05:00（`bootimus-i18n-autofix.timer`） |
| 2 枚举 | `audit.py`（`collect_enum_values`） | `internal/models` 里所有 `*State/*Kind/*Status/*Mode` 常量 | 值以外的文案 | 同上（和静态同一趟） |
| 3 DOM | `audit-dom.js` + `autofix-dom.sh` | 真实渲染后的文本节点：徽章、服务端句子、`${a} of ${b}` 拼装 | 需要权限/数据才出现的页面（已用预置数据部分解决） | 每天 05:40（`bootimus-i18n-dom-audit.timer`） |

## 为什么要有第 2 层

`app.js` 把后端字段直接塞进徽章：

```js
return `<span class="badge ${cls}">${escapeHtml(state)}</span>`;   // ready / pending / deploy
```

`ready`、`pending`、`deploy` 这些值在 `web/static` 里**一个字都没有**（它们来自 `internal/models` 的常量），
静态扫描永远扫不到；而 DOM 扫描要看到它们，还得先让那张表有数据。两条都断，就漏了。
枚举层把「后端加了状态、UI 就会多一处英文」这件事变成 CI 能拦住的契约。

## 手动跑

```bash
python3 ops/i18n/audit.py              # 第 1+2 层，秒级，缺什么直接列出来
bash ops/i18n/autofix.sh              # 第 1 层缺失 → 翻译 → 复查 → 提交（要走 LLM）
bash ops/i18n/autofix-dom.sh --dry-run # 第 3 层：起临时实例 + Playwright 扫全部 tab，只报告不入队
bash ops/i18n/autofix-dom.sh          # 第 3 层：扫出来交给 DSH（入队 i18n-gap）
bash ops/i18n/rebuild.sh              # zh.json 改完必须重建 web/static/i18n.js
```

第 3 层起的临时实例用 18081/18080/1069 端口，跑完会关掉；它只写 `/tmp/dom-new.json` 和队列，不动仓库。

## 加词条的规矩

- `ops/i18n/zh.json` **只增不覆盖**：老 key 就算暂时扫不到也别删（`stale` 只提示，不自动清理）。
- 值是按小写原文配的：徽章里的 `READY` 只是 CSS `text-transform`，DOM 文本是 `ready`。
- `deploy`/`Deploy` 是两个不同 key（作业列的值 vs 按钮文案），别合并。
- 原生 `confirm()/alert()` 走 `apply_engine_extras.py` 的 `NATIVE_TEXT_PATTERNS`（浏览器自己画的框，DOM 扫描摸不到）。

## 2026-11-05 漏了什么

磁盘镜像页：镜像行 `Deploy` 按钮、作业列 `deploy/capture`、状态列 `READY/PENDING/COMPLETED` 全是英文，
而当天 05:00 的定时审计报的是「缺失 0 条」。

1. 静态层：这些值不是字面量，扫不到。
2. DOM 层：`autofix-dom.sh` 只预置了 3 个 client，磁盘镜像页是空表 → 徽章根本没渲染。
3. 调度层：`autofix-dom.sh` 当时**没有**定时器，只有人手动跑。

对应修复：`audit.py` 加枚举层；`autofix-dom.sh` 预置镜像/任务数据；新增 `bootimus-i18n-dom-audit.timer` 每天 05:40 自动跑并自动入队。

