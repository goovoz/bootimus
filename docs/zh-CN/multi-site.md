# 多站点部署

运行一台 Bootimus 作为**主站**(primary),在那里管理一切;在每个分支站点运行一个**副本**(replica),它镜像主站并用本地副本引导本地客户端。副本是只读的:所有更改都在主站进行,并在副本下一次同步时到达。

## 目录

- [拓扑](#拓扑)
- [设置主站](#设置主站)
- [设置副本](#设置副本)
- [哪些会同步,哪些不会](#哪些会同步哪些不会)
- [本地提供 ISO 还是由主站提供](#本地提供-iso-还是由主站提供)
- [带宽](#带宽)
- [主站不可达时](#主站不可达时)
- [监控](#监控)
- [API](#api)

## 拓扑

```
                 HQ                                    Branch office
  +------------------------------+          +------------------------------+
  | Bootimus primary             |  HTTPS   | Bootimus replica             |
  | admin :8081  <---------------+----------+ pulls config and assets      |
  | boot  :8080                  |  every   | admin :8081 (read-only UI)   |
  | all changes are made here    |  5 min   | boot  :8080 serves clients   |
  +------------------------------+          +------------------------------+
                                                     | PXE / TFTP / HTTP
                                                     v
                                               branch clients
```

每个副本保留自己的数据库和数据目录,因此即使 WAN 链路中断,分支机构也能继续从上一次良好的副本引导。副本从不向主站写入。

## 设置主站

主站上除了为每个副本创建一个令牌外无需任何改动:

1. 打开 **Users** → **API Tokens**,创建一个以站点命名的令牌。
2. 选择作用域:
   - `read` 足以同步客户端、镜像、组、SAN 目标、主题、自定义配置文件、工具和自定义文件。
   - `admin` 还会同步**自动安装文件**以及 BMC 和 CHAP 密码。使用 `read` 令牌时这些密码会以脱敏形式到达,副本保留它已有的值。
3. 复制 `btk_...` 密钥;它只显示一次。

通过 HTTPS 提供主站的管理界面(`--admin-tls`,参见[部署指南](deployment.md#保护管理界面)),以免令牌以明文传输。

## 设置副本

像往常一样在分支站点安装 Bootimus,然后将它指向主站:

```bash
bootimus serve \
  --replica-primary https://bootimus.hq.example.com:8081 \
  --replica-token btk_... \
  --replica-interval 5m
```

或在 `bootimus.yaml` 中:

```yaml
replica:
  primary_url: https://bootimus.hq.example.com:8081
  token: btk_...
  interval: 5m
  sync_isos: true
```

环境变量为 `BOOTIMUS_REPLICA_PRIMARY_URL`、`BOOTIMUS_REPLICA_TOKEN`、`BOOTIMUS_REPLICA_INTERVAL` 和 `BOOTIMUS_REPLICA_SYNC_ISOS`。最小间隔为 30 秒。

副本在启动时同步一次,之后每个间隔同步一次。它的管理界面会显示一条横幅,标明主站名称和上次成功同步的时间,所有创建、编辑或删除操作都会返回 `409 Conflict` 并指向主站。本地用户和 API 令牌仍可在副本上管理,以便你能在那里登录。

如果分支站点没有支持 PXE 的 DHCP 服务器,在副本上启用 `--proxy-dhcp`,与独立安装完全相同。

## 哪些会同步,哪些不会

| 从主站同步 | 各实例本地保留 |
|-----------|---------------|
| 客户端(名称、组、允许的镜像、BMC 设置、自动安装文件、SAN 目标) | 引导日志、引导次数和上次引导时间 |
| 客户端组 | 硬件清单 |
| 镜像和镜像组(元数据、引导方式、引导参数、WinPE 选择) | 下次引导动作和 Wake-on-LAN |
| SAN 目标 | 只在分支站点引导过的已发现客户端 |
| 引导菜单主题 | 安装回调和状态 |
| 自定义发行版配置文件 | 用户、API 令牌和审计日志 |
| 已启用的引导工具及其文件 | 计划任务和 webhook |
| 自动安装文件(仅限 admin 令牌) | Kubernetes 集群和节点注册 |
| 自定义文件 | 维护设置 |
| 当前引导加载器集的名称(该集的文件必须已存在于副本上) | 自定义引导加载器集的文件 |

从主站消失的行会从副本中删除,只有一个例外:在分支站点发现且从未在主站上提升为静态的客户端会被保留。下次引导动作有意不做镜像,否则在分支站点已消耗的一次性动作会在下次同步时被重新应用。

## 本地提供 ISO 还是由主站提供

当 `sync_isos: true`(默认)时,副本从主站下载每个已启用的 ISO,在本地提取内核和 initrd,并用自己的磁盘为客户端提供服务。大型镜像库的首次同步需要一些时间;在此期间副本继续提供它已有的镜像,之后的同步只传输大小发生变化的文件。

当 `sync_isos: false` 时,副本只存储元数据,并把引导菜单条目指向主站的引导端口,因此分支站点的客户端会跨 WAN 流式获取 ISO 和引导文件。使用 NBD 或 NFS 引导方式的镜像需要本地文件,在此模式下无法引导。自动安装脚本始终由副本自身提供。

如果下载失败,该镜像会回退到主站的 URL,直到下一次成功同步。

## 带宽

- 元数据同步是小型 JSON 列表,每个间隔运行一次。
- ISO 只获取一次,仅当其在主站上的大小发生变化时才重新获取。
- 来自主站的清单让副本可以跳过自上次运行以来未变化的类型。
- 只有在需要更快传播时才缩短间隔;副本上还有一个 **Sync now** 按钮。

## 主站不可达时

副本继续提供其上一次良好状态。同步被标记为失败,横幅显示错误,本次运行中后续类型会被跳过而不是应用一半。失败的运行不会在本地删除任何内容。主站恢复响应后同步会自动继续。

## 监控

副本的 **Server Info** 标签页有一个 **Replica** 卡片,按类型列出上次同步是否成功、主站有多少行以及多少行发生了变化或被移除。同样的数据可通过 API 获取,`GET /api/me` 包含一个 `replica` 对象,以便脚本判断正在与哪个实例通信。

## API

```bash
# Replica: status of the last sync
curl -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/status

# Replica: sync now
curl -X POST -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/sync

# Primary: what a replica reads
curl -H "Authorization: Bearer $TOKEN" https://hq:8081/api/sync/manifest
```

主站为副本提供 `GET /api/sync/manifest`、`GET /api/sync/tool-files?name=` 和 `GET /api/sync/asset?kind=iso|tool|file`;三者都接受 read 作用域的令牌。
