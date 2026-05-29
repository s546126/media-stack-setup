#!/bin/bash
# ============================================================================
# Daily config backup -> Google Drive (via rclone), 14-day retention.
#
# Backs up the IRREPLACEABLE config (compose files, zurg config + rclone.conf,
# each *arr app's config dir incl. its sqlite DB, alist data). It does NOT back
# up media (that lives in Real-Debrid / the cloud) or regenerable caches.
#
# Setup:
#   1) Have an rclone remote for Google Drive (e.g. `gdrive`):  rclone config
#   2) Edit MEDIA_DIR / REMOTE below if your paths differ.
#   3) chmod +x config-backup.sh ; run once to verify ; then add to crontab:
#        ( crontab -l 2>/dev/null | grep -v config-backup.sh; \
#          echo "30 4 * * * /home/opc/config-backup.sh" ) | crontab -
# ============================================================================
set -o pipefail
export PATH=/home/linuxbrew/.linuxbrew/bin:/usr/local/bin:/usr/bin:/bin:$PATH

MEDIA_DIR=/mnt/data/media
REMOTE_DIR="gdrive:media-backups/configs"      # rclone remote:path
RETENTION_DAYS=14
TS=$(date +%F)
REMOTE="$REMOTE_DIR/media-configs-$TS.tar.gz"
LOG=/home/opc/config-backup.log

{
  echo "[$(date)] START config backup"
  # tar the configs (relative to /), exclude regenerable cache/log dirs, stream
  # straight to the cloud (rcat) so nothing large lands on local disk.
  sudo tar -czf - -C / \
    --exclude='*/cache/*' --exclude='*/Cache/*' --exclude='*/logs/*' \
    --exclude='*/log/*' --exclude='*.log' \
    "${MEDIA_DIR#/}/docker-compose.yml" \
    "${MEDIA_DIR#/}/docker-compose.arr.yml" \
    "${MEDIA_DIR#/}/zurg/config.yml" \
    "${MEDIA_DIR#/}/zurg/rclone.conf" \
    "${MEDIA_DIR#/}/zurg/scripts" \
    "${MEDIA_DIR#/}/prowlarr-config" \
    "${MEDIA_DIR#/}/sonarr-config" \
    "${MEDIA_DIR#/}/radarr-config" \
    "${MEDIA_DIR#/}/bazarr-config" \
    "${MEDIA_DIR#/}/jellyseerr-config" \
    "${MEDIA_DIR#/}/decypharr-config" \
    "${MEDIA_DIR#/}/alist" \
    2>/dev/null | rclone rcat "$REMOTE"
  echo "[$(date)] uploaded: $(rclone size "$REMOTE" 2>/dev/null | tr '\n' ' ')"
  # retention: drop backups older than RETENTION_DAYS
  rclone delete "$REMOTE_DIR" --min-age "${RETENTION_DAYS}d" 2>/dev/null
  echo "[$(date)] DONE (retention ${RETENTION_DAYS}d applied)"
} >> "$LOG" 2>&1

# Restore (manual): rclone cat gdrive:media-backups/configs/media-configs-YYYY-MM-DD.tar.gz \
#                   | sudo tar -xzf - -C /
