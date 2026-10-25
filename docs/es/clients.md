#  Guía de gestión de clientes

Guía completa para gestionar clientes de arranque en red con control de acceso basado en MAC.

##  Tabla de contenidos

- [Visión general](#visión-general)
- [Añadir clientes](#añadir-clientes)
- [Permisos de cliente](#permisos-de-cliente)
- [Imágenes públicas vs privadas](#imágenes-públicas-vs-privadas)
- [Estadísticas de cliente](#estadísticas-de-cliente)
- [Operaciones en masa](#operaciones-en-masa)
- [Previsualizar el menú de arranque de un cliente](#previsualizar-el-menú-de-arranque-de-un-cliente)
- [Arrancar desde iSCSI o AoE](#arrancar-desde-iscsi-o-aoe)
- [Consola remota](#consola-remota)
- [Solución de problemas](#solución-de-problemas)

## Visión general

Bootimus usa control de acceso basado en direcciones MAC para gestionar qué clientes pueden arrancar y a qué ISOs pueden acceder. Esto da un control granular sobre tu entorno de arranque en red.

### Conceptos clave

- **Client**: Un dispositivo de arranque en red identificado por dirección MAC
- **Static client**: Creado manualmente o promovido desde descubierto — un registro permanente
- **Discovered client**: Creado automáticamente cuando un dispositivo desconocido arranca por PXE (como un lease DHCP)
- **Enabled**: El cliente puede arrancar (aparece en el menú de arranque)
- **Disabled**: El cliente no puede arrancar (bloqueado del acceso al menú de arranque)
- **Assigned Images**: Cuando un cliente tiene imágenes asignadas, ve **solo esas imágenes** (no la lista pública completa)
- **Show Public Images**: Cuando se habilita junto con imágenes asignadas, el cliente ve tanto las asignadas como las públicas
- **Next Boot Action**: Un override de imagen de arranque de un solo uso que se auto-limpia tras usarse
- **Variables de instalación automática**: Valores con nombre disponibles para los archivos de auto-instalación como `{{VAR.<name>}}`; las variables de grupo son defaults y las de cliente las sobreescriben (ver [Auto-Install](auto-install.md#variables))

### Auto-descubrimiento de clientes

Cuando un dispositivo desconocido arranca por PXE, Bootimus crea automáticamente un registro de cliente **discovered** con:
- Dirección MAC de la petición PXE
- Inventario de hardware (CPU, memoria, fabricante, número de serie, NIC)
- Habilitado con imágenes públicas visibles por defecto

Los clientes descubiertos aparecen en la tabla de clientes con un badge "Discovered". Puedes promoverlos a clientes estáticos usando el botón **"Make Static"**, que los registra como entradas permanentes. Si un cliente previamente borrado vuelve a arrancar por PXE, se restaura automáticamente.

### Modos de base de datos

**Modo SQLite**:
- Clientes almacenados en base de datos SQLite
- Asignaciones de imagen almacenadas en el campo JSON `allowed_images`
- Perfecto para despliegues de un solo servidor

**Modo PostgreSQL**:
- Clientes almacenados en base de datos PostgreSQL
- Las asignaciones de imagen usan una tabla de relación muchos-a-muchos
- Mejor rendimiento para despliegues grandes

## Añadir clientes

### Vía interfaz web

1. Navega al panel admin: `http://your-server:8081`
2. Haz click en la pestaña **"Clients"**
3. Haz click en el botón **"Add Client"**
4. Rellena los detalles:
   - **MAC Address**: `00:11:22:33:44:55` (requerido)
   - **Name**: Nombre amigable (p. ej., "Lab Server 1")
   - **Description**: Detalles adicionales (opcional)
   - **Enabled**: Marca para permitir el arranque
5. Haz click en **"Create Client"**

### Vía API

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

### Formato de dirección MAC

Bootimus acepta direcciones MAC en estos formatos:
- `00:11:22:33:44:55` (separada por dos puntos, preferido)
- `00-11-22-33-44-55` (separada por guiones, auto-convertida)
- `001122334455` (sin separadores, auto-convertida)

Todos los formatos se normalizan a minúsculas separadas por dos puntos.

## Permisos de cliente

### Asignar imágenes a un cliente

**Vía interfaz web**:
1. Haz click en **"Edit"** en la fila del cliente
2. Selecciona imágenes del desplegable multi-selección
3. Haz click en **"Update Client"**

**Vía API**:
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

### Ver permisos de cliente

**Vía interfaz web**:
- Las imágenes asignadas del cliente se muestran en el modal de edición

**Vía API**:
```bash
# Get client details including assigned images
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients?mac=00:11:22:33:44:55" | jq
```

**Respuesta**:
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

## Imágenes públicas vs privadas

### Imágenes públicas

Las imágenes públicas están disponibles para **todos los clientes**, incluso los no registrados.

**Casos de uso**:
-  ISOs de rescate/recuperación
-  Herramientas de diagnóstico de red
-  Imágenes de despliegue comunes
-  Entornos de lab abiertos

**Hacer imagen pública**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=ubuntu.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": true}'
```

### Imágenes privadas

Las imágenes privadas están **disponibles solo para clientes asignados**.

**Casos de uso**:
-  Imágenes sensibles o con licencia
-  Despliegues específicos por cliente
-  Entornos restringidos
-  Imágenes beta/test

**Hacer imagen privada**:
```bash
curl -H "Authorization: Bearer $TOKEN" -X PUT "http://localhost:8081/api/images?filename=windows.iso" \
  -H "Content-Type: application/json" \
  -d '{"public": false}'
```

### Matriz de control de acceso

| Estado del cliente | Lo que ve |
|--------------|---------------|
| **Habilitado + Asignado** | Solo sus imágenes asignadas |
| **Habilitado + Sin asignaciones** | Todas las imágenes públicas |
| **Deshabilitado** | Todas las imágenes públicas |
| **No registrado** | Todas las imágenes públicas |

## Estadísticas de cliente

Bootimus registra estadísticas de arranque para cada cliente:

- **Boot Count**: Número total de intentos de arranque
- **Last Boot**: Timestamp del arranque más reciente
- **Success Rate**: Porcentaje de arranques exitosos

### Ver estadísticas

**Vía interfaz web**:
- Estadísticas mostradas en la tabla de clientes

**Vía API**:
```bash
# Get all clients with statistics
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | jq '.data[] | {name, boot_count, last_boot}'

# Get top clients by boot count
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients | \
  jq '.data | sort_by(.boot_count) | reverse | .[0:10] | .[] | {name, boot_count}'
```

### Logs de arranque

Visualiza logs de arranque detallados por cliente:

```bash
# Filter boot logs by MAC address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | \
  jq '.data[] | select(.mac_address=="00:11:22:33:44:55")'
```

## Operaciones en masa

### Añadir clientes en masa

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

### Asignar imágenes en masa

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

### Habilitar/deshabilitar en masa

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

### Exportar lista de clientes

```bash
#!/bin/bash
# export-clients.sh

ADMIN_PASSWORD="${ADMIN_PASSWORD:-your-password}"

echo "MAC Address,Name,Description,Enabled,Boot Count,Last Boot"

curl -H "Authorization: Bearer $TOKEN" -s http://localhost:8081/api/clients | \
  jq -r '.data[] | [.mac_address, .name, .description, .enabled, .boot_count, .last_boot] | @csv'
```

### Importar clientes desde CSV

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

Establece una imagen de arranque de un solo uso para un cliente. En el próximo arranque PXE, la imagen seleccionada estará pre-seleccionada como entrada por defecto del menú con un timeout. La acción se auto-limpia tras usarse — los arranques siguientes vuelven a la normalidad.

### Vía interfaz web

1. Haz click en **"Next Boot"** en la fila de un cliente
2. Selecciona una imagen del desplegable
3. Haz click en **"Set Next Boot"** para solo establecer la imagen, o **"Set & Wake"** para enviar también un paquete Wake-on-LAN

### Vía API

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

### Comportamiento

- El menú de arranque se muestra normalmente pero con la imagen del next boot pre-seleccionada como default
- Si el timeout global del menú está deshabilitado (puesto a 0), se aplica un timeout de 10 segundos como override
- Si el cliente no arranca antes de que se consuma la acción, el next boot se limpia en la primera petición PXE
- Los grupos vacíos se ocultan del menú cuando un cliente tiene imágenes asignadas

## Previsualizar el menú de arranque de un cliente

Genera el script iPXE exacto que recibirá un cliente sin esperar a que arranque por PXE. La previsualización no tiene efectos secundarios: no se escribe nada en el log de arranque, la acción de next boot del cliente no se consume y el estado de inscripción en clúster se deja intacto.

### Vía interfaz web

- Haz click en **"Preview Menu"** en la fila de un cliente, o
- Abre **Boot Menu** → **Preview as Client**, introduce una dirección MAC (o deja `unknown` para un cliente no registrado) y haz click en **"Preview Menu"**

El modal muestra si la MAC es un cliente registrado, si recibiría el menú normal o un arranque de instalación de clúster, cualquier imagen de next boot pendiente y el script completo con botones **Copy** y **Download**.

### Vía API

```bash
# Preview for a specific client
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8081/api/clients/menu-preview?mac=00:11:22:33:44:55"

# Preview the anonymous menu an unregistered client gets
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/clients/menu-preview
```

Los datos de la respuesta contienen `mac`, `known_client`, `client_name`, `next_boot_image`, `kind` (`menu` o `install`) y `script`. Los usuarios de solo lectura pueden llamar a este endpoint.

## Arrancar desde iSCSI o AoE

Los clientes pueden arrancar un sistema operativo que vive en un disco SAN en lugar de en uno local. Bootimus entrega el disco al firmware con el comando `sanboot` de iPXE, así que aquí funciona cualquier cosa que pueda arrancar desde una SAN con iPXE: targets iSCSI y ATA-over-Ethernet, e imágenes de disco en bruto servidas por HTTP. Bootimus no aloja los discos; apunta un target a un servidor de almacenamiento que ya tengas.

### Definir un target

**Clients** → **SAN Targets** → **Add SAN Target**:

| Campo | Significado |
|-------|-------------|
| Name | Se muestra en el menú de arranque como "Boot from SAN: *nombre*" |
| SAN URI | La URI SAN de iPXE, por ejemplo `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`, `aoe:e0.0` o `http://10.0.0.5/disk.img` |
| Initiator IQN | Opcional. `{{MAC}}` se sustituye por la dirección MAC del cliente sin dos puntos, de modo que un mismo target puede servir a muchos clientes con nombres de iniciador distintos |
| CHAP | Usuario y contraseña opcionales, más credenciales CHAP inversas (mutuas) si el target las exige |

El formato de la URI iSCSI es `iscsi:<server>:<protocol>:<port>:<lun>:<iqn>`. Deja vacíos el protocolo, el puerto y la LUN para usar los valores por defecto, como en el ejemplo anterior.

### Asignar un target

Abre un cliente (o un grupo de clientes) y elige el target en **SAN Target**. Un cliente sin target propio hereda el del grupo. El menú del cliente gana una entrada "Boot from SAN"; marca **Boot from SAN by default** para que sea la entrada a la que cae el menú cuando vence el timeout, de modo que una máquina reiniciada vuelva a levantarse directamente desde la SAN. Una imagen de next boot pendiente sigue teniendo prioridad, y las imágenes habituales siguen siendo seleccionables.

Para un arranque SAN puntual elige el target en **Next Boot**; aparece después de las imágenes y se limpia tras un arranque como cualquier otra acción de next boot.

### Targets de ejemplo

Un target Linux con `tgt`:

```bash
sudo tgtadm --lld iscsi --op new --mode target --tid 1 -T iqn.2024-01.lan:disk0
sudo tgtadm --lld iscsi --op new --mode logicalunit --tid 1 --lun 1 -b /srv/iscsi/disk0.img
sudo tgtadm --lld iscsi --op bind --mode target --tid 1 -I ALL
```

URI del target: `iscsi:10.0.0.5::::iqn.2024-01.lan:disk0`

Con LIO (`targetcli`) crea un backstore fileio o block, un iqn, una LUN y un portal, y o bien desactiva la autenticación para el TPG o bien define `userid`/`password` (y `mutual_userid`/`mutual_password`) para que coincidan con los campos CHAP de aquí.

**Windows** se instala y arranca desde iSCSI a través del SAN hook de iPXE: arranca el instalador de Windows desde el menú mientras el cliente tiene un target asignado, y el instalador ve el disco iSCSI como una unidad local. Tras la instalación marca **Boot from SAN by default** para que la máquina vuelva a él en cada arranque. Windows necesita el driver de red de la NIC desde la que arrancó; la mayoría de NICs Intel y Realtek modernas están cubiertas por el instalador.

### Salvedades

- Las credenciales CHAP se colocan en el script iPXE, que el cliente que arranca descarga por HTTP plano. Trata la red de arranque como de confianza, igual que ya haces con los archivos de respuesta de autoinstalación.
- El `sanboot` de iPXE requiere un bootloader en el disco SAN; no instala nada.
- Los usuarios de solo lectura ven los targets pero no las contraseñas CHAP.

## Wake-on-LAN

Envía un paquete mágico WOL para despertar un cliente remotamente. Combina con **Next Boot** para despertar una máquina y hacer que arranque en una imagen específica.

### Vía API

```bash
# Wake a client
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55"

# Wake with custom broadcast address
curl -H "Authorization: Bearer $TOKEN" -X POST "http://localhost:8081/api/clients/wake?mac=00:11:22:33:44:55" \
  -H "Content-Type: application/json" \
  -d '{"broadcast_addr":"192.168.1.255"}'
```

## Consola remota

Los clientes con un host BMC configurado tienen un botón **Open Console** en el diálogo de edición del cliente y un botón **Console** en la lista de clientes. Abre una consola serie interactiva en el navegador, transmitida a través de Bootimus por un WebSocket, de modo que se puede seguir una máquina durante el POST, la configuración del firmware y el instalador sin tener un monitor conectado. La consola es solo para administradores, una sesión por cliente a la vez, y se cierra tras 30 minutos sin actividad.

| Protocolo | Transporte | Requisitos |
|---|---|---|
| **Intel AMT** | Serial-over-LAN a través del servicio de redirección de AMT, implementado de forma nativa por Bootimus en el puerto 16994 (16995 cuando el cliente usa el puerto de gestión TLS 16993) | SOL y el listener de redirección habilitados en MEBx bajo *Remote Control*, o en la UI web de AMT bajo *Remote Control* → *Redirection*; el mismo usuario y contraseña que el control de energía |
| **Redfish** | IPMI Serial-over-LAN ejecutando `ipmitool sol activate` en el host de Bootimus | `ipmitool` en el `PATH` (la imagen Docker lo incluye), el puerto UDP 623 accesible en el BMC y SOL habilitado para el usuario del BMC. El puerto Redfish no se usa para la consola |

La consola serie solo muestra lo que el firmware o el sistema operativo escriben en el puerto serie. En la mayoría de servidores eso implica habilitar *Console Redirection* en la configuración de la BIOS o UEFI, y en Linux añadir `console=ttyS0,115200` (o `ttyS1`, según la placa) a la línea de comandos del kernel. Las máquinas con Intel AMT redirigen COM1 a 115200 baudios en cuanto se habilita SOL. Si la pantalla se queda en blanco, pulsa Intro; si no aparece nada durante el arranque, la redirección de consola no está activada en el firmware.

Las pulsaciones de teclas se envían tal cual, así que los atajos de terminal habituales funcionan. Cerrar el diálogo o hacer click en **Disconnect** termina la sesión y, en IPMI, desactiva el payload SOL en el BMC para que la siguiente conexión empiece limpia.

### Vía API

El endpoint es un WebSocket en `/api/clients/console?mac=...` en el puerto admin. Los navegadores no pueden enviar una cabecera `Authorization` en un WebSocket, así que el JWT se pasa como segunda entrada de `Sec-WebSocket-Protocol` después de `bearer`, o como parámetro de query `token`. Los frames binarios llevan la salida de la consola y las pulsaciones; los frames de texto de Bootimus son mensajes de estado JSON como `{"type":"connected","message":"..."}`.

```bash
# websocat example: interactive console for a client
websocat --binary "ws://localhost:8081/api/clients/console?mac=00:11:22:33:44:55&token=$TOKEN"
```

## Inventario de hardware

Bootimus recoge información de hardware de los clientes PXE durante el arranque, incluyendo:
- CPU, memoria, plataforma y arquitectura
- Fabricante, nombre de producto y número de serie
- UUID e info del chip NIC
- Dirección IP

Visualiza el inventario desde el modal **Edit & Assign Images** de cualquier cliente, o usa la API:

```bash
# Latest inventory
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory?mac=00:11:22:33:44:55"

# Inventory history
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/clients/inventory/history?mac=00:11:22:33:44:55&limit=10"
```

## Solución de problemas

### El cliente no ve el menú de arranque

**Síntomas**: El cliente arranca pero muestra un menú vacío o "No boot images available"

**Causas posibles**:
1. El cliente está deshabilitado
2. No hay imágenes públicas disponibles
3. No hay imágenes asignadas al cliente
4. Todas las imágenes están deshabilitadas

**Solución**:
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

### Dirección MAC no detectada

**Síntomas**: Los logs de arranque muestran dirección MAC "unknown"

**Causas posibles**:
1. iPXE no puede detectar la dirección MAC desde la interfaz de red
2. El cliente usa múltiples interfaces de red

**Solución**:
```bash
# Check boot logs for actual IP address
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/logs | jq '.data[] | {mac_address, ip_address}'

# Register client by IP if MAC is unknown
# (Note: Less reliable, IP may change)
```

### Las imágenes asignadas no aparecen

**Síntomas**: El cliente solo puede ver imágenes públicas, no las asignadas

**Causas posibles**:
1. Cliente no habilitado
2. Imágenes no habilitadas
3. Formato de dirección MAC equivocado
4. Problema de sincronización de base de datos

**Solución**:
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

### Error de cliente duplicado

**Síntomas**: "Client already exists" o error de constraint UNIQUE

**Causa**: La dirección MAC ya está registrada

**Solución**:
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

## Siguientes pasos

-  Configura la [Gestión de imágenes](images.md) para añadir ISOs
-  Usa la [Consola admin](admin.md) para gestión
-  Configura el [DHCP](dhcp.md) para arranque en red
-  Visualiza los [Logs de arranque](admin.md#boot-logs) para monitorización
