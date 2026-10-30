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

---

## Verify the whole pipeline

Add one movie to Radarr (monitored, search). Expect: Radarr → Decypharr →
Real-Debrid caches it → it appears under `/mnt/zurg` → Decypharr creates a staging symlink → Radarr imports it into
`/mnt/data/media/library/movies` → Plex/Jellyfin "Movies" library shows it (with
a Chinese poster). If the file appears in `/mnt/zurg` but not in the library, or
the library entry is a broken symlink, see `troubleshooting.md` (path consistency).
