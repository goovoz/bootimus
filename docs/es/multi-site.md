# Despliegues multi-sitio

Ejecuta un Bootimus como **primario**, donde gestionas todo, y una **réplica** en cada sucursal que refleja el primario y arranca los clientes locales desde copias locales. Las réplicas son de solo lectura: cada cambio se hace en el primario y llega a las réplicas en su siguiente sincronización.

## Tabla de contenidos

- [Topología](#topología)
- [Configurar el primario](#configurar-el-primario)
- [Configurar una réplica](#configurar-una-réplica)
- [Qué se sincroniza y qué no](#qué-se-sincroniza-y-qué-no)
- [Servir ISOs localmente o desde el primario](#servir-isos-localmente-o-desde-el-primario)
- [Ancho de banda](#ancho-de-banda)
- [Cuando el primario no es accesible](#cuando-el-primario-no-es-accesible)
- [Monitorización](#monitorización)
- [API](#api)

## Topología

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

Cada réplica mantiene su propia base de datos y su propio directorio de datos, así que una sucursal sigue arrancando desde su última copia buena incluso cuando el enlace WAN está caído. Las réplicas nunca escriben en el primario.

## Configurar el primario

En el primario no cambia nada más allá de crear un token para cada réplica:

1. Abre **Users** → **API Tokens** y crea un token con el nombre de la sede.
2. Elige el ámbito:
   - `read` basta para clientes, imágenes, grupos, destinos SAN, el tema, los perfiles personalizados, las herramientas y los archivos personalizados.
   - `admin` sincroniza además los **archivos de auto-instalación** y las contraseñas BMC y CHAP. Con un token `read` esas contraseñas llegan redactadas y la réplica conserva lo que ya tiene.
3. Copia el secreto `btk_...`; se muestra una sola vez.

Sirve la interfaz de administración del primario por HTTPS (`--admin-tls`, consulta la [guía de despliegue](deployment.md#proteger-la-interfaz-admin)) para que el token no viaje en texto claro.

## Configurar una réplica

Instala Bootimus en la sucursal como de costumbre y después apúntalo al primario:

```bash
bootimus serve \
  --replica-primary https://bootimus.hq.example.com:8081 \
  --replica-token btk_... \
  --replica-interval 5m
```

O en `bootimus.yaml`:

```yaml
replica:
  primary_url: https://bootimus.hq.example.com:8081
  token: btk_...
  interval: 5m
  sync_isos: true
```

Las variables de entorno son `BOOTIMUS_REPLICA_PRIMARY_URL`, `BOOTIMUS_REPLICA_TOKEN`, `BOOTIMUS_REPLICA_INTERVAL` y `BOOTIMUS_REPLICA_SYNC_ISOS`. El intervalo mínimo es de 30 segundos.

La réplica sincroniza una vez al arrancar y después en cada intervalo. Su interfaz de administración muestra un aviso con el nombre del primario y la hora de la última sincronización correcta, y cada creación, edición o borrado responde `409 Conflict` con una referencia al primario. Los usuarios locales y los tokens de API se pueden seguir gestionando en la réplica para que puedas iniciar sesión en ella.

Activa `--proxy-dhcp` en la réplica si la sucursal no tiene un servidor DHCP compatible con PXE, exactamente igual que harías en una instalación independiente.

## Qué se sincroniza y qué no

| Sincronizado desde el primario | Mantenido en local en cada instancia |
|--------------------------------|--------------------------------------|
| Clientes (nombre, grupo, imágenes permitidas, ajustes BMC, archivo de auto-instalación, destino SAN) | Logs de arranque, contadores de arranque y horas del último arranque |
| Grupos de clientes | Inventario de hardware |
| Imágenes y grupos de imágenes (metadatos, método de arranque, parámetros de arranque, selección WinPE) | Acciones de siguiente arranque y Wake-on-LAN |
| Destinos SAN | Clientes descubiertos que solo han arrancado en la sucursal |
| Tema del menú de arranque | Callbacks y estado de instalación |
| Perfiles de distro personalizados | Usuarios, tokens de API y el registro de auditoría |
| Herramientas de arranque activadas y sus archivos | Tareas programadas y webhooks |
| Archivos de auto-instalación (solo con token admin) | Clústeres Kubernetes e inscripción de nodos |
| Archivos personalizados | Ajustes de limpieza |
| El nombre del conjunto de cargadores de arranque activo (los archivos del conjunto ya deben existir en la réplica) | Archivos de conjuntos de cargadores personalizados |

Las filas que desaparecen del primario se eliminan de la réplica, con una excepción: los clientes descubiertos en la sucursal y nunca promovidos en el primario se dejan como están. Las acciones de siguiente arranque no se reflejan deliberadamente, porque una acción de un solo uso consumida en la sucursal se volvería a aplicar en la siguiente sincronización.

## Servir ISOs localmente o desde el primario

Con `sync_isos: true` (el valor por defecto) la réplica descarga cada ISO activado desde el primario, extrae kernels e initrds en local y sirve a los clientes desde su propio disco. La primera sincronización de una biblioteca grande tarda un rato; mientras tanto la réplica sigue sirviendo las imágenes que ya tiene, y las sincronizaciones posteriores solo transfieren los archivos cuyo tamaño cambió.

Con `sync_isos: false` la réplica almacena solo los metadatos y apunta las entradas del menú de arranque al puerto de arranque del primario, así que los clientes de la sucursal transmiten los ISOs y archivos de arranque a través de la WAN. Las imágenes que usan los métodos de arranque NBD o NFS necesitan los archivos en local y no arrancarán en este modo. Los scripts de auto-instalación siempre los sirve la propia réplica.

Si una descarga falla, esa imagen recurre a la URL del primario hasta la siguiente sincronización correcta.

## Ancho de banda

- Las sincronizaciones de metadatos son pequeñas listas JSON y se ejecutan en cada intervalo.
- Los ISOs se obtienen una vez y solo se vuelven a obtener cuando su tamaño cambia en el primario.
- Un manifiesto del primario permite a la réplica omitir los tipos que no han cambiado desde su última ejecución.
- Reduce el intervalo solo si necesitas una propagación más rápida; la réplica también tiene un botón **Sync now**.

## Cuando el primario no es accesible

La réplica sigue sirviendo su último estado bueno. La sincronización se marca como fallida, el aviso muestra el error y los tipos posteriores de esa ejecución se omiten en lugar de aplicarse a medias. En una ejecución fallida no se elimina nada en local. La sincronización se reanuda automáticamente cuando el primario vuelve a responder.

## Monitorización

La pestaña **Server Info** de una réplica tiene una tarjeta **Replica** que lista, por tipo, si la última sincronización tuvo éxito, cuántas filas tiene el primario y cuántas cambiaron o se eliminaron. Los mismos datos están disponibles desde la API, y `GET /api/me` incluye un objeto `replica` para que los scripts sepan con qué instancia están hablando.

## API

```bash
# Replica: status of the last sync
curl -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/status

# Replica: sync now
curl -X POST -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/sync

# Primary: what a replica reads
curl -H "Authorization: Bearer $TOKEN" https://hq:8081/api/sync/manifest
```

El primario expone `GET /api/sync/manifest`, `GET /api/sync/tool-files?name=` y `GET /api/sync/asset?kind=iso|tool|file` para las réplicas; los tres aceptan tokens de ámbito read.
