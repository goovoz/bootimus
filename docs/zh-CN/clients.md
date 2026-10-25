#  客户端管理指南

使用基于 MAC 的访问控制管理网络引导客户端的完整指南。

##  目录

- [概述](#概述)
- [添加客户端](#添加客户端)
- [客户端权限](#客户端权限)
- [公开镜像与私有镜像](#公开镜像与私有镜像)
- [客户端统计](#客户端统计)
- [批量操作](#批量操作)
- [下次引导动作](#下次引导动作)
- [预览客户端的引导菜单](#预览客户端的引导菜单)
- [从 iSCSI 或 AoE 引导](#从-iscsi-或-aoe-引导)
- [Wake-on-LAN](#wake-on-lan)
- [远程控制台](#远程控制台)
- [硬件清单](#硬件清单)
- [故障排查](#故障排查)

## 概述

Bootimus 通过基于 MAC 地址的访问控制来管理哪些客户端可以引导,以及它们可以访问哪些 ISO。这给你的网络引导环境提供了细粒度控制。

### 关键概念

- **客户端**:由 MAC 地址识别的网络引导设备
- **静态客户端**:手动创建或由发现的客户端提升而来 — 一次永久注册
- **发现的客户端**:未知设备 PXE 引导时自动创建(类似 DHCP 租约)
- **已启用**:客户端被允许引导(在引导菜单中显示)
- **已禁用**:客户端无法引导(被阻止访问引导菜单)
- **已分配镜像**:当一个客户端有已分配镜像时,它**只看到这些镜像**(而不是完整的公开列表)
- **显示公开镜像**:当与已分配镜像一起启用时,客户端能看到已分配镜像和公开镜像
- **下次引导动作**:一次性的引导镜像覆盖,使用后自动清除
- **自动安装变量**:以 `{{VAR.<name>}}` 形式提供给无人值守安装文件的命名值;组变量是默认值,客户端变量覆盖它们(见[无人值守安装](auto-install.md#变量))

### 客户端自动发现

当未知设备进行 PXE 引导时,Bootimus 会自动创建一条 **discovered** 客户端记录,包含:
- 从 PXE 请求中获取的 MAC 地址
- 硬件清单(CPU、内存、制造商、序列号、NIC)
- 默认已启用并可见公开镜像

发现的客户端在客户端表里带 "Discovered" 标记。你可以用 **"Make Static"** 按钮将它们提升为静态客户端,即把它们注册为永久条目。如果之前删除的客户端再次 PXE 引导,系统会自动恢复它。

### 数据库模式

**SQLite 模式**:
- 客户端存储在 SQLite 数据库中
- 镜像分配存在 `allowed_images` JSON 字段中
- 完美适用于单服务器部署

**PostgreSQL 模式**:
- 客户端存储在 PostgreSQL 数据库中
- 镜像分配使用多对多关系表
- 大规模部署性能更佳

## 添加客户端

### 通过 Web 界面

1. 进入管理面板:`http://your-server:8081`
2. 点击 **"Clients"** 标签页
3. 点击 **"Add Client"** 按钮
4. 填写详情:
   - **MAC Address**:`00:11:22:33:44:55`(必填)
   - **Name**:友好名称(例如 "Lab Server 1")
   - **Description**:其他详情(可选)
   - **Enabled**:勾选以允许引导
5. 点击 **"Create Client"**

### 通过 API

```bash
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients \
  -H "Content-Type: application/json" \
  -d '{
    "mac_address": "00:11:22:33:44:55",
    "name": "Lab Server 1",
    "description": "Dell PowerEdge R720",
    "enabled": true
  }'
```

### MAC 地址格式

Bootimus 接受以下格式的 MAC 地址:
- `00:11:22:33:44:55`(冒号分隔,首选)
- `00-11-22-33-44-55`(短横线分隔,自动转换)
- `001122334455`(无分隔符,自动转换)

所有格式都被规范化为小写的冒号分隔形式。

## 客户端权限

### 为客户端分配镜像

**通过 Web 界面**:
1. 在客户端行点击 **"Edit"**
2. 从多选下拉框中选择镜像
3. 点击 **"Update Client"**

**通过 API**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/assign-images \
  -H "Content-Type: application/json" \
  -d '{
    "mac_address": "00:11:22:33:44:55",
    "image_filenames": [
      "ubuntu-24.04-live-server-amd64.iso",
      "debian-13.2.0-amd64-netinst.iso",
      "archlinux-2025.12.01-x86_64.iso"
    ]
  }'
```

### 查看客户端权限

**通过 Web 界面**:
- 客户端已分配的镜像显示在编辑弹窗中

**通过 API**:
```bash
# Get client details including assigned images
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq
```

**响应**:
```json
{
  "success": true,
  "data": {
    "id": 1,
    "mac_address": "00:11:22:33:44:55",
    "name": "Lab Server 1",
    "description": "Dell PowerEdge R720",
    "enabled": true,
    "boot_count": 15,
    "last_boot": "2025-01-02T10:30:00Z",
    "allowed_images": [
      "ubuntu-24.04-live-server-amd64.iso",
      "debian-13.2.0-amd64-netinst.iso"
    ]
  }
}
```

## 公开镜像与私有镜像

### 公开镜像

公开镜像对**所有客户端**可用,即使是未注册的客户端。

**使用场景**:
-  救援/恢复 ISO
-  网络诊断工具
-  常用部署镜像
-  开放实验室环境

**将镜像设为公开**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": true}'
```

### 私有镜像

私有镜像**仅对已分配的客户端可用**。

**使用场景**:
-  敏感或受版权保护的镜像
-  客户端专属部署
-  受限环境
-  Beta/测试镜像

**将镜像设为私有**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=windows.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": false}'
```

### 访问控制矩阵

| 客户端状态 | 可见内容 |
|--------------|---------------|
| **已启用 + 已分配** | 仅看到已分配镜像 |
| **已启用 + 无分配** | 所有公开镜像 |
| **已禁用** | 所有公开镜像 |
| **未注册** | 所有公开镜像 |

## 客户端统计

Bootimus 为每个客户端跟踪引导统计:

- **Boot Count**:引导尝试总次数
- **Last Boot**:最近一次引导的时间戳
- **Success Rate**:成功引导的百分比

### 查看统计

**通过 Web 界面**:
- 统计显示在客户端表中

**通过 API**:
```bash
# Get all clients with statistics
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | jq '.data[] | {name, boot_count, last_boot}'

# Get top clients by boot count
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data | sort_by(.boot_count) | reverse | .[0:10] | .[] | {name, boot_count}'
```

### 引导日志

查看每个客户端的详细引导日志:

```bash
# Filter boot logs by MAC address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'
```

## 批量操作

### 批量添加客户端

```bash
#!/bin/bash
# bulk-add-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Format: MAC:NAME:DESCRIPTION
CLIENTS=(
  "00:11:22:33:44:01:Server-01:Production Web Server"
  "00:11:22:33:44:02:Server-02:Production Database Server"
  "00:11:22:33:44:03:Server-03:Production Cache Server"
  "00:11:22:33:44:10:Workstation-01:Developer Laptop"
  "00:11:22:33:44:11:Workstation-02:QA Testing Machine"
)

for entry in "${CLIENTS[@]}"; do
  IFS=':' read -r mac name description <<< "$entry"

  curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients \
    -H "Content-Type: application/json" \
    -d "{
      \"mac_address\":\"$mac\",
      \"name\":\"$name\",
      \"description\":\"$description\",
      \"enabled\":true
    }"

  echo "Added $name ($mac)"
  sleep 0.5
done
```

### 批量分配镜像

```bash
#!/bin/bash
# bulk-assign-images.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Assign Ubuntu and Debian to all servers
SERVER_MACS=(
  "00:11:22:33:44:01"
  "00:11:22:33:44:02"
  "00:11:22:33:44:03"
)

IMAGES='["ubuntu-24.04-live-server-amd64.iso","debian-13.2.0-amd64-netinst.iso"]'

for mac in "${SERVER_MACS[@]}"; do
  curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/assign-images \
    -H "Content-Type: application/json" \
    -d "{\"mac_address\":\"$mac\",\"image_filenames\":$IMAGES}"

  echo "Assigned images to $mac"
done
```

### 批量启用/禁用

```bash
#!/bin/bash
# bulk-enable.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Get all clients and enable them
macs=$(curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[].mac_address')

for mac in $macs; do
  curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=$mac" \
    -H "Content-Type: application/json" \
    -d '{"enabled":true}'
  echo "Enabled $mac"
done
```

### 导出客户端列表

```bash
#!/bin/bash
# export-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

echo "MAC Address,Name,Description,Enabled,Boot Count,Last Boot"

curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[] | [.mac_address, .name, .description, .enabled, .boot_count, .last_boot] | @csv'
```

### 从 CSV 导入客户端

```bash
#!/bin/bash
# import-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"
CSV_FILE="clients.csv"

# Skip header line and process CSV
tail -n +2 "$CSV_FILE" | while IFS=',' read -r mac name description enabled; do
  # Remove quotes from CSV values
  mac=$(echo $mac | tr -d '"')
  name=$(echo $name | tr -d '"')
  description=$(echo $description | tr -d '"')
  enabled=$(echo $enabled | tr -d '"')

  curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients \
    -H "Content-Type: application/json" \
    -d "{
      \"mac_address\":\"$mac\",
      \"name\":\"$name\",
      \"description\":\"$description\",
      \"enabled\":$enabled
    }"

  echo "Imported $name ($mac)"
done
```

## 下次引导动作

为客户端设置一次性的引导镜像。下次 PXE 引导时,所选镜像会作为默认菜单项被预选,并设有超时。动作在使用后自动清除 — 后续引导回归正常。

### 通过 Web 界面

1. 在客户端行点击 **"Next Boot"**
2. 从下拉框选择一个镜像
3. 点击 **"Set Next Boot"** 仅设置镜像,或点击 **"Set & Wake"** 同时发送一个 Wake-on-LAN 数据包

### 通过 API

```bash
# Set next boot image
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients/next-boot \
  -H "Content-Type: application/json" \
  -d '{"mac_address":"00:11:22:33:44:55","image_filename":"ubuntu-24.04.iso"}'

# Clear next boot action
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients/next-boot \
  -H "Content-Type: application/json" \
  -d '{"mac_address":"00:11:22:33:44:55","image_filename":""}'
```

### 行为

- 引导菜单照常显示,但下次引导镜像被预选为默认
- 如果全局菜单超时被禁用(设为 0),会作为覆盖应用 10 秒的超时
- 如果客户端在动作消费之前没有引导,下次 PXE 请求时会清除下次引导
- 当客户端有已分配镜像时,空组会从菜单中隐藏

## 预览客户端的引导菜单

无需等待客户端实际 PXE 引导,即可渲染出它将收到的 iPXE 脚本。预览没有副作用:不会写入引导日志,不会消费客户端的下次引导动作,集群注册状态也保持不变。

### 通过 Web 界面

- 点击客户端行上的 **"Preview Menu"**,或
- 打开 **Boot Menu** → **Preview as Client**,输入 MAC 地址(或保留 `unknown` 表示未注册的客户端),然后点击 **"Preview Menu"**

弹窗会显示该 MAC 是否为已注册客户端、它会收到普通菜单还是集群安装引导、是否有待处理的下次引导镜像,以及带 **Copy** 和 **Download** 按钮的完整脚本。

### 通过 API

```bash
# Preview for a specific client
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8081/api/clients/menu-preview?mac=00:11:22:33:44:55"

# Preview the anonymous menu an unregistered client gets
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients/menu-preview
```

响应数据包含 `mac`、`known_client`、`client_name`、`next_boot_image`、`kind`(`menu` 或 `install`)和 `script`。只读用户也可以调用此端点。

## 从 iSCSI 或 AoE 引导

客户端可以引导位于 SAN 磁盘而非本地磁盘上的操作系统。Bootimus 用 iPXE 的 `sanboot` 命令把磁盘交给固件,所以凡是 iPXE 能从 SAN 引导的都适用:iSCSI 和 ATA-over-Ethernet 目标,以及通过 HTTP 提供的原始磁盘镜像。Bootimus 不托管磁盘;请把目标指向你已经在运行的存储服务器。

### 定义目标

**Clients** → **SAN Targets** → **Add SAN Target**:

| 字段 | 含义 |
|------|------|
| Name | 在引导菜单中显示为 "Boot from SAN: *name*" |
| SAN URI | iPXE 的 SAN URI,例如 `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`、`aoe:e0.0` 或 `http://10.0.0.5/disk.img` |
| Initiator IQN | 可选。`{{MAC}}` 会替换为去掉冒号的客户端 MAC 地址,因此一个目标可以用不同的发起方名称服务多个客户端 |
| CHAP | 可选的用户名和密码,如果目标要求,还可填写反向(双向)CHAP 凭据 |

iSCSI URI 的格式为 `iscsi:<server>:<protocol>:<port>:<lun>:<iqn>`。协议、端口和 LUN 留空即使用默认值,如上例所示。

### 分配目标

打开一个客户端(或客户端组),在 **SAN Target** 下选择目标。没有自己目标的客户端会继承组的目标。该客户端的菜单会多出一个 "Boot from SAN" 条目;勾选 **Boot from SAN by default** 可让它成为超时触发时菜单落入的条目,这样重启的机器会直接从 SAN 启动。待处理的下次引导镜像仍然优先,常规镜像也仍可选择。

要进行一次性的 SAN 引导,请在 **Next Boot** 中选择该目标;它列在镜像之后,并像其他下次引导动作一样在一次引导后清除。

### 目标示例

使用 `tgt` 的 Linux 目标:

```bash
sudo tgtadm --lld iscsi --op new --mode target --tid 1 -T iqn.2024-01.lan:disk0
sudo tgtadm --lld iscsi --op new --mode logicalunit --tid 1 --lun 1 -b /srv/iscsi/disk0.img
sudo tgtadm --lld iscsi --op bind --mode target --tid 1 -I ALL
```

目标 URI:`iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`

使用 LIO(`targetcli`)时,创建一个 fileio 或 block 后端存储、一个 iqn、一个 LUN 和一个 portal,然后要么为该 TPG 禁用认证,要么设置 `userid`/`password`(以及 `mutual_userid`/`mutual_password`)以匹配这里的 CHAP 字段。

**Windows** 通过 iPXE 的 SAN 钩子从 iSCSI 安装和引导:在客户端已分配目标的情况下,从菜单引导 Windows 安装程序,安装程序会把 iSCSI 磁盘视为本地驱动器。安装完成后勾选 **Boot from SAN by default**,机器便会在每次启动时回到它。Windows 需要其引导所用网卡的网络驱动;大多数现代 Intel 和 Realtek 网卡安装程序都已覆盖。

### 注意事项

- CHAP 凭据会放入 iPXE 脚本中,而引导中的客户端是通过明文 HTTP 获取该脚本的。请像对待自动安装应答文件那样,把引导网络视为可信网络。
- iPXE 的 `sanboot` 要求 SAN 磁盘上已有引导加载程序;它不会安装任何东西。
- 只读用户能看到目标,但看不到 CHAP 密码。

## Wake-on-LAN

发送一个 WOL magic 包以远程唤醒客户端。与 **Next Boot** 结合使用,可以唤醒一台机器并让它引导到指定镜像。

### 通过 API

```bash
# Wake a client
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55"

# Wake with custom broadcast address
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"broadcast_addr":"192.168.1.255"}'
```

## 远程控制台

配置了 BMC 主机的客户端会在客户端编辑对话框中获得 **Open Console** 按钮,并在客户端列表中获得 **Console** 按钮。它会在浏览器中打开一个交互式串口控制台,经 Bootimus 通过 WebSocket 转发,因此无需外接显示器就能观察机器的 POST、固件设置和安装程序。控制台仅限管理员使用,每个客户端同一时间只能有一个会话,30 分钟无活动后自动关闭。

| 协议 | 传输方式 | 要求 |
|---|---|---|
| **Intel AMT** | 经由 AMT 重定向服务的 Serial-over-LAN,由 Bootimus 在 16994 端口原生实现(客户端使用 TLS 管理端口 16993 时为 16995) | 在 MEBx 的 *Remote Control* 下,或在 AMT Web 界面的 *Remote Control* → *Redirection* 下启用 SOL 和重定向监听器;用户和密码与电源控制相同 |
| **Redfish** | 在 Bootimus 主机上运行 `ipmitool sol activate` 实现的 IPMI Serial-over-LAN | `PATH` 中有 `ipmitool`(Docker 镜像已包含),BMC 的 UDP 623 端口可达,且已为该 BMC 用户启用 SOL。控制台不使用 Redfish 端口 |

串口控制台只显示固件或操作系统写到串口的内容。在大多数服务器上,这意味着要在 BIOS 或 UEFI 设置中启用 *Console Redirection*,在 Linux 上则要在内核命令行加入 `console=ttyS0,115200`(或 `ttyS1`,取决于主板)。Intel AMT 机器在启用 SOL 后会以 115200 波特率重定向 COM1。如果屏幕一直空白,请按回车;如果引导期间什么都没出现,说明固件中没有打开控制台重定向。

按键会按原样发送,所以常用的终端快捷键都能用。关闭对话框或点击 **Disconnect** 会结束会话,对于 IPMI 还会在 BMC 上停用 SOL 负载,以便下一次连接干净地开始。

### 通过 API

端点是管理端口上位于 `/api/clients/console?mac=...` 的 WebSocket。浏览器无法在 WebSocket 上发送 `Authorization` 头,因此 JWT 要么作为 `Sec-WebSocket-Protocol` 中紧跟 `bearer` 的第二项传递,要么作为 `token` 查询参数传递。二进制帧承载控制台输出和按键;来自 Bootimus 的文本帧是 JSON 状态消息,例如 `{"type":"connected","message":"..."}`。

```bash
# websocat example: interactive console for a client
websocat --binary "ws://localhost:8081/api/clients/console?mac=00:11:22:33:44:55&token=$TOKEN"
```

## 硬件清单

Bootimus 在 PXE 引导期间从客户端收集硬件信息,包括:
- CPU、内存、平台和架构
- 制造商、产品名和序列号
- UUID 和 NIC 芯片信息
- IP 地址

可在任意客户端的 **Edit & Assign Images** 弹窗中查看清单,或者使用 API:

```bash
# Latest inventory
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory?mac=00:11:22:33:44:55"

# Inventory history
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory/history?mac=00:11:22:33:44:55&limit=10"
```

## 故障排查

### 客户端看不到引导菜单

**症状**:客户端引导但显示空菜单或 "No boot images available"

**可能原因**:
1. 客户端被禁用
2. 没有可用的公开镜像
3. 没有镜像分配给该客户端
4. 所有镜像都被禁用

**解决方案**:
```bash
# Check client status
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq

# Enable client
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'

# Check available images
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images | jq '.data[] | {filename, enabled, public}'

# Make images public
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public":true,"enabled":true}'
```

### 未检测到 MAC 地址

**症状**:引导日志显示 "unknown" MAC 地址

**可能原因**:
1. iPXE 无法从网络接口检测 MAC 地址
2. 客户端使用多个网络接口

**解决方案**:
```bash
# Check boot logs for actual IP address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | jq '.data[] | {mac_address, ip_address}'

# Register client by IP if MAC is unknown
# (Note: Less reliable, IP may change)
```

### 已分配镜像不显示

**症状**:客户端只能看到公开镜像,看不到已分配的镜像

**可能原因**:
1. 客户端未启用
2. 镜像未启用
3. MAC 地址格式错误
4. 数据库同步问题

**解决方案**:
```bash
# Verify client exists and is enabled
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq

# Verify image assignments
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | \
  jq '.data.allowed_images'

# Re-assign images
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/assign-images \
  -H "Content-Type: application/json" \
  -d '{
    "mac_address":"00:11:22:33:44:55",
    "image_filenames":["ubuntu.iso","debian.iso"]
  }'

# Check database directly (SQLite)
sqlite3 data/bootimus.db "SELECT * FROM clients WHERE mac_address='00:11:22:33:44:55';"
```

### 客户端重复错误

**症状**:"Client already exists" 或 UNIQUE 约束错误

**原因**:MAC 地址已注册

**解决方案**:
```bash
# Find existing client
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'

# Update existing client instead
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"name":"Updated Name","enabled":true}'

# Or delete and re-create
curl -H "Authorization: Bearer $TOKEN" -X DELETE "http://localhost:8081/api/clients?mac=00:11:22:33:44:55"
```

## 下一步

-  配置 [镜像管理](images.md) 添加 ISO
-  使用 [管理控制台](admin.md) 进行管理
-  设置 [DHCP 配置](dhcp.md) 实现网络引导
-  查看 [引导日志](admin.md#boot-logs) 进行监控
