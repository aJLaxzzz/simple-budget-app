"""Native CLI integration on synthetic data. Never SSH, systemctl, Caddy or real PKI.
Run: python3 tests/ops/cli_integration.py /absolute/path/to/isolated/native/budget
"""
import json
import os
from pathlib import Path
import shutil
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
    assert load(p.journal)['phase']=='trial'
    a.recover(dict(request,decision='rollback',operation_id=request['request_id']))
    assert p.release().name=='one'
    a.commit(dict(request,https_verified_revision='a'*40))
    previous=list(p.state.glob('.live.previous-*'));assert previous
    final=json.loads(a.run([helper,'admin','check','--data-dir',p.live,'--installation-id','1'*32]).stdout)
    assert final['schema_version']==14
    assert 'trial' in a.modes and 'closed' in a.modes
    print('PASS: real CLI init, verified backup, candidate restore/migration, live migration, pre-open rollback, retained old data, integrity/schema check; OS/proxy mocked')
