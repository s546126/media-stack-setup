#!/usr/bin/env bash
# Linux: run as the deployment owner with Docker access and readable configs.
# See references/backup.md for setup, downtime, credentials, and restore.
set -Eeuo pipefail
umask 077

MEDIA_DIR=${MEDIA_DIR:-/mnt/data/media}
REMOTE_DIR=${REMOTE_DIR:-gdrive:media-backups/configs}
RETENTION_DAYS=${RETENTION_DAYS:-14}
BACKUP_TMP_DIR=${BACKUP_TMP_DIR:-/var/tmp}
MEDIA_PROJECT=${MEDIA_PROJECT:-media}
ARR_PROJECT=${ARR_PROJECT:-arr}
[[ $RETENTION_DAYS =~ ^[1-9][0-9]*$ ]] || { echo 'Invalid RETENTION_DAYS' >&2; exit 1; }
[[ $REMOTE_DIR == *:* && $REMOTE_DIR != *$'\n'* ]] || { echo 'Expected rclone remote:path' >&2; exit 1; }
for tool in docker rclone tar mktemp flock; do
  command -v "$tool" >/dev/null || { echo "Missing dependency: $tool" >&2; exit 1; }
done
cd "$MEDIA_DIR"
MEDIA_DIR=$PWD
exec 9>"$MEDIA_DIR/.config-backup.lock"
flock -n 9 || { echo 'Another backup is running' >&2; exit 1; }
[[ -f docker-compose.yml && -f docker-compose.arr.yml ]] || {
  echo 'Both compose files are required in MEDIA_DIR' >&2; exit 1;
}

stage=$(mktemp -d "$BACKUP_TMP_DIR/media-backup.XXXXXXXX")
stopped=()
cleanup() {
  local status=$?
  trap - EXIT
  if ((${#stopped[@]})); then
    if ! docker start "${stopped[@]}" >/dev/null; then
      echo 'ERROR: could not restart containers; run docker start manually' >&2
      status=1
    fi
  fi
  rm -rf -- "$stage"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Collect only configuration. Never follow media symlinks or traverse FUSE mounts.
paths=(docker-compose.yml docker-compose.arr.yml)
for path in .env zurg alist plex-config jellyfin-config prowlarr-config sonarr-config \
  radarr-config bazarr-config jellyseerr-config decypharr-config lidarr-config \
  qbittorrent-config navidrome-data; do
  [[ ! -e $path ]] || paths+=("$path")
done
for path in *-ts; do
  [[ ! -d $path ]] || paths+=("$path")
done

# Stop only writers of the included app configs. Leave FUSE and network
# sidecars running so a backup cannot detach mounts or interrupt unrelated apps.
format='{{.ID}} {{.Label "com.docker.compose.service"}}'
ids=$(docker ps --format "$format" --filter "label=com.docker.compose.project=$MEDIA_PROJECT")
arr_ids=$(docker ps --format "$format" --filter "label=com.docker.compose.project=$ARR_PROJECT")
while read -r id service; do
  case "$service" in
    alist|plex|jellyfin|prowlarr|sonarr|radarr|bazarr|jellyseerr|decypharr|lidarr|qbittorrent|navidrome)
      stopped+=("$id") ;;
  esac
done <<< "$ids${arr_ids:+$'\n'$arr_ids}"
if ((${#stopped[@]})); then
  docker stop --time 60 "${stopped[@]}" >/dev/null
fi
archive="$stage/configs.tar.gz"
tar -czf "$archive" --exclude='*/cache' --exclude='*/Cache' \
  --exclude='*/logs' --exclude='*/log' --exclude='*.log' -- "${paths[@]}"
tar -tzf "$archive" >/dev/null
if ((${#stopped[@]})); then
  docker start "${stopped[@]}" >/dev/null
  stopped=()
fi

# Publish only after a complete archive and successful upload. Failed uploads
# remain .partial and never trigger retention; rclone checks transfer integrity.
name="media-configs-$(date -u +%Y%m%dT%H%M%SZ)-${stage##*.}.tar.gz"
remote="${REMOTE_DIR%/}/$name"
rclone copyto "$archive" "$remote.partial"
rclone moveto "$remote.partial" "$remote"
rclone delete "${REMOTE_DIR%/}" --min-age "${RETENTION_DAYS}d" \
  --include '/media-configs-*.tar.gz'
printf 'DONE: %s (retention %s days)\n' "$remote" "$RETENTION_DAYS"
