# 身份认证指南

Bootimus 使用 JWT (JSON Web Token) 对管理面板进行身份认证。你也可以选择连接 LDAP 或 Active Directory 服务器,或者连接 Authentik、Keycloak 等 OpenID Connect (OIDC) 身份提供商来实现单点登录。

## 目录

- [本地认证](#本地认证)
- [登录流程](#登录流程)
  - [双因素认证](#双因素认证)
- [角色](#角色)
- [API 认证](#api-认证)
  - [API 令牌](#api-令牌)
- [LDAP / Active Directory](#ldap--active-directory)
- [OIDC / 单点登录](#oidc--单点登录)
- [来自 LDAP 和 OIDC 的用户](#来自-ldap-和-oidc-的用户)
- [配置参考](#配置参考)
- [故障排查](#故障排查)

## 本地认证

默认情况下,Bootimus 使用存储在数据库中的本地用户账号(SQLite 或 PostgreSQL)。

### 默认管理员账号

首次启动时,系统会生成一个随机密码并打印到服务器日志中:

```
╔════════════════════════════════════════════════════════════════╗
║                    ADMIN PASSWORD GENERATED                    ║
╠════════════════════════════════════════════════════════════════╣
║  Username: admin                                               ║
║  Password: AbCdEfGh1234567890-_XyZ123456                       ║
╠════════════════════════════════════════════════════════════════╣
║  This password will NOT be shown again!                        ║
║  Save it now or reset it using --reset-admin-password flag     ║
╚════════════════════════════════════════════════════════════════╝
```

### 重置管理员密码

重置为一个新的随机密码(会启动完整的服务器实例):
```bash
./bootimus serve --reset-admin-password
# 或使用 Docker
docker exec bootimus /bootimus serve --reset-admin-password
```

或者直接在数据库中设置指定的密码,无需绑定任何端口(适合紧急恢复):
```bash
# 交互式提示(隐藏输入,避免密码进入 shell 历史)
./bootimus user set-password admin

# 或以非交互方式提供,便于脚本使用
./bootimus user set-password admin --password '新密码'
```

### 用户管理

可以从管理面板的 **Users** 标签创建额外的用户。每个用户具有:
- **Username**:唯一的登录名
- **Password**:以 bcrypt 哈希存储
- **Admin**:用户是否为管理员;其他用户为只读(参见 [角色](#角色))
- **Enabled**:可禁用而无需删除
- **Source**:`Local`、`LDAP` 或 `OIDC` — 账号的来源(参见 [来自 LDAP 和 OIDC 的用户](#来自-ldap-和-oidc-的用户))

也可以在不启动服务器的情况下通过 CLI 管理用户(便于恢复)。这些命令直接作用于
所配置的数据库(SQLite 或 PostgreSQL):
```bash
./bootimus user list                       # 列出所有用户及其来源
./bootimus user enable <username>          # 启用账号
./bootimus user disable <username>         # 禁用账号
./bootimus user set-admin <username>       # 授予管理员权限
./bootimus user unset-admin <username>     # 撤销管理员权限
./bootimus user set-password <username>    # 设置密码(交互提示,或使用 --password)
```

## 登录流程

1. 访问 `http://your-server:8081`
2. 显示登录页面,带用户名和密码字段
3. 如果配置了 LDAP,会出现一个认证下拉框用于选择后端
4. 如果配置了 OIDC,表单下方会出现 **Sign in with …** 按钮
5. 登录成功后,签发一个 JWT token(有效期 24 小时)
6. token 存储在浏览器,并随所有 API 请求一起发送
7. 退出登录或 token 过期后,再次显示登录页

### 双因素认证

本地账号可以添加基于时间的一次性密码(TOTP)作为第二因素。此处不向 LDAP 和 OIDC 用户提供该功能;第二因素由它们的身份提供商负责。

**启用。** 打开 **Users** → **Two-Factor Authentication**,点击 **Set up**,用身份验证器应用扫描二维码(或手动输入密钥),然后用应用生成的验证码确认。随后 Bootimus 会显示十个一次性恢复码。请妥善保存,它们不会再次显示。

**登录。** 密码正确后,登录页会要求输入六位验证码。恢复码可以代替验证码使用,每个只能用一次。错误的验证码会与错误密码一样计入[暴力破解防护](#暴力破解防护)。

**丢失设备。** 管理员可以点击该用户旁边的 **Reset 2FA**,或调用 `POST /api/users/totp/reset?id=<id>`。如果唯一的管理员被锁在外面,请在服务器上运行:

```bash
bootimus user reset-totp admin
```

**通过 API。** 脚本应使用 [API 令牌](#api-令牌),而不是密码加 TOTP。如果确实要用受 TOTP 保护的账号登录,`POST /api/login` 会先返回 `401` 和 `{"error":"totp_required","totp_required":true}`;用相同的凭据加上 `"code":"123456"` 重复该请求即可。

### 暴力破解防护

密码登录(本地和 LDAP)在内存中进行速率限制:

- 同一用户名在 15 分钟内 **失败 5 次** 后,该用户名被锁定 1 分钟。只要近期的失败仍在窗口内,之后每再失败一次,锁定时间就翻倍,最长 15 分钟。
- 同一客户端地址在 15 分钟内 **失败 20 次** 后,该地址按同样的规则被锁定。更高的阈值意味着共享的反向代理地址不会因为某一个人的输错而被锁定。
- 登录成功会清除该用户名的失败计数。

锁定期间,API 返回 `429 Too Many Requests` 并带有 `Retry-After` 头,登录页会显示需要等待的时间。错误的凭据仍然返回通常的 `401`,因此响应不会泄露用户名是否存在。失败的尝试会连同用户名和客户端地址写入服务器日志。重启 Bootimus 会清除计数器。OIDC 登录经由身份提供商完成,不计入其中。

## 角色

Bootimus 有两种角色:

| 角色 | 访问权限 |
|------|--------|
| **Admin** | 拥有全部权限。 |
| **Read-only** | 可以查看仪表盘、客户端(包括硬件清单、启动历史和 BMC 电源状态)、客户端组、镜像、公共文件、发行版配置、启动菜单、引导加载程序、工具、Kubernetes 集群和节点、计划任务以及启动尝试历史。 |

只读用户:

- 无法更改任何内容 — 服务器会拒绝每一次写入并返回 `403 Your account has read-only access`,管理面板也会隐藏相应的按钮
- 无法查看用户、webhook 设置、备份、审计日志、实时服务器日志、自动安装文件和脚本,以及集群机器配置
- 永远不会收到 BMC (IPMI/Redfish) 密码或客户端配置补丁 — 发送给他们的每个 API 响应中都会移除这些内容

满足以下条件的用户是管理员:

- **Local**:其账号勾选了 **Administrator** 复选框
- **LDAP**:匹配 `BOOTIMUS_LDAP_GROUP_FILTER`(或未设置组过滤器)
- **OIDC**:属于 `BOOTIMUS_OIDC_ADMIN_GROUPS` 中的某个组,或列在 `BOOTIMUS_OIDC_ADMIN_USERS` 中

其他所有能够登录的用户都是只读的。`GET /api/me` 会返回当前登录用户的角色。

> **升级须知:** 在添加只读访问之前,未勾选 **Administrator** 复选框的本地用户可以登录,但看不到任何内容。现在他们拥有只读访问权限。请禁用你不希望其拥有该权限的此类账号。

## API 认证

所有 API 端点(`/api/login`、`/api/auth-info` 和 `/api/oidc/` 登录端点除外)都需要有效的 JWT token。只读用户只能发起 `GET` 请求,并且只能访问 [角色](#角色) 中列出的端点。

### 获取 token

```bash
# Login and get token
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}' | jq -r '.data.token')

echo $TOKEN
```

### 使用 token

```bash
# Include in all API requests
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients

# Example: list images
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images
```

### Token 细节

- **算法**:HMAC-SHA256
- **过期时间**:签发后 24 小时
- **密钥**:每次服务器启动时随机生成(重启后所有 token 失效)
- **声明**:用户名、管理员状态、签发时间、过期时间

### API 令牌

对于脚本、CI 任务和集成,请在 **Users** → **API Tokens** 下创建一个长期有效的令牌,而不是用密码登录。令牌以 `btk_` 开头,只在创建时显示一次,并以哈希形式存储。每个令牌都有一个作用域:

| 作用域 | 能做什么 |
|--------|----------|
| `read` | 对只读用户可见的同一批端点发起 `GET` 请求 |
| `admin` | 管理员能做的一切,但不能创建或吊销令牌 |

令牌可以设置以天为单位的有效期,并可随时吊销。审计日志会把用令牌做出的更改记录为 `token:<name>`,`/api/me` 则报告 `"principal": "token"`。

```bash
# Create a token (as an admin user)
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ansible","scope":"admin","expires_in_days":365}' \
  http://localhost:8081/api/tokens

# Use it exactly like a JWT
curl -H "Authorization: Bearer btk_..." http://localhost:8081/api/clients

# List and revoke
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/tokens
curl -X DELETE -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/tokens?id=3"
```

与 JWT 不同,API 令牌在 Bootimus 重启后仍然有效。

除非管理界面通过 HTTPS 提供服务,否则令牌以及用于获取它们的密码都会以明文传输。可以用 `--admin-tls` 启用 HTTPS,或在管理端口前放置一个终止 TLS 的反向代理;见[保护管理界面](deployment.md#保护管理界面)。

### 检查可用的认证后端

```bash
# No authentication required
curl http://localhost:8081/api/auth-info
```

响应:
```json
{
  "success": true,
  "data": [
    {"id": "local", "name": "Local"},
    {"id": "ldap", "name": "LDAP (dc.example.com)"},
    {"id": "oidc", "name": "Authentik", "type": "redirect", "url": "/api/oidc/login"}
  ]
}
```

## LDAP / Active Directory

Bootimus 支持把 LDAP 作为额外的认证后端。配置后,用户可以在登录页选择本地认证或 LDAP 认证。本地账号始终作为兜底。

### 工作原理

1. 用户在登录页选择 "LDAP" 并输入凭据
2. Bootimus 使用服务账号 (bind DN) 连接到 LDAP 服务器
3. 根据配置的过滤器搜索该用户
4. 尝试用提供的密码以找到的用户身份 bind
5. 若成功,检查组成员关系以确定管理员权限
6. 为其创建或更新本地用户记录(参见 [来自 LDAP 和 OIDC 的用户](#来自-ldap-和-oidc-的用户))
7. 签发 JWT token(与本地认证相同)

### Active Directory 示例

```bash
# Environment variables
export BOOTIMUS_LDAP_HOST=dc.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="service-account-password"
export BOOTIMUS_LDAP_USER_FILTER="(sAMAccountName=%s)"
export BOOTIMUS_LDAP_GROUP_FILTER="cn=bootimus-admins"
```

### OpenLDAP 示例

```bash
export BOOTIMUS_LDAP_HOST=ldap.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=readonly,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="readonly-password"
export BOOTIMUS_LDAP_USER_FILTER="(uid=%s)"
```

### LDAPS (TLS)

```bash
export BOOTIMUS_LDAP_HOST=ldaps.example.com
export BOOTIMUS_LDAP_PORT=636
export BOOTIMUS_LDAP_TLS=true

# Or use StartTLS on port 389
export BOOTIMUS_LDAP_HOST=ldap.example.com
export BOOTIMUS_LDAP_STARTTLS=true

# Skip certificate verification (not recommended for production)
export BOOTIMUS_LDAP_SKIP_VERIFY=true
```

### Docker Compose 示例

```yaml
services:
  bootimus:
    image: garybowers/bootimus:latest
    environment:
      BOOTIMUS_LDAP_HOST: dc.example.com
      BOOTIMUS_LDAP_BASE_DN: dc=example,dc=com
      BOOTIMUS_LDAP_BIND_DN: cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com
      BOOTIMUS_LDAP_BIND_PASSWORD: service-account-password
      BOOTIMUS_LDAP_USER_FILTER: (sAMAccountName=%s)
      BOOTIMUS_LDAP_GROUP_FILTER: cn=bootimus-admins
```

### 管理员组成员

如果设置了 `BOOTIMUS_LDAP_GROUP_FILTER`,仅匹配组的成员可获得管理员权限。组成员关系通过以下方式检查:

1. 用户对象上的 `memberOf` 属性
2. 如果没有 `memberOf`,则进行一次组搜索查询

如果**未设置** `BOOTIMUS_LDAP_GROUP_FILTER`,所有 LDAP 用户都获得管理员权限。

管理员权限会在每次登录时重新评估,因此把某人移出该组后,其下次登录时就会被降级。

### 通过 API 用 LDAP 登录

```bash
# Specify auth_method: "ldap"
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"jdoe","password":"ldap-password","auth_method":"ldap"}' | jq -r '.data.token')
```

## OIDC / 单点登录

Bootimus 可以通过任何 OpenID Connect 提供商登录用户 — Authentik、Keycloak、Zitadel、Authelia、Entra ID、Google Workspace 等。用户在登录页点击 **Sign in with …**,在提供商处完成认证,然后被送回 Bootimus 并已处于登录状态。账号会在首次登录时自动创建,管理员权限则取决于用户在提供商处所属的组。

### 工作原理

1. 用户在登录页点击 **Sign in with …**
2. Bootimus 将其重定向到提供商(带 PKCE 的授权码流程)
3. 用户在提供商处登录,提供商随后重定向回 `/api/oidc/callback`
4. Bootimus 用授权码换取 ID token,校验其签名、audience、过期时间和 nonce,并读取用户名和组声明
5. 如果配置了允许的组或用户,用户必须匹配其中之一(或是管理员)
6. 创建或更新本地用户记录(参见 [来自 LDAP 和 OIDC 的用户](#来自-ldap-和-oidc-的用户)),若用户属于管理员组或在管理员用户列表中则为管理员,否则为只读
7. 浏览器带着 JWT token 返回管理面板(与本地认证相同)

如果 ID token 中不包含用户名或组声明,Bootimus 还会向提供商的 userinfo 端点查询。

### 提供商设置

在你的提供商处创建一个 OIDC 客户端(有时称为应用或依赖方),配置如下:

| 设置 | 值 |
|---------|-------|
| 客户端类型 | Confidential(带客户端密钥) |
| 授权类型 | 授权码 |
| 重定向 URI | `https://bootimus.example.com/api/oidc/callback` — 用户访问管理面板所用的 URL,加上 `/api/oidc/callback` |
| Scopes | `openid`, `profile`, `email` |

请确保提供商在 `groups` 声明中包含用户所属的组。Bootimus 不会猜测自己的公开 URL,因此 `BOOTIMUS_OIDC_REDIRECT_URL` 必须与提供商处注册的重定向 URI 完全一致。

### Authentik 示例

1. 在 Authentik 中进入 **Applications → Providers**,创建一个 **OAuth2/OpenID Provider**:
   - **Name**:`bootimus`
   - **Authorisation flow**:你常用的显式或隐式同意流程
   - **Client type**:Confidential
   - **Redirect URIs/Origins**:`https://bootimus.example.com/api/oidc/callback`(strict)
   - **Scopes**:保留默认的 `openid`、`email` 和 `profile` 映射 — Authentik 的 `profile` 映射包含 `groups` 声明
   - **Subject mode**:任意;Bootimus 默认使用 `preferred_username` 作为用户名
2. 记下 **Client ID** 和 **Client Secret**
3. 进入 **Applications → Applications**,使用该提供商创建一个 slug 为 `bootimus` 的应用。可选地为其绑定策略或组,以控制谁能启动它。
4. 为 Bootimus 管理员创建组(例如 `bootimus-admins`),并可选地为其他被允许的用户创建组(例如 `bootimus-operators`)
5. 配置 Bootimus:

```bash
export BOOTIMUS_OIDC_ISSUER=https://authentik.example.com/application/o/bootimus/
export BOOTIMUS_OIDC_CLIENT_ID=<client id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_ALLOWED_GROUPS=bootimus-operators
export BOOTIMUS_OIDC_DISPLAY_NAME=Authentik
```

issuer URL 显示在 Authentik 提供商概览页面的 **OpenID Configuration Issuer** 中,并且总是以 `/application/o/<application slug>/` 结尾。

### Keycloak 示例

创建一个 confidential 的 OpenID Connect 客户端,启用 **Standard flow** 并设置上述重定向 URI。Keycloak 默认不发送组:请在客户端的专用 scope 中添加一个 **Group Membership** mapper,token 声明名称设为 `groups`,并关闭 **Full group path**。

```bash
export BOOTIMUS_OIDC_ISSUER=https://keycloak.example.com/realms/example
export BOOTIMUS_OIDC_CLIENT_ID=bootimus
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_DISPLAY_NAME=Keycloak
```

### Microsoft Entra ID 示例

Entra ID 在组声明中发送的是组 *ID* 而不是名称,因此映射角色最简单的方法是使用 **app roles**,Entra 会在 `roles` 声明中发送它们。

1. 在 [Microsoft Entra admin centre](https://entra.microsoft.com) 中进入 **App registrations → New registration**:
   - **Name**:`Bootimus`
   - **Supported account types**:*Accounts in this organisational directory only (Single tenant)*
   - **Redirect URI**:平台选 **Web**,填 `https://bootimus.example.com/api/oidc/callback`
2. 在概览页面记下 **Application (client) ID** 和 **Directory (tenant) ID**
3. 在 **Certificates & secrets** 下创建一个 **New client secret**,并复制其 **Value**
4. 在 **App roles** 下创建两个角色,**Allowed member types** 均为 *Users/Groups*:
   - Display name 为 `Bootimus Admin`,value 为 `Bootimus.Admin`
   - Display name 为 `Bootimus Read-only`,value 为 `Bootimus.ReadOnly`
5. 进入 **Enterprise applications → Bootimus → Users and groups**,把用户或组分配给这些角色。分配组需要 Entra ID P1 或 P2;免费版请逐个分配用户。
6. 可选:在 **Enterprise applications → Bootimus → Properties** 下,把 **Assignment required?** 设为 **Yes**,这样只有已分配的用户才能登录
7. 配置 Bootimus:

```bash
export BOOTIMUS_OIDC_ISSUER=https://login.microsoftonline.com/<tenant id>/v2.0
export BOOTIMUS_OIDC_CLIENT_ID=<application id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret value>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_GROUPS_CLAIM=roles
export BOOTIMUS_OIDC_ADMIN_GROUPS=Bootimus.Admin
export BOOTIMUS_OIDC_ALLOWED_GROUPS=Bootimus.ReadOnly
export BOOTIMUS_OIDC_DISPLAY_NAME=Microsoft
```

请保留默认的用户名声明 `preferred_username`,它是用户的登录名(例如 `alice@example.com`)。不要使用 `email`:Entra 不会验证它,而 Bootimus 会拒绝提供商未标记为已验证的电子邮件用户名。

如果你更想使用安全组,请在 **Token configuration** 下添加一个 **groups claim**,并把这些组的 **Object IDs** 填入 `BOOTIMUS_OIDC_ADMIN_GROUPS` 和 `BOOTIMUS_OIDC_ALLOWED_GROUPS`,同时让 `BOOTIMUS_OIDC_GROUPS_CLAIM` 保持为 `groups`。

### Google 示例

Google 不会发送组成员关系,也没有 `preferred_username` 声明,因此使用 Google 时,请把电子邮件地址用作用户名,并通过用户列表和域名列表而不是组来授予访问权限。

1. 在 [Google Cloud console](https://console.cloud.google.com) 中选择或创建一个项目,进入 **APIs & Services → OAuth consent screen**(在较新版本的控制台中称为 **Google Auth Platform**):
   - **User type**:如果使用 Google Workspace,请选 *Internal*,这样只有你所在组织的账号可以登录。*External* 允许任何 Google 账号,因此在这种情况下务必设置 `BOOTIMUS_OIDC_ALLOWED_USERS`。
   - 添加 `openid`、`email` 和 `profile` scopes
2. 进入 **APIs & Services → Credentials → Create credentials → OAuth client ID**:
   - **Application type**:Web application
   - **Authorised redirect URIs**:`https://bootimus.example.com/api/oidc/callback`(除 `http://localhost` 外,Google 只接受 HTTPS)
3. 记下 **Client ID** 和 **Client secret**
4. 配置 Bootimus:

```bash
export BOOTIMUS_OIDC_ISSUER=https://accounts.google.com
export BOOTIMUS_OIDC_CLIENT_ID=<client id>.apps.googleusercontent.com
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_USERNAME_CLAIM=email
export BOOTIMUS_OIDC_ADMIN_USERS=alice@example.com,bob@example.com
export BOOTIMUS_OIDC_ALLOWED_USERS=@example.com
export BOOTIMUS_OIDC_DISPLAY_NAME=Google
```

在这里,Alice 和 Bob 是管理员,其他所有使用 `@example.com` 地址的人都是只读用户。Bootimus 只接受 Google 标记为已验证的电子邮件用户名。

### 访问映射

| 设置 | 作用 |
|---------|--------|
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | 这些组中任一组的成员为管理员。 |
| `BOOTIMUS_OIDC_ADMIN_USERS` | 这些用户为管理员。条目为用户名,或 `@domain`,用于匹配以该域名结尾的所有用户名。 |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | 如果设置,这些组的成员可以作为只读用户登录。 |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | 如果设置,这些用户(用户名或 `@domain` 条目)可以作为只读用户登录。 |

如果两个允许列表都未设置,则**提供商认证通过的任何人都可以以只读权限登录** — 这种情况下 Bootimus 会在启动时记录一条警告。一旦设置了任一允许列表,只有匹配允许列表或管理员列表的用户才能登录。如果两个管理员列表都未设置,则没有任何 OIDC 用户是管理员。

这四项都接受以逗号分隔的列表,因此组名可以包含空格(`Bootimus Admins,Infrastructure`)。匹配时忽略大小写;`@domain` 条目只匹配该确切域名,不匹配其子域名。组和列表在每次登录时重新检查,因此变更会在用户下次登录时生效。

### Docker Compose 示例

```yaml
services:
  bootimus:
    image: garybowers/bootimus:latest
    environment:
      BOOTIMUS_OIDC_ISSUER: https://authentik.example.com/application/o/bootimus/
      BOOTIMUS_OIDC_CLIENT_ID: bootimus
      BOOTIMUS_OIDC_CLIENT_SECRET: client-secret
      BOOTIMUS_OIDC_REDIRECT_URL: https://bootimus.example.com/api/oidc/callback
      BOOTIMUS_OIDC_ADMIN_GROUPS: bootimus-admins
      BOOTIMUS_OIDC_DISPLAY_NAME: Authentik
```

### 安全说明

- 登录的 state、nonce 和 PKCE verifier 保存在一个已签名、短期(10 分钟)的 `HttpOnly` cookie 中,作用范围限定为 `/api/oidc/`。当重定向 URL 使用 HTTPS 时,它会被标记为 `Secure` — 生产环境请通过 HTTPS 提供管理面板。
- 仅使用授权码流程;提供商的 token 不会到达浏览器。浏览器收到的是常规的 Bootimus JWT。
- 本地密码登录仍可与 OIDC 并存,因此本地 `admin` 账号始终可用于恢复。

## 来自 LDAP 和 OIDC 的用户

通过 LDAP 或 OIDC 登录的用户会在首次登录时获得一条本地用户记录。这样你就可以在 **Users** 下看到他们、禁用他们,并像本地用户一样在每次请求时检查其访问权限。

- **Source**:每个用户都被标记为 `Local`、`LDAP` 或 `OIDC`。用户只能通过自己的来源登录:OIDC 或 LDAP 登录绝不会接管同名的本地账号(例如 `admin`),并且 LDAP 和 OIDC 用户不能使用本地密码登录。
- **用户名**会被转为小写,可包含字母、数字和 `. _ @ + -`。包含其他字符的登录会被拒绝。
- **管理员权限**取决于用户所属的组(对于 OIDC,还包括管理员用户列表),并在每次登录时刷新,因此对 LDAP 或 OIDC 用户更改 **Administrator** 复选框,只会持续到他们下次登录。不是管理员的用户为只读(参见 [角色](#角色))。
- 在 Bootimus 中**禁用**用户会立即阻止其访问,即使提供商仍允许其登录。
- **删除**用户只会移除本地记录;该记录会在其下次登录时重新创建。请使用提供商的组,或禁用该用户,来移除访问权限。
- LDAP 和 OIDC 用户的**密码**由提供商管理,因此不提供 **Reset Password**,`bootimus user set-password` 也会拒绝为他们设置密码。

如果有人使用的名称已属于另一来源的用户,登录会被拒绝,并提示 *a user with this name already exists from a different source*。这包括在 LDAP 用户被自动创建之前就已存在的本地账号。请删除旧的本地账号,以便它在下次登录时从 LDAP 或 OIDC 重新创建。

## 配置参考

### CLI 标志

| 标志 | 默认值 | 说明 |
|------|---------|-------------|
| `--ldap-host` | *(空)* | LDAP 服务器主机名(启用 LDAP 认证) |
| `--ldap-port` | `389` | LDAP 服务器端口 |
| `--ldap-tls` | `false` | 使用 LDAPS(连接时即 TLS) |
| `--ldap-starttls` | `false` | 连接后使用 StartTLS |
| `--ldap-skip-verify` | `false` | 跳过 TLS 证书校验 |
| `--ldap-bind-dn` | *(空)* | 用于用户搜索的服务账号 DN |
| `--ldap-bind-password` | *(空)* | 服务账号密码 |
| `--ldap-base-dn` | *(空)* | 用户搜索的 base DN |
| `--ldap-user-filter` | `(sAMAccountName=%s)` | 用户搜索过滤器(`%s` = 用户名) |
| `--ldap-group-filter` | *(空)* | 管理员组的 CN |
| `--ldap-group-base-dn` | *(空)* | 组搜索的 base DN(默认与 base DN 相同) |
| `--oidc-issuer` | *(空)* | OIDC issuer URL(启用 OIDC 登录) |
| `--oidc-client-id` | *(空)* | OIDC 客户端 ID |
| `--oidc-client-secret` | *(空)* | OIDC 客户端密钥 |
| `--oidc-redirect-url` | *(空)* | 在提供商处注册的重定向 URL,以 `/api/oidc/callback` 结尾 |
| `--oidc-scopes` | `openid,profile,email` | 以逗号分隔的请求 scope(始终包含 `openid`) |
| `--oidc-username-claim` | `preferred_username` | 用作 Bootimus 用户名的声明(例如 `email`) |
| `--oidc-groups-claim` | `groups` | 列出用户所属组的声明 |
| `--oidc-admin-groups` | *(空)* | 以逗号分隔的组,其成员为管理员 |
| `--oidc-allowed-groups` | *(空)* | 以逗号分隔的允许登录的组 |
| `--oidc-admin-users` | *(空)* | 以逗号分隔的管理员用户名或 `@domain` 条目 |
| `--oidc-allowed-users` | *(空)* | 以逗号分隔的允许登录的用户名或 `@domain` 条目 |
| `--oidc-display-name` | `SSO` | 登录按钮上显示的名称 |
| `--oidc-skip-verify` | `false` | 与提供商通信时跳过 TLS 证书校验 |

### 环境变量

所有标志都可以通过带 `BOOTIMUS_` 前缀的环境变量来设置:

| 变量 | 对应标志 |
|----------|---------|
| `BOOTIMUS_LDAP_HOST` | `--ldap-host` |
| `BOOTIMUS_LDAP_PORT` | `--ldap-port` |
| `BOOTIMUS_LDAP_TLS` | `--ldap-tls` |
| `BOOTIMUS_LDAP_STARTTLS` | `--ldap-starttls` |
| `BOOTIMUS_LDAP_SKIP_VERIFY` | `--ldap-skip-verify` |
| `BOOTIMUS_LDAP_BIND_DN` | `--ldap-bind-dn` |
| `BOOTIMUS_LDAP_BIND_PASSWORD` | `--ldap-bind-password` |
| `BOOTIMUS_LDAP_BASE_DN` | `--ldap-base-dn` |
| `BOOTIMUS_LDAP_USER_FILTER` | `--ldap-user-filter` |
| `BOOTIMUS_LDAP_GROUP_FILTER` | `--ldap-group-filter` |
| `BOOTIMUS_LDAP_GROUP_BASE_DN` | `--ldap-group-base-dn` |
| `BOOTIMUS_OIDC_ISSUER` | `--oidc-issuer` |
| `BOOTIMUS_OIDC_CLIENT_ID` | `--oidc-client-id` |
| `BOOTIMUS_OIDC_CLIENT_SECRET` | `--oidc-client-secret` |
| `BOOTIMUS_OIDC_REDIRECT_URL` | `--oidc-redirect-url` |
| `BOOTIMUS_OIDC_SCOPES` | `--oidc-scopes` |
| `BOOTIMUS_OIDC_USERNAME_CLAIM` | `--oidc-username-claim` |
| `BOOTIMUS_OIDC_GROUPS_CLAIM` | `--oidc-groups-claim` |
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | `--oidc-admin-groups` |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | `--oidc-allowed-groups` |
| `BOOTIMUS_OIDC_ADMIN_USERS` | `--oidc-admin-users` |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | `--oidc-allowed-users` |
| `BOOTIMUS_OIDC_DISPLAY_NAME` | `--oidc-display-name` |
| `BOOTIMUS_OIDC_SKIP_VERIFY` | `--oidc-skip-verify` |

### 配置文件 (bootimus.yaml)

```yaml
ldap:
  host: dc.example.com
  port: 389
  tls: false
  starttls: true
  bind_dn: cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com
  bind_password: service-account-password
  base_dn: dc=example,dc=com
  user_filter: (sAMAccountName=%s)
  group_filter: cn=bootimus-admins

oidc:
  issuer: https://authentik.example.com/application/o/bootimus/
  client_id: bootimus
  client_secret: client-secret
  redirect_url: https://bootimus.example.com/api/oidc/callback
  admin_groups: bootimus-admins
  allowed_groups: bootimus-operators
  admin_users: alice@example.com
  allowed_users: "@example.com"
  display_name: Authentik
```

## 故障排查

### LDAP 连接失败

检查连通性和 TLS 设置:
```bash
# Test LDAP connection
ldapsearch -H ldap://dc.example.com -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"

# Test LDAPS
ldapsearch -H ldaps://dc.example.com:636 -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"
```

### 找不到用户

验证用户过滤器是否返回结果:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" dn
```

常见过滤器:
- Active Directory:`(sAMAccountName=%s)`
- OpenLDAP:`(uid=%s)`
- 基于邮箱:`(mail=%s)`

### LDAP 用户不是管理员

检查组成员关系:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" memberOf
```

### OIDC:"Unable to reach the identity provider"

当第一个用户登录时,Bootimus 会获取 `<issuer>/.well-known/openid-configuration`。请查看服务器日志中的具体错误,并确认 Bootimus 主机可以访问 issuer:

```bash
curl https://authentik.example.com/application/o/bootimus/.well-known/openid-configuration
```

该文档中的 `issuer` 值必须与 `BOOTIMUS_OIDC_ISSUER` 完全一致,包括末尾的斜杠(如有)。如果提供商使用私有 CA,请将其添加到主机的信任库;`BOOTIMUS_OIDC_SKIP_VERIFY=true` 可用于测试,但不推荐使用。

### OIDC:提供商显示重定向 URI 错误

在提供商处注册的重定向 URI 与 `BOOTIMUS_OIDC_REDIRECT_URL` 必须完全一致 — 包括协议、主机、端口和路径。

### OIDC:"Sign-in expired or was started elsewhere"

登录必须在 10 分钟内、在发起登录的同一浏览器中完成,并且期间 Bootimus 不能重启。如果在反向代理后面反复出现这种情况,请确保代理会透传 cookie,并且用户访问 Bootimus 所用的主机与重定向 URL 中的主机相同。

### OIDC:用户不是管理员或被拒绝登录

服务器日志会记录每次登录被拒绝的原因。请检查提供商实际发送了哪些组 — 组名必须与 `BOOTIMUS_OIDC_ADMIN_GROUPS` 或 `BOOTIMUS_OIDC_ALLOWED_GROUPS` 匹配,并且除非关闭 **Full group path**,否则 Keycloak 会发送完整路径(`/bootimus-admins`)。如果你的提供商使用不同的声明名称,请设置 `BOOTIMUS_OIDC_GROUPS_CLAIM`(Entra ID 的 app roles 使用 `roles`)。对于没有组的提供商(例如 Google),请使用 `BOOTIMUS_OIDC_ADMIN_USERS` 和 `BOOTIMUS_OIDC_ALLOWED_USERS`。

### OIDC:"email … is not marked as verified by the provider"

当 `BOOTIMUS_OIDC_USERNAME_CLAIM` 为 `email` 时,提供商必须发送 `email_verified: true`,否则任何能够设置未验证地址的人都可以冒充他人登录。请在提供商处验证该地址,或改用其他用户名声明,例如 `preferred_username`。

### "A user with this name already exists from a different source"

参见 [来自 LDAP 和 OIDC 的用户](#来自-ldap-和-oidc-的用户)。请在 **Users** 下删除现有账号,然后重新登录。

### Token 过期

JWT token 有效期 24 小时。过期后会自动显示登录页。服务器重启时 token 也会失效(签名密钥会被重新生成)。

### 本地管理员被锁定

重置管理员密码:
```bash
./bootimus serve --reset-admin-password
```

或者,如果启动服务器不方便(例如端口冲突),可直接在数据库中设置密码:
```bash
./bootimus user set-password admin
```

无论 LDAP 或 OIDC 配置如何,这总是可用。
