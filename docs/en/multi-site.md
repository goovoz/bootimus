# Multi-Site Deployments

Run one Bootimus as the **primary** where you manage everything, and a **replica** at each branch site that mirrors the primary and boots local clients from local copies. Replicas are read-only: every change is made on the primary and arrives at the replicas on their next sync.

## Table of Contents

- [Topology](#topology)
- [Set up the primary](#set-up-the-primary)
- [Set up a replica](#set-up-a-replica)
- [What syncs and what does not](#what-syncs-and-what-does-not)
- [Serving ISOs locally or from the primary](#serving-isos-locally-or-from-the-primary)
- [Bandwidth](#bandwidth)
- [When the primary is unreachable](#when-the-primary-is-unreachable)
- [Monitoring](#monitoring)
- [API](#api)

## Topology

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

Each replica keeps its own database and data directory, so a branch keeps booting from its last good copy even when the WAN link is down. Replicas never write to the primary.

## Set up the primary

Nothing changes on the primary beyond creating a token for each replica:

1. Open **Users** → **API Tokens** and create a token named after the site.
2. Choose the scope:
   - `read` is enough for clients, images, groups, SAN targets, the theme, custom profiles, tools and custom files.
   - `admin` additionally syncs **auto-install files** and the BMC and CHAP passwords. With a `read` token those passwords arrive redacted and the replica keeps whatever it already has.
3. Copy the `btk_...` secret; it is shown once.

Serve the primary's admin interface over HTTPS (`--admin-tls`, see the [deployment guide](deployment.md#securing-the-admin-interface)) so the token does not travel in clear text.

## Set up a replica

Install Bootimus at the branch as usual, then point it at the primary:

```bash
bootimus serve \
  --replica-primary https://bootimus.hq.example.com:8081 \
  --replica-token btk_... \
  --replica-interval 5m
```

Or in `bootimus.yaml`:

```yaml
replica:
  primary_url: https://bootimus.hq.example.com:8081
  token: btk_...
  interval: 5m
  sync_isos: true
```

The environment variables are `BOOTIMUS_REPLICA_PRIMARY_URL`, `BOOTIMUS_REPLICA_TOKEN`, `BOOTIMUS_REPLICA_INTERVAL` and `BOOTIMUS_REPLICA_SYNC_ISOS`. The minimum interval is 30 seconds.

The replica syncs once at start-up and then on every interval. Its admin interface shows a banner naming the primary and the time of the last successful sync, and every create, edit or delete answers `409 Conflict` with a pointer to the primary. Local users and API tokens can still be managed on the replica so you can log in there.

Enable `--proxy-dhcp` on the replica if the branch has no PXE-aware DHCP server, exactly as you would for a standalone install.

## What syncs and what does not

| Synced from the primary | Kept local to each instance |
|-------------------------|-----------------------------|
| Clients (name, group, allowed images, BMC settings, auto-install file, SAN target) | Boot logs, boot counts and last-boot times |
| Client groups | Hardware inventory |
| Images and image groups (metadata, boot method, boot parameters, WinPE selection) | Next-boot actions and Wake-on-LAN |
| SAN targets | Discovered clients that have only ever booted at the branch |
| Boot menu theme | Install callbacks and status |
| Custom distro profiles | Users, API tokens and the audit log |
| Enabled boot tools and their files | Scheduled tasks and webhooks |
| Auto-install files (admin token only) | Kubernetes clusters and node enrolment |
| Custom files | Housekeeping settings |
| The active bootloader set name (the set's files must already exist on the replica) | Custom bootloader set files |

Rows that disappear from the primary are removed from the replica, with one exception: clients that were discovered at the branch and never promoted on the primary are left alone. Next-boot actions are deliberately not mirrored, because a one-shot action consumed at the branch would otherwise be re-applied on the next sync.

## Serving ISOs locally or from the primary

With `sync_isos: true` (the default) the replica downloads every enabled ISO from the primary, extracts kernels and initrds locally, and serves clients from its own disk. The first sync of a large library takes a while; the replica keeps serving the images it already has in the meantime, and later syncs only transfer files whose size changed.

With `sync_isos: false` the replica stores only the metadata and points boot menu entries at the primary's boot port, so clients at the branch stream ISOs and boot files across the WAN. Images using the NBD or NFS boot methods need the files locally and will not boot in this mode. Auto-install scripts are always served by the replica itself.

If a download fails, that image falls back to the primary's URL until the next successful sync.

## Bandwidth

- Metadata syncs are small JSON lists and run every interval.
- ISOs are fetched once and re-fetched only when their size changes on the primary.
- A manifest from the primary lets the replica skip kinds that have not changed since its last run.
- Lower the interval only if you need faster propagation; the replica also has a **Sync now** button.

## When the primary is unreachable

The replica keeps serving its last good state. The sync is marked failed, the banner shows the error, and later kinds in that run are skipped rather than half-applied. Nothing is deleted locally on a failed run. Syncing resumes automatically when the primary answers again.

## Monitoring

The **Server Info** tab on a replica has a **Replica** card listing, per kind, whether the last sync succeeded, how many rows the primary has and how many changed or were removed. The same data is available from the API, and `GET /api/me` includes a `replica` object so scripts can tell which instance they are talking to.

## API

```bash
# Replica: status of the last sync
curl -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/status

# Replica: sync now
curl -X POST -H "Authorization: Bearer $TOKEN" https://branch:8081/api/replica/sync

# Primary: what a replica reads
curl -H "Authorization: Bearer $TOKEN" https://hq:8081/api/sync/manifest
```

The primary exposes `GET /api/sync/manifest`, `GET /api/sync/tool-files?name=` and `GET /api/sync/asset?kind=iso|tool|file` for replicas; all three accept read-scope tokens.
