# Wiring the stack

After Phases 1–2 the containers run but do nothing useful yet. This connects
them. Most steps are doable via each app's REST API — the API key is in
`<app>-config/config.xml` (`grep -oE '<ApiKey>[^<]+' .../config.xml`). API-key
calls work even before you set up UI login.

Set `HOST=127.0.0.1` for default bindings. Before POSTing, GET the endpoint and
reuse existing root folders/applications to avoid duplicate entries.

Replace `$HOST` with how you reach the apps (the tailscale IP, or `localhost`
if you bind there). Container-to-container, apps use names (`sonarr`, `decypharr`).

## Order of operations

1. Decypharr → add Real-Debrid + set symlink output  ← the linchpin
2. Sonarr/Radarr → root folders
3. Sonarr/Radarr → add Decypharr as a qBittorrent download client
4. Prowlarr → add Sonarr/Radarr as applications, then add indexers (UI)
5. Bazarr → connect to Sonarr/Radarr, add Chinese
6. Jellyseerr → setup wizard (UI), Plex/Jellyfin libraries + 中文化
7. *(optional)* 弹幕 — Misaka + Sonarr/Radarr webhooks → bridge, backfill, players

---

## 1. Decypharr — the Real-Debrid bridge

Use Decypharr's UI for the installed image version; its config schema can change.
Back up the generated config before edits, and stop the app for manual file edits.
Configure Real-Debrid with its API token and the existing zurg mount at
`/mnt/zurg/__all__`. Select the external mount/symlink workflow; do not enable a
second downloader or copy workflow by accident.

- Use `/mnt/data/media/downloads` for staging; keep it separate from the final
  Sonarr/Radarr roots `/mnt/data/media/library/{tv,movies}`.
- Select the symlink download action (`default_download_action` on versions
  supporting that field) and confirm a test job produces actual symlinks.
- Keep authentication enabled; enter the matching credentials in the *arr
  qBittorrent client. A private tailnet is not an application login boundary.
- Verify the configured folder exists **inside Decypharr** before searching.
  Do not assume the image's defaults or a successful container start prove this.

Create the downloads directory writable by the configured app UID/GID. After
saving, use the client Test buttons and inspect redacted logs for successful RD
sync. The *arr import must preserve symlinks: disable hardlink/copy behavior as
required by the installed Decypharr integration and verify with a real test
release. A full local media file means the streaming workflow has not passed.

## 2. Root folders (Sonarr/Radarr)

These are where the curated library lives — the same dir the media servers read.

```bash
SK='REPLACE_WITH_SONARR_API_KEY'; RK='REPLACE_WITH_RADARR_API_KEY'
mkdir -p /mnt/data/media/library/tv /mnt/data/media/library/movies
curl --fail-with-body -sS -X POST "http://$HOST:8989/api/v3/rootfolder" -H "X-Api-Key: $SK" \
  -H 'Content-Type: application/json' -d '{"path":"/mnt/data/media/library/tv"}'
curl --fail-with-body -sS -X POST "http://$HOST:7878/api/v3/rootfolder" -H "X-Api-Key: $RK" \
  -H 'Content-Type: application/json' -d '{"path":"/mnt/data/media/library/movies"}'
```

## 3. Sonarr/Radarr → Decypharr download client

Add Decypharr as a qBittorrent client (host = container name `decypharr`, port 8282).
**Radarr requires `priority` ≥ 1** (a value of 0 fails validation — Sonarr tolerates it).

In each app's Settings → Download Clients, choose qBittorrent and enter
`decypharr:8282`, the credentials configured in Decypharr, and category `sonarr`
or `radarr`. Set priority to 1 and use **Test** before saving. Retrieve the
installed app's `/api/v3/downloadclient/schema` if automating this: field names
and authentication requirements are version-specific. Do not submit empty
credentials or repeat POSTs without checking existing clients.

## 4. Prowlarr → applications + indexers

Add Sonarr & Radarr as Prowlarr "applications" so indexers auto-sync to them.
(Internal URLs use container names; the API call below works once all are up.)

```bash
PK='REPLACE_WITH_PROWLARR_API_KEY'
curl --fail-with-body -sS -X POST "http://$HOST:9696/api/v1/applications" -H "X-Api-Key: $PK" \
 -H 'Content-Type: application/json' -d '{"name":"Sonarr","syncLevel":"fullSync",
 "implementation":"Sonarr","implementationName":"Sonarr","configContract":"SonarrSettings",
 "fields":[{"name":"prowlarrUrl","value":"http://prowlarr:9696"},
 {"name":"baseUrl","value":"http://sonarr:8989"},{"name":"apiKey","value":"'$SK'"},
 {"name":"syncCategories","value":[5000,5010,5020,5030,5040,5045,5050]},
 {"name":"animeSyncCategories","value":[5070]}]}'
# Radarr: implementation/configContract "Radarr"/"RadarrSettings", baseUrl http://radarr:7878,
#         syncCategories [2000,2010,2020,2030,2040,2045,2050,2060]
```

**Indexers themselves are a UI step** (Prowlarr → Settings → Indexers): you pick
trackers and enter any account/credentials. Some need FlareSolverr. This is the
one part that genuinely needs the user's choices. Once added, they sync to *arr.

