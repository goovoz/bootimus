# Руководство по автоустановке

Запускайте unattended-установки для Windows, Ubuntu, Debian, дистрибутивов семейства Red Hat, openSUSE, Fedora CoreOS, Flatcar и Proxmox VE. Положите конфиг один раз, прикрепите его к образу — и каждая PXE-загрузка доводит установку до конца без единого нажатия клавиши.

## Оглавление

- [Обзор](#обзор)
- [Поддерживаемые форматы](#поддерживаемые-форматы)
- [Библиотека файлов](#библиотека-файлов)
- [Прикрепление файлов](#прикрепление-файлов)
- [Порядок разрешения](#порядок-разрешения)
- [Плейсхолдеры](#плейсхолдеры)
- [Переменные](#переменные)
- [Предпросмотр](#предпросмотр)
- [Проверка](#проверка)
- [Примеры](#примеры)
- [Не поддерживается](#не-поддерживается)
- [Отчёт о завершении установки](#отчёт-о-завершении-установки)
- [Особенности Windows](#особенности-windows)
- [REST API](#rest-api)
- [Диагностика](#диагностика)

## Обзор

Конфиги автоустановки хранятся в библиотеке по дистрибутивам в `data/autoinstall/`. Вы можете:

- **Управлять файлами в UI** во вкладке **Auto-Install** — создавать, редактировать, загружать, скачивать и удалять конфиги без копания в файловой системе.
- **Задавать дефолт** для каждого образа (каждый клиент, грузящий этот образ, получит одинаковый конфиг).
- **Переопределять для клиента** или **для группы клиентов**, когда одной машине нужен другой конфиг (другой hostname, разметка диска, роль и т. д.).
- **Шаблонизировать** с помощью плейсхолдеров типа `{{HOSTNAME}}` и `{{IP}}`, которые подставляются на лету по личности грузящегося клиента, плюс собственные переменные `{{VAR.name}}`, задаваемые для клиента или группы.
- **Смотреть предпросмотр** результата для любого клиента и образа, а файлы YAML, XML и JSON при сохранении проверяются на корректность синтаксиса.

Bootimus раздаёт отрендеренный файл по HTTP на эндпоинте автоустановки. Linux-установщики получают его через параметр ядра; для Windows wimboot скачивает тот же URL как дополнительный `initrd` и инжектит его в RAM-диск WinPE под именем `AutoUnattend.xml`, чтобы `setup.exe /unattend:` подхватил его автоматически (см. [Особенности Windows](#особенности-windows)).

## Поддерживаемые форматы

| Семейство | Формат | Расширение | Определяется как |
|--------------|--------|----------------|-------------|
| Windows (10/11/Server) | `autounattend.xml` | `.xml` | `autounattend` |
| Ubuntu (Server live, 20.04+) | cloud-init / autoinstall | `.yaml`, `.yml` | `autoinstall` |
| Debian | preseed | `.cfg` | `preseed` |
| Red Hat / Rocky / Fedora / Alma | kickstart | `.ks` | `kickstart` |
| openSUSE Leap 15 / SLES 15 | профиль AutoYaST | `.xml` в `opensuse/` | `autoyast` |
| openSUSE Leap 16 / Tumbleweed / SLES 16 | профиль Agama | `.json`, `.jsonnet` в `opensuse/` | `agama` |
| Fedora CoreOS / Flatcar | конфиг Ignition | `.ign` | `ignition` |
| Proxmox VE | answer-файл | `.toml` в `proxmox/` | `proxmox` |
| Что-то другое | raw | любое | `generic` |

Тип выводится из расширения файла и каталога дистрибутива, в котором он лежит: файл `.xml` под `windows/` — это документ autounattend, а под `opensuse/` — профиль AutoYaST. Тип определяет метку в UI, заголовок `Content-Type` при раздаче файла и параметр ядра, который добавляет загрузочное меню:

| Тип | Параметр ядра, добавляемый загрузочным меню | `Content-Type` |
|------|------------------------------------------|----------------|
| `preseed` | `auto=true priority=critical url=<url>` | `text/plain` |
| `kickstart` | `inst.ks=<url>` | `text/plain` |
| `autoinstall` | `autoinstall ds=nocloud-net;s=<seed-url>/` | `text/yaml` |
| `autounattend` | нет (wimboot загружает его как дополнительный `initrd`, см. [Особенности Windows](#особенности-windows)) | `application/xml` |
| `autoyast` | `autoyast=<url>` | `application/xml` |
| `agama` | `inst.auto=<url>` | `application/json` |
| `ignition` | `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` (Fedora CoreOS) или `flatcar.first_boot=1 ignition.config.url=<url>` (Flatcar) | `application/json` |
| `proxmox` | нет (установщик сам забирает answer-файл, см. [Proxmox VE](#proxmox-ve-answer-файл)) | `application/toml` |
| `generic` | нет | `text/plain` |

`<url>` — это `http://<server>:8080/autoinstall/<image-filename>?mac=<mac>`. Файл `generic` раздаётся по запросу, но ни одному установщику о нём не сообщается; выберите один из конкретных форматов, если хотите, чтобы загрузочное меню передало его установщику.

## Библиотека файлов

Все файлы автоустановки лежат под `data/autoinstall/<distro>/<filename>`:

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

Сегмент `<distro>` должен совпадать с ID известного профиля дистрибутива (см. [Профили дистрибутивов](distro-profiles.md)). Директория создаётся автоматически при первом запуске.

### Добавление файлов через UI

Вкладка **Auto-Install** → **New File** открывает редактор с выбором дистрибутива, полем имени файла и удобной текстовой областью. **Upload File** берёт любой локальный файл и кладёт его в выбранную папку дистрибутива.

### Добавление файлов вручную

Просто положите их:

```bash
mkdir -p data/autoinstall/ubuntu
cp my-autoinstall.yaml data/autoinstall/ubuntu/default.yaml
```

Они сразу появятся в UI — без перезапуска, без сканирования.

## Прикрепление файлов

Файлы автоустановки бесполезны, пока вы их не привяжете. Привязать можно в трёх местах:

### Образ (дефолт)

Вкладка **Images** → откройте **Properties** образа → раздел **Auto-Install** → выберите файл. Каждый клиент, грузящий этот образ, получит этот конфиг — если ничего более специфичного не переопределит.

### Клиент (переопределение для машины)

Вкладка **Clients** → откройте клиента → выпадающий список **Auto-Install File**. Используйте, когда одной конкретной машине нужен другой конфиг (например, сборочный сервер vs остальной парк рабочих столов).

### Группа клиентов (переопределение для парка)

Вкладка **Groups** → откройте группу → **Auto-Install File**. Применяется ко всем клиентам группы. Полезно для сценариев «все рабочие станции в лаборатории 3».

## Порядок разрешения

Когда клиент запрашивает свой файл автоустановки, Bootimus идёт по этой иерархии:

```
1. Переопределение клиента      (Client.AutoInstallFile)
2. Переопределение группы       (ClientGroup.AutoInstallFile, если клиент в группе)
3. Дефолт образа                (Image.AutoInstallFile)
4. Старый инлайн-скрипт         (Image.AutoInstallScript — установки до 0.1.58)
5. → 404 (автоустановка не настроена)
```

Побеждает первое непустое совпадение. Эндпоинт логирует источник, из которого раздал:

```
Served auto-install script for ubuntu-24.04-live-server-amd64.iso \
  (source: client:b4:2e:99:01:5f:a3, type: autoinstall, size: 1247 bytes)
```

## Плейсхолдеры

Эти токены подставляются для каждого клиента в момент раздачи:

| Токен | Заменяется на |
|-------|---------------|
| `{{MAC}}` | MAC-адрес клиента (в нижнем регистре, через двоеточие); для Proxmox VE — MAC первого интерфейса из запроса установщика, совпавшего с известным клиентом, иначе первый интерфейс в списке |
| `{{CLIENT_NAME}}` | Дружественное имя из таблицы Clients |
| `{{HOSTNAME}}` | То же, что `{{CLIENT_NAME}}` (алиас для понятности в конфигах) |
| `{{CLIENT_GROUP}}` | Имя группы клиента или пустая строка, если группы нет |
| `{{IP}}` | IP клиента, отправившего запрос |
| `{{SERVER_ADDR}}` | Адрес сервера Bootimus |
| `{{SERVER_PORT}}` | HTTP-порт загрузки Bootimus |
| `{{SERVER_URL}}` | `http://<SERVER_ADDR>:<SERVER_PORT>` — тот же базовый URL, что использует загрузочное меню |
| `{{IMAGE_NAME}}` | Отображаемое имя грузящегося образа |
| `{{IMAGE_FILENAME}}` | Имя ISO-файла грузящегося образа |
| `{{IMAGE_URL}}` | URL ISO, как он раздаётся из `/isos/`; то же значение загрузочное меню передаёт как `iso-url=` |
| `{{CALLBACK_URL}}` | Полный URL, который установщик вызывает, чтобы сообщить о завершении, включая одноразовый токен (см. [Отчёт о завершении установки](#отчёт-о-завершении-установки)) |
| `{{INSTALL_TOKEN}}` | Сам токен из `{{CALLBACK_URL}}` для сборки собственного запроса |
| `{{VAR.<name>}}` | Переменная клиента или группы с именем `<name>` (см. [Переменные](#переменные)) |

Плейсхолдеры — это обычная подстановка строки, без экранирования. Заключайте их в кавычки соответственно целевому формату (XML, YAML и т. д.). Плейсхолдер без значения остаётся ровно таким, как написан, и при раздаче файла логируется как неразрешённый; используйте [Предпросмотр](#предпросмотр), чтобы заметить такие случаи до загрузки машины.

## Переменные

Встроенные плейсхолдеры покрывают личность клиента, но большинству парков нужны и значения, которых Bootimus знать не может: роль, номер стойки, дисковое устройство, URL зеркала. Этот пробел закрывают переменные. У каждого клиента и каждой группы клиентов есть набор именованных строковых значений, и каждое из них доступно в файлах автоустановки как `{{VAR.<name>}}`.

- Имена состоят только из букв, цифр и подчёркиваний (`role`, `rack_id`, `DISK`); всё остальное сервер отклоняет при сохранении клиента или группы.
- **Переменные группы — значения по умолчанию** для каждого участника. **Переменные клиента переопределяют** их по одному имени: группа может задать `role: worker`, а отдельная машина — `role: control-plane`, при этом по-прежнему наследуя `rack` группы.
- Токен `{{VAR.<name>}}` без значения на обоих уровнях остаётся неразрешённым.

Задаются они в UI: **Clients** → открыть клиента → **Переменные автоустановки**, либо **Groups** → открыть группу → **Переменные автоустановки**: каждая строка — это имя и значение, есть кнопка **Добавить переменную** и кнопка удаления у каждой строки. Через API `variables` — это объект строк и в payload клиента, и в payload группы:

```bash
# Значения по умолчанию группы
curl -u admin:pw -X PUT "http://localhost:8081/api/client-groups/update?id=3" \
  -H "Content-Type: application/json" \
  -d '{"name":"lab-3","variables":{"role":"worker","rack":"3","disk":"/dev/sda"}}'

# Одна машина переопределяет одно значение
curl -u admin:pw -X PUT "http://localhost:8081/api/clients?mac=b4:2e:99:01:5f:a3" \
  -H "Content-Type: application/json" \
  -d '{"variables":{"role":"control-plane"}}'
```

`"variables": null` в обновлении клиента очищает их; если ключа нет, они не меняются. `GET /api/clients` и `GET /api/client-groups` возвращают их под тем же ключом.

Kickstart, использующий оба уровня:

```
network --bootproto=dhcp --hostname={{HOSTNAME}}
ignoredisk --only-use={{VAR.disk}}
%post
echo "{{VAR.role}} in rack {{VAR.rack}} ({{CLIENT_GROUP}})" > /etc/machine-role
%end
```

Импорт CSV на вкладке **Clients** переменные не переносит; существующие переменные сохраняются, когда строка CSV обновляет клиента.

## Предпросмотр

У редактора на вкладке **Auto-Install** есть действие **Предпросмотр** для любого сохранённого файла. Выберите клиента и, при желании, образ — панель покажет ровно тот текст, который Bootimus отдал бы этой машине, только для чтения, с предупреждением, перечисляющим каждый оставшийся неразрешённым плейсхолдер. Без клиента значения клиента и группы рендерятся пустыми; без образа плейсхолдеры `IMAGE_*` остаются неразрешёнными. `{{CALLBACK_URL}}` и `{{INSTALL_TOKEN}}` рендерятся с фиксированным токеном `preview`, потому что запись об установке не создаётся.

Тот же рендеринг доступен через API:

```bash
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"
```

```json
{"success":true,"data":{"content":"#cloud-config\n...","unresolved":["VAR.disk"]}}
```

`mac` и `image` необязательны. Рендеринг ничего не записывает: он не создаёт запись об установке и не переводит установку между состояниями.

## Проверка

Когда файл сохраняется из редактора или загружается, Bootimus перед записью проверяет его синтаксис по расширению:

| Расширение | Проверка |
|-----------|-------|
| `.yaml`, `.yml` | Разбирается как YAML (файлы из нескольких документов допускаются) |
| `.xml` | Корректно сформированный XML |
| `.json` | Валидный JSON |
| всё остальное | Не проверяется |

Сначала каждый плейсхолдер заменяется безвредным фиктивным значением, поэтому `hostname: {{HOSTNAME}}` проходит, хотя голый `{{HOSTNAME}}` сам по себе невалидный YAML. При ошибке возвращается `400` с сообщением парсера и, если парсер его даёт, номером строки; редактор показывает это прямо под содержимым и предлагает **Всё равно сохранить** для случаев, когда вы знаете лучше парсера (например, файл намеренно является фрагментом). Через API передайте `"force": true` в теле сохранения или `force=true` как multipart-поле при загрузке, чтобы пропустить проверку.

Подстановка выполняется на эндпоинте автоустановки, поэтому действует для всех форматов. Windows-`autounattend.xml` тоже рендерится под грузящийся клиент, потому что wimboot скачивает его с этого эндпоинта при загрузке (см. [Особенности Windows](#особенности-windows)); её обходит только описанная там неотрендеренная резервная SMB-копия.

## Примеры

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

Bootimus сам добавляет параметр загрузки, когда к образу привязан файл автоустановки, поэтому в параметры загрузки образа ничего добавлять не нужно. Сгенерированный параметр выглядит так:

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

`data/autoinstall/windows/kiosk.xml`: стандартный документ `<unattend>` — см. [справочник autounattend от Microsoft](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/). Плейсхолдеры работают в любом текстовом узле, когда клиент загружается через wimboot — именно так Bootimus загружает образы Windows:

```xml
<ComputerName>{{HOSTNAME}}</ComputerName>
```

### openSUSE Leap 15 / SLES 15 (AutoYaST)

`data/autoinstall/opensuse/leap15.xml`: стандартный профиль AutoYaST — см. [руководство AutoYaST](https://doc.opensuse.org/projects/autoyast/). Bootimus добавляет `autoyast=<url>` в строку ядра. Профиль openSUSE уже передаёт `install=`, указывающий на извлечённый ISO, — именно это поднимает сеть и предоставляет репозиторий установки.

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

`data/autoinstall/opensuse/leap16.jsonnet` (или `.json`): [профиль Agama](https://agama-project.github.io/docs/user/profile). Bootimus добавляет `inst.auto=<url>`; больше Agama для unattended-установки в командной строке ничего не нужно.

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

Agama понимает только `inst.auto`; если ваш носитель Leap 16 или Tumbleweed загружает Agama, а вы прикрепили файл `.xml`, Bootimus передаст `autoyast=`, и установщик его проигнорирует. Оставьте профили AutoYaST для носителей Leap 15 и SLES 15.

### Fedora CoreOS / Flatcar (Ignition)

`data/autoinstall/coreos/worker.ign` или `data/autoinstall/flatcar/worker.ign`: JSON-документ [Ignition](https://coreos.github.io/ignition/), обычно сгенерированный из Butane. Bootimus добавляет `ignition.config.url=<url> ignition.firstboot ignition.platform.id=metal` для Fedora CoreOS и `flatcar.first_boot=1 ignition.config.url=<url>` для Flatcar. Профиль `coreos` также передаёт `coreos.live.rootfs_url=`, указывающий на `rootfs.img`, извлечённый из live-ISO.

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

Так live-система загружается с применённым конфигом. Чтобы установить на диск, добавьте `coreos.inst.install_dev=/dev/sda coreos.inst.ignition_url={{BASE_URL}}/autoinstall/{{FILENAME}}?mac={{MAC}}` в параметры загрузки образа — тогда `coreos-installer` запишет тот же конфиг в установленную систему. Ignition всё равно сначала применяет конфиг к live-окружению, поэтому это подходит для конфигов, которые создают пользователей, ключи и файлы; конфиг, который размечает или форматирует диски, в live-системе упадёт, и доставлять его следует только через `coreos.inst.ignition_url`.

### Proxmox VE (answer-файл)

Proxmox VE устанавливается из `answer.toml` ([Automated Installation](https://pve.proxmox.com/wiki/Automated_Installation)). Стандартный установщик не читает URL answer-файла из командной строки ядра, поэтому ISO нужно один раз подготовить с помощью `proxmox-auto-install-assistant`, чтобы он забирал ответ у Bootimus по HTTP:

1. На любом хосте Debian или Proxmox установите `proxmox-auto-install-assistant` и пересоберите ISO, указав в URL загрузки эндпоинт автоустановки Bootimus с именем файла, который вы собираетесь загрузить:

   ```bash
   proxmox-auto-install-assistant prepare-iso proxmox-ve_9.1-1.iso \
     --fetch-from http \
     --url "http://<server>:8080/autoinstall/proxmox-ve_9.1-1-auto-from-http.iso"
   ```

   Ассистент записывает `proxmox-ve_9.1-1-auto-from-http.iso` рядом с исходником. URL должен содержать имя файла загружаемого ISO, так что они должны совпадать.

2. Загрузите пересобранный ISO через **Images**. Профиль `proxmox` извлекает `/boot/linux26` и `/boot/initrd.img`, а загрузочное меню подгружает сам ISO как второй initrd (`proxmox.iso`) — именно так официальный сценарий iPXE поставляет установочный носитель.

3. Создайте `data/autoinstall/proxmox/answer.toml` и прикрепите его к образу, клиенту или группе:

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

При загрузке установщик отправляет `POST` с JSON-описанием машины (продукт, релиз ISO, данные DMI и `network_interfaces` с `link` и `mac` каждого интерфейса) на этот URL. Bootimus читает MAC-адреса интерфейсов, разрешает клиента по первому, совпавшему с известным клиентом (иначе откатывается к дефолту образа), рендерит прикреплённый `.toml` и возвращает его как `application/toml`. `{{MAC}}` — это совпавший MAC. Раздел `[post-installation-webhook]` заставляет установщик вызвать `{{CALLBACK_URL}}` по завершении установки, так что запись об установке клиента переходит в `completed`.

Для этого типа в командную строку ядра ничего не добавляется. Параметр ядра `proxmox-answer=<url>` запрошен в апстриме ([Proxmox bug 8219](https://bugzilla.proxmox.com/show_bug.cgi?id=8219)), но пока не вышел; когда выйдет, шаг пересборки станет ненужным.

## Не поддерживается

У некоторых дистрибутивов нет способа подхватить файл unattended-установки из командной строки ядра, поэтому Bootimus не делает вид, что поддерживает их. Файлы для этих дистрибутивов всё равно раздаются как `generic`, но установщику о них ничего не сообщается:

- **Alpine Linux** — `setup-alpine -f <answerfile>` — команда, которую запускают после загрузки; у установщика нет хука в параметрах ядра, чтобы забрать файл.
- **NixOS** — установка выполняется `nixos-install` по конфигурации, которую вы записываете на целевую систему; у live-ISO нет unattended-установщика.
- **FreeBSD** — скрипты `bsdinstall` читаются с локального носителя или из `BSDINSTALL_SCRIPT` в окружении, а не по URL из командной строки загрузчика.
- **Arch Linux** — у live-ISO нет установщика; `archinstall` умеет принимать конфиг-файл, но его нужно скачать и запустить вручную или из собственного профиля.

Для них загрузите live-образ и запустите установщик из SSH-сессии или собственного скрипта.

## Отчёт о завершении установки

Bootimus отслеживает каждую автоматическую установку с момента, когда клиент загружает ядро образа. Запись проходит через четыре состояния:

| Состояние | Устанавливается, когда |
|-----------|------------------------|
| `booted` | Клиент получил загрузочные файлы образа, а у образа есть файл автоустановки или встроенный скрипт |
| `installing` | Установщик получил файл автоустановки |
| `completed` | Установленная система вызвала `{{CALLBACK_URL}}` |
| `failed` | Обратный вызов сообщил `status=failed`, либо в течение таймаута никто не отчитался |

Текущее состояние показывается в столбце **Install** таблицы Clients, а полная история — в разделе **Install History** в редакторе клиента. Установки, которые так и не отчитались, помечаются как неудачные через шесть часов; измените это флагом `--install-timeout-hours` или параметром `install_timeout_hours` в `bootimus.yaml`. Каждый переход в `completed` или `failed` порождает событие вебхука `install.completed` или `install.failed`, если оно включено в **Settings → Webhook**.

Обратный вызов — это обычный HTTP-запрос, `GET` или `POST`, на загрузочный порт:

```
http://<server>:8080/install/callback?token=<token>&status=completed|failed&message=<optional text>
```

`{{CALLBACK_URL}}` уже содержит токен, так что скрипту достаточно дописать `&status=failed&message=...`. Засчитывается только первый отчёт по токену; последующие вызовы отвечают `already recorded`.

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

Модуль `phone_home` отправляет запрос после первой загрузки — это самый надёжный сигнал:

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

Либо отчитайтесь из окружения установщика через `late-commands`:

```yaml
  late-commands:
    - curtin in-target --target=/target -- curl -fsS "{{CALLBACK_URL}}"
```

### Windows (`autounattend.xml`)

Добавьте команду первого входа в `Microsoft-Windows-Shell-Setup`:

```xml
<FirstLogonCommands>
  <SynchronousCommand wcm:action="add">
    <Order>1</Order>
    <CommandLine>powershell -NoProfile -Command "Invoke-WebRequest -UseBasicParsing '{{CALLBACK_URL}}'"</CommandLine>
    <Description>Report install to Bootimus</Description>
  </SynchronousCommand>
</FirstLogonCommands>
```

Помните, что `&` внутри атрибутов или текста XML нужно записывать как `&amp;`, если вы дописываете параметры `status` или `message`.

### Узлы Talos и Flatcar

Узлы Kubernetes, зарегистрированные через вкладку **Kubernetes**, уже отчитываются через процесс машинной конфигурации и события `node.install_started` / `node.install_completed`. Обратный вызов им не нужен.

### Запрос истории

```bash
# История одного клиента
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/installs?mac=b4:2e:99:01:5f:a3"

# Недавние установки по всему парку
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/installs?limit=50"
```

## Особенности Windows

Образы Windows загружаются через [wimboot](https://ipxe.org/wimboot), который инжектит каждый дополнительный файл `initrd` из iPXE-скрипта в RAM-диск WinPE. Bootimus использует это, чтобы доставлять файл автоустановки для каждого клиента отдельно:

1. Когда для грузящегося клиента разрешается autounattend-файл (по обычному [порядку разрешения](#порядок-разрешения): клиент, затем группа, затем образ), сгенерированная секция загрузки iPXE добавляет перед `boot.wim` строку `initrd http://<server>:<port>/autoinstall/<iso>?mac=<mac> AutoUnattend.xml`.
2. Эндпоинт автоустановки рендерит файл с плейсхолдерами клиента, ровно так же, как для Linux-установщиков.
3. wimboot помещает файл в `X:\Windows\System32\AutoUnattend.xml` внутри WinPE.
4. Пропатченный `startnet.cmd` находит его там и запускает `setup.exe /unattend:X:\Windows\System32\AutoUnattend.xml`.

Поскольку файл разрешается и рендерится для каждого клиента, переопределения для клиента и группы и плейсхолдеры вроде `{{HOSTNAME}}` работают в Windows так же, как и везде. Параметры загрузки по-прежнему передаются wimboot; избегайте `rawwim` — он отключает инжект файлов.

**Резервный путь через SMB.** Когда `boot.wim` патчится для образа, к которому на уровне образа прикреплён autounattend-файл, Bootimus дополнительно стейджит этот файл без рендеринга как `AutoUnattend.xml` на SMB-шару образа. `startnet.cmd` копирует его в `X:\AutoUnattend.xml` только если wimboot ничего не инжектил, например когда клиент загрузил пропатченный `boot.wim` каким-то иным путём, а не через iPXE-меню Bootimus. В резервном пути нет ни подстановки плейсхолдеров, ни переопределений для клиента или группы: плейсхолдеры в нём доходят до Setup буквально.

Без autounattend-файла на любом уровне Setup как и раньше работает интерактивно.

**Требования к каталогу данных.** Bootimus запускает собственный `smbd`, состояние которого хранится в `data/smb`. Samba отказывается стартовать, если этот каталог лежит на файловой системе, не умеющей хранить права Unix (exFAT, NTFS или CIFS-монтирование), потому что её каталог блокировок тогда читается как `0777` вместо `0755`. Держите каталог данных на родной файловой системе Linux, например ext4 или XFS, и убедитесь, что никакой другой SMB-сервер на хосте не слушает порт 445. Bootimus проверяет оба условия при запуске и показывает результат в **Server Info** → **Windows SMB**.

**Устойчивость к перезагрузкам.** WinPE перезагружается посреди установки и переподключается с того же IP клиента. В прилагаемом конфиге Samba стоит `reset on zero vc = yes` и отключены oplocks, чтобы второй `net use` не споткнулся об устаревшее состояние сессии. Если вы заменили `data/smb/smb.conf` своим, продублируйте эти настройки.

**Откат патча.** При первом SMB-патче рядом сохраняется нетронутая копия `boot.wim` под именем `boot.wim.orig`. **Images** → **Убрать патч SMB** восстанавливает оригинал и удаляет SMB-шару образа — ISO после этого грузится ровно так, как была загружена. У образов, пропатченных старыми версиями Bootimus, резервной копии нет: сначала повторно извлеките образ (это создаст свежую нетронутую копию), затем уберите патч.

## REST API

Всё, что есть в UI, — это также REST-вызов.

```bash
# Список всех файлов автоустановки
curl -u admin:pw http://localhost:8081/api/autoinstall-files

# Прочитать файл
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/get?distro=ubuntu&filename=default.yaml"

# Создать или перезаписать файл (отвечает 400 при некорректном YAML, XML или JSON; "force":true сохраняет всё равно)
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/save \
  -H "Content-Type: application/json" \
  -d '{"distro":"ubuntu","filename":"default.yaml","content":"#cloud-config\n..."}'

# Отрендерить файл для клиента и образа, перечислив неразрешённые плейсхолдеры
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/render?distro=ubuntu&filename=default.yaml&mac=b4:2e:99:01:5f:a3&image=ubuntu-24.04-live-server-amd64.iso"

# Загрузить файл
curl -u admin:pw -X POST http://localhost:8081/api/autoinstall-files/upload \
  -F "distro=windows" \
  -F "filename=kiosk.xml" \
  -F "file=@./kiosk.xml"

# Скачать
curl -u admin:pw "http://localhost:8081/api/autoinstall-files/download?distro=ubuntu&filename=default.yaml" -o default.yaml

# Удалить
curl -u admin:pw -X POST "http://localhost:8081/api/autoinstall-files/delete?distro=ubuntu&filename=default.yaml"
```

Прикрепить файл к образу:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/images/update \
  -H "Content-Type: application/json" \
  -d '{"filename":"ubuntu-24.04-live-server-amd64.iso","auto_install_file":"ubuntu/default.yaml"}'
```

Прикрепить файл к клиенту:

```bash
curl -u admin:pw -X PUT http://localhost:8081/api/clients/b4:2e:99:01:5f:a3 \
  -H "Content-Type: application/json" \
  -d '{"auto_install_file":"ubuntu/lab-bench.yaml"}'
```

Эндпоинт автоустановки, на который ходят клиенты при загрузке:

```
GET  /autoinstall/<image-filename>?mac=<mac>             # preseed, kickstart, autoyast, agama, ignition
GET  /autoinstall/<image-filename>/mac/<mac>/user-data   # Ubuntu autoinstall
GET  /autoinstall/<image-filename>/mac/<mac>/meta-data   # Ubuntu autoinstall
POST /autoinstall/<image-filename>                       # Proxmox VE (JSON system information in the body)
```

Загрузочное меню формирует эти URL с MAC-адресом клиента, чтобы переопределения для конкретного клиента разрешались правильно; для Ubuntu cloud-init сам добавляет `user-data` и `meta-data`. Установщик Proxmox не знает MAC, с которым Bootimus его загрузил, поэтому он отправляет `POST` со своими сетевыми интерфейсами, а Bootimus сопоставляет их с таблицей Clients. Запросы `GET` без MAC-адреса или в другом виде, например `/autoinstall/<image-filename>/user-data`, возвращают 404.

## Диагностика

### 404 от `/autoinstall/...`

`no auto-install configuration for this image/client` — ни на одном уровне цепочки разрешения ничего не привязано. Прикрепите файл к образу, клиенту или его группе — или проверьте, что `auto_install_file` действительно указывает на существующий файл под `data/autoinstall/`.

### Плейсхолдеры выводятся буквально

`{{HOSTNAME}}` появляется в установленной системе как сама строка — значит, файл раздался до того, как сработала подстановка. Обычно потому, что клиент пришёл только по IP, и в запросе не было параметра `mac`. Убедитесь, что загрузочное меню формирует URL вида `/autoinstall/<iso>?mac=<mac>` или `/autoinstall/<iso>/mac/<mac>/`.

В Windows буквальные плейсхолдеры означают, что Setup использовал неотрендеренную копию с SMB-шары, а не файл, который инжектит wimboot. Посмотрите предпросмотр загрузочного меню клиента (см. [Предпросмотр загрузочного меню клиента](clients.md#previewing-a-clients-boot-menu)) и проверьте, что в секции Windows есть строка `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml`, а в параметрах загрузки образа нет `rawwim`.

Оставшийся буквальным `{{VAR.<name>}}` означает, что ни клиент, ни его группа не определяют эту переменную, либо имя написано иначе (имена чувствительны к регистру). Каждый неразрешённый плейсхолдер логируется при раздаче файла, а **Предпросмотр** в редакторе перечисляет их для выбранного клиента до того, как что-либо загрузится.

### Раздался не тот файл

Разрешение идёт от самого специфичного. Если у клиента есть собственное переопределение, а вы его не ожидали — вот почему дефолт уровня образа не используется. Посмотрите строку в логе сервера:

```
Served auto-install script for ... (source: client:..., type: ..., size: ...)
```

Поле `source:` точно скажет, какой слот победил.

### Установщик Proxmox сообщает, что не может скачать answer-файл

Пересобранный ISO скачивает файл ровно по тому URL, который зашил `prepare-iso`. Проверьте, что имя файла в этом URL совпадает с загруженным ISO (переименование ISO после пересборки всё ломает), что клиенту доступен порт 8080 и что на каком-то уровне цепочки разрешения прикреплён файл `.toml`. В логе сервера видны `POST` и разрешённый источник; `400` означает, что в теле не было `network_interfaces`, а это указывает на очень старый установщик.

### Windows Setup идёт интерактивно

- Для грузящегося клиента должен разрешаться autounattend-файл: прикрепите его к образу (свойства образа → Auto-Install), к клиенту или к его группе.
- Посмотрите предпросмотр загрузочного меню клиента и убедитесь, что в секции Windows есть строка `initrd .../autoinstall/<iso>?mac=<mac> AutoUnattend.xml`. Если её нет, для этого MAC не разрешился ни один autounattend-файл.
- Убедитесь, что в параметрах загрузки образа нет `rawwim`; он не даёт wimboot инжектить файлы в WinPE.
- Перепатчите `boot.wim` после обновления Bootimus, чтобы `startnet.cmd` искал инжектированный файл: **Images** → **Patch SMB** (или это произойдёт автоматически при следующей загрузке).
- Убедитесь, что SMB-шара доступна с клиента (`net view \\<server>` из WinPE); сам установщик по-прежнему берётся с шары.

### «AutoUnattend.xml neither injected by wimboot nor staged on the share, running interactive setup»

Логируется `startnet.cmd`, когда к образу прикреплён autounattend-файл, но ни один из двух путей доставки файл не дал. Проверьте в логе Bootimus запрос клиента `/autoinstall/<iso>?mac=...` в момент загрузки (404 означает, что для этого клиента файл не разрешился) и шаг стейджинга в момент патча. Перезапустите SMB-патч из свойств образа.

### `Z:\setup.exe not found on the share`

`startnet.cmd` подключил `Z:`, но в выведенном списке видна папка образа (`iso\`, `metadata.txt`), а не файлы установщика. Bootimus всегда расшаривает подпапку `iso` образа, значит клиент подключился к другому SMB-серверу по тому же адресу. Поищите в логе Bootimus `SMB: smbd exited` и посмотрите строку **Windows SMB** в **Server Info**. Обычная причина — каталог данных на файловой системе без прав Unix: `smbd` завершается с `invalid permissions on directory '.../smb/locks/msg.lock': has 0777 should be 0755`, а на порту 445 вместо него отвечает системная Samba. Перенесите каталог данных на ext4 или XFS, остановите другую Samba и перезапустите Bootimus.

### `net use fails after VM reboot`

Исправлено в 0.1.58 — в прилагаемый конфиг Samba добавлено `reset on zero vc = yes`. Если вы держите свой `smb.conf`, добавьте:

```
reset on zero vc = yes
oplocks = no
kernel oplocks = no
level2 oplocks = no
strict locking = no
deadtime = 1
```

## Дальше

- См. [Управление образами](images.md) — как прикреплять файлы к образам.
- См. [Управление клиентами](clients.md) — переопределения для конкретных клиентов.
- См. [Профили дистрибутивов](distro-profiles.md) — какие ID профилей мапятся на подкаталоги библиотеки.
