import contextlib
import copy
import io
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import Mock,patch

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import Paths,atomic,Refused,load,pointer
from remote import Admin,overview
import caddy_policy as cp
import server_manager as menu
import terminal_ui as ui
import mac
from test_operations import config,CLIENTS

class StagingPolicy(unittest.TestCase):
    def test_dual_mtls_and_shared_storage_all_modes(self):
        for mode in ('open','closed','trial','test-page'):
            out=cp.render_dual(config(),CLIENTS,mode)
            self.assertEqual(cp.validate(out,CLIENTS,True)['tls_policies'],4)
            self.assertEqual(out['storage'],config()['storage'])
            self.assertEqual(out['apps']['tls']['automation'],config()['apps']['tls']['automation'])
            for n in cp.walk(out):
                if n.get('handler')=='reverse_proxy':self.assertIn(n['upstreams'][0]['dial'],('127.0.0.1:3001','127.0.0.1:3002'))
    def test_each_staging_tls_policy_and_cookie_contract_required(self):
        out=cp.render_dual(config(),CLIENTS)
        for i in range(2):
            bad=copy.deepcopy(out);bad['apps']['http']['servers']['staging']['tls_connection_policies'][i]['client_authentication']['mode']='request'
            with self.assertRaises(Refused):cp.validate(bad,CLIENTS,True)
        bad=copy.deepcopy(out)
        proxy=next(n for n in cp.walk(bad['apps']['http']['servers']['staging']) if n.get('handler')=='reverse_proxy')
        proxy['headers']['request']={}
        with self.assertRaises(Refused):cp.validate(bad,CLIENTS,True)

