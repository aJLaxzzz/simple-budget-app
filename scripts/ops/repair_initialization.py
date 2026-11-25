#!/usr/bin/env python3
"""Narrow recovery of a stopped, never-published synthetic initialization.

Uploaded explicitly with a clean release; uses installed fixed-operation helpers.
No migration, password replacement, data recreation or Caddy configuration edits.
Normal recover performs the subsequent start / HTTPS / commit protocol.
"""
import os
from pathlib import Path
import sys
import tempfile
if __name__ == '__main__':
    sys.path.insert(0, '/usr/local/lib/simple-budget/ops')
from common import *
from remote import Admin, request_for


def repair(a, incoming, request):
    p=a.p
    require(p.instance=='acceptance' and request.get('confirm') is True, 'Only explicitly confirmed synthetic acceptance repair is supported')
    ident=p.identity()
    require(request.get('installation_id')==ident and load(p.etc/'identity.json').get('synthetic') is True, 'Synthetic installation identity mismatch')
    j=load(p.journal)
    require(j.get('kind')=='initialize' and j.get('phase') in ('trial','repairing-initialization') and j.get('installation_id')==ident and j.get('id')==request.get('operation_id'), 'Only the matching unfinished initialization can be repaired')
    require(a.service_state()=='stopped', 'Application must be stopped; inspect status/recover instead of repairing a running trial')
    require(not (p.state/'.live.restore.json').exists() and not (p.backups/'operation.json').exists(), 'Storage recovery is pending')
    require(not (p.global_state/'revocation-pending.json').exists(), 'Device revocation is pending')
    proxy=load(p.global_state/'proxy.json') if (p.global_state/'proxy.json').exists() else {}
    require(not proxy or proxy.get('mode')=='test-page', 'Public proxy has already changed; use normal recovery')
    current=p.release()
    require(str(current) in (j['new'],j.get('repair_from')), 'Release pointer differs from the initialization journal')
    m=a.manifest(incoming)
    require(m['schema_version']==j['version']['schema_version'], 'Repair cannot change schema')
    new,m=a.install_release(incoming)
    a.run([new/'budget','admin','check','--data-dir',p.live,'--installation-id',ident],timeout=120)
    # Real Linux/systemd test, using a synthetic secret and service user. It
    # cannot print a password: the command emits only valid/error, no data.
    with tempfile.TemporaryDirectory(prefix='budget-credential-probe-') as tmp:
        credential=Path(tmp)/'app-password';atomic(credential,b'Synthetic-probe-16\n')
        a.run(['/usr/bin/systemd-run','--unit=budget-credential-check-'+request['request_id'][:12],
               '--wait','--pipe','--collect','-p','User='+p.user,
               '-p','LoadCredential=app-password:'+str(credential),
               new/'budget','admin','check-credential'],timeout=60)
    if not j.get('repair_backup_id'):
        backup=a.backup('pre-update',True,False)
        j['repair_backup_id']=backup['manifest']['backup_id']
    j.setdefault('repair_from',str(current));j['new']=str(new);j['version']=m
    # If interrupted between journal and pointer, startup stays closed and the
    # same command may safely finish. Data/schema/credential never change.
    a.save_journal(j,'repairing-initialization')
    pointer(p.opt/'current',new)
    a.save_journal(j,'trial')
    a.system('reset-failed',p.unit)
    return {'result':'initialization_release_repaired','request_id':j['id'],
            'version':m,'backup_id':j['repair_backup_id'],'credential_probe':'passed'}


def main():
    require(os.geteuid()==0 and len(sys.argv)==2,'Expected sudo repair script and request token')
    incoming,request=request_for(sys.argv[1],'repair-initialization','acceptance')
    a=Admin(Paths('acceptance'))
    with lock(a.p.global_state/'operations.lock'):
        result=repair(a,incoming,request)
    atomic(incoming/'result.json',result)
    import pwd
    account=pwd.getpwnam(ADMIN);os.chown(incoming/'result.json',account.pw_uid,account.pw_gid)
    # Permit the existing allowlisted clean-incoming command after success.
    regular(incoming/'repair.py',owner=account.pw_uid);(incoming/'repair.py').unlink()
    print(json.dumps(result))

if __name__=='__main__':
    try:main()
    except (Refused,OSError,ValueError,KeyError) as e:
        print('Initialization repair refused: '+str(e),file=sys.stderr);sys.exit(1)
