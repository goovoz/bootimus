# Auto-Install-Leitfaden

Unbeaufsichtigte Installationen für Windows, Ubuntu, Debian, die Red-Hat-Familie, openSUSE, Fedora CoreOS, Flatcar und Proxmox VE. Einmal eine Config-Datei reinwerfen, an ein Image anhängen — und jeder PXE-Boot zieht die Installation ohne einen einzigen Tastendruck durch.

## Inhaltsverzeichnis

- [Überblick](#überblick)
- [Unterstützte Formate](#unterstützte-formate)
- [Die Dateibibliothek](#die-dateibibliothek)
- [Dateien anhängen](#dateien-anhängen)
- [Auflösungsreihenfolge](#auflösungsreihenfolge)
- [Platzhalter](#platzhalter)
- [Variablen](#variablen)
- [Vorschau](#vorschau)
- [Validierung](#validierung)
- [Beispiele](#beispiele)
- [Nicht unterstützt](#nicht-unterstützt)
- [Installationsabschluss melden](#installationsabschluss-melden)
- [Windows-Hinweise](#windows-hinweise)
- [REST-API](#rest-api)
- [Fehlersuche](#fehlersuche)

## Überblick

Auto-Install-Konfigurationen werden in einer Per-Distro-Bibliothek unter `data/autoinstall/` gespeichert. Du kannst:

- **Dateien im UI verwalten** im Tab **Auto-Install** — Configs anlegen, editieren, hochladen, herunterladen und löschen, ohne das Dateisystem anzufassen.
- **Default pro Image setzen** (jeder Client, der dieses Image bootet, bekommt dieselbe Config).
- **Pro Client oder Client-Gruppe überschreiben**, wenn eine Maschine eine andere Config braucht (anderer Hostname, anderes Disk-Layout, andere Rolle etc.).
- **Templates bauen** mit Platzhaltern wie `{{HOSTNAME}}` und `{{IP}}`, die beim Ausliefern anhand der Identität des bootenden Clients aufgelöst werden, plus eigenen `{{VAR.name}}`-Variablen pro Client oder pro Gruppe.
- **Vorschau** des gerenderten Ergebnisses für jeden Client und jedes Image, und YAML-, XML- und JSON-Dateien werden beim Speichern auf Wohlgeformtheit geprüft.

Bootimus liefert die gerenderte Datei über HTTP am Auto-Install-Endpunkt aus. Linux-Installer holen sie über einen Kernel-Parameter; für Windows holt wimboot dieselbe URL als zusätzliche `initrd` und injiziert sie als `AutoUnattend.xml` in die WinPE-RAM-Disk, sodass `setup.exe /unattend:` sie automatisch aufgreift (siehe [Windows-Hinweise](#windows-hinweise)).

## Unterstützte Formate

| Distro-Familie | Format | Datei-Endung | Erkannt als |
|--------------|--------|----------------|-------------|
| Windows (10/11/Server) | `autounattend.xml` | `.xml` | `autounattend` |
| Ubuntu (Server live, 20.04+) | cloud-init / autoinstall | `.yaml`, `.yml` | `autoinstall` |
| Debian | preseed | `.cfg` | `preseed` |
| Red Hat / Rocky / Fedora / Alma | kickstart | `.ks` | `kickstart` |
| openSUSE Leap 15 / SLES 15 | AutoYaST-Profil | `.xml` unter `opensuse/` | `autoyast` |
| openSUSE Leap 16 / Tumbleweed / SLES 16 | Agama-Profil | `.json`, `.jsonnet` unter `opensuse/` | `agama` |
| Fedora CoreOS / Flatcar | Ignition-Config | `.ign` | `ignition` |
| Proxmox VE | Answer-Datei | `.toml` unter `proxmox/` | `proxmox` |
| Alles andere | raw | beliebig | `generic` |

Der Typ wird aus der Datei-Endung und dem Distro-Verzeichnis abgeleitet, in dem die Datei liegt — eine `.xml`-Datei ist unter `windows/` also ein autounattend-Dokument, unter `opensuse/` aber ein AutoYaST-Profil. Der Typ steuert das Label im UI, den `Content-Type`-Header beim Ausliefern und den Kernel-Parameter, den das Boot-Menü hinzufügt:

| Typ | Vom Boot-Menü hinzugefügter Kernel-Parameter | `Content-Type` |
|------|------------------------------------------|----------------|
| `preseed` | `auto=true priority=critical url=<url>` | `text/plain` |
| `kickstart` | `inst.ks=<url>` | `text/plain` |
| `autoinstall` | `autoinstall ds=nocloud-net;s=<seed-url>/` | `text/yaml` |
| `autounattend` | keiner (wimboot lädt sie als zusätzliche `initrd`, siehe [Windows-Hinweise](#windows-hinweise)) | `application/xml` |
| `autoyast` | `autoyast=<url>` | `application/xml` |
| `agama` | `inst.auto=<url>` | `application/json` |
| `ignition` | `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` (Fedora CoreOS) oder `flatcar.first_boot=1 ignition.config.url=<url>` (Flatcar) | `application/json` |
| `proxmox` | keiner (der Installer holt die Answer-Datei selbst, siehe [Proxmox VE](#proxmox-ve-answer-datei)) | `application/toml` |
| `generic` | keiner | `text/plain` |

`<url>` ist `http://<server>:8080/autoinstall/<image-filename>?mac=<mac>`. Eine `generic`-Datei wird auf Anfrage ausgeliefert, aber kein Installer erfährt davon; wähle eines der spezifischen Formate, wenn das Boot-Menü sie an den Installer durchreichen soll.

## Die Dateibibliothek

Alle Auto-Install-Dateien liegen unter `data/autoinstall/<distro>/<filename>`:

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

Das Segment `<distro>` muss zu einer bekannten Distro-Profil-ID passen (siehe [Distro-Profile](distro-profiles.md)). Das Verzeichnis wird beim ersten Start automatisch angelegt.

### Dateien über das UI hinzufügen

Im Tab **Auto-Install** → **New File** öffnet sich der Editor mit einem Distro-Picker, einem Dateinamen-Feld und einer Syntax-freundlichen Textarea. **Upload File** nimmt eine beliebige lokale Datei und legt sie im gewählten Distro-Ordner ab.

### Dateien manuell hinzufügen

Einfach reinwerfen:

```bash
mkdir -p data/autoinstall/ubuntu
cp my-autoinstall.yaml data/autoinstall/ubuntu/default.yaml
```

Sie tauchen sofort im UI auf — kein Neustart, kein Scan.

## Dateien anhängen

Auto-Install-Dateien tun nichts, bis du sie verdrahtest. Es gibt drei Stellen zum Anhängen:

### Image (Default)

Tab **Images** → **Eigenschaften** eines Images öffnen → Abschnitt **Auto-Install** → Datei wählen. Jeder Client, der dieses Image bootet, bekommt diese Config, sofern nichts Spezifischeres überschreibt.

### Client (Per-Maschine-Override)

Tab **Clients** → Client öffnen → Dropdown **Auto-Install File**. Nutze das, wenn eine bestimmte Maschine eine andere Config braucht (z.B. ein Build-Server vs. der Rest der Schreibtisch-Flotte).

### Client-Gruppe (Per-Flotten-Override)

Tab **Groups** → Gruppe öffnen → **Auto-Install File**. Gilt für jeden Client in der Gruppe. Nützlich für "alle Workstations in Lab 3"-Szenarien.

## Auflösungsreihenfolge

Wenn ein Client seine Auto-Install-Datei anfordert, läuft Bootimus diese Hierarchie ab:

```
1. Per-Client-Override        (Client.AutoInstallFile)
2. Per-Group-Override         (ClientGroup.AutoInstallFile, falls Client in einer Gruppe)
3. Image-Default              (Image.AutoInstallFile)
4. Inline-Legacy-Skript       (Image.AutoInstallScript — Pre-0.1.58-Setups)
5. → 404 (kein Auto-Install konfiguriert)
```

Der erste nicht-leere Treffer gewinnt. Der Endpunkt loggt die Quelle, von der er ausgeliefert hat:

```
Served auto-install script for ubuntu-24.04-live-server-amd64.iso \
  (source: client:b4:2e:99:01:5f:a3, type: autoinstall, size: 1247 bytes)
```

## Platzhalter

Diese Tokens werden beim Ausliefern pro Client ersetzt:

| Token | Wird ersetzt durch |
|-------|---------------|
| `{{MAC}}` | Client-MAC-Adresse (lowercase, mit Doppelpunkten); bei Proxmox VE die erste Interface-MAC aus dem Request des Installers, die zu einem bekannten Client passt, sonst das erste aufgeführte Interface |
| `{{CLIENT_NAME}}` | Anzeigename aus der Clients-Tabelle |
| `{{HOSTNAME}}` | Wie `{{CLIENT_NAME}}` (Alias zur Klarheit in Configs) |
| `{{CLIENT_GROUP}}` | Name der Client-Gruppe, oder leer, wenn der Client keine hat |
| `{{IP}}` | Client-IP, die den Request gestellt hat |
| `{{SERVER_ADDR}}` | Adresse des Bootimus-Servers |
| `{{SERVER_PORT}}` | HTTP-Boot-Port von Bootimus |
| `{{SERVER_URL}}` | `http://<SERVER_ADDR>:<SERVER_PORT>`, dieselbe Basis-URL, die das Boot-Menü verwendet |
| `{{IMAGE_NAME}}` | Anzeigename des bootenden Images |
| `{{IMAGE_FILENAME}}` | ISO-Dateiname des bootenden Images |
| `{{IMAGE_URL}}` | URL des ISO, wie es unter `/isos/` ausgeliefert wird; derselbe Wert, den das Boot-Menü als `iso-url=` übergibt |
| `{{CALLBACK_URL}}` | Vollständige URL, die der Installer zum Melden des Abschlusses aufruft, inklusive Einmal-Token (siehe [Installationsabschluss melden](#installationsabschluss-melden)) |
| `{{INSTALL_TOKEN}}` | Der nackte Token hinter `{{CALLBACK_URL}}`, um einen eigenen Request zu bauen |
| `{{VAR.<name>}}` | Die Client- oder Gruppenvariable namens `<name>` (siehe [Variablen](#variablen)) |

Platzhalter sind plain String-Substitution — kein Escaping. Quote sie passend zum Zielformat (XML, YAML etc.). Ein Platzhalter ohne Wert bleibt genau so stehen, wie er geschrieben ist, und wird beim Ausliefern als unaufgelöst geloggt; mit der [Vorschau](#vorschau) findest du solche Fälle, bevor eine Maschine bootet.

## Variablen

Die eingebauten Platzhalter decken die Identität des Clients ab, aber die meisten Flotten brauchen auch Werte, die Bootimus nicht kennen kann: eine Rolle, eine Rack-Nummer, ein Disk-Device, eine Mirror-URL. Dafür gibt es Variablen. Jeder Client und jede Client-Gruppe trägt eine Menge benannter String-Werte, und jeder davon steht in Auto-Install-Dateien als `{{VAR.<name>}}` zur Verfügung.

- Namen bestehen nur aus Buchstaben, Ziffern und Unterstrichen (`role`, `rack_id`, `DISK`); alles andere lehnt der Server beim Speichern des Clients oder der Gruppe ab.
- **Gruppenvariablen sind Defaults** für jedes Mitglied. **Client-Variablen überschreiben** sie Name für Name: Eine Gruppe kann `role: worker` setzen und eine einzelne Maschine `role: control-plane`, während sie das `rack` der Gruppe weiterhin erbt.
- Ein `{{VAR.<name>}}`-Token ohne Wert auf beiden Ebenen bleibt unaufgelöst.

Gesetzt werden sie im UI unter **Clients** → Client öffnen → **Auto-Installations-Variablen** oder **Gruppen** → Gruppe öffnen → **Auto-Installations-Variablen**: Jede Zeile ist ein Name und ein Wert, mit **Variable hinzufügen** und einem Entfernen-Button pro Zeile. Über die API ist `variables` ein Objekt aus Strings sowohl im Client- als auch im Gruppen-Payload:

```bash
# Gruppen-Defaults
curl -u admin:pw -X PUT "http://localhost:8081/api/client-groups/update?id=3" \
  -H "Content-Type: application/json" \
  -d '{"name":"lab-3","variables":{"role":"worker","rack":"3","disk":"/dev/sda"}}'

# Eine Maschine überschreibt einen einzelnen Wert
curl -u admin:pw -X PUT "http://localhost:8081/api/clients?mac=b4:2e:99:01:5f:a3" \
  -H "Content-Type: application/json" \
  -d '{"variables":{"role":"control-plane"}}'
```

`"variables": null` in einem Client-Update löscht sie; fehlt der Schlüssel, bleiben sie unverändert. `GET /api/clients` und `GET /api/client-groups` liefern sie unter demselben Schlüssel zurück.

Ein Kickstart, der beide Ebenen nutzt:

```
network --bootproto=dhcp --hostname={{HOSTNAME}}
ignoredisk --only-use={{VAR.disk}}
%post
echo "{{VAR.role}} in rack {{VAR.rack}} ({{CLIENT_GROUP}})" > /etc/machine-role
%end
```

Der CSV-Import unter **Clients** transportiert keine Variablen; bestehende Variablen bleiben erhalten, wenn eine CSV-Zeile einen Client aktualisiert.

## Vorschau

Der Editor unter **Auto-Install** hat eine **Vorschau**-Aktion für jede gespeicherte Datei. Wähle einen Client und optional ein Image, und das Panel zeigt genau den Text, den Bootimus an diese Maschine ausliefern würde — schreibgeschützt, mit einer Warnung, die jeden unaufgelöst gebliebenen Platzhalter auflistet. Ohne Client werden Client- und Gruppenwerte leer gerendert; ohne Image bleiben die `IMAGE_*`-Platzhalter unaufgelöst. `{{CALLBACK_URL}}` und `{{INSTALL_TOKEN}}` werden mit dem festen Token `preview` gerendert, weil kein Installations-Eintrag angelegt wird.

Dasselbe Rendering gibt es über die API:

```bash
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"
```

```json
{"success":true,"data":{"content":"#cloud-config\n...","unresolved":["VAR.disk"]}}
```

`mac` und `image` sind optional. Rendern schreibt nie etwas: Es legt keinen Installations-Eintrag an und bewegt keine Installation durch ihre Zustände.

## Validierung

Wenn eine Datei aus dem Editor gespeichert oder hochgeladen wird, prüft Bootimus sie vor dem Schreiben anhand der Erweiterung auf Wohlgeformtheit:

| Erweiterung | Prüfung |
|-----------|-------|
| `.yaml`, `.yml` | Parst als YAML (Multi-Dokument-Dateien sind erlaubt) |
| `.xml` | Wohlgeformtes XML |
| `.json` | Gültiges JSON |
| alles andere | Nicht geprüft |

Jeder Platzhalter wird zuerst durch einen harmlosen Dummy-Wert ersetzt, sodass `hostname: {{HOSTNAME}}` durchgeht, obwohl ein nacktes `{{HOSTNAME}}` für sich kein gültiges YAML ist. Ein Fehlschlag antwortet mit `400`, der Meldung des Parsers und — wo der Parser sie liefert — der Zeilennummer; der Editor zeigt sie inline unter dem Inhalt und bietet **Trotzdem speichern** für die Fälle, in denen du es besser weißt als der Parser (etwa eine Datei, die absichtlich ein Fragment ist). Über die API übergibst du `"force": true` im Save-Body oder `force=true` als Multipart-Feld beim Upload, um die Prüfung zu überspringen.

Die Substitution passiert am Auto-Install-Endpunkt und gilt damit für jedes Format. Auch Windows-`autounattend.xml` wird für den bootenden Client gerendert, weil wimboot sie beim Boot von genau diesem Endpunkt holt (siehe [Windows-Hinweise](#windows-hinweise)); nur die dort beschriebene ungerenderte SMB-Fallback-Kopie umgeht sie.

## Beispiele

### Ubuntu Server (cloud-init)

`data/autoinstall/ubuntu/default.yaml`:

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

Bootimus fügt den Boot-Parameter selbst hinzu, sobald eine Auto-Install-Datei angehängt ist; in den Boot-Parametern des Images musst du also nichts eintragen. Der erzeugte Parameter sieht so aus:

```
autoinstall ds=nocloud-net;s=http://<server>:8080/autoinstall/<image-filename>/mac/<mac>/
```

### Debian (preseed)

`data/autoinstall/debian/server.cfg`:

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

`data/autoinstall/rocky/workstation.ks`:

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

`data/autoinstall/windows/kiosk.xml`: Standard-`<unattend>`-Dokument — siehe [Microsofts autounattend-Referenz](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/). Platzhalter funktionieren in jedem Text-Node, wenn der Client über wimboot bootet — so bootet Bootimus Windows-Images:

```xml
<ComputerName>{{HOSTNAME}}</ComputerName>
```

### openSUSE Leap 15 / SLES 15 (AutoYaST)

`data/autoinstall/opensuse/leap15.xml`: ein Standard-AutoYaST-Profil — siehe den [AutoYaST-Leitfaden](https://doc.opensuse.org/projects/autoyast/). Bootimus fügt `autoyast=<url>` zur Kernel-Zeile hinzu. Das openSUSE-Profil übergibt bereits `install=` mit Verweis auf die extrahierte ISO — das bringt das Netzwerk hoch und stellt das Installations-Repository bereit.

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

`data/autoinstall/opensuse/leap16.jsonnet` (oder `.json`): ein [Agama-Profil](https://agama-project.github.io/docs/user/profile). Bootimus fügt `inst.auto=<url>` hinzu; mehr braucht Agama auf der Kommandozeile für eine unbeaufsichtigte Installation nicht.

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

Agama versteht nur `inst.auto`; wenn dein Leap-16- oder Tumbleweed-Medium Agama bootet und du eine `.xml`-Datei anhängst, übergibt Bootimus `autoyast=` und der Installer ignoriert es. Behalte AutoYaST-Profile für Leap-15- und SLES-15-Medien.

### Fedora CoreOS / Flatcar (Ignition)

`data/autoinstall/coreos/worker.ign` oder `data/autoinstall/flatcar/worker.ign`: ein [Ignition](https://coreos.github.io/ignition/)-JSON-Dokument, üblicherweise aus Butane erzeugt. Bootimus fügt `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` für Fedora CoreOS und `flatcar.first_boot=1 ignition.config.url=<url>` für Flatcar hinzu. Das `coreos`-Profil übergibt außerdem `coreos.live.rootfs_url=` mit Verweis auf die aus der Live-ISO extrahierte `rootfs.img`.

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

Damit bootet das Live-System mit deiner Config. Um auf die Platte zu installieren, ergänze `coreos.inst.install_dev=/dev/sda coreos.inst.ignition_url={{BASE_URL}}/autoinstall/{{FILENAME}}?mac={{MAC}}` in den Boot-Parametern des Images, damit `coreos-installer` dieselbe Config ins installierte System schreibt. Ignition wendet die Config trotzdem zuerst auf die Live-Umgebung an; das passt also für Configs, die Benutzer, Schlüssel und Dateien anlegen — eine Config, die Platten partitioniert oder formatiert, scheitert im Live-System und sollte nur über `coreos.inst.ignition_url` ausgeliefert werden.

### Proxmox VE (Answer-Datei)

Proxmox VE installiert aus einer `answer.toml` ([Automated Installation](https://pve.proxmox.com/wiki/Automated_Installation)). Der Standard-Installer liest keine Answer-Datei-URL von der Kernel-Kommandozeile, daher muss die ISO einmal mit `proxmox-auto-install-assistant` vorbereitet werden, damit sie die Antwort per HTTP von Bootimus holt:

1. Installiere auf einem beliebigen Debian- oder Proxmox-Host `proxmox-auto-install-assistant` und remastere die ISO; die Fetch-URL zeigt dabei auf den Bootimus-Auto-Install-Endpunkt für den Dateinamen, den du gleich hochlädst:

   ```bash
   proxmox-auto-install-assistant prepare-iso proxmox-ve_9.1-1.iso \
     --fetch-from http \
     --url "http://<server>:8080/autoinstall/proxmox-ve_9.1-1-auto-from-http.iso"
   ```

   Der Assistent legt `proxmox-ve_9.1-1-auto-from-http.iso` neben die Quelle. Die URL muss den Dateinamen der ISO enthalten, die du hochlädst — beide müssen also zusammenpassen.

2. Lade die remasterte ISO über **Images** hoch. Das `proxmox`-Profil extrahiert `/boot/linux26` und `/boot/initrd.img`, und das Boot-Menü lädt die ISO selbst als zweite initrd (`proxmox.iso`) — so liefert auch der offizielle iPXE-Ablauf das Installationsmedium.

3. Lege `data/autoinstall/proxmox/answer.toml` an und hänge sie an das Image, einen Client oder eine Gruppe:

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

Beim Boot `POST`et der Installer eine JSON-Beschreibung der Maschine (Produkt, ISO-Release, DMI-Daten und `network_interfaces` mit jeweils `link` und `mac`) an die URL. Bootimus liest die Interface-MACs, löst den Client über die erste auf, die zu einem bekannten Client passt (sonst greift der Image-Default), rendert die angehängte `.toml` und liefert sie als `application/toml` zurück. `{{MAC}}` ist die MAC, die gepasst hat. Der Abschnitt `[post-installation-webhook]` lässt den Installer nach Abschluss der Installation `{{CALLBACK_URL}}` aufrufen, sodass der Install-Eintrag des Clients nach `completed` wechselt.

Für diesen Typ wird nichts zur Kernel-Kommandozeile hinzugefügt. Ein Kernel-Parameter `proxmox-answer=<url>` wurde upstream angefragt ([Proxmox-Bug 8219](https://bugzilla.proxmox.com/show_bug.cgi?id=8219)), ist aber noch nicht ausgeliefert; sobald das passiert, entfällt der Remastering-Schritt.

## Nicht unterstützt

Manche Distributionen haben keine Möglichkeit, eine Datei für die unbeaufsichtigte Installation von der Kernel-Kommandozeile aufzugreifen — Bootimus tut also nicht so, als würde es sie unterstützen. Dateien für diese Distros werden weiterhin als `generic` ausgeliefert, aber nichts teilt dem Installer etwas darüber mit:

- **Alpine Linux** — `setup-alpine -f <answerfile>` ist ein Befehl, den du nach dem Boot ausführst; der Installer hat keinen Kernel-Parameter-Hook, um eine Datei zu holen.
- **NixOS** — die Installation läuft über `nixos-install` mit einer Konfiguration, die du auf das Ziel schreibst; die Live-ISO hat keinen unbeaufsichtigten Installer.
- **FreeBSD** — `bsdinstall`-Skripte werden vom lokalen Medium oder aus `BSDINSTALL_SCRIPT` in der Umgebung gelesen, nicht von einer URL auf der Loader-Kommandozeile.
- **Arch Linux** — die Live-ISO hat keinen Installer; `archinstall` kann eine Config-Datei nehmen, die aber von Hand oder aus einem eigenen Profil geholt und ausgeführt werden muss.

Boote für diese stattdessen das Live-Image und starte den Installer aus einer SSH-Session oder einem eigenen Skript.

## Installationsabschluss melden

Bootimus verfolgt jede unbeaufsichtigte Installation ab dem Moment, in dem der Client den Kernel des Images lädt. Der Eintrag durchläuft vier Zustände:

| Zustand | Gesetzt, wenn |
|---------|---------------|
| `booted` | Der Client hat die Boot-Dateien des Images geholt und das Image hat eine Auto-Install-Datei oder ein Inline-Skript |
| `installing` | Der Installer hat die Auto-Install-Datei geholt |
| `completed` | Das installierte System hat `{{CALLBACK_URL}}` aufgerufen |
| `failed` | Der Callback hat `status=failed` gemeldet, oder innerhalb des Timeouts kam keine Rückmeldung |

Der aktuelle Zustand steht in der Spalte **Install** der Clients-Tabelle, die vollständige Historie unter **Install History** im Client-Editor. Installationen, die sich nie melden, werden nach sechs Stunden als fehlgeschlagen markiert; ändere das mit `--install-timeout-hours` oder `install_timeout_hours` in `bootimus.yaml`. Jeder Übergang nach `completed` oder `failed` löst ein `install.completed`- bzw. `install.failed`-Webhook-Event aus, wenn dieses Event unter **Settings → Webhook** aktiviert ist.

Der Callback ist ein einfacher HTTP-Request, `GET` oder `POST`, an den Boot-Port:

```
http://<server>:8080/install/callback?token=<token>&status=completed|failed&message=<optionaler Text>
```

`{{CALLBACK_URL}}` enthält den Token bereits, ein Skript muss also nur `&status=failed&message=...` anhängen. Nur die erste Meldung pro Token zählt; spätere Aufrufe antworten mit `already recorded`.

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

Das Modul `phone_home` meldet sich nach dem ersten Boot, was das zuverlässigste Signal ist:

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

Oder melde aus der Installer-Umgebung mit `late-commands`:

```yaml
  late-commands:
    - curtin in-target --target=/target -- curl -fsS "{{CALLBACK_URL}}"
```

### Windows (`autounattend.xml`)

Füge unter `Microsoft-Windows-Shell-Setup` einen First-Logon-Befehl hinzu:

```xml
<FirstLogonCommands>
  <SynchronousCommand wcm:action="add">
    <Order>1</Order>
    <CommandLine>powershell -NoProfile -Command "Invoke-WebRequest -UseBasicParsing '{{CALLBACK_URL}}'"</CommandLine>
    <Description>Report install to Bootimus</Description>
  </SynchronousCommand>
</FirstLogonCommands>
```

Denke daran, dass `&` in XML-Attributen oder -Text als `&amp;` geschrieben werden muss, wenn du `status`- oder `message`-Parameter anhängst.

### Talos- und Flatcar-Nodes

Kubernetes-Nodes, die über den Tab **Kubernetes** aufgenommen wurden, melden sich bereits über den Machine-Config-Ablauf und die Events `node.install_started` / `node.install_completed`. Sie brauchen keinen Callback.

### Historie abfragen

```bash
# Historie eines Clients
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/installs?mac=b4:2e:99:01:5f:a3"

# Aktuelle Installationen der gesamten Flotte
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/installs?limit=50"
```

## Windows-Hinweise

Windows-Images booten über [wimboot](https://ipxe.org/wimboot), das jede zusätzliche `initrd`-Datei aus dem iPXE-Skript in die WinPE-RAM-Disk injiziert. Bootimus nutzt das, um die Auto-Install-Datei pro Client auszuliefern:

1. Wenn für den bootenden Client eine autounattend-Datei aufgelöst wird (über die übliche [Auflösungsreihenfolge](#auflösungsreihenfolge): Client, dann Gruppe, dann Image), fügt der generierte iPXE-Boot-Abschnitt vor `boot.wim` die Zeile `initrd http://<server>:<port>/autoinstall/<iso>?mac=<mac> AutoUnattend.xml` ein.
2. Der Auto-Install-Endpunkt rendert die Datei mit den Platzhaltern des Clients, genau wie für Linux-Installer.
3. wimboot legt die Datei in WinPE unter `X:\Windows\System32\AutoUnattend.xml` ab.
4. Die gepatchte `startnet.cmd` findet sie dort und startet `setup.exe /unattend:X:\Windows\System32\AutoUnattend.xml`.

Weil die Datei pro Client aufgelöst und gerendert wird, funktionieren Per-Client- und Per-Gruppen-Overrides sowie Platzhalter wie `{{HOSTNAME}}` unter Windows genauso wie überall sonst. Boot-Parameter werden weiterhin an wimboot durchgereicht; vermeide `rawwim`, das schaltet die Datei-Injektion ab.

**SMB-Fallback.** Wenn `boot.wim` für ein Image gepatcht wird, an dem auf Image-Ebene eine autounattend-Datei hängt, stellt Bootimus diese Datei zusätzlich ungerendert als `AutoUnattend.xml` auf der SMB-Freigabe des Images bereit. `startnet.cmd` kopiert sie nur dann nach `X:\AutoUnattend.xml`, wenn wimboot nichts injiziert hat, etwa weil der Client die gepatchte `boot.wim` auf einem anderen Weg als über das Bootimus-iPXE-Menü gebootet hat. Der Fallback kennt keine Platzhalter-Substitution und keine Per-Client- oder Per-Gruppen-Overrides: Platzhalter darin erreichen Setup wörtlich.

Ohne autounattend-Datei auf irgendeiner Ebene läuft Setup wie gehabt interaktiv.

**Anforderungen an das Datenverzeichnis.** Bootimus betreibt einen eigenen `smbd`, dessen Zustand unter `data/smb` liegt. Samba startet nicht, wenn dieses Verzeichnis auf einem Dateisystem liegt, das keine Unix-Berechtigungen speichern kann (exFAT, NTFS oder ein CIFS-Mount), weil sein Lock-Verzeichnis dann als `0777` statt `0755` zurückgemeldet wird. Lass das Datenverzeichnis auf einem nativen Linux-Dateisystem wie ext4 oder XFS liegen und stell sicher, dass kein anderer SMB-Server auf dem Host Port 445 belegt. Bootimus prüft beides beim Start und meldet es unter **Server Info** → **Windows SMB**.

**Reboot-Resilienz.** WinPE startet mitten in der Installation neu und verbindet sich von derselben Client-IP wieder. Die mitgelieferte Samba-Config setzt `reset on zero vc = yes` und deaktiviert Oplocks, damit das zweite `net use` nicht an einem stale Session-State scheitert. Wenn du `data/smb/smb.conf` durch deine eigene ersetzt hast, übernimm diese Einstellungen.

**Patch rückgängig machen.** Beim ersten SMB-Patch wird eine unveränderte Kopie der `boot.wim` als `boot.wim.orig` daneben abgelegt. **Images** → **SMB-Patch entfernen** stellt das Original wieder her und entfernt die SMB-Freigabe des Images — die ISO bootet dann exakt wie hochgeladen. Images, die von älteren Bootimus-Versionen gepatcht wurden, haben keine Sicherung: erst das Image neu extrahieren (das erzeugt eine frische unveränderte Kopie), dann den Patch entfernen.

## REST-API

Alles im UI ist auch ein REST-Aufruf.

```bash
# Alle Auto-Install-Dateien auflisten
curl -u admin:pw http://localhost:8081/api/autoinstall-files

# Datei lesen
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/get?distro=ubuntu&filename=default.yaml"

# Datei anlegen oder überschreiben (antwortet 400 bei fehlerhaftem YAML, XML oder JSON; "force":true speichert trotzdem)
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/save \
  -H "Content-Type: application/json" \
  -d '{"distro":"ubuntu","filename":"default.yaml","content":"#cloud-config\n..."}'

# Datei für einen Client und ein Image rendern, unaufgelöste Platzhalter auflisten
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"

# Datei hochladen
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/upload \
  -F "distro=windows" \
  -F "filename=kiosk.xml" \
  -F "file=@./kiosk.xml"

# Herunterladen
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/download?distro=ubuntu&filename=default.yaml" -o default.yaml

# Löschen
curl -u admin:pw -X POST "http://localhost:8081/api/autoinstall-files/delete?distro=ubuntu&filename=default.yaml"
```

Datei an ein Image anhängen:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/images/update \
  -H "Content-Type: application/json" \
  -d '{"filename":"ubuntu-24.04-live-server-amd64.iso","auto_install_file":"ubuntu/default.yaml"}'
```

Datei an einen Client anhängen:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/clients/b4:2e:99:01:5f:a3 \
  -H "Content-Type: application/json" \
  -d '{"auto_install_file":"ubuntu/lab-bench.yaml"}'
```

Den Auto-Install-Endpunkt, den Clients beim Boot ansprechen:

```
GET  /autoinstall/<image-filename>?mac=<mac>             # preseed, kickstart, autoyast, agama, ignition
GET  /autoinstall/<image-filename>/mac/<mac>/user-data   # Ubuntu autoinstall
GET  /autoinstall/<image-filename>/mac/<mac>/meta-data   # Ubuntu autoinstall
POST /autoinstall/<image-filename>                       # Proxmox VE (JSON system information in the body)
```

Das Boot-Menü baut diese URLs mit der MAC-Adresse des Clients, damit Per-Client-Overrides korrekt aufgelöst werden; bei Ubuntu hängt cloud-init `user-data` und `meta-data` selbst an. Der Proxmox-Installer kennt die MAC nicht, mit der Bootimus ihn gebootet hat, daher `POST`et er seine Netzwerk-Interfaces und Bootimus gleicht sie mit der Clients-Tabelle ab. `GET`-Anfragen ohne MAC-Adresse oder in anderer Form, etwa `/autoinstall/<image-filename>/user-data`, liefern 404.

## Fehlersuche

### 404 von `/autoinstall/...`

`no auto-install configuration for this image/client` — auf keiner Ebene der Auflösungskette ist etwas angehängt. Entweder hänge eine Datei an das Image, den Client oder seine Gruppe — oder prüfe, ob `auto_install_file` tatsächlich auf eine existierende Datei unter `data/autoinstall/` zeigt.

### Platzhalter werden wörtlich ausgespielt

Wenn `{{HOSTNAME}}` als literaler String im installierten System auftaucht, wurde die Datei vor dem Substitutions-Lauf ausgeliefert — meistens, weil der Client nur per IP gebootet hat und der Request keinen `mac`-Query-Parameter enthielt. Stelle sicher, dass das Boot-Menü URLs der Form `/autoinstall/<iso>?mac=<mac>` oder `/autoinstall/<iso>/mac/<mac>/` erzeugt.

Unter Windows bedeuten wörtliche Platzhalter, dass Setup die ungerenderte Kopie von der SMB-Freigabe statt der von wimboot injizierten Datei benutzt hat. Zeige das Boot-Menü des Clients in der Vorschau an (siehe [Boot-Menü eines Clients in der Vorschau](clients.md#previewing-a-clients-boot-menu)) und prüfe, dass der Windows-Abschnitt eine Zeile `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml` enthält und die Boot-Parameter des Images kein `rawwim` enthalten.

Ein literal gebliebenes `{{VAR.<name>}}` bedeutet, dass weder der Client noch seine Gruppe diese Variable definiert oder der Name anders geschrieben ist (Namen sind case-sensitive). Jeder unaufgelöste Platzhalter wird beim Ausliefern geloggt, und die **Vorschau** im Editor listet sie für einen gewählten Client auf, bevor irgendetwas bootet.

### Falsche Datei ausgeliefert

Die Auflösung läuft nach Most-Specific-First. Wenn ein Client einen eigenen Override hat und du das nicht erwartest, ist das der Grund, warum das Image-Level-Default nicht greift. Prüfe die Server-Log-Zeile:

```
Served auto-install script for ... (source: client:..., type: ..., size: ...)
```

Das Feld `source:` sagt dir genau, welcher Slot gewonnen hat.

### Proxmox-Installer meldet, dass er die Answer-Datei nicht holen kann

Die remasterte ISO holt von genau der URL, die `prepare-iso` eingebacken hat. Prüfe, dass der Dateiname in dieser URL zur hochgeladenen ISO passt (die ISO nach dem Remastering umzubenennen bricht das), dass der Client Port 8080 erreicht und dass auf irgendeiner Ebene der Auflösungskette eine `.toml`-Datei angehängt ist. Das Server-Log zeigt den `POST` und die aufgelöste Quelle; ein `400` bedeutet, dass der Body keine `network_interfaces` enthielt, was auf einen sehr alten Installer hindeutet.

### Windows-Setup läuft interaktiv

- Für den bootenden Client muss eine autounattend-Datei aufgelöst werden: hänge eine ans Image (Image-Eigenschaften → Auto-Install), an den Client oder an seine Gruppe.
- Zeige das Boot-Menü des Clients in der Vorschau an und prüfe, dass der Windows-Abschnitt die Zeile `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml` enthält. Fehlt sie, wurde für diese MAC keine autounattend-Datei aufgelöst.
- Stelle sicher, dass die Boot-Parameter des Images kein `rawwim` enthalten; das verhindert, dass wimboot Dateien in WinPE injiziert.
- `boot.wim` nach einem Bootimus-Upgrade neu patchen, damit `startnet.cmd` die injizierte Datei sucht: **Images** → **Patch SMB** (oder beim nächsten Boot wird automatisch neu gepatcht).
- Erreichbarkeit der SMB-Freigabe vom Client prüfen (`net view \\<server>` aus WinPE); der Installer selbst kommt weiterhin von der Freigabe.

### "AutoUnattend.xml neither injected by wimboot nor staged on the share, running interactive setup"

Wird von `startnet.cmd` geloggt, wenn am Image eine autounattend-Datei hängt, aber keiner der beiden Auslieferungswege eine Datei geliefert hat. Prüfe im Bootimus-Log den `/autoinstall/<iso>?mac=...`-Request des Clients zum Boot-Zeitpunkt (ein 404 heißt, dass für diesen Client keine Datei aufgelöst wurde) und den Staging-Schritt um den Patch-Zeitpunkt herum. SMB-Patch aus den Image-Eigenschaften erneut ausführen.

### `Z:\setup.exe not found on the share`

`startnet.cmd` hat `Z:` eingebunden, aber die ausgegebene Auflistung zeigt den Image-Ordner (`iso\`, `metadata.txt`) statt der Installationsdateien. Bootimus gibt immer den `iso`-Unterordner des Images frei, der Client hat sich also mit einem anderen SMB-Server unter derselben Adresse verbunden. Prüfe das Bootimus-Log auf `SMB: smbd exited` und die Zeile **Windows SMB** unter **Server Info**. Die übliche Ursache ist ein Datenverzeichnis auf einem Dateisystem ohne Unix-Berechtigungen; `smbd` beendet sich dann mit `invalid permissions on directory '.../smb/locks/msg.lock': has 0777 should be 0755`, und stattdessen antwortet ein System-Samba auf Port 445. Verschiebe das Datenverzeichnis auf ext4 oder XFS, stoppe das andere Samba und starte Bootimus neu.

### `net use fails after VM reboot`

In 0.1.58 behoben, indem `reset on zero vc = yes` in der mitgelieferten Samba-Config aktiviert wurde. Wenn du eine eigene `smb.conf` pflegst, ergänze:

```
reset on zero vc = yes
oplocks = no
kernel oplocks = no
level2 oplocks = no
strict locking = no
deadtime = 1
```

## Nächste Schritte

- Siehe [Image-Verwaltung](images.md), um Dateien an Images zu hängen.
- Siehe [Client-Verwaltung](clients.md) für Per-Client-Overrides.
- Siehe [Distro-Profile](distro-profiles.md) für die zugrunde liegenden Profil-IDs, die auf die Bibliotheks-Unterverzeichnisse mappen.
