#!/usr/bin/env python3
"""Explicit infrastructure preparation only; no app data, Caddy restart or certificate issuance."""
import grp
import json
import os
from pathlib import Path
import platform
import pwd
import shutil
import sys
import tempfile
from common import *
from remote import Admin
import caddy_policy

def safe_admin_directory(path,root):
    for parent in [path,*path.parents]:
        require(not parent.is_symlink(),'Symlink administrative ancestor refused')
        if parent.exists() and root=='/':require(parent.stat().st_uid==0 and parent.stat().st_mode&0o022==0,'Administrative ancestor must be root-owned and not writable by others')

def verify_units_before_initialization(units,run,root='/'):
    # Offline verification checks executable presence even for disabled units.
    # Do not initialize production merely to satisfy that check. The installed
    # units remain untouched; controller.start later verifies their real binary.
    with tempfile.TemporaryDirectory(prefix='budget-unit-verify-') as temporary:
        inputs=[]
        for instance in ('production','acceptance'):
            paths=Paths(instance,root);original=units/paths.unit
            if (paths.opt/'current'/'budget').exists():inputs.append(original);continue
            text=read(original).decode()
            expected='ExecStart=/opt/'+paths.name+'/current/budget'
            require(text.count(expected)==1,'Unexpected application command in unit')
            shadow=Path(temporary)/paths.unit
            shadow.write_text(text.replace(expected,'ExecStart=/usr/bin/true'))
            inputs.append(shadow)
        run(['/usr/bin/systemd-analyze','verify',*map(str,inputs),str(units/'simple-budget-backup@production.timer')])

