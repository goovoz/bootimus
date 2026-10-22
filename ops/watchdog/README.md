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
| `HEALTH_URL` | `http://<本机IP>:8081/` | 健康检查地址 |

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

## 维护提示（本仓库特有）

`.gitignore` 里有 `bootimus-*`，按**文件名**匹配任意层级。所以：

- 这个目录叫 `ops/watchdog/` 而不是 `ops/bootimus-watchdog/`（目录名带 `bootimus-` 会被整目录忽略）
- `bootimus-watchdog.service` / `.timer` 两个文件名仍然命中该规则，是用 `git add -f` 强制纳入的；
  以后**新增**同类文件要记得 `git add -f`，否则 `git add` 会静默跳过

