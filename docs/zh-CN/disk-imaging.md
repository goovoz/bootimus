# 磁盘镜像

Bootimus 可以把客户端的磁盘捕获为镜像,再把该镜像部署回一台客户端或整个客户端组的所有成员。它以无人值守方式驱动内置的 **Clonezilla Live** 工具,因此工作流程与大家熟悉的 FOG 一样:排队一个任务,让机器 PXE 引导,然后在管理控制台查看结果。

## 目录

- [工作原理](#工作原理)
- [前提条件](#前提条件)
- [捕获磁盘](#捕获磁盘)
- [部署镜像](#部署镜像)
- [任务](#任务)
- [镜像存放位置](#镜像存放位置)
- [网络暴露面](#网络暴露面)
- [故障排查](#故障排查)

## 工作原理

1. 你在管理控制台或通过 API 为某个客户端排队一个**捕获**或**部署**任务。每个任务带有一个随机令牌。
2. 客户端下一次 PXE 引导时,菜单直接跳到由 Clonezilla 工具的内核、initrd 和 squashfs 构建的 Clonezilla 条目。
3. Clonezilla 从 Bootimus 获取一段简短的任务脚本(`/diskimage/job?token=…`),通过 NFS 挂载镜像存储,运行 `ocs-sr`,并向 `/diskimage/callback` 回报结果。
4. 捕获完成后客户端关机。部署完成后客户端重启进入刚恢复的磁盘。

任务只会发给排队时指定的 MAC 地址,已完成、失败或已取消的任务不会再次下发。

## 前提条件

- **Clonezilla 工具**:在 **Tools** 中下载并启用。缺少时磁盘镜像页面会提示。
- **NFS 服务器**:以 `--nfs-enabled` 启动 Bootimus(或在 `bootimus.yaml` 中设置 `nfs_enabled: true`)。Clonezilla 需要一个可写共享用于保存,内置 NFS 服务器无需额外软件包即可提供。
- 客户端使用**内核引导**(默认方式)。磁盘镜像不支持 sanboot 或 memdisk。
- 除常规的 TFTP 和 HTTP 端口外,客户端还需能访问 `2049/tcp` 端口(或配置的 `--nfs-port`)。

## 捕获磁盘

1. 打开 **Disk Images** 并点击 **Capture Disk…**,或在 **Clients** 中某个客户端所在行点击 **Capture Disk**。
2. 选择客户端,为镜像命名(小写字母、数字、点、连字符和下划线),并选择磁盘。默认是 `sda`;`nvme0n1` 之类也可以,`auto` 则选取 Clonezilla 找到的第一块固定可写磁盘。
3. 可选勾选 **Send Wake-on-LAN now**。
4. 重启客户端(或让魔术包来做)。它会引导 Clonezilla,保存磁盘,然后关机。

Clonezilla 运行 `ocs-sr -q2 -j2 -z1p -i 4096 -sc savedisk <name> <disk>`:尽可能使用 partclone,包含 MBR 与第一个分区之间的隐藏数据,多线程 gzip 压缩,按 4 GiB 分卷。

捕获进行中镜像显示为 **capturing**。客户端报告成功后镜像变为 **ready** 并记录其大小。如果 `ocs-sr` 失败,镜像标记为 **failed**,部分数据会被丢弃。

**通过 API**:
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","name":"win11-lab","description":"Lab build, Oct 2026","disk":"sda","wake":true}' \
  http://localhost:8081/api/disk-images/capture
```

## 部署镜像

1. 点击就绪镜像旁的 **Deploy**,客户端行上的 **Deploy Image**,或客户端组批量操作中的 **Deploy Disk Image to All…**。
2. 选择目标(一台客户端或一个客户端组)、要覆盖的磁盘,以及是否发送 Wake-on-LAN。组部署会遵循该组的 WOL 广播地址和错峰延迟。
3. 确认。目标磁盘上的所有内容都会被替换。

Clonezilla 运行 `ocs-sr -g auto -e1 auto -e2 -r -j2 -scr restoredisk <name> <disk>`:根据镜像重建分区表,分区扩展以填满更大的磁盘,在镜像原有位置重新安装 GRUB,完成后客户端重启。

组内每个成员都有自己的任务,因此你可以看着它们逐一完成。已有活动任务的客户端会被跳过并在响应中列出。

**通过 API**:
```bash
# One client
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","image_id":3,"disk":"sda"}' \
  http://localhost:8081/api/disk-images/deploy

# Every enabled member of client group 2, waking them as it goes
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"client_group_id":2,"image_id":3,"disk":"auto","wake":true}' \
  http://localhost:8081/api/disk-images/deploy
```

## 任务

**Capture and Deploy Jobs** 表格显示每个任务及其状态:

| 状态 | 含义 |
|------|------|
| `pending` | 已排队;等待客户端 PXE 引导 |
| `running` | Clonezilla 已获取任务脚本并正在工作 |
| `completed` | 客户端报告成功 |
| `failed` | 挂载或 `ocs-sr` 失败;消息列给出原因 |
| `cancelled` | 已从控制台或 API 取消 |

等待中和运行中的任务可以取消。取消捕获会丢弃正在写入的镜像。一台客户端同一时间只能有一个活动任务。

引导菜单预览(客户端行上的 **Preview Menu**)在任务活动期间会显示 Clonezilla 引导条目,这是快速确认客户端将要做什么的方法。

当 **Settings → Webhook** 中启用了该选项时,已结束的任务会触发 `disk.capture_completed`、`disk.capture_failed`、`disk.deploy_completed` 和 `disk.deploy_failed` webhook 事件。

## 镜像存放位置

镜像就是位于 `<data-dir>/disk-images/<name>/` 下的普通 Clonezilla 镜像目录,因此可以查看、复制到别处,或用任意 Clonezilla Live U 盘手动恢复。进行中的捕获写入 `<data-dir>/disk-images/.staging/<token>/`,客户端报告成功后再移到正式位置。

磁盘镜像体积很大,请留意 **Settings** 中的 **Housekeeping** 卡片,它显示数据目录各顶层条目占用的空间。从管理控制台创建的备份会跳过 `isos` 和 `tools` 目录但包含其他所有内容,所以下载备份前请先删除不再需要的磁盘镜像。

## 网络暴露面

Clonezilla 通过无认证的 NFS v3 挂载镜像存储,与 FOG 和 DRBL 的方式相同。Bootimus 限制了该共享暴露的内容:

- 常规引导导出(`isos/`)和磁盘镜像根目录为只读;
- 每个捕获任务获得自己的可写导出 `/disk-images/.staging/<token>`,它只在该任务活动期间存在,没有令牌无法猜到;
- 单个镜像目录不能直接挂载;部署挂载的是只读的镜像根目录。

请把引导网络放在你信任的网段上,PXE 本来就应如此。

## 故障排查

**任务一直处于 pending。** 客户端自任务排队后没有 PXE 引导,或者引导了但 Clonezilla 未启用,因此 Bootimus 下发了常规菜单。检查 **Tools**,然后在客户端行上使用 **Preview Menu**。

**消息列出现 `nfs_mount_failed`。** 客户端无法挂载共享。确认已设置 `--nfs-enabled`,NFS 端口可从客户端子网访问,且 `--server-addr` 是客户端能够到达的地址。

**`disk=auto` 时出现 `no_disk_found`。** Clonezilla 没有看到固定可写磁盘。请显式指定设备名。

**`ocs-sr_exit_N`。** Clonezilla 本身失败。客户端在重启前会显示 Clonezilla 输出 30 秒;常见原因是捕获的磁盘上有已挂载或损坏的文件系统,或者部署到比镜像小的磁盘。

**镜像显示为 failed。** 不要取消任何东西;在 **Disk Images** 页面删除它,然后重新捕获。