def _install(bundle,run=None,root='/'):
    run=run or Runner();p=Paths('production',root)
    require(root!='/' or os.geteuid()==0,'Run installer through sudo')
    require(root!='/' or platform.system()=='Linux' and platform.machine()=='x86_64','Ubuntu Linux amd64 required')
    release=read_os_release(p.root,owner=0 if p.root==Path('/') else os.getuid()).decode()
    require('ID=ubuntu' in release and 'VERSION_ID="24.04"' in release,'Expected Ubuntu 24.04 LTS')
    space(p.at('/var'),256<<20,128)
    info=read(p.at('/proc/meminfo'),16384).decode();require(int(re.search(r'MemTotal:\s+(\d+)',info)[1])>=700*1024,'At least 700 MiB physical RAM required')
    version=run(['/usr/bin/caddy','version']).stdout.decode().split()[0]
    require(version=='v2.11.9','Unreviewed Caddy version: validate exact module/config compatibility first')
    original=read(p.at('/etc/caddy/Caddyfile'),128<<10)
    if (p.global_state/'caddy-original').exists():require(read(p.global_state/'caddy-original')==original,'Caddyfile changed since infrastructure audit; review explicitly')
    adapted=json.loads(run(['/usr/bin/caddy','adapt','--config',p.at('/etc/caddy/Caddyfile'),'--adapter','caddyfile'],timeout=30).stdout)
    expected=['/etc/caddy/pki/mac-1.crt','/etc/caddy/pki/phone.crt']
    if (p.global_state/'clients.json').exists():
        expected=Admin(p,run).registry()[1]
        # Original Caddyfile is preserved as an audit source; current managed JSON uses registry.
        caddy_policy.validate(load(p.at('/etc/caddy/simple-budget.json')),expected,True) if p.at('/etc/caddy/simple-budget.json').exists() else None
    else:caddy_policy.validate(adapted,expected)
    manifest=load(bundle/'bundle.json')
    required={'ops/'+x for x in ('common.py','remote.py','bootstrap.py','caddy_policy.py')}
    required|={'systemd/'+x for x in ('simple-budget@.service','simple-budget-backup@.service','simple-budget-backup@.timer','simple-budget-tls.service','simple-budget-tls.timer','simple-budget-schedule.service','simple-budget-schedule.timer','caddy-budget.conf','journald-budget.conf')}
    require(set(manifest)==required,'Unexpected infrastructure bundle files')
    for name,digest in manifest.items():require(SHA.fullmatch(digest) and sha(bundle/name)==digest,'Infrastructure checksum mismatch')
    if root=='/':
        for user in ('budget','budget-acceptance'):
            try:
                account=pwd.getpwnam(user);require(account.pw_shell=='/usr/sbin/nologin' and account.pw_uid!=0,'Existing app user is not a system-only account')
                require(not any(g.gr_name=='sudo' and user in g.gr_mem for g in grp.getgrall()),'Application user must not have sudo membership')
            except KeyError:run(['/usr/sbin/useradd','--system','--user-group','--home-dir','/nonexistent','--no-create-home','--shell','/usr/sbin/nologin',user])
    for inst in ('production','acceptance'):
        q=Paths(inst,root)
        if q.live.exists() and list(q.live.iterdir()):
            require((q.etc/'identity.json').exists() and (q.live/'installation.json').exists(),'Existing data without a known installation: refuse automatic adoption')
            require(load(q.live/'installation.json')['installation_id']==q.identity(),'Existing live installation ID mismatch')
        require(not q.journal.exists(),'Recover the interrupted installation before reinstalling infrastructure')
        gid=grp.getgrnam(q.user).gr_gid if root=='/' else os.getgid()
        for directory,mode in ((q.opt,0o755),(q.opt/'releases',0o755),(q.state,0o750),(q.backups,0o750),(q.etc,0o700),(q.etc/'credentials',0o700)):
            safe_admin_directory(directory,root)
            if directory.exists():require(directory.is_dir() and not directory.is_symlink(),'Ambiguous existing installation path')
            directory.mkdir(mode=mode,parents=True,exist_ok=True)
            if root=='/':require(directory.stat().st_uid==0 and directory.stat().st_mode&0o022==0,'Existing administrative path has unsafe owner/permissions')
            if directory in (q.state,q.backups):
                os.chown(directory,os.getuid(),gid);os.chmod(directory,mode)
    p.global_state.mkdir(mode=0o700,exist_ok=True)
    administrative=[p.global_state,p.at('/usr/local/lib'),p.at('/usr/local/sbin'),p.at('/etc/systemd/system')]
    for directory in administrative:
        for parent in [directory,*directory.parents]:
            require(not parent.is_symlink(),'Symlink administrative directory refused')
            if parent.exists() and root=='/':require(parent.stat().st_uid==0 and parent.stat().st_mode&0o022==0,'Administrative ancestor must be root-owned and not writable by others')
    if not (p.global_state/'caddy-original').exists():
        atomic(p.global_state/'caddy-original',original)
        atomic(p.global_state/'caddy-base.json',adapted)
        admin=Admin(p,run)
        clients={Path(n).stem:{'fingerprint':admin.cert(p.at(n))['fingerprint'],'state':'allowed','imported_at':utc()} for n in expected}
        atomic(p.global_state/'clients.json',{'format':1,'clients':clients})
    destination=p.at('/usr/local/lib/simple-budget');destination.mkdir(parents=True,exist_ok=True)
    for name,digest in manifest.items():
        target=destination/name;target.parent.mkdir(mode=0o755,exist_ok=True)
        for directory in (destination,target.parent):
            require(not directory.is_symlink(),'Unsafe administrative code path')
            if root=='/':require(directory.stat().st_uid==0 and directory.stat().st_mode&0o022==0,'Administrative code must be root-owned')
        data=read(bundle/name,1024*1024);require(hashlib.sha256(data).hexdigest()==digest,'Bundle changed during installation')
        if target.exists() and sha(target)==digest:continue
        atomic(target,data,0o644)
    wrapper=b'#!/usr/bin/python3 -I\nimport sys\nsys.path.insert(0,"/usr/local/lib/simple-budget/ops")\nfrom remote import main\nmain()\n'
    controller=b'#!/usr/bin/python3 -I\nimport sys\nsys.path.insert(0,"/usr/local/lib/simple-budget/ops")\nfrom remote import controller_main\ncontroller_main()\n'
    for name,data in [('budget-admin',wrapper),('budget-controller',controller)]:atomic(p.at('/usr/local/sbin/'+name),data,0o755)
    units=p.at('/etc/systemd/system');units.mkdir(parents=True,exist_ok=True)
    template=read(destination/'systemd/simple-budget@.service').decode()
    for inst in ('production','acceptance'):
        q=Paths(inst,root)
        text=template.replace('simple-budget-%i',q.name).replace('%i',inst).replace('User=budget\n','User='+q.user+'\n').replace('Group=budget\n','Group='+q.user+'\n')
        atomic(units/q.unit,text.encode(),0o644)
    for file in (destination/'systemd').iterdir():
        if file.suffix in ('.service','.timer') and file.name!='simple-budget@.service':atomic(units/file.name,read(file),0o644)
    journal=p.at('/etc/systemd/journald.conf.d/simple-budget.conf');journal.parent.mkdir(parents=True,exist_ok=True)
    atomic(journal,read(destination/'systemd/journald-budget.conf'),0o644)
    run(['/usr/bin/systemctl','daemon-reload'])
    verify_units_before_initialization(units,run,root)
    atomic(p.global_state/'infrastructure.json',{'format':1,'installed_at':utc(),'caddy_version':version,'bundle':manifest})
    return {'result':'infrastructure_prepared','application_initialized':False,'caddy_changed':False,'caddy_version':version}

def install(bundle,run=None,root='/'):
    p=Paths('production',root)
    require(root!='/' or os.geteuid()==0 and platform.system()=='Linux' and platform.machine()=='x86_64','Installer requires Linux amd64 root')
    require(not p.global_state.is_symlink(),'Unsafe global administrative directory')
    safe_admin_directory(p.global_state,root)
    p.global_state.mkdir(mode=0o700,parents=True,exist_ok=True)
    if root=='/':require(p.global_state.stat().st_uid==0 and p.global_state.stat().st_mode&0o077==0,'Global administrator state must be private root-owned')
    with lock(p.global_state/'operations.lock'):
        result=_install(bundle,run,root)
    # OnBootSec may already have elapsed. Immediate monitor callbacks use the
    # same lock, so enable them only after the installation has released it.
    (run or Runner())(['/usr/bin/systemctl','enable','--now','simple-budget-tls.timer','simple-budget-schedule.timer'])
    return result

if __name__=='__main__':
    try:
        require(len(sys.argv)==1,'Bootstrap accepts no arbitrary arguments')
        print(json.dumps(install(Path(__file__).resolve().parent.parent)))
    except (Refused,OSError,ValueError,KeyError) as e:
        print('Installation refused: '+str(e),file=sys.stderr);sys.exit(1)
