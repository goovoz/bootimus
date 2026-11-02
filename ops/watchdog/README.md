# bootimus 运维巡检

本目录两套**只读**巡检，都不碰 docker、不改 compose：

| 脚本 | 干什么 |
|---|---|
| `ghcr-audit.sh` | 每天问 registry「现在有哪些 tag、指向哪个 digest」，跟昨天比；`latest` 不见了才告警 |
| `sync-watchdog.sh` | 盯 `sync-upstream` 是否失败，按失败步骤归因；瞬时故障自动重试，其余交给 AI / 人 |

> 早先还有一个「自动更新镜像」的 watchdog（按 `latest` 的 digest 变化拉镜像重启容器）。
> 那个方案已经不用了：现在由 `sync-upstream` 同步成功后自动触发构建，生产端按需手动拉取。
> 对应的 `watchdog.sh` / `replace_image.py` / `install.sh` / `bootimus-watchdog.*` / `default.env`
> 已从仓库删除。

## ghcr 标签巡检（只读）

**它解决的是「看着像丢了」这类困惑。** GitHub 的 Packages 页面偶尔会渲染失败：包页里那段版本列表是懒加载的，
接口一抖就显示空白或 "Sorry, something went wrong"，`/users/<user>/packages/container/<pkg>` 这个地址也曾
连续返回 500。镜像其实还在，但页面上看不到，很容易以为镜像被清空了。

巡检干的事很简单：每天直接问 registry「现在有哪些 tag、各自指向哪个 digest」，存成快照，跟昨天比：

| 情况 | 动作 |
|---|---|
| tag 集合没变 | 只写日志，不打扰 |
| 某些 tag 消失 | **不告警**——保留策略（`ghcr-retention.yml`）本来就是只留 latest 指向的那一个，旧 tag 被删是预期 |
| `latest` 不见了 | 推送告警（Server酱）：保留策略不会删它 |
| 某 tag 拿不到 digest / compose 里钉住的 digest 返回 404 | 推送告警 |
| 拿不到 token、tags 列表为空 | 只记日志、退出码 2，**不告警**（区分「API 抖动」和「真被删」） |

因为包是 public，巡检用匿名 token，**不需要 PAT**，也不碰 docker，权限只有读。

### 一个必须知道的坑：并发的假告警

第一次上线时，systemd 刚 `enable --now` 就立刻跑了一次，我同时又手动跑了一次。两个进程抢同一个临时文件，
后启动的把文件清空，前一个的比对就拿到空集，于是把**全部 tag 都判成「消失」**，发了一条误报推送。

现在脚本里有两道防：
- `flock` 单实例（`/var/lib/bootimus-watchdog/ghcr-audit.lock`），撞车时后来者直接退出
- 每次用 `mktemp` 建独立临时文件，比对前 `sort -u` 去重，且结果为空时**不做判定**

### 安装与验证

```bash
SRC_DIR=/tmp/wd bash install-ghcr-audit.sh     # 装脚本 + 两个 unit，生成 /etc/default/bootimus-ghcr-audit
bash /opt/bootimus/ghcr-audit.sh               # 首次：建快照
bash /opt/bootimus/ghcr-audit.sh               # 再来一次：应输出「无变化」，退出码 0
systemctl list-timers bootimus-ghcr-audit.timer
```

日志 `/var/log/ghcr-audit.log`；快照与变更历史在 `/var/lib/bootimus-watchdog/ghcr-tags.txt` 和 `ghcr-changes.log`。

## 镜像保留策略（跑在 Actions，不在本机）

**每个版本号只留最新一个 digest**，`latest` 指向版本号最高的那个。`.github/workflows/ghcr-retention.yml`：

- 触发：`Release (fork)` 跑完之后 / 每天 05:23 CST / 手动（可 `dry_run`、可 `keep_digest`）
- 保留：每个版本号（从 `0.1.77`、`0.1.77-ga2327e1` 这类 tag 里解析）→ `updated_at` 最新的那个 index，
  **加上它的两个平台子清单**；`latest` 若指向别处，会重打 tag 指回「最高版本号的最新构建」
- 删除：同版本号内的其它构建、以及它们各自的子清单
- 跳过：15 分钟内更新过的版本（避免和正在进行的构建抢）
- 兜底：一个版本号都解析不出来就中止，绝不在「不知道留谁」的情况下删东西

### 为什么 Packages 页面总有 2 个 untagged

多架构镜像是 **一个 index + 每平台一个 manifest**（linux/amd64、linux/arm64）。在 GitHub 的
Packages API 里这三个是**三个独立版本**，而 buildx 只给 index 打 tag，所以两个平台清单永远显示
untagged。换句话说：

```
4 tagged   = latest / 0.1.77 / 0.1.77-g<sha> / sha-<sha>   （都指向同一个 index）
2 untagged = 该 index 的 amd64、arm64 清单
```

**它们不能删**——删了 index 就拉不下来。保留 2 个版本号时就会看到 4 个 untagged，属正常。

### 回滚

旧 digest 会被删掉，所以**不能提前很久钉一个中间构建来回滚**。要回滚时临时处理：

- 跨版本：旧版本号的最新 digest 还在，直接把它 `keep_digest` 保住 / 或临时钉回 compose；
- 同版本内的某个中间构建：已经被删了，需要重新构建那个 commit（或 dispatch 一次带
  `keep_digest` 的保留策略，把目标 digest 排除，前提是它还没被删）。

