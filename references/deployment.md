# Deployment and acceptance

These templates target Docker Engine on Linux, not Docker Desktop: FUSE mount
propagation must reach the host. Use fixed Compose project names `media` and
`arr` (the backup script uses their labels).

## Prepare

Copy `compose-media.yml` to `/mnt/data/media/docker-compose.yml` and
`compose-arr.yml` to `/mnt/data/media/docker-compose.arr.yml`. All commands below
run from `/mnt/data/media`. Prepare the zurg configuration and rclone remote as
shown in the media template; create actual files before starting containers.
Choose images/tags compatible with your CPU; `latest` is a starting template,
not a reproducible release. Record tested image digests before production use.

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

Create a mode-600 `.env` with `BIND_IP=127.0.0.1` (default), or your actual host
Tailscale IPv4 address. Never set a public/wildcard address. Sidecar profiles
also need `TS_AUTHKEY` and a `./<service>-ts/serve.json` for each sidecar, as
shown in [tailscale-sidecars.md](tailscale-sidecars.md). Jellyfin proxies port
8096, alist 5244. No sidecar profile is enabled by default.

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

The projects retain separate default networks for their internal traffic. A
shared external `media-playback` bridge connects only Jellyseerr and the selected
media servers. Create it once as above, then set Jellyseerr's internal server URL
to `http://plex:32400` or `http://jellyfin:8096` (the alias belongs to the Jellyfin
sidecar). This connection needs no host public port or Tailscale outbound proxy.
For a zurg Plex scan hook, `http://plex:32400` works on the media default network.
Tailnet users still use the configured private published ports or sidecar HTTPS.

## Acceptance on the target host

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
