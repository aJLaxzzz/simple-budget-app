from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from legacy_acceptance import fingerprint


class LegacyProbeTests(unittest.TestCase):
    def test_logical_fingerprint_ignores_storage_order_but_detects_data_or_schema_changes(self):
        with tempfile.TemporaryDirectory() as temp:
            a,b=(Path(temp)/name for name in ('a.db','b.db'))
            for path,rows in ((a,[(1,'one'),(2,'two')]),(b,[(2,'two'),(1,'one')])):
                with sqlite3.connect(path) as db:
                    db.execute('CREATE TABLE sample(id INTEGER, value TEXT)')
                    db.executemany('INSERT INTO sample VALUES(?,?)',rows)
            self.assertEqual(fingerprint(a),fingerprint(b))
            with sqlite3.connect(b) as db:db.execute('UPDATE sample SET value=? WHERE id=2',('changed',))
            self.assertNotEqual(fingerprint(a),fingerprint(b))
            with sqlite3.connect(b) as db:
                db.execute('UPDATE sample SET value=? WHERE id=2',('two',));db.execute('CREATE INDEX by_id ON sample(id)')
            self.assertNotEqual(fingerprint(a),fingerprint(b))


if __name__=='__main__':unittest.main()
