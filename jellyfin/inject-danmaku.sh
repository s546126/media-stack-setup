#!/bin/bash
# Injects the jellyfin-danmaku web script (Izumiko/jellyfin-danmaku, MIT) into
# the Jellyfin web client, so every browser and the Jellyfin Android/iOS apps
# get 弹幕 inside Jellyfin's own player: no userscript, no separate site.
#
# Runs as a linuxserver.io custom init script: mount the directory holding it at
# /custom-cont-init.d (root-owned, chmod 755). It runs on every container start,
# because the web client lives inside the image and a recreated container starts
# from a clean copy. Never fails the start: on any problem it logs and exits 0,
# and Jellyfin comes up without danmaku.
#
# The script is pinned to one upstream commit and checked by sha256. It is
# downloaded once and cached under /config, so later starts work offline.
# The browser is told to call the danmaku API at DANMAKU_API_PREFIX, a path on
# Jellyfin's own origin that the jellyfin-ts sidecar proxies to Misaka (with the
# Misaka token added there), so there is no cross-origin request and no token in
# the browser. See references/wiring.md §7e.
set -u

EDE_COMMIT=${EDE_COMMIT:-9967db0a7d9c7f0ee1a1aef17fa610341f636169}   # v1.62.0
EDE_SHA256=${EDE_SHA256:-629e8f42d5ba4c2715d290dcc53b6d855d7bcba49e261aedc64a43caaf750d58}
EDE_URL=${EDE_URL:-https://raw.githubusercontent.com/Izumiko/jellyfin-danmaku/${EDE_COMMIT}/ede.js}
DANMAKU_API_PREFIX=${DANMAKU_API_PREFIX:-/danmaku}
WEB_DIR=${DANMAKU_WEB_DIR:-/usr/share/jellyfin/web}
CACHE_DIR=${DANMAKU_CACHE_DIR:-/config/danmaku-web}
MARK='<!-- media-stack-setup danmaku -->'

log() { echo "[danmaku-inject] $*"; }
verify() { [[ -f $1 ]] && [[ $(sha256sum "$1" | cut -d' ' -f1) == "$EDE_SHA256" ]]; }

index="$WEB_DIR/index.html"
cache="$CACHE_DIR/ede.js"

if [[ ! -f $index ]]; then
  log "no $index (not the linuxserver jellyfin image?), skipping"
  exit 0
fi
# ede.js only uses a custom API prefix longer than 7 characters; keep the value
# to plain URL characters so it is safe inside the injected HTML and sed below.
if [[ ! $DANMAKU_API_PREFIX =~ ^(/|https://)[A-Za-z0-9/:._-]+$ ]] || ((${#DANMAKU_API_PREFIX} < 8)); then
  log "DANMAKU_API_PREFIX='$DANMAKU_API_PREFIX' must be a path like /danmaku or an https URL (8+ chars), skipping"
  exit 0
fi
if grep -qF "$MARK" "$index" && verify "$WEB_DIR/ede.js"; then
  log "already injected"
  exit 0
fi
if ! grep -q '</body>' "$index"; then
  log "no </body> in $index, skipping"
  exit 0
fi

if ! verify "$cache"; then
  mkdir -p "$CACHE_DIR"
  if curl -fsSL --retry 3 --max-time 60 -o "$cache.tmp" "$EDE_URL" && verify "$cache.tmp"; then
    mv "$cache.tmp" "$cache"
    log "downloaded ede.js $EDE_COMMIT"
  else
    rm -f "$cache.tmp"
    log "could not get ede.js with sha256 $EDE_SHA256 from $EDE_URL; Jellyfin starts without danmaku"
    exit 0
  fi
fi

install -m 0644 "$cache" "$WEB_DIR/ede.js"
# Default the script's API to the same-origin proxy, once per browser: a value
# the user set (or cleared) in 弹幕设置 is left alone.
snippet="$MARK<script>try{if(localStorage.getItem('customApiPrefix')===null)localStorage.setItem('customApiPrefix','$DANMAKU_API_PREFIX')}catch(e){}</script><script src=\"ede.js\" defer></script>"
if grep -qF "$MARK" "$index"; then
  log "ede.js restored"
else
  sed -i "s#</body>#${snippet}</body>#" "$index"
  log "injected ede.js into $index (API prefix $DANMAKU_API_PREFIX)"
fi
exit 0
