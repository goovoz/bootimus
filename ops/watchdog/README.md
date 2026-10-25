# bootimus 自动更新 watchdog

盯 ghcr 上的最新构建，发现新 digest 就自动把 compose 切过去并重建容器；健康检查不过就自动回滚。

## 为什么不直接用 Watchtower

compose 里是**按 digest 固定**的（`image: ghcr.io/goovoz/bootimus@sha256:...`），
Watchtower 只认 tag，对 digest 引用不会动。所以这里自己写了个小的：
既能保持「文件里始终是可复现的 digest」，又能自动前进。

## 工作流程

```
每 15 分钟（systemd timer）
  └─ docker pull ghcr.io/goovoz/bootimus:latest
       ├─ 拿 latest 的 digest，和 compose 里固定的比
       │    └─ 一样 → 直接退出（不重启容器）
       └─ 不一样
            ├─ 有人正在装机？（默认看 8080 的 established 连接）→ 有就跳过
            ├─ 备份 compose → 改 digest → docker compose up -d
            ├─ 健康检查 http://<host>:8081/ 返回 200（最多等 60s）
            │    ├─ 通过 → 把版本串写回注释，清理旧镜像（保留最近 3 个）
            │    └─ 失败 → 还原 compose → up -d → 记入 failed-digests，本轮不再碰这个 digest
```

## 文件

| 路径（部署后） | 作用 |
|---|---|
| `/opt/bootimus/watchdog.sh` | 主脚本 |
| `/opt/bootimus/replace_image.py` | 只负责改 compose 的 image 行 + 注释 |
| `/etc/systemd/system/bootimus-watchdog.service` | oneshot 服务 |
| `/etc/systemd/system/bootimus-watchdog.timer` | 每 15 分钟触发，开机 3 分钟后首跑 |
| `/etc/default/bootimus-watchdog` | 可调参数 |
| `/var/log/bootimus-watchdog.log` | 运行日志（>5MB 自动轮转成 `.1`） |
| `/var/lib/bootimus-watchdog/` | 锁、`rollback.yml`、历史 compose 备份、`failed-digests` |
| `ghcr-audit.sh` | 只读巡检：latest 是否还在、tag 能否解析 digest、线上钉住的 digest 是否 200（不删除任何东西） |
| `bootimus-ghcr-audit.service` / `.timer` | 每天 07:30 CST 跑一次巡检 |
| `install-ghcr-audit.sh` | 巡检的安装脚本，复用 watchdog 的 Server酱 sendkey |

## 安装

在**运行容器的那台机器**上执行：

```bash
sudo bash install.sh
```

脚本会装文件、`daemon-reload`、启用 timer，并立刻跑一次（此刻应输出「已是最新」）。

## 微信推送（Server酱）

