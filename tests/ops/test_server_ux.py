import contextlib
import datetime as dt
import io
import json
import os
from pathlib import Path
import plistlib
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch,Mock
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import *
import local_backups as lb
import secret_store as ss
import pki_backup as pb
from admin_session import SessionPolicy
import session_transport as st
import server_manager as menu
import mac

class LocalPaths(unittest.TestCase):
    def test_scopes_collision_and_moscow_midnight(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)):
            a=lb.archive_destination('acceptance','20261125T010203Z-'+'a'*16)
            b=lb.archive_destination('production','20261125T010203Z-'+'a'*16)
            self.assertNotEqual(a.parent,b.parent)
            self.assertEqual(a.parent.stat().st_mode&0o777,0o700)
            now=dt.datetime(2026,12,31,21,0,tzinfo=dt.timezone.utc)
            p=lb.dmg_destination(now=now);self.assertEqual(p.name,'pki-2027-01-01_00-00-00.dmg');p.touch()
            self.assertEqual(lb.dmg_destination(now=now).name,'pki-2027-01-01_00-00-00-1.dmg')
    def test_existing_public_base_private_child(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)):
            p=Path(tmp)/'simple-budget-backups';p.mkdir();p.chmod(0o755)
            self.assertEqual(lb.directory('pki').stat().st_mode&0o777,0o700)
            self.assertEqual(p.stat().st_mode&0o777,0o755)
    def test_links_and_repository_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp);(p/'real').mkdir();(p/'link').symlink_to(p/'real')
            with self.assertRaises(Refused):lb.private_directory(p/'link'/'child')
            with patch.object(lb,'REPOSITORY',p):
                with self.assertRaises(Refused):lb.private_directory(p/'repochild')
            src=p/'src';src.write_text('x');dest=p/'dest';dest.symlink_to(src)
            with self.assertRaises(Refused):lb.publish(src,dest)
            dest.unlink();os.link(src,dest)
            with self.assertRaises(Refused):lb.publish(src,dest)
    def test_no_overwrite_or_publish_wrong_digest(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp);src=p/'src';dest=p/'dest';src.write_bytes(b'one');src.chmod(0o600)
            with self.assertRaises(Refused):lb.publish(src,dest,'0'*64)
            self.assertFalse(dest.exists());self.assertTrue(lb.publish(src,dest))
            src.write_bytes(b'one');src.chmod(0o600);self.assertFalse(lb.publish(src,dest))
            src.write_bytes(b'two')
            with self.assertRaises(Refused):lb.publish(src,dest)
            self.assertEqual(dest.read_bytes(),b'one')