class StagingOperations(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.p=Paths('acceptance',self.tmp.name)
        for p in (self.p.etc,self.p.state,self.p.global_state,self.p.backups,self.p.at('/etc/caddy')):p.mkdir(parents=True,exist_ok=True)
        atomic(self.p.etc/'identity.json',{'instance':'acceptance','installation_id':'a'*32,'synthetic':True})
        atomic(self.p.global_state/'stg-enabled.json',{'state':'ready'})
        self.a=Admin(self.p);self.a.stg_capable=Mock();self.a.staging_isolation=Mock(return_value={'passed':True});self.a.system=Mock(return_value=subprocess.CompletedProcess([],0,b'LoadState=loaded\nActiveState=inactive\n'))
        self.req={'installation_id':'a'*32,'confirm':True}
    def test_start_stop_never_restart_caddy_or_prod_and_keep_data(self):
        data=self.p.state/'sentinel';data.write_bytes(b'synthetic')
        for start,states in ((True,['stopped','running','running']),(False,['running','stopped','stopped'])):
            with patch.object(self.a,'service_state',side_effect=states),patch.object(self.a,'controller') as controller,patch.object(self.p,'release',return_value=Path('/synthetic')):
                result=self.a.stg_switch(self.req,start)
                controller.assert_called_once_with('start' if start else 'stop','a'*32)
                self.assertEqual(result['service'],'running' if start else 'stopped')
            self.assertEqual(data.read_bytes(),b'synthetic')
        for call in self.a.system.call_args_list:
            self.assertNotIn('restart',call.args);self.assertNotIn('caddy.service',call.args);self.assertNotIn('simple-budget.service',call.args)
        self.a.system.assert_any_call('disable',self.p.unit)
        self.a.system.assert_any_call('disable','--now','simple-budget-backup@acceptance.timer')
    def test_unknown_stale_state_pending_or_non_synthetic_refused(self):
        for state in ('unknown','running'):
            with patch.object(self.a,'service_state',return_value=state):
                with self.assertRaises(Refused):self.a.stg_switch(self.req,True)
        atomic(self.p.journal,{})
        with self.assertRaises(Refused):self.a.stg_switch(self.req,True)
        self.p.journal.unlink();atomic(self.p.etc/'identity.json',{'instance':'acceptance','installation_id':'a'*32,'synthetic':False})
        with self.assertRaises(Refused):self.a.stg_switch(self.req,True)
        self.a.system.assert_not_called()
    def test_failed_start_closes_gate_and_stops_only_stg(self):
        with patch.object(self.a,'service_state',return_value='stopped'),patch.object(self.a,'controller',side_effect=Refused('ready failed')):
            with self.assertRaises(Refused):self.a.stg_switch(self.req,True)
        self.assertFalse(self.p.at('/etc/caddy/stg-access-open').exists())
        self.a.system.assert_any_call('stop',self.p.unit,check=False)
    def test_clean_stop_start_does_not_reset_a_garbage_collected_unit(self):
        def system(*args,**kwargs):
            if args[0]=='reset-failed':raise Refused('Unit is not loaded')
            return subprocess.CompletedProcess([],0,b'LoadState=loaded\nActiveState=inactive\n')
        self.a.system.side_effect=system
        with patch.object(self.a,'service_state',side_effect=['stopped','running','running']),patch.object(self.a,'controller') as controller,patch.object(self.p,'release',return_value=Path('/synthetic')):
            self.assertEqual(self.a.stg_switch(self.req,True)['result'],'stg_started')
            controller.assert_called_once_with('start','a'*32)
        self.assertFalse(any(c.args[0]=='reset-failed' for c in self.a.system.call_args_list))
    def test_failed_unit_resets_only_stg_and_reset_error_is_not_ignored(self):
        for fails in (False,True):
            with self.subTest(fails=fails):
                self.a.system.reset_mock()
                def system(*args,**kwargs):
                    if args[0]=='reset-failed' and fails:raise Refused('reset failed')
                    return subprocess.CompletedProcess([],0,b'ActiveState=failed\nLoadState=loaded\n')
                self.a.system.side_effect=system
                with patch.object(self.a,'service_state',side_effect=['stopped','running','running']),patch.object(self.a,'controller') as controller,patch.object(self.p,'release',return_value=Path('/synthetic')):
                    if fails:
                        with self.assertRaisesRegex(Refused,'reset failed'):self.a.stg_switch(self.req,True)
                        controller.assert_not_called()
                    else:self.a.stg_switch(self.req,True)
                self.a.system.assert_any_call('reset-failed',self.p.unit)
    def test_transition_or_missing_unit_does_not_attempt_start(self):
        for raw in (b'LoadState=loaded\nActiveState=activating',b'LoadState=not-found\nActiveState=inactive',b''):
            self.a.system.return_value=subprocess.CompletedProcess([],0,raw)
            with patch.object(self.a,'service_state',return_value='stopped'),patch.object(self.a,'controller') as controller:
                with self.assertRaisesRegex(Refused,'state changed'):self.a.stg_switch(self.req,True)
                controller.assert_not_called()
    def test_systemctl_error_identifies_fixed_operation_without_secret_arguments(self):
        a=Admin(self.p,Mock(side_effect=Refused('Command failed: systemctl (exit 1)')))
        with self.assertRaises(Refused) as error:a.system('start',self.p.unit,'synthetic-secret-never-log')
        self.assertIn('systemctl start simple-budget-acceptance.service',str(error.exception))
        self.assertNotIn('synthetic-secret',str(error.exception))
    def test_route_gate_rejects_even_a_dangling_symlink(self):
        gate=self.p.at('/etc/caddy/stg-access-open')
        gate.symlink_to(self.p.state/'absent')
        with self.assertRaises(Refused):self.a.stg_gate('closed')
        self.assertTrue(gate.is_symlink())
    def test_legacy_stg_rollback_refused_before_stop_or_data_changes(self):
        old=self.p.opt/'releases'/'legacy';old.mkdir(parents=True)
        pointer(self.p.opt/'previous',old)
        atomic(self.p.state/'rollback-pair.json',{'old':str(old),'backup_id':'20261125T000000Z-'+'a'*16})
        self.a.stg_capable.side_effect=Refused('legacy cookie contract')
        self.a.proxy=Mock();self.a.controller=Mock();self.a.backup_command=Mock()
        with self.assertRaisesRegex(Refused,'legacy cookie'):self.a.rollback(self.req)
        self.assertFalse(self.p.journal.exists())
        journal={'id':'b'*32,'installation_id':'a'*32,'phase':'trial','old':str(old)}
        atomic(self.p.journal,journal)
        with self.assertRaisesRegex(Refused,'legacy cookie'):self.a.recover(dict(self.req,decision='rollback'))
        self.assertEqual(load(self.p.journal),journal)
        self.a.system.assert_not_called();self.a.proxy.assert_not_called();self.a.controller.assert_not_called();self.a.backup_command.assert_not_called()
    def test_prod_cannot_be_reset_or_toggled(self):
        a=Admin(Paths('production',self.tmp.name));a.system=Mock()
        for fn in (lambda:a.stg_switch(self.req,False),lambda:a.stg_reset(self.req)):
            with self.assertRaises(Refused):fn()
        a.system.assert_not_called()
    def test_setup_idempotent_separate_password_and_no_prod_restart(self):
        prod=Paths('production',self.tmp.name)
        for p in (prod.etc/'credentials',self.p.etc/'credentials'):p.mkdir(parents=True,exist_ok=True)
        for p in (prod,self.p):atomic(p.etc/'credentials/app-password',b'synthetic-password-12\n')
        atomic(self.p.global_state/'proxy.json',{'instance':'production','mode':'open'})
        (self.p.global_state/'stg-enabled.json').unlink()
        self.a.controller=Mock();self.a.schedule=Mock();self.a.run=Mock();self.a.stg_save_proxy=Mock()
        env={'PATH':'ignored','BUDGET_PASSWORD_FILE':'ignored','BUDGET_INSTALLATION_ID':'a'*32}
        with patch.object(self.a,'config',side_effect=lambda:dict(env)),patch.object(Admin,'proxy') as proxy,patch('remote.getpass.getpass',return_value='Separate-STG-synthetic-12') as prompt:
            self.a.stg_setup(self.req);self.a.stg_setup(self.req)
        self.assertEqual(prompt.call_count,2);proxy.assert_called_once_with('open')
        self.a.stg_save_proxy.assert_called_once_with()
        self.assertEqual(self.a.run.call_count,1) # one explicit firewall setup
        self.assertEqual((prod.etc/'credentials/app-password').read_bytes(),b'synthetic-password-12\n')
        self.assertIn('BUDGET_PUBLIC_ORIGIN=https://171.22.237.16:8443',(self.p.config).read_text())
        self.assertIn('BUDGET_ADDR=127.0.0.1:3002',(self.p.config).read_text())
        for call in self.a.controller.call_args_list:self.assertEqual(call.args,('stop','a'*32))
    def test_stg_schedule_does_not_write_prod_backup_metadata(self):
        for instance in ('production','acceptance'):
            p=Paths(instance,self.tmp.name)
            p.etc.mkdir(parents=True,exist_ok=True);p.backups.mkdir(parents=True,exist_ok=True)
            atomic(p.config,b'');atomic(p.backups/'repository.json',{})
        run=Mock(return_value=subprocess.CompletedProcess([],1,b''))
        with patch.object(Admin,'backup_command',autospec=True) as command:
            Admin(self.p,run).schedule()
            self.assertEqual(command.call_count,1)
            self.assertEqual(command.call_args.args[0].p.instance,'acceptance')
        self.assertTrue(all('simple-budget-backup@production.timer' not in c.args[0] for c in run.call_args_list))
    def test_promotion_uses_exact_server_binary_and_refuses_changed_sha(self):
        release=self.p.opt/'releases'/'tested';release.mkdir(parents=True)
        pointer(self.p.opt/'current',release)
        prod=Admin(Paths('production',self.tmp.name));prod.prepare=Mock(return_value={'result':'awaiting_https_acceptance'})
        req={'confirm':True,'release_sha256':'b'*64,'installation_id':'c'*32}
        with patch.object(Admin,'service_state',return_value='running'),patch.object(Admin,'ready'),patch.object(Admin,'stg_capable'),patch.object(Admin,'manifest',return_value={'files':[{'sha256':'b'*64}]}):
            prod.promote(req);prod.prepare.assert_called_once_with(release,req)
            prod.prepare.reset_mock()
            with self.assertRaises(Refused):prod.promote(dict(req,release_sha256='c'*64))
            prod.prepare.assert_not_called()
    def test_isolation_probe_cannot_treat_runuser_failure_as_denied_access(self):
        a=Admin(self.p)
        a.run=Mock(return_value=subprocess.CompletedProcess([],1,b''))
        with patch('remote.pwd.getpwnam',side_effect=lambda user:SimpleNamespace(pw_uid=995 if user=='budget-acceptance' else 996)):
            with self.assertRaises(Refused):a.staging_isolation()
        self.assertEqual(a.run.call_count,1)

class StagingSetupRecovery(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.p=Paths('acceptance',self.tmp.name);self.prod=Paths('production',self.tmp.name)
        for p in (self.p.etc/'credentials',self.prod.etc/'credentials',self.p.state,self.p.backups,
                  self.p.global_state,self.p.at('/etc/caddy'),self.p.at('/etc/systemd/system/caddy.service.d')):
            p.mkdir(parents=True,exist_ok=True)
        atomic(self.p.etc/'identity.json',{'instance':'acceptance','installation_id':'a'*32,'synthetic':True})
        for p,value in ((self.p,b'synthetic-stg-password'),(self.prod,b'synthetic-prod-password')):
            atomic(p.etc/'credentials/app-password',value)
        self.config=self.p.at('/etc/caddy/simple-budget.json')
        self.drop=self.p.at('/etc/systemd/system/caddy.service.d/simple-budget.conf')
        atomic(self.config,cp.render(config(),CLIENTS,'open',3001),0o640)
        atomic(self.drop,b'[Service]\nExecStart=synthetic\n',0o644)
        atomic(self.p.global_state/'caddy-base.json',config())
        atomic(self.p.global_state/'clients.json',{'synthetic':True})
        self.before=self.config.read_bytes();self.drop_before=self.drop.read_bytes()
        self.proxy={'instance':'production','mode':'open','config_sha256':hashlib.sha256(self.before).hexdigest()}
        atomic(self.p.global_state/'proxy.json',self.proxy)
        self.req={'installation_id':'a'*32,'confirm':True}
        self.a=Admin(self.p)
        self.a.run=Mock(return_value=subprocess.CompletedProcess([],0,b''))
        self.a.system=Mock(return_value=subprocess.CompletedProcess([],0,b''))
        self.a.controller=Mock();self.a.schedule=Mock()
        self.a.staging_isolation=Mock(return_value={'passed':True})
        self.a.config=Mock(side_effect=lambda:{'PATH':'ignored','BUDGET_PASSWORD_FILE':'ignored'})
        patcher=patch.object(Admin,'registry',return_value=({},CLIENTS));patcher.start();self.addCleanup(patcher.stop)
        self.snapshot=self.p.global_state/'stg-setup-recovery.json'
        self.enabled=self.p.global_state/'stg-enabled.json'
    def publish_dual(self,mode):
        self.assertEqual(mode,'open')
        self.assertTrue(self.snapshot.exists()) # durable recovery record precedes replacement
        atomic(self.config,cp.render_dual(config(),CLIENTS,'open'),0o640)
        atomic(self.drop,b'[Service]\nExecStart=new-synthetic\n',0o644)
    def assert_restored(self):
        self.assertEqual(self.config.read_bytes(),self.before)
        self.assertEqual(self.drop.read_bytes(),self.drop_before)
        self.assertEqual(load(self.p.global_state/'proxy.json'),self.proxy)
        self.assertFalse(self.enabled.exists())
        self.assertFalse(self.p.at('/etc/caddy/stg-access-open').exists())
        self.a.controller.assert_called_with('stop','a'*32)
        self.assertFalse(any('simple-budget.service' in c.args for c in self.a.system.call_args_list))
    def test_success_preserves_exact_private_snapshot_and_explicit_rollback(self):
        with patch.object(Admin,'proxy',side_effect=self.publish_dual) as proxy:
            self.a.stg_setup(self.req);self.a.stg_setup(self.req)
            self.assertEqual(proxy.call_count,1)
        snap=load(self.snapshot)
        self.assertEqual(snap['config'].encode(),self.before)
        self.assertEqual(self.snapshot.stat().st_mode&0o777,0o600)
        self.assertNotIn('synthetic-prod-password',self.snapshot.read_text())
        self.assertEqual(load(self.enabled)['state'],'ready')
        result=self.a.stg_setup(dict(self.req,decision='rollback'))
        self.assertTrue(result['caddy_restored']);self.assert_restored()
        self.assertEqual(sum(c.args[0]==['/usr/sbin/ufw','allow','8443/tcp'] for c in self.a.run.call_args_list),1)
        self.assertFalse(any('delete' in c.args[0] for c in self.a.run.call_args_list))
    def test_restart_or_firewall_failure_restores_and_reports_failure(self):
        for failure in ('restart','firewall'):
            with self.subTest(failure=failure):
                def publish(mode):
                    self.publish_dual(mode)
                    if failure=='restart':raise Refused('restart failed')
                def run(args,**kwargs):
                    if args[0]=='/usr/sbin/ufw':raise Refused('firewall failed')
                    return subprocess.CompletedProcess(args,0,b'')
                self.a.run.side_effect=run
                with patch.object(Admin,'proxy',side_effect=publish):
                    with self.assertRaisesRegex(Refused,'previous Caddy configuration restored'):self.a.stg_setup(self.req)
                self.assert_restored()
    def test_interruption_requires_explicit_recovery_and_retry_is_possible(self):
        def interrupt(mode):self.publish_dual(mode);raise KeyboardInterrupt()
        with patch.object(Admin,'proxy',side_effect=interrupt):
            with self.assertRaises(KeyboardInterrupt):self.a.stg_setup(self.req)
        self.assertEqual(load(self.enabled)['state'],'preparing')
        snapshot=self.snapshot.read_bytes()
        with self.assertRaisesRegex(Refused,'DECISION=rollback'):self.a.stg_setup(self.req)
        self.assertEqual(self.snapshot.read_bytes(),snapshot)
        self.a.stg_setup(dict(self.req,decision='rollback'));self.assert_restored()
        with patch.object(Admin,'proxy',side_effect=self.publish_dual):self.a.stg_setup(self.req)
        self.assertEqual(load(self.enabled)['state'],'ready')
    def test_failed_recovery_retains_record_and_can_be_retried(self):
        self.a.stg_save_proxy();self.publish_dual('open')
        def system(*args,**kwargs):
            if args==('restart','caddy.service'):raise Refused('systemctl failed')
            return subprocess.CompletedProcess([],0,b'')
        self.a.system.side_effect=system
        with self.assertRaises(Refused):self.a.stg_setup(dict(self.req,decision='rollback'))
        self.assertTrue(self.snapshot.exists())
        self.assertEqual(load(self.enabled)['state'],'recovering')
        self.a.system.side_effect=None
        self.a.stg_setup(dict(self.req,decision='rollback'));self.assert_restored()
    def test_validation_failure_before_setup_does_not_mutate_or_stop_services(self):
        self.a.run.side_effect=Refused('Caddy validate failed')
        with self.assertRaises(Refused):self.a.stg_setup(self.req)
        self.assertFalse(self.snapshot.exists())
        self.assertEqual(self.config.read_bytes(),self.before)
        self.a.controller.assert_not_called();self.a.system.assert_not_called()
    def test_recovery_validation_failure_preserves_current_configuration(self):
        self.a.stg_save_proxy();self.publish_dual('open');dual=self.config.read_bytes()
        self.a.run.side_effect=Refused('Caddy validate failed')
        with self.assertRaises(Refused):self.a.stg_restore_proxy()
        self.assertEqual(self.config.read_bytes(),dual)
        self.a.controller.assert_not_called();self.a.system.assert_not_called()
    def test_registry_or_configuration_drift_refuses_before_mutation(self):
        self.a.stg_save_proxy()
        atomic(self.p.global_state/'clients.json',{'changed':True})
        with self.assertRaisesRegex(Refused,'registry changed'):self.a.stg_restore_proxy()
        atomic(self.p.global_state/'clients.json',{'synthetic':True})
        atomic(self.config,cp.render(config(),CLIENTS,'closed',3001))
        with self.assertRaisesRegex(Refused,'outside setup'):self.a.stg_restore_proxy()
        self.a.controller.assert_not_called();self.a.system.assert_not_called()
    def test_pending_prod_and_invalid_snapshot_are_rejected(self):
        self.prod.state.mkdir(parents=True,exist_ok=True);atomic(self.prod.journal,{})
        with self.assertRaisesRegex(Refused,'Finish PROD'):self.a.stg_setup(self.req)
        self.assertFalse(self.snapshot.exists());self.a.controller.assert_not_called()
        self.prod.journal.unlink();self.a.stg_save_proxy()
        snap=load(self.snapshot);snap['config']='{}';atomic(self.snapshot,snap)
        with self.assertRaisesRegex(Refused,'configuration changed'):self.a.stg_restore_proxy()
        self.a.controller.assert_not_called()
    def test_snapshot_links_refused(self):
        target=self.p.state/'target';target.write_bytes(b'unchanged')
        for hard in (False,True):
            if hard:os.link(target,self.snapshot)
            else:self.snapshot.symlink_to(target)
            with self.assertRaises(Refused):self.a.stg_save_proxy()
            with self.assertRaises(Refused):self.a.stg_restore_proxy()
            self.assertEqual(target.read_bytes(),b'unchanged')
            self.snapshot.unlink()

class SetupCLI(unittest.TestCase):
    def test_default_and_explicit_recovery_keep_confirmation_and_fixed_session_action(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(mac,'LOCAL',Path(tmp)),patch.dict(os.environ,{'AUTH':'manual','DECISION':'','INSTANCE':'acceptance'}),patch.object(mac.secret_store,'SESSION',None),patch('session_transport.check_protocol'),patch.object(mac,'show_result'),patch.object(mac,'confirm') as confirm:
            atomic(Path(tmp)/'acceptance.json',{'installation_id':'a'*32})
            for args,decision in (([],'finish'),(['--decision','rollback'],'rollback')):
                t=Mock();t.request.return_value=({'result':'ok'},'b'*32)
                with patch.object(mac,'Transport',return_value=t):mac._main(['stg-setup',*args])
                self.assertEqual(t.operation,'stg-setup')
                first=t.request.call_args_list[0]
                self.assertEqual(first.args[:2],('stg-setup','acceptance'))
                self.assertEqual(first.args[2],{'installation_id':'a'*32,'confirm':True,'decision':decision})
            self.assertEqual(confirm.call_count,2)
            self.assertIn('Восстановить прежнюю',confirm.call_args.args[0])
            t=Mock()
            with patch.object(mac,'Transport',return_value=t),patch.object(mac,'confirm',side_effect=Refused('cancelled')):
                with self.assertRaises(Refused):mac._main(['stg-setup','--decision','rollback'])
            t.request.assert_not_called()

class OverviewTests(unittest.TestCase):
    def test_verified_sha_and_independent_unknown_state_without_privilege(self):
        with tempfile.TemporaryDirectory() as tmp:
            data=b'\x7fELF\x02synthetic executable'
            for instance in ('production','acceptance'):
                p=Paths(instance,tmp);release=p.opt/'releases'/'synthetic';release.mkdir(parents=True)
                atomic(release/'budget',data,0o755)
                atomic(release/'release.json',{'application':'simple-budget-app','target':'linux/amd64','embedded_frontend':True,'revision':('a' if instance=='production' else 'b')*40,'schema_version':14,'modified':False,'files':[{'name':'budget','size':len(data),'sha256':hashlib.sha256(data).hexdigest()}]},0o644)
                pointer(p.opt/'current',release)
            calls=[]
            def run(args,**kwargs):
                calls.append(args)
                text=b'LoadState=loaded\nActiveState=active\nMainPID=123\n' if 'simple-budget.service' in args else b'LoadState=loaded\nActiveState=inactive\nMainPID=0\n'
                return subprocess.CompletedProcess(args,0,text)
            first=overview(tmp,run)
            self.assertEqual(first['production']['service'],'running')
            self.assertEqual(first['acceptance']['service'],'stopped')
            self.assertEqual(first['production']['revision'],'a'*40)
            self.assertEqual(first['acceptance']['sha256'],hashlib.sha256(data).hexdigest())
            atomic(Paths('acceptance',tmp).release()/'budget',b'tampered')
            second=overview(tmp,run)
            self.assertIsNone(second['acceptance']['revision'])
            self.assertEqual(second['production']['revision'],'a'*40)
            self.assertTrue(all(args[:2]==['/usr/bin/systemctl','show'] for args in calls))

class StagingMenu(unittest.TestCase):
    def test_toggle_dispatch_refresh_and_unknown_refusal(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)),patch.dict(os.environ,{'INSTANCE':'','SUDO_MODE':''}),patch.object(menu.version_status,'git',return_value=''):
            for state,expected in (('running','stg-stop'),('stopped','stg-start'),('unknown',None)):
                run=Mock(return_value=subprocess.CompletedProcess([],0))
                observe=Mock(return_value={'production':{'service':'running','revision':'a'*40},'acceptance':{'service':state,'revision':'b'*40}})
                with patch('builtins.input',side_effect=['14','0']),contextlib.redirect_stdout(io.StringIO()) as output:
                    self.assertEqual(menu.main(run=run,observe=observe),0)
                if expected:
                    self.assertEqual(run.call_args.args[0][-1],expected)
                    self.assertEqual(run.call_args.kwargs['env']['INSTANCE'],'acceptance')
                    self.assertEqual(run.call_args.kwargs['env']['SUDO_MODE'],'session')
                else:run.assert_not_called()
                self.assertEqual(observe.call_count,2)
                self.assertIn('aaaaaaaaaaaa',output.getvalue());self.assertIn('bbbbbbbbbbbb',output.getvalue())
    def test_colors_opt_out_and_non_tty(self):
        with patch.object(sys.stdout,'isatty',return_value=True),patch.dict(os.environ,{'NO_COLOR':'1'}):
            self.assertEqual(ui.color('safe','red'),'safe')
        with patch.object(sys.stdout,'isatty',return_value=False):self.assertEqual(ui.color('safe','green'),'safe')

class TransportPresentation(unittest.TestCase):
    def test_quiet_service_transfers_and_verbose_errors(self):
        for verbose in ('0','1'):
            with patch.dict(os.environ,{'ENV':'production','VERBOSE':verbose}),patch.object(mac.subprocess,'check_output',return_value='hostname 171.22.237.16\nuser budgetadmin\nstricthostkeychecking true\n'):
                t=mac.Transport()
                try:
                    self.assertEqual('-q' in t.scp,verbose=='0')
                    self.assertEqual('LogLevel=ERROR' in t.ssh,verbose=='0')
                    failure=subprocess.CalledProcessError(255,['scp'])
                    with patch.object(mac.subprocess,'run',side_effect=failure),patch.object(ui,'interactive',return_value=True):
                        with self.assertRaises(subprocess.CalledProcessError):t.upload(Path('/synthetic/request.json'),'a'*32,'request.json')
                finally:mac.ACTIVE_TRANSPORTS.remove(t)
    def test_archive_progress_does_not_show_mailbox_progress(self):
        t=mac.Transport.__new__(mac.Transport);t.scp=['scp','-q'];t.session=None
        with patch.object(mac.subprocess,'run') as run,patch.object(ui,'interactive',return_value=True),patch.dict(os.environ,{'OUTPUT':''}),patch.object(mac.os,'chmod'):
            with tempfile.TemporaryDirectory() as tmp:
                t.download('a'*32,'result.json',Path(tmp)/'result.json')
                self.assertIn('-q',run.call_args.args[0])
                t.download('a'*32,'download.tar.gz',Path(tmp)/'backup.tar.gz')
                self.assertNotIn('-q',run.call_args.args[0])
            self.assertTrue(run.call_args.kwargs['check'])

if __name__=='__main__':unittest.main()
