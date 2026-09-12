import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[1]


class Templates(unittest.TestCase):
    def test_private_ports_and_optional_services(self):
        for name in ('media', 'arr'):
            config = yaml.safe_load((ROOT / f'references/compose-{name}.yml').read_text())
            for service in config['services'].values():
                self.assertNotEqual(service.get('network_mode'), 'host')
                for port in service.get('ports', []):
                    self.assertTrue(port.startswith(('127.0.0.1:', '${BIND_IP:-127.0.0.1}:')), port)
                if service.get('network_mode', '').startswith('service:'):
                    target = service['network_mode'].split(':', 1)[1]
                    self.assertIn(target, config['services'])
                    self.assertEqual(service.get('profiles'), config['services'][target].get('profiles'))
        media = yaml.safe_load((ROOT / 'references/compose-media.yml').read_text())
        self.assertEqual({k for k, v in media['services'].items() if not v.get('profiles')}, {'zurg', 'rclone'})

    def test_cross_project_playback_network(self):
        media = yaml.safe_load((ROOT / 'references/compose-media.yml').read_text())
        arr = yaml.safe_load((ROOT / 'references/compose-arr.yml').read_text())
        self.assertEqual(media['networks']['playback'], arr['networks']['playback'])
        self.assertTrue(media['networks']['playback']['external'])
        self.assertIn('playback', arr['services']['jellyseerr']['networks'])
        self.assertIn('playback', media['services']['plex']['networks'])
        self.assertIn('jellyfin', media['services']['jellyfin-ts']['networks']['playback']['aliases'])

    def test_mount_paths(self):
        for name, consumers in [('media', ['plex', 'jellyfin']), ('arr', ['sonarr', 'radarr', 'bazarr', 'decypharr'])]:
            config = yaml.safe_load((ROOT / f'references/compose-{name}.yml').read_text())
            for consumer in consumers:
                self.assertIn('/mnt:/mnt:rslave', config['services'][consumer]['volumes'])


class Backup(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.media = self.base / 'media with spaces'
        self.media.mkdir()
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        for name in ['docker-compose.yml', 'docker-compose.arr.yml', '.env']:
            (self.media / name).write_text('test')
        (self.media / 'plex-config').mkdir()
        (self.media / 'plex-config' / 'database.db').write_text('fixture')
        self.log = self.base / 'calls'
        self.env = dict(os.environ, MEDIA_DIR=str(self.media), BACKUP_TMP_DIR=str(self.base),
                        PATH=f'{self.bin}:{os.environ["PATH"]}', CALLS=str(self.log),
                        REMOTE_DIR='test:configs', FAIL='', RUNNING='1')
        self.mock('docker', '''
printf 'docker %s\\n' "$*" >> "$CALLS"
if [ "$1" = ps ]; then
  if [ "$RUNNING" = 1 ]; then
    case "$*" in *project=media*) printf "media-id plex\\nmount-id rclone\\nsidecar-id plex-ts\\n";; *project=arr*) echo "arr-id sonarr";; esac
  fi
elif [ "$1" = "$FAIL" ]; then exit 1; fi
''')
        self.mock('rclone', '''
printf 'rclone %s\\n' "$*" >> "$CALLS"
[ "$1" != "$FAIL" ] || exit 1
if [ "$1" = copyto ]; then tar -tzf "$2" > "$CALLS.archive"; fi
''')
        # macOS has no flock; only the lock adapter is substituted locally.
        # Linux CI uses the real flock, including the contention test below.
        import shutil
        if not shutil.which('flock'):
            self.mock('flock', 'exit 0\n')

    def mock(self, name, body):
        path = self.bin / name
        path.write_text('#!/bin/bash\nset -e\n' + body)
        path.chmod(0o755)

    def run_backup(self, fail='', running='1'):
        self.env.update(FAIL=fail, RUNNING=running)
        return subprocess.run(['bash', str(ROOT / 'scripts/config-backup.sh')], env=self.env,
                              text=True, capture_output=True)

    def calls(self):
        return self.log.read_text() if self.log.exists() else ''

    def test_success_includes_optional_configs_and_restarts_before_upload(self):
        result = self.run_backup()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertLess(calls.index('docker start'), calls.index('rclone copyto'))
        self.assertNotIn('mount-id', calls)
        self.assertNotIn('sidecar-id', calls)
        self.assertLess(calls.index('rclone moveto'), calls.index('rclone delete'))
        listing = Path(str(self.log) + '.archive').read_text()
        self.assertIn('plex-config/database.db', listing)
        self.assertIn('.env', listing)
        self.assertIn('--include /media-configs-*.tar.gz', calls)
        self.assertFalse(list(self.base.glob('media-backup.*')))

    def test_failed_upload_or_publish_never_prunes(self):
        for fail in ('copyto', 'moveto'):
            with self.subTest(fail=fail):
                result = self.run_backup(fail)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn('rclone delete', self.calls())
                self.assertNotIn('DONE', result.stdout)

    def test_retention_failure_is_reported(self):
        result = self.run_backup('delete')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('rclone moveto', self.calls())
        self.assertNotIn('DONE', result.stdout)

    def test_symlink_is_archived_without_reading_remote_media(self):
        import tarfile
        (self.media / 'plex-config' / 'remote-link').symlink_to('/mnt/zurg/missing-media')
        self.mock('rclone', '\n'.join([
            'printf "rclone %s\\n" "$*" >> "$CALLS"',
            'if [ "$1" = copyto ]; then cp "$2" "$CALLS.tar.gz"; fi',
        ]))
        result = self.run_backup()
        self.assertEqual(result.returncode, 0, result.stderr)
        with tarfile.open(str(self.log) + '.tar.gz') as archive:
            member = archive.getmember('plex-config/remote-link')
            self.assertTrue(member.issym())
            self.assertEqual(member.linkname, '/mnt/zurg/missing-media')

    def test_archive_failure_recovers_containers(self):
        self.mock('tar', 'exit 2\n')
        result = self.run_backup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('docker start media-id arr-id', self.calls())
        self.assertNotIn('rclone', self.calls())

    def test_stop_and_restart_failure(self):
        for fail in ('stop', 'start'):
            with self.subTest(fail=fail):
                result = self.run_backup(fail)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('docker start', self.calls())
                self.assertNotIn('rclone', self.calls())

    def test_no_running_containers(self):
        result = self.run_backup(running='0')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('docker stop', self.calls())
        self.assertNotIn('docker start', self.calls())

    def test_missing_required_file(self):
        (self.media / 'docker-compose.yml').unlink()
        self.assertNotEqual(self.run_backup().returncode, 0)
        self.assertEqual(self.calls(), '')

    def test_invalid_retention(self):
        self.env['RETENTION_DAYS'] = '0'
        self.assertNotEqual(self.run_backup().returncode, 0)
        self.assertEqual(self.calls(), '')

    def test_lock_contention(self):
        import shutil
        if not shutil.which('flock'):
            self.skipTest('Linux flock required')
        import fcntl
        with (self.media / '.config-backup.lock').open('w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertNotEqual(self.run_backup().returncode, 0)
            self.assertEqual(self.calls(), '')