class Secrets(unittest.TestCase):
    def tearDown(self):ss.SESSION=None
    def test_separation_instance_identity_device(self):
        scopes=[ss.scope(i,k,d,n) for i in ('acceptance','production') for k in ('app','client','pki') for d in ('mac-1','phone') for n in ('a'*32,'b'*32)]
        self.assertNotEqual(ss.scope('production','app','mac-1','a'*32),ss.scope('acceptance','app','mac-1','a'*32))
        self.assertEqual(len(set(scopes)),16)
        with self.assertRaises(Refused):ss.scope('acceptance','app','mac-1',None)
    def test_manual_does_not_start_keychain(self):
        with patch.object(ss,'Store') as store,patch.object(ss.getpass,'getpass',return_value='synthetic'):
            op=ss.Operation('acceptance','mac-1');self.assertEqual(op.get('app','prompt'),'synthetic');op.close();store.assert_not_called()
    def test_biometric_session_reused_and_closed(self):
        fake=Mock();fake.request.side_effect=['synthetic-app','synthetic-client']
        with patch.object(ss,'Store',return_value=fake) as ctor:
            op=ss.Operation('acceptance','mac-1','keychain','a'*32)
            self.assertEqual(op.get('app','x'),'synthetic-app');self.assertEqual(op.get('client','x'),'synthetic-client')
            ctor.assert_called_once();op.close();fake.close.assert_called_once()
    def test_cancel_does_not_fallback(self):
        fake=Mock();fake.request.side_effect=Refused('cancelled')
        with patch.object(ss,'Store',return_value=fake),patch.object(ss.getpass,'getpass') as manual:
            op=ss.Operation('acceptance','mac-1','keychain','a'*32)
            with self.assertRaises(Refused):op.get('app','x')
            manual.assert_not_called();op.close()
    def test_unavailable_locked_refuses_before_registration_prompt(self):
        for status in ({'storage_available':False},{'storage_available':False,'data_protection_status':-25308}):
            with patch.object(ss,'capabilities',return_value=status),patch.object(ss.getpass,'getpass') as prompt:
                with self.assertRaises(Refused):ss.manage('keychain-register','acceptance','mac-1','app','a'*32)
                prompt.assert_not_called()
    def test_register_replace_check_delete_never_print_secret(self):
        fake=Mock();fake.request.return_value='SYNTHETIC-SECRET-ONLY'
        with patch.object(ss,'capabilities',return_value={'storage_available':True}),patch.object(ss,'Store',return_value=fake),patch.object(ss.getpass,'getpass',return_value='SYNTHETIC-SECRET-ONLY'),patch('builtins.input',return_value='УДАЛИТЬ'),contextlib.redirect_stdout(io.StringIO()) as out:
            for action in ('keychain-register','keychain-register','keychain-check','keychain-delete'):
                ss.manage(action,'acceptance','mac-1','app','a'*32)
            self.assertNotIn('SYNTHETIC-SECRET-ONLY',out.getvalue())
            self.assertEqual([x.args[0] for x in fake.request.call_args_list],['put','put','get','delete'])
    def test_native_socket_only_no_secret_in_process_env_or_args(self):
        fake=Mock();fake.wait.return_value=0
        with patch.object(ss,'capabilities',return_value={'storage_available':True}),patch.object(ss.subprocess,'Popen',return_value=fake) as proc,patch.dict(os.environ,{'DYLD_INSERT_LIBRARIES':'bad','SECRET_TEST':'SYNTHETIC'}):
            store=ss.Store();args,kw=proc.call_args
            self.assertNotIn('SYNTHETIC',repr(args)+repr(kw['env']));self.assertNotIn('DYLD_INSERT_LIBRARIES',kw['env'])
            self.assertEqual(kw['stdout'],subprocess.DEVNULL);self.assertTrue(kw['pass_fds']);store.close()
    def test_bad_native_responses_fail_closed(self):
        for answer in ({'error':'cancelled'},{'status':-25308},{'status':-25300}):
            obj=ss.Store.__new__(ss.Store);obj.stream=Mock();obj.stream.readline.return_value=json.dumps(answer).encode()+b'\n'
            with self.assertRaises(Refused):obj.request('get',ss.scope('acceptance','app','mac-1','a'*32))

class SessionTests(unittest.TestCase):
    def request(self,action,token='b'*32):return {'action':action,'instance':'acceptance','installation_id':'a'*32,'request_id':token}
    def test_no_root_shell_cross_scope_or_repeat_mutation(self):
        p=SessionPolicy('deploy','acceptance','a'*32)
        for action in ('shell','restore','client-revoke'):
            with self.assertRaises(Refused):p.validate({'sequence':1,'action':action,'token':'b'*32},self.request(action))
        wrong=self.request('prepare');wrong['installation_id']='c'*32
        with self.assertRaises(Refused):p.validate({'sequence':1,'action':'prepare','token':'b'*32},wrong)
        p.validate({'sequence':1,'action':'prepare','token':'b'*32},self.request('prepare'))
        with self.assertRaises(Refused):p.validate({'sequence':2,'action':'prepare','token':'b'*32},self.request('prepare'))
        p.validate({'sequence':2,'action':'commit','token':'b'*32},self.request('commit'))
        with self.assertRaises(Refused):p.validate({'sequence':3,'action':'commit','token':'c'*32},self.request('commit','c'*32))
    def test_disconnect_and_nonzero_not_replayed(self):
        t=Mock();t.ssh=['ssh'];session=st.Session(t,'deploy','acceptance','a'*32)
        with patch.object(st.subprocess,'run',return_value=subprocess.CompletedProcess([],255,b'',b'')):
            with self.assertRaisesRegex(Refused,'reply unavailable'):session.wait(1)
        with patch.object(st.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'{"sequence":0,"exit":1}',b'')):
            with self.assertRaises(Refused):session.wait(1)
    def test_close_sends_only_fixed_close_once(self):
        session=st.Session(Mock(),'status','acceptance','a'*32);session.process=Mock();session.process.poll.return_value=None
        with patch.object(session,'send') as send,patch.object(session,'wait'):
            session.close();session.close();send.assert_called_once_with({'sequence':1,'action':'close'})
    def test_old_server_does_not_silently_fallback(self):
        session=st.Session(Mock(),'status','acceptance','a'*32);session.t.ssh=['ssh']
        with patch.object(st.subprocess,'run',return_value=subprocess.CompletedProcess([],1,b'',b'')),patch.object(st.subprocess,'Popen') as root:
            with self.assertRaisesRegex(Refused,'not installed'):session.start()
            root.assert_not_called()
    def test_failure_preserves_pending_and_closes_secrets(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp)/'pending-acceptance.json';atomic(p,{'request_id':'b'*32})
            fake=Mock();fake.close.side_effect=OSError('disconnected')
            original=list(mac.ACTIVE_TRANSPORTS);mac.ACTIVE_TRANSPORTS[:]=[fake]
            op=Mock();ss.SESSION=op
            try:
                with patch.object(mac,'_main',side_effect=Refused('mutation reply lost')):
                    with self.assertRaisesRegex(Refused,'mutation reply'):mac.main([])
                self.assertTrue(p.exists());op.close.assert_called_once();self.assertIsNone(ss.SESSION)
            finally:mac.ACTIVE_TRANSPORTS[:]=original

