# Руководство по управлению клиентами

Полное руководство по управлению клиентами сетевой загрузки с контролем доступа по MAC.

## Оглавление

- [Обзор](#обзор)
- [Добавление клиентов](#добавление-клиентов)
- [Права клиента](#права-клиента)
- [Публичные vs приватные образы](#публичные-vs-приватные-образы)
- [Статистика клиентов](#статистика-клиентов)
- [Массовые операции](#массовые-операции)
- [Предпросмотр меню загрузки клиента](#предпросмотр-меню-загрузки-клиента)
- [Загрузка с iSCSI или AoE](#загрузка-с-iscsi-или-aoe)
- [Удалённая консоль](#удалённая-консоль)
- [Диагностика](#диагностика)

## Обзор

Bootimus использует контроль доступа по MAC-адресам, чтобы управлять тем, какие клиенты могут грузиться и к каким ISO у них есть доступ. Это даёт гранулярный контроль над окружением сетевой загрузки.

### Ключевые понятия

- **Клиент**: устройство сетевой загрузки, идентифицируемое MAC-адресом
- **Статический клиент**: создан вручную или повышен из обнаруженного — постоянная регистрация
- **Обнаруженный клиент**: автоматически создан, когда неизвестное устройство выполнило PXE-загрузку (как DHCP-лиза)
- **Enabled**: клиенту разрешено грузиться (отображается в загрузочном меню)
- **Disabled**: клиент не может грузиться (заблокирован доступ к загрузочному меню)
- **Назначенные образы**: когда у клиента есть назначенные образы, он видит **только их** (не полный публичный список)
- **Show Public Images**: если включено вместе с назначенными образами, клиент видит и назначенные, и публичные
- **Next Boot Action**: одноразовое переопределение образа загрузки, автоматически сбрасывается после использования
- **Переменные автоустановки**: именованные значения, доступные файлам автоустановки как `{{VAR.<name>}}`; переменные группы — значения по умолчанию, переменные клиента их переопределяют (см. [Автоустановка](auto-install.md#переменные))

### Авто-обнаружение клиентов

Когда неизвестное устройство выполняет PXE-загрузку, Bootimus автоматически создаёт **обнаруженного** клиента с:
- MAC-адресом из PXE-запроса
- Аппаратной инвентаризацией (CPU, память, производитель, серийный номер, NIC)
- Включён, публичные образы видимы по умолчанию

Обнаруженные клиенты показываются в таблице с бейджем «Discovered». Их можно повысить до статических кнопкой **«Make Static»** — это регистрирует их как постоянные записи. Если ранее удалённый клиент снова стартует по PXE, он автоматически восстанавливается.

### Режимы базы данных

**Режим SQLite**:
- Клиенты хранятся в SQLite
- Назначения образов хранятся в JSON-поле `allowed_images`
- Идеально для одиночных серверов

**Режим PostgreSQL**:
- Клиенты хранятся в PostgreSQL
- Назначения образов — таблица many-to-many
- Лучше для крупных развёртываний

## Добавление клиентов

### Через веб-интерфейс

1. Откройте админ-панель: `http://your-server:8081`
2. Перейдите во вкладку **Clients**
3. Нажмите **«Add Client»**
4. Заполните детали:
   - **MAC Address**: `00:11:22:33:44:55` (обязательно)
   - **Name**: дружественное имя (например, «Lab Server 1»)
   - **Description**: доп. детали (опционально)
   - **Enabled**: поставьте, чтобы разрешить загрузку
5. Нажмите **«Create Client»**

### Через API

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

### Формат MAC-адреса

Bootimus принимает MAC-адреса в этих форматах:
- `00:11:22:33:44:55` (через двоеточие, предпочтительно)
- `00-11-22-33-44-55` (через дефис, авто-конвертация)
- `001122334455` (без разделителей, авто-конвертация)

Все форматы нормализуются в двоеточия в нижнем регистре.

## Права клиента

### Назначение образов клиенту

**Через веб-интерфейс**:
1. Нажмите **«Edit»** в строке клиента
2. Выберите образы из выпадающего списка
3. Нажмите **«Update Client»**

**Через API**:
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

### Просмотр прав клиента

**Через веб-интерфейс**:
- Назначенные образы клиента отображаются в модальном окне редактирования

**Через API**:
```bash
# Получить детали клиента с назначенными образами
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq
```

**Ответ**:
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

## Публичные vs приватные образы

### Публичные образы

Публичные образы доступны **всем клиентам**, даже незарегистрированным.

**Сценарии**:
- Rescue/recovery-ISO
- Сетевые диагностические инструменты
- Общие deployment-образы
- Открытые лабораторные окружения

**Сделать образ публичным**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": true}'
```

### Приватные образы

Приватные образы доступны **только назначенным клиентам**.

**Сценарии**:
- Чувствительные или лицензируемые образы
- Клиент-специфичные развёртывания
- Ограниченные окружения
- Бета/тестовые образы

**Сделать образ приватным**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=windows.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": false}'
```

### Матрица контроля доступа

| Состояние клиента | Что он видит |
|--------------|---------------|
| **Enabled + Назначены** | Только свои назначенные образы |
| **Enabled + Без назначений** | Все публичные образы |
| **Disabled** | Все публичные образы |
| **Не зарегистрирован** | Все публичные образы |

## Статистика клиентов

Bootimus собирает статистику загрузок по каждому клиенту:

- **Boot Count**: общее число попыток загрузки
- **Last Boot**: метка времени последней загрузки
- **Success Rate**: процент успешных загрузок

### Просмотр статистики

**Через веб-интерфейс**:
- Статистика показывается в таблице клиентов

**Через API**:
```bash
# Получить всех клиентов со статистикой
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | jq '.data[] | {name, boot_count, last_boot}'

# Топ клиентов по числу загрузок
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data | sort_by(.boot_count) | reverse | .[0:10] | .[] | {name, boot_count}'
```

### Логи загрузок

Подробные логи загрузок по клиенту:

```bash
# Фильтр логов по MAC-адресу
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'
```

## Массовые операции

### Массовое добавление клиентов

```bash
#!/bin/bash
# bulk-add-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Формат: MAC:NAME:DESCRIPTION
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

### Массовое назначение образов

```bash
#!/bin/bash
# bulk-assign-images.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Назначить Ubuntu и Debian всем серверам
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

### Массовое включение/выключение

```bash
#!/bin/bash
# bulk-enable.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

# Получить всех клиентов и включить их
macs=$(curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[].mac_address')

for mac in $macs; do
  curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=$mac" \
    -H "Content-Type: application/json" \
    -d '{"enabled":true}'
  echo "Enabled $mac"
done
```

### Экспорт списка клиентов

```bash
#!/bin/bash
# export-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

echo "MAC Address,Name,Description,Enabled,Boot Count,Last Boot"

curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[] | [.mac_address, .name, .description, .enabled, .boot_count, .last_boot] | @csv'
```

### Импорт клиентов из CSV

```bash
#!/bin/bash
# import-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"
CSV_FILE="clients.csv"

# Пропускаем заголовок и обрабатываем CSV
tail -n +2 "$CSV_FILE" | while IFS=',' read -r mac name description enabled; do
  # Убираем кавычки из значений CSV
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

Назначьте клиенту одноразовый образ загрузки. При следующей PXE-загрузке выбранный образ будет предустановлен по умолчанию с таймаутом. После использования действие автоматически сбрасывается — последующие загрузки возвращаются к норме.

### Через веб-интерфейс

1. Нажмите **«Next Boot»** в строке клиента
2. Выберите образ из выпадающего списка
3. Нажмите **«Set Next Boot»** — только установить образ, или **«Set & Wake»** — ещё и отправить Wake-on-LAN

### Через API

```bash
# Установить образ следующей загрузки
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients/next-boot \
  -H "Content-Type: application/json" \
  -d '{"mac_address":"00:11:22:33:44:55","image_filename":"ubuntu-24.04.iso"}'

# Сбросить действие следующей загрузки
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/clients/next-boot \
  -H "Content-Type: application/json" \
  -d '{"mac_address":"00:11:22:33:44:55","image_filename":""}'
```

### Поведение

- Меню загрузки отображается как обычно, но образ следующей загрузки предустановлен по умолчанию
- Если глобальный таймаут меню отключён (0), применяется таймаут 10 секунд как переопределение
- Если клиент не успеет загрузиться до того, как действие будет потреблено, next boot сбросится при первом PXE-запросе
- Пустые группы скрываются из меню, когда у клиента есть назначенные образы

## Предпросмотр меню загрузки клиента

Сформируйте точный iPXE-скрипт, который получит клиент, не дожидаясь его PXE-загрузки. Предпросмотр не имеет побочных эффектов: ничего не пишется в лог загрузок, действие next boot клиента не потребляется, а состояние регистрации в кластере не меняется.

### Через веб-интерфейс

- Нажмите **"Preview Menu"** в строке клиента, или
- Откройте **Boot Menu** → **Preview as Client**, введите MAC-адрес (или оставьте `unknown` для незарегистрированного клиента) и нажмите **"Preview Menu"**

В окне показывается, зарегистрирован ли этот MAC как клиент, получит ли он обычное меню или загрузку установки кластера, ожидающий образ next boot и полный скрипт с кнопками **Copy** и **Download**.

### Через API

```bash
# Предпросмотр для конкретного клиента
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8081/api/clients/menu-preview?mac=00:11:22:33:44:55"

# Предпросмотр анонимного меню, которое получает незарегистрированный клиент
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients/menu-preview
```

Данные ответа содержат `mac`, `known_client`, `client_name`, `next_boot_image`, `kind` (`menu` или `install`) и `script`. Пользователи только для чтения могут вызывать этот эндпоинт.

## Загрузка с iSCSI или AoE

Клиенты могут загружать операционную систему, расположенную на SAN-диске, а не на локальном. Bootimus передаёт диск прошивке командой iPXE `sanboot`, поэтому здесь работает всё, что умеет грузиться с SAN через iPXE: цели iSCSI и ATA-over-Ethernet, а также сырые образы дисков, отдаваемые по HTTP. Bootimus сам диски не хранит; направьте цель на сервер хранения, который у вас уже есть.

### Описание цели

**Clients** → **SAN Targets** → **Add SAN Target**:

| Поле | Значение |
|------|----------|
| Name | Показывается в меню загрузки как «Boot from SAN: *name*» |
| SAN URI | SAN URI в формате iPXE, например `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`, `aoe:e0.0` или `http://10.0.0.5/disk.img` |
| Initiator IQN | Необязательно. `{{MAC}}` заменяется на MAC-адрес клиента без двоеточий, так что одна цель может обслуживать много клиентов с разными именами инициаторов |
| CHAP | Необязательные имя пользователя и пароль, а также учётные данные обратного (взаимного) CHAP, если цель их требует |

Формат iSCSI URI: `iscsi:<server>:<protocol>:<port>:<lun>:<iqn>`. Оставьте протокол, порт и LUN пустыми для значений по умолчанию, как в примере выше.

### Назначение цели

Откройте клиента (или группу клиентов) и выберите цель в поле **SAN Target**. Клиент без собственной цели наследует цель группы. В меню клиента появляется пункт «Boot from SAN»; отметьте **Boot from SAN by default**, чтобы сделать его пунктом, к которому меню переходит по истечении таймаута, — тогда перезагруженная машина сразу поднимается с SAN. Ожидающий образ next boot по-прежнему имеет приоритет, а обычные образы остаются доступными для выбора.

Для разовой загрузки с SAN выберите цель в **Next Boot**; она перечислена после образов и сбрасывается после одной загрузки, как любое другое действие next boot.

### Примеры целей

Linux-цель на `tgt`:

```bash
sudo tgtadm --lld iscsi --op new --mode target --tid 1 -T iqn.2024-01.lan:disk0
sudo tgtadm --lld iscsi --op new --mode logicalunit --tid 1 --lun 1 -b /srv/iscsi/disk0.img
sudo tgtadm --lld iscsi --op bind --mode target --tid 1 -I ALL
```

URI цели: `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`

В LIO (`targetcli`) создайте backstore fileio или block, iqn, LUN и портал и либо отключите аутентификацию для TPG, либо задайте `userid`/`password` (и `mutual_userid`/`mutual_password`) в соответствии с полями CHAP здесь.

**Windows** устанавливается и загружается с iSCSI через SAN hook в iPXE: загрузите установщик Windows из меню, пока у клиента назначена цель, и установщик увидит iSCSI-диск как локальный. После установки отметьте **Boot from SAN by default**, чтобы машина возвращалась к нему при каждой загрузке. Windows нужен сетевой драйвер для NIC, с которого она загрузилась; большинство современных NIC Intel и Realtek установщиком покрываются.

### Оговорки

- Учётные данные CHAP помещаются в iPXE-скрипт, который загружающийся клиент получает по обычному HTTP. Считайте загрузочную сеть доверенной, как вы уже делаете для файлов ответов автоустановки.
- `sanboot` в iPXE требует наличия загрузчика на SAN-диске; он ничего не устанавливает.
- Пользователи только для чтения видят цели, но не пароли CHAP.

## Wake-on-LAN

Отправьте WOL magic packet, чтобы разбудить клиента удалённо. Сочетайте с **Next Boot** — разбудить машину и загрузить её в конкретный образ.

### Через API

```bash
# Разбудить клиента
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55"

# Разбудить с указанным broadcast-адресом
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"broadcast_addr":"192.168.1.255"}'
```

## Удалённая консоль

У клиентов с настроенным хостом BMC появляется кнопка **Open Console** в диалоге редактирования клиента и кнопка **Console** в списке клиентов. Она открывает интерактивную последовательную консоль в браузере, транслируемую через Bootimus по WebSocket, так что за машиной можно наблюдать во время POST, настройки прошивки и работы установщика без подключённого монитора. Консоль доступна только администраторам, по одному сеансу на клиента, и закрывается после 30 минут без активности.

| Протокол | Транспорт | Требования |
|---|---|---|
| **Intel AMT** | Serial-over-LAN через сервис перенаправления AMT, реализованный в Bootimus нативно, на порту 16994 (16995, когда у клиента указан TLS-порт управления 16993) | SOL и слушатель перенаправления включены в MEBx в разделе *Remote Control* или в веб-интерфейсе AMT в *Remote Control* → *Redirection*; те же пользователь и пароль, что и для управления питанием |
| **Redfish** | IPMI Serial-over-LAN через запуск `ipmitool sol activate` на хосте Bootimus | `ipmitool` в `PATH` (в Docker-образе он есть), доступный UDP-порт 623 на BMC и включённый SOL для пользователя BMC. Порт Redfish для консоли не используется |

Последовательная консоль показывает только то, что прошивка или операционная система пишет в последовательный порт. На большинстве серверов это означает включение *Console Redirection* в настройках BIOS или UEFI, а в Linux — добавление `console=ttyS0,115200` (или `ttyS1`, в зависимости от платы) в командную строку ядра. Машины с Intel AMT перенаправляют COM1 на скорости 115200 бод, как только включён SOL. Если экран остаётся пустым, нажмите Enter; если во время загрузки ничего не появляется, перенаправление консоли в прошивке не включено.

Нажатия клавиш отправляются как есть, поэтому обычные сочетания клавиш терминала работают. Закрытие диалога или нажатие **Disconnect** завершает сеанс и, для IPMI, деактивирует SOL-полезную нагрузку на BMC, чтобы следующее подключение начиналось чисто.

### Через API

Эндпоинт — WebSocket по адресу `/api/clients/console?mac=...` на админ-порту. Браузеры не могут отправить заголовок `Authorization` в WebSocket, поэтому JWT передаётся либо вторым элементом `Sec-WebSocket-Protocol` после `bearer`, либо параметром запроса `token`. Бинарные кадры несут вывод консоли и нажатия клавиш; текстовые кадры от Bootimus — это JSON-сообщения о состоянии вроде `{"type":"connected","message":"..."}`.

```bash
# Пример с websocat: интерактивная консоль клиента
websocat --binary "ws://localhost:8081/api/clients/console?mac=00:11:22:33:44:55&token=$TOKEN"
```

## Аппаратная инвентаризация

Bootimus собирает с PXE-клиентов информацию о железе во время загрузки:
- CPU, память, платформа и архитектура
- Производитель, название продукта и серийный номер
- UUID и инфа о чипе NIC
- IP-адрес

Посмотреть инвентарь можно в модальном окне **Edit & Assign Images** для любого клиента или через API:

```bash
# Последний инвентарь
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory?mac=00:11:22:33:44:55"

# История инвентаря
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory/history?mac=00:11:22:33:44:55&limit=10"
```

## Диагностика

### Клиент не видит меню загрузки

**Симптомы**: клиент грузится, но показывает пустое меню или «No boot images available»

**Возможные причины**:
1. Клиент выключен
2. Нет публичных образов
3. У клиента нет назначенных образов
4. Все образы отключены

**Решение**:
```bash
# Проверить статус клиента
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq

# Включить клиента
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'

# Проверить доступные образы
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/images | jq '.data[] | {filename, enabled, public}'

# Сделать образы публичными
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public":true,"enabled":true}'
```

### MAC-адрес не определяется

**Симптомы**: логи загрузок показывают «unknown» MAC

**Возможные причины**:
1. iPXE не может определить MAC с сетевого интерфейса
2. Клиент с несколькими сетевыми интерфейсами

**Решение**:
```bash
# Посмотреть актуальный IP в логах загрузок
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | jq '.data[] | {mac_address, ip_address}'

# Зарегистрировать клиента по IP, если MAC неизвестен
# (Замечание: менее надёжно, IP может меняться)
```

### Назначенные образы не показываются

**Симптомы**: клиент видит только публичные образы, а не назначенные

**Возможные причины**:
1. Клиент не включён
2. Образы не включены
3. Неверный формат MAC
4. Проблема синхронизации базы

**Решение**:
```bash
# Проверить, что клиент существует и включён
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq

# Проверить назначения образов
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | \
  jq '.data.allowed_images'

# Переназначить образы
curl -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8081/api/assign-images \
  -H "Content-Type: application/json" \
  -d '{
    "mac_address":"00:11:22:33:44:55",
    "image_filenames":["ubuntu.iso","debian.iso"]
  }'

# Проверить базу напрямую (SQLite)
sqlite3 data/bootimus.db "SELECT * FROM clients WHERE mac_address='00:11:22:33:44:55';"
```

### Ошибка дубликата клиента

**Симптомы**: «Client already exists» или ошибка UNIQUE constraint

**Причина**: MAC-адрес уже зарегистрирован

**Решение**:
```bash
# Найти существующего клиента
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'

# Обновить существующего вместо создания
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"name":"Updated Name","enabled":true}'

# Или удалить и пересоздать
curl -H "Authorization: Bearer $TOKEN" -X DELETE "http://localhost:8081/api/clients?mac=00:11:22:33:44:55"
```

## Дальше

- Настройте [управление образами](images.md), чтобы добавить ISO
- Используйте [админ-консоль](admin.md) для управления
- Настройте [DHCP-конфигурацию](dhcp.md) для сетевой загрузки
- Смотрите [логи загрузок](admin.md#boot-logs) для мониторинга
