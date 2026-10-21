# 发布流程（fork）

本仓库的发布由 fork 自己负责，与上游 `garybowers/bootimus` 的发布互不影响。

## 产物

| 产物 | 位置 | 说明 |
|---|---|---|
| 4 个平台二进制 | GitHub Release 资产 | `bootimus-linux-{amd64,arm64}`、`bootimus-windows-{amd64,arm64}`，与上游命名一致 |
| 校验和 | Release 资产 `SHA256SUMS` | `sha256sum bootimus-*` |
| 容器镜像 | `ghcr.io/goovoz/bootimus` | 多架构 `linux/amd64,linux/arm64` |

## 版本口径与 tag 方案（2026-10 起）

上游版本和 fork 的构建次数是两回事，所以版本拆成两段：

| 概念 | 取值 | 例子 |
|---|---|---|
| **基线版本** | `VERSION` 文件 / tag / 手动输入，纯 `x.y.z`，**永远与上游一致** | `0.1.77` |
| **构建修订** | 本次构建所在 commit 的 7 位短 SHA | `d5b132e` |
| 二进制版本串 | `<基线>+g<修订>`（semver build metadata，比大小时被忽略） | `0.1.77+gd5b132e` |
| 镜像不可变 tag | `<基线>-g<修订>`、`sha-<修订>` | `0.1.77-gd5b132e`、`sha-d5b132e` |
| 镜像漂移 tag | `<基线>`、`latest` | `0.1.77`、`latest` |

```bash
docker run --rm --entrypoint /bootimus ghcr.io/goovoz/bootimus:0.1.77-gd5b132e version
# bootimus      0.1.77+gd5b132e
```

**为什么不直接把 `VERSION` 升成 `0.1.78`**：`fork-follow-upstream-release.yml` 是拿
`VERSION` 和上游 `releases/latest` 比大小的。本地一旦变成 `0.1.78`，上游真发 `0.1.78` 时
会被判成「已同步」而**永远拉不下来**；`v0.1.78` 这个 tag 也会和上游将来的发布撞车。
所以基线跟着上游走，构建之间靠 `+g<sha>` / `-g<sha>` 区分。

### 部署时该用哪个引用

| 场景 | 写法 |
|---|---|
| 想完全可复现（推荐） | `image: ghcr.io/goovoz/bootimus@sha256:<digest>` |
| 想按构建锁定但仍可读 | `image: ghcr.io/goovoz/bootimus:0.1.77-gd5b132e` |
| 想跟着最新走（会漂移） | `image: ghcr.io/goovoz/bootimus:latest` |

digest 每次构建都会写进两处：**Actions 运行摘要**的最下方（附可直接粘贴的 `image:` 行），
以及 **Release 正文**的「镜像 digest」小节。

回滚就是换回上一份 digest 或上一个 `-g<sha>` tag：

```bash
docker pull ghcr.io/goovoz/bootimus@sha256:<旧 digest>
docker compose up -d
```

## 两条流水线

### 1. `fork-release.yml` —— 构建与发布

触发：

- **推送 `v*` 标签**（正常路径）
- **手动 dispatch**：可填 `version` 做冷启动（留空则取 tag 或 `VERSION` 文件；
  传 `0.1.77+gabc1234` 这类带后缀的值也能用，会被剥回基线 `0.1.77`）

两个 job：

- `binaries`：`make binaries VERSION=<基线>`（版本串只进 ldflags）→ `sha256sum` → 创建 Release
  （自动生成变更说明，并在正文追加「fork 构建信息」块：版本串、commit、两个补丁、镜像 tag）
- `docker`：`make sync-profiles` → buildx 多架构 → 推 ghcr，tag 见上表，并把 digest 写回 Release 正文

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

## Actions 必须固定到完整 SHA（仓库策略）

GitHub 已把 runner 强制在 Node.js 24 上运行，而 action 自身声明的运行时决定它是否告警/失败。
上游用的主版本还是 node20 时代的，所以这里统一升到**声明 `node24` 的主版本浮动 tag**：

| 用途 | 引用 | 备注 |
|---|---|---|
| 检出代码 | `actions/checkout@v7` | v4 / v5 是 node20 |
| Go 环境 | `actions/setup-go@v7` | v5 是 node20 |
| QEMU | `docker/setup-qemu-action@v4` | v3 是 node20 |
| buildx | `docker/setup-buildx-action@v4` | v3 是 node20 |
| 登录 registry | `docker/login-action@v4` | v3 是 node20 |
| 构建并推送 | `docker/build-push-action@v7` | v6 是 node20 |
| 创建 Release | `softprops/action-gh-release@v3` | v2 是 node20 |

用浮动主版本的好处：同主版本内的安全修复与 bug 修复自动生效，不需要维护 SHA。

现状：

- fork 自有 workflow（`sync-upstream.yml`、`fork-release.yml`、`fork-follow-upstream-release.yml`）已全部升级
- 上游 `ci.yml` 做 L1 兼容改动（只换 `uses:` 的主版本，逻辑不动，顶部有 `[bootimus-fork L1]` 说明）
- 上游 `release.yml` 未升级：它的两个 job 在 fork 里被 `if` 守卫跳过，不会解析 action

> 仓库此前启用过「Require actions to be pinned to a full-length commit SHA」策略（现已去掉）。
> 如需恢复，取 SHA 替换即可，注释里的 `# node24` 留作对照：
>
> ```bash
> git ls-remote https://github.com/actions/checkout 'refs/tags/v7^{}'   # 第一列就是 SHA
> ```

## 与补丁机制的关系

发布流程不改变 `main` 的形态，仍然是「上游 + 带 `Fork-Patch:` trailer 的补丁提交」。
`fork-follow-upstream-release.yml` 的提交带 `Fork-Meta:` trailer，因此不会被
`sync-upstream.yml` 的提交校验拦下。
