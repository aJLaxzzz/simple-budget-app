"""Native CLI integration on synthetic data. Never SSH, systemctl, Caddy or real PKI.
Run: python3 tests/ops/cli_integration.py /absolute/path/to/isolated/native/budget
"""
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import *
from remote import Admin

class NativeAdmin(Admin):
    def __init__(self,p,helper):
        super().__init__(p);self.helper=helper;self.selected=p.opt/'releases'/'one';self.modes=[]
    def install_release(self,src):return self.selected,self.manifest(self.selected)
    def manifest(self,path,**kw):
        v=json.loads(self.run([path/'budget','version']).stdout)
        return dict(v,revision=('a' if path.name=='one' else 'b')*40,modified=False)
    def system(self,*args,**kw):return subprocess.CompletedProcess(args,0,b'',b'')
    def controller(self,action,ident):require(ident==self.p.identity(),'Test identity mismatch')
    def proxy(self,mode):self.modes.append(mode)
    def ready(self,*args):return self.manifest(self.p.release())
    def schedule(self):pass
    def own_live(self):
        require(load(self.p.live/'installation.json')['installation_id']==self.p.identity(),'Test identity mismatch')
    def prune_releases(self):pass

native=None
if len(sys.argv)==1:
    native=tempfile.TemporaryDirectory(prefix='budget-native-helper-')
    helper=Path(native.name)/'budget'
    subprocess.run(['go','build','-o',str(helper),'.'],cwd=Path(__file__).resolve().parents[2],check=True)
else:helper=Path(sys.argv[1]).resolve(strict=True)
with tempfile.TemporaryDirectory(prefix='budget-ops-native-') as t:
    p=Paths('acceptance',t)
    for d in (p.etc/'credentials',p.state,p.backups,p.global_state,p.opt/'releases'/'one',p.opt/'releases'/'two'):d.mkdir(parents=True,mode=0o700)
    for name in ('one','two'):shutil.copy2(helper,p.opt/'releases'/name/'budget')
    atomic(p.global_state/'stg-enabled.json',{'state':'ready'}) # OS/proxy setup mocked
    a=NativeAdmin(p,helper);request={'installation_id':'1'*32,'request_id':'2'*32,'confirm':True}
    with patch('getpass.getpass',return_value='Test-pass-12'):
        result=a.initialize(Path(t),request)
    assert read(p.etc/'credentials/app-password')==b'Test-pass-12\n'
    assert result['result']=='awaiting_https_acceptance'
    a.commit(dict(request,https_verified_revision='a'*40))
    first=a.backup('manual',False,False)
    ident=first['manifest']['backup_id'];archive=a.archive(ident)
    verified=json.loads(a.run([helper,'admin','backup','verify','--archive',archive]).stdout)
    assert verified['installation_id']=='1'*32
    a.selected=p.opt/'releases'/'two';request['request_id']='3'*32
    a.prepare(Path(t),request)
    atomic(p.live/'web-auth.json',{'version':1,'sessions':[]})
    assert load(p.journal)['phase']=='trial'
    a.recover(dict(request,decision='rollback',operation_id=request['request_id']))
    assert p.release().name=='one'
    assert not (p.live/'web-auth.json').exists()
    a.commit(dict(request,https_verified_revision='a'*40))
    previous=list(p.state.glob('.live.previous-*'));assert previous
    final=json.loads(a.run([helper,'admin','check','--data-dir',p.live,'--installation-id','1'*32]).stdout)
    assert final['schema_version']==14
    assert 'trial' in a.modes and 'closed' in a.modes
    # The real native CLI is required: production markers are intentionally
    # forbidden inside Go test processes. This tree is exclusively synthetic.
    request['request_id']='5'*32
    result=a.stg_reset(request)
    assert result['result']=='awaiting_https_acceptance' and p.journal.exists()
    assert load(p.journal)['backup_id']
    a.commit(dict(request,https_verified_revision='a'*40))
    with sqlite3.connect(p.live/'planner.db') as db:
        for table,minimum in {'projects':3,'tasks':26,'sessions':1,'series':2,'history_events':1,'planner_icons':1}.items():
            assert db.execute('SELECT count(*) FROM '+table).fetchone()[0]>=minimum,table
    db.close()
    before={f.name:sha(f) for f in p.live.iterdir()}
    refused=a.run([helper,'admin','stg-fixture','--data-dir',p.live,'--installation-id','1'*32,'--confirm'],check=False)
    assert refused.returncode!=0 and before=={f.name:sha(f) for f in p.live.iterdir()}
    print('PASS: synthetic STG reset uses real archive/verify/restore, retains protected pre-update copy and commits through existing journal; existing-data fixture overwrite refused')
    a.run([helper,'admin','check','--data-dir',p.live,'--installation-id','1'*32])
    # Exercise the real non-test Go binary: unit tests use environment=test and
    # would otherwise mask a VACUUM snapshot whose journal mode is still DELETE.
    legacy=Path(t)/'legacy-source';shutil.copytree(p.live,legacy)
    (legacy/'installation.json').unlink()
    atomic(legacy/'web-auth.json',{'synthetic_token':'must-not-export'})
    atomic(legacy/'data.json',{'months':[{'id':'month-2040-12','year':2040,'month':12,'income':123,'incomes':[],'expenses':[]}]})
    source_hashes={f.name:sha(f) for f in legacy.iterdir()}
    transfer=Path(t)/'transfer';transfer.mkdir(mode=0o700)
    process=a.run([helper,'admin','legacy-export','--source',legacy,'--backup-dir',transfer,
                              '--installation-id','4'*32,'--confirm-stopped'],check=False)
    assert process.returncode==0,process.stderr.decode()
    exported=json.loads(process.stdout)
    archive=Path(exported['archive'])
    verified=json.loads(a.run([helper,'admin','backup','verify','--archive',archive]).stdout)
    assert verified['installation_id']=='4'*32 and verified['sqlite']['schema_version']==14
    assert 'web-auth.json' not in [f['name'] for f in verified['files']]
    assert source_hashes=={f.name:sha(f) for f in legacy.iterdir()}
    parent=Path(t)/'new-installation';parent.mkdir(mode=0o700);new_live=parent/'live'
    a.run([helper,'admin','backup','restore','--data-dir',new_live,'--backup-dir',transfer,
           '--installation-id','4'*32,'--archive',archive,'--scope','full','--confirm'])
    assert load(new_live/'installation.json')['installation_id']=='4'*32
    assert not (new_live/'web-auth.json').exists()
    for name in ('data.json','categories.json'):assert read(new_live/name)==read(legacy/name)
    a.run([helper,'admin','check','--data-dir',new_live,'--installation-id','4'*32])
    print('PASS: native production legacy-export/new-installation restore, unchanged source, financial JSON and transfer ID preserved, auth sidecar excluded')
    print('PASS: real CLI init, verified backup, candidate restore/migration, live migration, pre-open rollback, retained old data, integrity/schema check; OS/proxy mocked')
