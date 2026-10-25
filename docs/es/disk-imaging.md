# Imágenes de disco

Bootimus puede capturar el disco de un cliente en una imagen y desplegar esa imagen de vuelta a un cliente o a todos los miembros de un grupo de clientes. Controla de forma desatendida la herramienta **Clonezilla Live** incluida, así que el flujo de trabajo es el que la gente conoce de FOG: encolar un trabajo, dejar que la máquina arranque por PXE y leer el resultado en la consola de administración.

## Tabla de contenidos

- [Cómo funciona](#cómo-funciona)
- [Requisitos previos](#requisitos-previos)
- [Capturar un disco](#capturar-un-disco)
- [Desplegar una imagen](#desplegar-una-imagen)
- [Trabajos](#trabajos)
- [Dónde viven las imágenes](#dónde-viven-las-imágenes)
- [Exposición en la red](#exposición-en-la-red)
- [Solución de problemas](#solución-de-problemas)

## Cómo funciona

1. Encolas un trabajo de **captura** o **despliegue** para un cliente desde la consola de administración o la API. Cada trabajo lleva un token aleatorio.
2. En el siguiente arranque PXE del cliente, el menú salta directamente a una entrada de Clonezilla construida con el kernel, el initrd y el squashfs de la herramienta Clonezilla.
3. Clonezilla obtiene un breve script de trabajo de Bootimus (`/diskimage/job?token=…`), monta el almacén de imágenes por NFS, ejecuta `ocs-sr` e informa de vuelta a `/diskimage/callback`.
4. Una captura apaga el cliente al terminar. Un despliegue lo reinicia en el disco recién restaurado.

El trabajo solo se sirve a la dirección MAC para la que fue encolado, y un trabajo terminado, fallido o cancelado nunca se vuelve a servir.

## Requisitos previos

- **Herramienta Clonezilla**: descárgala y actívala en **Tools**. La página de imágenes de disco te avisa cuando falta.
- **Servidor NFS**: arranca Bootimus con `--nfs-enabled` (o `nfs_enabled: true` en `bootimus.yaml`). Clonezilla necesita un recurso compartido con escritura donde guardar, y el servidor NFS integrado lo proporciona sin paquetes adicionales.
- **Arranque por kernel** para el cliente (el valor por defecto). Las imágenes de disco no funcionan con sanboot ni memdisk.
- El puerto `2049/tcp` (o el `--nfs-port` configurado) accesible desde los clientes, junto con los puertos habituales de TFTP y HTTP.

## Capturar un disco

1. Abre **Disk Images** y pulsa **Capture Disk…**, o pulsa **Capture Disk** en la fila de un cliente en **Clients**.
2. Elige el cliente, dale un nombre a la imagen (letras minúsculas, dígitos, puntos, guiones y guiones bajos) y elige el disco. `sda` es el valor por defecto; `nvme0n1` y similares también funcionan, y `auto` elige el primer disco fijo y escribible que Clonezilla encuentre.
3. Opcionalmente marca **Send Wake-on-LAN now**.
4. Reinicia el cliente (o deja que el paquete mágico lo haga). Arranca Clonezilla, guarda el disco y se apaga.

Clonezilla ejecuta `ocs-sr -q2 -j2 -z1p -i 4096 -sc savedisk <name> <disk>`: partclone donde sea posible, datos ocultos entre el MBR y la primera partición incluidos, compresión gzip multihilo y división en volúmenes de 4 GiB.

Mientras la captura se ejecuta, la imagen se muestra como **capturing**. Cuando el cliente informa del éxito, la imagen pasa a **ready** y se registra su tamaño. Si `ocs-sr` falla, la imagen se marca como **failed** y los datos parciales se descartan.

**Vía API**:
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","name":"win11-lab","description":"Lab build, Oct 2026","disk":"sda","wake":true}' \
  http://localhost:8081/api/disk-images/capture
```

## Desplegar una imagen

1. Pulsa **Deploy** junto a una imagen lista, **Deploy Image** en la fila de un cliente o **Deploy Disk Image to All…** en las acciones en masa de un grupo de clientes.
2. Elige el destino (un cliente o un grupo de clientes), el disco a sobrescribir y si enviar Wake-on-LAN. Los despliegues de grupo respetan la dirección de broadcast WOL y el retardo escalonado del grupo.
3. Confirma. Todo lo que hay en el disco de destino se reemplaza.

Clonezilla ejecuta `ocs-sr -g auto -e1 auto -e2 -r -j2 -scr restoredisk <name> <disk>`: la tabla de particiones se recrea a partir de la imagen, las particiones se amplían para llenar un disco mayor, GRUB se reinstala donde la imagen lo tenía y el cliente se reinicia al terminar.

Cada miembro de un grupo recibe su propio trabajo, así que puedes ver cómo terminan uno a uno. Un cliente que ya tiene un trabajo activo se omite y se indica en la respuesta.

**Vía API**:
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

## Trabajos

La tabla **Capture and Deploy Jobs** muestra cada trabajo con su estado:

| Estado | Significado |
|--------|-------------|
| `pending` | Encolado; esperando a que el cliente arranque por PXE |
| `running` | Clonezilla ha obtenido el script de trabajo y está trabajando |
| `completed` | El cliente informó del éxito |
| `failed` | Falló el montaje u `ocs-sr`; la columna de mensaje indica el motivo |
| `cancelled` | Cancelado desde la consola o la API |

Los trabajos pendientes y en ejecución se pueden cancelar. Cancelar una captura descarta la imagen que se estaba escribiendo. Un cliente solo puede tener un trabajo activo a la vez.

Las previsualizaciones del menú de arranque (**Preview Menu** en la fila de un cliente) muestran la entrada de arranque de Clonezilla mientras un trabajo está activo, lo que es una forma rápida de comprobar qué hará el cliente.

Los trabajos terminados disparan los eventos webhook `disk.capture_completed`, `disk.capture_failed`, `disk.deploy_completed` y `disk.deploy_failed` cuando esa opción está activada en **Settings → Webhook**.

## Dónde viven las imágenes

Las imágenes son directorios de imagen de Clonezilla normales en `<data-dir>/disk-images/<name>/`, así que se pueden inspeccionar, copiar a otro sitio o restaurar a mano desde cualquier memoria USB de Clonezilla Live. Las capturas en curso se escriben en `<data-dir>/disk-images/.staging/<token>/` y se mueven a su sitio cuando el cliente informa del éxito.

Las imágenes de disco son grandes, así que vigila la tarjeta **Housekeeping** en **Settings**, que muestra el espacio que usa cada entrada de primer nivel del directorio de datos. Las copias de seguridad hechas desde la consola de administración omiten los directorios `isos` y `tools` pero incluyen todo lo demás, así que elimina las imágenes de disco que ya no necesites antes de descargar una copia de seguridad.

## Exposición en la red

Clonezilla monta el almacén de imágenes por NFS v3 sin autenticación, igual que funcionan FOG y DRBL. Bootimus limita lo que ese recurso compartido expone:

- la exportación de arranque normal (`isos/`) y la raíz de las imágenes de disco son de solo lectura;
- un trabajo de captura obtiene su propia exportación escribible en `/disk-images/.staging/<token>`, que solo existe mientras ese trabajo está activo y no se puede adivinar sin el token;
- los directorios de imagen individuales no se pueden montar directamente; los despliegues montan la raíz de imágenes de solo lectura.

Ejecuta la red de arranque en un segmento en el que confíes, como ya deberías hacer para PXE.

## Solución de problemas

**El trabajo se queda en pending.** El cliente no ha arrancado por PXE desde que se encoló el trabajo, o arrancó pero Clonezilla no está activado y Bootimus sirvió el menú normal. Revisa **Tools** y después **Preview Menu** en la fila del cliente.

**`nfs_mount_failed` en la columna de mensaje.** El cliente no pudo montar el recurso compartido. Confirma `--nfs-enabled`, que el puerto NFS es accesible desde la subred del cliente y que `--server-addr` es una dirección que el cliente puede alcanzar.

**`no_disk_found` con `disk=auto`.** Clonezilla no vio ningún disco fijo y escribible. Pasa el nombre del dispositivo explícitamente.

**`ocs-sr_exit_N`.** Clonezilla falló por sí mismo. El cliente muestra la salida de Clonezilla durante 30 segundos antes de reiniciar; las causas habituales son capturar un disco con un sistema de archivos montado o dañado, o desplegar en un disco más pequeño que la imagen.

**La imagen se muestra como failed.** No canceles nada; elimínala desde la página **Disk Images** y captura de nuevo.
