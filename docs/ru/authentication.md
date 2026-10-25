# Руководство по аутентификации

Bootimus использует JWT (JSON Web Token) для аутентификации в админ-панели. По желанию можно подключить LDAP- или Active Directory-сервер либо провайдер идентификации OpenID Connect (OIDC), например Authentik или Keycloak, для единого входа (SSO).

## Оглавление

- [Локальная аутентификация](#локальная-аутентификация)
- [Процесс входа](#процесс-входа)
  - [Двухфакторная аутентификация](#двухфакторная-аутентификация)
- [Роли](#роли)
- [Аутентификация API](#аутентификация-api)
  - [API-токены](#api-токены)
- [LDAP / Active Directory](#ldap--active-directory)
- [OIDC / единый вход (SSO)](#oidc--единый-вход-sso)
- [Пользователи из LDAP и OIDC](#пользователи-из-ldap-и-oidc)
- [Справочник по конфигурации](#справочник-по-конфигурации)
- [Диагностика](#диагностика)

## Локальная аутентификация

По умолчанию Bootimus использует локальные учётные записи, хранящиеся в базе данных (SQLite или PostgreSQL).

### Учётка администратора по умолчанию

При первом запуске генерируется случайный пароль и печатается в логи сервера:

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

### Сброс пароля администратора

Сбросить на новый случайный пароль (запускает полноценный экземпляр сервера):
```bash
./bootimus serve --reset-admin-password
# или через Docker
docker exec bootimus /bootimus serve --reset-admin-password
```

Либо задать конкретный пароль напрямую в базе данных, не привязываясь к портам
(идеально для аварийного восстановления):
```bash
# Интерактивный ввод (скрытый ввод, пароль не попадает в историю shell)
./bootimus user set-password admin

# Или передать неинтерактивно для скриптов
./bootimus user set-password admin --password 'новый-пароль'
```

### Управление пользователями

Дополнительных пользователей можно создать во вкладке **Users** админ-панели. У каждого пользователя есть:
- **Username**: уникальное имя для входа
- **Password**: хранится как bcrypt-хеш
- **Admin**: является ли пользователь администратором; остальные пользователи имеют доступ только для чтения (см. [Роли](#роли))
- **Enabled**: можно отключить без удаления
- **Source**: `Local`, `LDAP` или `OIDC` — откуда взята учётная запись (см. [Пользователи из LDAP и OIDC](#пользователи-из-ldap-и-oidc))

Пользователями также можно управлять из CLI, не запуская сервер (удобно для
восстановления). Эти команды работают напрямую с настроенной базой данных
(SQLite или PostgreSQL):
```bash
./bootimus user list                       # список всех пользователей и их источник
./bootimus user enable <username>          # включить учётную запись
./bootimus user disable <username>         # отключить учётную запись
./bootimus user set-admin <username>       # выдать права администратора
./bootimus user unset-admin <username>     # снять права администратора
./bootimus user set-password <username>    # задать пароль (запрос ввода или --password)
```

## Процесс входа

1. Откройте `http://your-server:8081`
2. Отображается страница входа с полями имени пользователя и пароля
3. Если настроен LDAP, появляется выпадающий список для выбора бэкенда
4. Если настроен OIDC, под формой появляется кнопка **Sign in with …**
5. При успешном входе выдаётся JWT-токен (действует 24 часа)
6. Токен сохраняется в браузере и отправляется со всеми API-запросами
7. При выходе или истечении токена снова показывается страница входа

### Двухфакторная аутентификация

Локальные учётки могут добавить одноразовый пароль на основе времени (TOTP) как второй фактор. Пользователям LDAP и OIDC он здесь не предлагается: второй фактор принадлежит их провайдеру идентификации.

**Включение.** Откройте **Users** → **Two-Factor Authentication**, нажмите **Set up**, отсканируйте QR-код приложением-аутентификатором (или введите секрет вручную), затем подтвердите кодом из приложения. После этого Bootimus покажет десять одноразовых кодов восстановления. Сохраните их: повторно они не показываются.

**Вход.** После правильного пароля страница входа запрашивает шестизначный код. Вместо него один раз можно использовать код восстановления. Неверные коды засчитываются в [защиту от перебора](#защита-от-перебора) так же, как неверные пароли.

**Потеря устройства.** Администратор может нажать **Reset 2FA** рядом с пользователем или вызвать `POST /api/users/totp/reset?id=<id>`. Если заблокирован единственный администратор, выполните на сервере:

```bash
bootimus user reset-totp admin
```

**Через API.** Скриптам стоит использовать [API-токены](#api-токены), а не пароль с TOTP. Если вы всё же входите учёткой с TOTP, `POST /api/login` сначала ответит `401` с `{"error":"totp_required","totp_required":true}`; повторите запрос с теми же учётными данными плюс `"code":"123456"`.

### Защита от перебора

Входы по паролю (локальные и LDAP) ограничиваются по частоте в памяти:

- После **5 неудачных попыток** для одного имени пользователя в течение 15 минут это имя блокируется на 1 минуту. Каждая следующая неудача, пока недавние неудачи ещё попадают в окно, удваивает блокировку — до 15 минут.
- После **20 неудачных попыток** с одного адреса клиента в течение 15 минут этот адрес блокируется по той же схеме. Более высокий порог означает, что общий адрес обратного прокси не будет заблокирован из-за опечаток одного человека.
- Успешный вход сбрасывает счётчик неудач для имени пользователя.

Во время блокировки API отвечает `429 Too Many Requests` с заголовком `Retry-After`, а страница входа показывает, сколько ждать. Неверные учётные данные по-прежнему возвращают обычный `401`, так что ответ не раскрывает, существует ли имя пользователя. Неудачные попытки записываются в лог сервера с именем пользователя и адресом клиента. Перезапуск Bootimus сбрасывает счётчики. Входы через OIDC проходят через провайдера идентификации и не учитываются.

## Роли

В Bootimus две роли:

| Роль | Доступ |
|------|--------|
| **Admin** | Всё. |
| **Read-only** | Может просматривать дашборд, клиентов (включая инвентарь оборудования, историю загрузок и состояние питания BMC), группы клиентов, образы, публичные файлы, профили дистрибутивов, меню загрузки, загрузчики, инструменты, кластеры и узлы Kubernetes, запланированные задачи и историю попыток загрузки. |

Пользователи только для чтения:

- ничего не могут менять — сервер отклоняет любую запись ответом `403 Your account has read-only access`, а админ-панель скрывает кнопки
- не видят пользователей, настройки вебхуков, бэкапы, журнал аудита, живой лог сервера, файлы и скрипты автоустановки, а также машинные конфиги кластеров
- никогда не получают пароли BMC (IPMI/Redfish) и патчи конфигурации клиентов — они удаляются из каждого API-ответа, отправляемого им

Пользователь является администратором, если:

- **Local**: на его учётке отмечен флажок **Administrator**
- **LDAP**: он соответствует `BOOTIMUS_LDAP_GROUP_FILTER` (или фильтр групп не задан)
- **OIDC**: он состоит в одной из групп `BOOTIMUS_OIDC_ADMIN_GROUPS` или указан в `BOOTIMUS_OIDC_ADMIN_USERS`

Все остальные, кто может войти, получают доступ только для чтения. `GET /api/me` возвращает роль текущего пользователя.

> **Обновление:** до появления доступа только для чтения локальные пользователи без отмеченного флажка **Administrator** могли войти, но ничего не видели. Теперь у них есть доступ только для чтения. Отключите такие учётки, если не хотите, чтобы он у них был.

## Аутентификация API

Все API-эндпоинты (кроме `/api/login`, `/api/auth-info` и эндпоинтов входа `/api/oidc/`) требуют действительный JWT-токен. Пользователи только для чтения могут выполнять только `GET`-запросы и только к эндпоинтам, перечисленным в разделе [Роли](#роли).

### Получить токен

```bash
# Логин и получение токена
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}' | jq -r '.data.token')

echo $TOKEN
```

### Использовать токен

```bash
# Включайте в каждый API-запрос
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients

# Пример: список образов
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images
```

### Детали токена

- **Алгоритм**: HMAC-SHA256
- **Срок жизни**: 24 часа с момента выдачи
- **Секрет**: генерируется случайно при каждом запуске сервера (все токены инвалидируются при перезапуске)
- **Claims**: имя пользователя, статус администратора, время выдачи, срок истечения

### API-токены

Для скриптов, CI-задач и интеграций создайте долгоживущий токен в **Users** → **API Tokens** вместо входа по паролю. Токены начинаются с `btk_`, показываются один раз при создании и хранятся в виде хеша. У каждого токена есть область действия:

| Область | Что может делать |
|---------|------------------|
| `read` | `GET`-запросы к тем же эндпоинтам, которые видит пользователь только для чтения |
| `admin` | Всё, что может администратор, кроме создания и отзыва токенов |

Токену можно задать срок действия в днях и отозвать его в любой момент. Журнал аудита записывает изменения, сделанные с токеном, как `token:<name>`, а `/api/me` сообщает `"principal": "token"`.

```bash
# Создать токен (от имени администратора)
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ansible","scope":"admin","expires_in_days":365}' \
  http://localhost:8081/api/tokens

# Использовать его точно так же, как JWT
curl -H "Authorization: Bearer btk_..." http://localhost:8081/api/clients

# Список и отзыв
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/tokens
curl -X DELETE -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/tokens?id=3"
```

В отличие от JWT, API-токены переживают перезапуск Bootimus.

Токены и пароли, которыми их получают, передаются в открытом виде, если админ-интерфейс не обслуживается по HTTPS. Включите его флагом `--admin-tls` или поставьте перед админ-портом обратный прокси с терминацией TLS; см. [Защита админ-интерфейса](deployment.md#защита-админ-интерфейса).

### Проверка доступных auth-бэкендов

```bash
# Аутентификация не требуется
curl http://localhost:8081/api/auth-info
```

Ответ:
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

Bootimus поддерживает LDAP как дополнительный бэкенд аутентификации. Когда он настроен, пользователи могут выбрать на странице входа между локальной и LDAP-аутентификацией. Локальные учётки всегда работают как резерв.

### Как это работает

1. Пользователь выбирает «LDAP» на странице входа и вводит креды
2. Bootimus подключается к LDAP-серверу через сервисную учётку (bind DN)
3. Ищет пользователя по заданному фильтру
4. Пытается забиндиться от имени найденного пользователя с указанным паролем
5. При успехе проверяет членство в группе для админ-доступа
6. Создаёт или обновляет для него локальную запись пользователя (см. [Пользователи из LDAP и OIDC](#пользователи-из-ldap-и-oidc))
7. Выдаёт JWT-токен (так же, как при локальной аутентификации)

### Пример Active Directory

```bash
# Переменные окружения
export BOOTIMUS_LDAP_HOST=dc.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="service-account-password"
export BOOTIMUS_LDAP_USER_FILTER="(sAMAccountName=%s)"
export BOOTIMUS_LDAP_GROUP_FILTER="cn=bootimus-admins"
```

### Пример OpenLDAP

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

# Или StartTLS на порту 389
export BOOTIMUS_LDAP_HOST=ldap.example.com
export BOOTIMUS_LDAP_STARTTLS=true

# Пропустить проверку сертификата (не рекомендуется для продакшена)
export BOOTIMUS_LDAP_SKIP_VERIFY=true
```

### Пример Docker Compose

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

### Членство в админ-группе

Если задан `BOOTIMUS_LDAP_GROUP_FILTER`, права администратора получают только пользователи — члены подходящей группы. Членство проверяется через:

1. Атрибут `memberOf` на объекте пользователя
2. Запрос поиска по группе, если `memberOf` недоступен

Если `BOOTIMUS_LDAP_GROUP_FILTER` **не задан**, все LDAP-пользователи получают права администратора.

Права администратора пересчитываются при каждом входе, поэтому если убрать человека из группы, он потеряет их при следующем входе.

### Логин через API с LDAP

```bash
# Укажите auth_method: "ldap"
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"jdoe","password":"ldap-password","auth_method":"ldap"}' | jq -r '.data.token')
```

## OIDC / единый вход (SSO)

Bootimus может авторизовать пользователей через любой провайдер OpenID Connect — Authentik, Keycloak, Zitadel, Authelia, Entra ID, Google Workspace и другие. Пользователь нажимает **Sign in with …** на странице входа, проходит аутентификацию у провайдера и возвращается в Bootimus уже авторизованным. Учётные записи создаются автоматически при первом входе, а права администратора определяются группами пользователя у провайдера.

### Как это работает

1. Пользователь нажимает **Sign in with …** на странице входа
2. Bootimus перенаправляет его к провайдеру (поток authorisation code с PKCE)
3. Пользователь входит у провайдера, который перенаправляет обратно на `/api/oidc/callback`
4. Bootimus обменивает код на ID-токен, проверяет его подпись, audience, срок действия и nonce и считывает claims с именем пользователя и группами
5. Если заданы разрешённые группы или пользователи, пользователь должен соответствовать одному из них (или быть администратором)
6. Создаётся или обновляется локальная запись пользователя (см. [Пользователи из LDAP и OIDC](#пользователи-из-ldap-и-oidc)): администратором он становится, если состоит в админ-группе или указан в списке администраторов, иначе доступ только для чтения
7. Браузер возвращается в админ-панель с JWT-токеном (так же, как при локальной аутентификации)

Если в ID-токене нет claim с именем пользователя или группами, Bootimus дополнительно обращается к userinfo-эндпоинту провайдера.

### Настройка провайдера

Создайте у провайдера OIDC-клиент (его иногда называют приложением или relying party) со следующими параметрами:

| Параметр | Значение |
|---------|-------|
| Тип клиента | Confidential (с client secret) |
| Тип гранта | Authorisation code |
| Redirect URI | `https://bootimus.example.com/api/oidc/callback` — URL, по которому пользователи открывают админ-панель, плюс `/api/oidc/callback` |
| Scopes | `openid`, `profile`, `email` |

Убедитесь, что провайдер передаёт группы пользователя в claim `groups`. Bootimus не пытается угадать собственный публичный URL, поэтому `BOOTIMUS_OIDC_REDIRECT_URL` должен в точности совпадать с redirect URI, зарегистрированным у провайдера.

### Пример Authentik

1. В Authentik откройте **Applications → Providers** и создайте **OAuth2/OpenID Provider**:
   - **Name**: `bootimus`
   - **Authorisation flow**: ваш обычный explicit- или implicit-consent flow
   - **Client type**: Confidential
   - **Redirect URIs/Origins**: `https://bootimus.example.com/api/oidc/callback` (strict)
   - **Scopes**: оставьте стандартные маппинги `openid`, `email` и `profile` — маппинг `profile` в Authentik включает claim `groups`
   - **Subject mode**: любой; по умолчанию Bootimus использует `preferred_username` как имя пользователя
2. Запишите **Client ID** и **Client Secret**
3. Откройте **Applications → Applications** и создайте приложение со slug `bootimus`, использующее этот провайдер. При желании привяжите к нему политику или группу, чтобы ограничить, кто вообще может его запускать.
4. Создайте группы для администраторов Bootimus (например, `bootimus-admins`) и, при необходимости, для остальных допущенных пользователей (например, `bootimus-operators`)
5. Настройте Bootimus:

```bash
export BOOTIMUS_OIDC_ISSUER=https://authentik.example.com/application/o/bootimus/
export BOOTIMUS_OIDC_CLIENT_ID=<client id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_ALLOWED_GROUPS=bootimus-operators
export BOOTIMUS_OIDC_DISPLAY_NAME=Authentik
```

URL издателя (issuer) показан на странице обзора провайдера в Authentik как **OpenID Configuration Issuer** и всегда заканчивается на `/application/o/<slug приложения>/`.

### Пример Keycloak

Создайте confidential-клиент OpenID Connect с включённым **Standard flow** и указанным выше redirect URI. Keycloak по умолчанию не передаёт группы: добавьте маппер **Group Membership** в dedicated scope клиента с именем token claim `groups` и выключенной опцией **Full group path**.

```bash
export BOOTIMUS_OIDC_ISSUER=https://keycloak.example.com/realms/example
export BOOTIMUS_OIDC_CLIENT_ID=bootimus
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_DISPLAY_NAME=Keycloak
```

### Пример Microsoft Entra ID

Entra ID передаёт в claim групп *ID* групп, а не их названия, поэтому проще всего сопоставлять роли через **app roles**, которые Entra передаёт в claim `roles`.

1. В [Microsoft Entra admin centre](https://entra.microsoft.com) откройте **App registrations → New registration**:
   - **Name**: `Bootimus`
   - **Supported account types**: *Accounts in this organisational directory only (Single tenant)*
   - **Redirect URI**: платформа **Web**, `https://bootimus.example.com/api/oidc/callback`
2. Запишите **Application (client) ID** и **Directory (tenant) ID** на странице обзора
3. В разделе **Certificates & secrets** создайте **New client secret** и скопируйте его **Value**
4. В разделе **App roles** создайте две роли, у каждой **Allowed member types** — *Users/Groups*:
   - Display name `Bootimus Admin`, value `Bootimus.Admin`
   - Display name `Bootimus Read-only`, value `Bootimus.ReadOnly`
5. Откройте **Enterprise applications → Bootimus → Users and groups** и назначьте ролям пользователей или группы. Назначение групп требует Entra ID P1 или P2; на бесплатном тарифе назначайте пользователей по одному.
6. При желании в **Enterprise applications → Bootimus → Properties** установите **Assignment required?** в **Yes**, чтобы войти могли только назначенные пользователи
7. Настройте Bootimus:

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

Оставьте claim имени пользователя по умолчанию, `preferred_username` — это имя для входа пользователя (например, `alice@example.com`). Не используйте `email`: Entra его не верифицирует, а Bootimus отклоняет email-имена, которые провайдер не пометил как подтверждённые.

Если вы предпочитаете группы безопасности, добавьте **groups claim** в разделе **Token configuration** и укажите **Object IDs** групп в `BOOTIMUS_OIDC_ADMIN_GROUPS` и `BOOTIMUS_OIDC_ALLOWED_GROUPS`, оставив `BOOTIMUS_OIDC_GROUPS_CLAIM` равным `groups`.

### Пример Google

Google не передаёт ни членство в группах, ни claim `preferred_username`, поэтому с Google в качестве имён пользователей используются email-адреса, а доступ выдаётся списками пользователей и доменов вместо групп.

1. В [Google Cloud console](https://console.cloud.google.com) выберите или создайте проект и откройте **APIs & Services → OAuth consent screen** (в новых версиях консоли он называется **Google Auth Platform**):
   - **User type**: *Internal*, если вы используете Google Workspace — тогда войти смогут только учётки вашей организации. *External* допускает любой аккаунт Google, поэтому в этом случае всегда задавайте `BOOTIMUS_OIDC_ALLOWED_USERS`.
   - Добавьте scopes `openid`, `email` и `profile`
2. Откройте **APIs & Services → Credentials → Create credentials → OAuth client ID**:
   - **Application type**: Web application
   - **Authorised redirect URIs**: `https://bootimus.example.com/api/oidc/callback` (Google принимает только HTTPS, кроме `http://localhost`)
3. Запишите **Client ID** и **Client secret**
4. Настройте Bootimus:

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

Здесь Alice и Bob — администраторы, а все остальные с адресом `@example.com` имеют доступ только для чтения. Bootimus принимает только те email-имена, которые Google пометил как подтверждённые.

### Сопоставление доступа

| Параметр | Эффект |
|---------|--------|
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | Члены любой из этих групп — администраторы. |
| `BOOTIMUS_OIDC_ADMIN_USERS` | Эти пользователи — администраторы. Записи — имена пользователей или `@domain` для совпадения со всеми именами, оканчивающимися на этот домен. |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | Если задан, члены этих групп могут входить с доступом только для чтения. |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | Если задан, эти пользователи (имена или записи `@domain`) могут входить с доступом только для чтения. |

Если ни один из списков разрешённых не задан, **войти с доступом только для чтения может любой, кого аутентифицировал провайдер** — в этом случае Bootimus пишет предупреждение в лог при запуске. Как только задан любой из списков разрешённых, войти могут только пользователи, попадающие в список разрешённых или администраторов. Если не задан ни один из списков администраторов, ни один OIDC-пользователь не становится администратором.

Все четыре параметра принимают список через запятую, поэтому названия групп могут содержать пробелы (`Bootimus Admins,Infrastructure`). Регистр при сравнении не учитывается; запись `@domain` совпадает только с этим доменом, но не с его поддоменами. Группы и списки перепроверяются при каждом входе, поэтому изменения применяются при следующем входе пользователя.

### Пример Docker Compose

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

### Заметки по безопасности

- Состояние входа, nonce и PKCE verifier хранятся в подписанной короткоживущей (10 минут) cookie `HttpOnly` с областью `/api/oidc/`. Она помечается как `Secure`, если redirect URL использует HTTPS — в продакшене отдавайте админ-панель по HTTPS.
- Используется только поток authorisation code; токены провайдера в браузер не попадают. Браузер получает обычный JWT Bootimus.
- Локальный вход по паролю продолжает работать вместе с OIDC, поэтому локальная учётка `admin` остаётся доступной для восстановления.

## Пользователи из LDAP и OIDC

Пользователи, входящие через LDAP или OIDC, получают локальную запись при первом входе. Благодаря этому их можно увидеть в разделе **Users**, отключить, и их доступ проверяется при каждом запросе так же, как у локальных пользователей.

- **Source**: каждый пользователь помечен как `Local`, `LDAP` или `OIDC`. Пользователь может входить только через свой источник: вход через OIDC или LDAP никогда не перехватит локальную учётку с тем же именем (например, `admin`), а пользователи LDAP и OIDC не могут входить по локальному паролю.
- **Имена пользователей** приводятся к нижнему регистру и могут содержать буквы, цифры и символы `. _ @ + -`. Входы с другими символами отклоняются.
- **Права администратора** определяются группами пользователя (а для OIDC — ещё и списком администраторов) и обновляются при каждом входе, поэтому изменение флажка **Administrator** для пользователя LDAP или OIDC действует только до его следующего входа. Пользователи, не являющиеся администраторами, имеют доступ только для чтения (см. [Роли](#роли)).
- **Отключение** пользователя в Bootimus блокирует его немедленно, даже если провайдер по-прежнему разрешает ему вход.
- **Удаление** пользователя убирает только локальную запись; при следующем входе она создаётся заново. Чтобы отозвать доступ, используйте группы провайдера или отключите пользователя.
- **Паролями** пользователей LDAP и OIDC управляет провайдер, поэтому для них не предлагается **Reset Password**, а `bootimus user set-password` отказывается с ними работать.

Если кто-то входит под именем, которое уже принадлежит пользователю из другого источника, вход отклоняется с сообщением *a user with this name already exists from a different source*. Это касается и локальных учёток, созданных до того, как пользователи LDAP стали создаваться автоматически. Удалите старую локальную учётку, чтобы при следующем входе она была создана заново из LDAP или OIDC.

## Справочник по конфигурации

### Флаги CLI

| Флаг | По умолчанию | Описание |
|------|---------|-------------|
| `--ldap-host` | *(пусто)* | Хост LDAP-сервера (включает LDAP-аутентификацию) |
| `--ldap-port` | `389` | Порт LDAP-сервера |
| `--ldap-tls` | `false` | Использовать LDAPS (TLS при подключении) |
| `--ldap-starttls` | `false` | Использовать StartTLS после подключения |
| `--ldap-skip-verify` | `false` | Пропустить проверку TLS-сертификата |
| `--ldap-bind-dn` | *(пусто)* | DN сервисной учётки для поиска пользователей |
| `--ldap-bind-password` | *(пусто)* | Пароль сервисной учётки |
| `--ldap-base-dn` | *(пусто)* | Base DN для поиска пользователей |
| `--ldap-user-filter` | `(sAMAccountName=%s)` | Фильтр поиска пользователей (`%s` = имя) |
| `--ldap-group-filter` | *(пусто)* | CN группы для админ-доступа |
| `--ldap-group-base-dn` | *(пусто)* | Base DN для поиска групп (по умолчанию — base DN) |
| `--oidc-issuer` | *(пусто)* | URL издателя OIDC (включает вход через OIDC) |
| `--oidc-client-id` | *(пусто)* | Client ID OIDC |
| `--oidc-client-secret` | *(пусто)* | Client secret OIDC |
| `--oidc-redirect-url` | *(пусто)* | Redirect URL, зарегистрированный у провайдера, оканчивается на `/api/oidc/callback` |
| `--oidc-scopes` | `openid,profile,email` | Запрашиваемые scopes через запятую (`openid` включается всегда) |
| `--oidc-username-claim` | `preferred_username` | Claim, используемый как имя пользователя Bootimus (например, `email`) |
| `--oidc-groups-claim` | `groups` | Claim со списком групп пользователя |
| `--oidc-admin-groups` | *(пусто)* | Группы через запятую, члены которых — администраторы |
| `--oidc-allowed-groups` | *(пусто)* | Группы через запятую, которым разрешён вход |
| `--oidc-admin-users` | *(пусто)* | Имена пользователей или записи `@domain` через запятую, которые являются администраторами |
| `--oidc-allowed-users` | *(пусто)* | Имена пользователей или записи `@domain` через запятую, которым разрешён вход |
| `--oidc-display-name` | `SSO` | Название на кнопке входа |
| `--oidc-skip-verify` | `false` | Пропустить проверку TLS-сертификата при обращении к провайдеру |

### Переменные окружения

Все флаги задаются через переменные окружения с префиксом `BOOTIMUS_`:

| Переменная | Соответствует |
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

### Файл конфигурации (bootimus.yaml)

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

## Диагностика

### Сбой подключения к LDAP

Проверьте связность и настройки TLS:
```bash
# Тест подключения LDAP
ldapsearch -H ldap://dc.example.com -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"

# Тест LDAPS
ldapsearch -H ldaps://dc.example.com:636 -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"
```

### Пользователь не найден

Проверьте, что фильтр возвращает результаты:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" dn
```

Типичные фильтры:
- Active Directory: `(sAMAccountName=%s)`
- OpenLDAP: `(uid=%s)`
- По email: `(mail=%s)`

### LDAP-пользователь не админ

Проверьте членство в группе:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" memberOf
```

### OIDC: «Unable to reach the identity provider»

Bootimus запрашивает `<issuer>/.well-known/openid-configuration`, когда первый пользователь входит в систему. Посмотрите точный текст ошибки в логе сервера и проверьте, что хост Bootimus может обратиться к издателю:

```bash
curl https://authentik.example.com/application/o/bootimus/.well-known/openid-configuration
```

Значение `issuer` в этом документе должно в точности совпадать с `BOOTIMUS_OIDC_ISSUER`, включая завершающий слэш. Если провайдер использует приватный CA, добавьте его в хранилище доверенных сертификатов хоста; `BOOTIMUS_OIDC_SKIP_VERIFY=true` подходит для тестирования, но не рекомендуется.

### OIDC: провайдер показывает ошибку redirect URI

Redirect URI, зарегистрированный у провайдера, и `BOOTIMUS_OIDC_REDIRECT_URL` должны совпадать в точности — схема, хост, порт и путь.

### OIDC: «Sign-in expired or was started elsewhere»

Вход должен завершиться в течение 10 минут, в том же браузере, где он был начат, и Bootimus не должен перезапускаться за это время. Если ошибка повторяется за обратным прокси, убедитесь, что прокси передаёт cookies и что пользователи открывают Bootimus по тому же хосту, что указан в redirect URL.

### OIDC: пользователь не администратор или ему отказано во входе

В логе сервера записана причина каждого отказа во входе. Проверьте, какие группы провайдер реально передаёт — названия групп должны совпадать с `BOOTIMUS_OIDC_ADMIN_GROUPS` или `BOOTIMUS_OIDC_ALLOWED_GROUPS`, а Keycloak присылает полные пути (`/bootimus-admins`), если не выключить **Full group path**. Если провайдер использует другое имя claim, задайте `BOOTIMUS_OIDC_GROUPS_CLAIM` (для app roles в Entra ID используется `roles`). Для провайдеров без групп, таких как Google, используйте `BOOTIMUS_OIDC_ADMIN_USERS` и `BOOTIMUS_OIDC_ALLOWED_USERS`.

### OIDC: «email … is not marked as verified by the provider»

Когда `BOOTIMUS_OIDC_USERNAME_CLAIM` равен `email`, провайдер должен передавать `email_verified: true`, иначе любой, кто может указать неподтверждённый адрес, мог бы войти под чужим именем. Подтвердите адрес у провайдера или используйте другой claim имени пользователя, например `preferred_username`.

### «A user with this name already exists from a different source»

См. [Пользователи из LDAP и OIDC](#пользователи-из-ldap-и-oidc). Удалите существующую учётную запись в разделе **Users** и войдите снова.

### Токен истёк

JWT-токены действительны 24 часа. После истечения автоматически показывается страница входа. Токены также инвалидируются при перезапуске сервера (секрет подписи генерируется заново).

### Локальный админ заблокирован

Сбросьте пароль администратора:
```bash
./bootimus serve --reset-admin-password
```

Либо, если запускать сервер неудобно (например, из-за конфликтов портов), задайте
пароль напрямую в базе данных:
```bash
./bootimus user set-password admin
```

Это всегда работает, независимо от конфигурации LDAP или OIDC.