class MenuTests(unittest.TestCase):
    def setUp(self):
        tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup)
        home=patch.object(Path,'home',return_value=Path(tmp.name));home.start();self.addCleanup(home.stop)
        env=patch.dict(os.environ,{'INSTANCE':'','SUDO_MODE':'','AUTH':'manual'});env.start();self.addCleanup(env.stop)
    def test_defaults_production_session_and_dispatches_existing_commands(self):
        run=Mock(return_value=subprocess.CompletedProcess([],0))
        with patch.dict(os.environ,{'ARCHIVE':'old'}),patch('builtins.input',side_effect=['bad','1','0']),contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(menu.main(run=run),0)
        args,kw=run.call_args;self.assertEqual(args[0][-1],'status');self.assertEqual(kw['env']['INSTANCE'],'production');self.assertNotIn('ARCHIVE',kw['env'])
        self.assertEqual(kw['env']['SUDO_MODE'],'session')
        self.assertIn('Выбрано: PROD',output.getvalue());self.assertIn('SUDO_MODE=session',output.getvalue())
    def test_pending_no_automatic_mutation(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)),patch('builtins.input',side_effect=['2','0']),contextlib.redirect_stdout(io.StringIO()):
            p=Path(tmp)/'.simple-budget-admin';p.mkdir();atomic(p/'pending-production.json',{})
            run=Mock();menu.main(run=run);run.assert_not_called()
    def test_eof_ctrlc_and_nonzero_return(self):
        for error,code in ((EOFError,0),(KeyboardInterrupt,130)):
            with patch('builtins.input',side_effect=error),contextlib.redirect_stdout(io.StringIO()):self.assertEqual(menu.main(),code)
        with patch('builtins.input',side_effect=['1','0']),contextlib.redirect_stdout(io.StringIO()) as out:
            menu.main(run=Mock(return_value=subprocess.CompletedProcess([],255)))
            self.assertIn('255',out.getvalue())
    def test_production_requires_explicit_confirmation(self):
        with patch('builtins.input',side_effect=['12','acceptance','12','production','no','1','12','production','PRODUCTION','1','0']),contextlib.redirect_stdout(io.StringIO()):
            run=Mock(return_value=subprocess.CompletedProcess([],0));menu.main(run=run)
            self.assertEqual([call.kwargs['env']['INSTANCE'] for call in run.call_args_list],['acceptance','production'])
    def test_explicit_legacy_and_acceptance_are_forwarded(self):
        with patch.dict(os.environ,{'INSTANCE':'acceptance','SUDO_MODE':'legacy'}),patch('builtins.input',side_effect=['1','0']),contextlib.redirect_stdout(io.StringIO()):
            run=Mock(return_value=subprocess.CompletedProcess([],0));menu.main(run=run)
            self.assertEqual(run.call_args.kwargs['env']['INSTANCE'],'acceptance')
            self.assertEqual(run.call_args.kwargs['env']['SUDO_MODE'],'legacy')
    def test_invalid_target_or_sudo_mode_refused_before_dispatch(self):
        for values in ({'INSTANCE':'unexpected'},{'SUDO_MODE':'nopasswd'}):
            with patch.dict(os.environ,values),contextlib.redirect_stdout(io.StringIO()):
                run=Mock();self.assertEqual(menu.main(run=run),2);run.assert_not_called()
    def test_cancelled_rollback_uses_existing_confirmation_without_mutation(self):
        state=Path.home()/'.simple-budget-admin';state.mkdir(mode=0o700)
        atomic(state/'production.json',{'instance':'production','installation_id':'a'*32})
        transport=Mock()
        def dispatch(args,env):
            with patch.dict(os.environ,env),patch.object(mac,'LOCAL',state),patch.object(mac,'Transport',return_value=transport),patch.object(st,'check_protocol'):
                with self.assertRaises(Refused):mac.main([args[-1]])
            return subprocess.CompletedProcess(args,1)
        with patch('builtins.input',side_effect=['7','нет','0']) as prompt,contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(menu.main(run=dispatch),0)
        transport.request.assert_not_called()
        self.assertTrue(any('ПОДТВЕРЖДАЮ' in call.args[0] for call in prompt.call_args_list))
        self.assertIn('Выбрано: PROD',output.getvalue());self.assertIn('ID '+'a'*32,output.getvalue())

