# 弹幕 (danmaku): Misaka + Sonarr/Radarr bridge (optional)

The trigger is **Sonarr/Radarr**, not the media server: their webhook already
carries the TMDB/TVDB id, season/episode and the file's full path, so the
bridge can name `<video>.xml` exactly and never has to guess from filenames.
Jellyfin/Plex only play.

```text
How it fits (Jellyfin/Plex only PLAY; they don't trigger anything here):
   new imports : Sonarr/Radarr "On Import" webhook → danmu-bridge → Misaka
                 (matched by TMDB/TVDB id + season/episode)
   existing    : danmu-bridge backfill pulls every item from the Sonarr/Radarr
                 APIs and submits them to Misaka
   files       : Misaka keeps the XML; danmu-bridge's sync writes
                 <video name>.xml next to each symlink under the library
                 (local disk — never into the /mnt/zurg FUSE mount)
   playback    : Jellyfin web: the jellyfin-danmaku script is injected into
                 Jellyfin's own page and calls https://jellyfin.<tailnet>.ts.net
                 /danmaku/…, which the jellyfin-ts sidecar proxies to
                 http://misaka:7768/api/v1/<TOKEN> (same origin, no CORS, token
                 stays server-side; misaka-ts joins media-playback for this).
                 dandanplay-compatible apps (SenPlayer, Hills, …) call Misaka's
                 API over Tailscale: https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>
   libretv     : (optional) LibreTV plays third-party sources in the browser;
                 its server asks Misaka by title + episode number for danmaku
                 (http://misaka:7768, token stays server-side)
```

**Prerequisite:** steps 1–2 done (arr stack running, `media-playback` network exists), `TS_AUTHKEY` set in `.env`, and `REPO` set as in [1-deploy.md](1-deploy.md).

## 1. Deploy the Misaka layer

In `.env` set `MISAKA_DB_PASSWORD` (choose one; it cannot be changed after first start without resetting `misaka-db/`) and `TS_AUTHKEY` (see [tailscale.md](tailscale.md); the Misaka sidecar always needs it, since `misaka-ts` has no profile). If your `.env` predates this layer, copy the `# --- danmaku ---` block from `.env.example` into it.

Run from `/mnt/data/media`, where `deploy/` was copied in step 1:

```bash
cd /mnt/data/media
mkdir -p misaka-config misaka-db misaka-ts danmu-bridge-data
chown 1000:1000 misaka-db danmu-bridge-data
sed 's/<PORT>/7768/' serve/app.json > misaka-ts/serve.json   # Misaka's sidecar proxies to 127.0.0.1:7768
docker build -t danmu-bridge:latest "$REPO/danmu-bridge"     # built locally; no published image
docker compose -p danmaku -f docker-compose.danmaku.yml config --quiet
docker compose -p danmaku -f docker-compose.danmaku.yml up -d misaka-db misaka-ts misaka
```

The bridge is started in section 2, once its API key is in `.env`.

## 2. Misaka first boot (UI, `https://misaka.<tailnet>.ts.net`)

- Admin password: `docker logs misaka 2>&1 | grep -i password` — log in as
  `admin`, change it.
- `弹幕源` → enable the danmaku sources you want; `元信息搜索源` → TMDB → paste
  a TMDB API key (needed for id-based matching; add TVDB too if Sonarr uses TVDB ids).
  A source that is not enabled here makes Misaka answer 400; the bridge log names it.
- `外部控制` → `API 密钥` → generate → this is `MISAKA_API_KEY` for the bridge.
- `弹幕` → `Token管理` → create one token per player/person → used in the
  player URL `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>`.
- Leave Misaka's own media-server sync / Jellyfin webhook **off** — the
  bridge is the only trigger; two triggers just queue duplicate imports.
- Misaka scrapes Chinese video sites. From a non-China host some sources
  geo-block or time out — set `设置` → `代理配置` if your sources come back empty.

In `.env` set `MISAKA_API_KEY` (the 外部控制 key from above), `SONARR_API_KEY` and `RADARR_API_KEY` (same keys as `$SK`/`$RK` in [2-wire.md](2-wire.md)), and `WEBHOOK_SECRET` (choose one, e.g. `openssl rand -hex 16`; an empty value disables webhook authentication). Then start the bridge:

```bash
docker compose -p danmaku -f docker-compose.danmaku.yml up -d danmu-bridge
```

What it does, every env var and its limits: [`danmu-bridge/README.md`](../danmu-bridge/README.md).

## 3. Sonarr/Radarr → bridge webhooks

`Settings → Connect → + → Webhook`, URL `http://danmu-bridge:8090/webhook/sonarr`
(Radarr: `/webhook/radarr`), method POST, username `danmu` (any), password =
the `WEBHOOK_SECRET` value from `.env`. Triggers: **On File Import**, **On File Upgrade** (these submit
to Misaka), plus **On Rename**, **On Episode/Movie File Delete**, **On
Series/Movie Delete** (these only re-run the xml sync, so renamed videos get
their xml moved and deleted ones lose theirs). **On Import Complete** is optional; ticking it too is harmless, since duplicate submissions are deduplicated. Via API:

