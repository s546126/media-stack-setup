---
name: media-stack-setup
description: >-
  Step-by-step guide to self-host a streaming media library on a single Linux
  box: Real-Debrid + zurg + rclone for storage-free streaming, the *arr stack
  (Sonarr/Radarr/Prowlarr/Bazarr/Jellyseerr) with a Decypharr debrid bridge for
  full automation, Plex/Jellyfin playback with Chinese metadata, private
  Tailscale access (no public exposure), and daily config backup. Use this
  whenever the user wants to build or deploy a self-hosted media server,
  Real-Debrid + Plex/Jellyfin, an *arr automation stack, zurg/rclone debrid
  mounting, or asks about 影音库 / 媒体库 / 追剧自动化 / 串流看片 on a Linux or
  Oracle Always Free machine — even if they only name one piece (e.g. "set up
  Sonarr with Real-Debrid", "mount Real-Debrid into Plex", "zurg rclone docker").
---

# Media Stack Setup — Real-Debrid 串流影音库 + 全自动化

Build a complete self-hosted media library where **the media never lives on
local disk** — Real-Debrid (RD) holds the files, `zurg`+`rclone` stream them
through a FUSE mount, and the *arr apps automate acquisition. Plex/Jellyfin
serve playback; everything is reachable privately over Tailscale.

This skill reflects a real single-host deployment (Oracle Always Free ARM). It
works on any Linux box with Docker. Read the referenced files when you reach the
phase that needs them — keep this file as the map.

## What gets built (the pipeline)

```
求片/索引 → Sonarr/Radarr 决定抓什么 → Decypharr 加种到 Real-Debrid
  → zurg 串流挂载 /mnt/zurg → 符号链接进 library → Plex/Jellyfin 播放
```

- **media stack** (`docker-compose.yml`): zurg (RD client, :9999 WebDAV) → rclone FUSE mount `/mnt/zurg`; alist (optional 百度/夸克/阿里云盘, :5244) → rclone-alist `/mnt/alist`; Plex (:32400) and/or Jellyfin (same role — pick one or run both).
- **arr stack** (`docker-compose.arr.yml`, separate compose project `arr`): Prowlarr :9696, Sonarr :8989, Radarr :7878, Bazarr :6767, Jellyseerr :5055, Decypharr :8282 (the RD bridge).
- **access**: a Tailscale sidecar per user-facing service → `https://<svc>.<tailnet>.ts.net`, nothing on the public internet.
- **safety net**: daily cron backup of all configs → Google Drive (rclone).

## Prerequisites — check existing context; ask only for missing requirements

1. **A Linux host with Docker** (1+ core, 2GB+ RAM; an Oracle Always Free ARM A1 works well). Use the official Docker Engine repository instructions for the host distribution if absent.
2. **A Real-Debrid subscription** + its API token (`https://real-debrid.com/apitoken`).
3. **A Tailscale account** (for private access) + ability to generate a **reusable, non-ephemeral** auth key (`https://login.tailscale.com/admin/settings/keys`).
4. *(Optional)* Plex account / Plex Pass; a Google account + `rclone` gdrive remote for backups; cloud-drive creds for alist.

Storage note: a **single boot disk is more robust than a separate data volume**. See `references/troubleshooting.md` ("storage layout") for why — a detached/failed extra volume mount is the classic way this stack breaks.

## Phases — run in order

Work top-down. Each phase points to the file with the concrete templates/commands.

### Phase 1 — media stack (streaming foundation)
Deploy zurg + rclone (+ optional alist) + Plex/Jellyfin.
→ Use the template in **`references/compose-media.yml`** and the notes at its top
(zurg `config.yml` with the RD token, the `rclone.conf`, the FUSE/`SYS_ADMIN`/`/dev/fuse` requirements, and the **`/mnt:/mnt:rslave`** mounts on Plex/Jellyfin).
First follow **`references/deployment.md`** for shared-mount preparation, profiles,
project names and private bindings. Verify: `ls /mnt/zurg` shows the RD library (`movies/`, `shows/`, …).

### Phase 2 — arr automation stack
Deploy Prowlarr/Sonarr/Radarr/Bazarr/Jellyseerr/Decypharr as a **separate compose project** (`-p arr`) so it never disturbs the media stack.
→ Use **`references/compose-arr.yml`**. Bind UIs to the host's Tailscale IP (or localhost) — never `0.0.0.0` on a public host.
Verify: all six containers `Up`; each UI returns 200/307.

### Phase 3 — wire it together
Most of this is automatable via each app's REST API (the API key lives in
`<app>-config/config.xml`). Decypharr's RD bridge + symlink output is the linchpin.
→ Follow **`references/wiring.md`** step by step (Decypharr RD + symlink dir,
Prowlarr↔Sonarr/Radarr, Sonarr/Radarr→Decypharr download client, root folders,
Bazarr↔arr). The symlink path-consistency rule is critical — see troubleshooting.

### Phase 4 — private access via Tailscale
Give each user-facing service its own Tailscale node using the **userspace
sidecar + serve** pattern, so internal `arr` cross-talk keeps working while
users get clean `https://<svc>.<tailnet>.ts.net` URLs.
→ Follow **`references/tailscale-sidecars.md`** (needs the reusable auth key).

### Phase 5 — 中文化 (Chinese metadata + subtitles)
Set Plex/Jellyfin library language to `zh-CN`; in Bazarr add a Chinese language
profile + a subtitle provider; optionally enable Plex/Jellyfin built-in subtitle
download. → Section in **`references/wiring.md`** ("中文化").

### Phase 6 — config backup
Configure and test **`scripts/config-backup.sh`** using **`references/backup.md`**.
It briefly stops running stack containers for a consistent archive, restarts them,
then uploads. Confirm a restore drill before scheduling cron.

## Operating principles (apply throughout)

- **Always `.bak` a compose/config file before editing**, and run `docker compose config` to validate before `up`. Bring up only the changed services (`docker compose up -d <svc>`) so you don't churn the whole stack.
- **Treat secrets as secrets**: the RD token, Tailscale auth key, `rclone.conf`, and Plex token are sensitive. Use env references in compose; never print or commit them.
- **Verify each phase before moving on** (a quick `ls`, `curl`, or `docker ps`). This stack has a few non-obvious failure modes; catching them per-phase is far cheaper than debugging the whole pipeline.
- When something breaks, consult **`references/troubleshooting.md`** first — it documents the real gotchas (symlink path consistency, SELinux label breakage, lost mounts, containerd image store).

## Bundled files

- `references/compose-media.yml` — media stack template + setup notes
- `references/compose-arr.yml` — arr automation stack template
- `references/wiring.md` — API wiring, Decypharr RD bridge, root folders, 中文化
- `references/tailscale-sidecars.md` — userspace sidecar + serve pattern
- `references/troubleshooting.md` — storage layout + real-world gotchas
- `scripts/config-backup.sh` — daily config backup to Google Drive
