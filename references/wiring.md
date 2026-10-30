# Wiring the stack

After Phases 1–2 the containers run but do nothing useful yet. This connects
them. Most steps are doable via each app's REST API — the API key is in
`<app>-config/config.xml` (`grep -oE '<ApiKey>[^<]+' .../config.xml`). API-key
calls work even before you set up UI login.

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

Decypharr writes `config.json` to its `/app` volume on first boot. Set:
- `download_folder` → the shared symlink library (e.g. `/mnt/data/media/library`)
- `use_auth` → `false` (it's on a private tailnet; simplifies the qbit client)
- add a `debrids` entry for Real-Debrid

Either use its web UI at `:8282` (add debrid → Real-Debrid → paste token →
folder `/mnt/zurg/__all__`), or edit `config.json` (back it up first; a bad
schema stops the container — revert if so):

```bash
CFG=/mnt/data/media/decypharr-config/config.json
cp "$CFG" "$CFG.bak"
RDTOKEN=$(grep -E '^token:' /mnt/data/media/zurg/config.yml | awk '{print $2}')
RDTOKEN="$RDTOKEN" python3 - <<'PY'
import json, os
p = "/mnt/data/media/decypharr-config/config.json"
d = json.load(open(p))
d["download_folder"] = "/mnt/data/media/library"
d["use_auth"] = False
d["debrids"] = [{
    "name": "realdebrid",
    "api_key": os.environ["RDTOKEN"],
    "folder": "/mnt/zurg/__all__",      # where zurg exposes every torrent by name
    "rate_limit": "250/minute",
}]
json.dump(d, open(p, "w"), indent=2)
print("decypharr configured")
PY
docker restart decypharr
# verify: logs should show "debrids=1" and "Initial sync ... completed", healthy
docker logs --since 40s decypharr 2>&1 | grep -iE "debrids=|error|panic"
```

## 2. Root folders (Sonarr/Radarr)

These are where the curated library lives — the same dir the media servers read.

```bash
SK=<sonarr_apikey>; RK=<radarr_apikey>
mkdir -p /mnt/data/media/library/tv /mnt/data/media/library/movies
curl -s -X POST "http://$HOST:8989/api/v3/rootfolder" -H "X-Api-Key: $SK" \
  -H 'Content-Type: application/json' -d '{"path":"/mnt/data/media/library/tv"}'
curl -s -X POST "http://$HOST:7878/api/v3/rootfolder" -H "X-Api-Key: $RK" \
  -H 'Content-Type: application/json' -d '{"path":"/mnt/data/media/library/movies"}'
```

## 3. Sonarr/Radarr → Decypharr download client

Add Decypharr as a qBittorrent client (host = container name `decypharr`, port 8282).
**Radarr requires `priority` ≥ 1** (a value of 0 fails validation — Sonarr tolerates it).

```bash
# Sonarr
curl -s -X POST "http://$HOST:8989/api/v3/downloadclient" -H "X-Api-Key: $SK" \
 -H 'Content-Type: application/json' -d '{"enable":true,"name":"decypharr",
 "implementation":"QBittorrent","configContract":"QBittorrentSettings","protocol":"torrent",
 "fields":[{"name":"host","value":"decypharr"},{"name":"port","value":8282},
 {"name":"useSsl","value":false},{"name":"username","value":""},{"name":"password","value":""},
 {"name":"tvCategory","value":"sonarr"}]}'
# Radarr  (note priority:1 and movieCategory)
curl -s -X POST "http://$HOST:7878/api/v3/downloadclient" -H "X-Api-Key: $RK" \
 -H 'Content-Type: application/json' -d '{"enable":true,"name":"decypharr","priority":1,
 "implementation":"QBittorrent","configContract":"QBittorrentSettings","protocol":"torrent",
 "fields":[{"name":"host","value":"decypharr"},{"name":"port","value":8282},
 {"name":"useSsl","value":false},{"name":"username","value":""},{"name":"password","value":""},
 {"name":"movieCategory","value":"radarr"}]}'
```

## 4. Prowlarr → applications + indexers

Add Sonarr & Radarr as Prowlarr "applications" so indexers auto-sync to them.
(Internal URLs use container names; the API call below works once all are up.)

```bash
PK=<prowlarr_apikey>
curl -s -X POST "http://$HOST:9696/api/v1/applications" -H "X-Api-Key: $PK" \
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

**Jellyseerr** (`:5055`) is a setup-wizard UI: sign in with Plex, add the Jellyfin
server, and connect Sonarr/Radarr (host `sonarr`/`radarr` + API key) so requests
auto-fulfil. This is interactive (Plex OAuth) — not scriptable.

**Plex libraries (中文 metadata)** — Plex trusts localhost without a token, so from
the host you can create libraries pointing at the curated dirs, with zh-CN:

```bash
PT=<plex_token>   # from plex_update.sh, or localhost may not need it
curl -s -X POST "http://localhost:32400/library/sections?name=Movies&type=movie\
&agent=tv.plex.agents.movie&scanner=Plex%20Movie&language=zh-CN\
&location=%2Fmnt%2Fdata%2Fmedia%2Flibrary%2Fmovies&X-Plex-Token=$PT"
curl -s -X POST "http://localhost:32400/library/sections?name=TV&type=show\
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
- **Jellyfin web**: install the dd-danmaku userscript/extension (chen3861229
  or l429609201 fork) and set its API to
  `https://misaka.<tailnet>.ts.net/api/v1/<TOKEN>`. Must be https — the
  Jellyfin page is https and browsers block http calls from it.
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
Real-Debrid caches it → it appears under `/mnt/zurg` → Decypharr symlinks it into
`/mnt/data/media/library/movies` → Plex/Jellyfin "Movies" library shows it (with
a Chinese poster). If the file appears in `/mnt/zurg` but not in the library, or
the library entry is a broken symlink, see `troubleshooting.md` (path consistency).
