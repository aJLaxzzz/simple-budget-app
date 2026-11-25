import os
from pathlib import Path
import pwd
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import Paths, Refused, atomic, load, pointer, sha
from remote import Admin
from repair_initialization_lock import repair


class InitializationLockTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.p = Paths('production', self.tmp.name)
        self.release = self.p.opt/'releases'/'synthetic'
        for path in (self.p.live, self.p.etc, self.p.backups, self.p.global_state, self.release):
            path.mkdir(parents=True, exist_ok=True)
        self.identity, self.operation = '1'*32, '2'*32
        identity = {'instance': 'production', 'installation_id': self.identity}
        atomic(self.p.etc/'identity.json', identity)
        atomic(self.p.live/'installation.json', dict(identity, environment='production'))
        atomic(self.p.live/'data.json', {'months': []})
        pointer(self.p.opt/'current', self.release)
        archive = self.p.state/('restore-source-'+self.operation+'.tar.gz')
        atomic(archive, b'synthetic archive placeholder')
        self.journal = dict(kind='initialize', phase='trial', id=self.operation,
                            installation_id=self.identity, new=str(self.release), version={},
                            identity=identity, configuration={'BUDGET_ENV': 'production'},
                            archive=str(archive), archive_sha256=sha(archive))
        atomic(self.p.journal, self.journal)
        self.a = Admin(self.p)
        self.account = pwd.getpwuid(os.getuid())

    def tearDown(self):
        self.tmp.cleanup()

    def test_imported_missing_lock_is_accessible_after_first_backup_and_retry(self):
        path = self.p.live/'planner.db.lock'
        def backup(*args):
            if not path.exists():
                # Model a maintenance-created file not writable by the web user.
                atomic(path, b'', 0o400)
        def ownership():
            for file in self.p.live.iterdir():
                file.chmod(0o600)
        def start(*args):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with path.open('r+b'):
                pass
        with patch.object(self.a, 'manifest', return_value={}), \
                patch.object(self.a, 'run', return_value=subprocess.CompletedProcess([], 0)), \
                patch.object(self.a, 'backup', side_effect=backup), \
                patch.object(self.a, 'own_live', side_effect=ownership), \
                patch.object(self.a, 'controller', side_effect=start), \
                patch.object(self.a, 'proxy'):
            self.a.finish_initialization(dict(self.journal))
            self.a.finish_initialization(dict(self.journal))
        self.assertEqual(load(self.p.live/'data.json'), {'months': []})

    def run_repair(self):
        with patch('repair_initialization_lock.pwd.getpwnam', return_value=self.account), \
                patch.object(self.a, 'service_state', return_value='stopped'), \
                patch.object(self.a, 'system') as system:
            result = repair(self.a, self.identity, self.operation)
            system.assert_called_once_with('reset-failed', self.p.unit)
            return result

    def test_repair_is_idempotent_and_preserves_inode_data_and_journal(self):
        path = self.p.live/'planner.db.lock'; atomic(path, b'')
        inode = path.stat().st_ino
        journal = sha(self.p.journal); data = sha(self.p.live/'data.json')
        for _ in range(2):
            self.assertFalse(self.run_repair()['application_started'])
        self.assertEqual(path.stat().st_ino, inode)
        self.assertEqual(sha(self.p.journal), journal)
        self.assertEqual(sha(self.p.live/'data.json'), data)

    def test_symlink_hardlink_and_nonempty_lock_refused(self):
        path = self.p.live/'planner.db.lock'; target = self.p.state/'target'
        atomic(target, b'')
        path.symlink_to(target)
        with self.assertRaises(Refused): self.run_repair()
        path.unlink(); os.link(target, path)
        with self.assertRaises(Refused): self.run_repair()
        path.unlink(); atomic(path, b'not a lock')
        with self.assertRaises(Refused): self.run_repair()

    def test_wrong_identity_operation_and_published_phase_refused(self):
        atomic(self.p.live/'planner.db.lock', b'')
        with patch.object(self.a, 'service_state', return_value='stopped'):
            for identity, operation in (('3'*32, self.operation), (self.identity, '3'*32)):
                with self.assertRaises(Refused): repair(self.a, identity, operation)
            atomic(self.p.journal, dict(self.journal, phase='committed'))
            with self.assertRaises(Refused): repair(self.a, self.identity, self.operation)


if __name__ == '__main__':
    unittest.main()
