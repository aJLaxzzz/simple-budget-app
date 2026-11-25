#!/usr/bin/env python3
"""Installed fixed-operation administrator. Never accepts a shell command or root prefix."""
import datetime as dt
import getpass
import grp
import json
import os
from pathlib import Path
import platform
import pwd
import re
import shutil
import socket
import ssl
import sys
import tempfile
import time
import uuid
from common import *
import caddy_policy

class Admin:
    def __init__(self, paths, run=None):
        self.p=paths; self.run=run or Runner()
    def system(self, *args, check=True):
        return self.run(['/usr/bin/systemctl',*args],check=check,timeout=90)
    def config(self):
        raw=read(self.p.config,16384).decode(); env={'PATH':'/usr/sbin:/usr/bin:/sbin:/bin'}
        for line in raw.splitlines():
            if not line or line.startswith('#'):continue
            k,v=line.split('=',1); require(k.startswith('BUDGET_') or k in ('TZ','GOMEMLIMIT','GOMAXPROCS'), 'Unexpected configuration entry')
            require(not any(c in v for c in '\n\r\0\"\''), 'Invalid environment value'); env[k]=v
        require(env.get('BUDGET_INSTALLATION_ID')==self.p.identity(), 'Configuration ID mismatch')
        require(env.get('BUDGET_DATA_DIR')==str(self.p.live) and env.get('BUDGET_BACKUP_DIR')==str(self.p.backups), 'Configuration path mismatch')
        env['BUDGET_PASSWORD_FILE']=str(self.p.etc/'credentials/app-password')
        return env
    def binary(self, release=None):
        p=(release or self.p.release())/'budget'; regular(p,128<<20,0 if self.p.root==Path('/') else None)
        return p
    def ready(self, release=None):
        return json.loads(self.run([self.binary(release),'ready'],env=self.config(),timeout=10).stdout)
    def service_state(self):
        # Use named properties; order of systemctl output is not a contract.
        q=self.system('show',self.p.unit,'--property=LoadState,ActiveState,SubState,MainPID').stdout.decode()
        d=dict(x.split('=',1) for x in q.splitlines() if '=' in x)
        require(d.get('LoadState')=='loaded','Application unit is not loaded')
        if d.get('ActiveState')=='active' and int(d.get('MainPID','0'))>0:return 'running'
        if d.get('ActiveState') in ('inactive','failed') and d.get('MainPID')=='0':return 'stopped'
        raise Refused('Application service is transitioning or unknown')
    def controller(self, action, installation):
        require(installation==self.p.identity(),'Controller installation mismatch')
        if action=='status': require(self.service_state()=='running','Service not running');return
        if action=='stopped': require(self.service_state()=='stopped','Service not stopped');return
        if action=='stop':
            self.system('stop',self.p.unit);require(self.service_state()=='stopped','Service did not stop');return
        require(action=='start','Unknown controller action')
        self.startup_guard()
        self.run(['/usr/bin/systemd-analyze','verify',self.p.at('/etc/systemd/system')/self.p.unit],timeout=90)
        self.system('start',self.p.unit)
        for _ in range(30):
            try:self.ready();return
            except Refused:time.sleep(1)
        raise Refused('Authenticated readiness failed after start')
    def startup_guard(self):
        self.p.identity()
        if self.p.journal.exists():
            j=load(self.p.journal)
            require(j.get('installation_id')==self.p.identity() and j.get('phase') in ('trial','opening','committed','rollback-trial'), 'Unfinished deployment blocks startup')
        require(not (self.p.state/'.live.restore.json').exists(),'Backup recovery required before startup')
    def backup(self, reason='manual', protected=False, controller=True):
        args=[self.binary(),'admin','backup','create','--data-dir',self.p.live,'--backup-dir',self.p.backups,'--installation-id',self.p.identity(),'--reason',reason]
        if protected:args+=['--protect']
        if controller:args+=['--controller',self.p.controller]
        return json.loads(self.run(args,timeout=2700).stdout)
    def backup_command(self, action, *extra, binary=None):
        # Old releases predate web-auth.json. Invalidate this non-user-data sidecar
        # before asking their restore CLI to inspect live. Existing journals and
        # stopped-service controller keep startup closed throughout this step.
        session_file=self.p.live/'web-auth.json'
        if action=='restore' and '--data-dir' in extra and Path(extra[extra.index('--data-dir')+1])==self.p.live and session_file.exists():
            require(self.p.journal.exists(),'Restore session invalidation requires deployment journal')
            require(load(self.p.journal)['installation_id']==self.p.identity(),'Restore identity mismatch')
            self.controller('stopped',self.p.identity())
            regular(session_file,256<<10)
            session_file.unlink();sync_dir(self.p.live)
        return json.loads(self.run([binary or self.binary(),'admin','backup',action,'--backup-dir',self.p.backups,'--installation-id',self.p.identity(),*extra],timeout=2700).stdout)
    def archive(self, ident):
        require(BACKUP_ID.fullmatch(ident or ''),'Invalid backup ID')
        found=[self.p.backups/c/ident/'backup.tar.gz' for c in ('daily','pre-update','manual') if (self.p.backups/c/ident/'receipt.json').exists()]
        require(len(found)==1,'Backup ID not unique or absent');regular(found[0]);return found[0]
    def schedule(self):
        for instance in ('production','acceptance'):
            p=Paths(instance,self.p.root)
            if not p.config.exists() or not (p.backups/'repository.json').exists():continue
            a=Admin(p,self.run); timer='simple-budget-backup@'+instance+'.timer'
            enabled=self.system('is-enabled',timer,check=False).returncode==0
            active=self.system('is-active',timer,check=False).returncode==0
            args=[]
            if enabled and active:
                q=self.system('show',timer,'--property=NextElapseUSecRealtime','--value').stdout.decode().strip()
                # GNU date is used only on Ubuntu, never in Mac scripts.
                value=self.run(['/usr/bin/date','--date',q,'--iso-8601=seconds']).stdout.decode().strip()
                require(dt.datetime.fromisoformat(value)>dt.datetime.now(dt.timezone.utc),'Timer has no future elapse')
                args=['--enabled','--next-at',value]
            a.backup_command('schedule',*args)
    def registry(self):
        reg=load(self.p.global_state/'clients.json')
        require(reg.get('format')==1 and reg.get('clients'),'PKI registry unavailable')
        result=[]
        for name,c in reg['clients'].items():
            require(NAME.fullmatch(name) and SHA.fullmatch(c['fingerprint']),'Invalid device registry')
            if c['state']=='allowed':
                p=self.p.at('/etc/caddy/pki/'+name+'.crt')
                require(self.cert(p)['fingerprint']==c['fingerprint'],'Public certificate changed outside registry')
                result.append('/etc/caddy/pki/'+name+'.crt')
        require(result,'Empty allowlist: leave Caddy stopped')
        return reg,result
    def cert(self,path):
        raw=read(path,64<<10).decode()
        require(raw.count('-----BEGIN CERTIFICATE-----')==1 and 'PRIVATE KEY' not in raw,'Single public certificate required')
        der=ssl.PEM_cert_to_DER_cert(raw)
        out=self.run(['/usr/bin/openssl','x509','-in',path,'-noout','-serial','-issuer','-dates','-ext','subjectAltName,extendedKeyUsage,basicConstraints']).stdout.decode()
        fields=dict(x.split('=',1) for x in out.splitlines() if '=' in x and not x.startswith(' '))
        expiry=ssl.cert_time_to_seconds(fields['notAfter'])
        return {'fingerprint':hashlib.sha256(der).hexdigest(),'serial':fields.get('serial'),'issuer':fields.get('issuer'),'not_before':fields.get('notBefore'),'ip_san':re.findall(r'IP Address:([0-9a-fA-F.:]+)',out),'not_after':fields['notAfter'],'remaining_hours':round((expiry-time.time())/3600,2),'details':out}
    def proxy_guard(self):
        reg,expected=self.registry()
        pending=self.p.global_state/'revocation-pending.json'
        if pending.exists():
            intent=load(pending);record=reg['clients'].get(intent['device'],{})
            require(record.get('state')=='revoked' and record.get('fingerprint')==intent['fingerprint'],'Incomplete device revocation blocks Caddy startup')
        return caddy_policy.validate(load(self.p.at('/etc/caddy/simple-budget.json')),expected,True)
    def proxy(self, mode):
        reg,expected=self.registry();base=load(self.p.global_state/'caddy-base.json')
        config=caddy_policy.render(base,expected,mode,self.p.port)
        p=self.p.at('/etc/caddy/simple-budget.json');candidate=p.with_suffix('.candidate.json')
        atomic(candidate,config,0o640);os.chown(candidate,0,grp.getgrnam('caddy').gr_gid) if self.p.root==Path('/') else None
        self.run(['/usr/bin/caddy','validate','--config',candidate],timeout=30)
        # Persist the secure route before restart. Never roll back a revoked allowlist.
        os.replace(candidate,p);sync_dir(p.parent)
        drop=self.p.at('/etc/systemd/system/caddy.service.d/simple-budget.conf')
        drop.parent.mkdir(parents=True,exist_ok=True)
        atomic(drop,read(self.p.at('/usr/local/lib/simple-budget/systemd/caddy-budget.conf')),0o644)
        self.system('daemon-reload')
        try:
            self.system('restart','caddy.service')
            require(self.system('is-active','caddy.service',check=False).returncode==0,'Caddy failed')
        except BaseException:
            self.system('stop','caddy.service',check=False)
            raise
        atomic(self.p.global_state/'proxy.json',{'instance':self.p.instance,'mode':mode,'at':utc(),'registry_sha256':sha(self.p.global_state/'clients.json'),'config_sha256':sha(p)})
    def seal_previous(self,result):
        name=result.get('previous_data')
        if not name:return
        path=Path(name)
        require(path.parent==self.p.state and path.name.startswith('.live.previous-') and path.is_dir() and not path.is_symlink(),'Unexpected previous-data path')
        for file in path.iterdir():regular(file)
        if self.p.root==Path('/'):
            for file in path.iterdir():os.chown(file,0,0);os.chmod(file,0o600)
            os.chown(path,0,0);os.chmod(path,0o700)

    def own_live(self):
        uid=pwd.getpwnam(self.p.user).pw_uid;gid=grp.getgrnam(self.p.user).gr_gid
        require(load(self.p.live/'installation.json')['installation_id']==self.p.identity(),'Live identity changed')
        allowed={'installation.json','planner.db','planner.db-wal','planner.db-shm','planner.db.lock','data.json','categories.json','web-auth.json'}
        require(all(x.name in allowed for x in self.p.live.iterdir()),'Unknown live file preserved; inspect before ownership changes')
        for file in self.p.live.iterdir():regular(file)
        for p in [self.p.live,*self.p.live.iterdir()]:
            require(not p.is_symlink(),'Unexpected live symlink');os.chown(p,uid,gid)
            os.chmod(p,0o700 if p.is_dir() else 0o600)
    def manifest(self, directory, clean=True):
        m=load(directory/'release.json',16384)
        require(m.get('application')=='simple-budget-app' and m.get('target')=='linux/amd64' and m.get('embedded_frontend') is True,'Unexpected release target')
        require(re.fullmatch('[a-f0-9]{40}',m.get('revision','')) and isinstance(m.get('schema_version'),int),'Invalid release metadata')
        require(not clean or m.get('modified') is False,'Production release must come from a clean committed tree')
        f=m.get('files',[]);require(len(f)==1 and f[0].get('name')=='budget' and SHA.fullmatch(f[0].get('sha256','')),'Invalid release allowlist')
        require(regular(directory/'budget',128<<20).st_size==f[0]['size'] and sha(directory/'budget')==f[0]['sha256'],'Release checksum/size mismatch')
        with open(directory/'budget','rb') as header:require(header.read(5)==b'\x7fELF\x02','64-bit ELF required')
        return m
    def install_release(self, incoming):
        m=self.manifest(incoming);space(self.p.opt,3*m['files'][0]['size']+64*1024*1024)
        name=m['revision'][:12]+'-'+m['files'][0]['sha256'][:16];target=self.p.opt/'releases'/name
        if target.exists():require(self.manifest(target)==m,'Existing release differs');return target,m
        stage=Path(tempfile.mkdtemp(prefix='.release-',dir=self.p.opt/'releases'))
        try:
            copy_verified(incoming/'budget',stage/'budget',m['files'][0]['sha256'],0o755,128<<20)
            atomic(stage/'release.json',m,0o644)
            v=json.loads(self.run([stage/'budget','version']).stdout)
            require(v['revision']==m['revision'] and v['schema_version']==m['schema_version'] and v['modified'] is False,'Binary version differs from manifest')
            os.chmod(stage,0o755);os.rename(stage,target);sync_dir(target.parent)
        finally:
            if stage.exists():shutil.rmtree(stage)
        return target,m
    def save_journal(self,j,phase):
        j['phase']=phase;j['updated_at']=utc();atomic(self.p.journal,j)
    def prepare(self, incoming, request):
        require(not self.p.journal.exists(),'Recover unfinished deployment before another operation')
        require(request['installation_id']==self.p.identity(),'Wrong installation ID')
        new,m=self.install_release(incoming);old=self.p.release()
        if new==old:
            self.ready();return {'result':'already_current','version':m}
        previous=str(self.p.release('previous')) if (self.p.opt/'previous').exists() else None
        j={'format':1,'id':request['request_id'],'installation_id':self.p.identity(),'old':str(old),'new':str(new),'version':m,'started_at':utc(),'backup_id':None,'previous':previous}
        self.save_journal(j,'closing')
        try:
            self.proxy('closed')
            self.controller('stop',self.p.identity())
            self.save_journal(j,'backup')
            receipt=self.backup('pre-update',True,False)
            j['backup_id']=receipt['manifest']['backup_id'];self.save_journal(j,'candidate')
            self.preflight_restore(new,self.archive(j['backup_id']))
            self.save_journal(j,'migrating')
            self.run([new/'budget','admin','migrate','--data-dir',self.p.live,'--installation-id',self.p.identity(),'--confirm'],timeout=600)
            self.own_live()
            self.save_journal(j,'switching');pointer(self.p.opt/'previous',old);pointer(self.p.opt/'current',new)
            self.save_journal(j,'trial');self.controller('start',self.p.identity());self.proxy('trial')
            return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':m}
        except BaseException:
            # Persist state and leave public access closed. No blind old-binary startup.
            self.system('stop',self.p.unit,check=False)
            raise
    def preflight_restore(self,release,archive):
        stage=Path(tempfile.mkdtemp(prefix='candidate-',dir=self.p.state));os.chmod(stage,0o700)
        live=stage/'live';repo=stage/'backups';repo.mkdir(mode=0o700)
        try:
            self.run([release/'budget','admin','backup','restore','--archive',archive,'--data-dir',live,'--backup-dir',repo,'--installation-id',self.p.identity(),'--confirm'],timeout=900)
            self.run([release/'budget','admin','check','--data-dir',live,'--installation-id',self.p.identity()],timeout=120)
        finally:shutil.rmtree(stage)
    def commit(self,request):
        j=load(self.p.journal)
        require(request.get('request_id')==j['id'] and request.get('installation_id')==self.p.identity(),'Commit identity mismatch')
        require(j['phase'] in ('trial','rollback-trial'),'Deployment is not awaiting acceptance')
        require(request.get('https_verified_revision')==j['version']['revision'],'Mac HTTPS verification missing')
        self.ready()
        # Before exposing writes, mark a non-auto-rollback boundary durably.
        self.save_journal(j,'opening');self.proxy('open');self.save_journal(j,'committed')
        return self.finish_commit(j)
    def finish_commit(self,j):
        atomic(self.p.state/'last-deployment.json',j)
        if j['old']!=j['new'] and j.get('backup_id'):atomic(self.p.state/'rollback-pair.json',j)
        self.system('enable',self.p.unit,'simple-budget-backup@'+self.p.instance+'.timer')
        self.system('start','simple-budget-backup@'+self.p.instance+'.timer')
        self.schedule();self.prune_releases()
        if j.get('archive'):
            source=Path(j['archive'])
            require(source.parent==self.p.state and source.name=='restore-source-'+j['id']+'.tar.gz','Invalid retained restore source')
            source.unlink(missing_ok=True);sync_dir(self.p.state)
        self.p.journal.unlink(missing_ok=True);sync_dir(self.p.state)
        return {'result':'committed','release':str(self.p.release())}
    def recover(self,request):
        if not self.p.journal.exists():
            if (self.p.backups/'operation.json').exists():
                require(request.get('confirm') is True and request.get('installation_id')==self.p.identity() and request.get('decision')=='finish','Interrupted backup requires explicit finish and identity')
                require(load(self.p.backups/'operation.json').get('kind')=='backup','Restore recovery requires its deployment journal')
                result=self.backup_command('recover','--data-dir',self.p.live,'--decision','finish','--confirm')
                if self.service_state()=='running':self.ready()
                self.schedule();return {'result':'backup_recovered'}
            require(request.get('confirm') is True and request.get('decision')=='finish' and ID.fullmatch(request.get('operation_id','')),'Explicit pending operation identity required')
            if not (self.p.etc/'identity.json').exists():
                require(ID.fullmatch(request.get('installation_id','')) and not self.p.config.exists() and (not self.p.live.exists() or not list(self.p.live.iterdir())),'Unknown partial installation; inspect before recovery')
                return {'result':'not_started','request_id':request['operation_id']}
            require(request.get('installation_id')==self.p.identity(),'Recovery installation mismatch')
            last=load(self.p.state/'last-deployment.json') if (self.p.state/'last-deployment.json').exists() else {}
            self.ready()
            return {'result':'already_committed' if request['operation_id']==last.get('id') else 'not_started','request_id':request['operation_id']}
        j=load(self.p.journal);
        if request.get('operation_id'):require(request['operation_id']==j['id'],'Another deployment is pending')
        require(request.get('installation_id')==j.get('installation_id') and request.get('confirm') is True,'Recovery confirmation and identity required')
        decision=request.get('decision');require(decision in ('finish','rollback'),'Explicit finish or rollback required')
        if j.get('kind')=='initialize' and j['phase']=='initializing':
            require(decision=='finish','Unstarted initialization can only be finished; partial data are preserved')
            return self.finish_initialization(j)
        require(j['installation_id']==self.p.identity(),'Journal identity mismatch')
        if j['phase'] in ('opening','committed'):
            require(decision=='finish','Writes may have resumed: automatic data rollback forbidden')
            self.ready();self.proxy('open');self.save_journal(j,'committed')
            return self.finish_commit(j)
        if decision=='finish' and j.get('kind')=='restore' and j['phase'] not in ('trial','rollback-trial'):
            source=Path(j['archive']);require(source.parent==self.p.state and source.name=='restore-source-'+j['id']+'.tar.gz' and sha(source)==j['archive_sha256'],'Recovery archive changed')
            self.proxy('closed');self.system('stop',self.p.unit)
            if (self.p.backups/'operation.json').exists():self.backup_command('recover','--data-dir',self.p.live,'--decision','finish','--confirm')
            if not j.get('backup_id'):
                try:j['backup_id']=self.backup('pre-update',True,False)['manifest']['backup_id']
                except Refused:require(j.get('allow_corrupt_live') is True,'Fresh live backup is required before recovery restore')
                self.save_journal(j,'restoring')
            result=self.backup_command('restore','--archive',source,'--data-dir',self.p.live,'--scope',j['scope'],'--confirm');self.seal_previous(result)
            self.own_live();self.save_journal(j,'trial');self.controller('start',self.p.identity());self.proxy('trial')
            return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version']}
        if decision=='finish':
            require(j['phase'] in ('trial','rollback-trial'),'Incomplete data preparation; use explicit rollback')
            self.controller('start',self.p.identity());self.proxy('trial')
            return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version']}
        self.proxy('closed');self.system('stop',self.p.unit)
        old=Path(j['old']);require(old.parent==self.p.opt/'releases','Invalid old release path')
        op=self.p.backups/'operation.json'
        if op.exists():self.backup_command('recover','--data-dir',self.p.live,'--decision','rollback','--confirm',binary=old/'budget')
        if j.get('backup_id'):
            result=self.backup_command('restore','--archive',self.archive(j['backup_id']),'--data-dir',self.p.live,'--confirm',binary=old/'budget');self.seal_previous(result)
        else:require(j['phase'] in ('closing','backup'),'Missing rollback backup')
        pointer(self.p.opt/'current',old)
        if 'previous' in j:
            if j['previous']:pointer(self.p.opt/'previous',Path(j['previous']))
            elif (self.p.opt/'previous').is_symlink():(self.p.opt/'previous').unlink();sync_dir(self.p.opt)
        self.own_live();j['new']=str(old);j['version']=self.manifest(old)
        self.save_journal(j,'rollback-trial');self.controller('start',self.p.identity());self.proxy('trial')
        return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version']}
    def prune_releases(self):
        require(not self.p.journal.exists() or load(self.p.journal)['phase']=='committed','Deployment is unfinished')
        keep={self.p.release()}
        if (self.p.opt/'previous').exists():keep.add(self.p.release('previous'))
        for p in (self.p.opt/'releases').iterdir():
            if p in keep:continue
            require(p.is_dir() and not p.is_symlink(),'Unknown release entry')
            self.manifest(p)
            require({x.name for x in p.iterdir()}=={'budget','release.json'},'Unknown release files')
            shutil.rmtree(p)
        sync_dir(self.p.opt/'releases')
    def restore(self, incoming, request):
        require(request.get('confirm') is True and request['installation_id']==self.p.identity(),'Restore requires explicit confirmation and matching ID')
        require(not self.p.journal.exists(),'Recover unfinished deployment first')
        source=incoming/'backup.tar.gz';regular(source,8<<30)
        space(self.p.state,3*source.stat().st_size+(128<<20))
        saved=self.p.state/('restore-source-'+request['request_id']+'.tar.gz')
        copy_verified(source,saved,sha(source));source=saved
        verified=json.loads(self.run([self.binary(),'admin','backup','verify','--archive',source],timeout=900).stdout)
        require(verified['installation_id']==self.p.identity(),'Restore archive installation ID mismatch')
        require(request.get('scope','full') in ('full','planner'),'Unsupported restore scope')
        j={'format':1,'id':request['request_id'],'installation_id':self.p.identity(),'old':str(self.p.release()),'new':str(self.p.release()),'version':self.manifest(self.p.release()),'backup_id':None,'kind':'restore','archive':str(source),'archive_sha256':sha(source),'scope':request.get('scope','full'),'allow_corrupt_live':request.get('allow_corrupt_live') is True}
        self.save_journal(j,'closing');self.proxy('closed');self.controller('stop',self.p.identity())
        # Corrupted live DB may not support a fresh backup. Explicit separate acknowledgement
        # retains the previous directory via stage-2 recovery; never deletes it.
        try:j['backup_id']=self.backup('pre-update',True,False)['manifest']['backup_id']
        except Refused:
            require(request.get('allow_corrupt_live') is True,'Live backup failed; inspect and explicitly acknowledge corrupted live data')
        self.save_journal(j,'restoring')
        result=self.backup_command('restore','--archive',source,'--data-dir',self.p.live,'--scope',request.get('scope','full'),'--confirm')
        self.seal_previous(result)
        self.own_live();self.save_journal(j,'trial');self.controller('start',self.p.identity());self.proxy('trial')
        return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version']}
    def rollback(self,request):
        require(request.get('confirm') is True and request.get('installation_id')==self.p.identity(),'Rollback requires data-loss confirmation and matching identity')
        last=load(self.p.state/'rollback-pair.json')
        require(last.get('backup_id') and Path(last['old'])==self.p.release('previous'),'No retained compatible rollback pair')
        # This is an explicitly requested restoration, never an automatic post-commit rollback.
        require(not self.p.journal.exists(),'Recover unfinished operation first')
        old=self.p.release('previous');target=self.archive(last['backup_id']);self.preflight_restore(old,target)
        j={'format':1,'id':request['request_id'],'installation_id':self.p.identity(),'old':str(self.p.release()),'new':str(old),'version':self.manifest(old),'backup_id':None,'kind':'explicit-rollback'}
        self.save_journal(j,'closing');self.proxy('closed');self.controller('stop',self.p.identity())
        j['backup_id']=self.backup('pre-update',True,False)['manifest']['backup_id'];self.save_journal(j,'restoring')
        result=self.backup_command('restore','--archive',target,'--data-dir',self.p.live,'--confirm',binary=old/'budget');self.seal_previous(result)
        self.own_live();pointer(self.p.opt/'previous',Path(j['old']));pointer(self.p.opt/'current',old)
        self.save_journal(j,'rollback-trial');self.controller('start',self.p.identity());self.proxy('trial')
        return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version']}
    def initialize(self,incoming,request):
        require(request.get('confirm') is True and ID.fullmatch(request.get('installation_id','')),'Explicit new-installation identity required')
        if self.p.journal.exists():
            j=load(self.p.journal)
            require(j.get('kind')=='initialize' and j['installation_id']==request['installation_id'],'Another installation operation exists')
            return self.finish_initialization(j)
        require(not (self.p.etc/'identity.json').exists(),'Installation already exists; no automatic adoption')
        require(not self.p.live.exists() or not list(self.p.live.iterdir()),'New installation must have empty live directory')
        release,m=self.install_release(incoming)
        archive=incoming/'backup.tar.gz';saved=None
        if archive.exists():
            metadata=json.loads(self.run([release/'budget','admin','backup','verify','--archive',archive],timeout=900).stdout)
            require(metadata['installation_id']==request['installation_id'],'Import installation ID mismatch')
            space(self.p.state,3*archive.stat().st_size+(128<<20))
            saved=self.p.state/('restore-source-'+request['request_id']+'.tar.gz');copy_verified(archive,saved,sha(archive))
        else:require(self.p.instance=='acceptance' or request.get('empty_installation') is True,'Production import archive required')
        credential=self.p.etc/'credentials/app-password'
        if not credential.exists():
            password=getpass.getpass('Новый пароль приложения (минимум 12 символов, рекомендуется 16): ')
            require(12<=len(password) and len(password.encode("utf-8"))<=4096 and password==password.strip() and not any(c in password for c in '\r\n\0') and password==getpass.getpass('Повторите пароль: '),'Password mismatch or invalid characters/length')
            atomic(credential,(password+'\n').encode());del password
        require(credential.stat().st_mode&0o077==0,'Unsafe credential permissions')
        identity={'instance':self.p.instance,'installation_id':request['installation_id'],'synthetic':self.p.instance=='acceptance'}
        env={'BUDGET_ENV':'production','BUDGET_ADDR':'127.0.0.1:'+str(self.p.port),'BUDGET_PUBLIC_ORIGIN':'https://'+IP,'BUDGET_ALLOWED_HOSTS':IP,'BUDGET_PROXY_MODE':'loopback-https','BUDGET_MIGRATIONS':'check-only','TZ':'Europe/Moscow','BUDGET_INSTALLATION_ID':identity['installation_id'],'BUDGET_DATA_DIR':str(self.p.live),'BUDGET_BACKUP_DIR':str(self.p.backups),'GOMEMLIMIT':'256MiB','GOMAXPROCS':'1'}
        j={'format':1,'id':request['request_id'],'installation_id':identity['installation_id'],'old':str(release),'new':str(release),'version':m,'backup_id':None,'kind':'initialize','identity':identity,'configuration':env}
        if saved:j.update(archive=str(saved),archive_sha256=sha(saved))
        # Write recovery intent before any installation marker or live data is created.
        self.save_journal(j,'initializing')
        return self.finish_initialization(j)
    def finish_initialization(self,j):
        require(j.get('kind')=='initialize' and j['phase'] in ('initializing','trial'),'Not an unfinished initialization')
        release=Path(j['new']);require(release.parent==self.p.opt/'releases','Invalid initial release path')
        self.manifest(release)
        atomic(self.p.etc/'identity.json',j['identity'])
        atomic(self.p.config,''.join(k+'='+v+'\n' for k,v in j['configuration'].items()).encode())
        if not (self.p.opt/'current').exists():pointer(self.p.opt/'current',release)
        if (self.p.backups/'operation.json').exists():
            self.backup_command('recover','--data-dir',self.p.live,'--decision','finish','--confirm')
        valid=False
        if (self.p.live/'installation.json').exists():
            self.run([release/'budget','admin','check','--data-dir',self.p.live,'--installation-id',self.p.identity()],timeout=120);valid=True
        if not valid:
            if self.p.live.exists() and list(self.p.live.iterdir()):
                saved=self.p.state/('.live.initialization-failed-'+uuid.uuid4().hex)
                os.rename(self.p.live,saved);sync_dir(self.p.state)
            self.p.live.mkdir(mode=0o700,exist_ok=True)
            if j.get('archive'):
                source=Path(j['archive']);require(source.parent==self.p.state and source.name=='restore-source-'+j['id']+'.tar.gz' and sha(source)==j['archive_sha256'],'Initial archive changed')
                self.backup_command('restore','--archive',source,'--data-dir',self.p.live,'--confirm',binary=release/'budget')
            else:self.run([release/'budget','admin','init','--data-dir',self.p.live,'--installation-id',self.p.identity(),'--confirm'],timeout=120)
        # Imported archives have no planner.db.lock. The first root maintenance
        # backup creates it; assign ownership only after maintenance has closed.
        self.backup('manual',False,False);self.own_live()
        self.save_journal(j,'trial');self.controller('start',self.p.identity());self.proxy('trial')
        return {'result':'awaiting_https_acceptance','request_id':j['id'],'version':j['version'],'installation_id':self.p.identity()}
    def tls_status(self):
        # Obtain the actually served chain without possessing a client private key on VPS.
        p=self.run(['/usr/bin/openssl','s_client','-connect','127.0.0.1:443','-noservername','-showcerts','-verify_ip',IP,'-verify_return_error'],input=b'',check=False,timeout=15)
        require(len(p.stdout)<1024*1024,'Unexpected certificate response')
        blocks=re.findall(rb'-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----',p.stdout,re.S)
        require(blocks,'No serving certificate')
        with tempfile.TemporaryDirectory(prefix='budget-tls-') as tmp:
            leaf=Path(tmp)/'leaf.crt';chain=Path(tmp)/'chain.crt';leaf.write_bytes(blocks[0]+b'\n');chain.write_bytes(b'\n'.join(blocks[1:])+b'\n')
            args=['/usr/bin/openssl','verify','-purpose','sslserver','-verify_ip',IP,'-CApath','/etc/ssl/certs']
            if len(blocks)>1:args+=['-untrusted',chain]
            self.run([*args,leaf]);info=self.cert(leaf)
        info.pop('details',None);info['status']='critical' if info['remaining_hours']<24 else 'warning' if info['remaining_hours']<48 else 'ok'
        info['observed_at']=utc();info['caddy_active']=self.system('is-active','caddy.service',check=False).returncode==0
        state=self.p.global_state/'tls-observation.json';previous=load(state) if state.exists() else {}
        info['last_observed_serial_change']=previous.get('last_observed_serial_change')
        if previous.get('serial') and previous['serial']!=info['serial']:info['last_observed_serial_change']=utc()
        # Serial observation is not falsely labelled a successful automatic renewal.
        events=self.events('caddy.service');info['acme_events']=events
        info['last_successful_issue_or_renewal']=events['certificate_events'][-1] if events['certificate_events'] else None
        atomic(state,info)
        return info
    def events(self,unit):
        p=self.run(['/usr/bin/journalctl','-u',unit,'--since','7 days ago','-n','300','-o','json','--no-pager'],check=False)
        counts={'materializer_errors':0,'internal_errors':0,'acme_errors':0,'certificate_events':[]}
        for line in p.stdout.decode(errors='replace').splitlines():
            try:row=json.loads(line)
            except ValueError:continue
            msg=row.get('MESSAGE','')
            if 'planner materialization failed' in msg:counts['materializer_errors']+=1
            if 'planner internal error' in msg:counts['internal_errors']+=1
            try:event=json.loads(msg)
            except ValueError:continue
            if any(x in event.get('logger','') for x in ('acme','tls.obtain','tls.renew')) and event.get('level')=='error':counts['acme_errors']+=1
            if event.get('msg') in ('certificate obtained successfully','certificate renewed successfully') and (event.get('identifier')==IP or IP in event.get('identifiers',[])):
                counts['certificate_events'].append({'message':event['msg'],'timestamp':event.get('ts')})
        return counts
    def status(self):
        d={'instance':self.p.instance,'ubuntu':read_os_release(self.p.root,owner=0 if self.p.root==Path('/') else os.getuid()).decode(),'uptime_seconds':read(self.p.at('/proc/uptime'),1024).decode().split()[0],'reboot_required':self.p.at('/var/run/reboot-required').exists(),'deployment_journal':self.p.journal.exists(),'restore_journal':(self.p.state/'.live.restore.json').exists(),'caddy_active':self.system('is-active','caddy.service',check=False).returncode==0}
        d['resources']={'memory':read(self.p.at('/proc/meminfo'),16384).decode(),'load':read(self.p.at('/proc/loadavg'),1024).decode()}
        st=os.statvfs(self.p.state);d['disk']={'free_bytes':st.f_bavail*st.f_frsize,'total_bytes':st.f_blocks*st.f_frsize,'free_inodes':st.f_favail}
        def observe(key,callback):
            try:d[key]=callback()
            except (Refused,OSError,ValueError,KeyError):d[key]={'status':'unavailable','error':'Check the service/configuration or pending operation; observation failed'}
        if self.p.config.exists():
            d['installation_id']=self.p.identity()
            observe('service',self.service_state);observe('release',lambda:self.manifest(self.p.release()))
            observe('backup',lambda:self.backup_command('status'));observe('background_events',lambda:self.events(self.p.unit))
            d['sqlite_bytes']={n:(self.p.live/n).stat().st_size for n in ('planner.db','planner.db-wal') if (self.p.live/n).exists()}
            d['backup_counts']={c:len(list((self.p.backups/c).iterdir())) if (self.p.backups/c).exists() else 0 for c in ('daily','pre-update','manual','pins')}
        observe('clients',self.client_list);observe('tls',self.tls_status)
        d['backup_operation_pending']=(self.p.backups/'operation.json').exists()
        d['revocation_pending']=(self.p.global_state/'revocation-pending.json').exists()
        if self.p.journal.exists():
            journal=load(self.p.journal);d['deployment']={k:journal.get(k) for k in ('id','kind','phase','updated_at')}
        if (self.p.etc/'identity.json').exists():d['synthetic']=load(self.p.etc/'identity.json').get('synthetic',False)
        if (self.p.global_state/'proxy.json').exists():
            d['proxy']=load(self.p.global_state/'proxy.json')
            d['proxy']['configuration_matches']=d['proxy'].get('config_sha256')==sha(self.p.at('/etc/caddy/simple-budget.json'))
        return d
    def client_list(self):
        reg=load(self.p.global_state/'clients.json');out=[]
        pending=load(self.p.global_state/'revocation-pending.json') if (self.p.global_state/'revocation-pending.json').exists() else {}
        for name,c in reg['clients'].items():
            cert=self.cert(self.p.at('/etc/caddy/pki/'+name+'.crt'));cert.pop('details',None)
            out.append({'device':name,'state':c['state'],'revocation_pending':pending.get('device')==name,**cert})
        return out
    def client_change(self,incoming,request,revoke=False):
        name=request.get('device','');require(NAME.fullmatch(name),'Invalid device name')
        reg=load(self.p.global_state/'clients.json')
        intent_path=self.p.global_state/'revocation-pending.json'
        intent=load(intent_path) if intent_path.exists() else None
        require(not intent or revoke and intent.get('device')==name and intent.get('fingerprint')==request.get('fingerprint'),'Finish the pending revocation first')
        if revoke:
            require(request.get('confirm') is True,'Revocation confirmation required')
            require(name in reg['clients'] and reg['clients'][name]['fingerprint']==request.get('fingerprint'),'Device fingerprint mismatch')
            require(reg['clients'][name]['state']=='revoked' and intent or sum(c['state']=='allowed' for c in reg['clients'].values())>1,'Cannot revoke the final administrative device through this command')
            atomic(intent_path,{'device':name,'fingerprint':request['fingerprint'],'at':utc()})
            # Close ALL existing TLS/HTTP2 connections BEFORE durably revoking.
            self.system('stop','caddy.service')
            reg['clients'][name]['state']='revoked';reg['clients'][name]['revoked_at']=utc()
        else:
            require(name not in reg['clients'],'Use a new versioned device name; never overwrite an allowed/retired certificate')
            cert=incoming/'client.crt';info=self.cert(cert)
            require(info['remaining_hours']>24 and 'TLS Web Client Authentication' in info['details'] and 'CA:FALSE' in info['details'],'Client certificate purpose/lifetime invalid')
            self.run(['/usr/bin/openssl','verify','-purpose','sslclient','-CAfile',self.p.at('/etc/caddy/pki/ca.crt'),cert])
            require(info['fingerprint'] not in [c['fingerprint'] for c in reg['clients'].values()],'Certificate fingerprint already registered')
            dest=self.p.at('/etc/caddy/pki/'+name+'.crt');copy_verified(cert,dest,mode=0o640,limit=64<<10)
            if self.p.root==Path('/'):os.chown(dest,0,grp.getgrnam('caddy').gr_gid)
            reg['clients'][name]={'fingerprint':info['fingerprint'],'state':'allowed','added_at':utc()}
        atomic(self.p.global_state/'clients.json',reg)
        proxy=load(self.p.global_state/'proxy.json') if (self.p.global_state/'proxy.json').exists() else {'instance':'acceptance','mode':'test-page'}
        a=Admin(Paths(proxy['instance'],self.p.root),self.run)
        try:a.proxy(proxy['mode'])
        except BaseException:self.system('stop','caddy.service',check=False);raise
        if revoke:intent_path.unlink();sync_dir(self.p.global_state)
        return {'device':name,**reg['clients'][name],'connections_terminated':revoke,'resumption':'disabled; verify with device probe'}

