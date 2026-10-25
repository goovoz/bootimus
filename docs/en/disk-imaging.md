# Disk Imaging

Bootimus can capture a client's disk into an image and deploy that image back to one client or to every member of a client group. It drives the bundled **Clonezilla Live** tool unattended, so the workflow is the one people know from FOG: queue a job, let the machine PXE boot, and read the result in the admin console.

## Table of Contents

- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Capturing a disk](#capturing-a-disk)
- [Deploying an image](#deploying-an-image)
- [Jobs](#jobs)
- [Where images live](#where-images-live)
- [Network exposure](#network-exposure)
- [Troubleshooting](#troubleshooting)

## How it works

1. You queue a **capture** or **deploy** job for a client from the admin console or the API. Each job carries a random token.
2. On the client's next PXE boot the menu skips straight to a Clonezilla entry built from the Clonezilla tool's kernel, initrd and squashfs.
3. Clonezilla fetches a short job script from Bootimus (`/diskimage/job?token=…`), mounts the image store over NFS, runs `ocs-sr` and reports back to `/diskimage/callback`.
4. A capture powers the client off when it finishes. A deploy reboots it into the freshly restored disk.

The job is only ever served to the MAC address it was queued for, and a finished, failed or cancelled job is never served again.

## Prerequisites

- **Clonezilla tool**: download and enable it under **Tools**. The disk imaging page tells you when it is missing.
- **NFS server**: start Bootimus with `--nfs-enabled` (or `nfs_enabled: true` in `bootimus.yaml`). Clonezilla needs a writable share to save into, and the built-in NFS server provides it without any extra packages.
- **Kernel boot** for the client (the default). Disk imaging does not work with sanboot or memdisk.
- Port `2049/tcp` (or the configured `--nfs-port`) reachable from clients, alongside the usual TFTP and HTTP ports.

## Capturing a disk

1. Open **Disk Images** and click **Capture Disk…**, or click **Capture Disk** on a client's row under **Clients**.
2. Pick the client, give the image a name (lower-case letters, digits, dots, dashes and underscores) and choose the disk. `sda` is the default; `nvme0n1` and friends work too, and `auto` picks the first fixed, writable disk Clonezilla finds.
3. Optionally tick **Send Wake-on-LAN now**.
4. Reboot the client (or let the magic packet do it). It boots Clonezilla, saves the disk and powers off.

Clonezilla runs `ocs-sr -q2 -j2 -z1p -i 4096 -sc savedisk <name> <disk>`: partclone where possible, hidden data between the MBR and the first partition included, multithreaded gzip compression, 4 GiB volume splits.

While the capture runs the image shows as **capturing**. When the client reports success the image becomes **ready** and its size is recorded. If `ocs-sr` fails the image is marked **failed** and the partial data is discarded.

**Via API**:
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mac_address":"aa:bb:cc:dd:ee:ff","name":"win11-lab","description":"Lab build, Oct 2026","disk":"sda","wake":true}' \
  http://localhost:8081/api/disk-images/capture
```

## Deploying an image

1. Click **Deploy** next to a ready image, **Deploy Image** on a client row, or **Deploy Disk Image to All…** in a client group's bulk actions.
2. Choose the target (one client or a client group), the disk to overwrite and whether to send Wake-on-LAN. Group deploys honour the group's WOL broadcast address and stagger delay.
3. Confirm. Everything on the target disk is replaced.

Clonezilla runs `ocs-sr -g auto -e1 auto -e2 -r -j2 -scr restoredisk <name> <disk>`: the partition table is recreated from the image, partitions are grown to fill a larger disk, GRUB is reinstalled where the image had it, and the client reboots when done.

Each member of a group gets its own job, so you can watch them finish one by one. A client that already has an active job is skipped and listed in the response.

**Via API**:
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

The **Capture and Deploy Jobs** table shows every job with its state:

| State | Meaning |
|-------|---------|
| `pending` | Queued; waiting for the client to PXE boot |
| `running` | Clonezilla has fetched the job script and is working |
| `completed` | The client reported success |
| `failed` | Mount or `ocs-sr` failed; the message column has the reason |
| `cancelled` | Cancelled from the console or API |

Pending and running jobs can be cancelled. Cancelling a capture discards the image that was being written. A client can only have one active job at a time.

Boot menu previews (**Preview Menu** on a client row) show the Clonezilla boot entry while a job is active, which is a quick way to check what the client will do.

Finished jobs fire the `disk.capture_completed`, `disk.capture_failed`, `disk.deploy_completed` and `disk.deploy_failed` webhook events when that option is enabled under **Settings → Webhook**.

## Where images live

Images are plain Clonezilla image directories under `<data-dir>/disk-images/<name>/`, so they can be inspected, copied elsewhere, or restored by hand from any Clonezilla Live USB stick. Captures in progress are written under `<data-dir>/disk-images/.staging/<token>/` and moved into place when the client reports success.

Disk images are large, so keep an eye on the **Housekeeping** card under **Settings**, which shows the space each top-level data directory entry uses. Backups made from the admin console skip the `isos` and `tools` directories but include everything else, so delete disk images you no longer need before downloading a backup.

## Network exposure

Clonezilla mounts the image store over NFS v3 with no authentication, the same way FOG and DRBL work. Bootimus limits what that share exposes:

- the normal boot export (`isos/`) and the disk image root are read-only;
- a capture job gets its own writable export at `/disk-images/.staging/<token>`, which exists only while that job is active and is unguessable without the token;
- individual image directories cannot be mounted directly; deploys mount the read-only image root.

Run the boot network on a segment you trust, as you already should for PXE.

## Troubleshooting

**The job stays pending.** The client has not PXE booted since the job was queued, or it booted but Clonezilla is not enabled so Bootimus served the normal menu. Check **Tools**, then **Preview Menu** on the client row.

**`nfs_mount_failed` in the message column.** The client could not mount the share. Confirm `--nfs-enabled`, that the NFS port is reachable from the client's subnet, and that `--server-addr` is an address the client can reach.

**`no_disk_found` with `disk=auto`.** Clonezilla saw no fixed, writable disk. Pass the device name explicitly.

**`ocs-sr_exit_N`.** Clonezilla itself failed. The client shows the Clonezilla output for 30 seconds before rebooting; a capture of a disk with a mounted or damaged filesystem, or a deploy to a disk smaller than the image, are the usual causes.

**The image shows as failed.** Cancel nothing; delete it from the **Disk Images** page and capture again.