```bash
WS=<YOUR_WEBHOOK_SECRET>   # same value as WEBHOOK_SECRET in .env
curl -s -X POST "http://$HOST:8989/api/v3/notification" -H "X-Api-Key: $SK" \
 -H 'Content-Type: application/json' -d '{"name":"danmu-bridge",
 "implementation":"Webhook","configContract":"WebhookSettings",
 "onDownload":true,"onUpgrade":true,"onRename":true,
 "onEpisodeFileDelete":true,"onSeriesDelete":true,
 "fields":[{"name":"url","value":"http://danmu-bridge:8090/webhook/sonarr"},
 {"name":"method","value":1},{"name":"username","value":"danmu"},
 {"name":"password","value":"'$WS'"}]}'
# Radarr: same body on :7878 with $RK, url .../webhook/radarr,
#         "onMovieFileDelete":true,"onMovieDelete":true instead of the episode/series ones
```
Press **Test** (or `POST .../api/v3/notification/test`): `docker logs danmu-bridge`
should show `webhook test received from=sonarr`. `GET http://danmu-bridge:8090/healthz`
returns `ok`. If basic auth is awkward, `.../webhook?token=<WEBHOOK_SECRET>` also works.

## 4. Backfill the existing library (once)

```bash
docker exec danmu-bridge danmu-bridge backfill -dry-run    # list what would be submitted
docker exec danmu-bridge danmu-bridge backfill -limit 5    # try a few first
docker exec danmu-bridge danmu-bridge backfill             # everything (add -only tv|movie to split)
```
It walks every series/movie that has files in Sonarr/Radarr and submits them
to Misaka by id + season/episode, one at a time (`BACKFILL_INTERVAL`, default
20s, apart), waiting for each Misaka task. Progress is saved, so a restart or
Ctrl-C just resumes; `-reset` starts over. On a big library this runs for a
long time. The xml files appear on the next sync (every `SYNC_INTERVAL`, default
5m; `docker exec danmu-bridge danmu-bridge sync` runs one now). Backfill shares Misaka's search lock with the running bridge; a collision is retried automatically.

To run only the series, with a 30-second pause between submissions: `docker exec danmu-bridge danmu-bridge backfill -only tv -interval 30s`.

## 5. Jellyfin web: in-player 弹幕

