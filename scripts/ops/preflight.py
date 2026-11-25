#!/usr/bin/env python3
"""Read-only VPS baseline before installation. Writes only a sanitized result beside this script.
Run via interactive sudo in the operator's terminal; never collects credentials or private keys.
"""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import ssl
import subprocess
import tempfile
from common import IP,ADMIN,Runner,Paths,read,atomic,require,Refused
from remote import Admin
import caddy_policy


def collect(run=None,root='/'):
    run=run or Runner();p=Paths('production',root)
    out={'observed_at':dt.datetime.now(dt.timezone.utc).isoformat(),'changes_applied':False}
    raw=read(p.at('/etc/caddy/Caddyfile'),128<<10)
    require(b'PRIVATE KEY' not in raw,'Unexpected private material in Caddyfile')
    out['caddyfile_sha256']=hashlib.sha256(raw).hexdigest()
    version=run(['/usr/bin/caddy','version']).stdout.decode().split()[0]
    require(version=='v2.11.9','Unexpected Caddy version; review before installation')
    out['caddy_version']=version
    config=json.loads(run(['/usr/bin/caddy','adapt','--config',p.at('/etc/caddy/Caddyfile'),'--adapter','caddyfile']).stdout)
    expected=['/etc/caddy/pki/mac-1.crt','/etc/caddy/pki/phone.crt']
    out['tls_policies']=caddy_policy.validate(config,expected)
    out['acme_storage']=config['storage']['root']
    out['public_devices']={Path(x).stem:Admin(p,run).cert(p.at(x)) for x in expected}
    for v in out['public_devices'].values():v.pop('details',None)
    for label,args in (
        ('firewall',['/usr/sbin/ufw','status','verbose']),
        ('listeners',['/usr/bin/ss','-lntup']),
        ('clock',['/usr/bin/timedatectl','show','--property=Timezone','--property=NTPSynchronized']),
        ('caddy_unit',['/usr/bin/systemctl','show','caddy','-p','User,Group,ActiveState,SubState,UnitFileState,MainPID,MemoryCurrent']),
        ('tailscale',['/usr/bin/systemctl','show','tailscaled','-p','LoadState,ActiveState,UnitFileState'])):
        result=run(args,check=False);out[label]={'exit':result.returncode,'output':result.stdout.decode()[:16000]}
    out['pending_paths']=[]
    for instance in ('production','acceptance'):
        paths=Paths(instance,root)
        for f in (paths.journal,paths.state/'.live.restore.json',paths.backups/'operation.json',paths.etc/'identity.json'):
            if f.exists():out['pending_paths'].append(str(f))
    global_state=p.global_state
    if (global_state/'revocation-pending.json').exists():out['pending_paths'].append(str(global_state/'revocation-pending.json'))
    response=run(['/usr/bin/openssl','s_client','-connect','127.0.0.1:443','-noservername','-showcerts'],input=b'',check=False,timeout=15)
    blocks=re.findall(rb'-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----',response.stdout,re.S)
    require(blocks,'No serving TLS certificate')
    with tempfile.TemporaryDirectory(prefix='budget-preflight-public-cert-') as t:
        leaf=Path(t)/'leaf.crt';leaf.write_bytes(blocks[0]+b'\n')
        chain=Path(t)/'chain.crt';chain.write_bytes(b'\n'.join(blocks[1:])+b'\n')
        args=['/usr/bin/openssl','verify','-purpose','sslserver','-verify_ip',IP,'-CApath','/etc/ssl/certs']
        if len(blocks)>1:args+=['-untrusted',chain]
        run([*args,leaf])
        out['served_certificate']=Admin(p,run).cert(leaf);out['served_certificate'].pop('details',None)
    require(not out['pending_paths'],'Existing installation or unfinished operation requires review')
    return out


def main():
    require(os.geteuid()==0,'Run preflight with interactive sudo')
    folder=Path(__file__).resolve().parent
    require(folder.parent==Path('/home/budgetadmin/.simple-budget-incoming') and re.fullmatch('[a-f0-9]{32}',folder.name),'Unexpected diagnostic upload directory')
    account=pwd.getpwnam(ADMIN)
    require(folder.stat().st_uid==account.pw_uid and folder.stat().st_mode&0o077==0,'Private administrator upload directory required')
    result=collect()
    dest=folder/'baseline.json';atomic(dest,result);os.chown(dest,account.pw_uid,account.pw_gid)
    print(json.dumps(result,ensure_ascii=False,indent=2))

if __name__=='__main__':
    try:main()
    except (Refused,OSError,ValueError,KeyError) as e:
        print('Read-only preflight refused: '+str(e));raise SystemExit(1)
