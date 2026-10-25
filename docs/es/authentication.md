# Guía de autenticación

Bootimus usa autenticación JWT (JSON Web Token) para el panel admin. Opcionalmente, puedes conectar un servidor LDAP o Active Directory, o un proveedor de identidad OpenID Connect (OIDC) como Authentik o Keycloak para el inicio de sesión único.

## Tabla de contenidos

- [Autenticación local](#autenticación-local)
- [Flujo de login](#flujo-de-login)
  - [Autenticación de dos factores](#autenticación-de-dos-factores)
- [Roles](#roles)
- [Autenticación de la API](#autenticación-de-la-api)
  - [Tokens de API](#tokens-de-api)
- [LDAP / Active Directory](#ldap--active-directory)
- [OIDC / inicio de sesión único](#oidc--inicio-de-sesión-único)
- [Usuarios de LDAP y OIDC](#usuarios-de-ldap-y-oidc)
- [Referencia de configuración](#referencia-de-configuración)
- [Solución de problemas](#solución-de-problemas)

## Autenticación local

Por defecto, Bootimus usa cuentas locales almacenadas en la base de datos (SQLite o PostgreSQL).

### Cuenta admin por defecto

En el primer arranque se genera una contraseña aleatoria y se imprime en los logs del servidor:

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

### Resetear contraseña admin

Resetear a una nueva contraseña aleatoria (arranca una instancia completa del servidor):
```bash
./bootimus serve --reset-admin-password
# o con Docker
docker exec bootimus /bootimus serve --reset-admin-password
```

O establecer una contraseña concreta directamente en la base de datos, sin
abrir ningún puerto (ideal para recuperación de emergencia):
```bash
# Solicitud interactiva (entrada oculta, mantiene la contraseña fuera del historial del shell)
./bootimus user set-password admin

# O proporcionarla de forma no interactiva para scripting
./bootimus user set-password admin --password 'nueva-contraseña'
```

### Gestión de usuarios

Se pueden crear usuarios adicionales desde la pestaña **Users** del panel admin. Cada usuario tiene:
- **Username**: Nombre de login único
- **Password**: Almacenada como hash bcrypt
- **Admin**: Si el usuario es administrador; el resto de usuarios son de solo lectura (mira [Roles](#roles))
- **Enabled**: Se puede deshabilitar sin borrar
- **Source**: `Local`, `LDAP` o `OIDC` — de dónde procede la cuenta (mira [Usuarios de LDAP y OIDC](#usuarios-de-ldap-y-oidc))

Los usuarios también se pueden gestionar desde la CLI sin arrancar el servidor
(útil para recuperación). Estos comandos operan directamente sobre la base de
datos configurada (SQLite o PostgreSQL):
```bash
./bootimus user list                       # listar todos los usuarios y su origen
./bootimus user enable <username>          # habilitar una cuenta
./bootimus user disable <username>         # deshabilitar una cuenta
./bootimus user set-admin <username>       # conceder derechos de admin
./bootimus user unset-admin <username>     # revocar derechos de admin
./bootimus user set-password <username>    # establecer una contraseña (solicita, o --password)
```

## Flujo de login

1. Navega a `http://your-server:8081`
2. Se muestra la página de login con campos de usuario y contraseña
3. Si LDAP está configurado, aparece un desplegable de autenticación para elegir el backend
4. Si OIDC está configurado, aparece un botón **Sign in with …** debajo del formulario
5. Tras un login exitoso, se emite un token JWT (válido 24 horas)
6. El token se almacena en el navegador y se envía con todas las peticiones a la API
7. Al hacer logout o expirar el token, se muestra de nuevo la página de login

### Autenticación de dos factores

Las cuentas locales pueden añadir una contraseña de un solo uso basada en tiempo (TOTP) como segundo factor. A los usuarios de LDAP y OIDC no se les ofrece aquí; el segundo factor lo gestiona su proveedor de identidad.

**Activarla.** Abre **Users** → **Two-Factor Authentication**, pulsa **Set up**, escanea el código QR con una app de autenticación (o introduce el secreto a mano) y confirma con un código de la app. Bootimus muestra entonces diez códigos de recuperación de un solo uso. Guárdalos; no se vuelven a mostrar.

**Iniciar sesión.** Tras una contraseña correcta, la página de login pide el código de seis dígitos. Un código de recuperación sirve en su lugar, una sola vez. Los códigos incorrectos cuentan para el [bloqueo por fuerza bruta](#protección-contra-fuerza-bruta) igual que las contraseñas incorrectas.

**Perder el dispositivo.** Un administrador puede pulsar **Reset 2FA** junto al usuario, o llamar a `POST /api/users/totp/reset?id=<id>`. Si el único administrador se ha quedado fuera, ejecuta esto en el servidor:

```bash
bootimus user reset-totp admin
```

**Vía API.** Los scripts deberían usar [tokens de API](#tokens-de-api) en lugar de contraseña y TOTP. Si inicias sesión con una cuenta protegida por TOTP, `POST /api/login` responde primero `401` con `{"error":"totp_required","totp_required":true}`; repite la petición con las mismas credenciales más `"code":"123456"`.

### Protección contra fuerza bruta

Los logins con contraseña (local y LDAP) están limitados en memoria:

- Tras **5 intentos fallidos** para el mismo usuario en 15 minutos, ese usuario queda bloqueado 1 minuto. Cada fallo adicional mientras los fallos recientes sigan dentro de la ventana duplica el bloqueo, hasta 15 minutos.
- Tras **20 intentos fallidos** desde la misma dirección cliente en 15 minutos, esa dirección queda bloqueada con el mismo esquema. El umbral más alto evita que la dirección compartida de un reverse proxy quede bloqueada por los errores de una sola persona.
- Un login correcto borra el contador de fallos del usuario.

Mientras dura el bloqueo, la API responde `429 Too Many Requests` con una cabecera `Retry-After` y la página de login muestra cuánto hay que esperar. Las credenciales incorrectas siguen devolviendo el `401` habitual, así que la respuesta no revela si un usuario existe. Los intentos fallidos se escriben en el log del servidor con el usuario y la dirección cliente. Reiniciar Bootimus borra los contadores. Los inicios de sesión OIDC pasan por el proveedor de identidad y no se cuentan.

## Roles

Bootimus tiene dos roles:

| Rol | Acceso |
|-----|--------|
| **Admin** | Todo. |
| **Read-only** | Puede ver el dashboard, los clientes (incluido el inventario de hardware, el historial de arranque y el estado de energía del BMC), los grupos de clientes, las imágenes, los archivos públicos, los perfiles de distribución, el menú de arranque, los bootloaders, las herramientas, los clústeres y nodos de Kubernetes, las tareas programadas y el historial de intentos de arranque. |

Los usuarios de solo lectura:

- no pueden cambiar nada — el servidor rechaza cualquier escritura con `403 Your account has read-only access`, y el panel admin oculta los botones
- no pueden ver los usuarios, los ajustes de webhooks, las copias de seguridad, el registro de auditoría, el log del servidor en vivo, los archivos y scripts de autoinstalación, ni las configuraciones de máquina de los clústeres
- nunca reciben las contraseñas del BMC (IPMI/Redfish) ni los parches de configuración de los clientes — se eliminan de todas las respuestas de la API que se les envían

Un usuario es administrador si:

- **Local**: la casilla **Administrator** está marcada en su cuenta
- **LDAP**: coincide con `BOOTIMUS_LDAP_GROUP_FILTER` (o no hay filtro de grupo definido)
- **OIDC**: está en alguno de los grupos de `BOOTIMUS_OIDC_ADMIN_GROUPS` o figura en `BOOTIMUS_OIDC_ADMIN_USERS`

Todos los demás que pueden iniciar sesión son de solo lectura. `GET /api/me` devuelve el rol del usuario con la sesión iniciada.

> **Actualización:** antes de añadir el acceso de solo lectura, los usuarios locales sin la casilla **Administrator** marcada podían iniciar sesión pero no veían nada. Ahora tienen acceso de solo lectura. Deshabilita las cuentas de ese tipo que no quieras que lo tengan.

## Autenticación de la API

Todos los endpoints de la API (excepto `/api/login`, `/api/auth-info` y los endpoints de inicio de sesión `/api/oidc/`) requieren un token JWT válido. Los usuarios de solo lectura solo pueden hacer peticiones `GET`, y únicamente a los endpoints indicados en [Roles](#roles).

### Obtener un token

```bash
# Login and get token
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}' | jq -r '.data.token')

echo $TOKEN
```

### Usar el token

```bash
# Include in all API requests
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients

# Example: list images
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images
```

### Detalles del token

- **Algoritmo**: HMAC-SHA256
- **Expiración**: 24 horas desde la emisión
- **Secret**: Generado aleatoriamente en cada arranque del servidor (todos los tokens se invalidan al reiniciar)
- **Claims**: Username, estado admin, hora de emisión, expiración

### Tokens de API

Para scripts, trabajos de CI e integraciones, crea un token de larga duración en **Users** → **API Tokens** en lugar de iniciar sesión con contraseña. Los tokens empiezan por `btk_`, se muestran una sola vez al crearlos y se almacenan hasheados. Cada token tiene un ámbito:

| Ámbito | Qué puede hacer |
|--------|-----------------|
| `read` | Peticiones `GET` a los mismos endpoints que puede ver un usuario de solo lectura |
| `admin` | Todo lo que puede hacer un administrador, salvo crear o revocar tokens |

A los tokens se les puede dar una caducidad en días y revocarlos en cualquier momento. El registro de auditoría anota los cambios hechos con un token como `token:<name>`, y `/api/me` devuelve `"principal": "token"`.

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

A diferencia de los JWT, los tokens de API sobreviven a un reinicio de Bootimus.

Los tokens y las contraseñas con que se obtienen viajan en texto claro salvo que la interfaz admin se sirva por HTTPS. Actívalo con `--admin-tls` o pon un reverse proxy que termine TLS delante del puerto admin; ver [Proteger la interfaz admin](deployment.md#proteger-la-interfaz-admin).

### Comprobar backends de auth disponibles

```bash
# No authentication required
curl http://localhost:8081/api/auth-info
```

Respuesta:
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

Bootimus soporta autenticación LDAP como backend adicional. Cuando está configurado, los usuarios pueden elegir entre autenticación local y LDAP en la página de login. Las cuentas locales siempre funcionan como fallback.

### Cómo funciona

1. El usuario selecciona "LDAP" en la página de login e introduce las credenciales
2. Bootimus se conecta al servidor LDAP usando la cuenta de servicio (bind DN)
3. Busca al usuario por el filtro configurado
4. Intenta bindear como el usuario encontrado con la contraseña proporcionada
5. Si tiene éxito, comprueba la membresía de grupo para acceso admin
6. Crea o actualiza un registro de usuario local para él (mira [Usuarios de LDAP y OIDC](#usuarios-de-ldap-y-oidc))
7. Emite un token JWT (igual que en auth local)

### Ejemplo con Active Directory

```bash
# Environment variables
export BOOTIMUS_LDAP_HOST=dc.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="service-account-password"
export BOOTIMUS_LDAP_USER_FILTER="(sAMAccountName=%s)"
export BOOTIMUS_LDAP_GROUP_FILTER="cn=bootimus-admins"
```

### Ejemplo con OpenLDAP

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

### Ejemplo con Docker Compose

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

### Membresía de grupo admin

Si `BOOTIMUS_LDAP_GROUP_FILTER` está definido, solo los usuarios miembros del grupo coincidente obtienen acceso admin. La membresía de grupo se comprueba mediante:

1. El atributo `memberOf` en el objeto de usuario
2. Una búsqueda de grupo si `memberOf` no está disponible

Si `BOOTIMUS_LDAP_GROUP_FILTER` **no está definido**, todos los usuarios LDAP obtienen acceso admin.

Los derechos de admin se reevalúan en cada login, así que si quitas a alguien del grupo, pierde el rol de admin la próxima vez que inicie sesión.

### Login por API con LDAP

```bash
# Specify auth_method: "ldap"
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"jdoe","password":"ldap-password","auth_method":"ldap"}' | jq -r '.data.token')
```

## OIDC / inicio de sesión único

Bootimus puede autenticar usuarios mediante cualquier proveedor OpenID Connect — Authentik, Keycloak, Zitadel, Authelia, Entra ID, Google Workspace y otros. Los usuarios hacen click en **Sign in with …** en la página de login, se autentican en el proveedor y vuelven a Bootimus con la sesión ya iniciada. Las cuentas se crean automáticamente en el primer login, y los derechos de admin siguen los grupos del usuario en el proveedor.

### Cómo funciona

1. El usuario hace click en **Sign in with …** en la página de login
2. Bootimus lo redirige al proveedor (flujo de código de autorización con PKCE)
3. El usuario inicia sesión en el proveedor, que lo redirige de vuelta a `/api/oidc/callback`
4. Bootimus intercambia el código por un ID token, verifica su firma, audiencia, expiración y nonce, y lee los claims de username y grupos
5. Si hay grupos o usuarios permitidos configurados, el usuario debe coincidir con alguno de ellos (o ser administrador)
6. Se crea o actualiza un registro de usuario local (mira [Usuarios de LDAP y OIDC](#usuarios-de-ldap-y-oidc)), como administrador si está en un grupo admin o en la lista de usuarios admin, y de solo lectura en caso contrario
7. El navegador vuelve al panel admin con un token JWT (igual que en auth local)

Si el ID token no incluye el claim de username o de grupos, Bootimus también consulta el endpoint userinfo del proveedor.

### Configuración del proveedor

Crea un cliente OIDC (a veces llamado aplicación o relying party) en tu proveedor con:

| Ajuste | Valor |
|--------|-------|
| Tipo de cliente | Confidencial (con client secret) |
| Tipo de grant | Código de autorización |
| Redirect URI | `https://bootimus.example.com/api/oidc/callback` — la URL con la que los usuarios acceden al panel admin, más `/api/oidc/callback` |
| Scopes | `openid`, `profile`, `email` |

Asegúrate de que el proveedor incluya los grupos del usuario en un claim `groups`. Bootimus no adivina su propia URL pública, así que `BOOTIMUS_OIDC_REDIRECT_URL` debe coincidir exactamente con la redirect URI registrada en el proveedor.

### Ejemplo con Authentik

1. En Authentik, ve a **Applications → Providers** y crea un **OAuth2/OpenID Provider**:
   - **Name**: `bootimus`
   - **Authorisation flow**: tu flujo de consentimiento explícito o implícito habitual
   - **Client type**: Confidential
   - **Redirect URIs/Origins**: `https://bootimus.example.com/api/oidc/callback` (strict)
   - **Scopes**: mantén los mappings por defecto `openid`, `email` y `profile` — el mapping `profile` de Authentik incluye el claim `groups`
   - **Subject mode**: cualquiera; Bootimus usa `preferred_username` como username por defecto
2. Anota el **Client ID** y el **Client Secret**
3. Ve a **Applications → Applications** y crea una aplicación con slug `bootimus` usando ese provider. Opcionalmente, vincúlale una política o grupo para controlar quién puede lanzarla.
4. Crea grupos para los administradores de Bootimus (p. ej. `bootimus-admins`) y, opcionalmente, para otros usuarios permitidos (p. ej. `bootimus-operators`)
5. Configura Bootimus:

```bash
export BOOTIMUS_OIDC_ISSUER=https://authentik.example.com/application/o/bootimus/
export BOOTIMUS_OIDC_CLIENT_ID=<client id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_ALLOWED_GROUPS=bootimus-operators
export BOOTIMUS_OIDC_DISPLAY_NAME=Authentik
```

La URL del issuer aparece en la página de resumen del provider en Authentik como **OpenID Configuration Issuer**, y siempre termina en `/application/o/<application slug>/`.

### Ejemplo con Keycloak

Crea un cliente OpenID Connect confidencial con **Standard flow** habilitado y la redirect URI anterior. Keycloak no envía los grupos por defecto: añade un mapper **Group Membership** al dedicated scope del cliente con el nombre de claim de token `groups` y **Full group path** desactivado.

```bash
export BOOTIMUS_OIDC_ISSUER=https://keycloak.example.com/realms/example
export BOOTIMUS_OIDC_CLIENT_ID=bootimus
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_DISPLAY_NAME=Keycloak
```

### Ejemplo con Microsoft Entra ID

Entra ID envía los *IDs* de los grupos, y no sus nombres, en su claim de grupos, así que la forma más sencilla de mapear roles es con **app roles**, que Entra envía en un claim `roles`.

1. En el [Microsoft Entra admin centre](https://entra.microsoft.com), ve a **App registrations → New registration**:
   - **Name**: `Bootimus`
   - **Supported account types**: *Accounts in this organisational directory only (Single tenant)*
   - **Redirect URI**: plataforma **Web**, `https://bootimus.example.com/api/oidc/callback`
2. Anota el **Application (client) ID** y el **Directory (tenant) ID** de la página de resumen
3. En **Certificates & secrets**, crea un **New client secret** y copia su **Value**
4. En **App roles**, crea dos roles, cada uno con **Allowed member types** *Users/Groups*:
   - Display name `Bootimus Admin`, value `Bootimus.Admin`
   - Display name `Bootimus Read-only`, value `Bootimus.ReadOnly`
5. Ve a **Enterprise applications → Bootimus → Users and groups** y asigna usuarios o grupos a los roles. Asignar grupos requiere Entra ID P1 o P2; en el nivel gratuito, asigna los usuarios de uno en uno.
6. Opcionalmente, en **Enterprise applications → Bootimus → Properties**, establece **Assignment required?** en **Yes** para que solo puedan iniciar sesión los usuarios asignados
7. Configura Bootimus:

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

Mantén el claim de username por defecto, `preferred_username`, que es el nombre de inicio de sesión del usuario (p. ej. `alice@example.com`). No uses `email`: Entra no lo verifica, y Bootimus rechaza los usernames de tipo email que el proveedor no ha marcado como verificados.

Si prefieres usar grupos de seguridad, añade un **groups claim** en **Token configuration** y lista los **Object IDs** de los grupos en `BOOTIMUS_OIDC_ADMIN_GROUPS` y `BOOTIMUS_OIDC_ALLOWED_GROUPS`, dejando `BOOTIMUS_OIDC_GROUPS_CLAIM` como `groups`.

### Ejemplo con Google

Google no envía la membresía de grupos ni un claim `preferred_username`, así que con Google se usan direcciones de email como usernames y se concede el acceso con listas de usuarios y dominios en lugar de grupos.

1. En la [Google Cloud console](https://console.cloud.google.com), selecciona o crea un proyecto y ve a **APIs & Services → OAuth consent screen** (llamada **Google Auth Platform** en las versiones más recientes de la consola):
   - **User type**: *Internal* si usas Google Workspace, de modo que solo puedan iniciar sesión las cuentas de tu organización. *External* permite cualquier cuenta de Google, así que en ese caso define siempre `BOOTIMUS_OIDC_ALLOWED_USERS`.
   - Añade los scopes `openid`, `email` y `profile`
2. Ve a **APIs & Services → Credentials → Create credentials → OAuth client ID**:
   - **Application type**: Web application
   - **Authorised redirect URIs**: `https://bootimus.example.com/api/oidc/callback` (Google solo acepta HTTPS, salvo `http://localhost`)
3. Anota el **Client ID** y el **Client secret**
4. Configura Bootimus:

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

Aquí Alice y Bob son administradores y todos los demás con una dirección `@example.com` son de solo lectura. Bootimus solo acepta usernames de tipo email que Google marque como verificados.

### Mapeo de acceso

| Ajuste | Efecto |
|--------|--------|
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | Los miembros de cualquiera de estos grupos son administradores. |
| `BOOTIMUS_OIDC_ADMIN_USERS` | Estos usuarios son administradores. Las entradas son usernames, o `@domain` para coincidir con todos los usernames que terminen en ese dominio. |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | Si está definido, los miembros de estos grupos pueden iniciar sesión como usuarios de solo lectura. |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | Si está definido, estos usuarios (usernames o entradas `@domain`) pueden iniciar sesión como usuarios de solo lectura. |

Si no se define ninguna de las listas de permitidos, **cualquiera que el proveedor autentique puede iniciar sesión con acceso de solo lectura** — Bootimus registra una advertencia al arrancar cuando es el caso. Una vez definida cualquiera de las listas de permitidos, solo pueden iniciar sesión los usuarios que coincidan con una lista de permitidos o de admin. Si no se define ninguna lista de admin, ningún usuario OIDC es administrador.

Las cuatro aceptan una lista separada por comas, así que los nombres de grupo pueden contener espacios (`Bootimus Admins,Infrastructure`). La comparación ignora mayúsculas y minúsculas; una entrada `@domain` coincide solo con ese dominio exacto, no con sus subdominios. Los grupos y las listas se vuelven a comprobar en cada login, así que los cambios se aplican la próxima vez que el usuario inicie sesión.

### Ejemplo con Docker Compose

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

### Notas de seguridad

- El state del login, el nonce y el verifier PKCE se guardan en una cookie `HttpOnly` firmada y de corta duración (10 minutos), limitada a `/api/oidc/`. Se marca como `Secure` cuando la redirect URL usa HTTPS — sirve el panel admin por HTTPS en producción.
- Solo se usa el flujo de código de autorización; los tokens del proveedor nunca llegan al navegador. El navegador recibe el JWT habitual de Bootimus.
- El login local con contraseña sigue funcionando junto a OIDC, así que la cuenta local `admin` sigue disponible para recuperación.

## Usuarios de LDAP y OIDC

Los usuarios que inician sesión mediante LDAP u OIDC obtienen un registro de usuario local la primera vez que se autentican. Esto te permite verlos en **Users**, deshabilitarlos y que su acceso se compruebe en cada petición igual que el de los usuarios locales.

- **Source**: cada usuario se marca como `Local`, `LDAP` u `OIDC`. Un usuario solo puede iniciar sesión por su propio origen: un login OIDC o LDAP nunca se apropiará de una cuenta local con el mismo nombre (como `admin`), y los usuarios LDAP y OIDC no pueden usar el login local con contraseña.
- **Los usernames** se pasan a minúsculas y pueden contener letras, dígitos y `. _ @ + -`. Los logins con otros caracteres se rechazan.
- **Los derechos de admin** siguen los grupos del usuario (y, en OIDC, la lista de usuarios admin) y se actualizan en cada login, así que cambiar la casilla **Administrator** de un usuario LDAP u OIDC solo dura hasta que vuelva a iniciar sesión. Los usuarios que no son administradores son de solo lectura (mira [Roles](#roles)).
- **Deshabilitar** un usuario en Bootimus lo bloquea de inmediato, aunque el proveedor todavía le permita iniciar sesión.
- **Borrar** un usuario solo elimina el registro local; se vuelve a crear en su próximo login. Usa los grupos del proveedor, o deshabilita al usuario, para quitarle el acceso.
- **Las contraseñas** de los usuarios LDAP y OIDC las gestiona el proveedor, así que no se ofrece **Reset Password** para ellos y `bootimus user set-password` los rechaza.

Si alguien inicia sesión con un nombre que ya pertenece a un usuario de otro origen, el login se rechaza con *a user with this name already exists from a different source*. Esto incluye cuentas locales creadas antes de que los usuarios LDAP se aprovisionaran automáticamente. Borra la cuenta local antigua para que se pueda recrear desde LDAP u OIDC en el siguiente login.

## Referencia de configuración

### Flags de CLI

| Flag | Default | Descripción |
|------|---------|-------------|
| `--ldap-host` | *(vacío)* | Hostname del servidor LDAP (habilita auth LDAP) |
| `--ldap-port` | `389` | Puerto del servidor LDAP |
| `--ldap-tls` | `false` | Usar LDAPS (TLS al conectar) |
| `--ldap-starttls` | `false` | Usar StartTLS tras conectar |
| `--ldap-skip-verify` | `false` | Saltarse la verificación del certificado TLS |
| `--ldap-bind-dn` | *(vacío)* | DN de la cuenta de servicio para búsqueda de usuarios |
| `--ldap-bind-password` | *(vacío)* | Contraseña de la cuenta de servicio |
| `--ldap-base-dn` | *(vacío)* | Base DN para búsqueda de usuarios |
| `--ldap-user-filter` | `(sAMAccountName=%s)` | Filtro de búsqueda de usuarios (`%s` = username) |
| `--ldap-group-filter` | *(vacío)* | CN del grupo para acceso admin |
| `--ldap-group-base-dn` | *(vacío)* | Base DN para búsqueda de grupos (por defecto, el base DN) |
| `--oidc-issuer` | *(vacío)* | URL del issuer OIDC (habilita el login OIDC) |
| `--oidc-client-id` | *(vacío)* | Client ID de OIDC |
| `--oidc-client-secret` | *(vacío)* | Client secret de OIDC |
| `--oidc-redirect-url` | *(vacío)* | Redirect URL registrada en el proveedor, terminada en `/api/oidc/callback` |
| `--oidc-scopes` | `openid,profile,email` | Scopes a solicitar separados por comas (`openid` siempre se incluye) |
| `--oidc-username-claim` | `preferred_username` | Claim usado como username de Bootimus (p. ej. `email`) |
| `--oidc-groups-claim` | `groups` | Claim que lista los grupos del usuario |
| `--oidc-admin-groups` | *(vacío)* | Grupos separados por comas cuyos miembros son administradores |
| `--oidc-allowed-groups` | *(vacío)* | Grupos separados por comas con permiso para iniciar sesión |
| `--oidc-admin-users` | *(vacío)* | Usernames o entradas `@domain` separados por comas que son administradores |
| `--oidc-allowed-users` | *(vacío)* | Usernames o entradas `@domain` separados por comas con permiso para iniciar sesión |
| `--oidc-display-name` | `SSO` | Nombre mostrado en el botón de login |
| `--oidc-skip-verify` | `false` | Saltarse la verificación del certificado TLS al comunicarse con el proveedor |

### Variables de entorno

Todos los flags se pueden definir vía variables de entorno con el prefijo `BOOTIMUS_`:

| Variable | Mapea a |
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

### Archivo de configuración (bootimus.yaml)

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

## Solución de problemas

### Falló la conexión LDAP

Revisa conectividad y settings de TLS:
```bash
# Test LDAP connection
ldapsearch -H ldap://dc.example.com -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"

# Test LDAPS
ldapsearch -H ldaps://dc.example.com:636 -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"
```

### Usuario no encontrado

Verifica que el filtro de usuario devuelva resultados:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" dn
```

Filtros comunes:
- Active Directory: `(sAMAccountName=%s)`
- OpenLDAP: `(uid=%s)`
- Basado en email: `(mail=%s)`

### Usuario LDAP no es admin

Comprueba la membresía de grupo:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" memberOf
```

### OIDC: "Unable to reach the identity provider"

Bootimus obtiene `<issuer>/.well-known/openid-configuration` cuando el primer usuario inicia sesión. Revisa el log del servidor para ver el error exacto, y comprueba que el host de Bootimus pueda alcanzar el issuer:

```bash
curl https://authentik.example.com/application/o/bootimus/.well-known/openid-configuration
```

El valor `issuer` de ese documento debe coincidir exactamente con `BOOTIMUS_OIDC_ISSUER`, incluida cualquier barra final. Si el proveedor usa una CA privada, añádela al almacén de confianza del host; `BOOTIMUS_OIDC_SKIP_VERIFY=true` sirve para pruebas pero no se recomienda.

### OIDC: el proveedor muestra un error de redirect URI

La redirect URI registrada en el proveedor y `BOOTIMUS_OIDC_REDIRECT_URL` deben coincidir exactamente — esquema, host, puerto y ruta.

### OIDC: "Sign-in expired or was started elsewhere"

El login debe completarse en 10 minutos, en el mismo navegador que lo inició, y Bootimus no debe haberse reiniciado entretanto. Si sigue ocurriendo detrás de un reverse proxy, asegúrate de que el proxy deje pasar las cookies y de que los usuarios accedan a Bootimus por el mismo host que la redirect URL.

### OIDC: el usuario no es administrador o es rechazado

El log del servidor registra por qué se rechazó cada login. Comprueba qué grupos envía realmente el proveedor — los nombres de grupo deben coincidir con `BOOTIMUS_OIDC_ADMIN_GROUPS` o `BOOTIMUS_OIDC_ALLOWED_GROUPS`, y Keycloak envía rutas completas (`/bootimus-admins`) a menos que **Full group path** esté desactivado. Si tu proveedor usa otro nombre de claim, define `BOOTIMUS_OIDC_GROUPS_CLAIM` (los app roles de Entra ID usan `roles`). Para proveedores sin grupos, como Google, usa `BOOTIMUS_OIDC_ADMIN_USERS` y `BOOTIMUS_OIDC_ALLOWED_USERS`.

### OIDC: "email … is not marked as verified by the provider"

Cuando `BOOTIMUS_OIDC_USERNAME_CLAIM` es `email`, el proveedor debe enviar `email_verified: true`; de lo contrario, cualquiera que pudiera establecer una dirección sin verificar podría iniciar sesión como otra persona. Verifica la dirección en el proveedor, o usa otro claim de username como `preferred_username`.

### "A user with this name already exists from a different source"

Mira [Usuarios de LDAP y OIDC](#usuarios-de-ldap-y-oidc). Borra la cuenta existente en **Users** e inicia sesión de nuevo.

### Token expirado

Los tokens JWT son válidos durante 24 horas. Tras la expiración, la página de login se muestra automáticamente. Los tokens también se invalidan cuando el servidor se reinicia (el secret de firma se regenera).

### Admin local bloqueado

Resetea la contraseña admin:
```bash
./bootimus serve --reset-admin-password
```

O, si arrancar el servidor es incómodo (p. ej. conflictos de puertos), establece
la contraseña directamente en la base de datos:
```bash
./bootimus user set-password admin
```

Esto siempre funciona, independientemente de la configuración LDAP u OIDC.
