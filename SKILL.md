---
name: media-stack-setup
description: >-
  Step-by-step guide to self-host a streaming media library on a single Linux
  box: Real-Debrid + zurg + rclone for remote streaming with local caching, the *arr stack
  (Sonarr/Radarr/Prowlarr/Bazarr/Jellyseerr) with a Decypharr debrid bridge for
  full automation, Plex/Jellyfin playback with Chinese metadata, optional
  弹幕 (danmaku) via a Misaka danmu server fed by Sonarr/Radarr webhooks, private
  Tailscale access (no public exposure), and daily config backup. Use this
  whenever the user wants to build or deploy a self-hosted media server,
  Real-Debrid + Plex/Jellyfin, an *arr automation stack, zurg/rclone debrid
  mounting, or asks about 影音库 / 媒体库 / 追剧自动化 / 串流看片 / 弹幕 on a Linux
  or Oracle Always Free machine — even if they only name one piece (e.g. "set up
  Sonarr with Real-Debrid", "mount Real-Debrid into Plex", "zurg rclone docker",
  "给 Jellyfin 加弹幕", "Misaka 弹幕服务器").
---

# Media Stack Setup — Real-Debrid 串流影音库 + 全自动化

Build a complete self-hosted media library with **remote media and a local
streaming cache** — Real-Debrid (RD) holds the files, `zurg`+`rclone` stream them
through a FUSE mount, and the *arr apps automate acquisition. Plex/Jellyfin
serve playback; everything is reachable privately over Tailscale.

This skill reflects a real single-host deployment (Oracle Always Free ARM). It
works on any Linux box with Docker. Keep this file as the map; the steps live in `docs/`.

## How to run it

1. Follow the **Setup path** table in `README.md` top to bottom.
2. Open each linked doc only when you reach its step. Steps 1 and 2 are required; step 3 (弹幕) and LibreTV only when the user asks for them.
3. Prerequisites are at the top of `docs/1-deploy.md`. Check the existing context first and ask only for what is missing.
4. Verify each step before moving on. When something breaks, go to `docs/troubleshooting.md`.

## Operating principles (apply throughout)

- **Always `.bak` a compose/config file before editing**, and run `docker compose config` to validate before `up`. Bring up only the changed services (`docker compose up -d <svc>`) so you don't churn the whole stack.
- **Treat secrets as secrets**: the RD token, Tailscale auth key, `rclone.conf`, and Plex token are sensitive. Use env references in compose; never print or commit them.
- **Verify each phase before moving on** (a quick `ls`, `curl`, or `docker ps`). This stack has a few non-obvious failure modes; catching them per-phase is far cheaper than debugging the whole pipeline.
- When something breaks, consult **`docs/troubleshooting.md`** first — it documents the real gotchas (symlink path consistency, SELinux label breakage, lost mounts, containerd image store).
- **Danmaku**: use the bridge in `danmu-bridge/` (build it locally; its README lists every env var) — don't improvise a replacement.
- **One xml writer only**: no Jellyfin danmaku plugin alongside `danmu-bridge`.
