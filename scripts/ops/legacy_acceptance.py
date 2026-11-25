#!/usr/bin/env python3
"""Unprivileged Linux probe of the approved binary, exclusively in private synthetic /tmp.
No live process, real source data, credentials, systemd or Caddy are changed.
"""
import hashlib
import argparse
import re
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tarfile
import tempfile
import uuid

BINARY = Path('/opt/simple-budget-acceptance/releases/37b5dcde4f3b-d0ac7d8002708002/budget')
DIGEST = 'd0ac7d80027080025c621545eba5d5e041d9ea3564af4dfd55e322921831a6b8'


def fingerprint(path):
    # VACUUM may reorder physical rows/dump statements; compare all logical
    # tables plus exact schema, not incidental SELECT/iterdump output order.
    digest = hashlib.sha256()
    with sqlite3.connect(path.as_uri()+'?mode=ro&immutable=1', uri=True) as db:
        schema = db.execute("SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name").fetchall()
        digest.update(repr(schema).encode())
        names = db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name").fetchall()
        for (name,) in names:
            rows = sorted(repr(row) for row in db.execute('SELECT * FROM "'+name.replace('"','""')+'"'))
            digest.update(repr((name, rows)).encode())
    return digest.hexdigest()


def main(binary=BINARY, digest=DIGEST):
    assert os.uname().sysname == 'Linux' and os.geteuid() != 0
    assert binary.is_absolute() and binary.is_file() and not binary.is_symlink()
    assert re.fullmatch('[a-f0-9]{64}', digest)
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == digest
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'LANG')}
    def run(*args):
        result = subprocess.run([str(binary), *map(str, args)], env=env, capture_output=True, timeout=60)
        assert result.returncode == 0, 'Synthetic CLI failed: ' + str(args[:2]) + ': ' + result.stderr.decode()[:500]
        return json.loads(result.stdout)
    with tempfile.TemporaryDirectory(prefix='budget-legacy-auth-') as temp:
        root = Path(temp); source = root/'source'; source.mkdir(mode=0o700)
        run('admin', 'init', '--data-dir', source, '--environment', 'production', '--confirm')
        # Only this newly generated synthetic marker is removed to reproduce legacy layout.
        (source/'installation.json').unlink()
        (source/'data.json').write_text(json.dumps({'months':[{'id':'month-2040-12','year':2040,'month':12,'income':123,
                                                              'incomes':[],'expenses':[]}]}))
        (source/'categories.json').write_text(json.dumps({'expense':[{'id':'synthetic','name':'Synthetic','subcategories':[]}],'income':[]}))
        (source/'web-auth.json').write_text(json.dumps({'synthetic_secret':'never-export-this'}))
        for f in source.iterdir(): f.chmod(0o600)
        before = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in source.iterdir()}
        contents = fingerprint(source/'planner.db')
        repo = root/'transfer'; repo.mkdir(mode=0o700); identity = uuid.uuid4().hex
        result = run('admin', 'legacy-export', '--source', source, '--backup-dir', repo,
                     '--installation-id', identity, '--confirm-stopped')
        archive = Path(result['archive'])
        assert root in archive.parents
        manifest = run('admin', 'backup', 'verify', '--archive', archive)
        assert manifest['installation_id'] == identity and manifest['sqlite']['schema_version'] == 14
        with tarfile.open(archive, 'r:gz') as tar:
            assert not any('web-auth' in item.name for item in tar)
        assert before == {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in source.iterdir()}
        parent = root/'destination'; parent.mkdir(mode=0o700); live = parent/'live'
        restore = ['admin','backup','restore','--environment','production','--data-dir',live,'--backup-dir',repo,
                   '--installation-id',identity,'--archive',archive,'--scope','full','--confirm']
        run(*restore)
        assert fingerprint(live/'planner.db') == contents
        with sqlite3.connect((live/'planner.db').as_uri()+'?mode=ro',uri=True) as db:
            assert db.execute('PRAGMA journal_mode').fetchone()==('wal',)
        for name in ('data.json','categories.json'):
            assert (live/name).read_bytes() == (source/name).read_bytes()
        assert not (live/'web-auth.json').exists()
        assert json.loads((live/'installation.json').read_text())['installation_id'] == identity
        (live/'web-auth.json').write_text('{"synthetic_secret":"old-session"}'); (live/'web-auth.json').chmod(0o600)
        run(*restore)
        assert not (live/'web-auth.json').exists()
        assert run('admin','check','--data-dir',live,'--installation-id',identity)['schema_version'] == 14
        print(json.dumps({'passed':True,'binary_sha256':digest,'environment':'Linux temporary synthetic directories',
                          'checks':['legacy source unchanged','transfer identity retained','schema14 format1 archive verified',
                                    'financial JSON byte preservation','SQLite logical content preserved','no auth sidecar in archive',
                                    'new installation restore with WAL','existing session sidecar invalidated on restore'],
                          'migrate_data_wrapper':'not run: production initialization remains forbidden'}))


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,default=BINARY)
    parser.add_argument('--sha256',default=DIGEST)
    args=parser.parse_args();main(args.binary,args.sha256)
