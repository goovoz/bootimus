# Déploiements multi-site

Fais tourner un Bootimus comme **primaire**, où tu gères tout, et une **réplique** sur chaque site distant qui reflète le primaire et boote les clients locaux depuis des copies locales. Les répliques sont en lecture seule : chaque modification se fait sur le primaire et arrive sur les répliques à leur prochaine synchronisation.

## Table des matières

- [Topologie](#topologie)
- [Configurer le primaire](#configurer-le-primaire)
- [Configurer une réplique](#configurer-une-réplique)
- [Ce qui se synchronise et ce qui ne se synchronise pas](#ce-qui-se-synchronise-et-ce-qui-ne-se-synchronise-pas)
- [Servir les ISOs en local ou depuis le primaire](#servir-les-isos-en-local-ou-depuis-le-primaire)
- [Bande passante](#bande-passante)
- [Quand le primaire est injoignable](#quand-le-primaire-est-injoignable)
- [Supervision](#supervision)
- [API](#api)

## Topologie

```
                 HQ                                    Branch office
  +------------------------------+          +------------------------------+
  | Bootimus primary             |  HTTPS   | Bootimus replica             |
  | admin :8081  <---------------+----------+ pulls config and assets      |
  | boot  :8080                  |  every   | admin :8081 (read-only UI)   |
  | all changes are made here    |  5 min   | boot  :8080 serves clients   |
  +------------------------------+          +------------------------------+
                                                     | PXE / TFTP / HTTP
                                                     v
                                               branch clients
```

Chaque réplique garde sa propre base de données et son propre répertoire de données, donc une agence continue de booter depuis sa dernière bonne copie même quand le lien WAN est coupé. Les répliques n'écrivent jamais sur le primaire.

## Configurer le primaire

Rien ne change sur le primaire à part la création d'un token pour chaque réplique :

1. Ouvre **Users** → **API Tokens** et crée un token nommé d'après le site.
2. Choisis la portée :
   - `read` suffit pour les clients, images, groupes, cibles SAN, le thème, les profils personnalisés, les outils et les fichiers personnalisés.
   - `admin` synchronise en plus les **fichiers d'auto-installation** et les mots de passe BMC et CHAP. Avec un token `read`, ces mots de passe arrivent masqués et la réplique garde ce qu'elle a déjà.
3. Copie le secret `btk_...` ; il n'est affiché qu'une fois.

Sers l'interface d'administration du primaire en HTTPS (`--admin-tls`, voir le [guide de déploiement](deployment.md#sécuriser-linterface-dadministration)) pour que le token ne circule pas en clair.

## Configurer une réplique

Installe Bootimus sur le site comme d'habitude, puis pointe-le vers le primaire :

```bash
bootimus serve \
  --replica-primary https://bootimus.hq.example.com:8081 \
  --replica-token btk_... \
  --replica-interval 5m
```

Ou dans `bootimus.yaml` :

```yaml
replica:
  primary_url: https://bootimus.hq.example.com:8081
  token: btk_...
  interval: 5m
  sync_isos: true
```

Les variables d'environnement sont `BOOTIMUS_REPLICA_PRIMARY_URL`, `BOOTIMUS_REPLICA_TOKEN`, `BOOTIMUS_REPLICA_INTERVAL` et `BOOTIMUS_REPLICA_SYNC_ISOS`. L'intervalle minimum est de 30 secondes.

La réplique se synchronise une fois au démarrage puis à chaque intervalle. Son interface d'administration affiche un bandeau nommant le primaire et l'heure de la dernière synchronisation réussie, et chaque création, modification ou suppression répond `409 Conflict` avec un renvoi vers le primaire. Les utilisateurs locaux et les tokens d'API peuvent toujours être gérés sur la réplique pour que tu puisses t'y connecter.

Active `--proxy-dhcp` sur la réplique si le site n'a pas de serveur DHCP compatible PXE, exactement comme pour une installation autonome.

## Ce qui se synchronise et ce qui ne se synchronise pas

| Synchronisé depuis le primaire | Gardé en local sur chaque instance |
|--------------------------------|------------------------------------|
| Clients (nom, groupe, images autorisées, réglages BMC, fichier d'auto-installation, cible SAN) | Logs de boot, compteurs de boot et dates de dernier boot |
| Groupes de clients | Inventaire matériel |
| Images et groupes d'images (métadonnées, méthode de boot, paramètres de boot, sélection WinPE) | Actions de prochain boot et Wake-on-LAN |
| Cibles SAN | Clients découverts qui n'ont jamais booté qu'au site distant |
| Thème du menu de boot | Callbacks et état d'installation |
| Profils de distro personnalisés | Utilisateurs, tokens d'API et journal d'audit |
| Outils de boot activés et leurs fichiers | Tâches planifiées et webhooks |
| Fichiers d'auto-installation (token admin uniquement) | Clusters Kubernetes et enrôlement des nœuds |
| Fichiers personnalisés | Réglages de nettoyage |
| Le nom du jeu de bootloaders actif (les fichiers du jeu doivent déjà exister sur la réplique) | Fichiers des jeux de bootloaders personnalisés |

Les lignes qui disparaissent du primaire sont retirées de la réplique, à une exception près : les clients découverts au site distant et jamais promus sur le primaire sont laissés tranquilles. Les actions de prochain boot ne sont volontairement pas reflétées, car une action à usage unique consommée sur le site serait sinon réappliquée à la synchronisation suivante.

## Servir les ISOs en local ou depuis le primaire

Avec `sync_isos: true` (le défaut), la réplique télécharge chaque ISO activé depuis le primaire, extrait les noyaux et initrds en local et sert les clients depuis son propre disque. La première synchronisation d'une grande bibliothèque prend du temps ; la réplique continue entre-temps de servir les images qu'elle a déjà, et les synchronisations suivantes ne transfèrent que les fichiers dont la taille a changé.

Avec `sync_isos: false`, la réplique ne stocke que les métadonnées et fait pointer les entrées du menu de boot vers le port de boot du primaire, donc les clients du site transmettent les ISOs et fichiers de boot à travers le WAN. Les images utilisant les méthodes de boot NBD ou NFS ont besoin des fichiers en local et ne booteront pas dans ce mode. Les scripts d'auto-installation sont toujours servis par la réplique elle-même.

Si un téléchargement échoue, cette image se rabat sur l'URL du primaire jusqu'à la prochaine synchronisation réussie.

## Bande passante

- Les synchronisations de métadonnées sont de petites listes JSON et tournent à chaque intervalle.
- Les ISOs sont récupérés une fois et ne le sont à nouveau que si leur taille change sur le primaire.
- Un manifeste du primaire permet à la réplique de sauter les types qui n'ont pas changé depuis son dernier passage.
- Réduis l'intervalle seulement si tu as besoin d'une propagation plus rapide ; la réplique a aussi un bouton **Sync now**.

## Quand le primaire est injoignable

La réplique continue de servir son dernier bon état. La synchronisation est marquée en échec, le bandeau affiche l'erreur, et les types suivants de ce passage sont sautés plutôt qu'appliqués à moitié. Rien n'est supprimé en local lors d'un passage en échec. La synchronisation reprend automatiquement quand le primaire répond à nouveau.

## Supervision

L'onglet **Server Info** d'une réplique a une carte **Replica** qui liste, par type, si la dernière synchronisation a réussi, combien de lignes le primaire possède et combien ont changé ou été retirées. Les mêmes données sont disponibles via l'API, et `GET /api/me` inclut un objet `replica` pour que les scripts sachent à quelle instance ils parlent.

## API

```bash
# Replica: status of the last sync
curl -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/status

# Replica: sync now
curl -X POST -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/sync

# Primary: what a replica reads
curl -H "Authorization: Bearer $TOKEN" https://hq:8081/api/sync/manifest
```

Le primaire expose `GET /api/sync/manifest`, `GET /api/sync/tool-files?name=` et `GET /api/sync/asset?kind=iso|tool|file` pour les répliques ; les trois acceptent des tokens de portée read.