class PKITests(unittest.TestCase):
    def fixture(self,p):
        root=p/'pki';root.mkdir(mode=0o700)
        for name,content in [('ca.key',b'-----BEGIN ENCRYPTED PRIVATE KEY-----\nSYNTHETIC-NOT-A-KEY'),('ca.crt',b'SYNTHETIC CERT')]:
            (root/name).write_bytes(content);(root/name).chmod(0o600)
        local=p/'.simple-budget-admin';local.mkdir();atomic(local/'device-registry.json',{'format':1,'clients':{'mac-1':{'state':'allowed','fingerprint':'a'*64}}})
        return root
    def test_stale_registry_no_password_or_image_creation(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)):
            p=Path(tmp);root=self.fixture(p);os.utime(p/'.simple-budget-admin/device-registry.json',(1,1))
            with patch.object(pb.secret_store,'password') as prompt,patch.object(pb,'hdi') as hdi:
                with self.assertRaises(Refused):pb.create(root)
                prompt.assert_not_called();hdi.assert_not_called()
    def test_verify_failure_does_not_publish(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)):
            root=self.fixture(Path(tmp))
            def hdi(args,password=None):
                if args[0]=='create':Path(args[-1]).write_bytes(b'FAKE ENCRYPTED');return b''
                raise Refused('verify failed')
            with patch.object(pb.secret_store,'password',return_value='synthetic-pass-16'),patch.object(pb.getpass,'getpass',return_value='synthetic-pass-16'),patch.object(pb,'hdi',side_effect=hdi):
                with self.assertRaises(Refused):pb.create(root)
            self.assertEqual(list((Path(tmp)/'simple-budget-backups/pki').iterdir()),[])
    def test_null_terminated_stdin_password_never_in_args(self):
        with patch.object(pb.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'',b'')) as run:
            pb.hdi(['verify','-stdinpass','fake.dmg'],'synthetic-secret')
            args,kw=run.call_args;self.assertNotIn('synthetic-secret',repr(args)+repr(kw.get('env')));self.assertEqual(kw['input'],b'synthetic-secret\0')
    def test_mount_check_detaches_and_preserves_previous(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(Path,'home',return_value=Path(tmp)):
            root=self.fixture(Path(tmp));parent=lb.directory('pki');(parent/'previous.dmg').write_bytes(b'old')
            import shutil
            commands=[]
            def hdi(args,password=None):
                commands.append(args[0])
                if args[0]=='create':Path(args[-1]).write_bytes(b'FAKE ENCRYPTED')
                if args[0]=='attach':
                    mount=Path(args[args.index('-mountpoint')+1]);files=mount.parent/'files'
                    for f in files.iterdir():shutil.copy2(f,mount/f.name)
                    return plistlib.dumps({'system-entities':[{'mount-point':str(mount)}]})
                return b''
            with patch.object(pb.secret_store,'password',return_value='synthetic-pass-16'),patch.object(pb.getpass,'getpass',return_value='synthetic-pass-16'),patch.object(pb,'hdi',side_effect=hdi),patch.object(pb.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'',b'')) as run:
                dest=pb.create(root)
            self.assertEqual(commands,['create','verify','attach']);self.assertTrue(dest.exists());self.assertEqual((parent/'previous.dmg').read_bytes(),b'old')
            self.assertEqual(run.call_args.args[0][1],'detach');self.assertFalse(any(x.name.startswith('.pki-') for x in parent.iterdir()))

