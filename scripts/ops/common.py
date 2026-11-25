"""Bounded filesystem and process primitives shared by local administration tools."""
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile

IP = '171.22.237.16'
ADMIN = 'budgetadmin'
ID = re.compile(r'^[a-f0-9]{32}$')
BACKUP_ID = re.compile(r'^\d{8}T\d{6}Z-[a-f0-9]{16}$')
NAME = re.compile(r'^[a-z][a-z0-9-]{0,39}$')
SHA = re.compile(r'^[a-f0-9]{64}$')

class Refused(RuntimeError):
    pass

def require(value, message):
    if not value:
        raise Refused(message)

def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat()

def regular(path, limit=None, owner=None):
    p = Path(path)
    for parent in [p.parent, *p.parent.parents]:
        require(not parent.is_symlink() or str(parent) in ('/tmp','/var'), 'Symlink parent refused')
    st = p.lstat()
    require(stat.S_ISREG(st.st_mode) and st.st_nlink == 1, 'Regular non-linked file required')
    require(limit is None or st.st_size <= limit, 'File exceeds limit')
    require(owner is None or st.st_uid == owner, 'Unexpected file owner')
    return st

def read(path, limit=1024*1024, owner=None):
    st = regular(path, limit, owner)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as f:
        require(os.fstat(f.fileno()).st_ino == st.st_ino, 'File changed during read')
        data = f.read(limit+1)
    require(len(data) <= limit, 'File exceeds limit')
    return data

def read_os_release(system_root='/', owner=0):
    """Read OS identity, allowing only the distribution's exact os-release link.

    Never resolve arbitrary links: read the fixed canonical target with the same
    O_NOFOLLOW / single-link checks used for other files. Parent ownership and
    permissions prevent an unprivileged replacement of either endpoint.
    system_root/owner are injected only by isolated tests; server callers use /,0.
    """
    root=Path(system_root)
    require(root.is_absolute() and not root.is_symlink(),'Unsafe OS metadata root')
    source=root/'etc/os-release'
    def trusted_parents(path):
        for parent in path.parents:
            st=parent.lstat()
            require(stat.S_ISDIR(st.st_mode) and st.st_uid==owner and st.st_mode&0o022==0,'Unsafe OS metadata directory')
            if parent==root:return
        raise Refused('OS metadata escaped system root')
    try:
        trusted_parents(source)
        st=source.lstat()
        require(st.st_uid==owner,'Unexpected OS metadata owner')
        if stat.S_ISLNK(st.st_mode):
            require(st.st_nlink==1 and os.readlink(source) in ('../usr/lib/os-release','/usr/lib/os-release'),'Unexpected os-release symlink target')
            source=root/'usr/lib/os-release'
            trusted_parents(source)
        st=regular(source,16384,owner)
        require(st.st_mode&0o022==0,'OS metadata is writable by others')
        return read(source,16384,owner)
    except (Refused,OSError) as e:
        raise Refused('OS identity /etc/os-release: '+str(e)) from e

def load(path, limit=1024*1024, owner=None):
    return json.loads(read(path, limit, owner))

def sync_dir(path):
    fd = os.open(path, os.O_RDONLY)
    try: os.fsync(fd)
    finally: os.close(fd)

def atomic(path, value, mode=0o600):
    p = Path(path)
    require(not p.is_symlink() and not p.parent.is_symlink(), 'Unsafe publication path')
    if p.exists(): regular(p)
    fd, name = tempfile.mkstemp(prefix='.budget-', dir=p.parent)
    try:
        with os.fdopen(fd, 'wb') as f:
            os.fchmod(f.fileno(), mode)
            f.write(value if isinstance(value, bytes) else (json.dumps(value, indent=2)+'\n').encode())
            f.flush(); os.fsync(f.fileno())
        os.replace(name, p); sync_dir(p.parent)
    finally:
        if os.path.exists(name): os.unlink(name)

def sha(path):
    regular(path)
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for b in iter(lambda: f.read(1024*1024), b''): h.update(b)
    return h.hexdigest()

