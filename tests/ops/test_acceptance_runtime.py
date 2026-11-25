"""Read-only/failure safety contracts for the one-shot synthetic VPS probe."""
import json
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest.mock import patch, Mock

sys.path.insert(0, str(Path(__file__).resolve().parents[2]/'scripts/ops'))
import acceptance_runtime as runtime
import common
import remote


class RuntimeProbeTests(unittest.TestCase):
    def test_refuses_nonroot_or_nonlinux_before_paths(self):
        for platform, uid in [('darwin', 0), ('linux', 1000)]:
            with self.subTest(platform=platform, uid=uid), patch.object(runtime.sys, 'platform', platform), patch.object(runtime.os, 'geteuid', return_value=uid), patch.object(common, 'Paths') as paths:
                with self.assertRaisesRegex(RuntimeError, 'Run on VPS'):
                    runtime.run()
                paths.assert_not_called()

    def test_refuses_real_or_wrong_installation_before_admin_or_lock(self):
        for synthetic, identity in [(False,runtime.EXPECTED_ID), (True,'0'*32)]:
            with tempfile.TemporaryDirectory() as tmp:
                etc=Path(tmp);(etc/'identity.json').write_text(json.dumps({'synthetic':synthetic}))
                paths=Mock(etc=etc);paths.identity.return_value=identity
                with patch.object(runtime.sys,'platform','linux'),patch.object(runtime.os,'geteuid',return_value=0),patch.object(common,'Paths',return_value=paths) as constructor,patch.object(common,'lock') as lock,patch.object(remote,'Admin') as admin:
                    with self.assertRaisesRegex(RuntimeError,'Only expected synthetic'):
                        runtime.run()
                    constructor.assert_called_once_with('acceptance');lock.assert_not_called();admin.assert_not_called()

    def test_digest_preserves_ids_blobs_and_history_without_db_byte_dependency(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'fixture.db'
            with sqlite3.connect(path) as db:
                db.executescript('CREATE TABLE tasks(id TEXT PRIMARY KEY, title TEXT); CREATE TABLE history(id TEXT PRIMARY KEY, task TEXT REFERENCES tasks(id), content BLOB); INSERT INTO tasks VALUES("task-a","Synthetic"); INSERT INTO history VALUES("history-a","task-a",x\'000102\');')
            before=runtime.database_digest(path)
            with sqlite3.connect(path) as db: db.execute('VACUUM')
            self.assertEqual(runtime.database_digest(path),before)
            self.assertEqual(before['table_counts'],{'history':1,'tasks':1})
            with sqlite3.connect(path) as db: db.execute('UPDATE history SET content=?',(b'changed',))
            self.assertNotEqual(runtime.database_digest(path)['sha256'],before['sha256'])
            with sqlite3.connect(path) as db: db.execute('UPDATE history SET task="missing"')
            with self.assertRaisesRegex(RuntimeError,'foreign keys'):runtime.database_digest(path)

    def test_digest_rejects_symlink_and_corruption(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'bad.db';path.write_bytes(b'not a database')
            link=Path(tmp)/'link.db';link.symlink_to(path)
            with self.assertRaisesRegex(RuntimeError,'linked'):runtime.database_digest(link)
            with self.assertRaises(sqlite3.DatabaseError):runtime.database_digest(path)

    def test_command_failure_does_not_expose_output_and_timeout_kills_child(self):
        with self.assertRaises(RuntimeError) as error:
            runtime.invoke([sys.executable,'-c','import sys; print("synthetic-private-value",file=sys.stderr); sys.exit(3)'])
        self.assertNotIn('synthetic-private-value',str(error.exception))
        out,metrics=runtime.invoke([sys.executable,'-c','print("ok")'])
        self.assertEqual(out,b'ok\n');self.assertEqual(metrics['exit'],0)
        with self.assertRaisesRegex(RuntimeError,'Timed out'):
            runtime.invoke([sys.executable,'-c','import time; time.sleep(30)'],timeout=0.05)


if __name__ == '__main__':
    unittest.main()
