# Configuration backups

`scripts/config-backup.sh` requires Linux Bash, `flock`, Docker CLI, GNU tar, and
rclone. Run as an account that can read every included config and control Docker.
Do not put interactive sudo inside cron. Configure that account's rclone remote
first. Use an rclone `crypt` remote if the archive should be encrypted in Drive:
archives contain `.env`, API tokens, and rclone credentials.

The script locks per `MEDIA_DIR`, stops the currently running app writers in Compose projects `media` and `arr`
(Plex, Jellyfin, alist and the supported *arr apps, including optional Lidarr,
qBittorrent and Navidrome), archives their configuration, and restarts
those same containers before uploading. This causes a short playback/service
interruption but makes SQLite databases and WAL files consistent. A failed stop
or tar triggers a restart attempt and fails the job. SIGKILL/power loss cannot
run cleanup: check services after a terminated backup. No unrelated process may
write these configuration directories during the snapshot.

Set `MEDIA_PROJECT` / `ARR_PROJECT` if your actual project names differ. Check
`docker ps --format '{{.Names}} {{.Labels}}'` before the first run. FUSE containers, Tailscale sidecars and unrelated services remain running.
Custom app writers/config directories require extending the script's allowlists. Bind-mounted configs are archived; Docker named
volumes (`zurgdata`, Tailscale identities) are **not**. After a full host loss,
zurg rebuilds its state and sidecars need fresh auth keys and re-registration;
check hostname changes and ACL grants. Back up named volumes separately if node
identity preservation is required.

Defaults:

```bash
MEDIA_DIR=/mnt/data/media \
REMOTE_DIR=gdrive:media-backups/configs \
RETENTION_DAYS=14 \
BACKUP_TMP_DIR=/var/tmp \
/path/to/config-backup.sh
```

Have enough space in `BACKUP_TMP_DIR` for one compressed config archive, including
Plex/Jellyfin metadata. All existing listed app config directories and `*-ts`
configuration directories are included; absent optional apps are skipped.
Both Compose files are required. Cache/log directories are excluded. Timestamps
and random suffixes prevent same-day overwrite. Uploads first use `.partial`;
only successfully published archives trigger retention. Retention only matches
`media-configs-*.tar.gz` at the remote directory root, leaving unrelated files
and incomplete uploads alone. Investigate/remove stale `.partial` files manually.

After testing, add an entry with `crontab -e` (adjust executable paths/PATH for
cron). Redirect output to a private log owned by the backup account, and monitor
nonzero exits using your scheduler or cron mail:

```cron
30 4 * * * /path/to/config-backup.sh >> /path/to/private-backup.log 2>&1
```

## Restore drill

Download a specific successful archive with `rclone copyto remote:path file`.
List it first with `tar -tzf file`, then extract to an empty private directory:

```bash
mkdir -m 700 restore-check
tar -xzf file -C restore-check
```

Verify configuration and database integrity in that directory before replacing
live data. Stop the target apps before copying restored configs into `MEDIA_DIR`,
preserve required ownership/permissions, and start only the restored services.
Recreate mount preparation, named volumes and secrets as needed. Never extract
an unreviewed archive directly over `/` or a running deployment.