def copy_verified(source, target, expected=None, mode=0o600, limit=8 << 30):
    st = regular(source, limit)
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    h = hashlib.sha256()
    try:
        require(os.fstat(fd).st_ino == st.st_ino, 'Source changed')
        with os.fdopen(fd, 'rb') as src, open(target, 'xb') as dst:
            os.chmod(target, mode)
            for b in iter(lambda: src.read(1024*1024), b''):
                dst.write(b); h.update(b)
            dst.flush(); os.fsync(dst.fileno())
        require(expected is None or h.hexdigest() == expected, 'Checksum mismatch')
        sync_dir(Path(target).parent)
    except BaseException:
        Path(target).unlink(missing_ok=True)
        raise

def space(path, extra=0, inodes=64):
    s = os.statvfs(path)
    reserve = max(2 << 30, (s.f_blocks*s.f_frsize*15+99)//100)
    require(s.f_bavail*s.f_frsize >= reserve+extra, 'Insufficient disk reserve (2 GiB / 15% plus staging)')
    require(s.f_favail >= inodes, 'Insufficient free inodes')

@contextlib.contextmanager
def lock(path):
    p=Path(path)
    if p.exists(): regular(p)
    fd=os.open(p, os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW, 0o600)
    try:
        try: fcntl.flock(fd, fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError: raise Refused('Another administrative operation is running')
        yield
    finally: os.close(fd)

class Runner:
    def __call__(self, args, *, timeout=120, check=True, env=None, input=None):
        # No shell. Never include raw stderr or a credential-bearing environment in errors.
        try:
            p=subprocess.run([str(x) for x in args], input=input, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, timeout=timeout, env=env)
        except (OSError, subprocess.TimeoutExpired):
            raise Refused('Command unavailable or timed out: '+Path(args[0]).name)
        if check and p.returncode:
            raise Refused('Command failed: '+Path(args[0]).name+' (exit '+str(p.returncode)+')')
        return p

class Paths:
    def __init__(self, instance='production', root='/'):
        require(instance in ('production','acceptance'), 'Unknown installation')
        self.instance=instance; self.root=Path(root).resolve()
        self.name='simple-budget'+('-acceptance' if instance=='acceptance' else '')
        self.port=3001 if instance=='production' else 3002
        self.user='budget' if instance=='production' else 'budget-acceptance'
        self.opt=self.at('/opt/'+self.name)
        self.state=self.at('/var/lib/'+self.name)
        self.live=self.state/'live'
        self.backups=self.at('/var/backups/'+self.name)
        self.etc=self.at('/etc/'+self.name)
        self.config=self.etc/'configuration'
        self.journal=self.state/'deployment.json'
        self.global_state=self.at('/var/lib/simple-budget-admin')
        self.controller=self.at('/usr/local/sbin/budget-controller')
        self.unit=self.name+'.service'
    def at(self, absolute): return self.root/absolute.lstrip('/')
    def identity(self):
        c=load(self.etc/'identity.json')
        require(ID.fullmatch(c['installation_id']) and c['instance']==self.instance, 'Installation identity mismatch')
        return c['installation_id']
    def release(self, which='current'):
        require(which in ('current','previous'), 'Invalid release selector')
        link=self.opt/which
        require(link.is_symlink(), 'Release pointer missing')
        p=link.resolve(strict=True)
        require(p.parent==self.opt/'releases' and not p.is_symlink(), 'Release escapes installation')
        return p

def pointer(path, target):
    p=Path(path); tmp=p.with_name('.'+p.name+'-next')
    if tmp.exists() or tmp.is_symlink():
        require(tmp.is_symlink() and tmp.resolve(strict=True).parent==Path(target).parent, 'Unsafe interrupted release pointer')
        tmp.unlink();sync_dir(p.parent)
    require(not p.exists() or p.is_symlink(), 'Refusing non-symlink release pointer')
    os.symlink(str(target), tmp); os.replace(tmp,p); sync_dir(p.parent)
