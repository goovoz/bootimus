# bootimus（fork）

[Bootimus](https://github.com/garybowers/bootimus) 的 PXE / HTTP 启动服务器镜像，自用分支。
功能与上游一致，另外叠了几个还没提上游的补丁；上游有新提交会自动合并进来。

## 相比上游多了什么

| 加的东西 | 说明 |
|---|---|
| 中文界面 | 管理台约 870 条文案汉化，app.js 动态渲染出来的文本也跟着翻 |
| 下载走代理 | 设置页填代理地址（可带凭据），下载 ISO 时按开关走代理 |
| 断点续传 | 先写 `.part` + ETag，断了用 Range 续；启动时清 7 天前残留 |
| 不带后缀的 ISO 链接 | 探测 Content-Type、按响应头取名，不用手工补 `.iso` |
| 自动安装模板 | 4 套模板 + 变量表单 + 预览，生成结果直接进文件库 |
| 镜像目录自动跟随 | URL 失效时按目录里最新的同类文件改写，结果缓存 15 分钟 |

## docker compose

PXE 要应答 DHCP、监听 TFTP，所以用 host 网络：

```yaml
services:
  bootimus:
    image: deemop/bootimus:latest
    container_name: bootimus
    network_mode: host
    environment:
      BOOTIMUS_SERVER_ADDR: 192.168.1.100      # 改成这台机的业务 IP
      BOOTIMUS_DATA_DIR: /data
      BOOTIMUS_PROXY_DHCP_ENABLED: "true"      # 内网已有 DHCP 时，用它应答 PXE 请求
      BOOTIMUS_BIND_INTERFACE: eth0
    volumes:
      - ./data:/data
    cap_add:
      - NET_BIND_SERVICE
    restart: unless-stopped
```

```bash
docker compose up -d
docker logs bootimus | grep Password    # 初始管理密码
```

管理台是 `http://192.168.1.100:8081`；PXE 客户端走 69/udp 和 8080/tcp。

不需要 PXE / DHCP 代理的话，去掉 `network_mode`，换成端口映射：

```yaml
    ports:
      - "69:69/udp"
      - "8080:8080/tcp"
      - "8081:8081/tcp"
```

装 Windows 无人值守要再开 SMB：加 `BOOTIMUS_WINDOWS_SMB_ENABLED: "true"` 和 `- "445:445/tcp"`。

## tag

| tag | 说明 |
|---|---|
| `0.1.78-g<sha>` | 不可变，对应一次构建 |
| `0.1.78` | 该版本最新的一次构建 |
| `latest` | 最新的构建 |

`linux/amd64` + `linux/arm64`。基线版本跟上游对齐，`+g<sha>` / `-g<sha>` 是本仓库的构建修订。

补丁清单、同步机制在仓库的 [docs/FORK-PATCHES.md](https://github.com/goovoz/bootimus/blob/main/docs/FORK-PATCHES.md)。