class BrokerIntegration(unittest.TestCase):
    def test_one_bounded_session_dispatches_existing_handler_and_closes(self):
        import admin_session as broker
        import remote
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);p=Paths('acceptance',root);p.global_state.mkdir(parents=True)
            parent=p.at('/home/budgetadmin/.simple-budget-incoming');parent.mkdir(parents=True)
            sid='c'*32;opid='b'*32;incoming=parent/sid;incoming.mkdir(mode=0o700)
            operation=parent/opid;operation.mkdir(mode=0o700)
            start={'action':'session','instance':'acceptance','request_id':sid,'operation':'deploy','installation_id':'a'*32}
            atomic(incoming/'request.json',start)
            atomic(incoming/'command.json',{'sequence':1,'action':'prepare','token':opid})
            calls=[]
            def request_for(token,action,instance):
                if action=='session':return incoming,start
                return operation,{'action':action,'instance':instance,'installation_id':'a'*32,'request_id':token}
            def run(args,**kw):
                calls.append(args)
                # Existing handler can acquire its lock; broker must not nest it.
                with lock(p.global_state/'operations.lock'):pass
                if args[1]=='prepare':
                    atomic(operation/'result.json',{'result':'awaiting_https_acceptance','request_id':opid})
                    atomic(incoming/'command.json',{'sequence':2,'action':'commit','token':opid})
                else:
                    atomic(operation/'result.json',{'result':'committed'})
                    atomic(incoming/'command.json',{'sequence':3,'action':'close'})
                return subprocess.CompletedProcess(args,0)
            with patch.object(remote,'request_for',side_effect=request_for),patch.object(broker.pwd,'getpwnam',return_value=SimpleNamespace(pw_uid=os.getuid(),pw_gid=os.getgid())),patch.object(Paths,'identity',return_value='a'*32):
                broker.serve('acceptance',sid,root,run=run)
            self.assertEqual([x[1] for x in calls],['prepare','commit'])
            self.assertTrue(all(x[0]=='/usr/local/sbin/budget-admin' for x in calls))
            self.assertEqual(load(incoming/'reply.json')['state'],'closed')
            self.assertFalse((incoming/'request.json').exists())
            self.assertTrue((operation/'result.json').exists())
    def test_idle_timeout_closes_without_any_command(self):
        import admin_session as broker
        import remote
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            incoming=Path(tmp);req={'action':'session','instance':'acceptance','request_id':'c'*32,'operation':'status','installation_id':'a'*32}
            atomic(incoming/'request.json',req)
            with patch.object(remote,'request_for',return_value=(incoming,req)),patch.object(broker.pwd,'getpwnam',return_value=SimpleNamespace(pw_uid=os.getuid(),pw_gid=os.getgid())),patch.object(Paths,'identity',return_value='a'*32):
                run=Mock();broker.serve('acceptance','c'*32,tmp,run=run,clock=iter([0,1801]).__next__)
                run.assert_not_called();self.assertEqual(load(incoming/'reply.json')['state'],'expired')
    def test_failed_handler_preserves_journal_and_stops_session(self):
        import admin_session as broker
        import remote
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            incoming=Path(tmp);req={'action':'session','instance':'acceptance','request_id':'c'*32,'operation':'deploy','installation_id':'a'*32}
            atomic(incoming/'request.json',req);atomic(incoming/'command.json',{'sequence':1,'action':'prepare','token':'b'*32})
            journal=incoming/'deployment.json';atomic(journal,{'phase':'trial'})
            def requests(token,action,instance):return incoming,req if action=='session' else {'action':action,'instance':instance,'installation_id':'a'*32}
            with patch.object(remote,'request_for',side_effect=requests),patch.object(broker.pwd,'getpwnam',return_value=SimpleNamespace(pw_uid=os.getuid(),pw_gid=os.getgid())),patch.object(Paths,'identity',return_value='a'*32):
                run=Mock(return_value=subprocess.CompletedProcess([],1));broker.serve('acceptance','c'*32,tmp,run=run)
                self.assertEqual(run.call_count,1);self.assertEqual(load(incoming/'reply.json')['exit'],1);self.assertTrue(journal.exists())

