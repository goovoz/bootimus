# Multi-Site-Deployments

Betreibe einen Bootimus als **Primary**, auf dem du alles verwaltest, und an jedem Zweigstellen-Standort eine **Replik**, die den Primary spiegelt und lokale Clients aus lokalen Kopien bootet. Repliken sind schreibgeschützt: Jede Änderung geschieht auf dem Primary und erreicht die Repliken bei ihrer nächsten Synchronisation.

## Inhaltsverzeichnis

- [Topologie](#topologie)
- [Den Primary einrichten](#den-primary-einrichten)
- [Eine Replik einrichten](#eine-replik-einrichten)
- [Was synchronisiert wird und was nicht](#was-synchronisiert-wird-und-was-nicht)
- [ISOs lokal oder vom Primary ausliefern](#isos-lokal-oder-vom-primary-ausliefern)
- [Bandbreite](#bandbreite)
- [Wenn der Primary nicht erreichbar ist](#wenn-der-primary-nicht-erreichbar-ist)
- [Überwachung](#überwachung)
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

Jede Replik hat ihre eigene Datenbank und ihr eigenes Datenverzeichnis. Eine Zweigstelle bootet also weiter von ihrer letzten guten Kopie, auch wenn die WAN-Verbindung ausfällt. Repliken schreiben nie auf den Primary.

## Den Primary einrichten

Auf dem Primary ändert sich nichts, außer dass du für jede Replik ein Token anlegst:

1. Öffne **Users** → **API Tokens** und lege ein Token an, das nach dem Standort benannt ist.
2. Wähle den Scope:
   - `read` reicht für Clients, Images, Gruppen, SAN-Ziele, das Theme, eigene Profile, Tools und eigene Dateien.
   - `admin` synchronisiert zusätzlich **Auto-Install-Dateien** sowie die BMC- und CHAP-Passwörter. Mit einem `read`-Token kommen diese Passwörter geschwärzt an, und die Replik behält, was sie bereits hat.
3. Kopiere das `btk_...`-Secret; es wird nur einmal angezeigt.

Liefere die Admin-Oberfläche des Primary über HTTPS aus (`--admin-tls`, siehe den [Deployment-Leitfaden](deployment.md#admin-oberfläche-absichern)), damit das Token nicht im Klartext übertragen wird.

## Eine Replik einrichten

Installiere Bootimus in der Zweigstelle wie gewohnt und richte es dann auf den Primary aus:

```bash
bootimus serve \
  --replica-primary https://bootimus.hq.example.com:8081 \
  --replica-token btk_... \
  --replica-interval 5m
```

Oder in `bootimus.yaml`:

```yaml
replica:
  primary_url: https://bootimus.hq.example.com:8081
  token: btk_...
  interval: 5m
  sync_isos: true
```

Die Umgebungsvariablen heißen `BOOTIMUS_REPLICA_PRIMARY_URL`, `BOOTIMUS_REPLICA_TOKEN`, `BOOTIMUS_REPLICA_INTERVAL` und `BOOTIMUS_REPLICA_SYNC_ISOS`. Das kleinste Intervall beträgt 30 Sekunden.

Die Replik synchronisiert einmal beim Start und danach in jedem Intervall. Ihre Admin-Oberfläche zeigt ein Banner mit dem Namen des Primary und dem Zeitpunkt der letzten erfolgreichen Synchronisation, und jedes Anlegen, Bearbeiten oder Löschen antwortet mit `409 Conflict` und einem Verweis auf den Primary. Lokale Benutzer und API-Token lassen sich auf der Replik weiterhin verwalten, damit du dich dort anmelden kannst.

Aktiviere `--proxy-dhcp` auf der Replik, wenn die Zweigstelle keinen PXE-fähigen DHCP-Server hat, genau wie bei einer eigenständigen Installation.

## Was synchronisiert wird und was nicht

| Vom Primary synchronisiert | Lokal je Instanz |
|----------------------------|------------------|
| Clients (Name, Gruppe, erlaubte Images, BMC-Einstellungen, Auto-Install-Datei, SAN-Ziel) | Boot-Logs, Boot-Zähler und Zeitpunkte des letzten Boots |
| Client-Gruppen | Hardware-Inventar |
| Images und Image-Gruppen (Metadaten, Boot-Methode, Boot-Parameter, WinPE-Auswahl) | Next-Boot-Aktionen und Wake-on-LAN |
| SAN-Ziele | Entdeckte Clients, die nur jemals in der Zweigstelle gebootet haben |
| Boot-Menü-Theme | Install-Callbacks und -Status |
| Eigene Distro-Profile | Benutzer, API-Token und das Audit-Log |
| Aktivierte Boot-Tools und ihre Dateien | Geplante Aufgaben und Webhooks |
| Auto-Install-Dateien (nur mit Admin-Token) | Kubernetes-Cluster und Node-Enrolment |
| Eigene Dateien | Housekeeping-Einstellungen |
| Der Name des aktiven Bootloader-Sets (die Dateien des Sets müssen auf der Replik bereits vorhanden sein) | Dateien eigener Bootloader-Sets |

Zeilen, die auf dem Primary verschwinden, werden von der Replik entfernt, mit einer Ausnahme: Clients, die in der Zweigstelle entdeckt und auf dem Primary nie zu statischen Clients gemacht wurden, bleiben unangetastet. Next-Boot-Aktionen werden bewusst nicht gespiegelt, weil eine in der Zweigstelle verbrauchte Einmal-Aktion sonst bei der nächsten Synchronisation erneut angewendet würde.

## ISOs lokal oder vom Primary ausliefern

Mit `sync_isos: true` (dem Standard) lädt die Replik jedes aktivierte ISO vom Primary herunter, extrahiert Kernel und Initrds lokal und bedient Clients von ihrer eigenen Festplatte. Die erste Synchronisation einer großen Bibliothek dauert eine Weile; die Replik liefert in der Zwischenzeit weiter die Images aus, die sie schon hat, und spätere Synchronisationen übertragen nur Dateien, deren Größe sich geändert hat.

Mit `sync_isos: false` speichert die Replik nur die Metadaten und richtet die Boot-Menü-Einträge auf den Boot-Port des Primary, sodass Clients in der Zweigstelle ISOs und Boot-Dateien über das WAN streamen. Images mit den Boot-Methoden NBD oder NFS brauchen die Dateien lokal und booten in diesem Modus nicht. Auto-Install-Skripte liefert immer die Replik selbst aus.

Schlägt ein Download fehl, fällt das betreffende Image bis zur nächsten erfolgreichen Synchronisation auf die URL des Primary zurück.

## Bandbreite

- Metadaten-Synchronisationen sind kleine JSON-Listen und laufen in jedem Intervall.
- ISOs werden einmal geholt und nur erneut geholt, wenn sich ihre Größe auf dem Primary ändert.
- Ein Manifest des Primary erlaubt der Replik, Arten zu überspringen, die sich seit ihrem letzten Lauf nicht geändert haben.
- Verkürze das Intervall nur, wenn du eine schnellere Verbreitung brauchst; die Replik hat außerdem einen **Sync now**-Button.

## Wenn der Primary nicht erreichbar ist

Die Replik liefert weiter ihren letzten guten Zustand aus. Die Synchronisation wird als fehlgeschlagen markiert, das Banner zeigt den Fehler, und spätere Arten in diesem Lauf werden übersprungen statt halb angewendet. Bei einem fehlgeschlagenen Lauf wird lokal nichts gelöscht. Die Synchronisation läuft automatisch weiter, sobald der Primary wieder antwortet.

## Überwachung

Der Tab **Server Info** einer Replik hat eine Karte **Replica**, die je Art auflistet, ob die letzte Synchronisation erfolgreich war, wie viele Zeilen der Primary hat und wie viele geändert oder entfernt wurden. Dieselben Daten gibt es über die API, und `GET /api/me` enthält ein `replica`-Objekt, damit Skripte erkennen, mit welcher Instanz sie sprechen.

## API

```bash
# Replica: status of the last sync
curl -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/status

# Replica: sync now
curl -X POST -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/sync

# Primary: what a replica reads
curl -H "Authorization: Bearer $TOKEN" https://hq:8081/api/sync/manifest
```

Der Primary stellt für Repliken `GET /api/sync/manifest`, `GET /api/sync/tool-files?name=` und `GET /api/sync/asset?kind=iso|tool|file` bereit; alle drei akzeptieren Token mit Read-Scope.
