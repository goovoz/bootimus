# ops/dsh —— 把「需要判断的事」交给 DSH

这个目录是 bootimus 和本机 DSH（DeepSeekRuntime）之间的交接层。
**它替代的是原来散在三个脚本里的「调本地 AI 网关问一句」**：

| 原来的调用点 | 原来干什么 | 现在 |
|---|---|---|
| `ops/watchdog/sync-watchdog.sh` → `llm_advise()` | 整份 CI 日志 → `CAUSE/FIX/HUMAN` 三行文案 | 落盘日志 + 入队 `sync-failure` |
| `ops/watchdog/auto-resolve-sync.sh` → `ask_ai()` | 逐冲突块问合并方案 → 写回 → 编译 → 推 | 拆成 `--collect/--fix-trailers/--verify/--publish` 四个确定性动作，由 DSH 串 |
| `ops/i18n/_common.sh` → `llm_call()` | 批量翻译 → 写 `zh.json` → 复查 → 推 | 审计后入队 `i18n-gap`，翻译和复查都由 DSH 做 |

**为什么换**：网关调用是一次性问答 —— 看不到仓库、跑不了命令、改不动文件、没有记忆。
结果就是归因只能写进通知、冲突只能逐块猜、翻译只能瞎猜语境。DSH 跑在本机、连着 101
（`ssh root@172.20.0.101`），有完整的工具和技能，能真的把事做完再验证。

**换了以后 101 上不再需要 `AI_BASE_URL` / `AI_API_KEY`**，`/root/ai_gateway.env` 可以删。

---

## 链路

```
┌─ 101 (Ubuntu 24.04) ────────────────────────────────────────────────┐
│                                                                     │
│  bootimus-sync-watch.timer (30 min)                                 │
│    └─ sync-watchdog.sh                                              │
│         发现 / 规则归因 / 重试瞬时失败 / 通知        ← 纯确定性       │
│         needs_ai=true → dsh-handoff.sh enqueue                       │
│                                                                     │
│  bootimus-i18n-autofix.timer (每天) / -dom.timer                    │
│    └─ autofix*.sh                                                   │
│         审计（静态 / 真实 DOM）                     ← 纯确定性       │
│         有缺失 → dsh-handoff.sh enqueue                             │
│                                                                     │
│  /var/lib/bootimus-dsh/tasks/<id>.json     ← 唯一的交接面            │
│  /var/lib/bootimus-dsh/evidence/           ← CI 日志、冲突现场…      │
│  /root/work/bootimus                       ← DSH 动手的唯一工作副本  │
└─────────────────────────────────────────────────────────────────────┘
                              ▲   ssh（claim / report / git / go build）
                              │
┌─ 本机 Windows ──────────────────────────────────────────────────────┐
│  DSH（工作区 bootimus）                                              │
│    claim → 读证据 → 在 101 上 rebase / 改文件 / 验证 → report        │
│    技能：~/.dsh/skills/bootimus-ops/SKILL.md                        │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 装

101 上（一次就够）：

```bash
cd /root/work/bootimus
sudo bash ops/watchdog/install-sync-watchdog.sh            # watchdog + 队列客户端
systemctl list-timers bootimus-sync-watch.timer --no-pager
```

i18n 两个 timer 是另外装的（`/etc/systemd/system/bootimus-i18n-autofix*.service`），
本次改动只换了它们里面的脚本逻辑，unit 不用动。它们引用了
`EnvironmentFile=-/root/ai_gateway.env`，删掉那个文件后 `-` 前缀会让 systemd 忽略缺失，
不影响启动。

---

## 用（DSH 侧）

1. 在本机 DSH 里打开 **bootimus** 工作区。
2. 说「看下队列 / 有活了」或者让它自己按定时器检查。
3. DSH 会：

```bash
H=/root/work/bootimus/ops/dsh/dsh-handoff.sh
ssh root@172.20.0.101 "bash $H list --status queued"
ssh root@172.20.0.101 "bash $H claim --kind sync-failure"     # 拿任务 + 证据路径
# …读证据、在 101 上改、跑 --verify…
ssh root@172.20.0.101 "bash $H report <id> --status done --message '…' --commit <sha>"
```

完整手册见 [HANDOFF-PROTOCOL.md](HANDOFF-PROTOCOL.md)。

---

## 回滚

改动全在这几个文件里，`git revert` 一个提交就能回到「脚本自己问网关」：
`ops/watchdog/sync-watchdog.sh`、`ops/watchdog/auto-resolve-sync.sh`、
`ops/i18n/_common.sh`、`ops/i18n/autofix.sh`、`ops/i18n/autofix-dom.sh`。
队列目录 `/var/lib/bootimus-dsh` 删掉即可，不影响其它东西。
