# 交接协议（bootimus ⇄ DSH）

> 一句话：**101 上的定时任务只负责发现和打包证据，判断和动手在 DSH 那边。**
> 两边靠 `/var/lib/bootimus-dsh` 里的 JSON 任务交接，不靠任何 HTTP 模型调用。

---

## 1. 为什么不再让脚本问模型

旧方案是脚本里 `curl` 本地 AI 网关（`http://172.20.0.9:7863/v1`）要一段文案 / 一段合并结果。
三个硬伤：

| 硬伤 | 具体表现 |
|---|---|
| 看不到仓库 | 只喂日志或冲突块，模型不知道 `[bootimus-fork L1]` 锚点的含义，也不知道上游那次提交为什么改 |
| 改不动东西 | 归因结果只能写进通知；冲突合并只能吐代码片段，由脚本替它拼接、编译、回滚 |
| 没有记忆 | 每次都是零上下文，同一个坑下个月再踩一遍还是重新推一遍 |

DSH 有 read / grep / glob / edit / ssh / 定时器，跑在本机、连着 101，
能读整棵树、能跑 `go build`、能提交推送、还能把教训写进技能文档。所以：
**把「判断」从脚本里搬走，脚本只留确定性动作。**

---

## 2. 目录与组件

在 101 上：

```
/var/lib/bootimus-dsh/
  tasks/<id>.json          任务本体（status 即生命周期）
  evidence/                证据包
    ci-<run_id>.log          失败那次的完整 CI 日志（已去时间戳 / ANSI）
    conflict-<ts>/           冲突现场（冲突文件全文 + base 版 + upstream 新增提交）
    dom-new-latest.json      DOM 侧待判断的英文清单
  .lock                    flock，写操作串行化

/root/work/bootimus/       唯一的工作副本（有 upstream remote）
ops/dsh/dsh-handoff.sh     队列客户端（enqueue / list / claim / report）
ops/watchdog/sync-watchdog.sh      发现 + 归因 + 重试 + 入队
ops/watchdog/auto-resolve-sync.sh  工具箱：--collect / --fix-trailers / --verify / --publish
ops/i18n/autofix*.sh       审计 + 入队（不再翻译）
```

本机（Windows）侧：

```
C:\Users\hhh\Documents\bootimus\   DSH 工作区 bootimus（只读镜像 + 说明）
~/.dsh/skills/bootimus-ops/        技能：怎么领任务、怎么收尾
```

---

## 3. 任务格式

```json
{
  "id": "20261102T014023-sync-failure-23dc1b50",
  "kind": "sync-failure",
  "fingerprint": "sync-failure/41142094920",
  "priority": 1,
  "status": "queued",
  "created_at": "2026-11-02 01:40:23",
  "attempts": 0,
  "claimed_by": null,
  "claimed_at": null,
  "lease_until": null,
  "result": null,

  "signal": {
    "health": "FAILED",
    "failure_kind": "TRAILER",
    "step": "校验提交标记并判定补丁是否仍必要",
    "detail": "有提交没带 Fork-Patch / Fork-Meta 标记，同步在校验阶段停手",
    "behind_upstream": 0,
    "trailer_offenders": "",
    "last_success_at": ""
  },
  "run": { "id": "41142094920", "sha": "0ee1881", "url": "https://github.com/…" },
  "repo": {
    "workdir": "/root/work/bootimus",
    "repo": "goovoz/bootimus",
    "upstream": "garybowers/bootimus",
    "branch": "main"
  },
  "evidence": {
    "ci_log": "/var/lib/bootimus-dsh/evidence/ci-41142094920.log",
    "state_json": "/var/lib/bootimus-sync-watch/state.json",
    "watchdog_log": "/var/log/bootimus-sync-watch.log"
  },
  "playbook": "ops/dsh/HANDOFF-PROTOCOL.md#sync-failure"
}
```

字段约定：

| 字段 | 谁写 | 说明 |
|---|---|---|
| `id` | enqueue | `<UTC 时间戳>-<kind>-<指纹前 8 位>` |
| `fingerprint` | 入队方 | 幂等键。**同指纹未结束就不重复入队** |
| `priority` | 入队方 | 1 最高。sync-failure=1，i18n-gap=3/4，冲突=1 |
| `status` | 双方 | `queued` → `claimed` → `done` / `failed` |
| `signal` | 入队方 | 结构化信号，DSH **不要只信它**，以证据文件为准 |
| `evidence` | 入队方 | 绝对路径，DSH 用 ssh 直接取 |
| `result` | DSH | `{status, message, commit, branch, detail, at}` |

`evidence` 里的路径都是 101 上的路径。DSH 从本机读它们走：
`ssh root@172.20.0.101 "sed -n '1,200p' <路径>"`。

---

## 4. 生命周期

```
                 enqueue                 claim                    report --status done
  （事件发生） ──────────> queued ──────────────> claimed ─────────────────────────> done
                                                     │
                                                     └── report --status failed ──> failed
                                                     │
                                          租约过期（默认 90 分钟）
                                                     ↓
                                         list --stale 可见 → 可被重新 claim（attempts+1）
```

