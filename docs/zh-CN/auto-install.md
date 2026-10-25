# 无人值守安装指南

跨 Windows、Ubuntu、Debian、Red Hat 系列发行版、openSUSE、Fedora CoreOS、Flatcar 和 Proxmox VE 运行无人值守安装。配置文件丢进去一次,挂到镜像上,之后每次 PXE 引导都能在不按一次键的情况下完成安装。

## 目录

- [概述](#概述)
- [支持的格式](#支持的格式)
- [文件库](#文件库)
- [挂载文件](#挂载文件)
- [解析顺序](#解析顺序)
- [占位符](#占位符)
- [变量](#变量)
- [预览](#预览)
- [校验](#校验)
- [示例](#示例)
- [不支持的发行版](#不支持的发行版)
- [报告安装完成](#报告安装完成)
- [Windows 注意事项](#windows-注意事项)
- [REST API](#rest-api)
- [故障排查](#故障排查)

## 概述

无人值守安装配置存储在 `data/autoinstall/` 下按发行版分类的文件库中。你可以:

- **在 UI 里管理文件** — 在 **Auto-Install** 标签页中创建、编辑、上传、下载、删除配置,无需触碰文件系统。
- **为镜像设置默认值** — 引导该镜像的每个客户端都得到同一份配置。
- **按客户端覆盖** 或 **按客户端组覆盖** — 当某台机器需要不同的配置时(不同的 hostname、磁盘布局、角色等)。
- **模板化** — 使用类似 `{{HOSTNAME}}` 和 `{{IP}}` 的占位符,在分发时根据正在引导的客户端身份解析,还可以使用按客户端或按组设置的自定义 `{{VAR.name}}` 变量。
- **预览** — 查看任意客户端和镜像的渲染结果,并在保存时对 YAML、XML 和 JSON 文件做格式校验。

Bootimus 通过 HTTP 在无人值守安装端点上提供渲染后的文件。Linux 安装程序通过内核参数获取它;对 Windows 而言,wimboot 会把同一个 URL 作为额外的 `initrd` 下载并以 `AutoUnattend.xml` 的名字注入 WinPE 的 RAM 盘,这样 `setup.exe /unattend:` 就能自动取用(见 [Windows 注意事项](#windows-注意事项))。

## 支持的格式

| 发行版家族 | 格式 | 文件扩展名 | 识别为 |
|--------------|--------|----------------|-------------|
| Windows (10/11/Server) | `autounattend.xml` | `.xml` | `autounattend` |
| Ubuntu (Server live, 20.04+) | cloud-init / autoinstall | `.yaml`, `.yml` | `autoinstall` |
| Debian | preseed | `.cfg` | `preseed` |
| Red Hat / Rocky / Fedora / Alma | kickstart | `.ks` | `kickstart` |
| openSUSE Leap 15 / SLES 15 | AutoYaST profile | `opensuse/` 下的 `.xml` | `autoyast` |
| openSUSE Leap 16 / Tumbleweed / SLES 16 | Agama profile | `opensuse/` 下的 `.json`, `.jsonnet` | `agama` |
| Fedora CoreOS / Flatcar | Ignition 配置 | `.ign` | `ignition` |
| Proxmox VE | 应答文件 | `proxmox/` 下的 `.toml` | `proxmox` |
| 其他任何 | 原始格式 | 任意 | `generic` |

类型由文件的扩展名及其所在的发行版目录共同决定,因此同一个 `.xml` 文件在 `windows/` 下是 autounattend 文档,而在 `opensuse/` 下则是 AutoYaST profile。类型决定了 UI 内的标签、文件被服务时的 `Content-Type` 头,以及引导菜单添加的内核参数:

| 类型 | 引导菜单添加的内核参数 | `Content-Type` |
|------|------------------------------------------|----------------|
| `preseed` | `auto=true priority=critical url=<url>` | `text/plain` |
| `kickstart` | `inst.ks=<url>` | `text/plain` |
| `autoinstall` | `autoinstall ds=nocloud-net;s=<seed-url>/` | `text/yaml` |
| `autounattend` | 无(wimboot 将其作为额外的 `initrd` 获取,见 [Windows 注意事项](#windows-注意事项)) | `application/xml` |
| `autoyast` | `autoyast=<url>` | `application/xml` |
| `agama` | `inst.auto=<url>` | `application/json` |
| `ignition` | `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal`(Fedora CoreOS)或 `flatcar.first_boot=1 ignition.config.url=<url>`(Flatcar) | `application/json` |
| `proxmox` | 无(安装程序自行获取应答文件,见 [Proxmox VE](#proxmox-ve-应答文件)) | `application/toml` |
| `generic` | 无 | `text/plain` |

`<url>` 即 `http://<server>:8080/autoinstall/<image-filename>?mac=<mac>`。`generic` 文件在请求时会被提供,但不会告知任何安装程序;如果希望引导菜单把文件传给安装程序,请选用上述某种具体格式。

## 文件库

所有无人值守安装文件都位于 `data/autoinstall/<distro>/<filename>` 下:

```
data/autoinstall/
├── windows/
│   ├── kiosk.xml
│   └── server-2022.xml
├── ubuntu/
│   ├── default.yaml
│   └── lab-bench.yaml
├── debian/
│   └── server.cfg
├── rocky/
│   └── workstation.ks
├── opensuse/
│   ├── leap15.xml
│   └── leap16.jsonnet
├── coreos/
│   └── worker.ign
└── proxmox/
    └── answer.toml
```

`<distro>` 段必须匹配一个已知的发行版 profile ID(详见 [发行版 Profile](distro-profiles.md))。该目录在首次启动时自动创建。

### 通过 UI 添加文件

**Auto-Install** 标签 → **New File** 打开编辑器,内含发行版选择器、文件名输入框,以及对语法友好的文本区。**Upload File** 接受任意本地文件并放入所选的发行版文件夹。

### 手动添加文件

直接丢进去就行:

```bash
mkdir -p data/autoinstall/ubuntu
cp my-autoinstall.yaml data/autoinstall/ubuntu/default.yaml
```

它们会立即出现在 UI 中 — 无需重启,无需扫描。

## 挂载文件

无人值守安装文件在你将其挂到某处之前不会产生任何效果。有三处可以挂载:

### 镜像(默认值)

**Images** 标签 → 打开镜像的 **Properties** → **Auto-Install** 部分 → 选择一个文件。引导该镜像的每个客户端都会得到这份配置,除非有更具体的覆盖。

### 客户端(按机器覆盖)

**Clients** 标签 → 打开一个客户端 → **Auto-Install File** 下拉框。当某台特定机器需要不同配置时使用(例如,一台构建服务器 vs. 桌面机群的其余机器)。

### 客户端组(按机群覆盖)

**Groups** 标签 → 打开一个组 → **Auto-Install File**。适用于该组内的每个客户端。适合"实验室 3 的所有工作站"这类场景。

## 解析顺序

当客户端请求其无人值守安装文件时,Bootimus 会沿以下层级查找:

```
1. 按客户端覆盖              (Client.AutoInstallFile)
2. 按组覆盖                  (ClientGroup.AutoInstallFile, if client is in a group)
3. 镜像默认值                (Image.AutoInstallFile)
4. 内联旧版脚本              (Image.AutoInstallScript — pre-0.1.58 setups)
5. → 404 (no auto-install configured)
```

第一个非空匹配胜出。端点会记录它服务来源的日志:

```
Served auto-install script for ubuntu-24.04-live-server-amd64.iso \
  (source: client:b4:2e:99:01:5f:a3, type: autoinstall, size: 1247 bytes)
```

## 占位符

这些占位符会在分发时按客户端替换:

| 占位符 | 替换为 |
|-------|---------------|
| `{{MAC}}` | 客户端 MAC 地址(小写,冒号分隔);对 Proxmox VE 而言,是安装程序请求中第一个匹配到已知客户端的接口 MAC,若无匹配则为列出的第一个接口 |
| `{{CLIENT_NAME}}` | Clients 表中的友好名称 |
| `{{HOSTNAME}}` | 同 `{{CLIENT_NAME}}`(在配置中更清晰的别名) |
| `{{CLIENT_GROUP}}` | 客户端所属组的名称,没有组时为空 |
| `{{IP}}` | 发起请求的客户端 IP |
| `{{SERVER_ADDR}}` | Bootimus 服务器地址 |
| `{{SERVER_PORT}}` | Bootimus 的 HTTP 引导端口 |
| `{{SERVER_URL}}` | `http://<SERVER_ADDR>:<SERVER_PORT>`,与引导菜单使用的基础 URL 相同 |
| `{{IMAGE_NAME}}` | 正在引导的镜像的显示名 |
| `{{IMAGE_FILENAME}}` | 正在引导的镜像的 ISO 文件名 |
| `{{IMAGE_URL}}` | ISO 在 `/isos/` 下的访问 URL,与引导菜单作为 `iso-url=` 传递的值相同 |
| `{{CALLBACK_URL}}` | 安装程序用来报告完成的完整 URL,包含一次性令牌(见[报告安装完成](#报告安装完成)) |
| `{{INSTALL_TOKEN}}` | `{{CALLBACK_URL}}` 背后的裸令牌,用于构造你自己的请求 |
| `{{VAR.<name>}}` | 名为 `<name>` 的客户端或组变量(见[变量](#变量)) |

占位符是纯字符串替换 — 不做转义。请按目标格式(XML、YAML 等)适当地引用它们。没有值的占位符会按原文保留,并在分发文件时记录为未解析;使用[预览](#预览)可以在机器引导前发现这些问题。

## 变量

内置占位符覆盖了客户端的身份信息,但大多数机群还需要 Bootimus 无从得知的值:角色、机架编号、磁盘设备、镜像源 URL。变量正是为此而设。每个客户端和每个客户端组都带有一组命名的字符串值,每一个都可以在无人值守安装文件中以 `{{VAR.<name>}}` 的形式使用。

- 名称只能使用字母、数字和下划线(`role`、`rack_id`、`DISK`);保存客户端或组时,服务器会拒绝其他任何字符。
- **组变量是默认值**,适用于所有成员。**客户端变量逐个名称覆盖**它们:组可以设置 `role: worker`,某台机器单独设置 `role: control-plane`,同时仍然继承组的 `rack`。
- 两个层级都没有值的 `{{VAR.<name>}}` 保持未解析。

在 UI 中设置:**Clients** → 打开客户端 → **自动安装变量**,或 **Groups** → 打开组 → **自动安装变量**:每行是一个名称和一个值,有 **添加变量** 按钮和每行的删除按钮。通过 API,`variables` 在客户端和组的 payload 中都是一个字符串对象:

```bash
# 组默认值
curl -u admin:pw -X PUT "http://localhost:8081/api/client-groups/update?id=3" \
  -H "Content-Type: application/json" \
  -d '{"name":"lab-3","variables":{"role":"worker","rack":"3","disk":"/dev/sda"}}'

# 某台机器覆盖单个值
curl -u admin:pw -X PUT "http://localhost:8081/api/clients?mac=b4:2e:99:01:5f:a3" \
  -H "Content-Type: application/json" \
  -d '{"variables":{"role":"control-plane"}}'
```

在客户端更新中发送 `"variables": null` 会清空变量;省略该键则保持不变。`GET /api/clients` 和 `GET /api/client-groups` 以同一个键返回它们。

同时使用两个层级的 kickstart:

```
network --bootproto=dhcp --hostname={{HOSTNAME}}
ignoredisk --only-use={{VAR.disk}}
%post
echo "{{VAR.role}} in rack {{VAR.rack}} ({{CLIENT_GROUP}})" > /etc/machine-role
%end
```

**Clients** 下的 CSV 导入不携带变量;CSV 行更新客户端时,现有变量会保留。

## 预览

**Auto-Install** 下的编辑器为任何已保存的文件提供 **预览** 操作。选择一个客户端,并可选地选择一个镜像,面板会以只读方式显示 Bootimus 将发送给该机器的确切文本,并用警告列出所有仍未解析的占位符。不选客户端时,客户端和组的值渲染为空;不选镜像时,`IMAGE_*` 占位符保持未解析。`{{CALLBACK_URL}}` 和 `{{INSTALL_TOKEN}}` 使用固定令牌 `preview` 渲染,因为不会创建安装记录。

同样的渲染也可以通过 API 获得:

```bash
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"
```

```json
{"success":true,"data":{"content":"#cloud-config\n...","unresolved":["VAR.disk"]}}
```

`mac` 和 `image` 都是可选的。渲染不会写入任何内容:不会创建安装记录,也不会改变安装的状态。

## 校验

从编辑器保存或上传文件时,Bootimus 会在写入之前按扩展名检查其格式是否正确:

| 扩展名 | 检查 |
|-----------|-------|
| `.yaml`、`.yml` | 作为 YAML 解析(允许多文档文件) |
| `.xml` | 格式正确的 XML |
| `.json` | 有效的 JSON |
| 其他 | 不检查 |

每个占位符都会先被替换为无害的占位值,因此 `hostname: {{HOSTNAME}}` 可以通过,尽管单独的 `{{HOSTNAME}}` 本身不是有效的 YAML。校验失败返回 `400`,附带解析器的消息以及(解析器提供时的)行号;编辑器会在内容下方内联显示,并提供 **仍然保存**,供你比解析器更清楚的情况使用(例如一个故意写成片段的文件)。通过 API,在保存请求体中传 `"force": true`,或在上传时以 multipart 字段传 `force=true`,即可跳过检查。

替换发生在无人值守安装端点上,因此对所有格式都生效。Windows 的 `autounattend.xml` 同样会按正在引导的客户端渲染,因为 wimboot 在引导时正是从该端点获取它(见 [Windows 注意事项](#windows-注意事项));只有那里描述的未渲染 SMB 回退副本会绕过替换。

## 示例

### Ubuntu Server (cloud-init)

`data/autoinstall/ubuntu/default.yaml`:

```yaml
#cloud-config
autoinstall:
  version: 1
  identity:
    hostname: {{HOSTNAME}}
    username: ubuntu
    password: "$6$rounds=4096$..."  # mkpasswd -m sha-512
  ssh:
    install-server: true
    allow-pw: false
    authorized-keys:
      - ssh-ed25519 AAAA...
  storage:
    layout:
      name: lvm
  late-commands:
    - curtin in-target -- systemctl enable --now serial-getty@ttyS0.service
```

只要附加了无人值守安装文件,Bootimus 就会自动添加该启动参数,无需在镜像的启动参数中手动添加。生成的参数如下:

```
autoinstall ds=nocloud-net;s=http://<server>:8080/autoinstall/<image-filename>/mac/<mac>/
```

### Debian (preseed)

`data/autoinstall/debian/server.cfg`:

```
d-i debian-installer/locale string en_GB.UTF-8
d-i keyboard-configuration/xkb-keymap select gb
d-i netcfg/get_hostname string {{HOSTNAME}}
d-i netcfg/get_domain string lan
d-i partman-auto/method string lvm
d-i partman-auto/choose_recipe select atomic
d-i passwd/root-login boolean false
d-i passwd/user-fullname string Admin
d-i passwd/username string admin
d-i passwd/user-password-crypted password $6$rounds=4096$...
d-i pkgsel/include string openssh-server
d-i grub-installer/bootdev string default
d-i finish-install/reboot_in_progress note
```

### Rocky / Fedora (kickstart)

`data/autoinstall/rocky/workstation.ks`:

```
text
lang en_GB.UTF-8
keyboard gb
timezone Europe/London --utc
network --bootproto=dhcp --hostname={{HOSTNAME}}
rootpw --lock
user --name=admin --groups=wheel --password=$6$rounds=4096$... --iscrypted
sshkey --username=admin "ssh-ed25519 AAAA..."
bootloader --location=mbr
clearpart --all --initlabel
autopart --type=lvm
%packages
@^minimal-environment
openssh-server
%end
```

### Windows 11 / Server (autounattend)

`data/autoinstall/windows/kiosk.xml`:标准 `<unattend>` 文档 — 参见 [微软的 autounattend 参考](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/)。当客户端通过 wimboot 引导时(Bootimus 正是这样引导 Windows 镜像的),占位符在任何文本节点内都生效:

```xml
<ComputerName>{{HOSTNAME}}</ComputerName>
```

### openSUSE Leap 15 / SLES 15 (AutoYaST)

`data/autoinstall/opensuse/leap15.xml`:标准 AutoYaST profile — 参见 [AutoYaST 指南](https://doc.opensuse.org/projects/autoyast/)。Bootimus 会在内核命令行中添加 `autoyast=<url>`。openSUSE profile 已经传入了指向提取后 ISO 的 `install=`,正是它负责启用网络并提供安装仓库。

```xml
<?xml version="1.0"?>
<!DOCTYPE profile>
<profile xmlns="http://www.suse.com/1.0/yast2ns" xmlns:config="http://www.suse.com/1.0/configns">
  <networking>
    <dns>
      <hostname>{{HOSTNAME}}</hostname>
    </dns>
  </networking>
  <users config:type="list">
    <user>
      <username>root</username>
      <user_password>$6$rounds=4096$...</user_password>
      <encrypted config:type="boolean">true</encrypted>
    </user>
  </users>
</profile>
```

### openSUSE Leap 16 / Tumbleweed (Agama)

`data/autoinstall/opensuse/leap16.jsonnet`(或 `.json`):一份 [Agama profile](https://agama-project.github.io/docs/user/profile)。Bootimus 会添加 `inst.auto=<url>`;无人值守安装时 Agama 不再需要命令行上的其他任何参数。

```jsonnet
{
  product: { id: 'Leap_16.1' },
  user: {
    fullName: 'Admin',
    userName: 'admin',
    hashedPassword: true,
    password: '$6$rounds=4096$...',
  },
  localization: { language: 'en_GB.UTF-8', keyboard: 'gb', timezone: 'Europe/London' },
  network: { connections: [{ id: 'wired', method4: 'auto' }] },
  storage: { drives: [{ partitions: [{ generate: 'default' }] }] },
  scripts: {
    post: [{ name: 'report', body: 'curl -fsS "{{CALLBACK_URL}}"' }],
  },
}
```

Agama 只认 `inst.auto`;如果你的 Leap 16 或 Tumbleweed 介质引导的是 Agama,而你挂载的是 `.xml` 文件,Bootimus 会传入 `autoyast=`,安装程序则会忽略它。AutoYaST profile 请留给 Leap 15 和 SLES 15 介质使用。

### Fedora CoreOS / Flatcar (Ignition)

`data/autoinstall/coreos/worker.ign` 或 `data/autoinstall/flatcar/worker.ign`:一份 [Ignition](https://coreos.github.io/ignition/) JSON 文档,通常由 Butane 生成。Bootimus 会为 Fedora CoreOS 添加 `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal`,为 Flatcar 添加 `flatcar.first_boot=1 ignition.config.url=<url>`。`coreos` profile 还会传入 `coreos.live.rootfs_url=`,指向从 live ISO 中提取的 `rootfs.img`。

```json
{
  "ignition": { "version": "3.4.0" },
  "passwd": {
    "users": [{ "name": "core", "sshAuthorizedKeys": ["ssh-ed25519 AAAA..."] }]
  },
  "storage": {
    "files": [{
      "path": "/etc/hostname",
      "mode": 420,
      "contents": { "source": "data:,{{HOSTNAME}}" }
    }]
  }
}
```

这样引导的 live 系统会应用你的配置。若要安装到磁盘,请在镜像的引导参数中加入 `coreos.inst.install_dev=/dev/sda coreos.inst.ignition_url={{BASE_URL}}/autoinstall/{{FILENAME}}?mac={{MAC}}`,让 `coreos-installer` 把同一份配置写入已安装的系统。Ignition 仍会先把配置应用到 live 环境,因此这适合创建用户、密钥和文件的配置;对磁盘分区或格式化的配置会在 live 系统中失败,只应通过 `coreos.inst.ignition_url` 分发。

### Proxmox VE (应答文件)

Proxmox VE 从 `answer.toml` 安装([自动化安装](https://pve.proxmox.com/wiki/Automated_Installation))。原版安装程序不会从内核命令行读取应答文件的 URL,因此必须先用 `proxmox-auto-install-assistant` 对 ISO 做一次准备,让它通过 HTTP 从 Bootimus 获取应答文件:

1. 在任意 Debian 或 Proxmox 主机上安装 `proxmox-auto-install-assistant` 并重新制作 ISO,把获取 URL 指向 Bootimus 无人值守安装端点上你即将上传的文件名:

   ```bash
   proxmox-auto-install-assistant prepare-iso proxmox-ve_9.1-1.iso \
     --fetch-from http \
     --url "http://<server>:8080/autoinstall/proxmox-ve_9.1-1-auto-from-http.iso"
   ```

   该助手会在源文件旁边写出 `proxmox-ve_9.1-1-auto-from-http.iso`。URL 中必须包含你上传的 ISO 的文件名,所以两者要保持一致。

2. 通过 **Images** 上传重新制作的 ISO。`proxmox` profile 会提取 `/boot/linux26` 和 `/boot/initrd.img`,引导菜单则把 ISO 本身作为第二个 initrd(`proxmox.iso`)加载,这正是官方 iPXE 流程提供安装介质的方式。

3. 创建 `data/autoinstall/proxmox/answer.toml` 并把它挂载到镜像、客户端或组:

   ```toml
   [global]
   keyboard = "en-gb"
   country = "gb"
   fqdn = "{{HOSTNAME}}.lan"
   mailto = "root@lan"
   timezone = "Europe/London"
   root-password-hashed = "$6$rounds=4096$..."
   root-ssh-keys = ["ssh-ed25519 AAAA..."]

   [network]
   source = "from-dhcp"

   [disk-setup]
   filesystem = "zfs"
   zfs.raid = "raid1"
   disk-list = ["sda", "sdb"]

   [post-installation-webhook]
   url = "{{CALLBACK_URL}}"
   ```

引导时,安装程序会把一份描述该机器的 JSON(产品、ISO 版本、DMI 数据,以及带有各接口 `link` 和 `mac` 的 `network_interfaces`)以 `POST` 发送到该 URL。Bootimus 读取其中的接口 MAC,按第一个匹配到已知客户端的 MAC 解析客户端(否则回退到镜像默认值),渲染挂载的 `.toml` 并以 `application/toml` 返回。`{{MAC}}` 即匹配到的那个 MAC。`[post-installation-webhook]` 段让安装程序在安装完成后调用 `{{CALLBACK_URL}}`,从而把该客户端的安装记录转为 `completed`。

此类型不会向内核命令行添加任何内容。上游已有人请求增加 `proxmox-answer=<url>` 内核参数([Proxmox bug 8219](https://bugzilla.proxmox.com/show_bug.cgi?id=8219)),但尚未发布;一旦发布,重新制作 ISO 的步骤就不再需要了。

## 不支持的发行版

有些发行版没有任何办法从内核命令行获取无人值守安装文件,因此 Bootimus 不会假装支持它们。这些发行版的文件仍会作为 `generic` 提供,但不会有任何东西告知安装程序:

- **Alpine Linux** — `setup-alpine -f <answerfile>` 是引导之后才运行的命令;安装程序没有可通过内核参数获取应答文件的钩子。
- **NixOS** — 安装由 `nixos-install` 驱动,依赖你写入目标系统的配置;live ISO 没有无人值守安装程序。
- **FreeBSD** — `bsdinstall` 脚本从本地介质或环境变量 `BSDINSTALL_SCRIPT` 读取,而不是从 loader 命令行上的 URL。
- **Arch Linux** — live ISO 没有安装程序;`archinstall` 可以接受配置文件,但必须手动或通过自定义 profile 获取并运行。

对于这些发行版,请改为引导 live 镜像,然后通过 SSH 会话或自定义脚本运行安装程序。

## 报告安装完成

Bootimus 从客户端加载镜像内核的那一刻起就跟踪每一次无人值守安装。记录会经历四种状态:

| 状态 | 设置时机 |
|------|----------|
| `booted` | 客户端已获取镜像的引导文件,且该镜像带有自动安装文件或内联脚本 |
| `installing` | 安装程序已获取自动安装文件 |
| `completed` | 已安装的系统调用了 `{{CALLBACK_URL}}` |
| `failed` | 回调报告了 `status=failed`,或在超时内没有任何回报 |

当前状态显示在 Clients 表的 **Install** 列,完整历史位于客户端编辑器的 **Install History** 下。从未回报的安装会在六小时后标记为失败;可用 `--install-timeout-hours` 或 `bootimus.yaml` 中的 `install_timeout_hours` 更改。只要在 **Settings → Webhook** 下启用了相应事件,每次转为 `completed` 或 `failed` 都会触发 `install.completed` 或 `install.failed` webhook 事件。

回调是一个发往引导端口的普通 HTTP 请求,`GET` 或 `POST` 均可:

```
http://<server>:8080/install/callback?token=<token>&status=completed|failed&message=<optional text>
```

`{{CALLBACK_URL}}` 已经包含了令牌,所以脚本只需追加 `&status=failed&message=...`。每个令牌只有第一次报告算数;之后的调用会返回 `already recorded`。

### Kickstart(`%post`)

```
%post --log=/root/ks-post.log
curl -fsS "{{CALLBACK_URL}}" || curl -fsS "{{CALLBACK_URL}}&status=failed&message=post-script"
%end
```

### Preseed(`late_command`)

```
d-i preseed/late_command string in-target sh -c 'wget -qO- "{{CALLBACK_URL}}" || curl -fsS "{{CALLBACK_URL}}"'
```

### Ubuntu autoinstall / cloud-init

`phone_home` 模块会在首次启动后发送请求,这是最可靠的信号:

```yaml
#cloud-config
autoinstall:
  version: 1
  user-data:
    phone_home:
      url: "{{CALLBACK_URL}}"
      post: [instance_id, hostname]
      tries: 10
```

或者用 `late-commands` 从安装程序环境中报告:

```yaml
  late-commands:
    - curtin in-target --target=/target -- curl -fsS "{{CALLBACK_URL}}"
```

### Windows(`autounattend.xml`)

在 `Microsoft-Windows-Shell-Setup` 下添加一条首次登录命令:

```xml
<FirstLogonCommands>
  <SynchronousCommand wcm:action="add">
    <Order>1</Order>
    <CommandLine>powershell -NoProfile -Command "Invoke-WebRequest -UseBasicParsing '{{CALLBACK_URL}}'"</CommandLine>
    <Description>Report install to Bootimus</Description>
  </SynchronousCommand>
</FirstLogonCommands>
```

请记住,如果追加 `status` 或 `message` 参数,XML 属性或文本中的 `&` 必须写成 `&amp;`。

### Talos 和 Flatcar 节点

通过 **Kubernetes** 标签页注册的 Kubernetes 节点已经通过机器配置流程以及 `node.install_started` / `node.install_completed` 事件进行报告。它们不需要回调。

### 查询历史

```bash
# History for one client
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/installs?mac=b4:2e:99:01:5f:a3"

# Recent installs across the fleet
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/installs?limit=50"
```

## Windows 注意事项

Windows 镜像通过 [wimboot](https://ipxe.org/wimboot) 引导,它会把 iPXE 脚本中每个额外的 `initrd` 文件注入 WinPE 的 RAM 盘。Bootimus 借此按客户端分发无人值守安装文件:

1. 当正在引导的客户端解析到某个 autounattend 文件时(按常规的[解析顺序](#解析顺序):客户端、然后客户端组、然后镜像),生成的 iPXE 引导段会在 `boot.wim` 之前加入 `initrd http://<server>:<port>/autoinstall/<iso>?mac=<mac> AutoUnattend.xml`。
2. 无人值守安装端点用该客户端的占位符渲染文件,与 Linux 安装程序完全一致。
3. wimboot 把文件放在 WinPE 内的 `X:\Windows\System32\AutoUnattend.xml`。
4. 打过补丁的 `startnet.cmd` 在那里找到它,并以 `setup.exe /unattend:X:\Windows\System32\AutoUnattend.xml` 启动 Setup。

由于文件是按客户端解析和渲染的,按客户端和按客户端组的覆盖以及 `{{HOSTNAME}}` 之类的占位符在 Windows 上和其他地方一样有效。引导参数仍会传给 wimboot;请避免 `rawwim`,它会关闭文件注入。

**SMB 回退。** 当为一个在镜像级别挂载了 autounattend 文件的镜像打补丁 `boot.wim` 时,Bootimus 还会把该文件以未渲染的形式作为 `AutoUnattend.xml` 暂存到该镜像的 SMB 共享上。只有当 wimboot 没有注入任何文件时,例如客户端通过 Bootimus iPXE 菜单之外的其他途径引导了打过补丁的 `boot.wim`,`startnet.cmd` 才会把它复制到 `X:\AutoUnattend.xml`。该回退没有占位符替换,也没有按客户端或按客户端组的覆盖:其中的占位符会原样传给 Setup。

如果任何级别都没有挂载 autounattend 文件,Setup 会和以前一样以交互模式运行。

**数据目录要求。** Bootimus 运行自己的 `smbd`,其状态保存在 `data/smb` 下。如果该目录位于无法保存 Unix 权限的文件系统上(exFAT、NTFS 或 CIFS 挂载),Samba 会拒绝启动,因为其锁目录会被读成 `0777` 而不是 `0755`。请把数据目录放在 ext4 或 XFS 等原生 Linux 文件系统上,并确保主机上没有其他 SMB 服务监听 445 端口。Bootimus 会在启动时检查这两个条件,并在 **Server Info** → **Windows SMB** 中报告。

**重启韧性。** WinPE 在安装中途会重启并从同一个客户端 IP 重新连接。内置的 Samba 配置设置了 `reset on zero vc = yes` 并禁用了 oplocks,这样第二次 `net use` 不会被陈旧的会话状态卡住。如果你用自己的 `data/smb/smb.conf` 替换了默认配置,请同步这些设置。

**撤销补丁。** 第一次打 SMB 补丁时,会在旁边保留一份原始的 `boot.wim`,命名为 `boot.wim.orig`。**Images** → **移除 SMB 补丁** 会恢复原始文件并删除该镜像的 SMB 共享——之后 ISO 的引导行为与刚上传时完全一致。由旧版 Bootimus 打补丁的镜像没有备份:请先重新提取镜像(会生成一份新的原始副本),再移除补丁。

## REST API

UI 里的每件事也都是一次 REST 调用。

```bash
# List all auto-install files
curl -u admin:pw http://localhost:8081/api/autoinstall-files

# Read a file
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/get?distro=ubuntu&filename=default.yaml"

# 创建或覆盖文件(YAML、XML 或 JSON 格式错误时返回 400;加上 "force":true 可仍然保存)
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/save \
  -H "Content-Type: application/json" \
  -d '{"distro":"ubuntu","filename":"default.yaml","content":"#cloud-config\n..."}'

# 为某个客户端和镜像渲染文件,并列出未解析的占位符
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"

# Upload a file
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/upload \
  -F "distro=windows" \
  -F "filename=kiosk.xml" \
  -F "file=@./kiosk.xml"

# Download
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/download?distro=ubuntu&filename=default.yaml" -o default.yaml

# Delete
curl -u admin:pw -X POST "http://localhost:8081/api/autoinstall-files/delete?distro=ubuntu&filename=default.yaml"
```

将文件挂载到镜像:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/images/update \
  -H "Content-Type: application/json" \
  -d '{"filename":"ubuntu-24.04-live-server-amd64.iso","auto_install_file":"ubuntu/default.yaml"}'
```

将文件挂载到客户端:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/clients/b4:2e:99:01:5f:a3 \
  -H "Content-Type: application/json" \
  -d '{"auto_install_file":"ubuntu/lab-bench.yaml"}'
```

客户端在启动时访问的无人值守安装端点:

```
GET  /autoinstall/<image-filename>?mac=<mac>             # preseed, kickstart, autoyast, agama, ignition
GET  /autoinstall/<image-filename>/mac/<mac>/user-data   # Ubuntu autoinstall
GET  /autoinstall/<image-filename>/mac/<mac>/meta-data   # Ubuntu autoinstall
POST /autoinstall/<image-filename>                       # Proxmox VE (JSON system information in the body)
```

引导菜单会用客户端的 MAC 地址构造这些 URL,以便按客户端覆盖能正确解析;对于 Ubuntu,cloud-init 会自行追加 `user-data` 和 `meta-data`。Proxmox 安装程序并不知道 Bootimus 用哪个 MAC 引导了它,所以它会以 `POST` 发送自己的网络接口,由 Bootimus 与 Clients 表进行匹配。不带 MAC 地址或其他形式的 `GET` 请求(例如 `/autoinstall/<image-filename>/user-data`)会返回 404。

## 故障排查

### `/autoinstall/...` 返回 404

`no auto-install configuration for this image/client` — 解析链的任何层级都没有挂载文件。要么挂载到镜像、客户端或其所属组,要么检查 `auto_install_file` 是否真的指向 `data/autoinstall/` 下存在的某个文件。

### 占位符按字面输出

`{{HOSTNAME}}` 在已安装系统中显示为字面字符串,意味着文件在替换运行之前就被分发了 — 通常是因为客户端仅通过 IP 启动而请求未携带 `mac` 查询参数。确认引导菜单生成的 URL 形如 `/autoinstall/<iso>?mac=<mac>` 或 `/autoinstall/<iso>/mac/<mac>/`。

在 Windows 上,占位符按字面输出意味着 Setup 使用了 SMB 共享上暂存的未渲染副本,而不是 wimboot 注入的文件。预览该客户端的引导菜单(见[预览客户端的引导菜单](clients.md#previewing-a-clients-boot-menu)),检查 Windows 段中是否包含 `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml` 这一行,并确认镜像的引导参数中没有 `rawwim`。

`{{VAR.<name>}}` 保持字面值,意味着客户端及其组都没有定义该变量,或者名称拼写不同(名称区分大小写)。每个未解析的占位符都会在分发文件时记录到日志,编辑器的 **预览** 也会在引导前针对所选客户端列出它们。

### 分发了错误的文件

解析是"最具体优先"。如果一个客户端有自己的覆盖而你并没意识到,这就是为什么镜像级默认没有被使用。检查服务器日志这一行:

```
Served auto-install script for ... (source: client:..., type: ..., size: ...)
```

`source:` 字段告诉你究竟是哪个槽位胜出。

### Proxmox 安装程序提示无法获取应答文件

重新制作的 ISO 会从 `prepare-iso` 烧录进去的精确 URL 获取文件。请检查该 URL 中的文件名与上传的 ISO 一致(重新制作后再重命名 ISO 会导致失效)、客户端能访问 8080 端口,以及解析链的某个层级挂载了 `.toml` 文件。服务器日志会显示该 `POST` 和解析到的来源;`400` 表示请求体中没有 `network_interfaces`,这说明安装程序版本非常老。

### Windows Setup 交互式运行

- 正在引导的客户端必须能解析到一个 autounattend 文件:把它挂载到镜像(镜像属性 → Auto-Install)、客户端或其所属客户端组。
- 预览该客户端的引导菜单,确认 Windows 段包含 `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml` 这一行。若没有,说明该 MAC 没有解析到任何 autounattend 文件。
- 确保镜像的引导参数中没有 `rawwim`;它会阻止 wimboot 向 WinPE 注入文件。
- 升级 Bootimus 后重新打补丁 `boot.wim`,让 `startnet.cmd` 知道去查找注入的文件:**Images** → **Patch SMB**(或下次启动时会自动重新打补丁)。
- 确认 SMB 共享在客户端可达(在 WinPE 中执行 `net view \\<server>`);安装程序本身仍来自共享。

### "AutoUnattend.xml neither injected by wimboot nor staged on the share, running interactive setup"

由 `startnet.cmd` 在镜像挂载了 autounattend 文件但两条分发路径都没有产生文件时记录。检查 Bootimus 日志中客户端引导时的 `/autoinstall/<iso>?mac=...` 请求(404 表示该客户端没有解析到文件),以及打补丁时段附近的暂存步骤。从镜像属性重新执行 SMB 补丁。

### `Z:\setup.exe not found on the share`

`startnet.cmd` 已映射 `Z:`,但它打印的目录列表显示的是镜像文件夹(`iso\`、`metadata.txt`)而不是安装文件。Bootimus 始终共享镜像的 `iso` 子文件夹,所以客户端连接到了同一地址上的另一个 SMB 服务。检查 Bootimus 日志中是否有 `SMB: smbd exited`,并查看 **Server Info** 下的 **Windows SMB** 一行。常见原因是数据目录位于没有 Unix 权限的文件系统上,这会让 `smbd` 以 `invalid permissions on directory '.../smb/locks/msg.lock': has 0777 should be 0755` 退出,转而由系统的 Samba 在 445 端口应答。把数据目录移到 ext4 或 XFS,停止另一个 Samba,然后重启 Bootimus。

### `net use fails after VM reboot`

在 0.1.58 通过在内置 Samba 配置中启用 `reset on zero vc = yes` 已修复。如果你维护着自定义 `smb.conf`,请加入:

```
reset on zero vc = yes
oplocks = no
kernel oplocks = no
level2 oplocks = no
strict locking = no
deadtime = 1
```

## 下一步

- 参见 [镜像管理](images.md) 了解如何将文件挂载到镜像。
- 参见 [客户端管理](clients.md) 了解按客户端覆盖。
- 参见 [发行版 Profile](distro-profiles.md) 了解映射到文件库子目录的底层 profile ID。
