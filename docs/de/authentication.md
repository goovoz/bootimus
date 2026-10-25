# Authentifizierungs-Leitfaden

Bootimus nutzt JWT-Authentifizierung (JSON Web Token) für das Admin-Panel. Optional kannst du einen LDAP- oder Active-Directory-Server oder einen OpenID-Connect-Identity-Provider (OIDC) wie Authentik oder Keycloak für Single Sign-On anbinden.

## Inhaltsverzeichnis

- [Lokale Authentifizierung](#lokale-authentifizierung)
- [Login-Ablauf](#login-ablauf)
  - [Zwei-Faktor-Authentifizierung](#zwei-faktor-authentifizierung)
- [Rollen](#rollen)
- [API-Authentifizierung](#api-authentifizierung)
  - [API-Token](#api-token)
- [LDAP / Active Directory](#ldap--active-directory)
- [OIDC / Single Sign-On](#oidc--single-sign-on)
- [Nutzer aus LDAP und OIDC](#nutzer-aus-ldap-und-oidc)
- [Konfigurations-Referenz](#konfigurations-referenz)
- [Fehlersuche](#fehlersuche)

## Lokale Authentifizierung

Standardmäßig nutzt Bootimus lokale Benutzerkonten, die in der Datenbank (SQLite oder PostgreSQL) gespeichert sind.

### Standard-Admin-Konto

Beim ersten Start wird ein zufälliges Passwort generiert und in die Server-Logs geschrieben:

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

### Admin-Passwort zurücksetzen

Auf ein neues zufälliges Passwort zurücksetzen (startet eine vollständige Server-Instanz):
```bash
./bootimus serve --reset-admin-password
# oder mit Docker
docker exec bootimus /bootimus serve --reset-admin-password
```

Oder ein bestimmtes Passwort direkt in der Datenbank setzen, ohne einen Port
zu binden (ideal für die Notfallwiederherstellung):
```bash
# Interaktive Abfrage (verdeckte Eingabe, hält das Passwort aus der Shell-Historie heraus)
./bootimus user set-password admin

# Oder es für Skripte nicht-interaktiv übergeben
./bootimus user set-password admin --password 'neues-passwort'
```

### Nutzerverwaltung

Zusätzliche Nutzer können im Tab **Users** des Admin-Panels angelegt werden. Jeder Nutzer hat:
- **Username**: Eindeutiger Login-Name
- **Password**: Als bcrypt-Hash gespeichert
- **Admin**: Ob der Nutzer Administrator ist; alle anderen Nutzer sind schreibgeschützt (siehe [Rollen](#rollen))
- **Enabled**: Kann ohne Löschung deaktiviert werden
- **Source**: `Local`, `LDAP` oder `OIDC` — woher das Konto stammt (siehe [Nutzer aus LDAP und OIDC](#nutzer-aus-ldap-und-oidc))

Nutzer können auch über die CLI verwaltet werden, ohne den Server zu starten
(nützlich für die Wiederherstellung). Diese Befehle wirken direkt auf die
konfigurierte Datenbank (SQLite oder PostgreSQL):
```bash
./bootimus user list                       # alle Nutzer und ihre Quelle auflisten
./bootimus user enable <username>          # ein Konto aktivieren
./bootimus user disable <username>         # ein Konto deaktivieren
./bootimus user set-admin <username>       # Admin-Rechte erteilen
./bootimus user unset-admin <username>     # Admin-Rechte entziehen
./bootimus user set-password <username>    # ein Passwort setzen (Abfrage, oder --password)
```

## Login-Ablauf

1. Navigiere zu `http://your-server:8081`
2. Die Login-Seite zeigt Benutzername- und Passwortfelder
3. Wenn LDAP konfiguriert ist, erscheint ein Auth-Dropdown zur Backend-Auswahl
4. Wenn OIDC konfiguriert ist, erscheint unter dem Formular ein Button **Sign in with …**
5. Bei erfolgreichem Login wird ein JWT-Token ausgestellt (24 Stunden gültig)
6. Der Token wird im Browser gespeichert und mit allen API-Requests mitgeschickt
7. Bei Logout oder Token-Ablauf wird wieder die Login-Seite gezeigt

### Zwei-Faktor-Authentifizierung

Lokale Konten können ein zeitbasiertes Einmalpasswort (TOTP) als zweiten Faktor hinzufügen. LDAP- und OIDC-Nutzern wird es hier nicht angeboten; dort ist der Identity-Provider für den zweiten Faktor zuständig.

**Aktivieren.** Öffne **Users** → **Two-Factor Authentication**, klicke **Set up**, scanne den QR-Code mit einer Authenticator-App (oder tippe das Secret ein) und bestätige mit einem Code aus der App. Bootimus zeigt dann zehn einmal verwendbare Recovery-Codes. Bewahre sie auf; sie werden nicht noch einmal angezeigt.

**Anmelden.** Nach einem korrekten Passwort fragt die Login-Seite nach dem sechsstelligen Code. Ein Recovery-Code funktioniert stattdessen, einmalig. Falsche Codes zählen wie falsche Passwörter zur [Brute-Force-Sperre](#brute-force-schutz).

**Gerät verloren.** Ein Administrator kann neben dem Nutzer **Reset 2FA** klicken oder `POST /api/users/totp/reset?id=<id>` aufrufen. Ist der einzige Administrator ausgesperrt, führe das auf dem Server aus:

```bash
bootimus user reset-totp admin
```

**Per API.** Skripte sollten [API-Token](#api-token) statt Passwort und TOTP verwenden. Meldest du dich doch mit einem TOTP-geschützten Konto an, antwortet `POST /api/login` zuerst mit `401` und `{"error":"totp_required","totp_required":true}`; wiederhole den Request mit denselben Zugangsdaten plus `"code":"123456"`.

### Brute-Force-Schutz

Passwort-Logins (lokal und LDAP) werden im Speicher ratenbegrenzt:

- Nach **5 Fehlversuchen** für denselben Benutzernamen innerhalb von 15 Minuten wird dieser Benutzername für 1 Minute gesperrt. Jeder weitere Fehlversuch, solange die letzten Fehlversuche noch im Zeitfenster liegen, verdoppelt die Sperre, bis zu 15 Minuten.
- Nach **20 Fehlversuchen** von derselben Client-Adresse innerhalb von 15 Minuten wird diese Adresse nach demselben Schema gesperrt. Die höhere Schwelle sorgt dafür, dass eine gemeinsam genutzte Reverse-Proxy-Adresse nicht durch die Tippfehler einer einzelnen Person gesperrt wird.
- Ein erfolgreicher Login setzt den Fehlerzähler des Benutzernamens zurück.

Während der Sperre antwortet die API mit `429 Too Many Requests` und einem `Retry-After`-Header, und die Login-Seite zeigt an, wie lange zu warten ist. Falsche Zugangsdaten liefern weiterhin das übliche `401`, die Antwort verrät also nicht, ob ein Benutzername existiert. Fehlversuche werden mit Benutzername und Client-Adresse ins Server-Log geschrieben. Ein Neustart von Bootimus setzt die Zähler zurück. OIDC-Anmeldungen laufen über den Identity-Provider und werden nicht gezählt.

## Rollen

Bootimus kennt zwei Rollen:

| Rolle | Zugriff |
|------|--------|
| **Admin** | Alles. |
| **Read-only** | Kann das Dashboard, Clients (einschließlich Hardware-Inventar, Boot-Verlauf und BMC-Power-Status), Client-Gruppen, Images, öffentliche Dateien, Distro-Profile, das Boot-Menü, Bootloader, Tools, Kubernetes-Cluster und -Nodes, geplante Aufgaben und den Verlauf der Boot-Versuche ansehen. |

Schreibgeschützte Nutzer:

- können nichts ändern — der Server lehnt jeden Schreibzugriff mit `403 Your account has read-only access` ab, und das Admin-Panel blendet die Buttons aus
- sehen keine Nutzer, Webhook-Einstellungen, Backups, das Audit-Log, das Live-Server-Log, Auto-Install-Dateien und -Skripte oder Machine-Configs von Clustern
- erhalten nie BMC-Passwörter (IPMI/Redfish) oder Config-Patches von Clients — diese werden aus jeder API-Antwort entfernt, die an sie geht

Ein Nutzer ist Administrator, wenn:

- **Lokal**: bei seinem Konto die Checkbox **Administrator** angehakt ist
- **LDAP**: er auf `BOOTIMUS_LDAP_GROUP_FILTER` passt (oder kein Gruppenfilter gesetzt ist)
- **OIDC**: er in einer der `BOOTIMUS_OIDC_ADMIN_GROUPS` ist oder in `BOOTIMUS_OIDC_ADMIN_USERS` aufgeführt wird

Alle anderen, die sich anmelden können, sind schreibgeschützt. `GET /api/me` liefert die Rolle des angemeldeten Nutzers.

> **Upgrade:** Bevor der schreibgeschützte Zugriff eingeführt wurde, konnten sich lokale Nutzer ohne angehakte Checkbox **Administrator** zwar anmelden, sahen aber nichts. Sie haben jetzt schreibgeschützten Zugriff. Deaktiviere Konten, die ihn nicht haben sollen.

## API-Authentifizierung

Alle API-Endpunkte (außer `/api/login`, `/api/auth-info` und den `/api/oidc/`-Anmelde-Endpunkten) verlangen einen gültigen JWT-Token. Schreibgeschützte Nutzer können nur `GET`-Requests senden, und zwar nur an die unter [Rollen](#rollen) aufgeführten Endpunkte.

### Token holen

```bash
# Login und Token holen
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}' | jq -r '.data.token')

echo $TOKEN
```

### Token verwenden

```bash
# In allen API-Requests mitgeben
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients

# Beispiel: Images auflisten
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images
```

### Token-Details

- **Algorithmus**: HMAC-SHA256
- **Ablauf**: 24 Stunden nach Ausstellung
- **Secret**: Zufällig bei jedem Server-Start generiert (alle Token werden bei Neustart ungültig)
- **Claims**: Username, Admin-Status, Ausstellungszeit, Ablauf

### API-Token

Lege für Skripte, CI-Jobs und Integrationen unter **Users** → **API Tokens** einen langlebigen Token an, statt dich mit einem Passwort anzumelden. Token beginnen mit `btk_`, werden beim Anlegen einmalig angezeigt und gehasht gespeichert. Jeder Token hat einen Scope:

| Scope | Was er darf |
|-------|-------------|
| `read` | `GET`-Requests an dieselben Endpunkte, die ein schreibgeschützter Nutzer sehen kann |
| `admin` | Alles, was ein Administrator darf, außer Token anlegen oder widerrufen |

Token können ein Ablaufdatum in Tagen bekommen und jederzeit widerrufen werden. Das Audit-Log verzeichnet Änderungen, die mit einem Token gemacht wurden, als `token:<name>`, und `/api/me` meldet `"principal": "token"`.

```bash
# Token anlegen (als Admin-Nutzer)
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ansible","scope":"admin","expires_in_days":365}' \
  http://localhost:8081/api/tokens

# Genau wie einen JWT verwenden
curl -H "Authorization: Bearer btk_..." http://localhost:8081/api/clients

# Auflisten und widerrufen
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/tokens
curl -X DELETE -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/tokens?id=3"
```

Anders als JWTs überleben API-Token einen Neustart von Bootimus.

Token und die Passwörter, mit denen sie geholt werden, laufen im Klartext über die Leitung, solange die Admin-Oberfläche nicht über HTTPS ausgeliefert wird. Aktiviere das mit `--admin-tls` oder setze einen TLS-terminierenden Reverse-Proxy vor den Admin-Port; siehe [Admin-Oberfläche absichern](deployment.md#admin-oberfläche-absichern).

### Verfügbare Auth-Backends abfragen

```bash
# Keine Authentifizierung nötig
curl http://localhost:8081/api/auth-info
```

Antwort:
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

Bootimus unterstützt LDAP-Authentifizierung als zusätzliches Backend. Wenn konfiguriert, können Nutzer auf der Login-Seite zwischen lokaler und LDAP-Authentifizierung wählen. Lokale Konten funktionieren immer als Fallback.

### Funktionsweise

1. Nutzer wählt "LDAP" auf der Login-Seite und gibt Zugangsdaten ein
2. Bootimus verbindet sich mit dem LDAP-Server über das Service-Konto (Bind-DN)
3. Sucht den Nutzer über den konfigurierten Filter
4. Versucht ein Bind als gefundener Nutzer mit dem angegebenen Passwort
5. Bei Erfolg wird die Gruppenmitgliedschaft für Admin-Zugriff geprüft
6. Legt einen lokalen Nutzereintrag für ihn an oder aktualisiert ihn (siehe [Nutzer aus LDAP und OIDC](#nutzer-aus-ldap-und-oidc))
7. Stellt einen JWT-Token aus (genau wie bei lokaler Auth)

### Active-Directory-Beispiel

```bash
# Environment-Variablen
export BOOTIMUS_LDAP_HOST=dc.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="service-account-password"
export BOOTIMUS_LDAP_USER_FILTER="(sAMAccountName=%s)"
export BOOTIMUS_LDAP_GROUP_FILTER="cn=bootimus-admins"
```

### OpenLDAP-Beispiel

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

# Oder StartTLS auf Port 389 nutzen
export BOOTIMUS_LDAP_HOST=ldap.example.com
export BOOTIMUS_LDAP_STARTTLS=true

# Zertifikatsprüfung überspringen (nicht für Produktion empfohlen)
export BOOTIMUS_LDAP_SKIP_VERIFY=true
```

### Docker-Compose-Beispiel

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

### Admin-Gruppenmitgliedschaft

Wenn `BOOTIMUS_LDAP_GROUP_FILTER` gesetzt ist, bekommen nur Nutzer, die Mitglied der passenden Gruppe sind, Admin-Zugriff. Die Gruppenmitgliedschaft wird geprüft über:

1. Das `memberOf`-Attribut am User-Objekt
2. Eine Gruppen-Suchanfrage, falls `memberOf` nicht verfügbar ist

Wenn `BOOTIMUS_LDAP_GROUP_FILTER` **nicht gesetzt** ist, bekommen alle LDAP-Nutzer Admin-Zugriff.

Admin-Rechte werden bei jedem Login neu bewertet. Wird jemand aus der Gruppe entfernt, verliert er sie also beim nächsten Login.

### Login per API mit LDAP

```bash
# auth_method: "ldap" angeben
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"jdoe","password":"ldap-password","auth_method":"ldap"}' | jq -r '.data.token')
```

## OIDC / Single Sign-On

Bootimus kann Nutzer über jeden OpenID-Connect-Provider anmelden — Authentik, Keycloak, Zitadel, Authelia, Entra ID, Google Workspace und andere. Nutzer klicken auf der Login-Seite auf **Sign in with …**, authentifizieren sich beim Provider und werden bereits angemeldet zu Bootimus zurückgeleitet. Konten werden beim ersten Login automatisch angelegt, und die Admin-Rechte folgen den Gruppen des Nutzers beim Provider.

### Funktionsweise

1. Der Nutzer klickt auf der Login-Seite auf **Sign in with …**
2. Bootimus leitet ihn zum Provider weiter (Authorization-Code-Flow mit PKCE)
3. Der Nutzer meldet sich beim Provider an, der zurück zu `/api/oidc/callback` weiterleitet
4. Bootimus tauscht den Code gegen ein ID-Token, prüft Signatur, Audience, Ablauf und Nonce und liest die Claims für Username und Gruppen
5. Sind erlaubte Gruppen oder Nutzer konfiguriert, muss der Nutzer auf eine davon passen (oder Administrator sein)
6. Ein lokaler Nutzereintrag wird angelegt oder aktualisiert (siehe [Nutzer aus LDAP und OIDC](#nutzer-aus-ldap-und-oidc)), als Administrator, wenn der Nutzer in einer Admin-Gruppe ist oder auf der Admin-Nutzerliste steht, andernfalls als schreibgeschützter Nutzer
7. Der Browser wird mit einem JWT-Token zum Admin-Panel zurückgeschickt (genau wie bei lokaler Auth)

Enthält das ID-Token den Username- oder Gruppen-Claim nicht, fragt Bootimus zusätzlich den Userinfo-Endpunkt des Providers ab.

### Provider einrichten

Lege beim Provider einen OIDC-Client an (manchmal Application oder Relying Party genannt) mit:

| Einstellung | Wert |
|---------|-------|
| Client-Typ | Confidential (mit Client-Secret) |
| Grant-Typ | Authorization Code |
| Redirect-URI | `https://bootimus.example.com/api/oidc/callback` — die URL, unter der Nutzer das Admin-Panel erreichen, plus `/api/oidc/callback` |
| Scopes | `openid`, `profile`, `email` |

Stelle sicher, dass der Provider die Gruppen des Nutzers in einem `groups`-Claim mitliefert. Bootimus errät seine öffentliche URL nicht, daher muss `BOOTIMUS_OIDC_REDIRECT_URL` exakt mit der beim Provider registrierten Redirect-URI übereinstimmen.

### Authentik-Beispiel

1. Gehe in Authentik zu **Applications → Providers** und lege einen **OAuth2/OpenID Provider** an:
   - **Name**: `bootimus`
   - **Authorisation flow**: dein üblicher Explicit- oder Implicit-Consent-Flow
   - **Client type**: Confidential
   - **Redirect URIs/Origins**: `https://bootimus.example.com/api/oidc/callback` (strict)
   - **Scopes**: die Standard-Mappings `openid`, `email` und `profile` beibehalten — das `profile`-Mapping von Authentik enthält den `groups`-Claim
   - **Subject mode**: beliebig; Bootimus verwendet standardmäßig `preferred_username` als Username
2. **Client ID** und **Client Secret** notieren
3. Gehe zu **Applications → Applications** und lege eine Application mit dem Slug `bootimus` an, die diesen Provider nutzt. Optional kannst du eine Policy oder Gruppe daran binden, um zu steuern, wer sie überhaupt starten darf.
4. Lege Gruppen für Bootimus-Administratoren (z. B. `bootimus-admins`) und optional für weitere erlaubte Nutzer (z. B. `bootimus-operators`) an
5. Bootimus konfigurieren:

```bash
export BOOTIMUS_OIDC_ISSUER=https://authentik.example.com/application/o/bootimus/
export BOOTIMUS_OIDC_CLIENT_ID=<client id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_ALLOWED_GROUPS=bootimus-operators
export BOOTIMUS_OIDC_DISPLAY_NAME=Authentik
```

Die Issuer-URL zeigt Authentik auf der Übersichtsseite des Providers als **OpenID Configuration Issuer** an, und sie endet immer auf `/application/o/<Application-Slug>/`.

### Keycloak-Beispiel

Lege einen Confidential-OpenID-Connect-Client mit aktiviertem **Standard flow** und der oben genannten Redirect-URI an. Keycloak sendet Gruppen standardmäßig nicht: Füge dem Dedicated Scope des Clients einen **Group Membership**-Mapper mit dem Token-Claim-Namen `groups` hinzu und schalte **Full group path** aus.

```bash
export BOOTIMUS_OIDC_ISSUER=https://keycloak.example.com/realms/example
export BOOTIMUS_OIDC_CLIENT_ID=bootimus
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_DISPLAY_NAME=Keycloak
```

### Microsoft-Entra-ID-Beispiel

Entra ID sendet in seinem Groups-Claim Gruppen-*IDs* statt Namen. Am einfachsten ordnest du Rollen daher mit **App roles** zu, die Entra in einem `roles`-Claim sendet.

1. Gehe im [Microsoft Entra admin centre](https://entra.microsoft.com) zu **App registrations → New registration**:
   - **Name**: `Bootimus`
   - **Supported account types**: *Accounts in this organisational directory only (Single tenant)*
   - **Redirect URI**: Plattform **Web**, `https://bootimus.example.com/api/oidc/callback`
2. **Application (client) ID** und **Directory (tenant) ID** von der Übersichtsseite notieren
3. Lege unter **Certificates & secrets** ein **New client secret** an und kopiere dessen **Value**
4. Lege unter **App roles** zwei Rollen an, jeweils mit **Allowed member types** *Users/Groups*:
   - Display name `Bootimus Admin`, Value `Bootimus.Admin`
   - Display name `Bootimus Read-only`, Value `Bootimus.ReadOnly`
5. Gehe zu **Enterprise applications → Bootimus → Users and groups** und weise den Rollen Nutzer oder Gruppen zu. Gruppen zuzuweisen erfordert Entra ID P1 oder P2; in der kostenlosen Stufe weist du Nutzer einzeln zu.
6. Setze optional unter **Enterprise applications → Bootimus → Properties** **Assignment required?** auf **Yes**, damit sich überhaupt nur zugewiesene Nutzer anmelden können
7. Bootimus konfigurieren:

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

Behalte den Standard-Username-Claim `preferred_username` bei, der dem Anmeldenamen des Nutzers entspricht (z. B. `alice@example.com`). Verwende nicht `email`: Entra verifiziert ihn nicht, und Bootimus lehnt E-Mail-Usernames ab, die der Provider nicht als verifiziert markiert hat.

Möchtest du lieber Sicherheitsgruppen verwenden, füge unter **Token configuration** einen **groups claim** hinzu und trage die **Object IDs** der Gruppen in `BOOTIMUS_OIDC_ADMIN_GROUPS` und `BOOTIMUS_OIDC_ALLOWED_GROUPS` ein; `BOOTIMUS_OIDC_GROUPS_CLAIM` bleibt dann `groups`.

### Google-Beispiel

Google sendet weder Gruppenmitgliedschaften noch einen `preferred_username`-Claim. Bei Google verwendest du daher E-Mail-Adressen als Usernames und vergibst Zugriff über Nutzer- und Domain-Listen statt über Gruppen.

1. Wähle in der [Google Cloud console](https://console.cloud.google.com) ein Projekt aus oder lege eines an und gehe zu **APIs & Services → OAuth consent screen** (in neueren Versionen der Console **Google Auth Platform** genannt):
   - **User type**: *Internal*, wenn du Google Workspace nutzt, damit sich nur Konten deiner Organisation anmelden können. *External* erlaubt jedes Google-Konto, setze in diesem Fall also immer `BOOTIMUS_OIDC_ALLOWED_USERS`.
   - Füge die Scopes `openid`, `email` und `profile` hinzu
2. Gehe zu **APIs & Services → Credentials → Create credentials → OAuth client ID**:
   - **Application type**: Web application
   - **Authorised redirect URIs**: `https://bootimus.example.com/api/oidc/callback` (Google akzeptiert außer `http://localhost` nur HTTPS)
3. **Client ID** und **Client secret** notieren
4. Bootimus konfigurieren:

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

Hier sind Alice und Bob Administratoren, und alle anderen mit einer `@example.com`-Adresse sind schreibgeschützt. Bootimus akzeptiert nur E-Mail-Usernames, die Google als verifiziert markiert.

### Zugriffs-Mapping

| Einstellung | Wirkung |
|---------|--------|
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | Mitglieder einer dieser Gruppen sind Administratoren. |
| `BOOTIMUS_OIDC_ADMIN_USERS` | Diese Nutzer sind Administratoren. Einträge sind Usernames oder `@domain`, um jeden Username zu treffen, der auf diese Domain endet. |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | Wenn gesetzt, können sich Mitglieder dieser Gruppen als schreibgeschützte Nutzer anmelden. |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | Wenn gesetzt, können sich diese Nutzer (Usernames oder `@domain`-Einträge) als schreibgeschützte Nutzer anmelden. |

Ist keine der beiden Allowed-Listen gesetzt, **kann sich jeder, den der Provider authentifiziert, mit schreibgeschütztem Zugriff anmelden** — Bootimus schreibt in diesem Fall beim Start eine Warnung ins Log. Sobald eine der beiden Allowed-Listen gesetzt ist, können sich nur Nutzer anmelden, die auf eine Allowed- oder Admin-Liste passen. Ist keine der beiden Admin-Listen gesetzt, ist kein OIDC-Nutzer Administrator.

Alle vier erwarten eine kommagetrennte Liste, Gruppennamen dürfen also Leerzeichen enthalten (`Bootimus Admins,Infrastructure`). Beim Abgleich wird die Groß-/Kleinschreibung ignoriert; ein `@domain`-Eintrag trifft nur genau diese Domain, nicht ihre Subdomains. Gruppen und Listen werden bei jedem Login neu geprüft, Änderungen gelten also ab dem nächsten Login des Nutzers.

### Docker-Compose-Beispiel

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

### Sicherheitshinweise

- Sign-in-State, Nonce und PKCE-Verifier liegen in einem signierten, kurzlebigen (10 Minuten) `HttpOnly`-Cookie, das auf `/api/oidc/` beschränkt ist. Es wird als `Secure` markiert, wenn die Redirect-URL HTTPS nutzt — betreibe das Admin-Panel in Produktion über HTTPS.
- Es wird nur der Authorization-Code-Flow verwendet; Token des Providers erreichen den Browser nie. Der Browser erhält das übliche Bootimus-JWT.
- Der lokale Passwort-Login funktioniert weiterhin neben OIDC, das lokale `admin`-Konto bleibt also für die Wiederherstellung verfügbar.

## Nutzer aus LDAP und OIDC

Nutzer, die sich über LDAP oder OIDC anmelden, bekommen bei ihrem ersten Login einen lokalen Nutzereintrag. So siehst du sie unter **Users**, kannst sie deaktivieren, und ihr Zugriff wird bei jeder Anfrage wie bei lokalen Nutzern geprüft.

- **Source**: Jeder Nutzer ist als `Local`, `LDAP` oder `OIDC` markiert. Ein Nutzer kann sich nur über seine eigene Quelle anmelden: Ein OIDC- oder LDAP-Login übernimmt nie ein lokales Konto mit demselben Namen (z. B. `admin`), und LDAP- und OIDC-Nutzer können den lokalen Passwort-Login nicht verwenden.
- **Usernames** werden in Kleinbuchstaben umgewandelt und dürfen Buchstaben, Ziffern und `. _ @ + -` enthalten. Logins mit anderen Zeichen werden abgelehnt.
- **Admin-Rechte** folgen den Gruppen des Nutzers (und bei OIDC der Admin-Nutzerliste) und werden bei jedem Login aktualisiert. Eine Änderung der Checkbox **Administrator** bei einem LDAP- oder OIDC-Nutzer hält daher nur bis zu seinem nächsten Login. Nutzer, die keine Administratoren sind, sind schreibgeschützt (siehe [Rollen](#rollen)).
- **Deaktivieren** eines Nutzers in Bootimus sperrt ihn sofort, auch wenn der Provider ihn weiterhin anmelden lässt.
- **Löschen** eines Nutzers entfernt nur den lokalen Eintrag; er wird beim nächsten Login des Nutzers neu angelegt. Entziehe den Zugriff über die Gruppen des Providers oder deaktiviere den Nutzer.
- **Passwörter** von LDAP- und OIDC-Nutzern verwaltet der Provider, daher wird für sie kein **Reset Password** angeboten und `bootimus user set-password` lehnt sie ab.

Meldet sich jemand mit einem Namen an, der bereits einem Nutzer aus einer anderen Quelle gehört, wird der Login mit *a user with this name already exists from a different source* abgelehnt. Das betrifft auch lokale Konten, die angelegt wurden, bevor LDAP-Nutzer automatisch bereitgestellt wurden. Lösche das alte lokale Konto, damit es beim nächsten Login aus LDAP oder OIDC neu angelegt werden kann.

## Konfigurations-Referenz

### CLI-Flags

| Flag | Default | Beschreibung |
|------|---------|-------------|
| `--ldap-host` | *(leer)* | LDAP-Server-Hostname (aktiviert LDAP-Auth) |
| `--ldap-port` | `389` | LDAP-Server-Port |
| `--ldap-tls` | `false` | LDAPS verwenden (TLS beim Verbindungsaufbau) |
| `--ldap-starttls` | `false` | StartTLS nach Verbindungsaufbau verwenden |
| `--ldap-skip-verify` | `false` | TLS-Zertifikatsprüfung überspringen |
| `--ldap-bind-dn` | *(leer)* | Service-Konto-DN für die Nutzersuche |
| `--ldap-bind-password` | *(leer)* | Service-Konto-Passwort |
| `--ldap-base-dn` | *(leer)* | Base-DN für die Nutzersuche |
| `--ldap-user-filter` | `(sAMAccountName=%s)` | Nutzer-Suchfilter (`%s` = Username) |
| `--ldap-group-filter` | *(leer)* | Gruppen-CN für Admin-Zugriff |
| `--ldap-group-base-dn` | *(leer)* | Base-DN für die Gruppensuche (Default: Base-DN) |
| `--oidc-issuer` | *(leer)* | OIDC-Issuer-URL (aktiviert OIDC-Login) |
| `--oidc-client-id` | *(leer)* | OIDC-Client-ID |
| `--oidc-client-secret` | *(leer)* | OIDC-Client-Secret |
| `--oidc-redirect-url` | *(leer)* | Beim Provider registrierte Redirect-URL, endet auf `/api/oidc/callback` |
| `--oidc-scopes` | `openid,profile,email` | Kommagetrennte Scopes, die angefordert werden (`openid` ist immer enthalten) |
| `--oidc-username-claim` | `preferred_username` | Claim, der als Bootimus-Username verwendet wird (z. B. `email`) |
| `--oidc-groups-claim` | `groups` | Claim mit den Gruppen des Nutzers |
| `--oidc-admin-groups` | *(leer)* | Kommagetrennte Gruppen, deren Mitglieder Administratoren sind |
| `--oidc-allowed-groups` | *(leer)* | Kommagetrennte Gruppen, die sich anmelden dürfen |
| `--oidc-admin-users` | *(leer)* | Kommagetrennte Usernames oder `@domain`-Einträge, die Administratoren sind |
| `--oidc-allowed-users` | *(leer)* | Kommagetrennte Usernames oder `@domain`-Einträge, die sich anmelden dürfen |
| `--oidc-display-name` | `SSO` | Name auf dem Login-Button |
| `--oidc-skip-verify` | `false` | TLS-Zertifikatsprüfung bei der Kommunikation mit dem Provider überspringen |

### Environment-Variablen

Alle Flags lassen sich über Environment-Variablen mit dem Präfix `BOOTIMUS_` setzen:

| Variable | Entspricht |
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

### Konfigurationsdatei (bootimus.yaml)

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

## Fehlersuche

### LDAP-Verbindung fehlgeschlagen

Konnektivität und TLS-Einstellungen prüfen:
```bash
# LDAP-Verbindung testen
ldapsearch -H ldap://dc.example.com -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"

# LDAPS testen
ldapsearch -H ldaps://dc.example.com:636 -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"
```

### Nutzer nicht gefunden

Prüfe, ob der User-Filter Ergebnisse liefert:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" dn
```

Gängige Filter:
- Active Directory: `(sAMAccountName=%s)`
- OpenLDAP: `(uid=%s)`
- E-Mail-basiert: `(mail=%s)`

### LDAP-Nutzer ist kein Admin

Gruppenmitgliedschaft prüfen:
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" memberOf
```

### OIDC: "Unable to reach the identity provider"

Bootimus ruft `<issuer>/.well-known/openid-configuration` ab, wenn sich der erste Nutzer anmeldet. Prüfe im Server-Log den genauen Fehler und ob der Bootimus-Host den Issuer erreichen kann:

```bash
curl https://authentik.example.com/application/o/bootimus/.well-known/openid-configuration
```

Der `issuer`-Wert in diesem Dokument muss exakt mit `BOOTIMUS_OIDC_ISSUER` übereinstimmen, einschließlich eines abschließenden Schrägstrichs. Nutzt der Provider eine private CA, füge sie dem Trust Store des Hosts hinzu; `BOOTIMUS_OIDC_SKIP_VERIFY=true` funktioniert zum Testen, wird aber nicht empfohlen.

### OIDC: Provider zeigt Redirect-URI-Fehler

Die beim Provider registrierte Redirect-URI und `BOOTIMUS_OIDC_REDIRECT_URL` müssen exakt übereinstimmen — Schema, Host, Port und Pfad.

### OIDC: "Sign-in expired or was started elsewhere"

Der Login muss innerhalb von 10 Minuten im selben Browser abgeschlossen werden, der ihn gestartet hat, und Bootimus darf zwischenzeitlich nicht neu gestartet worden sein. Tritt das hinter einem Reverse-Proxy dauerhaft auf, stelle sicher, dass der Proxy Cookies durchreicht und Nutzer Bootimus unter demselben Host wie in der Redirect-URL erreichen.

### OIDC: Nutzer ist kein Admin oder wird abgelehnt

Das Server-Log hält fest, warum jeder Login abgelehnt wurde. Prüfe, welche Gruppen der Provider tatsächlich sendet — Gruppennamen müssen zu `BOOTIMUS_OIDC_ADMIN_GROUPS` bzw. `BOOTIMUS_OIDC_ALLOWED_GROUPS` passen, und Keycloak sendet vollständige Pfade (`/bootimus-admins`), solange **Full group path** nicht ausgeschaltet ist. Nutzt dein Provider einen anderen Claim-Namen, setze `BOOTIMUS_OIDC_GROUPS_CLAIM` (App Roles von Entra ID verwenden `roles`). Bei Providern ohne Gruppen, etwa Google, verwende `BOOTIMUS_OIDC_ADMIN_USERS` und `BOOTIMUS_OIDC_ALLOWED_USERS`.

### OIDC: "email … is not marked as verified by the provider"

Ist `BOOTIMUS_OIDC_USERNAME_CLAIM` auf `email` gesetzt, muss der Provider `email_verified: true` senden, sonst könnte sich jeder, der eine unverifizierte Adresse setzen kann, als jemand anderes anmelden. Verifiziere die Adresse beim Provider oder verwende einen anderen Username-Claim wie `preferred_username`.

### "A user with this name already exists from a different source"

Siehe [Nutzer aus LDAP und OIDC](#nutzer-aus-ldap-und-oidc). Lösche das bestehende Konto unter **Users** und melde dich erneut an.

### Token abgelaufen

JWT-Token sind 24 Stunden gültig. Nach Ablauf wird automatisch die Login-Seite gezeigt. Token werden außerdem ungültig, wenn der Server neu startet (das Signing-Secret wird neu erzeugt).

### Lokaler Admin ausgesperrt

Admin-Passwort zurücksetzen:
```bash
./bootimus serve --reset-admin-password
```

Oder, wenn das Starten des Servers unpraktisch ist (z. B. Portkonflikte), das
Passwort direkt in der Datenbank setzen:
```bash
./bootimus user set-password admin
```

Das funktioniert immer, unabhängig von der LDAP- oder OIDC-Konfiguration.
