import copy
import hashlib
import importlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import *
import caddy_policy as cp
from remote import Admin,cleanup
from bootstrap import install
import mac

CLIENTS=['/etc/caddy/pki/mac-1.crt','/etc/caddy/pki/phone.crt']
def config():
    policy={'client_authentication':{'mode':'require_and_verify','ca':{'provider':'file','pem_files':[cp.CA]},'verifiers':[{'verifier':'leaf','leaf_certs_loaders':[{'loader':'file','files':CLIENTS}]}]}}
    return {'storage':{'module':'file_system','root':cp.ACME_ROOT},'apps':{'http':{'servers':{'secure':{'listen':[':443'],'protocols':['h1','h2'],'tls_connection_policies':[dict(policy,match={'sni':[IP]}),copy.deepcopy(policy)],'routes':[{'match':[{'host':[IP]}],'handle':[{'handler':'static_response','status_code':200}]},{'handle':[{'handler':'static_response','status_code':404}]}]},'plain':{'listen':[':80'],'routes':[{'handle':[{'handler':'static_response','status_code':404}]}]}}},'tls':{'automation':{'policies':[{'subjects':[IP],'issuers':[{'module':'acme','ca':'https://acme-v02.api.letsencrypt.org/directory','profile':'shortlived','challenges':{'tls-alpn':{'disabled':True}}}]}]}}}}

