# 5. Daily use

Run from `/mnt/data/media`; `REPO` is set as in [1-deploy.md](1-deploy.md).

## Where things are

- Plex: `http://<BIND_IP>:32400`. Arr apps: `http://<BIND_IP>:<port>` (9696, 8989, 7878, 6767, 5055, 8282).
- Jellyfin, Misaka, LibreTV: sidecar only, at `https://jellyfin.<tailnet>.ts.net`, `https://misaka.<tailnet>.ts.net`, `https://libretv.<tailnet>.ts.net`.

## Update images

```bash
# repeat the --profile flags you used at first start
docker compose -p media -f docker-compose.yml --profile plex pull && docker compose -p media -f docker-compose.yml --profile plex up -d
docker compose -p arr -f docker-compose.arr.yml pull && docker compose -p arr -f docker-compose.arr.yml up -d
docker compose -p danmaku -f docker-compose.danmaku.yml pull misaka misaka-db misaka-ts && docker compose -p danmaku -f docker-compose.danmaku.yml up -d
```

`danmu-bridge` and `libretv-danmaku` are built locally, not pulled: rebuild them below.

## After `git pull` of this repo

```bash
cd "$REPO" && git pull
diff -ru "$REPO/deploy" /mnt/data/media   # template changes; .env and zurg files are not in deploy/
```

Copy in what changed by hand (`cp -rn` would skip it), keeping your `.env`. If your `docker-compose.danmaku.yml` still has secrets written inline, first move them into `.env` under the names in the danmaku block of `.env.example` (`MISAKA_DB_PASSWORD` must be the password the database was created with), then copy the new file and run `docker compose -p danmaku -f docker-compose.danmaku.yml config --quiet`. Then:

- Bridge: `docker build -t danmu-bridge:latest "$REPO/danmu-bridge" && docker compose -p danmaku -f docker-compose.danmaku.yml up -d danmu-bridge`
- LibreTV: `docker build -t libretv-danmaku:latest "$REPO/libretv" && docker compose -p danmaku -f docker-compose.danmaku.yml --profile libretv up -d libretv`
- Jellyfin injector: `install -o root -g root -m 755 "$REPO/jellyfin/inject-danmaku.sh" jellyfin-init/ && docker compose -p media -f docker-compose.yml --profile jellyfin restart jellyfin`

## 弹幕 by hand

`docker exec danmu-bridge danmu-bridge sync` writes the xml files now. `docker exec danmu-bridge danmu-bridge backfill -limit 5` re-submits a few items. For one series after fixing its episode mapping, see [3-danmaku.md](3-danmaku.md) §4 and §7.

## Backups and problems

`scripts/config-backup.sh` runs from cron; check its private log after each run ([4-backup.md](4-backup.md)). Anything broken: [troubleshooting.md](troubleshooting.md).
