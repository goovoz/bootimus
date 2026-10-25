# Guide d'authentification

Bootimus utilise l'authentification JWT (JSON Web Token) pour le panneau d'administration. Tu peux également connecter un serveur LDAP ou Active Directory, ou un fournisseur d'identité OpenID Connect (OIDC) comme Authentik ou Keycloak, pour l'authentification unique (SSO).

## Table des matières

- [Authentification locale](#authentification-locale)
- [Flux de connexion](#flux-de-connexion)
  - [Authentification à deux facteurs](#authentification-à-deux-facteurs)
- [Rôles](#rôles)
- [Authentification de l'API](#authentification-de-lapi)
  - [Tokens d'API](#tokens-dapi)
- [LDAP / Active Directory](#ldap--active-directory)
- [OIDC / authentification unique](#oidc--authentification-unique)
- [Utilisateurs LDAP et OIDC](#utilisateurs-ldap-et-oidc)
- [Référence de configuration](#référence-de-configuration)
- [Dépannage](#dépannage)

## Authentification locale

Par défaut, Bootimus utilise des comptes utilisateurs locaux stockés en base (SQLite ou PostgreSQL).

### Compte admin par défaut

Au premier démarrage, un mot de passe aléatoire est généré et affiché dans les logs du serveur :

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

### Réinitialiser le mot de passe admin

Réinitialiser vers un nouveau mot de passe aléatoire (démarre une instance complète du serveur) :
```bash
./bootimus serve --reset-admin-password
# ou avec Docker
docker exec bootimus /bootimus serve --reset-admin-password
```

Ou définir un mot de passe précis directement dans la base de données, sans
ouvrir de port (idéal pour la récupération d'urgence) :
```bash
# Saisie interactive (entrée masquée, garde le mot de passe hors de l'historique du shell)
./bootimus user set-password admin

# Ou le fournir de façon non interactive pour les scripts
./bootimus user set-password admin --password 'nouveau-mot-de-passe'
```

### Gestion des utilisateurs

Des utilisateurs supplémentaires peuvent être créés depuis l'onglet **Users** du panneau d'administration. Chaque utilisateur a :
- **Username** : nom de login unique
- **Password** : stocké en hash bcrypt
- **Admin** : si l'utilisateur est administrateur ; les autres utilisateurs sont en lecture seule (voir [Rôles](#rôles))
- **Enabled** : peut être désactivé sans suppression
- **Source** : `Local`, `LDAP` ou `OIDC` — d'où vient le compte (voir [Utilisateurs LDAP et OIDC](#utilisateurs-ldap-et-oidc))

Les utilisateurs peuvent aussi être gérés depuis la CLI sans démarrer le serveur
(utile pour la récupération). Ces commandes agissent directement sur la base de
données configurée (SQLite ou PostgreSQL) :
```bash
./bootimus user list                       # lister tous les utilisateurs et leur source
./bootimus user enable <username>          # activer un compte
./bootimus user disable <username>         # désactiver un compte
./bootimus user set-admin <username>       # accorder les droits admin
./bootimus user unset-admin <username>     # retirer les droits admin
./bootimus user set-password <username>    # définir un mot de passe (saisie interactive, ou --password)
```

## Flux de connexion

1. Va sur `http://your-server:8081`
2. La page de login s'affiche avec les champs nom d'utilisateur et mot de passe
3. Si LDAP est configuré, un menu déroulant d'authentification apparaît pour sélectionner le backend
4. Si OIDC est configuré, un bouton **Sign in with …** apparaît sous le formulaire
5. À la connexion réussie, un token JWT est émis (valable 24 heures)
6. Le token est stocké dans le navigateur et envoyé avec toutes les requêtes API
7. À la déconnexion ou à l'expiration du token, la page de login est réaffichée

### Authentification à deux facteurs

Les comptes locaux peuvent ajouter un mot de passe à usage unique basé sur le temps (TOTP) comme second facteur. Il n'est pas proposé ici aux utilisateurs LDAP et OIDC : c'est leur fournisseur d'identité qui gère le second facteur.

**Activation.** Ouvre **Users** → **Two-Factor Authentication**, clique sur **Set up**, scanne le QR code avec une application d'authentification (ou saisis le secret à la main), puis confirme avec un code de l'application. Bootimus affiche alors dix codes de récupération à usage unique. Conserve-les ; ils ne seront plus affichés.

**Connexion.** Après un mot de passe correct, la page de login demande le code à six chiffres. Un code de récupération fonctionne à sa place, une seule fois. Les codes erronés comptent pour le [verrouillage anti-force brute](#protection-contre-la-force-brute) comme les mots de passe erronés.

**Perte de l'appareil.** Un administrateur peut cliquer sur **Reset 2FA** à côté de l'utilisateur, ou appeler `POST /api/users/totp/reset?id=<id>`. Si le seul administrateur est bloqué, lance ceci sur le serveur :

```bash
bootimus user reset-totp admin
```

**Via l'API.** Les scripts doivent utiliser des [tokens d'API](#tokens-dapi) plutôt qu'un mot de passe et un TOTP. Si tu te connectes quand même avec un compte protégé par TOTP, `POST /api/login` répond d'abord `401` avec `{"error":"totp_required","totp_required":true}` ; renvoie la requête avec les mêmes identifiants plus `"code":"123456"`.

### Protection contre la force brute

Les connexions par mot de passe (locales et LDAP) sont limitées en mémoire :

- Après **5 tentatives échouées** pour le même nom d'utilisateur en 15 minutes, ce nom d'utilisateur est verrouillé 1 minute. Chaque échec supplémentaire tant que les échecs récents sont encore dans la fenêtre double le verrouillage, jusqu'à 15 minutes.
- Après **20 tentatives échouées** depuis la même adresse cliente en 15 minutes, cette adresse est verrouillée selon le même barème. Le seuil plus élevé évite qu'une adresse de reverse proxy partagée soit verrouillée par les fautes de frappe d'une seule personne.
- Une connexion réussie remet à zéro le compteur d'échecs du nom d'utilisateur.

Pendant le verrouillage, l'API répond `429 Too Many Requests` avec un en-tête `Retry-After` et la page de login indique combien de temps attendre. Des identifiants erronés renvoient toujours le `401` habituel, la réponse ne révèle donc pas si un nom d'utilisateur existe. Les tentatives échouées sont écrites dans le log du serveur avec le nom d'utilisateur et l'adresse cliente. Redémarrer Bootimus remet les compteurs à zéro. Les connexions OIDC passent par le fournisseur d'identité et ne sont pas comptées.

## Rôles

Bootimus a deux rôles :

| Rôle | Accès |
|------|--------|
| **Admin** | Tout. |
| **Read-only** | Peut consulter le tableau de bord, les clients (y compris l'inventaire matériel, l'historique de boot et l'état d'alimentation BMC), les groupes de clients, les images, les fichiers publics, les profils de distribution, le menu de boot, les bootloaders, les outils, les clusters et nœuds Kubernetes, les tâches planifiées et l'historique des tentatives de boot. |

Les utilisateurs en lecture seule :

- ne peuvent rien modifier — le serveur refuse toute écriture avec `403 Your account has read-only access`, et le panneau d'administration masque les boutons
- ne peuvent pas voir les utilisateurs, les paramètres des webhooks, les sauvegardes, le journal d'audit, le log du serveur en direct, les fichiers et scripts d'auto-installation, ni les machine configs des clusters
- ne reçoivent jamais les mots de passe BMC (IPMI/Redfish) ni les patches de config des clients — ils sont retirés de chaque réponse de l'API qui leur est envoyée

Un utilisateur est administrateur si :

- **Local** : la case **Administrator** est cochée sur son compte
- **LDAP** : il correspond à `BOOTIMUS_LDAP_GROUP_FILTER` (ou aucun filtre de groupe n'est défini)
- **OIDC** : il appartient à l'un des `BOOTIMUS_OIDC_ADMIN_GROUPS` ou figure dans `BOOTIMUS_OIDC_ADMIN_USERS`

Tous les autres utilisateurs qui peuvent se connecter sont en lecture seule. `GET /api/me` renvoie le rôle de l'utilisateur connecté.

> **Mise à niveau :** avant l'ajout de l'accès en lecture seule, les utilisateurs locaux dont la case **Administrator** n'était pas cochée pouvaient se connecter mais ne voyaient rien. Ils ont maintenant un accès en lecture seule. Désactive les comptes de ce type que tu ne veux pas laisser accéder au panneau.

## Authentification de l'API

Tous les endpoints de l'API (sauf `/api/login`, `/api/auth-info` et les endpoints de connexion `/api/oidc/`) requièrent un token JWT valide. Les utilisateurs en lecture seule ne peuvent faire que des requêtes `GET`, et uniquement vers les endpoints listés dans [Rôles](#rôles).

### Obtenir un token

```bash
# Login and get token
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your-password"}' | jq -r '.data.token')

echo $TOKEN
```

### Utiliser le token

```bash
# Include in all API requests
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients

# Example: list images
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images
```

### Détails du token

- **Algorithme** : HMAC-SHA256
- **Expiration** : 24 heures à partir de l'émission
- **Secret** : généré aléatoirement à chaque démarrage du serveur (tous les tokens sont invalidés au redémarrage)
- **Claims** : nom d'utilisateur, statut admin, heure d'émission, expiration

### Tokens d'API

Pour les scripts, les jobs CI et les intégrations, crée un token de longue durée dans **Users** → **API Tokens** au lieu de te connecter avec un mot de passe. Les tokens commencent par `btk_`, ne sont affichés qu'une fois à la création et sont stockés hachés. Chaque token a une portée :

| Portée | Ce qu'il peut faire |
|--------|---------------------|
| `read` | Les requêtes `GET` sur les mêmes endpoints qu'un utilisateur en lecture seule peut voir |
| `admin` | Tout ce qu'un administrateur peut faire, sauf créer ou révoquer des tokens |

Les tokens peuvent recevoir une expiration en jours et être révoqués à tout moment. Le journal d'audit enregistre les modifications faites avec un token sous `token:<name>`, et `/api/me` renvoie `"principal": "token"`.

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

Contrairement aux JWT, les tokens d'API survivent à un redémarrage de Bootimus.

Les tokens et les mots de passe utilisés pour les obtenir circulent en clair tant que l'interface admin n'est pas servie en HTTPS. Active-le avec `--admin-tls` ou place un reverse proxy terminant TLS devant le port admin ; voir [Sécuriser l'interface d'administration](deployment.md#sécuriser-linterface-dadministration).

### Vérifier les backends d'authentification disponibles

```bash
# No authentication required
curl http://localhost:8081/api/auth-info
```

Réponse :
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

Bootimus supporte l'authentification LDAP comme backend additionnel. Une fois configuré, les utilisateurs peuvent choisir entre l'authentification locale et LDAP sur la page de login. Les comptes locaux fonctionnent toujours en fallback.

### Fonctionnement

1. L'utilisateur sélectionne « LDAP » sur la page de login et entre ses identifiants
2. Bootimus se connecte au serveur LDAP avec le compte de service (bind DN)
3. Recherche l'utilisateur avec le filtre configuré
4. Tente un bind en tant qu'utilisateur trouvé avec le mot de passe fourni
5. En cas de succès, vérifie l'appartenance au groupe pour l'accès admin
6. Crée ou met à jour un enregistrement utilisateur local pour lui (voir [Utilisateurs LDAP et OIDC](#utilisateurs-ldap-et-oidc))
7. Émet un token JWT (comme pour l'auth locale)

### Exemple Active Directory

```bash
# Environment variables
export BOOTIMUS_LDAP_HOST=dc.example.com
export BOOTIMUS_LDAP_BASE_DN="dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_DN="cn=svc-bootimus,ou=Service Accounts,dc=example,dc=com"
export BOOTIMUS_LDAP_BIND_PASSWORD="service-account-password"
export BOOTIMUS_LDAP_USER_FILTER="(sAMAccountName=%s)"
export BOOTIMUS_LDAP_GROUP_FILTER="cn=bootimus-admins"
```

### Exemple OpenLDAP

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

### Exemple Docker Compose

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

### Appartenance au groupe admin

Si `BOOTIMUS_LDAP_GROUP_FILTER` est défini, seuls les utilisateurs membres du groupe correspondant obtiennent l'accès admin. L'appartenance au groupe est vérifiée via :

1. L'attribut `memberOf` sur l'objet utilisateur
2. Une requête de recherche de groupe si `memberOf` n'est pas disponible

Si `BOOTIMUS_LDAP_GROUP_FILTER` n'est **pas défini**, tous les utilisateurs LDAP obtiennent l'accès admin.

Les droits admin sont réévalués à chaque connexion : retirer quelqu'un du groupe le rétrograde à sa prochaine connexion.

### Login via l'API avec LDAP

```bash
# Specify auth_method: "ldap"
TOKEN=$(curl -s -X POST http://localhost:8081/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"jdoe","password":"ldap-password","auth_method":"ldap"}' | jq -r '.data.token')
```

## OIDC / authentification unique

Bootimus peut connecter les utilisateurs via n'importe quel fournisseur OpenID Connect — Authentik, Keycloak, Zitadel, Authelia, Entra ID, Google Workspace et d'autres. Les utilisateurs cliquent sur **Sign in with …** sur la page de login, s'authentifient auprès du fournisseur, puis sont renvoyés vers Bootimus déjà connectés. Les comptes sont créés automatiquement à la première connexion, et les droits admin suivent les groupes de l'utilisateur chez le fournisseur.

### Fonctionnement

1. L'utilisateur clique sur **Sign in with …** sur la page de login
2. Bootimus le redirige vers le fournisseur (flux authorization code avec PKCE)
3. L'utilisateur s'authentifie chez le fournisseur, qui le redirige vers `/api/oidc/callback`
4. Bootimus échange le code contre un ID token, vérifie sa signature, son audience, son expiration et son nonce, puis lit les claims du nom d'utilisateur et des groupes
5. Si des groupes ou des utilisateurs autorisés sont configurés, l'utilisateur doit correspondre à l'un d'eux (ou être administrateur)
6. Un enregistrement utilisateur local est créé ou mis à jour (voir [Utilisateurs LDAP et OIDC](#utilisateurs-ldap-et-oidc)), en tant qu'administrateur s'il appartient à un groupe admin ou figure dans la liste des utilisateurs admin, et en lecture seule sinon
7. Le navigateur est renvoyé vers le panneau d'administration avec un token JWT (comme pour l'auth locale)

Si l'ID token ne contient pas le claim du nom d'utilisateur ou des groupes, Bootimus interroge aussi l'endpoint userinfo du fournisseur.

### Configuration du fournisseur

Crée un client OIDC (parfois appelé application ou relying party) chez ton fournisseur avec :

| Paramètre | Valeur |
|---------|-------|
| Type de client | Confidentiel (avec un client secret) |
| Grant type | Authorization code |
| Redirect URI | `https://bootimus.example.com/api/oidc/callback` — l'URL par laquelle les utilisateurs accèdent au panneau d'administration, plus `/api/oidc/callback` |
| Scopes | `openid`, `profile`, `email` |

Assure-toi que le fournisseur inclut les groupes de l'utilisateur dans un claim `groups`. Bootimus ne devine pas sa propre URL publique : `BOOTIMUS_OIDC_REDIRECT_URL` doit donc correspondre exactement à la redirect URI enregistrée chez le fournisseur.

### Exemple Authentik

1. Dans Authentik, va dans **Applications → Providers** et crée un **OAuth2/OpenID Provider** :
   - **Name** : `bootimus`
   - **Authorisation flow** : ton flux de consentement explicite ou implicite habituel
   - **Client type** : Confidential
   - **Redirect URIs/Origins** : `https://bootimus.example.com/api/oidc/callback` (strict)
   - **Scopes** : garde les mappings par défaut `openid`, `email` et `profile` — le mapping `profile` d'Authentik inclut le claim `groups`
   - **Subject mode** : au choix ; Bootimus utilise `preferred_username` comme nom d'utilisateur par défaut
2. Note le **Client ID** et le **Client Secret**
3. Va dans **Applications → Applications** et crée une application avec le slug `bootimus` utilisant ce provider. Tu peux éventuellement lui associer une policy ou un groupe pour contrôler qui peut la lancer.
4. Crée des groupes pour les administrateurs de Bootimus (ex. `bootimus-admins`) et, éventuellement, pour les autres utilisateurs autorisés (ex. `bootimus-operators`)
5. Configure Bootimus :

```bash
export BOOTIMUS_OIDC_ISSUER=https://authentik.example.com/application/o/bootimus/
export BOOTIMUS_OIDC_CLIENT_ID=<client id>
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_ALLOWED_GROUPS=bootimus-operators
export BOOTIMUS_OIDC_DISPLAY_NAME=Authentik
```

L'URL de l'issuer est affichée sur la page de présentation du provider dans Authentik sous **OpenID Configuration Issuer**, et se termine toujours par `/application/o/<slug de l'application>/`.

### Exemple Keycloak

Crée un client OpenID Connect confidentiel avec **Standard flow** activé et la redirect URI ci-dessus. Keycloak n'envoie pas les groupes par défaut : ajoute un mapper **Group Membership** au scope dédié du client avec le nom de claim de token `groups` et **Full group path** désactivé.

```bash
export BOOTIMUS_OIDC_ISSUER=https://keycloak.example.com/realms/example
export BOOTIMUS_OIDC_CLIENT_ID=bootimus
export BOOTIMUS_OIDC_CLIENT_SECRET=<client secret>
export BOOTIMUS_OIDC_REDIRECT_URL=https://bootimus.example.com/api/oidc/callback
export BOOTIMUS_OIDC_ADMIN_GROUPS=bootimus-admins
export BOOTIMUS_OIDC_DISPLAY_NAME=Keycloak
```

### Exemple Microsoft Entra ID

Entra ID envoie des *ID* de groupes plutôt que des noms dans son claim de groupes ; le moyen le plus simple d'attribuer les rôles est donc d'utiliser les **app roles**, qu'Entra envoie dans un claim `roles`.

1. Dans le [Microsoft Entra admin centre](https://entra.microsoft.com), va dans **App registrations → New registration** :
   - **Name** : `Bootimus`
   - **Supported account types** : *Accounts in this organisational directory only (Single tenant)*
   - **Redirect URI** : plateforme **Web**, `https://bootimus.example.com/api/oidc/callback`
2. Note l'**Application (client) ID** et le **Directory (tenant) ID** depuis la page de présentation
3. Sous **Certificates & secrets**, crée un **New client secret** et copie sa **Value**
4. Sous **App roles**, crée deux rôles, chacun avec **Allowed member types** *Users/Groups* :
   - Display name `Bootimus Admin`, value `Bootimus.Admin`
   - Display name `Bootimus Read-only`, value `Bootimus.ReadOnly`
5. Va dans **Enterprise applications → Bootimus → Users and groups** et assigne des utilisateurs ou des groupes aux rôles. L'assignation de groupes requiert Entra ID P1 ou P2 ; avec l'offre gratuite, assigne les utilisateurs un par un.
6. Si tu le souhaites, sous **Enterprise applications → Bootimus → Properties**, passe **Assignment required?** sur **Yes** pour que seuls les utilisateurs assignés puissent se connecter
7. Configure Bootimus :

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

Garde le claim de nom d'utilisateur par défaut, `preferred_username`, qui est le nom de connexion de l'utilisateur (ex. `alice@example.com`). N'utilise pas `email` : Entra ne le vérifie pas, et Bootimus refuse les noms d'utilisateur de type email que le fournisseur n'a pas marqués comme vérifiés.

Si tu préfères les groupes de sécurité, ajoute un **groups claim** sous **Token configuration** et liste les **Object IDs** des groupes dans `BOOTIMUS_OIDC_ADMIN_GROUPS` et `BOOTIMUS_OIDC_ALLOWED_GROUPS`, en laissant `BOOTIMUS_OIDC_GROUPS_CLAIM` sur `groups`.

### Exemple Google

Google n'envoie ni l'appartenance aux groupes ni de claim `preferred_username` : avec Google, tu utilises donc les adresses email comme noms d'utilisateur et tu accordes l'accès avec des listes d'utilisateurs et de domaines plutôt que des groupes.

1. Dans la [Google Cloud console](https://console.cloud.google.com), sélectionne ou crée un projet et va dans **APIs & Services → OAuth consent screen** (appelé **Google Auth Platform** dans les versions plus récentes de la console) :
   - **User type** : *Internal* si tu utilises Google Workspace, pour que seuls les comptes de ton organisation puissent se connecter. *External* autorise n'importe quel compte Google : définis donc toujours `BOOTIMUS_OIDC_ALLOWED_USERS` dans ce cas.
   - Ajoute les scopes `openid`, `email` et `profile`
2. Va dans **APIs & Services → Credentials → Create credentials → OAuth client ID** :
   - **Application type** : Web application
   - **Authorised redirect URIs** : `https://bootimus.example.com/api/oidc/callback` (Google n'accepte que le HTTPS, hormis `http://localhost`)
3. Note le **Client ID** et le **Client secret**
4. Configure Bootimus :

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

Ici, Alice et Bob sont administrateurs et tous les autres utilisateurs ayant une adresse `@example.com` sont en lecture seule. Bootimus n'accepte que les noms d'utilisateur de type email que Google marque comme vérifiés.

### Mapping des accès

| Paramètre | Effet |
|---------|--------|
| `BOOTIMUS_OIDC_ADMIN_GROUPS` | Les membres de l'un de ces groupes sont administrateurs. |
| `BOOTIMUS_OIDC_ADMIN_USERS` | Ces utilisateurs sont administrateurs. Les entrées sont des noms d'utilisateur, ou `@domaine` pour correspondre à tous les noms d'utilisateur se terminant par ce domaine. |
| `BOOTIMUS_OIDC_ALLOWED_GROUPS` | Si défini, les membres de ces groupes peuvent se connecter en tant qu'utilisateurs en lecture seule. |
| `BOOTIMUS_OIDC_ALLOWED_USERS` | Si défini, ces utilisateurs (noms d'utilisateur ou entrées `@domaine`) peuvent se connecter en tant qu'utilisateurs en lecture seule. |

Si aucune des listes autorisées n'est définie, **tout utilisateur authentifié par le fournisseur peut se connecter avec un accès en lecture seule** — Bootimus journalise un avertissement au démarrage dans ce cas. Dès que l'une des listes autorisées est définie, seuls les utilisateurs qui correspondent à une liste autorisée ou admin peuvent se connecter. Si aucune des listes admin n'est définie, aucun utilisateur OIDC n'est administrateur.

Les quatre acceptent une liste séparée par des virgules, donc les noms de groupes peuvent contenir des espaces (`Bootimus Admins,Infrastructure`). La comparaison ignore la casse ; une entrée `@domaine` correspond uniquement à ce domaine exact, pas à ses sous-domaines. Les groupes et les listes sont revérifiés à chaque connexion : les changements s'appliquent à la prochaine connexion de l'utilisateur.

### Exemple Docker Compose

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

### Notes de sécurité

- Le state de connexion, le nonce et le verifier PKCE sont conservés dans un cookie `HttpOnly` signé et de courte durée (10 minutes), limité à `/api/oidc/`. Il est marqué `Secure` quand l'URL de redirection utilise HTTPS — sers le panneau d'administration en HTTPS en production.
- Seul le flux authorization code est utilisé ; les tokens du fournisseur n'atteignent jamais le navigateur. Le navigateur reçoit le JWT Bootimus habituel.
- La connexion locale par mot de passe continue de fonctionner à côté d'OIDC, donc le compte local `admin` reste disponible pour la récupération.

## Utilisateurs LDAP et OIDC

Les utilisateurs qui se connectent via LDAP ou OIDC obtiennent un enregistrement utilisateur local dès leur première connexion. Tu peux ainsi les voir dans **Users**, les désactiver, et faire vérifier leur accès à chaque requête comme pour les utilisateurs locaux.

- **Source** : chaque utilisateur est marqué `Local`, `LDAP` ou `OIDC`. Un utilisateur ne peut se connecter que via sa propre source : une connexion OIDC ou LDAP ne reprendra jamais un compte local du même nom (comme `admin`), et les utilisateurs LDAP et OIDC ne peuvent pas utiliser la connexion locale par mot de passe.
- **Les noms d'utilisateur** sont mis en minuscules et peuvent contenir des lettres, des chiffres et `. _ @ + -`. Les connexions avec d'autres caractères sont refusées.
- **Les droits admin** suivent les groupes de l'utilisateur (et, pour OIDC, la liste des utilisateurs admin) et sont rafraîchis à chaque connexion : modifier la case **Administrator** d'un utilisateur LDAP ou OIDC ne dure donc que jusqu'à sa prochaine connexion. Les utilisateurs qui ne sont pas administrateurs sont en lecture seule (voir [Rôles](#rôles)).
- **Désactiver** un utilisateur dans Bootimus le bloque immédiatement, même si le fournisseur l'autorise encore à se connecter.
- **Supprimer** un utilisateur ne retire que l'enregistrement local ; il est recréé à sa prochaine connexion. Utilise les groupes du fournisseur, ou désactive l'utilisateur, pour retirer l'accès.
- **Les mots de passe** des utilisateurs LDAP et OIDC sont gérés par le fournisseur : **Reset Password** n'est donc pas proposé pour eux et `bootimus user set-password` les refuse.

Si quelqu'un se connecte avec un nom qui appartient déjà à un utilisateur d'une autre source, la connexion est refusée avec *a user with this name already exists from a different source*. Cela inclut les comptes locaux créés avant que les utilisateurs LDAP ne soient provisionnés automatiquement. Supprime l'ancien compte local pour qu'il soit recréé depuis LDAP ou OIDC à la prochaine connexion.

## Référence de configuration

### Flags CLI

| Flag | Défaut | Description |
|------|---------|-------------|
| `--ldap-host` | *(vide)* | Hostname du serveur LDAP (active l'auth LDAP) |
| `--ldap-port` | `389` | Port du serveur LDAP |
| `--ldap-tls` | `false` | Utiliser LDAPS (TLS à la connexion) |
| `--ldap-starttls` | `false` | Utiliser StartTLS après la connexion |
| `--ldap-skip-verify` | `false` | Sauter la vérification du certificat TLS |
| `--ldap-bind-dn` | *(vide)* | DN du compte de service pour la recherche utilisateur |
| `--ldap-bind-password` | *(vide)* | Mot de passe du compte de service |
| `--ldap-base-dn` | *(vide)* | Base DN pour la recherche utilisateur |
| `--ldap-user-filter` | `(sAMAccountName=%s)` | Filtre de recherche utilisateur (`%s` = username) |
| `--ldap-group-filter` | *(vide)* | CN du groupe pour l'accès admin |
| `--ldap-group-base-dn` | *(vide)* | Base DN pour la recherche de groupe (par défaut le base DN) |
| `--oidc-issuer` | *(vide)* | URL de l'issuer OIDC (active la connexion OIDC) |
| `--oidc-client-id` | *(vide)* | Client ID OIDC |
| `--oidc-client-secret` | *(vide)* | Client secret OIDC |
| `--oidc-redirect-url` | *(vide)* | URL de redirection enregistrée chez le fournisseur, se terminant par `/api/oidc/callback` |
| `--oidc-scopes` | `openid,profile,email` | Scopes à demander, séparés par des virgules (`openid` est toujours inclus) |
| `--oidc-username-claim` | `preferred_username` | Claim utilisé comme nom d'utilisateur Bootimus (ex. `email`) |
| `--oidc-groups-claim` | `groups` | Claim listant les groupes de l'utilisateur |
| `--oidc-admin-groups` | *(vide)* | Groupes, séparés par des virgules, dont les membres sont administrateurs |
| `--oidc-allowed-groups` | *(vide)* | Groupes, séparés par des virgules, autorisés à se connecter |
| `--oidc-admin-users` | *(vide)* | Noms d'utilisateur ou entrées `@domaine`, séparés par des virgules, qui sont administrateurs |
| `--oidc-allowed-users` | *(vide)* | Noms d'utilisateur ou entrées `@domaine`, séparés par des virgules, autorisés à se connecter |
| `--oidc-display-name` | `SSO` | Nom affiché sur le bouton de connexion |
| `--oidc-skip-verify` | `false` | Sauter la vérification du certificat TLS lors des échanges avec le fournisseur |

### Variables d'environnement

Tous les flags peuvent être définis via des variables d'environnement avec le préfixe `BOOTIMUS_` :

| Variable | Correspond à |
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

### Fichier de config (bootimus.yaml)

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

## Dépannage

### Connexion LDAP échouée

Vérifie la connectivité et les paramètres TLS :
```bash
# Test LDAP connection
ldapsearch -H ldap://dc.example.com -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"

# Test LDAPS
ldapsearch -H ldaps://dc.example.com:636 -D "cn=svc-bootimus,dc=example,dc=com" -w password -b "dc=example,dc=com" "(sAMAccountName=testuser)"
```

### Utilisateur introuvable

Vérifie que le filtre utilisateur renvoie des résultats :
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" dn
```

Filtres courants :
- Active Directory : `(sAMAccountName=%s)`
- OpenLDAP : `(uid=%s)`
- Basé sur l'email : `(mail=%s)`

### L'utilisateur LDAP n'est pas admin

Vérifie l'appartenance au groupe :
```bash
ldapsearch -H ldap://dc.example.com -D "bind-dn" -w password \
  -b "dc=example,dc=com" "(sAMAccountName=testuser)" memberOf
```

### OIDC : « Unable to reach the identity provider »

Bootimus récupère `<issuer>/.well-known/openid-configuration` quand le premier utilisateur se connecte. Consulte les logs du serveur pour l'erreur exacte, et vérifie que l'hôte Bootimus peut joindre l'issuer :

```bash
curl https://authentik.example.com/application/o/bootimus/.well-known/openid-configuration
```

La valeur `issuer` de ce document doit correspondre exactement à `BOOTIMUS_OIDC_ISSUER`, y compris un éventuel slash final. Si le fournisseur utilise une CA privée, ajoute-la au magasin de confiance de l'hôte ; `BOOTIMUS_OIDC_SKIP_VERIFY=true` fonctionne pour tester mais n'est pas recommandé.

### OIDC : le fournisseur affiche une erreur de redirect URI

La redirect URI enregistrée chez le fournisseur et `BOOTIMUS_OIDC_REDIRECT_URL` doivent correspondre exactement — schéma, hôte, port et chemin.

### OIDC : « Sign-in expired or was started elsewhere »

La connexion doit se terminer dans les 10 minutes, dans le même navigateur que celui qui l'a démarrée, et Bootimus ne doit pas avoir redémarré entre-temps. Si ça se répète derrière un reverse proxy, vérifie que le proxy laisse passer les cookies et que les utilisateurs accèdent à Bootimus sur le même hôte que l'URL de redirection.

### OIDC : l'utilisateur n'est pas administrateur ou est refusé

Les logs du serveur indiquent pourquoi chaque connexion a été refusée. Vérifie quels groupes le fournisseur envoie réellement — les noms de groupes doivent correspondre à `BOOTIMUS_OIDC_ADMIN_GROUPS` ou `BOOTIMUS_OIDC_ALLOWED_GROUPS`, et Keycloak envoie des chemins complets (`/bootimus-admins`) sauf si **Full group path** est désactivé. Si ton fournisseur utilise un autre nom de claim, définis `BOOTIMUS_OIDC_GROUPS_CLAIM` (les app roles d'Entra ID utilisent `roles`). Pour les fournisseurs sans groupes, comme Google, utilise `BOOTIMUS_OIDC_ADMIN_USERS` et `BOOTIMUS_OIDC_ALLOWED_USERS`.

### OIDC : « email … is not marked as verified by the provider »

Quand `BOOTIMUS_OIDC_USERNAME_CLAIM` vaut `email`, le fournisseur doit envoyer `email_verified: true` ; sinon, quiconque peut définir une adresse non vérifiée pourrait se connecter à la place de quelqu'un d'autre. Vérifie l'adresse chez le fournisseur, ou utilise un autre claim de nom d'utilisateur comme `preferred_username`.

### « A user with this name already exists from a different source »

Voir [Utilisateurs LDAP et OIDC](#utilisateurs-ldap-et-oidc). Supprime le compte existant dans **Users** et reconnecte-toi.

### Token expiré

Les tokens JWT sont valables 24 heures. Après expiration, la page de login s'affiche automatiquement. Les tokens sont aussi invalidés au redémarrage du serveur (le secret de signature est regénéré).

### Admin local verrouillé

Réinitialise le mot de passe admin :
```bash
./bootimus serve --reset-admin-password
```

Ou, si démarrer le serveur est peu pratique (ex. conflits de ports), définir le
mot de passe directement dans la base de données :
```bash
./bootimus user set-password admin
```

Ça marche toujours, quelle que soit la config LDAP ou OIDC.