另外 `fork-release.yml` **不再创建 GitHub Release**：二进制只作为 Actions 构建产物（Artifacts，
保留 90 天）上传，镜像 digest 写在每次运行的 Job Summary 里。Releases 页面因此不再堆积构建记录。

## 维护提示（本仓库特有）

`.gitignore` 里有 `bootimus-*`，按**文件名**匹配任意层级。所以：

- 这个目录叫 `ops/watchdog/` 而不是 `ops/bootimus-watchdog/`（目录名带 `bootimus-` 会被整目录忽略）
- `bootimus-watchdog.service` / `.timer` 两个文件名仍然命中该规则，是用 `git add -f` 强制纳入的；
  以后**新增**同类文件要记得 `git add -f`，否则 `git add` 会静默跳过



---

## 上游同步 watchdog（sync-watchdog）

监控 `.github/workflows/sync-upstream.yml` 的健康度。**为什么需要它**：同步失败时信号只在
GitHub 页面上，没人看就没人知道 —— 2026-10-27 就吃过一次，8 个提交漏了 `Fork-Patch` trailer，
同步在校验阶段停手，一直卡在「落后上游 18 个提交」直到人工发现。

### 它做什么

每 30 分钟跑一次，按顺序：

1. 拉最近 10 次 sync-upstream 运行，判定 `OK` / `RUNNING` / `STALE` / `FAILED`
2. FAILED 时**按失败步骤归因**：`TRAILER`（缺标记）/ `CONFLICT`（重放冲突）/
   `DETECT_UNKNOWN` / `PERMISSION`（SYNC_TOKEN）/ `TEST_FAIL` / `TRANSIENT`
3. **只有 TRANSIENT 会自动重新 dispatch**（当日上限 `MAX_RETRY_PER_DAY`，默认 2 次）
4. 归因结果写进 `/var/lib/bootimus-sync-watch/state.json`
5. 同一故障只通知一次（按 `健康/类型/run_id` 去重）
6. 判不出来的（`needs_ai: true`）**入队交给本机的 DSH**，由它真去改仓库（见下节）

### 为什么自愈这么保守

改提交历史、解冲突、动探测器都要判断，脚本硬干容易把小故障放成大故障。所以脚本负责
「发现 + 归因 + 通知 + 重试瞬时失败」，判断类的事交给 DSH 或人。

### 交班：为什么是 DSH 而不是 AI 网关（2026-11 改）

早先失败归因是调本地 AI 网关（`http://172.20.0.9:7863/v1`）问一句「这日志为什么失败」，
拿回 `CAUSE / FIX / HUMAN` 三行写进通知。那套东西的价值只到「多一段人话」，因为：

- 一次性问答，模型**看不到仓库**，也跑不了任何命令，改不动任何文件；
- 归因结果没人用 —— 写进通知里，人不看就等于没做；
- 冲突解决更进一步：`auto-resolve-sync.sh` 当时逐冲突块问网关，一次一块地「猜」，
  猜错就整体回滚，而且它把「怎么改」和「改完能不能过」绑死在同一条流程里。

现在改成**交接队列**：定时任务只负责把证据打齐（完整 CI 日志、失败步骤、落后多少、
冲突现场），入队到 `/var/lib/bootimus-dsh`；本机跑着的 DSH 领走任务，在
`/root/work/bootimus` 上自己 rebase、自己改文件、跑 `go build` / `go test`、
自己 `--force-with-lease` 推送，最后把结论写回任务。

协议、生命周期、安全边界都在 **[`ops/dsh/`](../dsh/README.md)**。要点：

- 脚本**不再调用任何模型**，也不需要 `/root/ai_gateway.env`；
- 幂等：同一个 `run_id` 只入队一次，30 分钟一轮不会堆任务；
- 租约：DSH 半路挂掉后 90 分钟租约过期，任务可被重新领取；
- 保守性没变：能自动做的仍然只有「重试瞬时失败」，语义判断一律经 DSH 的手、并且
  要过编译和测试才允许推送。

### 安装

```bash
sudo bash ops/watchdog/install-sync-watchdog.sh            # 安装并启用
sudo bash ops/watchdog/install-sync-watchdog.sh --dry-run  # 只看计划
sudo bash ops/watchdog/install-sync-watchdog.sh --uninstall
```

PAT 来源：`$GH_TOKEN` > `/root/gh_pat.txt` > 已有配置。装完配置落在
`/etc/default/bootimus-sync-watch`（权限 600，含密钥）。安装脚本会顺带把队列客户端
装成 `/usr/local/sbin/bootimus-dsh-handoff`，并把老的 `AI_*` 网关参数注释掉。

### 手工排查

```bash
# 看最近一次判定
cat /var/lib/bootimus-sync-watch/state.json | jq

# 立刻跑一次（CHECK_ONLY 不产生动作，也不入队）
CHECK_ONLY=1 sudo -E /usr/local/sbin/bootimus-sync-watch

# 拿历史某次失败演练归因（不会真的重试）
sudo -E RUN_ID_OVERRIDE=40418007912 CHECK_ONLY=1 /usr/local/sbin/bootimus-sync-watch

# 看队列里有什么
sudo bash ops/dsh/dsh-handoff.sh list
sudo bash ops/dsh/dsh-handoff.sh status

# 看定时器
systemctl list-timers bootimus-sync-watch.timer --no-pager
```

退出码：`0` 正常/进行中；`1` 需要 DSH 或人工；`2` 已自动重试；`3` 脚本自身出错。
