#  Guide de gestion des clients

Guide complet pour gérer les clients de boot réseau avec contrôle d'accès basé sur MAC.

##  Table des matières

- [Vue d'ensemble](#vue-densemble)
- [Ajouter des clients](#ajouter-des-clients)
- [Permissions des clients](#permissions-des-clients)
- [Images publiques vs privées](#images-publiques-vs-privées)
- [Statistiques des clients](#statistiques-des-clients)
- [Opérations en masse](#opérations-en-masse)
- [Prévisualiser le menu de boot d'un client](#prévisualiser-le-menu-de-boot-dun-client)
- [Booter depuis iSCSI ou AoE](#booter-depuis-iscsi-ou-aoe)
- [Console distante](#console-distante)
- [Dépannage](#dépannage)

## Vue d'ensemble

Bootimus utilise un contrôle d'accès basé sur les adresses MAC pour gérer quels clients peuvent booter et à quels ISOs ils peuvent accéder. Ça apporte un contrôle granulaire sur ton environnement de boot réseau.

### Concepts clés

- **Client** : un périphérique de boot réseau identifié par adresse MAC
- **Client statique** : créé manuellement ou promu depuis discovered — un enregistrement permanent
- **Client découvert** : créé automatiquement quand un périphérique inconnu boote en PXE (comme un lease DHCP)
- **Enabled** : le client est autorisé à booter (apparaît dans le menu de boot)
- **Disabled** : le client ne peut pas booter (bloqué de l'accès au menu de boot)
- **Images assignées** : quand un client a des images assignées, il voit **uniquement ces images** (pas la liste publique complète)
- **Show Public Images** : quand activé en parallèle des images assignées, le client voit à la fois les images assignées et les images publiques
- **Next Boot Action** : un override d'image de boot one-time qui s'efface automatiquement après usage
- **Variables d'installation automatique** : des valeurs nommées disponibles dans les fichiers d'auto-installation sous la forme `{{VAR.<name>}}` ; les variables de groupe sont des valeurs par défaut, celles du client les surchargent (voir [Auto-Install](auto-install.md#variables))

### Auto-découverte de clients

Quand un périphérique inconnu boote en PXE, Bootimus crée automatiquement un enregistrement client **discovered** avec :
- L'adresse MAC issue de la requête PXE
- L'inventaire matériel (CPU, mémoire, fabricant, numéro de série, NIC)
- Activé avec les images publiques visibles par défaut

Les clients découverts apparaissent dans la table des clients avec un badge « Discovered ». Tu peux les promouvoir en clients statiques avec le bouton **« Make Static »**, qui les enregistre comme des entrées permanentes. Si un client précédemment supprimé reboote en PXE, il est automatiquement restauré.

### Modes de base de données

**Mode SQLite** :
- Clients stockés dans une base SQLite
- Assignations d'images stockées dans le champ JSON `allowed_images`
- Parfait pour les déploiements mono-serveur

**Mode PostgreSQL** :
- Clients stockés dans une base PostgreSQL
- Assignations d'images via table de relation many-to-many
- Meilleures performances pour les gros déploiements

## Ajouter des clients

### Via l'interface web

1. Va sur le panneau admin : `http://your-server:8081`
2. Clique sur l'onglet **« Clients »**
3. Clique sur le bouton **« Add Client »**
4. Remplis les détails :
   - **MAC Address** : `00:11:22:33:44:55` (obligatoire)
   - **Name** : nom convivial (par ex. « Lab Server 1 »)
   - **Description** : détails supplémentaires (optionnel)
   - **Enabled** : coche pour autoriser le boot
5. Clique sur **« Create Client »**

### Via l'API

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

### Format d'adresse MAC

Bootimus accepte les adresses MAC dans ces formats :
- `00:11:22:33:44:55` (séparée par des deux-points, préféré)
- `00-11-22-33-44-55` (séparée par des tirets, auto-convertie)
- `001122334455` (sans séparateurs, auto-convertie)

Tous les formats sont normalisés en minuscules séparés par des deux-points.

## Permissions des clients

### Assigner des images à un client

**Via l'interface web** :
1. Clique sur **« Edit »** sur la ligne client
2. Sélectionne les images depuis la liste déroulante multi-sélection
3. Clique sur **« Update Client »**

**Via l'API** :
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

### Voir les permissions d'un client

**Via l'interface web** :
- Les images assignées d'un client sont affichées dans la modal d'édition

**Via l'API** :
```bash
# Get client details including assigned images
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq
```

**Réponse** :
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

## Images publiques vs privées

### Images publiques

Les images publiques sont disponibles pour **tous les clients**, y compris ceux non enregistrés.

**Cas d'usage** :
-  ISOs de secours/récupération
-  Outils de diagnostic réseau
-  Images de déploiement courantes
-  Environnements de lab ouverts

**Rendre une image publique** :
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": true}'
```

### Images privées

Les images privées sont **uniquement disponibles pour les clients assignés**.

**Cas d'usage** :
-  Images sensibles ou sous licence
-  Déploiements spécifiques à un client
-  Environnements restreints
-  Images beta/test

**Rendre une image privée** :
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=windows.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": false}'
```

### Matrice de contrôle d'accès

| État du client | Ce qu'ils voient |
|--------------|---------------|
| **Activé + Assigné** | Uniquement leurs images assignées |
| **Activé + Sans assignation** | Toutes les images publiques |
| **Désactivé** | Toutes les images publiques |
| **Non enregistré** | Toutes les images publiques |

## Statistiques des clients

Bootimus suit des statistiques de boot pour chaque client :

- **Boot Count** : nombre total de tentatives de boot
- **Last Boot** : timestamp du boot le plus récent
- **Success Rate** : pourcentage de boots réussis

### Voir les statistiques

**Via l'interface web** :
- Statistiques affichées dans la table des clients

**Via l'API** :
```bash
# Get all clients with statistics
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | jq '.data[] | {name, boot_count, last_boot}'

# Get top clients by boot count
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data | sort_by(.boot_count) | reverse | .[0:10] | .[] | {name, boot_count}'
```

### Logs de boot

Voir les logs de boot détaillés par client :

```bash
# Filter boot logs by MAC address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'
```

## Opérations en masse

### Ajout en masse de clients

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

### Assignation d'images en masse

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

### Activer/Désactiver en masse

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

### Exporter la liste des clients

```bash
#!/bin/bash
# export-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

echo "MAC Address,Name,Description,Enabled,Boot Count,Last Boot"

curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[] | [.mac_address, .name, .description, .enabled, .boot_count, .last_boot] | @csv'
```

### Importer des clients depuis CSV

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

## Next Boot Action

Définit une image de boot one-time pour un client. Au prochain boot PXE, l'image sélectionnée sera pré-sélectionnée comme entrée par défaut du menu avec un timeout. L'action s'efface automatiquement après usage — les boots suivants reviennent à la normale.

### Via l'interface web

1. Clique sur **« Next Boot »** sur une ligne client
2. Sélectionne une image dans la liste déroulante
3. Clique sur **« Set Next Boot »** pour juste définir l'image, ou **« Set & Wake »** pour envoyer aussi un paquet Wake-on-LAN

### Via l'API

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

### Comportement

- Le menu de boot s'affiche normalement mais avec l'image next boot pré-sélectionnée par défaut
- Si le timeout global du menu est désactivé (à 0), un timeout de 10 secondes est appliqué en override
- Si le client ne boote pas avant que l'action soit consommée, le next boot s'efface à la première requête PXE
- Les groupes vides sont masqués du menu quand un client a des images assignées

## Prévisualiser le menu de boot d'un client

Génère le script iPXE exact qu'un client recevra sans attendre qu'il boote en PXE. La prévisualisation n'a aucun effet de bord : rien n'est écrit dans le log de boot, l'action next boot du client n'est pas consommée, et l'état d'enrôlement cluster n'est pas touché.

### Via l'interface web

- Clique sur **« Preview Menu »** sur une ligne client, ou
- Ouvre **Boot Menu** → **Preview as Client**, saisis une adresse MAC (ou laisse `unknown` pour un client non enregistré) et clique sur **« Preview Menu »**

La fenêtre indique si la MAC correspond à un client enregistré, s'il recevrait le menu normal ou un boot d'installation cluster, l'éventuelle image next boot en attente, et le script complet avec des boutons **Copy** et **Download**.

### Via l'API

```bash
# Preview for a specific client
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8081/api/clients/menu-preview?mac=00:11:22:33:44:55"

# Preview the anonymous menu an unregistered client gets
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients/menu-preview
```

Les données de la réponse contiennent `mac`, `known_client`, `client_name`, `next_boot_image`, `kind` (`menu` ou `install`) et `script`. Les utilisateurs en lecture seule peuvent appeler cet endpoint.

## Booter depuis iSCSI ou AoE

Les clients peuvent booter un système d'exploitation qui réside sur un disque SAN plutôt que sur un disque local. Bootimus confie le disque au firmware avec la commande `sanboot` d'iPXE, donc tout ce qui peut booter depuis un SAN avec iPXE fonctionne ici : cibles iSCSI et ATA-over-Ethernet, et images disque brutes servies en HTTP. Bootimus n'héberge pas les disques ; pointe une cible vers un serveur de stockage que tu exploites déjà.

### Définir une cible

**Clients** → **SAN Targets** → **Add SAN Target** :

| Champ | Signification |
|-------|---------------|
| Name | Affiché dans le menu de boot sous « Boot from SAN: *name* » |
| SAN URI | L'URI SAN iPXE, par exemple `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`, `aoe:e0.0` ou `http://10.0.0.5/disk.img` |
| Initiator IQN | Optionnel. `{{MAC}}` est remplacé par l'adresse MAC du client sans les deux-points, pour qu'une seule cible serve plusieurs clients avec des noms d'initiateur distincts |
| CHAP | Nom d'utilisateur et mot de passe optionnels, plus les identifiants CHAP inverse (mutuel) si la cible l'exige |

Le format d'URI iSCSI est `iscsi:<server>:<protocol>:<port>:<lun>:<iqn>`. Laisse le protocole, le port et le LUN vides pour les valeurs par défaut, comme dans l'exemple ci-dessus.

### Assigner une cible

Ouvre un client (ou un groupe de clients) et choisis la cible sous **SAN Target**. Un client sans cible propre hérite de celle du groupe. Le menu du client gagne une entrée « Boot from SAN » ; coche **Boot from SAN by default** pour en faire l'entrée sur laquelle le menu retombe à l'expiration du timeout, de sorte qu'une machine redémarrée reparte directement depuis le SAN. Une image next boot en attente reste prioritaire, et les images habituelles restent sélectionnables.

Pour un boot SAN ponctuel, choisis la cible dans **Next Boot** ; elle apparaît après les images et s'efface après un boot comme toute autre action next boot.

### Exemples de cibles

Une cible Linux avec `tgt` :

```bash
sudo tgtadm --lld iscsi --op new --mode target --tid 1 -T iqn.2024-01.lan:disk0
sudo tgtadm --lld iscsi --op new --mode logicalunit --tid 1 --lun 1 -b /srv/iscsi/disk0.img
sudo tgtadm --lld iscsi --op bind --mode target --tid 1 -I ALL
```

URI de la cible : `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`

Avec LIO (`targetcli`), crée un backstore fileio ou block, un iqn, un LUN et un portal, puis désactive l'authentification pour le TPG ou définis `userid`/`password` (et `mutual_userid`/`mutual_password`) pour qu'ils correspondent aux champs CHAP ici.

**Windows** s'installe et boote depuis iSCSI grâce au SAN hook d'iPXE : lance l'installeur Windows depuis le menu pendant que le client a une cible assignée, et l'installeur voit le disque iSCSI comme un disque local. Après l'installation, coche **Boot from SAN by default** pour que la machine y revienne à chaque boot. Windows a besoin du pilote réseau de la carte depuis laquelle il a booté ; la plupart des cartes Intel et Realtek récentes sont couvertes par l'installeur.

### Mises en garde

- Les identifiants CHAP sont placés dans le script iPXE, que le client récupère en HTTP non chiffré au boot. Considère le réseau de boot comme de confiance, comme tu le fais déjà pour les fichiers de réponse d'auto-installation.
- Le `sanboot` d'iPXE exige un bootloader sur le disque SAN ; il n'installe rien.
- Les utilisateurs en lecture seule voient les cibles mais pas les mots de passe CHAP.

## Wake-on-LAN

Envoie un magic packet WOL pour réveiller un client à distance. Combine avec **Next Boot** pour réveiller une machine et lui faire booter une image spécifique.

### Via l'API

```bash
# Wake a client
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55"

# Wake with custom broadcast address
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"broadcast_addr":"192.168.1.255"}'
```

## Console distante

Les clients ayant un hôte BMC configuré disposent d'un bouton **Open Console** dans la boîte de dialogue d'édition du client et d'un bouton **Console** dans la liste des clients. Il ouvre une console série interactive dans le navigateur, relayée par Bootimus via un WebSocket, pour suivre une machine pendant le POST, la configuration du firmware et l'installeur sans écran branché. La console est réservée aux administrateurs, une session par client à la fois, et se ferme après 30 minutes sans activité.

| Protocole | Transport | Prérequis |
|---|---|---|
| **Intel AMT** | Serial-over-LAN via le service de redirection AMT, implémenté nativement par Bootimus sur le port 16994 (16995 quand le client utilise le port de gestion TLS 16993) | SOL et l'écouteur de redirection activés dans MEBx sous *Remote Control*, ou dans l'interface web AMT sous *Remote Control* → *Redirection* ; les mêmes utilisateur et mot de passe que pour le contrôle d'alimentation |
| **Redfish** | IPMI Serial-over-LAN en exécutant `ipmitool sol activate` sur l'hôte Bootimus | `ipmitool` dans le `PATH` (l'image Docker l'inclut), le port UDP 623 joignable sur le BMC, et SOL activé pour l'utilisateur BMC. Le port Redfish n'est pas utilisé pour la console |

La console série n'affiche que ce que le firmware ou le système d'exploitation écrit sur le port série. Sur la plupart des serveurs, cela veut dire activer *Console Redirection* dans la configuration BIOS ou UEFI, et sous Linux ajouter `console=ttyS0,115200` (ou `ttyS1`, selon la carte) à la ligne de commande du kernel. Les machines Intel AMT redirigent COM1 à 115200 bauds une fois SOL activé. Si l'écran reste vide, appuie sur Entrée ; si rien n'apparaît pendant le boot, la redirection de console n'est pas activée dans le firmware.

Les frappes clavier sont envoyées telles quelles, donc les raccourcis habituels du terminal fonctionnent. Fermer la boîte de dialogue ou cliquer sur **Disconnect** termine la session et, pour IPMI, désactive la charge utile SOL sur le BMC pour que la connexion suivante reparte proprement.

### Via l'API

L'endpoint est un WebSocket sur `/api/clients/console?mac=...` sur le port admin. Les navigateurs ne peuvent pas envoyer d'en-tête `Authorization` sur un WebSocket, le JWT est donc passé soit comme seconde entrée `Sec-WebSocket-Protocol` après `bearer`, soit comme paramètre de requête `token`. Les trames binaires transportent la sortie de la console et les frappes clavier ; les trames texte envoyées par Bootimus sont des messages d'état JSON tels que `{"type":"connected","message":"..."}`.

```bash
# websocat example: interactive console for a client
websocat --binary "ws://localhost:8081/api/clients/console?mac=00:11:22:33:44:55&token=$TOKEN"
```

## Inventaire matériel

Bootimus collecte les infos matérielles des clients PXE pendant le boot, dont :
- CPU, mémoire, plateforme et architecture
- Fabricant, nom de produit et numéro de série
- UUID et infos chip du NIC
- Adresse IP

Visualise l'inventaire depuis la modal **Edit & Assign Images** pour n'importe quel client, ou via l'API :

```bash
# Latest inventory
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory?mac=00:11:22:33:44:55"

# Inventory history
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory/history?mac=00:11:22:33:44:55&limit=10"
```

## Dépannage

### Le client ne voit pas le menu de boot

**Symptômes** : le client boote mais affiche un menu vide ou « No boot images available »

**Causes possibles** :
1. Le client est désactivé
2. Aucune image publique disponible
3. Aucune image assignée au client
4. Toutes les images sont désactivées

**Solution** :
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

### Adresse MAC non détectée

**Symptômes** : les logs de boot affichent une adresse MAC « unknown »

**Causes possibles** :
1. iPXE ne peut pas détecter l'adresse MAC depuis l'interface réseau
2. Le client utilise plusieurs interfaces réseau

**Solution** :
```bash
# Check boot logs for actual IP address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | jq '.data[] | {mac_address, ip_address}'

# Register client by IP if MAC is unknown
# (Note: Less reliable, IP may change)
```

### Les images assignées ne s'affichent pas

**Symptômes** : le client ne voit que les images publiques, pas celles assignées

**Causes possibles** :
1. Client non activé
2. Images non activées
3. Mauvais format d'adresse MAC
4. Problème de sync base de données

**Solution** :
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

### Erreur de client en doublon

**Symptômes** : « Client already exists » ou erreur de contrainte UNIQUE

**Cause** : adresse MAC déjà enregistrée

**Solution** :
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

## Étapes suivantes

-  Configure la [Gestion des images](images.md) pour ajouter des ISOs
-  Utilise la [Console d'administration](admin.md) pour la gestion
-  Configure la [Configuration DHCP](dhcp.md) pour le boot réseau
-  Consulte les [Logs de boot](admin.md#boot-logs) pour le monitoring
