#!/usr/bin/env python3
"""Repair only an existing empty SQLite lock after a stopped import trial.

Does not import/restore data, change the release, or start the application.
The existing recover command must perform start, HTTPS proof and commit.
"""
import fcntl
import os
import pwd
import stat
import sys

if __name__ == '__main__':
    sys.path.insert(0, '/usr/local/lib/simple-budget/ops')
from common import Paths, Refused, ID, require, regular, load, sha, lock
from remote import Admin


def repair(a, identity, operation):
    p = a.p
    require(ID.fullmatch(identity) and ID.fullmatch(operation), 'Invalid identity')
    require(a.service_state() == 'stopped', 'Application must be stopped')
    require(p.identity() == identity, 'Wrong installation')
    journal = load(p.journal)
    require(journal.get('kind') == 'initialize' and journal.get('phase') == 'trial'
            and journal.get('id') == operation and journal.get('installation_id') == identity,
            'Only the matching unfinished initialization trial can be repaired')
    require(str(p.release()) == journal['new'], 'Release differs from journal')
    for path in (p.state/'.live.restore.json', p.backups/'operation.json',
                 p.global_state/'revocation-pending.json'):
        require(not path.exists() and not path.is_symlink(), 'Other recovery pending')
    require(load(p.live/'installation.json')['installation_id'] == identity, 'Live identity mismatch')
    require(journal.get('archive'), 'Imported initialization archive required')
    archive = p.state/('restore-source-'+operation+'.tar.gz')
    require(str(archive) == journal['archive'] and sha(archive) == journal['archive_sha256'],
            'Retained import archive changed')
    account = pwd.getpwnam(p.user)
    path = p.live/'planner.db.lock'
    parent = os.open(p.state, os.O_RDONLY | os.O_NOFOLLOW)
    fd = None
    try:
        fcntl.flock(parent, fcntl.LOCK_EX | fcntl.LOCK_NB)
        old = regular(path, 0)
        require((old.st_uid, old.st_gid) in ((0, 0), (account.pw_uid, account.pw_gid))
                and stat.S_IMODE(old.st_mode) == 0o600, 'Unexpected lock owner/mode')
        fd = os.open(path, os.O_RDWR | os.O_NOFOLLOW)
        opened = os.fstat(fd)
        require((opened.st_dev, opened.st_ino, opened.st_nlink) ==
                (old.st_dev, old.st_ino, 1), 'Lock changed during open')
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        preserved = {f: sha(f) for f in p.live.iterdir() if f != path}
        journal_sha = sha(p.journal)
        os.fchown(fd, account.pw_uid, account.pw_gid)
        os.fchmod(fd, 0o600)
        os.fsync(fd)
        now = os.fstat(fd)
        require((now.st_uid, now.st_gid, now.st_ino, now.st_nlink) ==
                (account.pw_uid, account.pw_gid, old.st_ino, 1), 'Ownership repair failed')
        require(all(sha(f) == digest for f, digest in preserved.items())
                and sha(p.journal) == journal_sha, 'Data or journal changed')
        a.system('reset-failed', p.unit)
        return {'result': 'initialization_lock_owner_repaired', 'installation_id': identity,
                'request_id': operation, 'data_and_journal_unchanged': True,
                'lock_inode_preserved': True, 'application_started': False}
    finally:
        if fd is not None:
            os.close(fd)
        os.close(parent)


if __name__ == '__main__':
    import json
    try:
        require(os.geteuid() == 0 and len(sys.argv) == 4, 'Expected root and INSTANCE ID OPERATION')
        a = Admin(Paths(sys.argv[1]))
        with lock(a.p.global_state/'operations.lock'):
            result = repair(a, sys.argv[2], sys.argv[3])
        print(json.dumps(result))
    except (Refused, OSError, ValueError, KeyError) as error:
        print('Lock repair refused: '+str(error), file=sys.stderr)
        raise SystemExit(1)