装完在 `/etc/default/bootimus-watchdog` 里填一行 `SC_SENDKEY=SCT...`（[sct.ftqq.com](https://sct.ftqq.com/) 获取），
`systemctl restart` 不需要，下一轮就生效。

推送策略——**只在真正发生变更时发**，没变更不打扰：

| 事件 | 标题 | 内容 |
|---|---|---|
| 更新成功 | `bootimus 已更新到 0.1.77+g8bedb3d` | 主机、新旧镜像 digest、健康检查耗时 |
| 健康检查失败并回滚 | `bootimus 更新已回滚` | 失败的 digest、当前仍在跑的 digest、最后一次 HTTP 码、回滚后是否正常 |
| `compose up` 失败并回滚 | `bootimus 更新失败` | 失败 digest + compose 输出尾部 5 行 |

实现走 `https://sctapi.ftqq.com/<SENDKEY>.send`（`title` 必填且 ≤32 字符，`desp` 走 Markdown）。
推送失败只写日志、退出码不变——通知永远不该影响更新结果。

手动验证密钥是否可用（不重启容器）：

```bash
SC_SENDKEY=SCT... /opt/bootimus/watchdog.sh   # 若已是最新，不会推送
# 想单测推送，直接调接口：
curl -X POST "https://sctapi.ftqq.com/SCT....send" \
     -d "title=bootimus 测试" -d "desp=收到即表示 sendkey 可用"
```

## 时区（重要）

bootimus 的定时任务、日志时间戳都按**进程本地时区**解释。容器默认是 UTC，所以 compose 里必须给：

```yaml
    environment:
      TZ: Asia/Shanghai
```

否则「每天 03:00」会在北京时间 11:00 触发。镜像自带 `zoneinfo`，设 `TZ` 即可，不用重建镜像。
宿主机本身仍可以是 UTC——容器内 `TZ` 一设，`docker logs` 与界面里的时间就是北京时间。

watchdog 自己的日志与推送也统一按 Asia/Shanghai（脚本里 `export TZ=${TZ:-Asia/Shanghai}`），
所以日志里是 `+08:00`，不会再出现 UTC 与本地时间混排。

## 常用操作

```bash
systemctl list-timers bootimus-watchdog.timer     # 下次什么时候跑
journalctl -u bootimus-watchdog.service -n 30     # systemd 侧日志
tail -f /var/log/bootimus-watchdog.log            # 脚本自己的日志

# 手动跑一次
/opt/bootimus/watchdog.sh

# 只看有没有新版本，不动容器
CHECK_ONLY=1 /opt/bootimus/watchdog.sh

# 暂停 / 恢复自动更新
systemctl disable --now bootimus-watchdog.timer
systemctl enable --now bootimus-watchdog.timer
```

## 参数（`/etc/default/bootimus-watchdog`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIP_IF_BUSY` | `1` | 装机流量上有活跃连接时跳过本轮 |
| `BUSY_PORTS` | `8080` | 判断「忙」的端口。**不要加 8081**：管理台长期开着页面会被误判成忙 |
| `KEEP_IMAGES` | `3` | 本地保留几个镜像，回滚用 |
| `HEALTH_TIMEOUT` | `60` | 等就绪的最长秒数 |
| `HEALTH_URL` | 空（自动探测） | 健康检查地址，见下 |
| `HEALTH_PORT` | `8081` | 自动探测时用的端口 |

### HEALTH_URL 为什么默认留空

iVentoy 风格的部署常见 `network_mode: host` + `--bind-interface eth0`，这时服务**只监听 eth0**，
`http://127.0.0.1:8081/` 连不上（`000`）。如果默认值写死 loopback，健康检查会把好镜像判成坏的并回滚；
写成某台机器的真实 IP，又会把内网地址带进仓库。

所以默认留空，运行时按这个顺序确定：

1. `/etc/default/bootimus-watchdog` 里显式配了 `HEALTH_URL` → 直接用，不探测
2. 否则查 `ss -Hltn` 里 `8081` 的实际监听地址：
   - `*:8081` / `0.0.0.0:8081` / `[::]:8081`（未绑网卡）→ `127.0.0.1`
   - `*%eth0:8081`（`SO_BINDTODEVICE`，`--bind-interface eth0` 就是这个形态）→ **`eth0` 的 IPv4**
   - `198.51.100.10:8081`（直接绑地址）→ 原样使用
3. 每个候选都实探一次，只接受有响应的；都不通则再扫本机全部全局 IPv4，
   最后回落 `http://127.0.0.1:8081/` 并写日志告警

> 踩过的坑：只按「通配 → loopback」判断是不够的。`ss` 对绑定网卡的监听显示成
> `*%eth0:8081`，字面上像通配，实际 loopback 连不上；而且 `code=$(curl ... || echo 000)`
> 会让失败时的状态码变成两行 `000\n000`，字符串比较永远不相等 —— 两处都已修。

要固定地址就显式写一行（推荐绑定具体网卡的部署都写上）：

```bash
HEALTH_URL=http://<本机业务IP>:8081/
```

脚本启动日志里会打印实际用的地址（`健康检查地址（显式配置）` / `（自动探测）`），排查时先看这一行。

## 回滚

自动回滚是失败时的默认行为。**人工**回滚：

```bash
cd /opt/bootimus
cp docker-compose.yml.bak-<时间戳> docker-compose.yml   # 或 /var/lib/bootimus-watchdog/rollback.yml
docker compose up -d
```

`failed-digests` 里记录的 digest 会被跳过；确认可以重试后删掉那个文件。

## 演练回滚路径

不换镜像、只强制让健康检查失败，验证回滚真的会执行：

```bash
FORCE_FAIL_HEALTH=1 /opt/bootimus/watchdog.sh; echo "退出码 $?"
# 期望：退出码 2，compose 被还原，容器跑回上一个 digest，failed-digests 里多一条
```

演练完记得清掉 `failed-digests`，否则那个好构建会被挡住：

```bash
rm -f /var/lib/bootimus-watchdog/failed-digests
```

## 已知边界

- 只跟随 `latest` 这个 tag 的 digest 变化；如果哪天发布流程不再更新 `latest`，watchdog 就看不到新构建
- 健康检查只看管理台是否 200，不校验 PXE/TFTP 服务
- 重启会让正在进行的 PXE 会话中断，所以才有 `SKIP_IF_BUSY`；UDP 的 TFTP 传输没法从 `ss` 的 established 状态看出来

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