## 5. Bazarr → Sonarr/Radarr + Chinese

Bazarr needs Sonarr and/or Radarr connected (it has no standalone library mode).
Connection details go in its `config/config.yaml` (back up first; use `sudo
python3` if pyyaml is only there), or set them in the UI. Set
`general.use_sonarr/use_radarr=true` and the `sonarr`/`radarr` sections to
`ip: sonarr|radarr`, `port: 8989|7878`, `apikey`, `base_url: ""`, `ssl: false`.

The **Chinese language profile and subtitle providers are DB-backed → do them in
the UI**: Languages → add 中文(Chinese) → make a profile → assign to series/movies;
Providers → enable one that carries Chinese (e.g. opensubtitles.com with a free
account, or assrt with a token).

## 6. Jellyseerr + Plex/Jellyfin libraries + 中文化

**Jellyseerr** (`:5055`) is a setup-wizard UI: choose Plex or Jellyfin, authenticate
with that server, and connect Sonarr/Radarr (host `sonarr`/`radarr` + API key) so requests
auto-fulfil. Use the UI for provider authentication. See [deployment.md](deployment.md) for
cross-project network reachability before entering the server URL.

**Plex libraries (中文 metadata)** — use an authenticated Plex token to create
libraries pointing at the curated directories. The following assumes the default
localhost binding; use the configured private address otherwise:

```bash
PT='REPLACE_WITH_PLEX_TOKEN'
curl --fail-with-body -sS -X POST "http://localhost:32400/library/sections?name=Movies&type=movie\
&agent=tv.plex.agents.movie&scanner=Plex%20Movie&language=zh-CN\
&location=%2Fmnt%2Fdata%2Fmedia%2Flibrary%2Fmovies&X-Plex-Token=$PT"
curl --fail-with-body -sS -X POST "http://localhost:32400/library/sections?name=TV&type=show\
&agent=tv.plex.agents.series&scanner=Plex%20TV%20Series&language=zh-CN\
&location=%2Fmnt%2Fdata%2Fmedia%2Flibrary%2Ftv&X-Plex-Token=$PT"
```

**Jellyfin** library + Chinese metadata + OpenSubtitles plugin are UI steps (its
API needs a key created in the UI first). Point its libraries at the same
`/mnt/data/media/library/{movies,tv}`, set preferred metadata/subtitle language
to Chinese.

## 7. 弹幕 (danmaku) — Misaka + Sonarr/Radarr bridge  *(optional)*

Deploy `references/compose-danmaku.yml` first (its header has the commands).
The trigger is **Sonarr/Radarr**, not the media server: their webhook already
carries the TMDB/TVDB id, season/episode and the file's full path, so the
bridge can name `<video>.xml` exactly and never has to guess from filenames.
Jellyfin/Plex only play.

**7a. Misaka first boot (UI, `https://misaka.<tailnet>.ts.net`)**
- Admin password: `docker logs misaka 2>&1 | grep -i password` — log in as
  `admin`, change it.
- `弹幕源` → enable the danmaku sources you want; `元信息搜索源` → TMDB → paste
  a TMDB API key (needed for id-based matching; add TVDB too if Sonarr uses TVDB ids).
- `外部控制` → `API 密钥` → generate → this is `MISAKA_API_KEY` for the bridge.
- `弹幕` → `Token管理` → create one token per player/person → used in the
  player URL `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>`.
- Leave Misaka's own media-server sync / Jellyfin webhook **off** — the
  bridge is the only trigger; two triggers just queue duplicate imports.
- Misaka scrapes Chinese video sites. From a non-China host some sources
  geo-block or time out — set `设置` → `代理配置` if your sources come back empty.

Put the API key into `docker-compose.danmaku.yml`, build the bridge from this
repo's `danmu-bridge/` and start it:

```bash
docker build -t danmu-bridge:latest <PATH_TO_THIS_REPO>/danmu-bridge
docker compose -p danmaku -f docker-compose.danmaku.yml up -d danmu-bridge
```
What it does, every env var and its limits: `danmu-bridge/README.md`.

**7b. Sonarr/Radarr → bridge webhooks**

`Settings → Connect → + → Webhook`, URL `http://danmu-bridge:8090/webhook/sonarr`
(Radarr: `/webhook/radarr`), method POST, username `danmu` (any), password =
`WEBHOOK_SECRET`. Triggers: **On File Import**, **On File Upgrade** (these submit
to Misaka), plus **On Rename**, **On Episode/Movie File Delete**, **On
Series/Movie Delete** (these only re-run the xml sync, so renamed videos get
their xml moved and deleted ones lose theirs). Via API:

