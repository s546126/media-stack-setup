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

---

## Verify the whole pipeline

Add one movie to Radarr (monitored, search). Expect: Radarr → Decypharr →
Real-Debrid caches it → it appears under `/mnt/zurg` → Decypharr symlinks it into
`/mnt/data/media/library/movies` → Plex/Jellyfin "Movies" library shows it (with
a Chinese poster). If the file appears in `/mnt/zurg` but not in the library, or
the library entry is a broken symlink, see `troubleshooting.md` (path consistency).
