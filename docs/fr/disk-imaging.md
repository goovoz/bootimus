# Imagerie disque

Bootimus peut capturer le disque d'un client dans une image et redéployer cette image sur un client ou sur tous les membres d'un groupe de clients. Il pilote l'outil **Clonezilla Live** fourni de manière non interactive, donc le déroulement est celui que les gens connaissent de FOG : tu mets un job en file, tu laisses la machine booter en PXE et tu lis le résultat dans la console d'administration.

## Table des matières

- [Comment ça marche](#comment-ça-marche)
- [Prérequis](#prérequis)
- [Capturer un disque](#capturer-un-disque)
- [Déployer une image](#déployer-une-image)
- [Jobs](#jobs)
- [Où vivent les images](#où-vivent-les-images)
- [Exposition réseau](#exposition-réseau)
- [Dépannage](#dépannage)

## Comment ça marche

1. Tu mets en file un job de **capture** ou de **déploiement** pour un client depuis la console d'administration ou l'API. Chaque job porte un token aléatoire.
2. Au prochain boot PXE du client, le menu saute directement à une entrée Clonezilla construite à partir du noyau, de l'initrd et du squashfs de l'outil Clonezilla.
3. Clonezilla récupère un court script de job auprès de Bootimus (`/diskimage/job?token=…`), monte le dépôt d'images via NFS, lance `ocs-sr` et rend compte à `/diskimage/callback`.
4. Une capture éteint le client quand elle se termine. Un déploiement le redémarre sur le disque fraîchement restauré.

Le job n'est servi qu'à l'adresse MAC pour laquelle il a été mis en file, et un job terminé, échoué ou annulé n'est jamais servi à nouveau.

## Prérequis

- **Outil Clonezilla** : télécharge-le et active-le dans **Tools**. La page d'imagerie disque te prévient quand il manque.
- **Serveur NFS** : démarre Bootimus avec `--nfs-enabled` (ou `nfs_enabled: true` dans `bootimus.yaml`). Clonezilla a besoin d'un partage en écriture où enregistrer, et le serveur NFS intégré le fournit sans paquet supplémentaire.
- **Boot par noyau** pour le client (le défaut). L'imagerie disque ne fonctionne pas avec sanboot ni memdisk.
- Le port `2049/tcp` (ou le `--nfs-port` configuré) joignable depuis les clients, en plus des ports TFTP et HTTP habituels.

## Capturer un disque

1. Ouvre **Disk Images** et clique sur **Capture Disk…**, ou clique sur **Capture Disk** sur la ligne d'un client dans **Clients**.
2. Choisis le client, donne un nom à l'image (lettres minuscules, chiffres, points, tirets et tirets bas) et choisis le disque. `sda` est le défaut ; `nvme0n1` et consorts marchent aussi, et `auto` prend le premier disque fixe et inscriptible que Clonezilla trouve.
3. Coche éventuellement **Send Wake-on-LAN now**.
4. Redémarre le client (ou laisse le paquet magique le faire). Il boote Clonezilla, sauvegarde le disque et s'éteint.

Clonezilla lance `ocs-sr -q2 -j2 -z1p -i 4096 -sc savedisk <name> <disk>` : partclone quand c'est possible, données cachées entre le MBR et la première partition incluses, compression gzip multithread, découpage en volumes de 4 Gio.

Pendant la capture, l'image apparaît comme **capturing**. Quand le client signale la réussite, l'image passe à **ready** et sa taille est enregistrée. Si `ocs-sr` échoue, l'image est marquée **failed** et les données partielles sont jetées.

**Via l'API** :
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","name":"win11-lab","description":"Lab build, Oct 2026","disk":"sda","wake":true}' \
  http://localhost:8081/api/disk-images/capture
```

## Déployer une image

1. Clique sur **Deploy** à côté d'une image prête, sur **Deploy Image** sur la ligne d'un client, ou sur **Deploy Disk Image to All…** dans les actions groupées d'un groupe de clients.
2. Choisis la cible (un client ou un groupe de clients), le disque à écraser et s'il faut envoyer un Wake-on-LAN. Les déploiements de groupe respectent l'adresse de broadcast WOL et le délai d'échelonnement du groupe.
3. Confirme. Tout ce qui se trouve sur le disque cible est remplacé.

Clonezilla lance `ocs-sr -g auto -e1 auto -e2 -r -j2 -scr restoredisk <name> <disk>` : la table de partitions est recréée depuis l'image, les partitions sont agrandies pour remplir un disque plus grand, GRUB est réinstallé là où l'image l'avait, et le client redémarre une fois terminé.

Chaque membre d'un groupe reçoit son propre job, donc tu peux les voir se terminer un par un. Un client qui a déjà un job actif est ignoré et listé dans la réponse.

**Via l'API** :
```bash
# One client
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","image_id":3,"disk":"sda"}' \
  http://localhost:8081/api/disk-images/deploy

# Every enabled member of client group 2, waking them as it goes
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"client_group_id":2,"image_id":3,"disk":"auto","wake":true}' \
  http://localhost:8081/api/disk-images/deploy
```

## Jobs

Le tableau **Capture and Deploy Jobs** montre chaque job avec son état :

| État | Signification |
|------|---------------|
| `pending` | En file ; attend que le client boote en PXE |
| `running` | Clonezilla a récupéré le script de job et travaille |
| `completed` | Le client a signalé la réussite |
| `failed` | Le montage ou `ocs-sr` a échoué ; la colonne message donne la raison |
| `cancelled` | Annulé depuis la console ou l'API |

Les jobs en attente et en cours peuvent être annulés. Annuler une capture jette l'image en cours d'écriture. Un client ne peut avoir qu'un seul job actif à la fois.

Les aperçus du menu de boot (**Preview Menu** sur la ligne d'un client) montrent l'entrée de boot Clonezilla tant qu'un job est actif, ce qui est un moyen rapide de vérifier ce que le client va faire.

Les jobs terminés déclenchent les événements webhook `disk.capture_completed`, `disk.capture_failed`, `disk.deploy_completed` et `disk.deploy_failed` quand cette option est activée dans **Settings → Webhook**.

## Où vivent les images

Les images sont de simples répertoires d'image Clonezilla sous `<data-dir>/disk-images/<name>/`, donc on peut les inspecter, les copier ailleurs ou les restaurer à la main depuis n'importe quelle clé USB Clonezilla Live. Les captures en cours sont écrites sous `<data-dir>/disk-images/.staging/<token>/` et déplacées à leur place quand le client signale la réussite.

Les images disque sont volumineuses, alors garde un œil sur la carte **Housekeeping** dans **Settings**, qui montre l'espace utilisé par chaque entrée de premier niveau du répertoire de données. Les sauvegardes faites depuis la console d'administration sautent les répertoires `isos` et `tools` mais incluent tout le reste, donc supprime les images disque dont tu n'as plus besoin avant de télécharger une sauvegarde.

## Exposition réseau

Clonezilla monte le dépôt d'images en NFS v3 sans authentification, comme fonctionnent FOG et DRBL. Bootimus limite ce que ce partage expose :

- l'export de boot normal (`isos/`) et la racine des images disque sont en lecture seule ;
- un job de capture reçoit son propre export inscriptible sur `/disk-images/.staging/<token>`, qui n'existe que tant que ce job est actif et qu'on ne peut pas deviner sans le token ;
- les répertoires d'image individuels ne peuvent pas être montés directement ; les déploiements montent la racine d'images en lecture seule.

Fais tourner le réseau de boot sur un segment de confiance, comme tu devrais déjà le faire pour PXE.

## Dépannage

**Le job reste en pending.** Le client n'a pas booté en PXE depuis la mise en file du job, ou il a booté mais Clonezilla n'est pas activé et Bootimus a servi le menu normal. Vérifie **Tools**, puis **Preview Menu** sur la ligne du client.

**`nfs_mount_failed` dans la colonne message.** Le client n'a pas pu monter le partage. Confirme `--nfs-enabled`, que le port NFS est joignable depuis le sous-réseau du client, et que `--server-addr` est une adresse que le client peut atteindre.

**`no_disk_found` avec `disk=auto`.** Clonezilla n'a vu aucun disque fixe et inscriptible. Passe le nom du périphérique explicitement.

**`ocs-sr_exit_N`.** Clonezilla lui-même a échoué. Le client affiche la sortie de Clonezilla pendant 30 secondes avant de redémarrer ; les causes habituelles sont la capture d'un disque avec un système de fichiers monté ou abîmé, ou un déploiement sur un disque plus petit que l'image.

**L'image apparaît comme failed.** N'annule rien ; supprime-la depuis la page **Disk Images** et recapture.
