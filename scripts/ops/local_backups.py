"""Private, no-overwrite local archives. Never rotates or moves old user files."""
import datetime as dt
import os
from pathlib import Path
import stat
from zoneinfo import ZoneInfo
from common import Refused, require, regular, sha, sync_dir

REPOSITORY=Path(__file__).resolve().parents[2]

def private_directory(path, private=True):
    path=Path(os.path.abspath(Path(path).expanduser()))
    # macOS system /var and /tmp aliases are not user-controlled links.
    for part in reversed([path,*path.parents]):
        if str(part) in ('/','/var','/tmp'): continue
        if not part.exists() and not part.is_symlink(): part.mkdir(mode=0o700)
        st=part.lstat()
        require(stat.S_ISDIR(st.st_mode),'Backup directory symlink/non-directory refused')
    st=path.stat()
    require(st.st_uid==os.getuid() and st.st_mode&(0o077 if private else 0o022)==0,'Backup directory must be owned by you with mode 0700')
    resolved=path.resolve(); repo=REPOSITORY.resolve()
    require(resolved!=repo and repo not in resolved.parents,'Backups must be outside the Git repository')
    return path

def directory(category):
    require(category in ('production','acceptance','migration','pki'),'Invalid backup category')
    base=private_directory(Path.home()/'simple-budget-backups',private=False)
    return private_directory(base/category)

def archive_destination(instance,ident):
    from common import BACKUP_ID
    require(instance in ('acceptance','production') and BACKUP_ID.fullmatch(ident or ''),'Invalid archive scope/ID')
    return directory(instance)/(ident+'.tar.gz')

def dmg_destination(explicit=None, now=None):
    if explicit:
        p=Path(os.path.abspath(Path(explicit).expanduser()))
        private_directory(p.parent)
        require(p.suffix=='.dmg' and not os.path.lexists(p),'A new .dmg filename is required')
        return p
    parent=directory('pki')
    now=now or dt.datetime.now(dt.timezone.utc)
    require(now.tzinfo is not None,'Timezone-aware timestamp required')
    stem='pki-'+now.astimezone(ZoneInfo('Europe/Moscow')).strftime('%Y-%m-%d_%H-%M-%S')
    for n in range(10000):
        p=parent/(stem+('' if n==0 else '-'+str(n))+'.dmg')
        if not os.path.lexists(p):return p
    raise Refused('Too many PKI filename collisions')

def publish(source,dest,expected=None):
    """Atomic no-replace publication on the same filesystem; identical is idempotent."""
    source=Path(source); dest=Path(dest); regular(source)
    digest=sha(source)
    require(expected is None or digest==expected,'Downloaded checksum mismatch')
    try:os.link(source,dest,follow_symlinks=False)
    except FileExistsError:
        regular(dest,owner=os.getuid())
        require(dest.stat().st_mode&0o077==0 and sha(dest)==digest,'Existing backup differs or is unsafe; never overwrite')
        return False
    source.unlink();sync_dir(dest.parent)
    return True
