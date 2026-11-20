# 1. Configure and deploy

These templates target Docker Engine on Linux, not Docker Desktop: FUSE mount
propagation must reach the host. Use fixed Compose project names `media`, `arr`,
and (optional) `danmaku` (the backup script uses their labels).

## Prerequisites

1. **A Linux host with Docker** (1+ core, 2GB+ RAM; an Oracle Always Free ARM A1 works well). Use the official Docker Engine repository instructions for the host distribution if absent.
2. **A Real-Debrid subscription** + its API token (`https://real-debrid.com/apitoken`).
3. **A Tailscale account** (for private access) + ability to generate a **reusable, non-ephemeral** auth key (`https://login.tailscale.com/admin/settings/keys`).
4. *(Optional)* Plex account / Plex Pass; a Google account + `rclone` gdrive remote for backups; cloud-drive creds for alist.

Storage note: a **single boot disk is more robust than a separate data volume**. See [troubleshooting.md](troubleshooting.md) ("storage layout") for why — a detached/failed extra volume mount is the classic way this stack breaks.

## Get the files

```bash
git clone https://github.com/s546126/media-stack-setup.git ~/media-stack-setup
REPO=~/media-stack-setup
sudo mkdir -p /mnt/data/media && sudo chown "$(id -u):$(id -g)" /mnt/data/media
cp -rn "$REPO/deploy/." /mnt/data/media/; cd /mnt/data/media
```

`cp -rn` never overwrites a file you have already edited, so it is safe to re-run.
Every later command runs from `/mnt/data/media`.

## Configure

### `.env`

```bash
cp .env.example .env && chmod 600 .env
```

Set `BIND_IP=127.0.0.1` (default), or your actual host Tailscale IPv4 address, in `.env`.
Never set a public/wildcard address. Sidecar profiles also need `TS_AUTHKEY` and a `./<service>-ts/serve.json` for each sidecar, as shown in [tailscale.md](tailscale.md). Jellyfin proxies port 8096, alist 5244. No sidecar profile is enabled by default.

### zurg and rclone

Create the real files from the examples before starting containers:

```bash
cp zurg/config.yml.example zurg/config.yml
cp zurg/rclone.conf.example zurg/rclone.conf
```

- Put your Real-Debrid API token into `zurg/config.yml` (`token:`). The full zurg example is at github.com/debridmediamanager/zurg-testing.
- `zurg/rclone.conf` has a `[zurg]` WebDAV remote. The optional `[alist]` remote is commented out; add its credentials and uncomment it only if you use the alist profile.
- Optional: mount `zurg/scripts/plex_update.sh`, the zurg-provided Plex partial-scan script, and use `http://plex:32400` plus your Plex token. `localhost` inside zurg is zurg itself, not Plex.

Notes on the media stack:

- rclone needs cap_add SYS_ADMIN + /dev/fuse + apparmor:unconfined to FUSE-mount.
- The mount uses :rshared so the FUSE submount propagates to other containers.
- Plex/Jellyfin mount BOTH the app paths AND /mnt:/mnt:rslave — the latter is REQUIRED so the arr stack's symlinks (which point at /mnt/zurg/...) resolve.
- PUID/PGID=1000 should match the owner of the ./ config dirs.
- Enable --profile plex and/or --profile jellyfin; alist is also opt-in.
- Set TS_AUTHKEY in .env only when enabling Tailscale-backed profiles.
- Plex and Jellyfin do the same job — run one or both.

### Sidecar serve.json

For each sidecar profile you enable, copy the template and set its port (Jellyfin 8096, alist 5244):

```bash
mkdir -p jellyfin-ts && sed 's/<PORT>/8096/' serve/app.json > jellyfin-ts/serve.json
```

## Prepare the host

All commands below run from `/mnt/data/media`. Choose images/tags compatible with
your CPU; `latest` is a starting template, not a reproducible release. Record tested
image digests before production use.

```bash
docker network inspect media-playback >/dev/null 2>&1 || docker network create media-playback
sudo mkdir -p /mnt/zurg /mnt/data/media/library/{tv,movies} /mnt/data/cache
# Make the mount a shared mountpoint so container FUSE mounts propagate out.
mountpoint -q /mnt/zurg || sudo mount --bind /mnt/zurg /mnt/zurg
sudo mount --make-rshared /mnt/zurg
findmnt -o TARGET,PROPAGATION /mnt/zurg
test -c /dev/fuse
```

Repeat for `/mnt/alist` if using that profile. Persist mount preparation using a
host boot unit ordered before the Compose services; re-check after reboot.
`depends_on` orders container startup but does not prove the mount is ready.
Do not start library scans until the host and each consumer can read a known
file through `/mnt/zurg`. Never use `--allow-non-empty` to hide a stale mount.
Set ownership of writable library/config/cache directories to the app UID/GID
(the examples use 1000:1000); adjust rclone's `--uid/--gid` together with app IDs.

## Bring up

```bash
docker compose -p media -f docker-compose.yml --profile plex config --quiet
docker compose -p media -f docker-compose.yml up -d zurg rclone
ls /mnt/zurg
# Only after validating an actual file on the mount:
docker compose -p media -f docker-compose.yml --profile plex up -d plex
docker compose -p arr -f docker-compose.arr.yml config --quiet
docker compose -p arr -f docker-compose.arr.yml up -d
```

Choose `--profile jellyfin` instead of or alongside Plex; enable `--profile alist`
only after configuring its WebDAV remote. Plex uses bridge networking and a
private published port. Claim it through an SSH tunnel when using localhost,
and leave Plex Remote Access and router port forwarding disabled.

## Networks

The projects retain separate default networks for their internal traffic. A
shared external `media-playback` bridge connects only Jellyseerr, the selected
media servers and, with the optional danmaku layer, Misaka (so Jellyfin's
sidecar can serve its API under `/danmaku/`). Create it once as above, then set Jellyseerr's internal server URL
to `http://plex:32400` or `http://jellyfin:8096` (the alias belongs to the Jellyfin
sidecar). This connection needs no host public port or Tailscale outbound proxy.
For a zurg Plex scan hook, `http://plex:32400` works on the media default network.
Tailnet users still use the configured private published ports or sidecar HTTPS.

The danmaku layer is set up in [3-danmaku.md](3-danmaku.md), and the wiring of the
arr apps in [2-wire.md](2-wire.md).

## Acceptance on the target host

The first check and the mount read can be done now; the test release comes at the end of [2-wire.md](2-wire.md) and the backup check in [4-backup.md](4-backup.md). Come back to this list before calling the host done.

- Validate Compose, then check logs and authenticated UI access. An HTTP redirect
  alone is insufficient to prove readiness.
- Read one known mounted file inside Decypharr, Sonarr/Radarr, and the media server.
- Add one permitted test release; confirm the final library contains working
  symlinks (not full local copies), and play it through the selected media server.
- Restart rclone and reboot the host; repeat mount and playback checks.
- Run a backup, extract it to a separate directory, and verify an app DB opens.
- Confirm service ports are unavailable from outside the tailnet.

Media is remote, but VFS caches (20G for zurg, optionally 10G for alist), metadata,
transcodes, and backup staging use local disk. Cache limits are not hard quotas
for open files. Allow additional headroom and monitor free space.

Verify: `ls /mnt/zurg` lists files and `docker ps` shows the containers up; otherwise see [troubleshooting.md](troubleshooting.md). Next: [2-wire.md](2-wire.md).
