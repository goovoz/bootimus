# Disk-Imaging

Bootimus kann die Festplatte eines Clients in ein Image erfassen und dieses Image wieder auf einen Client oder auf alle Mitglieder einer Client-Gruppe ausrollen. Dazu steuert es das mitgelieferte **Clonezilla Live**-Tool unbeaufsichtigt, der Ablauf ist also der aus FOG bekannte: Job einreihen, Rechner per PXE booten lassen, Ergebnis in der Admin-Konsole ablesen.

## Inhaltsverzeichnis

- [So funktioniert es](#so-funktioniert-es)
- [Voraussetzungen](#voraussetzungen)
- [Eine Festplatte erfassen](#eine-festplatte-erfassen)
- [Ein Image ausrollen](#ein-image-ausrollen)
- [Jobs](#jobs)
- [Wo die Images liegen](#wo-die-images-liegen)
- [Netzwerk-Exposition](#netzwerk-exposition)
- [Fehlersuche](#fehlersuche)

## So funktioniert es

1. Du reihst in der Admin-Konsole oder über die API einen **Capture**- oder **Deploy**-Job für einen Client ein. Jeder Job trägt ein zufälliges Token.
2. Beim nächsten PXE-Boot des Clients springt das Menü direkt zu einem Clonezilla-Eintrag, der aus Kernel, Initrd und Squashfs des Clonezilla-Tools gebaut wird.
3. Clonezilla holt sich ein kurzes Job-Skript von Bootimus (`/diskimage/job?token=…`), mountet den Image-Speicher per NFS, führt `ocs-sr` aus und meldet sich unter `/diskimage/callback` zurück.
4. Eine Erfassung schaltet den Client am Ende aus. Ein Deploy startet ihn in die frisch wiederhergestellte Festplatte neu.

Der Job wird nur an die MAC-Adresse ausgeliefert, für die er eingereiht wurde, und ein abgeschlossener, fehlgeschlagener oder abgebrochener Job wird nie erneut ausgeliefert.

## Voraussetzungen

- **Clonezilla-Tool**: unter **Tools** herunterladen und aktivieren. Die Disk-Imaging-Seite zeigt an, wenn es fehlt.
- **NFS-Server**: Bootimus mit `--nfs-enabled` starten (oder `nfs_enabled: true` in `bootimus.yaml`). Clonezilla braucht eine beschreibbare Freigabe zum Speichern, und der eingebaute NFS-Server stellt sie ohne zusätzliche Pakete bereit.
- **Kernel-Boot** für den Client (der Standard). Disk-Imaging funktioniert nicht mit sanboot oder memdisk.
- Port `2049/tcp` (oder der konfigurierte `--nfs-port`) muss von den Clients aus erreichbar sein, zusätzlich zu den üblichen TFTP- und HTTP-Ports.

## Eine Festplatte erfassen

1. Öffne **Disk Images** und klicke auf **Capture Disk…**, oder klicke unter **Clients** in der Zeile eines Clients auf **Capture Disk**.
2. Wähle den Client, gib dem Image einen Namen (Kleinbuchstaben, Ziffern, Punkte, Bindestriche und Unterstriche) und wähle die Festplatte. `sda` ist der Standard; `nvme0n1` und Co. funktionieren ebenfalls, und `auto` nimmt die erste fest eingebaute, beschreibbare Festplatte, die Clonezilla findet.
3. Optional **Send Wake-on-LAN now** ankreuzen.
4. Starte den Client neu (oder lass das Magic Packet das erledigen). Er bootet Clonezilla, sichert die Festplatte und schaltet sich aus.

Clonezilla führt `ocs-sr -q2 -j2 -z1p -i 4096 -sc savedisk <name> <disk>` aus: partclone wo möglich, versteckte Daten zwischen MBR und erster Partition inklusive, mehrthreadige gzip-Kompression, Aufteilung in 4-GiB-Teile.

Während die Erfassung läuft, zeigt das Image den Status **capturing**. Meldet der Client Erfolg, wird das Image **ready** und seine Größe wird gespeichert. Schlägt `ocs-sr` fehl, wird das Image als **failed** markiert und die Teildaten werden verworfen.

**Per API**:
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","name":"win11-lab","description":"Lab build, Oct 2026","disk":"sda","wake":true}' \
  http://localhost:8081/api/disk-images/capture
```

## Ein Image ausrollen

1. Klicke neben einem fertigen Image auf **Deploy**, in einer Client-Zeile auf **Deploy Image** oder in den Bulk-Aktionen einer Client-Gruppe auf **Deploy Disk Image to All…**.
2. Wähle das Ziel (ein Client oder eine Client-Gruppe), die zu überschreibende Festplatte und ob Wake-on-LAN gesendet werden soll. Gruppen-Deploys berücksichtigen die WOL-Broadcast-Adresse und die Staffelverzögerung der Gruppe.
3. Bestätige. Alles auf der Zielfestplatte wird ersetzt.

Clonezilla führt `ocs-sr -g auto -e1 auto -e2 -r -j2 -scr restoredisk <name> <disk>` aus: Die Partitionstabelle wird aus dem Image neu erstellt, Partitionen werden auf eine größere Festplatte vergrößert, GRUB wird dort neu installiert, wo das Image es hatte, und der Client startet am Ende neu.

Jedes Gruppenmitglied bekommt seinen eigenen Job, sodass du zusehen kannst, wie sie nacheinander fertig werden. Ein Client, der bereits einen aktiven Job hat, wird übersprungen und in der Antwort aufgeführt.

**Per API**:
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

Die Tabelle **Capture and Deploy Jobs** zeigt jeden Job mit seinem Status:

| Status | Bedeutung |
|--------|-----------|
| `pending` | Eingereiht; wartet darauf, dass der Client per PXE bootet |
| `running` | Clonezilla hat das Job-Skript geholt und arbeitet |
| `completed` | Der Client hat Erfolg gemeldet |
| `failed` | Mount oder `ocs-sr` ist fehlgeschlagen; die Spalte mit der Meldung nennt den Grund |
| `cancelled` | Über Konsole oder API abgebrochen |

Wartende und laufende Jobs können abgebrochen werden. Das Abbrechen einer Erfassung verwirft das Image, das gerade geschrieben wurde. Ein Client kann immer nur einen aktiven Job haben.

Boot-Menü-Vorschauen (**Preview Menu** in einer Client-Zeile) zeigen den Clonezilla-Boot-Eintrag, solange ein Job aktiv ist. So prüfst du schnell, was der Client tun wird.

Abgeschlossene Jobs lösen die Webhook-Ereignisse `disk.capture_completed`, `disk.capture_failed`, `disk.deploy_completed` und `disk.deploy_failed` aus, wenn diese Option unter **Settings → Webhook** aktiviert ist.

## Wo die Images liegen

Images sind gewöhnliche Clonezilla-Image-Verzeichnisse unter `<data-dir>/disk-images/<name>/`. Sie lassen sich also inspizieren, woanders hinkopieren oder von jedem Clonezilla-Live-USB-Stick aus von Hand wiederherstellen. Laufende Erfassungen werden unter `<data-dir>/disk-images/.staging/<token>/` geschrieben und an ihren Platz verschoben, sobald der Client Erfolg meldet.

Disk-Images sind groß. Behalte deshalb die Karte **Housekeeping** unter **Settings** im Blick, die zeigt, wie viel Platz jeder Eintrag auf oberster Ebene des Datenverzeichnisses belegt. Backups aus der Admin-Konsole überspringen die Verzeichnisse `isos` und `tools`, enthalten aber alles andere. Lösche also nicht mehr benötigte Disk-Images, bevor du ein Backup herunterlädst.

## Netzwerk-Exposition

Clonezilla mountet den Image-Speicher per NFS v3 ohne Authentifizierung, genauso wie FOG und DRBL arbeiten. Bootimus begrenzt, was diese Freigabe offenlegt:

- der normale Boot-Export (`isos/`) und die Wurzel der Disk-Images sind schreibgeschützt;
- ein Capture-Job bekommt seinen eigenen beschreibbaren Export unter `/disk-images/.staging/<token>`, der nur existiert, solange der Job aktiv ist, und ohne das Token nicht zu erraten ist;
- einzelne Image-Verzeichnisse lassen sich nicht direkt mounten; Deploys mounten die schreibgeschützte Image-Wurzel.

Betreibe das Boot-Netz in einem Segment, dem du vertraust, so wie du es für PXE ohnehin tun solltest.

## Fehlersuche

**Der Job bleibt pending.** Der Client hat seit dem Einreihen nicht per PXE gebootet, oder er hat gebootet, aber Clonezilla ist nicht aktiviert, sodass Bootimus das normale Menü ausgeliefert hat. Prüfe **Tools** und dann **Preview Menu** in der Client-Zeile.

**`nfs_mount_failed` in der Meldungsspalte.** Der Client konnte die Freigabe nicht mounten. Prüfe `--nfs-enabled`, dass der NFS-Port aus dem Subnetz des Clients erreichbar ist und dass `--server-addr` eine Adresse ist, die der Client erreichen kann.

**`no_disk_found` mit `disk=auto`.** Clonezilla hat keine fest eingebaute, beschreibbare Festplatte gefunden. Gib den Gerätenamen explizit an.

**`ocs-sr_exit_N`.** Clonezilla selbst ist fehlgeschlagen. Der Client zeigt die Clonezilla-Ausgabe 30 Sekunden lang an, bevor er neu startet; die üblichen Ursachen sind die Erfassung einer Festplatte mit gemountetem oder beschädigtem Dateisystem oder ein Deploy auf eine Festplatte, die kleiner ist als das Image.

**Das Image zeigt failed.** Nichts abbrechen; lösche es auf der Seite **Disk Images** und erfasse es erneut.