class PolicyTests(unittest.TestCase):
    def test_every_route_mode_keeps_mtls_and_storage(self):
        original=config()
        for mode in ('closed','trial','open','test-page'):
            c=cp.render(original,CLIENTS,mode,3001)
            self.assertEqual(cp.validate(c,CLIENTS,True)['tls_policies'],2)
            self.assertEqual(c['storage'],original['storage'])
            nodes=list(cp.walk(c['apps']['http']['servers']['secure']['routes']))
            self.assertEqual(sum(n.get('handler')=='reverse_proxy' for n in nodes),int(mode in ('open','trial')))
        self.assertNotIn('admin',original)
    def test_unprotected_fallback_rejected(self):
        c=config();c['apps']['http']['servers']['secure']['tls_connection_policies'][1]={}
        with self.assertRaises(Refused):cp.validate(c,CLIENTS)
    def test_ca_signed_but_not_allowlisted_is_not_enabled(self):
        c=cp.render(config(),[CLIENTS[0]],'open',3001)
        for p in c['apps']['http']['servers']['secure']['tls_connection_policies']:self.assertEqual(cp.leaves(p),{CLIENTS[0]})
    def test_policy_mismatch(self):
        c=config();c['apps']['http']['servers']['secure']['tls_connection_policies'][1]['client_authentication']['verifiers'][0]['leaf_certs_loaders'][0]['files']=[CLIENTS[0]]
        with self.assertRaises(Refused):cp.validate(c,CLIENTS)
    def test_no_acme_or_admin_or_http_fallback_bypass(self):
        for mutate in (lambda c:c['storage'].update(root='/tmp'),lambda c:c['apps']['http']['servers']['plain']['routes'][0]['handle'][0].update(status_code=200),lambda c:c['apps']['tls']['automation']['policies'][0]['issuers'][0].update(module='internal'),lambda c:c['apps']['http']['servers']['secure'].update(protocols=['h1','h2','h3'])):
            c=config();mutate(c)
            with self.assertRaises(Refused):cp.validate(c,CLIENTS)
    def test_tickets_and_admin_explicit(self):
        c=cp.render(config(),CLIENTS,'open',3001);c['admin']={}
        with self.assertRaises(Refused):cp.validate(c,CLIENTS,True)
    def test_caddy_start_refuses_old_allowlist_after_interrupted_revoke(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('production',t);(p.root/'etc/caddy').mkdir(parents=True)
            atomic(p.at('/etc/caddy/simple-budget.json'),cp.render(config(),CLIENTS,'open',3001))
            with patch.object(Admin,'registry',return_value=({},[CLIENTS[0]])):
                with self.assertRaises(Refused):Admin(p).proxy_guard()
            with patch.object(Admin,'registry',return_value=({},CLIENTS)):
                self.assertEqual(Admin(p).proxy_guard()['tls_policies'],2)
    def test_invalid_upstream(self):
        with self.assertRaises(Refused):cp.render(config(),CLIENTS,'open',22)

class PrimitiveTests(unittest.TestCase):
    def test_symlink_hardlink_and_oversize(self):
        with tempfile.TemporaryDirectory() as t:
            p=Path(t);(p/'a').write_bytes(b'hello');(p/'s').symlink_to(p/'a')
            with self.assertRaises(Refused):read(p/'s')
            with self.assertRaises(Refused):read(p/'a',2)
            os.link(p/'a',p/'b')
            with self.assertRaises(Refused):read(p/'a')
    def test_atomic_durable_and_checksum(self):
        with tempfile.TemporaryDirectory() as t:
            p=Path(t);atomic(p/'a',{'one':1});self.assertEqual(load(p/'a'),{'one':1})
            with self.assertRaises(Refused):copy_verified(p/'a',p/'b','0'*64)
            self.assertFalse((p/'b').exists())
    def test_lock_contention(self):
        with tempfile.TemporaryDirectory() as t:
            with lock(Path(t)/'lock'):
                with self.assertRaises(Refused):
                    with lock(Path(t)/'lock'):pass
    def test_space_and_inode_guard(self):
        class Stat: f_blocks=1000;f_frsize=4096;f_bavail=999;f_favail=1000
        with patch('os.statvfs',return_value=Stat()):
            with self.assertRaises(Refused):space('/')
        Stat.f_blocks=10**7;Stat.f_bavail=10**7;Stat.f_favail=0
        with patch('os.statvfs',return_value=Stat()):
            with self.assertRaises(Refused):space('/')
    def test_no_network_without_explicit_environment(self):
        with patch.dict(os.environ,{'ENV':''}),patch('subprocess.check_output') as call:
            with self.assertRaises(Refused):mac.Transport()
            call.assert_not_called()
    def test_ssh_wrong_target_or_insecure_hostkey(self):
        for raw in ('hostname 127.0.0.1\nuser budgetadmin\n','hostname '+IP+'\nuser root\n','hostname '+IP+'\nuser budgetadmin\nstricthostkeychecking false\n'):
            with patch.dict(os.environ,{'ENV':'production'}),patch('subprocess.check_output',return_value=raw):
                with self.assertRaises(Refused):mac.Transport()
    def test_ssh_disconnect_is_not_success(self):
        with patch.dict(os.environ,{'ENV':'production'}),patch('subprocess.check_output',return_value='hostname '+IP+'\nuser budgetadmin\nstricthostkeychecking ask\n'):
            t=mac.Transport();self.assertIn('StrictHostKeyChecking=yes',t.ssh)
            with patch('subprocess.run',side_effect=subprocess.CalledProcessError(255,['ssh'])):
                with self.assertRaises(subprocess.CalledProcessError):t.command(['true'])
    def test_clean_release_refuses_dirty_tree(self):
        with patch('subprocess.check_output',return_value=b' M server.go\n'):
            with self.assertRaises(Refused):mac.clean_tree()

class FakeAdmin(Admin):
    def __init__(self,p):
        super().__init__(p);self.calls=[];self.running=True;self.fail=None
        self.old=p.opt/'releases'/'old';self.new=p.opt/'releases'/'new'
        self.version={'revision':'b'*40,'schema_version':14,'modified':False}
        self.run=self.execute
    def hit(self,name):
        self.calls.append(name)
        if self.fail==name:raise Refused('injected '+name)
    def execute(self,args,**kw):self.hit('cli:'+('migrate' if 'migrate' in args else str(args[1])));return subprocess.CompletedProcess(args,0,b'{}',b'')
    def proxy(self,mode):self.hit('proxy:'+mode)
    def system(self,*args,**kw):self.hit('system:'+args[0]);return subprocess.CompletedProcess(args,0,b'',b'')
    def controller(self,action,ident):self.hit('controller:'+action);self.running=action=='start'
    def install_release(self,src):self.hit('release');return self.new,self.version
    def backup(self,*args):self.hit('backup');return {'manifest':{'backup_id':'20261125T000000Z-'+'a'*16}}
    def archive(self,ident):return self.p.backups/ident
    def preflight_restore(self,*args):self.hit('candidate')
    def own_live(self):self.hit('ownership')
    def ready(self,*args):self.hit('ready');return self.version
    def schedule(self):self.hit('schedule')
    def prune_releases(self):self.hit('prune')
    def backup_command(self,action,*args,**kw):self.hit('backup:'+action);return {}
    def manifest(self,path,**kw):return dict(self.version,revision=('a' if path==self.old else 'b')*40)

class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.p=Paths('acceptance',self.tmp.name)
        for d in (self.p.opt/'releases'/'old',self.p.opt/'releases'/'new',self.p.state,self.p.etc,self.p.backups,self.p.global_state):d.mkdir(parents=True,exist_ok=True)
        atomic(self.p.etc/'identity.json',{'instance':'acceptance','installation_id':'1'*32});pointer(self.p.opt/'current',self.p.opt/'releases'/'old')
        self.a=FakeAdmin(self.p);self.req={'request_id':'2'*32,'installation_id':'1'*32}
    def tearDown(self):self.tmp.cleanup()
    def test_initialize_password_boundaries_and_no_secret_leak(self):
        cases=[('p'*11,False),('p'*12,True),('p'*16,True),('p'*40,True),
               ('я'*11,False),('я'*12,True),('p'*4096,True),('p'*4097,False),
               ('я'*2049,False),('p'*12+'\n',False),('p'*12+'\0',False)]
        for password,valid in cases:
            with self.subTest(length=len(password),valid=valid),tempfile.TemporaryDirectory() as t:
                p=Paths('acceptance',t);(p.etc/'credentials').mkdir(parents=True)
                a=FakeAdmin(p)
                with patch('getpass.getpass',return_value=password) as prompt,patch.object(a,'save_journal') as journal,patch.object(a,'finish_initialization',return_value={}) as finish:
                    if valid:
                        a.initialize(Path(t),dict(self.req,confirm=True))
                        self.assertEqual(read(p.etc/'credentials/app-password'),(password+'\n').encode())
                        self.assertEqual((p.etc/'credentials/app-password').stat().st_mode&0o777,0o600)
                        journal.assert_called_once();finish.assert_called_once()
                    else:
                        with self.assertRaises(Refused) as error:a.initialize(Path(t),dict(self.req,confirm=True))
                        self.assertNotIn(password,str(error.exception))
                        self.assertFalse((p.etc/'credentials/app-password').exists())
                        journal.assert_not_called();finish.assert_not_called()
                    self.assertIn('минимум 12',prompt.call_args_list[0].args[0])
                    self.assertIn('рекомендуется 16',prompt.call_args_list[0].args[0])
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);(p.etc/'credentials').mkdir(parents=True)
            with patch('getpass.getpass',side_effect=['p'*12,'q'*12]):
                with self.assertRaises(Refused):FakeAdmin(p).initialize(Path(t),dict(self.req,confirm=True))
            self.assertFalse((p.etc/'credentials/app-password').exists())
    def test_prepare_stays_closed_until_mac_acceptance(self):
        out=self.a.prepare(Path(self.tmp.name),self.req)
        self.assertEqual(out['result'],'awaiting_https_acceptance');self.assertEqual(load(self.p.journal)['phase'],'trial')
        self.assertNotIn('proxy:open',self.a.calls)
        self.assertLess(self.a.calls.index('controller:stop'),self.a.calls.index('backup'))
        self.assertLess(self.a.calls.index('candidate'),self.a.calls.index('cli:migrate'))
        self.a.commit(dict(self.req,https_verified_revision='b'*40));self.assertFalse(self.p.journal.exists());self.assertIn('proxy:open',self.a.calls)
    def test_every_prepare_failure_preserves_journal(self):
        for fail in ('proxy:closed','backup','candidate','cli:migrate','ownership','controller:start','proxy:trial'):
            with self.subTest(fail=fail):
                if self.p.journal.exists():self.p.journal.unlink()
                pointer(self.p.opt/'current',self.a.old);self.a.calls=[];self.a.fail=fail
                with self.assertRaises(Refused):self.a.prepare(Path(self.tmp.name),self.req)
                self.assertTrue(self.p.journal.exists());self.assertNotIn('proxy:open',self.a.calls)
    def test_wrong_installation_and_replay_refused(self):
        with self.assertRaises(Refused):self.a.prepare(Path(self.tmp.name),dict(self.req,installation_id='3'*32))
        self.a.prepare(Path(self.tmp.name),self.req)
        with self.assertRaises(Refused):self.a.prepare(Path(self.tmp.name),self.req)
    def test_commit_without_https_proof_refused(self):
        self.a.prepare(Path(self.tmp.name),self.req)
        with self.assertRaises(Refused):self.a.commit(self.req)
    def test_interrupted_symlink_publication_is_recoverable(self):
        (self.p.opt/'.current-next').symlink_to(self.a.new)
        pointer(self.p.opt/'current',self.a.old)
        self.assertEqual(self.p.release(),self.a.old)
        self.assertFalse((self.p.opt/'.current-next').exists())
    def test_opening_boundary_never_auto_restores_data(self):
        self.a.prepare(Path(self.tmp.name),self.req);j=load(self.p.journal);self.a.save_journal(j,'opening')
        with self.assertRaises(Refused):self.a.recover(dict(self.req,confirm=True,decision='rollback'))
        self.assertNotIn('backup:restore',self.a.calls)
    def test_explicit_preopen_recovery_restores_matching_release(self):
        self.a.prepare(Path(self.tmp.name),self.req)
        r=self.a.recover(dict(self.req,confirm=True,decision='rollback'))
        self.assertEqual(r['version']['revision'],'a'*40);self.assertEqual(self.p.release(),self.a.old)
        self.assertEqual(load(self.p.journal)['phase'],'rollback-trial')
    def test_lost_commit_reply_reconciles_exact_operation(self):
        self.a.prepare(Path(self.tmp.name),self.req)
        self.a.commit(dict(self.req,https_verified_revision='b'*40))
        r=self.a.recover(dict(self.req,confirm=True,decision='finish',operation_id=self.req['request_id']))
        self.assertEqual(r['result'],'already_committed')
        self.assertEqual(self.a.recover(dict(self.req,confirm=True,decision='finish',operation_id='f'*32))['result'],'not_started')
        with self.assertRaises(Refused):self.a.recover(dict(self.req,confirm=True,decision='finish',operation_id='invalid'))
    def test_ssh_loss_before_initialization_does_not_strand_pending_token(self):
        (self.p.etc/'identity.json').unlink()
        r=self.a.recover(dict(self.req,confirm=True,decision='finish',operation_id=self.req['request_id']))
        self.assertEqual(r['result'],'not_started');self.assertFalse(self.p.live.exists())
    def test_commit_timer_failure_keeps_recoverable_boundary(self):
        self.a.prepare(Path(self.tmp.name),self.req);self.a.fail='schedule'
        with self.assertRaises(Refused):self.a.commit(dict(self.req,https_verified_revision='b'*40))
        self.assertEqual(load(self.p.journal)['phase'],'committed')
        self.a.fail=None;self.a.recover(dict(self.req,confirm=True,decision='finish'))
        self.assertFalse(self.p.journal.exists())
        self.assertNotIn('backup:restore',self.a.calls)
    def test_partial_initialization_preserves_data_and_resumes(self):
        (self.p.opt/'current').unlink();self.p.live.mkdir();(self.p.live/'planner.db').write_bytes(b'partial-preserved')
        j=dict(self.req,id=self.req['request_id'],kind='initialize',phase='initializing',new=str(self.a.new),old=str(self.a.new),version=self.a.version,identity={'instance':'acceptance','installation_id':'1'*32},configuration={'BUDGET_ENV':'production'})
        self.a.save_journal(j,'initializing');r=self.a.recover(dict(self.req,confirm=True,decision='finish'))
        self.assertEqual(r['result'],'awaiting_https_acceptance')
        preserved=list(self.p.state.glob('.live.initialization-failed-*'))
        self.assertEqual(len(preserved),1);self.assertEqual((preserved[0]/'planner.db').read_bytes(),b'partial-preserved')
        self.assertEqual(load(self.p.journal)['phase'],'trial')
    def test_rollback_wrong_identity_refused_before_mutation(self):
        with self.assertRaises(Refused):self.a.rollback(dict(self.req,confirm=True,installation_id='f'*32))
        self.assertEqual(self.a.calls,[])
    def test_startup_gate_after_restart(self):
        atomic(self.p.journal,dict(self.req,phase='migrating'))
        with self.assertRaises(Refused):Admin(self.p).startup_guard()
        atomic(self.p.journal,dict(self.req,phase='trial'));Admin(self.p).startup_guard()
        atomic(self.p.state/'.live.restore.json',{})
        with self.assertRaises(Refused):Admin(self.p).startup_guard()
    def test_controller_does_not_call_itself_or_acquire_ops_lock(self):
        a=Admin(self.p,lambda args,**kw:subprocess.CompletedProcess(args,0,b'LoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0\n',b''))
        a.controller('stopped','1'*32)
        with self.assertRaises(Refused):a.controller('status','1'*32)
        with self.assertRaises(Refused):a.controller('start','9'*32)
    def test_release_hash_and_dirty_rejected(self):
        d=self.p.opt/'releases'/'sample';d.mkdir();(d/'budget').write_bytes(b'\x7fELF\x02fixture')
        m={'application':'simple-budget-app','target':'linux/amd64','embedded_frontend':True,'revision':'a'*40,'schema_version':14,'modified':False,'files':[{'name':'budget','size':12,'sha256':sha(d/'budget')}]}
        m['files'][0]['size']=(d/'budget').stat().st_size;atomic(d/'release.json',m)
        Admin(self.p).manifest(d);m['modified']=True;atomic(d/'release.json',m)
        with self.assertRaises(Refused):Admin(self.p).manifest(d)
        m['modified']=False;m['files'][0]['sha256']='0'*64;atomic(d/'release.json',m)
        with self.assertRaises(Refused):Admin(self.p).manifest(d)
    def test_unit_hardening_paths_and_timer(self):
        root=Path(__file__).resolve().parents[2]
        unit=(root/'deploy/systemd/simple-budget@.service').read_text()
        for value in ('User=budget','LoadCredential=app-password:','ReadWritePaths=/var/lib/simple-budget-%i/live','NoNewPrivileges=true','MemoryMax=448M','InaccessiblePaths=/etc/caddy /var/lib/caddy'):self.assertIn(value,unit)
        timer=(root/'deploy/systemd/simple-budget-backup@.timer').read_text();self.assertIn('03:30:00 Europe/Moscow',timer);self.assertIn('Persistent=true',timer)