REQUEST_ACTIONS={'prepare','commit','initialize','restore','rollback','recover','client-add','client-revoke','fetch-backup','cleanup'}
READ_ACTIONS={'access-stop','clean-incoming','status','logs','backup','daily','schedule','tls-status','client-list','startup-guard','proxy-guard','pki-export','secure-page'}

def request_for(token,action,instance):
    require(ID.fullmatch(token or ''),'Expected request token')
    incoming=Path('/home/budgetadmin/.simple-budget-incoming')/token
    uid=pwd.getpwnam(ADMIN).pw_uid
    require(incoming.is_dir() and not incoming.is_symlink() and incoming.stat().st_uid==uid and incoming.stat().st_mode&0o077==0,'Unsafe incoming directory')
    request=load(incoming/'request.json',16384,uid)
    allowed={'action','instance','request_id','installation_id','confirm','empty_installation','https_verified_revision','decision','scope','allow_corrupt_live','device','fingerprint','backup_id','kind','name','operation_id','operation'}
    require(set(request)<=allowed and request.get('action')==action and request.get('instance')==instance and request.get('request_id')==token,'Request operation mismatch')
    return incoming,request

def controller_main():
    try:
        require(os.geteuid()==0 and len(sys.argv)==3,'Controller requires root and two arguments')
        action,ident=sys.argv[1:];require(action in ('status','stopped','stop','start') and ID.fullmatch(ident),'Invalid controller operation')
        found=[]
        for instance in ('production','acceptance'):
            p=Paths(instance)
            if (p.etc/'identity.json').exists() and p.identity()==ident:found.append(p)
        require(len(found)==1,'Controller cannot select unique installation')
        # Deliberately no outer ops lock: caller owns it; nested locks would deadlock.
        Admin(found[0]).controller(action,ident)
    except (Refused,OSError,ValueError,KeyError):sys.exit(1)