class PrivateTTYTests(unittest.TestCase):
    def test_local_secret_input_not_forwarded_during_proof(self):
        import pty
        import termios
        # Native PTY plus a real reply process. No ssh, sudo, terminal, or real secret.
        master,slave=pty.openpty()
        settings=termios.tcgetattr(slave);settings[3]&=~termios.ECHO;termios.tcsetattr(slave,termios.TCSANOW,settings)
        session=st.Session(Mock(),'status','acceptance','a'*32);session.master=master
        try:
            with tempfile.TemporaryDirectory() as tmp:
                waiter=Path(tmp)/'reply.py';waiter.write_text('import json; print(json.dumps({"sequence":0,"exit":0}))')
                session.t.ssh=[sys.executable,str(waiter)]
                local_input=io.StringIO('synthetic-local-app-secret\n')
                with patch.object(sys,'stdin',local_input):session.wait(timeout=2,interactive=False)
                self.assertEqual(local_input.tell(),0)
                import select
                self.assertEqual(select.select([slave],[],[],.05)[0],[])
        finally:os.close(master);os.close(slave)
    def test_start_uses_private_pty_instead_of_inheriting_terminal(self):
        import pty
        t=Mock();t.ssh=['ssh'];s=st.Session(t,'status','acceptance','a'*32)
        process=Mock();process.poll.return_value=None
        with patch.object(st.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'2\n',b'')),patch.object(st.subprocess,'Popen',return_value=process) as create,patch.object(sys.stdin,'isatty',return_value=True),patch.object(s,'wait'):
            s.start();kwargs=create.call_args.kwargs
            self.assertIsInstance(kwargs['stdin'],int);self.assertEqual(kwargs['stdin'],kwargs['stdout'])
            self.assertIsNotNone(s.master);s.close(abort=True)
            self.assertIsNone(s.master)

class MakeCompatibility(unittest.TestCase):
    def test_menu_default_with_make_exported_empty_variables(self):
        repo=Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as tmp:
            env=dict(os.environ,HOME=tmp,INSTANCE='production',AUTH='',SUDO_MODE='')
            p=subprocess.run(['make','-s','budget'],cwd=repo,env=env,input=b'0\n',stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=10)
            self.assertEqual(p.returncode,0,p.stderr)
            self.assertIn('Выбрано: PROD'.encode(),p.stdout);self.assertIn(b'AUTH=manual; SUDO_MODE=session',p.stdout)
    def test_make_menu_defaults_and_explicit_overrides(self):
        repo=Path(__file__).resolve().parents[2]
        cases=[([],{},'production','session'),
               (['INSTANCE=acceptance','SUDO_MODE=legacy'],{},'acceptance','legacy'),
               ([],{'INSTANCE':'acceptance','SUDO_MODE':'legacy'},'acceptance','legacy')]
        with tempfile.TemporaryDirectory() as tmp:
            for args,extra,instance,sudo in cases:
                env={k:v for k,v in os.environ.items() if k not in ('INSTANCE','SUDO_MODE','AUTH','ENV','MAKEFLAGS','MAKEOVERRIDES')}
                env.update(HOME=tmp,AUTH='manual',**extra)
                p=subprocess.run(['make','--no-print-directory','-s','server',*args],cwd=repo,env=env,input=b'0\n',capture_output=True,timeout=10)
                self.assertEqual(p.returncode,0,p.stderr)
                self.assertIn(('Выбрано: '+('PROD' if instance=='production' else 'STG')).encode(),p.stdout)
                self.assertIn(('SUDO_MODE='+sudo).encode(),p.stdout)
    def test_direct_mutation_make_defaults_remain_safe(self):
        repo=Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as tmp:
            probe=Path(tmp)/'probe.py'
            probe.write_text("import os,json,sys;print(json.dumps({'action':sys.argv[-1],**{k:os.environ.get(k,'') for k in ('INSTANCE','SUDO_MODE','ENV')}}))")
            env={k:v for k,v in os.environ.items() if k not in ('INSTANCE','SUDO_MODE','ENV','MAKEFLAGS','MAKEOVERRIDES')};env['HOME']=tmp
            for action in ('deploy','restore','rollback','initialize','migrate-data','cleanup','client-revoke'):
                # Replace only the Python entrypoint: never connect to a server.
                p=subprocess.run(['make','--no-print-directory','-s',action,'PYTHON='+sys.executable+' '+str(probe)],cwd=repo,env=env,capture_output=True,timeout=10)
                self.assertEqual(p.returncode,0,p.stderr)
                self.assertEqual(json.loads(p.stdout),{'action':action,'INSTANCE':'acceptance','SUDO_MODE':'legacy','ENV':''})

