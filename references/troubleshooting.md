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

## Security hygiene

- Never expose service ports on `0.0.0.0` on a public-IP host. Bind to the
  tailscale IP or use sidecars. (A firewall may also be down — don't rely on it.)
- The RD token, Tailscale auth key, `rclone.conf`, and Plex token are secrets.
  Reference them via env/files; don't print or commit them. After the sidecars
  have registered their nodes, you can revoke a reusable auth key for hygiene.
- A single-host autonomous maintenance agent with broad shell access also fetches
  web/metadata (a prompt-injection surface) — give it explicit boundaries and
  treat external content as untrusted.
