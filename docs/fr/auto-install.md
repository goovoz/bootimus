# Guide d'installation automatique

Lance des installations sans surveillance sur Windows, Ubuntu, Debian, les distros de la famille Red Hat, openSUSE, Fedora CoreOS, Flatcar et Proxmox VE. Dépose un fichier de config une fois, attache-le à une image, et chaque boot PXE termine l'installation sans une seule frappe au clavier.

## Table des matières

- [Vue d'ensemble](#vue-densemble)
- [Formats supportés](#formats-supportés)
- [La bibliothèque de fichiers](#la-bibliothèque-de-fichiers)
- [Attacher des fichiers](#attacher-des-fichiers)
- [Ordre de résolution](#ordre-de-résolution)
- [Placeholders](#placeholders)
- [Variables](#variables)
- [Aperçu](#aperçu)
- [Validation](#validation)
- [Exemples](#exemples)
- [Non supporté](#non-supporté)
- [Signaler la fin de l'installation](#signaler-la-fin-de-linstallation)
- [Notes Windows](#notes-windows)
- [API REST](#api-rest)
- [Dépannage](#dépannage)

## Vue d'ensemble

Les configs d'auto-installation sont stockées dans une bibliothèque par distro sous `data/autoinstall/`. Tu peux :

- **Gérer les fichiers dans l'UI** sous l'onglet **Auto-Install** — créer, éditer, uploader, télécharger et supprimer des configs sans toucher au filesystem.
- **Définir un défaut** par image (chaque client bootant cette image obtient la même config).
- **Surcharger par client** ou **par groupe de clients** quand une machine a besoin d'une config différente (hostname différent, layout disque, rôle, etc.).
- **Templatiser** avec des placeholders comme `{{HOSTNAME}}` et `{{IP}}` résolus au moment du service en utilisant l'identité du client qui boote, plus tes propres variables `{{VAR.name}}` définies par client ou par groupe.
- **Prévisualiser** le résultat rendu pour n'importe quel client et image, et faire vérifier la bonne formation des fichiers YAML, XML et JSON à l'enregistrement.

Bootimus sert le fichier rendu en HTTP sur l'endpoint auto-install. Les installeurs Linux le récupèrent via un paramètre kernel ; pour Windows, wimboot télécharge la même URL comme `initrd` supplémentaire et l'injecte dans le RAM disk WinPE sous le nom `AutoUnattend.xml`, pour que `setup.exe /unattend:` le récupère automatiquement (voir [Notes Windows](#notes-windows)).

## Formats supportés

| Famille de distro | Format | Extension de fichier | Détecté comme |
|--------------|--------|----------------|-------------|
| Windows (10/11/Server) | `autounattend.xml` | `.xml` | `autounattend` |
| Ubuntu (Server live, 20.04+) | cloud-init / autoinstall | `.yaml`, `.yml` | `autoinstall` |
| Debian | preseed | `.cfg` | `preseed` |
| Red Hat / Rocky / Fedora / Alma | kickstart | `.ks` | `kickstart` |
| openSUSE Leap 15 / SLES 15 | profil AutoYaST | `.xml` sous `opensuse/` | `autoyast` |
| openSUSE Leap 16 / Tumbleweed / SLES 16 | profil Agama | `.json`, `.jsonnet` sous `opensuse/` | `agama` |
| Fedora CoreOS / Flatcar | config Ignition | `.ign` | `ignition` |
| Proxmox VE | fichier de réponse | `.toml` sous `proxmox/` | `proxmox` |
| Tout le reste | brut | n'importe quelle | `generic` |

Le type est dérivé de l'extension du fichier et du répertoire de distro dans lequel il vit : un fichier `.xml` est donc un document autounattend sous `windows/` mais un profil AutoYaST sous `opensuse/`. Le type détermine le label affiché dans l'UI, l'en-tête `Content-Type` quand le fichier est servi, et le paramètre kernel que le menu de boot ajoute :

| Type | Paramètre kernel ajouté par le menu de boot | `Content-Type` |
|------|------------------------------------------|----------------|
| `preseed` | `auto=true priority=critical url=<url>` | `text/plain` |
| `kickstart` | `inst.ks=<url>` | `text/plain` |
| `autoinstall` | `autoinstall ds=nocloud-net;s=<seed-url>/` | `text/yaml` |
| `autounattend` | aucun (wimboot le récupère comme `initrd` supplémentaire, voir [Notes Windows](#notes-windows)) | `application/xml` |
| `autoyast` | `autoyast=<url>` | `application/xml` |
| `agama` | `inst.auto=<url>` | `application/json` |
| `ignition` | `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` (Fedora CoreOS) ou `flatcar.first_boot=1 ignition.config.url=<url>` (Flatcar) | `application/json` |
| `proxmox` | aucun (l'installeur récupère lui-même le fichier de réponse, voir [Proxmox VE](#proxmox-ve-fichier-de-réponse)) | `application/toml` |
| `generic` | aucun | `text/plain` |

`<url>` vaut `http://<server>:8080/autoinstall/<image-filename>?mac=<mac>`. Un fichier `generic` est servi sur demande mais aucun installeur n'en est informé ; choisis l'un des formats spécifiques si tu veux que le menu de boot le passe à l'installeur.

## La bibliothèque de fichiers

Tous les fichiers d'auto-install vivent sous `data/autoinstall/<distro>/<filename>` :

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

Le segment `<distro>` doit correspondre à un ID de profil de distro connu (voir [Profils de distro](distro-profiles.md)). Le répertoire est créé automatiquement au premier démarrage.

### Ajouter des fichiers via l'UI

**Auto-Install** → **New File** ouvre l'éditeur avec un sélecteur de distro, un champ nom de fichier et un textarea syntax-friendly. **Upload File** prend n'importe quel fichier local et le dépose dans le dossier distro choisi.

### Ajouter des fichiers manuellement

Dépose-les juste :

```bash
mkdir -p data/autoinstall/ubuntu
cp my-autoinstall.yaml data/autoinstall/ubuntu/default.yaml
```

Ils apparaissent immédiatement dans l'UI — pas de redémarrage, pas de scan.

## Attacher des fichiers

Les fichiers d'auto-install n'ont aucun effet tant que tu ne les as pas branchés. Il y a trois endroits où en attacher un :

### Image (par défaut)

Onglet **Images** → ouvre les **Properties** d'une image → section **Auto-Install** → choisis un fichier. Chaque client qui boote cette image récupère cette config sauf si quelque chose de plus spécifique la surcharge.

### Client (override par machine)

Onglet **Clients** → ouvre un client → menu déroulant **Auto-Install File**. Utilise ça quand une machine spécifique a besoin d'une config différente (par ex. un serveur de build vs le reste de la flotte de bureau).

### Groupe de clients (override par flotte)

Onglet **Groups** → ouvre un groupe → **Auto-Install File**. S'applique à chaque client du groupe. Utile pour les scénarios « tous les postes de travail du lab 3 ».

## Ordre de résolution

Quand un client demande son fichier d'auto-install, Bootimus parcourt cette hiérarchie :

```
1. Per-client override        (Client.AutoInstallFile)
2. Per-group override         (ClientGroup.AutoInstallFile, if client is in a group)
3. Image default              (Image.AutoInstallFile)
4. Inline legacy script       (Image.AutoInstallScript — pre-0.1.58 setups)
5. → 404 (no auto-install configured)
```

La première correspondance non vide gagne. L'endpoint log la source depuis laquelle il a servi :

```
Served auto-install script for ubuntu-24.04-live-server-amd64.iso \
  (source: client:b4:2e:99:01:5f:a3, type: autoinstall, size: 1247 bytes)
```

## Placeholders

Ces tokens sont substitués par client au moment du service :

| Token | Remplacé par |
|-------|---------------|
| `{{MAC}}` | Adresse MAC du client (minuscules, séparée par des deux-points) ; pour Proxmox VE, la MAC de la première interface de la requête de l'installeur qui correspond à un client connu, sinon la première interface listée |
| `{{CLIENT_NAME}}` | Nom convivial depuis la table Clients |
| `{{HOSTNAME}}` | Identique à `{{CLIENT_NAME}}` (alias pour plus de clarté dans les configs) |
| `{{CLIENT_GROUP}}` | Nom du groupe du client, ou vide s'il n'en a pas |
| `{{IP}}` | IP du client qui a émis la requête |
| `{{SERVER_ADDR}}` | Adresse du serveur Bootimus |
| `{{SERVER_PORT}}` | Port HTTP de boot de Bootimus |
| `{{SERVER_URL}}` | `http://<SERVER_ADDR>:<SERVER_PORT>`, la même URL de base que celle du menu de boot |
| `{{IMAGE_NAME}}` | Nom d'affichage de l'image qui boote |
| `{{IMAGE_FILENAME}}` | Nom de fichier ISO de l'image qui boote |
| `{{IMAGE_URL}}` | URL de l'ISO telle que servie sous `/isos/`, la même valeur que le menu de boot passe en `iso-url=` |
| `{{CALLBACK_URL}}` | URL complète que l'installeur appelle pour signaler la fin, avec un token à usage unique (voir [Signaler la fin de l'installation](#signaler-la-fin-de-linstallation)) |
| `{{INSTALL_TOKEN}}` | Le token brut derrière `{{CALLBACK_URL}}`, pour construire ta propre requête |
| `{{VAR.<name>}}` | La variable de client ou de groupe nommée `<name>` (voir [Variables](#variables)) |

Les placeholders sont de la substitution de chaîne brute — pas d'échappement. Quote-les correctement pour le format cible (XML, YAML, etc.). Un placeholder sans valeur est laissé exactement tel qu'écrit et journalisé comme non résolu au moment du service ; utilise l'[Aperçu](#aperçu) pour les repérer avant qu'une machine ne boote.

## Variables

Les placeholders intégrés couvrent l'identité du client, mais la plupart des flottes ont aussi besoin de valeurs que Bootimus ne peut pas connaître : un rôle, un numéro de rack, un périphérique disque, une URL de miroir. Les variables comblent ce manque. Chaque client et chaque groupe de clients porte un ensemble de valeurs chaîne nommées, et chacune d'elles est disponible dans les fichiers d'auto-installation sous la forme `{{VAR.<name>}}`.

- Les noms n'utilisent que des lettres, des chiffres et des tirets bas (`role`, `rack_id`, `DISK`) ; le serveur rejette tout le reste à l'enregistrement du client ou du groupe.
- **Les variables de groupe sont des valeurs par défaut** pour chaque membre. **Les variables de client les surchargent** nom par nom : un groupe peut définir `role: worker` et une seule machine `role: control-plane` tout en héritant toujours du `rack` du groupe.
- Un token `{{VAR.<name>}}` sans valeur à aucun des deux niveaux reste non résolu.

Définis-les dans l'UI sous **Clients** → ouvrir un client → **Variables d'installation automatique**, ou **Groupes** → ouvrir un groupe → **Variables d'installation automatique** : chaque ligne est un nom et une valeur, avec **Ajouter une variable** et un bouton de suppression par ligne. Via l'API, `variables` est un objet de chaînes aussi bien dans le payload du client que dans celui du groupe :

```bash
# Valeurs par défaut du groupe
curl -u admin:pw -X PUT "http://localhost:8081/api/client-groups/update?id=3" \
  -H "Content-Type: application/json" \
  -d '{"name":"lab-3","variables":{"role":"worker","rack":"3","disk":"/dev/sda"}}'

# Une machine surcharge une seule valeur
curl -u admin:pw -X PUT "http://localhost:8081/api/clients?mac=b4:2e:99:01:5f:a3" \
  -H "Content-Type: application/json" \
  -d '{"variables":{"role":"control-plane"}}'
```

Envoyer `"variables": null` dans une mise à jour de client les efface ; omettre la clé les laisse inchangées. `GET /api/clients` et `GET /api/client-groups` les renvoient sous la même clé.

Un kickstart qui utilise les deux niveaux :

```
network --bootproto=dhcp --hostname={{HOSTNAME}}
ignoredisk --only-use={{VAR.disk}}
%post
echo "{{VAR.role}} in rack {{VAR.rack}} ({{CLIENT_GROUP}})" > /etc/machine-role
%end
```

L'import CSV sous **Clients** ne transporte pas les variables ; les variables existantes sont conservées quand une ligne CSV met à jour un client.

## Aperçu

L'éditeur sous **Auto-Install** a une action **Aperçu** pour tout fichier enregistré. Choisis un client et, en option, une image, et le panneau affiche le texte exact que Bootimus servirait à cette machine, en lecture seule, avec un avertissement listant chaque placeholder resté non résolu. Sans client, les valeurs de client et de groupe sont rendues vides ; sans image, les placeholders `IMAGE_*` restent non résolus. `{{CALLBACK_URL}}` et `{{INSTALL_TOKEN}}` sont rendus avec le token fixe `preview`, car aucun enregistrement d'installation n'est créé.

Le même rendu est disponible via l'API :

```bash
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"
```

```json
{"success":true,"data":{"content":"#cloud-config\n...","unresolved":["VAR.disk"]}}
```

`mac` et `image` sont optionnels. Le rendu n'écrit jamais rien : il ne crée pas d'enregistrement d'installation et ne fait pas avancer une installation dans ses états.

## Validation

Quand un fichier est enregistré depuis l'éditeur ou uploadé, Bootimus vérifie sa bonne formation selon l'extension avant de l'écrire :

| Extension | Vérification |
|-----------|-------|
| `.yaml`, `.yml` | Se parse en YAML (les fichiers multi-documents sont acceptés) |
| `.xml` | XML bien formé |
| `.json` | JSON valide |
| tout le reste | Non vérifié |

Chaque placeholder est d'abord remplacé par une valeur factice inoffensive, de sorte que `hostname: {{HOSTNAME}}` passe même si un `{{HOSTNAME}}` nu n'est pas du YAML valide en soi. Un échec répond `400` avec le message du parseur et, quand le parseur le fournit, le numéro de ligne ; l'éditeur l'affiche inline sous le contenu et propose **Enregistrer quand même** pour les cas où tu en sais plus que le parseur (par exemple un fichier qui est délibérément un fragment). Via l'API, passe `"force": true` dans le body d'enregistrement ou `force=true` comme champ multipart à l'upload pour sauter la vérification.

La substitution se fait sur l'endpoint auto-install, elle s'applique donc à tous les formats. L'`autounattend.xml` Windows est lui aussi rendu pour le client qui boote, parce que wimboot le télécharge depuis cet endpoint au boot (voir [Notes Windows](#notes-windows)) ; seule la copie de secours SMB non rendue décrite là-bas la contourne.

## Exemples

### Ubuntu Server (cloud-init)

`data/autoinstall/ubuntu/default.yaml` :

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

Bootimus ajoute lui-même le paramètre de boot dès qu'un fichier auto-install est attaché, il n'y a donc rien à ajouter aux boot params de l'image. Le paramètre généré ressemble à ceci :

```
autoinstall ds=nocloud-net;s=http://<server>:8080/autoinstall/<image-filename>/mac/<mac>/
```

### Debian (preseed)

`data/autoinstall/debian/server.cfg` :

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

`data/autoinstall/rocky/workstation.ks` :

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

`data/autoinstall/windows/kiosk.xml` : document `<unattend>` standard — voir la [référence autounattend de Microsoft](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/). Les placeholders fonctionnent dans n'importe quel nœud texte quand le client boote via wimboot, ce qui est la façon dont Bootimus boote les images Windows :

```xml
<ComputerName>{{HOSTNAME}}</ComputerName>
```

### openSUSE Leap 15 / SLES 15 (AutoYaST)

`data/autoinstall/opensuse/leap15.xml` : un profil AutoYaST standard — voir le [guide AutoYaST](https://doc.opensuse.org/projects/autoyast/). Bootimus ajoute `autoyast=<url>` à la ligne kernel. Le profil openSUSE passe déjà `install=` pointant vers l'ISO extrait, ce qui monte le réseau et fournit le dépôt d'installation.

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

`data/autoinstall/opensuse/leap16.jsonnet` (ou `.json`) : un [profil Agama](https://agama-project.github.io/docs/user/profile). Bootimus ajoute `inst.auto=<url>` ; Agama n'a besoin de rien d'autre sur la ligne de commande pour une installation sans surveillance.

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

Agama ne comprend que `inst.auto` ; si ton média Leap 16 ou Tumbleweed boote Agama et que tu attaches un fichier `.xml`, Bootimus passera `autoyast=` et l'installeur l'ignorera. Garde les profils AutoYaST pour les médias Leap 15 et SLES 15.

### Fedora CoreOS / Flatcar (Ignition)

`data/autoinstall/coreos/worker.ign` ou `data/autoinstall/flatcar/worker.ign` : un document JSON [Ignition](https://coreos.github.io/ignition/), généralement généré depuis Butane. Bootimus ajoute `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` pour Fedora CoreOS et `flatcar.first_boot=1 ignition.config.url=<url>` pour Flatcar. Le profil `coreos` passe aussi `coreos.live.rootfs_url=` pointant vers le `rootfs.img` extrait de l'ISO live.

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

Ceci boote le système live avec ta config appliquée. Pour installer sur disque, ajoute `coreos.inst.install_dev=/dev/sda coreos.inst.ignition_url={{BASE_URL}}/autoinstall/{{FILENAME}}?mac={{MAC}}` aux paramètres de boot de l'image pour que `coreos-installer` écrive la même config sur le système installé. Ignition applique quand même la config à l'environnement live d'abord, ce qui convient aux configs qui créent des utilisateurs, des clés et des fichiers ; une config qui partitionne ou formate des disques échouera dans le système live et ne devrait être livrée que via `coreos.inst.ignition_url`.

### Proxmox VE (fichier de réponse)

Proxmox VE s'installe depuis un `answer.toml` ([Automated Installation](https://pve.proxmox.com/wiki/Automated_Installation)). L'installeur d'origine ne lit pas d'URL de fichier de réponse sur la ligne de commande kernel, l'ISO doit donc être préparé une fois avec `proxmox-auto-install-assistant` pour récupérer la réponse depuis Bootimus en HTTP :

1. Sur n'importe quel hôte Debian ou Proxmox, installe `proxmox-auto-install-assistant` et remasterise l'ISO, en pointant l'URL de récupération vers l'endpoint auto-install de Bootimus pour le nom de fichier que tu es sur le point d'uploader :

   ```bash
   proxmox-auto-install-assistant prepare-iso proxmox-ve_9.1-1.iso \
     --fetch-from http \
     --url "http://<server>:8080/autoinstall/proxmox-ve_9.1-1-auto-from-http.iso"
   ```

   L'assistant écrit `proxmox-ve_9.1-1-auto-from-http.iso` à côté de la source. L'URL doit contenir le nom de fichier de l'ISO que tu uploades, fais donc correspondre les deux.

2. Uploade l'ISO remasterisé via **Images**. Le profil `proxmox` extrait `/boot/linux26` et `/boot/initrd.img`, et le menu de boot charge l'ISO lui-même comme second initrd (`proxmox.iso`), ce qui est la façon dont le flux iPXE officiel fournit le média d'installation.

3. Crée `data/autoinstall/proxmox/answer.toml` et attache-le à l'image, à un client ou à un groupe :

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

Au boot, l'installeur fait un `POST` d'une description JSON de la machine (produit, release de l'ISO, données DMI et `network_interfaces` avec chaque `link` et `mac`) vers l'URL. Bootimus lit les MAC des interfaces, résout le client par la première qui correspond à un client connu (en se rabattant sinon sur le défaut de l'image), rend le `.toml` attaché et le renvoie en `application/toml`. `{{MAC}}` est la MAC qui a correspondu. La section `[post-installation-webhook]` fait appeler `{{CALLBACK_URL}}` par l'installeur une fois l'installation terminée, de sorte que l'enregistrement d'installation du client passe à `completed`.

Rien n'est ajouté à la ligne de commande kernel pour ce type. Un paramètre kernel `proxmox-answer=<url>` a été demandé en amont ([bug Proxmox 8219](https://bugzilla.proxmox.com/show_bug.cgi?id=8219)) mais n'a pas été livré ; quand ce sera le cas, l'étape de remasterisation deviendra inutile.

## Non supporté

Certaines distributions n'ont aucun moyen de récupérer un fichier d'installation sans surveillance depuis la ligne de commande kernel, Bootimus ne prétend donc pas les supporter. Les fichiers pour ces distros sont quand même servis en `generic`, mais rien n'en informe l'installeur :

- **Alpine Linux** — `setup-alpine -f <answerfile>` est une commande que tu lances après le boot ; l'installeur n'a aucun hook en paramètre kernel pour en récupérer un.
- **NixOS** — l'installation est un `nixos-install` piloté par une configuration que tu écris sur la cible ; l'ISO live n'a pas d'installeur sans surveillance.
- **FreeBSD** — les scripts `bsdinstall` sont lus depuis le média local ou `BSDINSTALL_SCRIPT` dans l'environnement, pas depuis une URL sur la ligne de commande du loader.
- **Arch Linux** — l'ISO live n'a pas d'installeur ; `archinstall` peut prendre un fichier de config, mais il faut le récupérer et le lancer à la main ou depuis un profil custom.

Pour celles-ci, boote l'image live et lance l'installeur depuis une session SSH ou un script custom à la place.

## Signaler la fin de l'installation

Bootimus suit chaque installation automatique dès l'instant où le client charge le kernel de l'image. L'enregistrement passe par quatre états :

| État | Défini quand |
|------|--------------|
| `booted` | Le client a récupéré les fichiers de boot de l'image et l'image a un fichier d'auto-installation ou un script inline |
| `installing` | L'installeur a récupéré le fichier d'auto-installation |
| `completed` | Le système installé a appelé `{{CALLBACK_URL}}` |
| `failed` | Le callback a signalé `status=failed`, ou rien n'a été signalé dans le délai imparti |

L'état courant s'affiche dans la colonne **Install** de la table Clients et l'historique complet se trouve sous **Install History** dans l'éditeur de client. Les installations qui ne signalent jamais rien sont marquées en échec après six heures ; modifie ce délai avec `--install-timeout-hours` ou `install_timeout_hours` dans `bootimus.yaml`. Chaque passage à `completed` ou `failed` déclenche un événement webhook `install.completed` ou `install.failed` quand cet événement est activé dans **Settings → Webhook**.

Le callback est une simple requête HTTP, `GET` ou `POST`, vers le port de boot :

```
http://<server>:8080/install/callback?token=<token>&status=completed|failed&message=<optional text>
```

`{{CALLBACK_URL}}` contient déjà le token, il suffit donc à un script d'ajouter `&status=failed&message=...`. Seul le premier signalement pour un token compte ; les appels suivants répondent `already recorded`.

### Kickstart (`%post`)

```
%post --log=/root/ks-post.log
curl -fsS "{{CALLBACK_URL}}" || curl -fsS "{{CALLBACK_URL}}&status=failed&message=post-script"
%end
```

### Preseed (`late_command`)

```
d-i preseed/late_command string in-target sh -c 'wget -qO- "{{CALLBACK_URL}}" || curl -fsS "{{CALLBACK_URL}}"'
```

### Ubuntu autoinstall / cloud-init

Le module `phone_home` envoie la requête après le premier boot, ce qui est le signal le plus fiable :

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

Ou signale depuis l'environnement de l'installeur avec `late-commands` :

```yaml
  late-commands:
    - curtin in-target --target=/target -- curl -fsS "{{CALLBACK_URL}}"
```

### Windows (`autounattend.xml`)

Ajoute une commande de première ouverture de session sous `Microsoft-Windows-Shell-Setup` :

```xml
<FirstLogonCommands>
  <SynchronousCommand wcm:action="add">
    <Order>1</Order>
    <CommandLine>powershell -NoProfile -Command "Invoke-WebRequest -UseBasicParsing '{{CALLBACK_URL}}'"</CommandLine>
    <Description>Report install to Bootimus</Description>
  </SynchronousCommand>
</FirstLogonCommands>
```

N'oublie pas que `&` dans les attributs ou le texte XML doit s'écrire `&amp;` si tu ajoutes des paramètres `status` ou `message`.

### Nœuds Talos et Flatcar

Les nœuds Kubernetes enrôlés via l'onglet **Kubernetes** signalent déjà leur état via le flux de machine config et les événements `node.install_started` / `node.install_completed`. Ils n'ont pas besoin de callback.

### Consulter l'historique

```bash
# History for one client
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/installs?mac=b4:2e:99:01:5f:a3"

# Recent installs across the fleet
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/installs?limit=50"
```

## Notes Windows

Les images Windows bootent via [wimboot](https://ipxe.org/wimboot), qui injecte chaque fichier `initrd` supplémentaire du script iPXE dans le RAM disk WinPE. Bootimus s'en sert pour livrer le fichier d'auto-install par client :

1. Quand le client qui boote résout vers un fichier autounattend (via l'[ordre de résolution](#ordre-de-résolution) habituel : client, puis groupe, puis image), la section de boot iPXE générée ajoute `initrd http://<server>:<port>/autoinstall/<iso>?mac=<mac> AutoUnattend.xml` avant `boot.wim`.
2. L'endpoint auto-install rend le fichier avec les placeholders du client, exactement comme pour les installeurs Linux.
3. wimboot place le fichier dans `X:\Windows\System32\AutoUnattend.xml` à l'intérieur de WinPE.
4. Le `startnet.cmd` patché le trouve là et lance `setup.exe /unattend:X:\Windows\System32\AutoUnattend.xml`.

Comme le fichier est résolu et rendu par client, les overrides par client et par groupe et les placeholders comme `{{HOSTNAME}}` fonctionnent sous Windows comme partout ailleurs. Les paramètres de boot sont toujours transmis à wimboot ; évite `rawwim`, qui désactive l'injection de fichiers.

**Secours SMB.** Quand `boot.wim` est patché pour une image qui a un fichier autounattend attaché au niveau image, Bootimus stage aussi ce fichier, non rendu, sous le nom `AutoUnattend.xml` sur le partage SMB de l'image. `startnet.cmd` ne le copie dans `X:\AutoUnattend.xml` que si wimboot n'a rien injecté, par exemple quand le client a booté le `boot.wim` patché par un autre chemin que le menu iPXE de Bootimus. Ce secours n'a ni substitution de placeholders ni overrides par client ou par groupe : les placeholders qu'il contient arrivent à Setup tels quels.

Sans fichier autounattend à aucun niveau, Setup tourne en interactif comme avant.

**Prérequis du répertoire de données.** Bootimus lance son propre `smbd` dont l'état vit sous `data/smb`. Samba refuse de démarrer si ce répertoire est sur un système de fichiers incapable de stocker les permissions Unix (exFAT, NTFS ou un montage CIFS), car son répertoire de verrous est alors lu comme `0777` au lieu de `0755`. Garde le répertoire de données sur un système de fichiers Linux natif comme ext4 ou XFS, et vérifie qu'aucun autre serveur SMB sur l'hôte n'écoute sur le port 445. Bootimus vérifie ces deux conditions au démarrage et les signale sous **Server Info** → **Windows SMB**.

**Résilience au redémarrage.** WinPE redémarre en plein milieu de l'installation et se reconnecte depuis la même IP client. La config Samba embarquée définit `reset on zero vc = yes` et désactive les oplocks pour que le second `net use` ne se vautre pas sur un état de session périmé. Si tu as remplacé `data/smb/smb.conf` par le tien, miroite ces réglages.

**Annuler le patch.** Le premier patch SMB conserve une copie intacte de `boot.wim` sous le nom `boot.wim.orig` à côté. **Images** → **Retirer le patch SMB** restaure l'original et supprime le partage SMB de l'image — l'ISO boote alors exactement comme à l'upload. Les images patchées par d'anciennes versions de Bootimus n'ont pas de sauvegarde : ré-extrais d'abord l'image (ce qui produit une copie intacte fraîche), puis retire le patch.

## API REST

Tout ce qu'il y a dans l'UI est aussi un appel REST.

```bash
# List all auto-install files
curl -u admin:pw http://localhost:8081/api/autoinstall-files

# Read a file
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/get?distro=ubuntu&filename=default.yaml"

# Créer ou écraser un fichier (répond 400 si le YAML, XML ou JSON est mal formé ; ajoute "force":true pour enregistrer quand même)
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/save \
  -H "Content-Type: application/json" \
  -d '{"distro":"ubuntu","filename":"default.yaml","content":"#cloud-config\n..."}'

# Rendre un fichier pour un client et une image, en listant les placeholders non résolus
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

Attacher un fichier à une image :

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/images/update \
  -H "Content-Type: application/json" \
  -d '{"filename":"ubuntu-24.04-live-server-amd64.iso","auto_install_file":"ubuntu/default.yaml"}'
```

Attacher un fichier à un client :

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/clients/b4:2e:99:01:5f:a3 \
  -H "Content-Type: application/json" \
  -d '{"auto_install_file":"ubuntu/lab-bench.yaml"}'
```

L'endpoint auto-install que les clients tapent au boot :

```
GET  /autoinstall/<image-filename>?mac=<mac>             # preseed, kickstart, autoyast, agama, ignition
GET  /autoinstall/<image-filename>/mac/<mac>/user-data   # Ubuntu autoinstall
GET  /autoinstall/<image-filename>/mac/<mac>/meta-data   # Ubuntu autoinstall
POST /autoinstall/<image-filename>                       # Proxmox VE (JSON system information in the body)
```

Le menu de boot construit ces URLs avec l'adresse MAC du client pour que les overrides par client se résolvent correctement ; pour Ubuntu, cloud-init ajoute lui-même `user-data` et `meta-data`. L'installeur Proxmox ne connaît pas la MAC avec laquelle Bootimus l'a booté, il fait donc un `POST` de ses interfaces réseau et Bootimus les compare à la table Clients. Les requêtes `GET` sans adresse MAC, ou sous une autre forme comme `/autoinstall/<image-filename>/user-data`, renvoient une 404.

## Dépannage

### 404 depuis `/autoinstall/...`

`no auto-install configuration for this image/client` — rien n'est attaché à aucun niveau de la chaîne de résolution. Soit attache un fichier à l'image, au client ou à son groupe, soit vérifie que `auto_install_file` pointe bien vers un fichier qui existe sous `data/autoinstall/`.

### Placeholders rendus littéralement

`{{HOSTNAME}}` qui apparaît comme une chaîne littérale dans le système installé signifie que le fichier a été servi avant que la substitution ne tourne — généralement parce que le client a booté uniquement par IP et que la requête ne contenait pas de query param `mac`. Confirme que le menu de boot génère des URLs de la forme `/autoinstall/<iso>?mac=<mac>` ou `/autoinstall/<iso>/mac/<mac>/`.

Sous Windows, des placeholders littéraux signifient que Setup a utilisé la copie non rendue stagée sur le partage SMB plutôt que le fichier injecté par wimboot. Prévisualise le menu de boot du client (voir [Prévisualiser le menu de boot d'un client](clients.md#previewing-a-clients-boot-menu)) et vérifie que la section Windows contient une ligne `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml`, et que les paramètres de boot de l'image n'incluent pas `rawwim`.

Un `{{VAR.<name>}}` resté littéral signifie que ni le client ni son groupe ne définit cette variable, ou que le nom est orthographié différemment (les noms sont sensibles à la casse). Chaque placeholder non résolu est journalisé au moment du service, et l'**Aperçu** de l'éditeur les liste pour un client choisi avant que quoi que ce soit ne boote.

### Mauvais fichier servi

La résolution se fait du plus spécifique au moins spécifique. Si un client a son propre override et que tu ne t'y attends pas, c'est pour ça que le défaut au niveau image n'est pas utilisé. Vérifie la ligne de log serveur :

```
Served auto-install script for ... (source: client:..., type: ..., size: ...)
```

Le champ `source:` te dit exactement quel slot a gagné.

### L'installeur Proxmox dit qu'il ne peut pas récupérer le fichier de réponse

L'ISO remasterisé récupère depuis l'URL exacte gravée par `prepare-iso`. Vérifie que le nom de fichier dans cette URL correspond à l'ISO tel qu'uploadé (renommer l'ISO après remasterisation casse tout), que le client peut atteindre le port 8080, et qu'un fichier `.toml` est attaché à un niveau quelconque de la chaîne de résolution. Le log serveur montre le `POST` et la source résolue ; un `400` signifie que le corps ne contenait pas de `network_interfaces`, ce qui pointe vers un installeur très ancien.

### Windows Setup tourne en interactif

- Un fichier autounattend doit se résoudre pour le client qui boote : attache-en un à l'image (image properties → Auto-Install), au client ou à son groupe.
- Prévisualise le menu de boot du client et confirme que la section Windows contient la ligne `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml`. Si elle manque, aucun fichier autounattend ne s'est résolu pour cette MAC.
- Assure-toi que les paramètres de boot de l'image n'incluent pas `rawwim` ; il empêche wimboot d'injecter des fichiers dans WinPE.
- Re-patche `boot.wim` après une mise à jour de Bootimus pour que `startnet.cmd` sache chercher le fichier injecté : **Images** → **Patch SMB** (ou ça se re-patche automatiquement au prochain boot).
- Confirme que le partage SMB est accessible depuis le client (`net view \\<server>` depuis WinPE) ; l'installeur lui-même vient toujours du partage.

### « AutoUnattend.xml neither injected by wimboot nor staged on the share, running interactive setup »

Loggé par `startnet.cmd` quand l'image a un fichier autounattend attaché mais qu'aucun des deux chemins de livraison n'en a produit un. Regarde dans le log Bootimus la requête `/autoinstall/<iso>?mac=...` du client au moment du boot (un 404 signifie qu'aucun fichier ne s'est résolu pour ce client) et l'étape de staging autour de l'heure du patch. Relance le patch SMB depuis les propriétés de l'image.

### `Z:\setup.exe not found on the share`

`startnet.cmd` a monté `Z:`, mais le listing qu'il affiche montre le dossier de l'image (`iso\`, `metadata.txt`) au lieu des fichiers d'installation. Bootimus partage toujours le sous-dossier `iso` de l'image, donc le client s'est connecté à un autre serveur SMB sur la même adresse. Cherche `SMB: smbd exited` dans le log Bootimus et regarde la ligne **Windows SMB** sous **Server Info**. La cause habituelle est un répertoire de données sur un système de fichiers sans permissions Unix : `smbd` quitte alors avec `invalid permissions on directory '.../smb/locks/msg.lock': has 0777 should be 0755`, et c'est un Samba système qui répond sur le port 445 à sa place. Déplace le répertoire de données sur ext4 ou XFS, arrête l'autre Samba et redémarre Bootimus.

### `net use échoue après le redémarrage de la VM`

Corrigé en 0.1.58 en activant `reset on zero vc = yes` dans la config Samba embarquée. Si tu maintiens un `smb.conf` custom, ajoute :

```
reset on zero vc = yes
oplocks = no
kernel oplocks = no
level2 oplocks = no
strict locking = no
deadtime = 1
```

## Étapes suivantes

- Voir [Gestion des images](images.md) pour attacher des fichiers aux images.
- Voir [Gestion des clients](clients.md) pour les overrides par client.
- Voir [Profils de distro](distro-profiles.md) pour les IDs de profils sous-jacents qui mappent vers les sous-répertoires de la bibliothèque.