```bash
WS=<YOUR_WEBHOOK_SECRET>   # same value as WEBHOOK_SECRET in the bridge
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

**7c. Backfill the existing library (once)**

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
5m; `docker exec danmu-bridge danmu-bridge sync` runs one now).

**7d. Anime season/episode mismatches**

Sonarr numbers anime by TVDB seasons; danmaku sites often number a cour as
its own show, or run episodes on (S2E01 = "第 13 集"). Fix per series in Misaka,
not in Sonarr:
- **本地剧集组** (episode groups): map `local` (Sonarr's S/E) → `source`
  (the site's episode), e.g. season 2 `{"local":1,"source":13}`. Takes priority
  over auto-matching. Or bind a TMDB episode group to the entry.
- **识别词** (title recognition): whole-series season/episode offsets and
  title replacements — quicker when every episode is shifted by the same amount.
Then re-submit that series (`backfill` again, or refresh it in Misaka's UI).

**7e. Players**
- **Jellyfin web, embedded** (also the Jellyfin Android app with its web
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
  2. `./jellyfin-ts/serve.json` gets a second handler (replace `<TOKEN>`; the
     file now holds a secret, so `chmod 600` it). tailscaled watches the
     directory and picks the change up without a restart:
     ```json
     {
       "TCP": { "443": { "HTTPS": true } },
       "Web": {
         "${TS_CERT_DOMAIN}:443": {
           "Handlers": {
             "/":         { "Proxy": "http://127.0.0.1:8096" },
             "/danmaku/": { "Proxy": "http://misaka:7768/api/v1/<TOKEN>" }
           }
         }
       }
     }
     ```
     `misaka` resolves because `misaka-ts` joins the `media-playback` network
     (alias `misaka`) next to `jellyfin-ts`; `docker compose -p danmaku -f
     docker-compose.danmaku.yml up -d` applies that after an upgrade from an
     older compose file.
  3. The injector, from this repo (root-owned and executable, or linuxserver's
     init skips it):
     ```bash
     mkdir -p /mnt/data/media/jellyfin-init
     install -o root -g root -m 755 <PATH_TO_THIS_REPO>/jellyfin/inject-danmaku.sh /mnt/data/media/jellyfin-init/
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
  - The script defaults to `/danmaku` only in a browser that never saved its
    own API setting. One that used the old setup keeps
    `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>`, which still works
    (Misaka allows any origin) but is cross-origin; change 自定义API to
    `/danmaku`. Remove any Tampermonkey copy of the script, or every comment
    is drawn twice.
  - In 御坂网络一键配置, "测试" first probes `/danmaku/api/health`, which is
    not proxied, so that line fails; the search line after it is what counts.
    Leave 使用本地xml弹幕 off (it needs jellyfin-plugin-danmu, see below).
  - Jellyfin Media Player and the TV apps use their own bundled UI, so they get
    no 弹幕 this way; use a dandanplay-compatible player there.
- **SenPlayer / Hills / Afusekt / 小幻影视** (dandanplay-compatible): 自定义弹幕
  API → same URL. The device must be on the tailnet.
- **Don't also install a Jellyfin danmaku *plugin* that downloads its own xml**
  next to the videos (e.g. jellyfin-plugin-danmu). The bridge never overwrites
  an xml it didn't write, so wherever the plugin got there first, that video
  keeps the plugin's xml and never gets Misaka's. One writer only.
- Players that read a sidecar `<video>.xml` from the library get danmaku with
  no API at all — that's what the bridge's sync is for.

**7f. LibreTV web player with danmaku** *(optional)*

[LibreTV](https://github.com/LibreSpark/LibreTV) searches third-party video
sources and plays them in the browser; it is not a front end for this library.
This repo builds it from a pinned upstream commit plus `libretv/danmaku.patch`,
which makes its server ask Misaka for each episode's danmaku by title + episode
number (`/match`, then `/search/episodes`, then `/comment`). Details and limits:
`libretv/README.md`.

- In Misaka: `设置` → turn on **后备匹配** (match fallback) so shows that aren't
  in the library get searched and imported on first play. If you restricted
  fallback to certain tokens, add LibreTV's token there.
- `弹幕` → `Token管理` → create a token just for LibreTV → `MISAKA_TOKEN`.
- `./libretv-ts/serve.json`: the serve.json from tailscale-sidecars.md with
  `"Proxy": "http://127.0.0.1:8080"`.
- Set `PASSWORD` and `MISAKA_TOKEN` in the `libretv` service, then:

```bash
docker build -t libretv-danmaku:latest <PATH_TO_THIS_REPO>/libretv
docker compose -p danmaku -f docker-compose.danmaku.yml --profile libretv up -d
```

Open `https://libretv.<tailnet>.ts.net`, log in, add a 采集站 in its settings
(or preset them with `DEFAULT_SOURCES`) if none is listed, play an episode. A hint like `弹幕 1234 条 · <title> 第5集`
shows when danmaku loaded. A show Misaka has never seen can take tens of seconds
the first time. Wrong show or wrong episode: fix it in Misaka as in 7d.

---

## Verify the whole pipeline

Add one movie to Radarr (monitored, search). Expect: Radarr → Decypharr →
Real-Debrid caches it → it appears under `/mnt/zurg` → Decypharr creates a staging symlink → Radarr imports it into
`/mnt/data/media/library/movies` → Plex/Jellyfin "Movies" library shows it (with
a Chinese poster). If the file appears in `/mnt/zurg` but not in the library, or
the library entry is a broken symlink, see `troubleshooting.md` (path consistency).
