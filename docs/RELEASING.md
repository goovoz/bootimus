# 发布流程（fork）

本仓库的发布由 fork 自己负责，与上游 `garybowers/bootimus` 的发布互不影响。

## 产物

| 产物 | 位置 | 说明 |
|---|---|---|
| 4 个平台二进制 | GitHub Release 资产 | `bootimus-linux-{amd64,arm64}`、`bootimus-windows-{amd64,arm64}`，与上游命名一致 |
| 校验和 | Release 资产 `SHA256SUMS` | `sha256sum bootimus-*` |
| 容器镜像 | `ghcr.io/goovoz/bootimus:<版本>` + `:latest` | 多架构 `linux/amd64,linux/arm64` |

镜像只保留 `latest` 和精确版本号两个 tag，不产 `<major>.<minor>` 之类的浮动 tag。

## 两条流水线

### 1. `fork-release.yml` —— 构建与发布

触发：

- **推送 `v*` 标签**（正常路径）
- **手动 dispatch**：可填 `version` 做冷启动（留空则取 tag 或 `VERSION` 文件）

两个 job：

- `binaries`：`make binaries VERSION=<v>` → `sha256sum` → 创建 Release（自动生成变更说明，
  并在正文追加一段「这是 fork 构建，含补丁 `iso-url-no-suffix`」）
- `docker`：`make sync-profiles` → buildx 多架构 → 推 ghcr，tag 为 `<版本>` 与 `latest`

### 2. `fork-follow-upstream-release.yml` —— 自动跟随上游

每天 04:17（北京时间）检查上游 `releases/latest`：

1. 上游版本 > 本地 `VERSION` → `make bump NEW_VERSION=<v>`（同步 VERSION + 两份 profiles 及内嵌副本）
2. 提交（带 `Fork-Meta: follow-upstream-release`）→ 打标签 `v<v>` → 推 main 与标签
3. **显式派发** `fork-release.yml`（用 GITHUB_TOKEN 推的标签不会触发 tag-push workflow）
4. 本地版本已高于上游时**不降级**

手动触发时可选 `dry_run=true`，只报告不提交。

## 上游 `release.yml` 的处理

上游自带的 `release.yml` 原样保留，但两个 job 都加了守卫：

```yaml
if: github.repository == 'garybowers/bootimus'
```

原因：它的 docker job 硬编码 `IMAGE: garybowers/bootimus` 并依赖 `DOCKERHUB_USERNAME/TOKEN`，
在 fork 里跑必然失败，还会尝试推到上游的 Docker Hub 命名空间。加了守卫后 fork 里它直接 skipped，
代码仍随上游更新，改动只有两行（带 `[bootimus-fork L1]` 锚点）。

## 首次使用注意

- **ghcr 包默认 private**：第一次推送后到 *Packages → bootimus → Package settings* 把可见性改成 public，
  否则匿名 `docker pull` 会 401
- 手动发一个版本：`git tag -a v0.1.77 -m "bootimus 0.1.77" && git push origin v0.1.77`
- 只有 tag 推送才会走 tag 路径；在分支上 dispatch `fork-release.yml` 会用 `VERSION` 文件里的版本号

## 与补丁机制的关系

发布流程不改变 `main` 的形态，仍然是「上游 + 带 `Fork-Patch:` trailer 的补丁提交」。
`fork-follow-upstream-release.yml` 的提交带 `Fork-Meta:` trailer，因此不会被
`sync-upstream.yml` 的提交校验拦下。