**Jellyfin web, embedded** (also the Jellyfin Android app with its web
  player, and Jellyfin iOS): 弹幕 shows inside Jellyfin's own player. Nothing is
  installed per device and nobody is sent to another site. Two pieces:
  the [jellyfin-danmaku](https://github.com/Izumiko/jellyfin-danmaku) script
  (Izumiko, MIT; the Jellyfin port; the chen3861229 / l429609201 dd-danmaku
  scripts are for Emby) is injected into Jellyfin's page at container start, and
  Jellyfin's own sidecar serves Misaka's API under `/danmaku/` on the same
  origin. The browser calls
  `https://jellyfin.<tailnet>.ts.net/danmaku/api/v2/…`; tailscale serve strips
  `/danmaku` and forwards to `http://misaka:7768/api/v1/<TOKEN>/api/v2/…`. Same
  origin means no CORS and no mixed content, and the token never reaches the
  browser.
  1. Misaka: `弹幕` → `Token管理` → create a token just for Jellyfin.
  2. Install the Jellyfin serve.json template. It replaces the file from step 1 and adds a `/danmaku/` handler:
     ```bash
     install -m 600 serve/jellyfin-danmaku.json jellyfin-ts/serve.json
     ```
     Then edit `jellyfin-ts/serve.json` and replace `<TOKEN>` with the token from item 1. The file now holds a secret, which is why it is mode 600. tailscaled watches the directory and picks the change up without a restart.
     `misaka` resolves because `misaka-ts` joins the `media-playback` network
     (alias `misaka`) next to `jellyfin-ts`; `docker compose -p danmaku -f
     docker-compose.danmaku.yml up -d` applies that after an upgrade from an
     older compose file.
  3. The injector, from this repo (root-owned and executable, or linuxserver's
     init skips it):
     ```bash
     mkdir -p /mnt/data/media/jellyfin-init
     install -o root -g root -m 755 $REPO/jellyfin/inject-danmaku.sh /mnt/data/media/jellyfin-init/
     docker compose -p media -f docker-compose.yml --profile jellyfin up -d jellyfin
     docker logs jellyfin 2>&1 | grep danmaku-inject    # "injected ede.js into …"
     ```
     The compose file mounts `./jellyfin-init` at `/custom-cont-init.d`; an empty
     directory does nothing. The script is pinned to jellyfin-danmaku v1.62.0
     (one commit, checked by sha256), downloaded once from
     raw.githubusercontent.com and cached in `jellyfin-config/danmaku-web/`, then
     re-injected on every start (a recreated container starts from a clean web
     client). If GitHub is unreachable from the host, set `EDE_URL` on the
     `jellyfin` service to a mirror of the same file, e.g.
     `https://cdn.jsdelivr.net/gh/Izumiko/jellyfin-danmaku@9967db0a7d9c7f0ee1a1aef17fa610341f636169/ede.js`
     (the hash is still checked), or put the file at
     `jellyfin-config/danmaku-web/ede.js` yourself. Upgrade: set `EDE_COMMIT`
     and `EDE_SHA256` (env on `jellyfin`, or edit the script).
  4. Check: `curl -s "https://jellyfin.<tailnet>.ts.net/danmaku/api/v2/search/anime?keyword=<a title Misaka has>"`
     returns Misaka's JSON. Then open Jellyfin (hard-refresh once), play an
     episode: the 弹幕 toggle sits at the bottom left of the player, and
     `设置` → `弹幕设置` shows 自定义API `/danmaku`.
  - Open Jellyfin at `https://jellyfin.<tailnet>.ts.net`. `/danmaku/` exists
    only on the sidecar, not on `http://<host>:8096`.
  - In 御坂网络一键配置, "测试" first probes `/danmaku/api/health`, which is
    not proxied, so that line fails; the search line after it is what counts.
    Leave 使用本地xml弹幕 off (it needs jellyfin-plugin-danmu, see below).
  - Jellyfin Media Player and the TV apps use their own bundled UI, so they get
    no 弹幕 this way; use a dandanplay-compatible player there.

## 6. Other players

- **SenPlayer / Hills / Afusekt / 小幻影视** (dandanplay-compatible): 自定义弹幕
  API → `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>` (a token from section 2). The device must be on the tailnet.
- **Don't also install a Jellyfin danmaku *plugin* that downloads its own xml**
  next to the videos (e.g. jellyfin-plugin-danmu). The bridge never overwrites
  an xml it didn't write, so wherever the plugin got there first, that video
  keeps the plugin's xml and never gets Misaka's. One writer only.
- Players that read a sidecar `<video>.xml` from the library get danmaku with
  no API at all — that's what the bridge's sync is for.

**Verify:** import one episode in Sonarr → the Misaka task succeeds → `<video>.xml` appears beside its symlink → 弹幕 shows in SenPlayer or Jellyfin web. If not: [troubleshooting.md](troubleshooting.md).

## 7. Anime season/episode mismatches

Sonarr numbers anime by TVDB seasons; danmaku sites often number a cour as
its own show, or run episodes on (S2E01 = "第 13 集"). Fix per series in Misaka,
not in Sonarr:
- **本地剧集组** (episode groups): map `local` (Sonarr's S/E) → `source`
  (the site's episode), e.g. season 2 `{"local":1,"source":13}`. Takes priority
  over auto-matching. Or bind a TMDB episode group to the entry.
- **识别词** (title recognition): whole-series season/episode offsets and
  title replacements — quicker when every episode is shifted by the same amount.
Then re-submit that series (`backfill` again, or refresh it in Misaka's UI).

## 8. LibreTV web player with danmaku (optional)

[LibreTV](https://github.com/LibreSpark/LibreTV) searches third-party video
sources and plays them in the browser; it is not a front end for this library.
This repo builds it from a pinned upstream commit plus `libretv/danmaku.patch`,
which makes its server ask Misaka for each episode's danmaku by title + episode
number (`/match`, then `/search/episodes`, then `/comment`). Details and limits:
`libretv/README.md`.

- In Misaka: `设置` → turn on **后备匹配** (match fallback) so shows that aren't
  in the library get searched and imported on first play. If you restricted
  fallback to certain tokens, add LibreTV's token there.
- `弹幕` → `Token管理` → create a token just for LibreTV → `LIBRETV_MISAKA_TOKEN` in `.env`.
- `./libretv-ts/serve.json`: see [tailscale.md](tailscale.md); on the host, from `/mnt/data/media`: `mkdir -p libretv-ts && sed 's/<PORT>/8080/' serve/app.json > libretv-ts/serve.json`.
- Set `LIBRETV_PASSWORD` and `LIBRETV_MISAKA_TOKEN` in `.env`, then:

```bash
docker build -t libretv-danmaku:latest "$REPO/libretv"
docker compose -p danmaku -f docker-compose.danmaku.yml --profile libretv up -d
```

Open `https://libretv.<tailnet>.ts.net`, log in, add a 采集站 in its settings
(or preset them with `DEFAULT_SOURCES`) if none is listed, play an episode. A hint like `弹幕 1234 条 · <title> 第5集`
shows when danmaku loaded. A show Misaka has never seen can take tens of seconds
the first time. Wrong show or wrong episode: fix it in Misaka as in section 7.