规则：

1. **幂等**：`enqueue` 发现同 `fingerprint` 已有 `queued` / `claimed` 任务时，返回
   `SKIP <已有 id>`，不新建。同步恢复（`done`）之后再来同样的故障，才会开新任务。
2. **租约**：`claim` 写 `lease_until = now + TTL`（默认 5400 秒）。DSH 崩了不会把任务
   永久卡住 —— 过期后 `list --stale` 能看到，`claim` 也能重新领。
3. **重试**：`attempts` 每次 claim 加 1。`attempts ≥ 3` 还没成的事，别再自动干，
   直接 `report --status failed` 写清卡在哪，交人。
4. **收尾必须 report**：不 report 的任务停在 `claimed`，下一轮 watchdog 看到
   `behind_upstream` 还是那么多、`health` 还是 FAILED，会再开一个新任务（指纹变了），
   队列就脏了。

---

## 5. 任务类型与处理手册

### sync-failure

**触发**：`sync-watchdog.sh` 判定 `health=FAILED` 且 `failure.kind` 不是 `TRANSIENT`。

**先看什么**（按顺序）：

```bash
# 1. 归因（规则给的，可能不准，但方向通常对）
ssh root@172.20.0.101 "jq . /var/lib/bootimus-sync-watch/state.json"

# 2. 完整 CI 日志（这才是证据）
ssh root@172.20.0.101 "grep -n '##\[error\]\|::error::\|^# ' /var/lib/bootimus-dsh/evidence/ci-<run_id>.log | head -40"

# 3. 仓库现状
ssh root@172.20.0.101 "cd /root/work/bootimus && git fetch -q upstream && git log --oneline -5 && git rev-list --count HEAD..upstream/main"
```

**按 `failure_kind` 分路**：

| kind | 意思 | 怎么办 |
|---|---|---|
| `TRAILER` | 有提交缺 `Fork-Patch:` / `Fork-Meta:` trailer | `auto-resolve-sync.sh --fix-trailers` 补标记 → `--verify` → `--publish`。补完要复查 `git log` 里 slug 是否合理（自动 slug 是 `type-scope`，取不出来才用 sha） |
| `CONFLICT` | 补丁重放冲突 | `--collect` 出冲突现场 → 逐个文件判断上游意图 → 手工合并 → `--verify` → `--publish`。**不许**用 `-X ours` / `-X theirs` 一把梭 |
| `DETECT_UNKNOWN` | 探测器判不出补丁是否还需要 | 看 `docs/FORK-PATCHES.md` 的探测器逻辑，判断上游是否已经实现了该补丁；已实现就退役该补丁，没实现就修探测器 |
| `PERMISSION` | 推送被拒 | 查 `SYNC_TOKEN` 是否过期 / 有没有 `contents: write`。这类改不了代码，直接 `report --status failed` 说清 |
| `TEST_FAIL` | 重放后编译/测试不过 | 先在本地跑 `--verify` 复现，再修代码 |
| `TRANSIENT` | 看不出失败步骤 | watchdog 已经自动重试过 2 次；还没好就查是不是 Actions 侧故障，`report failed` |

**收尾**：

```bash
ssh root@172.20.0.101 "cd /root/work/bootimus && bash ops/dsh/dsh-handoff.sh report <id> \
  --status done --message '补了 8 个 Fork-Patch trailer，推送后 sync-upstream 已重跑' \
  --commit $(git rev-parse --short HEAD) --branch main"
```

### sync-stale

`health=STALE`（超过 `STALE_HOURS` 没有一次成功）。多半是 workflow 被禁用、cron 挂了、
或者仓库改名。先查 Actions 页面和 `.github/workflows/sync-upstream.yml` 的
`on:` 触发器，不要急着重跑。

### conflict-replay

和 `CONFLICT` 同源。当 `auto-resolve-sync.sh --collect` 报 `rebase_clean=true` 时，
说明上游已经把补丁合并了 —— 这时该做的是**退役补丁**，不是解冲突。

### i18n-gap

**触发**：`ops/i18n/autofix.sh`（静态）或 `autofix-dom.sh`（真实 DOM）审出缺失并入队。

两个 scope 不一样：

- `scope=static`：`web/static` 里的字面量 + `generate.go` 模板字段的 `Label` / `Help`。
- `scope=dom`：起一个临时实例扫真实渲染结果，能抓到服务端返回的句子。
  `evidence.dom_list_file` 是完整清单，`evidence.dom_list` 只放了前 300 条。

**必须遵守的翻译规则**（原样保留，别自己改口径）：

1. 技术标识不翻：`PXE` `iPXE` `UEFI` `BIOS` `NFS` `SMB` `TFTP` `HTTP` `API` `JSON`
   `MAC` `IP` `URL` `ISO` `DHCP` `Kubernetes` `Webhook` `netboot` `Clonezilla` `memdisk` `initrd`