def main():
    try:
        if sys.argv[1:]==['session-protocol']:
            print('1');return
        if len(sys.argv)==4 and sys.argv[1]=='session':
            require(os.geteuid()==0 and platform.system()=='Linux','Session requires Linux root')
            from admin_session import serve
            serve(sys.argv[2],sys.argv[3]);return
        require(os.geteuid()==0 and platform.system()=='Linux','Administrator requires Linux root')
        require(len(sys.argv) in (3,4),'usage: budget-admin OP production|acceptance [REQUEST_TOKEN]')
        action,instance=sys.argv[1:3];require(action in REQUEST_ACTIONS|READ_ACTIONS,'Unknown operation')
        p=Paths(instance);a=Admin(p)
        if action=='startup-guard':a.startup_guard();return
        if action=='proxy-guard':a.proxy_guard();return
        incoming=request=None
        if action in REQUEST_ACTIONS or len(sys.argv)==4:
            require(len(sys.argv)==4,'Request token required');incoming,request=request_for(sys.argv[3],action,instance)
        else:require(len(sys.argv)==3,'Unexpected arguments')
        with lock(p.global_state/'operations.lock'):
            if action=='prepare':result=a.prepare(incoming,request)
            elif action=='commit':result=a.commit(request)
            elif action=='initialize':result=a.initialize(incoming,request)
            elif action=='restore':result=a.restore(incoming,request)
            elif action=='rollback':result=a.rollback(request)
            elif action=='recover':result=a.recover(request)
            elif action in ('backup','daily'):
                require(not p.journal.exists(),'Deployment unfinished')
                result=a.backup('daily' if action=='daily' else 'manual');a.schedule()
            elif action=='schedule':a.schedule();result={'result':'observed'}
            elif action=='status':result=a.status()
            elif action=='logs':result={'application':a.events(p.unit),'caddy':a.events('caddy.service')}
            elif action=='tls-status':result=a.tls_status()
            elif action=='client-list':result=a.client_list()
            elif action in ('client-add','client-revoke'):result=a.client_change(incoming,request,action=='client-revoke')
            elif action=='access-stop':a.system('stop','caddy.service');result={'result':'access_closed'}
            elif action=='secure-page':a.proxy('test-page');result={'result':'protected_test_page'}
            elif action=='pki-export':
                require(not (p.global_state/'revocation-pending.json').exists(),'Finish pending revocation before exporting registry')
                result=load(p.global_state/'clients.json')
            elif action=='fetch-backup':
                path=a.archive(request['backup_id']);a.run([a.binary(),'admin','backup','verify','--archive',path],timeout=900)
                dest=incoming/'download.tar.gz';copy_verified(path,dest,mode=0o600)
                os.chown(dest,pwd.getpwnam(ADMIN).pw_uid,pwd.getpwnam(ADMIN).pw_gid)
                result={'file':'download.tar.gz','sha256':sha(dest),'size':dest.stat().st_size}
            elif action=='cleanup':result=cleanup(a,request)
            elif action=='clean-incoming':
                require(incoming is not None,'Request required')
                names={'budget','release.json','backup.tar.gz','client.crt','request.json','result.json','download.tar.gz'}
                for child in incoming.iterdir():require(child.name in names,'Unknown incoming file');regular(child)
                for child in incoming.iterdir():child.unlink()
                incoming.rmdir();incoming=None;result={'result':'removed'}
            else:raise Refused('Unsupported operation')
        if incoming is not None:
            atomic(incoming/'result.json',result)
            os.chown(incoming/'result.json',pwd.getpwnam(ADMIN).pw_uid,pwd.getpwnam(ADMIN).pw_gid)
        print(json.dumps(result,ensure_ascii=False))
        if action=='tls-status' and result['status']!='ok':sys.exit(2)
    except (Refused,OSError,ValueError,KeyError,TypeError) as e:
        # Do not print subprocess stderr, credentials, request bodies or stack locals.
        print('Administrative operation refused: '+str(e),file=sys.stderr);sys.exit(1)

