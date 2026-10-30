# fork 同步冲突怎么处理

上游改了同一个文件时，`sync-upstream` 的 rebase 会停住。workflow 会把冲突标记
原样提交到 `sync-conflict/<时间>` 分支并失败，**不会自己猜**——这是对的，
语义冲突不该由脚本赌。

本文记录已经遇到过的冲突类型和对应解法。新的类型往下加。

## 先看现状

```bash
# 落后多少
git fetch upstream
git rev-list --count HEAD..upstream/main

# 失败在哪一步（含 LLM 归因）
tail -30 /var/log/bootimus-sync-watch.log
jq '{health,behind_upstream,needs_ai}' /var/lib/bootimus-sync-watch/state.json
```

## 处理流程

```bash
cd /root/work/bootimus
git checkout -b sync/$(date +%Y%m%d-%H%M) main
git rebase upstream/main          # 停在冲突处
# 解冲突（见下）
git add <冲突文件>
GIT_EDITOR=true git rebase --continue
go build ./... && go test ./internal/admin/ ./internal/autoinstall/
git push --force-with-lease origin HEAD:main
```

`--force-with-lease` 必须有：rebase 会重写 fork 提交的 SHA。

## 已遇到的冲突类型

### 1. 两边各自新增，位置撞在一起

`internal/server/server.go`（2026-10-30）：

```go
<<<<<<< HEAD
        adminHandler.BootAuthReady = s.AdminTLSEnabled
        mux.HandleFunc("/boot-auth", s.handleBootAuthentication)
=======
        // [bootimus-fork L1] fork-patch: download-resume
        adminHandler.CleanupStaleDownloads()
>>>>>>> c34fd61
```

**解法**：两段都保留，上游在前、fork 在后。这类没有语义冲突，只是 git 不知道顺序。

### 2. 上游改了描述，fork 在旁边加了新行

`internal/admin/openapi.go`（2026-10-30）：

- 上游把 `/api/theme` 的 Summary 改了（加了 boot_password 说明）
- fork 在同一位置新增了 `/api/proxy-config` 的 GET/PUT 两行

**解法**：保留 fork 的两行新端点，`/api/theme` 用**上游的新描述**。
注意别把 fork 侧那行旧的 `/api/theme` 也留下来 —— 会变成重复端点。

## 解完要检查的

```bash
# 1. 标记还在不在（每个补丁都要有）
git log --format='%h %s' upstream/main..HEAD | head -20
git log -1 --format='%B' <sha> | grep -E '^Fork-(Patch|Meta):'

# 2. 编译 + 关键包测试
go build ./...
go test ./internal/admin/ ./internal/autoinstall/ ./internal/server/

# 3. 汉化有没有被上游新增文案打破
python3 ops/i18n/audit.py        # 应为「缺失 0 条」

# 4. 同步后确认
git rev-list --count HEAD..upstream/main   # 应为 0
```

## 告警怎么来的

`bootimus-sync-watch.timer` 每 30 分钟查一次。判定为失败且需要人处理时：

- 有 `SC_SENDKEY` → Server酱推送
- **没有 → 自动开 GitHub Issue（label `sync-failure`）**，已有未关闭的就追加评论
- 同步恢复后自动关掉这些 issue

`SC_SENDKEY` 在 `/etc/default/bootimus-sync-watch` 里，目前是注释状态（模板占位符），
所以实际走的是 Issue 通道。
