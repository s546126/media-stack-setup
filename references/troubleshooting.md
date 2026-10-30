# Troubleshooting & hard-won gotchas

These are real failure modes from running this stack. Most are subtle and cost
hours if you don't know them.

## Storage layout — prefer a single disk

The media never lands on disk (it streams from RD), so you don't need a big data
volume. A **separate block volume is a liability**: on cloud hosts an iSCSI data
volume mounted via `/etc/fstab` with `_netdev` (and no `nofail`) can silently
**fail to mount after a reboot**. When that happens, anything configured to write
under the mountpoint (e.g. docker's `data-root` at `/mnt/data/docker`) quietly
writes to the **boot disk** instead, filling it up — and your containers' real
data ends up split between a stale unmounted volume and the boot disk.

Recommendation: keep everything on the **boot disk** and, if you want more room,
**grow the boot volume** rather than attach a second one. To expand an attached
boot volume online (Oracle/most clouds): resize in the cloud console/CLI, then on
the host: rescan the disk, `growpart /dev/sda 3`, `pvresize`, `lvextend -l
+100%FREE`, `xfs_growfs /`.

## Symlink path consistency (the #1 pipeline bug)

Decypharr creates **symlinks** whose targets are absolute paths like
`/mnt/zurg/__all__/<torrent>/<file>`. For a symlink to resolve inside a
container, that container must see **both** the symlink dir **and** the target
path at the **same absolute path**. That's why every consumer — Sonarr, Radarr,
Plex, Jellyfin, Decypharr — mounts `/mnt:/mnt:rslave`, not just app-specific
subpaths. If Plex mounts the RD library at `/data` but the symlink points at
`/mnt/zurg/...`, playback sees a **broken symlink**. Symptom: file appears in
`/mnt/zurg` and a symlink exists in the library, but the media server shows
nothing / "unavailable".

`:rshared`/`:rslave` matters too: `/mnt/zurg` is a FUSE submount created by the
rclone container *after* start; without rshared propagation from the host (and
rslave into consumers) the submount won't appear inside other containers.

## FUSE mount won't start

The rclone container needs `cap_add: [SYS_ADMIN]`, `devices:
[/dev/fuse:/dev/fuse:rwm]`, and `security_opt: ["apparmor:unconfined"]`. Missing
any of these → `fusermount: exec: permission denied` or an empty `/mnt/zurg`.

## containerd / docker data relocation via symlink

If you relocate container storage with a symlink (e.g. `/var/lib/containerd ->
/mnt/data/containerd`) and the target's underlying mount is lost, the symlink
becomes **dangling** and containerd starts with an empty store — images/containers
"vanish". Check `readlink -f /var/lib/containerd` resolves to a populated dir.
Note: when docker uses the **containerd image store** (no `overlay2` dir under the
docker data-root), image layers live under **containerd's** root, not docker's —
back up / migrate the right directory.

## SELinux: 203/EXEC after restoring files

On SELinux-enforcing hosts (Oracle Linux, RHEL, Fedora), restoring files from a
tar made **without** `--selinux`/`--xattrs` loses SELinux labels. If a restored
**binary** (e.g. `/usr/bin/python3.9`) gets a wrong type label like
`user_home_t`, systemd services that exec it fail with `status=203/EXEC` ("Permission
denied") — even though running it manually via `sudo` works (unconfined domain).
Tell-tale: many *Python-based* services fail at once (dnf, firewalld, tuned,
cloud-init, setroubleshootd) while Go binaries (dockerd) are fine. Fix:
`sudo restorecon -v /usr/bin/python3.9` (or `restorecon -Rv /usr`). When backing
up system files, use `tar --selinux --xattrs --acls` so labels survive.

## docker ps hangs / containers "activating"

Usually the data-root is unavailable (see storage/symlink items above) or the
daemon is wedged. Check `systemctl status docker containerd`, confirm the
data-root path exists and is populated, then restart containerd before docker.
Ensure both are `enabled` for boot (docker pulls containerd in via `Wants=`, but
enable containerd explicitly to be safe).

## Stale iSCSI device after deleting a cloud volume

After detaching/deleting a block volume in the cloud, the host may still show a
phantom `/dev/sdX` with a FAILED iSCSI session, which blocks `growpart`/LVM
scans. Confirm the boot disk is NOT iSCSI (`readlink -f /sys/block/sda` →
`virtio`), then log out the dead session:
`sudo iscsiadm -m node -T <iqn> -p <portal> -u` and `... -o delete ...`.

## Decypharr won't pick up the debrid / symlinks empty

- Container logs should show `debrids=1` and "Initial sync ... completed". If
  `debrids=0`, the `debrids` block in `config.json` is missing/malformed (or you
  edited the UI but didn't save). A malformed `config.json` can stop the
  container — keep the `.bak` and revert.
- `folder` must point at where zurg exposes torrents by name (`/mnt/zurg/__all__`).
- `default_download_action` should be `symlink` (not copy — copying defeats the
  no-local-storage design).

## 弹幕 (danmaku) layer

- **Webhook test fails / nothing happens on import**: Sonarr/Radarr and
  `danmu-bridge` must share `arr_default` (`docker network inspect arr_default`
  lists both). The danmaku project declares that network `external`, so bring
  the arr stack up first. Check `docker logs danmu-bridge` for a 401 (secret
  mismatch) vs. no request at all (network/URL).
- **Bridge submits but Misaka imports nothing**: Misaka's task list shows the
  reason. Usual causes: no TMDB key in 元信息搜索源 (id search can't resolve a
  title); all danmaku sources disabled; or the sources are geo-blocked from a
  non-China host — configure `设置 → 代理配置`. "Duplicate task" refusals within
  3h are Misaka's `externalApiDuplicateTaskThresholdHours`, not an error.
- **Wrong episodes' danmaku (anime)**: TVDB vs. site numbering. Fix with a
  本地剧集组 or a 识别词 offset in Misaka, then resubmit — see wiring.md §7d.
- **xml lands in the wrong place / not next to the video**: the sync writes
  beside the **symlink** under `/mnt/data/media/library`, never under
  `/mnt/zurg` (read-only FUSE; writes there fail or vanish). The bridge must
  see `/mnt` at `/mnt` (same path rule as everything else here).
- **xml files flip-flop or duplicate**: two writers. Remove any Jellyfin
  danmaku plugin that downloads its own xml, and keep Misaka's own media-server
  sync off — the bridge is the only writer.
- **Jellyfin web shows no danmaku but SenPlayer does**: the dd-danmaku script
  is calling `http://...` from the https Jellyfin page (mixed content, blocked
  silently) or an IP the browser can't reach. Use
  `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>`. If the browser console shows
  a CORS error, that's between the script and Misaka — check both projects'
  current notes; this repo hasn't verified CORS behavior.
- **Misaka won't start**: it needs MySQL or Postgres (no SQLite). With
  `postgres:18` the volume is `/var/lib/postgresql` (not `.../data`); a wrong
  path or a dir not owned by 1000:1000 shows up in `docker logs misaka-db`.

## Security hygiene

- Never expose service ports on `0.0.0.0` on a public-IP host. Bind to the
  tailscale IP or use sidecars. (A firewall may also be down — don't rely on it.)
- The RD token, Tailscale auth key, `rclone.conf`, and Plex token are secrets.
  Reference them via env/files; don't print or commit them. After the sidecars
  have registered their nodes, you can revoke a reusable auth key for hygiene.
- A single-host autonomous maintenance agent with broad shell access also fetches
  web/metadata (a prompt-injection surface) — give it explicit boundaries and
  treat external content as untrusted.