def cleanup(a,request):
    p=a.p;require(request.get('confirm') is True and request.get('installation_id')==p.identity(),'Cleanup confirmation/identity required')
    require(not p.journal.exists() and not (p.backups/'operation.json').exists() and not (p.state/'.live.restore.json').exists(),'Pending operation protects data')
    last=load(p.state/'last-deployment.json') if (p.state/'last-deployment.json').exists() else {}
    if request.get('kind')=='backup':
        ident=request.get('backup_id');require(BACKUP_ID.fullmatch(ident or ''),'Invalid backup ID');pair=load(p.state/'rollback-pair.json') if (p.state/'rollback-pair.json').exists() else {}
        require(ident not in (last.get('backup_id'),pair.get('backup_id')),'Backup required by retained rollback pair')
        if (p.backups/'pins'/(ident+'.json')).exists():a.backup_command('unprotect','--backup-id',ident,'--confirm')
        return a.backup_command('delete','--backup-id',ident,'--confirm')
    require(request.get('kind')=='previous-data','Unknown cleanup category')
    name=request.get('name','');require(re.fullmatch(r'\.live\.previous-[a-zA-Z0-9-]+',name),'Invalid previous-data name')
    target=p.state/name;require(target.is_dir() and not target.is_symlink(),'Previous data absent')
    require(a.service_state()=='stopped','Previous-data cleanup requires explicit maintenance stop')
    require(load(target/'installation.json')['installation_id']==p.identity(),'Previous-data identity mismatch')
    a.run([a.binary(),'admin','backup','verify','--archive',a.archive(a.backup_command('status')['latest']['manifest']['backup_id'])],timeout=900)
    allowed={'installation.json','planner.db','planner.db-wal','planner.db-shm','planner.db.lock','data.json','categories.json','web-auth.json'}
    for child in target.iterdir():require(child.name in allowed,'Unknown previous-data file');regular(child)
    with lock(target/'planner.db.lock'):
        shutil.rmtree(target);sync_dir(p.state)
    return {'result':'previous_data_removed','name':name}

if __name__=='__main__':main()