if __name__=='__main__':unittest.main()

class InstallerTests(unittest.TestCase):
    def test_infrastructure_is_idempotent_and_never_starts_application(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('production',t);root=p.root
            for d in ('etc/caddy/pki','proc','var','usr/local/sbin'): (root/d).mkdir(parents=True,exist_ok=True)
            (root/'usr/lib').mkdir(exist_ok=True)
            (root/'usr/lib/os-release').write_text('ID=ubuntu\nVERSION_ID="24.04"\n')
            (root/'etc/os-release').symlink_to('../usr/lib/os-release')
            (root/'proc/meminfo').write_text('MemTotal: 884736 kB\n')
            (root/'etc/caddy/Caddyfile').write_text('synthetic audited source')
            pem=b'-----BEGIN CERTIFICATE-----\nZg==\n-----END CERTIFICATE-----\n'
            for name in ('ca','mac-1','phone'):(root/('etc/caddy/pki/'+name+'.crt')).write_bytes(pem)
            bundle=root/'bundle';(bundle/'ops').mkdir(parents=True);(bundle/'systemd').mkdir()
            repo=Path(__file__).resolve().parents[2];manifest={}
            for name in ('common.py','remote.py','bootstrap.py','caddy_policy.py','admin_session.py'):
                data=(repo/'scripts/ops'/name).read_bytes();(bundle/'ops'/name).write_bytes(data);manifest['ops/'+name]=hashlib.sha256(data).hexdigest()
            for file in (repo/'deploy/systemd').iterdir():
                data=file.read_bytes();(bundle/'systemd'/file.name).write_bytes(data);manifest['systemd/'+file.name]=hashlib.sha256(data).hexdigest()
            atomic(bundle/'bundle.json',manifest);calls=[]
            def run(args,**kw):
                args=list(map(str,args));calls.append(args);data=b''
                if 'enable' in args:
                    with lock(p.global_state/'operations.lock'):pass
                if args[0]=='/usr/bin/caddy':data=b'v2.11.9 test' if args[1]=='version' else json.dumps(config()).encode()
                if args[0]=='/usr/bin/openssl':data=b'serial=01\nissuer=CN = Synthetic\nnotAfter=Nov 24 00:00:00 2027 GMT\n'
                return subprocess.CompletedProcess(args,0,data,b'')
            # Reproduce the original failure after admin directory/lock creation.
            with patch('bootstrap.read_os_release',side_effect=lambda r,**kw:read(Path(r)/'etc/os-release')):
                with self.assertRaisesRegex(Refused,'Regular non-linked'):install(bundle,run,root)
            self.assertTrue((p.global_state/'operations.lock').exists());self.assertFalse(p.opt.exists())
            # Also recover a later partial installation with already written units.
            def interrupted(args,**kw):
                if 'daemon-reload' in args:raise Refused('injected interruption after units')
                return run(args,**kw)
            with self.assertRaisesRegex(Refused,'injected interruption'):install(bundle,interrupted,root)
            self.assertTrue((root/'etc/systemd/system/simple-budget.service').exists())
            first=install(bundle,run,root);second=install(bundle,run,root)
            self.assertEqual(first,second);self.assertFalse(p.live.exists());self.assertFalse(p.config.exists())
            self.assertEqual((root/'etc/caddy/Caddyfile').read_text(),'synthetic audited source')
            self.assertFalse(any('restart' in c or 'simple-budget.service' in c and 'start' in c for c in calls))
            unit=(root/'etc/systemd/system/simple-budget.service').read_text()
            self.assertIn('BUDGET', (root/'usr/local/lib/simple-budget/ops/remote.py').read_text())
            self.assertIn('/var/lib/simple-budget/live',unit);self.assertNotIn('simple-budget-production',unit)
            self.assertNotIn('NOPASSWD',''.join(x.read_text() for x in (root/'etc/systemd/system').iterdir() if x.is_file()))
    def test_client_revocation_records_first_and_failed_restart_stays_closed(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.global_state.mkdir(parents=True)
            atomic(p.global_state/'clients.json',{'format':1,'clients':{'mac-1':{'state':'allowed','fingerprint':'1'*64},'phone':{'state':'allowed','fingerprint':'2'*64}}})
            calls=[]
            def run(args,**kw):calls.append(list(args));return subprocess.CompletedProcess(args,0,b'',b'')
            with patch.object(Admin,'proxy',side_effect=Refused('invalid new config')):
                with self.assertRaises(Refused):Admin(p,run).client_change(None,{'device':'phone','fingerprint':'2'*64,'confirm':True},True)
            self.assertEqual(load(p.global_state/'clients.json')['clients']['phone']['state'],'revoked')
            self.assertEqual(sum('stop' in c and 'caddy.service' in c for c in calls),2)
    def test_revocation_intent_blocks_reboot_before_registry_write(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('production',t);p.global_state.mkdir(parents=True);p.at('/etc/caddy').mkdir(parents=True)
            atomic(p.at('/etc/caddy/simple-budget.json'),cp.render(config(),CLIENTS,'open',3001))
            reg={'clients':{'phone':{'state':'allowed','fingerprint':'2'*64}}}
            atomic(p.global_state/'revocation-pending.json',{'device':'phone','fingerprint':'2'*64})
            with patch.object(Admin,'registry',return_value=(reg,CLIENTS)):
                with self.assertRaises(Refused):Admin(p).proxy_guard()
    def test_retry_interrupted_revocation_keeps_other_device(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.global_state.mkdir(parents=True)
            reg={'format':1,'clients':{'mac-1':{'state':'allowed','fingerprint':'1'*64},'phone':{'state':'revoked','fingerprint':'2'*64}}}
            atomic(p.global_state/'clients.json',reg);atomic(p.global_state/'revocation-pending.json',{'device':'phone','fingerprint':'2'*64})
            def run(args,**kw):return subprocess.CompletedProcess(args,0,b'',b'')
            with patch.object(Admin,'proxy'):
                r=Admin(p,run).client_change(None,{'device':'phone','fingerprint':'2'*64,'confirm':True},True)
            self.assertEqual(r['state'],'revoked');self.assertFalse((p.global_state/'revocation-pending.json').exists())
            self.assertEqual(load(p.global_state/'clients.json')['clients']['mac-1']['state'],'allowed')
    def test_last_device_and_wrong_fingerprint_refused(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.global_state.mkdir(parents=True)
            atomic(p.global_state/'clients.json',{'format':1,'clients':{'mac-1':{'state':'allowed','fingerprint':'1'*64}}})
            a=Admin(p,lambda *args,**kw:self.fail('No system command expected'))
            for fingerprint in ('1'*64,'2'*64):
                with self.assertRaises(Refused):a.client_change(None,{'device':'mac-1','fingerprint':fingerprint,'confirm':True},True)

class LocalDefaultsTests(unittest.TestCase):
    def test_exported_empty_make_variables_keep_safe_defaults(self):
        with patch.dict(os.environ,{'INSTANCE':'','DEVICE':''}),patch.object(sys,'argv',['mac.py','pki-backup']),patch('pki.execute') as execute:
            mac.main()
        execute.assert_called_once_with('pki-backup','mac-1',None)
    def test_default_make_does_not_connect_or_deploy(self):
        result=subprocess.run(['make','-s'],cwd=Path(__file__).resolve().parents[2],capture_output=True,text=True,check=True)
        self.assertIn('No network operation runs by default',result.stdout)

class ScheduleTests(unittest.TestCase):
    def test_disabled_timer_never_claimed_enabled(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.etc.mkdir(parents=True);p.backups.mkdir(parents=True)
            atomic(p.config,b'');atomic(p.backups/'repository.json',{})
            calls=[]
            def run(args,**kw):
                calls.append(list(map(str,args)))
                return subprocess.CompletedProcess(args,1 if 'is-active' in args or 'is-enabled' in args else 0,b'',b'')
            with patch.object(Admin,'backup_command') as command:Admin(p,run).schedule()
            command.assert_called_once_with('schedule')
    def test_next_run_comes_from_systemd(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.etc.mkdir(parents=True);p.backups.mkdir(parents=True)
            atomic(p.config,b'');atomic(p.backups/'repository.json',{})
            future='2037-11-25T00:30:00+00:00'
            def run(args,**kw):return subprocess.CompletedProcess(args,0,(future+'\n').encode(),b'')
            with patch.object(Admin,'backup_command') as command:Admin(p,run).schedule()
            command.assert_called_once_with('schedule','--enabled','--next-at',future)

class ReadOnlyPreflightTests(unittest.TestCase):
    def test_baseline_never_runs_mutating_commands_or_collects_private_keys(self):
        import preflight
        with tempfile.TemporaryDirectory() as t:
            p=Paths('production',t);p.at('/etc/caddy').mkdir(parents=True);p.at('/etc/caddy/Caddyfile').write_text('synthetic safe config')
            calls=[]
            def run(args,**kw):
                args=list(map(str,args));calls.append(args);data=b''
                if args[:2]==['/usr/bin/caddy','version']:data=b'v2.11.9 test'
                elif args[:2]==['/usr/bin/caddy','adapt']:data=json.dumps(config()).encode()
                elif args[:2]==['/usr/bin/openssl','s_client']:data=b'-----BEGIN CERTIFICATE-----\nZg==\n-----END CERTIFICATE-----\n'
                return subprocess.CompletedProcess(args,0,data,b'')
            with patch.object(Admin,'cert',return_value={'serial':'test','details':'public-only','remaining_hours':100}):
                result=preflight.collect(run,t)
            self.assertFalse(result['changes_applied']);self.assertEqual(result['tls_policies']['tls_policies'],2)
            self.assertEqual(result['pending_paths'],[])
            self.assertFalse(any(set(c)&{'stop','start','restart','reload','enable','disable','renew','delete'} for c in calls))
            self.assertFalse(any('.key' in arg or '.p12' in arg for c in calls for arg in c))

class FirstInstallationUnitTests(unittest.TestCase):
    def test_verify_defers_only_missing_app_executable_without_changing_units(self):
        from bootstrap import verify_units_before_initialization
        with tempfile.TemporaryDirectory() as t:
            root=Path(t);units=root/'units';units.mkdir()
            originals={}
            for instance in ('production','acceptance'):
                p=Paths(instance,t)
                originals[p.unit]='[Service]\nExecStart=/opt/'+p.name+'/current/budget\nNoNewPrivileges=true\n'
                (units/p.unit).write_text(originals[p.unit])
            calls=[]
            def run(args,**kwargs):
                calls.append(args)
                for name in args[2:4]:
                    text=Path(name).read_text();self.assertIn('ExecStart=/usr/bin/true',text);self.assertIn('NoNewPrivileges=true',text)
                return subprocess.CompletedProcess(args,0,b'',b'')
            verify_units_before_initialization(units,run,t)
            self.assertEqual(len(calls),1)
            for name,text in originals.items():self.assertEqual((units/name).read_text(),text)
    def test_controller_requires_real_unit_verification_before_start(self):
        with tempfile.TemporaryDirectory() as t:
            p=Paths('acceptance',t);p.etc.mkdir(parents=True);p.state.mkdir(parents=True)
            atomic(p.etc/'identity.json',{'installation_id':'1'*32,'instance':'acceptance'})
            calls=[]
            def run(args,**kwargs):
                calls.append(list(map(str,args)))
                raise Refused('missing actual executable')
            with self.assertRaises(Refused):Admin(p,run).controller('start','1'*32)
            self.assertEqual(calls,[['/usr/bin/systemd-analyze','verify',str(p.at('/etc/systemd/system')/p.unit)]])

class OSReleaseSecurityTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.root=Path(self.tmp.name).resolve()
        (self.root/'etc').mkdir();(self.root/'usr/lib').mkdir(parents=True)
        self.target=self.root/'usr/lib/os-release';self.target.write_bytes(b'ID=ubuntu\nVERSION_ID="24.04"\n');self.target.chmod(0o644)
        self.source=self.root/'etc/os-release'
    def tearDown(self):self.tmp.cleanup()
    def value(self):return read_os_release(self.root,owner=os.getuid())
    def test_distribution_link_and_regular_file(self):
        for target in ('../usr/lib/os-release','/usr/lib/os-release'):
            self.source.symlink_to(target);self.assertEqual(self.value(),self.target.read_bytes())
            with self.assertRaises(Refused):read(self.source)
            self.source.unlink()
        self.source.write_bytes(self.target.read_bytes());self.assertEqual(self.value(),self.target.read_bytes())
    def test_arbitrary_link_and_link_chain_rejected(self):
        self.source.symlink_to('../usr/lib/other')
        with self.assertRaisesRegex(Refused,'symlink target'):self.value()
        self.source.unlink();self.source.symlink_to('../usr/lib/os-release')
        self.target.unlink();self.target.symlink_to('other')
        with self.assertRaisesRegex(Refused,'Regular non-linked'):self.value()
    def test_hardlinks_rejected_for_direct_file_and_target(self):
        self.source.symlink_to('../usr/lib/os-release');os.link(self.target,self.root/'extra')
        with self.assertRaises(Refused):self.value()
        self.source.unlink();os.link(self.target,self.source)
        with self.assertRaises(Refused):self.value()
    def test_unsafe_permissions_and_ownership_rejected(self):
        self.source.symlink_to('../usr/lib/os-release');self.target.chmod(0o666)
        with self.assertRaisesRegex(Refused,'writable'):self.value()
        self.target.chmod(0o644);(self.root/'usr/lib').chmod(0o777)
        with self.assertRaisesRegex(Refused,'directory'):self.value()
        (self.root/'usr/lib').chmod(0o755)
        with self.assertRaisesRegex(Refused,'directory'):read_os_release(self.root,owner=os.getuid()+1)
    def test_symlink_directory_rejected(self):
        self.source.symlink_to('../usr/lib/os-release');(self.root/'usr').rename(self.root/'alternate');(self.root/'usr').symlink_to('alternate')
        with self.assertRaisesRegex(Refused,'directory'):self.value()

class InitializationRepairTests(unittest.TestCase):
    def setUp(self):
        import repair_initialization
        self.repair_module=repair_initialization
        self.tmp=tempfile.TemporaryDirectory();self.p=Paths('acceptance',self.tmp.name)
        for d in (self.p.etc/'credentials',self.p.live,self.p.global_state,self.p.backups,self.p.opt/'releases'/'old',self.p.opt/'releases'/'new'):d.mkdir(parents=True,exist_ok=True)
        atomic(self.p.etc/'identity.json',{'instance':'acceptance','installation_id':'1'*32,'synthetic':True})
        atomic(self.p.live/'planner.db',b'synthetic unchanged data')
        atomic(self.p.etc/'credentials/app-password',b'Test-pass-12\n')
        pointer(self.p.opt/'current',self.p.opt/'releases'/'old')
        self.a=FakeAdmin(self.p);self.a.version['schema_version']=14
        self.a.service_state=lambda:'stopped'
        self.request={'installation_id':'1'*32,'operation_id':'2'*32,'request_id':'3'*32,'confirm':True}
        self.j={'kind':'initialize','phase':'trial','installation_id':'1'*32,'id':'2'*32,'new':str(self.a.old),'old':str(self.a.old),'version':self.a.version}
        atomic(self.p.journal,self.j)
        self.original=(read(self.p.live/'planner.db'),read(self.p.etc/'credentials/app-password'))
    def tearDown(self):self.tmp.cleanup()
    def repair(self):return self.repair_module.repair(self.a,Path(self.tmp.name),self.request)
    def test_repair_probes_then_backs_up_then_switches_without_data_mutation(self):
        result=self.repair()
        self.assertEqual(result['result'],'initialization_release_repaired')
        self.assertEqual(self.p.release(),self.a.new)
        self.assertEqual(load(self.p.journal)['phase'],'trial')
        self.assertEqual(load(self.p.journal)['id'],'2'*32)
        self.assertEqual(load(self.p.journal)['repair_from'],str(self.a.old))
        self.assertLess(self.a.calls.index('cli:--unit=budget-credential-check-'+self.request['request_id'][:12]),self.a.calls.index('backup'))
        self.assertEqual(self.original,(read(self.p.live/'planner.db'),read(self.p.etc/'credentials/app-password')))
        self.assertFalse(any(x.startswith('proxy:') or x.startswith('controller:') for x in self.a.calls))
    def test_production_or_wrong_journal_or_open_proxy_refused(self):
        for key,value in (('kind','restore'),('phase','opening'),('installation_id','4'*32),('id','5'*32)):
            with self.subTest(key=key):
                atomic(self.p.journal,dict(self.j,**{key:value}))
                with self.assertRaises(Refused):self.repair()
        atomic(self.p.journal,self.j)
        self.a.service_state=lambda:'running'
        with self.assertRaises(Refused):self.repair()
        self.a.service_state=lambda:'stopped'
        self.request['confirm']=False
        with self.assertRaises(Refused):self.repair()
        self.request['confirm']=True
        atomic(self.p.global_state/'proxy.json',{'mode':'open'})
        with self.assertRaises(Refused):self.repair()
        (self.p.global_state/'proxy.json').unlink()
        self.p.instance='production'
        with self.assertRaises(Refused):self.repair()
    def test_schema_change_or_failed_probe_never_switches(self):
        with patch.object(self.a,'manifest',return_value=dict(self.a.version,schema_version=15)):
            with self.assertRaises(Refused):self.repair()
        self.a.fail='cli:--unit=budget-credential-check-'+self.request['request_id'][:12]
        with self.assertRaises(Refused):self.repair()
        self.assertEqual(self.p.release(),self.a.old)
        self.assertEqual(load(self.p.journal),self.j)
        self.assertNotIn('backup',self.a.calls)
    def test_interruption_between_journal_and_pointer_can_be_retried(self):
        with patch.object(self.repair_module,'pointer',side_effect=Refused('interrupted')):
            with self.assertRaises(Refused):self.repair()
        self.assertEqual(load(self.p.journal)['phase'],'repairing-initialization')
        self.assertEqual(self.p.release(),self.a.old)
        self.repair()
        self.assertEqual(self.p.release(),self.a.new)
        self.assertEqual(self.a.calls.count('backup'),1)
        self.assertEqual(load(self.p.journal)['phase'],'trial')

class RepairMacContractTests(unittest.TestCase):
    def test_repair_keeps_original_pending_identity_until_recover(self):
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'acceptance.json';pending=root/'pending-acceptance.json'
            atomic(state,{'instance':'acceptance','installation_id':'1'*32})
            original={'action':'initialize','instance':'acceptance','installation_id':'1'*32,'request_id':'2'*32};atomic(pending,original)
            transport=Mock();transport.request.side_effect=[({'result':'initialization_release_repaired'},'3'*32),({'result':'removed'},'3'*32)]
            with patch.object(mac,'LOCAL',root),patch.object(mac,'local_state',return_value=state),patch.object(mac,'Transport',return_value=transport),patch.object(mac,'confirm'),patch.object(mac,'release',return_value=(root,{})),patch('sys.argv',['mac.py','repair-initialization','--instance','acceptance']),patch('builtins.print'):
                mac.main()
            call=transport.request.call_args_list[0]
            self.assertEqual(call.args[0],'repair-initialization')
            self.assertEqual(call.args[2]['operation_id'],'2'*32)
            self.assertEqual(call.args[2]['installation_id'],'1'*32)
            self.assertIn('repair.py',call.args[3])
            self.assertEqual(load(pending),original)
            self.assertEqual(transport.request.call_args_list[1].args[0],'clean-incoming')

class DownloadAcceptanceTests(unittest.TestCase):
    @staticmethod
    def response(cache_fields=('no-store',),status=200,body=b'synthetic',attachment=True):
        import io,http.client
        headers=[('Cache-Control',v) for v in cache_fields]
        if attachment:headers.append(('Content-Disposition','attachment; filename=test.tar.gz'))
        headers.append(('Content-Length',str(len(body))))
        raw=('HTTP/1.1 '+str(status)+' Test\r\n'+''.join(k+': '+v+'\r\n' for k,v in headers)+'\r\n').encode()+body
        class Sock:
            def makefile(self,*args):return io.BytesIO(raw)
        r=http.client.HTTPResponse(Sock());r.begin();return r
    def test_real_httpresponse_combines_caddy_upstream_headers(self):
        for fields in (('no-store',),('no-store','no-store'),('NO-STORE, no-store',)):
            with self.subTest(fields=fields):mac.require_download_response(self.response(fields))
        duplicate=self.response(('no-store','no-store'))
        self.assertEqual(duplicate.getheader('Cache-Control'),'no-store, no-store')
    def test_weaker_missing_cache_and_http_errors_are_not_accepted(self):
        for fields in ((),('public',),('no-cache',),('no-store, public',),('no-store,',),('max-age=60',)):
            with self.subTest(fields=fields):
                with self.assertRaises(Refused):mac.require_download_response(self.response(fields))
        for status in (401,403,404,503):
            with self.assertRaisesRegex(Refused,'HTTP '+str(status)):mac.require_download_response(self.response(status=status))
        with self.assertRaises(Refused):mac.require_download_response(self.response(attachment=False))
    def test_verified_download_and_identity_before_publication(self):
        from unittest.mock import Mock
        ident='20261125T111716Z-6a2003d4c4009d94';installation='1'*32
        for mode in ('ok','wrong-installation','wrong-backup','verify-error','http-error'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as t:
                conn=Mock();conn.getresponse.return_value=self.response(('no-store','no-store'),status=503 if mode=='http-error' else 200)
                metadata={'installation_id':'2'*32 if mode=='wrong-installation' else installation,'backup_id':ident if mode!='wrong-backup' else '20261125T111716Z-'+'a'*16}
                check=Mock(return_value=json.dumps(metadata).encode())
                if mode=='verify-error':check.side_effect=subprocess.CalledProcessError(1,['verify'])
                with patch.object(mac,'LOCAL',Path(t)),patch.object(mac.http.client,'HTTPSConnection',return_value=conn),patch.object(mac,'local_binary',return_value=Path('/synthetic/helper')),patch('subprocess.check_output',check),patch('builtins.print'):
                    if mode=='ok':
                        dest=mac.download_verified_backup(None,'Basic synthetic',ident,installation)
                        self.assertEqual(dest.read_bytes(),b'synthetic')
                        self.assertEqual(dest.stat().st_mode&0o777,0o600)
                        self.assertEqual(dest.stat().st_nlink,1)
                        self.assertFalse(list(Path(t).glob('*.partial')))
                        self.assertEqual(load(Path(t)/('download-'+ident+'.json'))['backup_id'],ident)
                        with self.assertRaises(Refused):mac.download_verified_backup(None,'Basic synthetic',ident,installation)
                    else:
                        with self.assertRaises((Refused,subprocess.CalledProcessError)):mac.download_verified_backup(None,'Basic synthetic',ident,installation)
                        self.assertFalse(list(Path(t).iterdir()))
                conn.close.assert_called_once()

class HTTPSReadinessDiagnosticsTests(unittest.TestCase):
    def test_http_status_is_explained_without_exposing_secret_or_body(self):
        from unittest.mock import Mock
        expected={'revision':'a'*40,'schema_version':14,'modified':False}
        for code,hint in ((401,'application authentication rejected'),(403,'Host/Origin'),(429,'rate limited'),(502,'proxy'),(503,'maintenance'),(404,'unexpected HTTP')):
            with self.subTest(code=code):
                connection=Mock();response=connection.getresponse.return_value;response.status=code
                response.read.return_value=b'private response body'
                with patch.object(mac,'app_connection',return_value=(None,'synthetic-secret-not-for-logs')),patch.object(mac.http.client,'HTTPSConnection',return_value=connection),patch.object(mac.getpass,'getpass',return_value='synthetic-wrong') as prompt,patch('builtins.print'):
                    with self.assertRaises(Refused) as error:mac.https_accept(expected,'mac-1')
                message=str(error.exception)
                self.assertIn('HTTP '+str(code),message);self.assertIn(hint,message)
                self.assertNotIn('synthetic-secret-not-for-logs',message);self.assertNotIn('private response body',message)
                response.read.assert_not_called();self.assertEqual(connection.close.call_count,3 if code==401 else 1)
                self.assertEqual(prompt.call_count,2 if code==401 else 0)
    def test_success_requires_matching_clean_release(self):
        from unittest.mock import Mock
        expected={'revision':'a'*40,'schema_version':14,'modified':False}
        for payload,valid in ((expected,True),(dict(expected,revision='b'*40),False),(dict(expected,schema_version=15),False),(dict(expected,modified=True),False)):
            connection=Mock();response=connection.getresponse.return_value;response.status=200;response.read.return_value=json.dumps(payload).encode()
            with patch.object(mac,'app_connection',return_value=(None,'synthetic-secret')),patch.object(mac.http.client,'HTTPSConnection',return_value=connection):
                if valid:self.assertEqual(mac.https_accept(expected,'mac-1'),'a'*40)
                else:
                    with self.assertRaises(Refused):mac.https_accept(expected,'mac-1')
            connection.close.assert_called_once()

    def test_typo_reprompts_only_app_password_and_reuses_tls_context(self):
        from unittest.mock import Mock
        import base64
        expected={'revision':'a'*40,'schema_version':14,'modified':False}
        context=object();connections=[]
        for status in (401,401,200):
            c=Mock();c.getresponse.return_value.status=status;c.getresponse.return_value.read.return_value=json.dumps(expected).encode();connections.append(c)
        with patch.object(mac,'app_connection',return_value=(context,'wrong-one')) as initial,patch.object(mac.http.client,'HTTPSConnection',side_effect=connections) as factory,patch.object(mac.getpass,'getpass',side_effect=['wrong-two','correct-synthetic']) as prompt,patch('builtins.print') as output:
            self.assertEqual(mac.https_accept(expected,'mac-1'),'a'*40)
        initial.assert_called_once_with('mac-1');self.assertEqual(prompt.call_count,2)
        self.assertIn('2 из 3',prompt.call_args_list[0].args[0]);self.assertIn('3 из 3',prompt.call_args_list[1].args[0])
        for i,c in enumerate(connections):
            c.close.assert_called_once();self.assertIs(factory.call_args_list[i].kwargs['context'],context)
            self.assertEqual(c.request.call_args.args[:2],('GET','/api/ready'))
            encoded=c.request.call_args.kwargs['headers']['Authorization'].split(' ',1)[1]
            self.assertEqual(base64.b64decode(encoded).decode(),'budget:'+['wrong-one','wrong-two','correct-synthetic'][i])
        self.assertNotIn('correct-synthetic',str(output.call_args_list))
    def test_network_failure_does_not_retry_or_prompt_for_password(self):
        from unittest.mock import Mock
        c=Mock();c.request.side_effect=TimeoutError('synthetic network failure')
        with patch.object(mac,'app_connection',return_value=(None,'synthetic')),patch.object(mac.http.client,'HTTPSConnection',return_value=c) as factory,patch.object(mac.getpass,'getpass') as prompt:
            with self.assertRaises(TimeoutError):mac.https_accept({},'mac-1')
        factory.assert_called_once();prompt.assert_not_called();c.close.assert_called_once()
    def test_cancel_at_password_retry_preserves_failure(self):
        from unittest.mock import Mock
        c=Mock();c.getresponse.return_value.status=401
        with patch.object(mac,'app_connection',return_value=(None,'synthetic')),patch.object(mac.http.client,'HTTPSConnection',return_value=c) as factory,patch.object(mac.getpass,'getpass',side_effect=KeyboardInterrupt),patch('builtins.print'):
            with self.assertRaises(KeyboardInterrupt):mac.https_accept({},'mac-1')
        factory.assert_called_once();c.close.assert_called_once()

class WebSessionRestoreTests(unittest.TestCase):
    def test_old_release_restore_drops_sessions_only_after_stopped_identity_gate(self):
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as root:
            p=Paths('acceptance',root)
            for directory in (p.live,p.etc,p.backups):directory.mkdir(parents=True,exist_ok=True)
            atomic(p.etc/'identity.json',{'instance':'acceptance','installation_id':'1'*32})
            atomic(p.journal,{'installation_id':'1'*32})
            secret=p.live/'web-auth.json';atomic(secret,{'sessions':[]})
            runner=Mock(return_value=subprocess.CompletedProcess([],0,b'{}',b''));a=Admin(p,runner)
            a.controller=Mock(side_effect=Refused('still running'))
            with self.assertRaises(Refused):a.backup_command('restore','--data-dir',p.live,binary=Path('/synthetic/old/budget'))
            self.assertTrue(secret.exists());runner.assert_not_called()
            a.controller=Mock();a.backup_command('restore','--data-dir',p.live,binary=Path('/synthetic/old/budget'))
            a.controller.assert_called_once_with('stopped','1'*32);self.assertFalse(secret.exists())
    def test_restore_sidecar_symlink_is_not_followed(self):
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as root:
            p=Paths('acceptance',root)
            for directory in (p.live,p.etc,p.backups):directory.mkdir(parents=True,exist_ok=True)
            atomic(p.etc/'identity.json',{'instance':'acceptance','installation_id':'1'*32});atomic(p.journal,{'installation_id':'1'*32})
            target=Path(root)/'untouched';target.write_text('synthetic');(p.live/'web-auth.json').symlink_to(target)
            a=Admin(p,Mock());a.controller=Mock()
            with self.assertRaises(Refused):a.backup_command('restore','--data-dir',p.live,binary=Path('/synthetic/old/budget'))
            self.assertEqual(target.read_text(),'synthetic');a.run.assert_not_called()