class SessionPreflight(unittest.TestCase):
    def test_protocol_failure_before_pending_or_release_upload(self):
        for mode in ('session','legacy'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as tmp,patch.object(mac,'LOCAL',Path(tmp)),patch.dict(os.environ,{'SUDO_MODE':mode,'AUTH':'manual'}),patch.object(mac,'Transport',return_value=Mock()) as transport,patch.object(st,'check_protocol',side_effect=Refused('old wrapper')),patch.object(mac,'release') as release:
                atomic(Path(tmp)/'acceptance.json',{'installation_id':'a'*32,'instance':'acceptance'})
                with self.assertRaisesRegex(Refused,'old wrapper'):mac.main(['deploy'])
                release.assert_not_called();transport.return_value.request.assert_not_called()
                self.assertFalse((Path(tmp)/'pending-acceptance.json').exists())
    def test_v1_cannot_accept_new_request_contract(self):
        transport=Mock();transport.ssh=['ssh']
        with patch.object(st.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'1\n',b'')):
            with self.assertRaisesRegex(Refused,'protocol v2 not installed'):st.check_protocol(transport)
        self.assertIsNot(transport._session_protocol,True)

class SecretPreflight(unittest.TestCase):
    def test_keychain_cancel_before_server_mutation(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(mac,'LOCAL',Path(tmp)),patch.dict(os.environ,{'AUTH':'keychain','SUDO_MODE':'legacy'}),patch.object(ss,'Store',side_effect=Refused('cancelled')),patch.object(mac,'Transport') as transport:
            atomic(Path(tmp)/'acceptance.json',{'installation_id':'a'*32,'instance':'acceptance'})
            with self.assertRaisesRegex(Refused,'cancelled'):mac.main(['deploy'])
            transport.assert_not_called();self.assertFalse((Path(tmp)/'pending-acceptance.json').exists());self.assertIsNone(ss.SESSION)
    def test_per_operation_cache_cleared(self):
        fake=Mock();fake.request.return_value='synthetic-only'
        with patch.object(ss,'Store',return_value=fake):
            op=ss.Operation('acceptance','mac-1','keychain','a'*32)
            self.assertEqual(op.get('app','x'),op.get('app','x'));fake.request.assert_called_once()
            op.close();self.assertEqual(op.cache,{})

class BrokerListResponse(unittest.TestCase):
    def test_client_list_response_keeps_session_alive_for_cleanup(self):
        import admin_session as broker
        import remote
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);p=Paths('acceptance',root)
            parent=p.at('/home/budgetadmin/.simple-budget-incoming');parent.mkdir(parents=True)
            sid='c'*32;opid='b'*32;incoming=parent/sid;incoming.mkdir(mode=0o700)
            operation=parent/opid;operation.mkdir(mode=0o700)
            start={'action':'session','instance':'acceptance','request_id':sid,'operation':'client-list','installation_id':'a'*32}
            atomic(incoming/'request.json',start)
            atomic(incoming/'command.json',{'sequence':1,'action':'client-list','token':opid})
            calls=[]
            def request_for(token,action,instance):
                return (incoming,start) if action=='session' else (operation,{'action':action,'instance':instance,'installation_id':'a'*32,'request_id':token})
            def run(args,**kw):
                calls.append(args[1])
                if args[1]=='client-list':
                    atomic(operation/'result.json',[{'device':'synthetic','state':'allowed'}])
                    atomic(incoming/'command.json',{'sequence':2,'action':'clean-incoming','token':opid})
                else:atomic(incoming/'command.json',{'sequence':3,'action':'close'})
                return subprocess.CompletedProcess(args,0)
            with patch.object(remote,'request_for',side_effect=request_for),patch.object(broker.pwd,'getpwnam',return_value=SimpleNamespace(pw_uid=os.getuid(),pw_gid=os.getgid())),patch.object(Paths,'identity',return_value='a'*32):
                broker.serve('acceptance',sid,root,run=run)
            self.assertEqual(calls,['client-list','clean-incoming'])
            self.assertEqual(load(incoming/'reply.json')['state'],'closed')