2. 产品名不翻：`Bootimus` `Docker Hub` `GitHub` `Redfish` `Intel AMT`
3. 术语统一：image→镜像，client→客户端，boot→启动，bootloader→引导加载器，
   profile→配置，group→分组，token→令牌，audit→审计，housekeeping→日常维护，
   disk image→磁盘镜像，enrollment→纳管
4. **不该翻的**：发行版名（AlmaLinux / Debian / Arch Linux）、发行版标识（`alma, almalinux` /
   `debian, truenas`）、内核命令行参数（`root=live`、`modules=loop,squashfs`）、文件名（`ipxe.efi`）、
   纯数字+单位（`1 MB`）、版本号（`v0.1.79`）、带国旗的语言选项
5. **变量句**（`Showing 4 of 4`、`3 removed`）：数字是运行时填的，写进词表永远匹配不到，
   只能改代码用占位符 —— 这类**不要**写 `zh.json`

**落盘**：`ops/i18n/zh.json`，**只新增不覆盖**（人工校对过的条目不许被自动流程改掉）。
写完必须跑：

```bash
bash ops/i18n/rebuild.sh          # 从 zh.json 生成 web/static/i18n.js
node --check web/static/i18n.js
python3 ops/i18n/audit.py         # 要看到「缺失 0 条」
CGO_ENABLED=0 go build ./...
git add ops/i18n/zh.json web/static/i18n.js
git commit -m "i18n: 补 N 条界面文案" -m "Fork-Patch: i18n-zh-full"
git push origin HEAD:main         # push 会触发构建
```

`/tmp/autofix-dom.sh` 那条路径还会 `kill` 掉 `bm-` 开头的进程，注意别在别的测试跑着的时候执行。

---

## 6. 安全边界

**允许**：

- 在 `/root/work/bootimus` 里改文件、rebase、commit；
- 跑 `go build` / `go test` / `audit.py` / `rebuild.sh`；
- 通过 `auto-resolve-sync.sh --publish` 推送（它带 `--force-with-lease`）；
- 改本文件、`ops/dsh/*`、`ops/watchdog/*`、`ops/i18n/*` 的脚本本身。

**禁止**：

- 在 Windows 本机编译 / 构建 / 跑测试 —— 本机只读、只规划；
- `git push --force`（必须 `--force-with-lease`，基线写死 `origin/main`）；
- 用 `-X ours` / `-X theirs` / `git checkout --theirs` 批量消冲突；
- 改 `zh.json` 里已有的条目（只能新增）；
- 在没有跑通 `--verify` 的情况下 `--publish`；
- `cat /root/.github_pat`（要看长度用 `wc -c`）；
- 把队列任务里的证据文件删掉 —— `dsh-handoff.sh gc` 自己会清 14 天前的。

---

## 7. 命令行速查

在 101 上（本机通过 ssh 调同一份脚本）：

```bash
H=/root/work/bootimus/ops/dsh/dsh-handoff.sh

bash $H status                 # 队列计数
bash $H list                   # 全量
bash $H list --status queued   # 待领
bash $H list --stale           # 租约过期、可重领
bash $H show <id>              # 任务全文
bash $H claim --kind sync-failure            # 领最老的一个
bash $H claim --id <id> --ttl 7200           # 指定 + 自定义租约
bash $H report <id> --status done --message '…' --commit abc1234
bash $H gc --keep-days 14
```

```bash
# 冲突工具箱（在 /root/work/bootimus 里）
bash ops/watchdog/auto-resolve-sync.sh --collect        # 出证据，仓库保持干净
bash ops/watchdog/auto-resolve-sync.sh --fix-trailers   # 补 trailer（改写历史，可回滚）
bash ops/watchdog/auto-resolve-sync.sh --verify         # 闸门：格式/编译/测试/审计/标记
bash ops/watchdog/auto-resolve-sync.sh --publish        # force-with-lease 推 main
DRY_RUN=1 bash ops/watchdog/auto-resolve-sync.sh --publish   # 只看会推什么
```

---

## 8. 排障

| 现象 | 查什么 |
|---|---|
| 队列一直是 `queued` | DSH 没在跑 / 没在 bootimus 工作区 / 没人叫它看队列 |
| 任务 `claimed` 不动了 | 租约过期后 `list --stale`，再 `claim --id` 重领；先确认上一个人干到哪了（`git status`） |
| `enqueue` 报 `SKIP`，但确实想重开 | 同指纹还没结束。先 `report failed` 结掉旧的，或换指纹 |
| `--verify` 只报「汉化有缺失」 | 不阻塞同步，会另开 `i18n-gap` 任务 |
| `--publish` 报 lease 失败 | 有人推过了。`git fetch origin && git rebase origin/main` 再看 |
| watchdog 日志 `没有 … 跳过交班` | `DSH_HANDOFF` 路径不对，或仓库被挪了 |
