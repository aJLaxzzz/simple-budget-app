#!/usr/bin/env python3
"""Mac entrypoint. No network operation runs without explicit ENV=production."""
import argparse
import base64
import getpass
import http.client
import json
import os
import re
from pathlib import Path
import shlex
import ssl
import subprocess
import sys
import tempfile
import uuid
from common import *
import secret_store
import local_backups
import version_status
import terminal_ui as ui

ROOT=Path(__file__).resolve().parents[2]
LOCAL=Path.home()/'.simple-budget-admin'
ACTIVE_TRANSPORTS=[]

class Transport:
    def __init__(self):
        self.session=None;self.operation=None;self.instance=None
        ACTIVE_TRANSPORTS.append(self)
        require(os.environ.get('ENV')=='production','Select the server explicitly: ENV=production')
        raw=subprocess.check_output(['/usr/bin/ssh','-G','budget'],text=True)
        config=dict(line.split(' ',1) for line in raw.splitlines() if ' ' in line)
        require(config.get('hostname')==IP and config.get('user')==ADMIN,'SSH alias budget must resolve to budgetadmin@'+IP)
        require(config.get('stricthostkeychecking') not in ('false','no','off'),'SSH config must not disable host-key verification')
        self.ssh=['/usr/bin/ssh','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3']
        self.scp=['/usr/bin/scp','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=15']
        if not ui.verbose():
            self.ssh+=['-o','LogLevel=ERROR'];self.scp+=['-q']
    def close(self):
        if self.session:self.session.close()
    def command(self,args,tty=False):
        return subprocess.run([*self.ssh,*(['-t'] if tty else []),'budget',shlex.join([str(x) for x in args])],check=True)
    def upload(self,path,token,name):
        require(ID.fullmatch(token) and re.fullmatch('[a-zA-Z0-9_.-]+',name),'Unsafe transfer name')
        scp=self.scp
        if name in ('budget','backup.tar.gz'):
            ui.stage('Загрузка обновления' if name=='budget' else 'Загрузка проверенного архива')
            if ui.interactive() and os.environ.get('OUTPUT')!='json':scp=[x for x in scp if x!='-q']
        subprocess.run([*scp,str(path),'budget:.simple-budget-incoming/'+token+'/'+name],check=True)
    def download(self,token,name,target):
        require(ID.fullmatch(token) and re.fullmatch('[a-zA-Z0-9_.-]+',name),'Unsafe transfer name')
        require(not Path(target).exists(),'Refusing overwrite of local file')
        scp=self.scp
        if name.endswith('.tar.gz') and ui.interactive() and os.environ.get('OUTPUT')!='json':scp=[x for x in scp if x!='-q']
        subprocess.run([*scp,'budget:.simple-budget-incoming/'+token+'/'+name,str(target)],check=True)
        os.chmod(target,0o600)
    def request(self,action,instance,fields=None,files=None,token=None):
        token=token or uuid.uuid4().hex
        require(ID.fullmatch(token),'Invalid operation token')
        # Fixed user-level directory commands; no remote root shell or arbitrary root path.
        self.command(['mkdir','-p','-m','700','.simple-budget-incoming/'+token])
        req={'action':action,'instance':instance,'request_id':token,'quiet':not ui.verbose(),**(fields or {})}
        with tempfile.TemporaryDirectory(prefix='budget-ops-') as tmp:
            f=Path(tmp)/'request.json';atomic(f,req);self.upload(f,token,'request.json')
            for name,path in (files or {}).items():self.upload(path,token,name)
            if action!='clean-incoming':ui.stage('Поиск последней резервной копии' if action=='status' and self.operation=='fetch-backup' else ui.STAGES.get(action,'Операция: '+action))
            elif ui.verbose():ui.stage('Очистка служебных файлов')
            else:ui.LAST_STAGE='Очистка служебных файлов'
            if action=='repair-initialization':
                require(instance=='acceptance','Repair is synthetic-only')
                self.command(['sudo','/usr/bin/python3','-I','/home/budgetadmin/.simple-budget-incoming/'+token+'/repair.py',token],tty=True)
            else:
                mode=(os.environ.get('SUDO_MODE') or 'legacy')
                require(mode in ('legacy','session'),'SUDO_MODE must be legacy or session')
                if mode=='session':
                    from session_transport import Session
                    if self.session is None:
                        state=load(local_state(instance))
                        self.session=Session(self,self.operation or action,instance,state['installation_id'])
                    self.session.execute(action,token)
                else:self.command(['sudo','/usr/local/sbin/budget-admin',action,instance,token],tty=True)
            if action=='clean-incoming':return {'result':'removed'}
            result=Path(tmp)/'result.json';self.download(token,'result.json',result)
            return load(result),token

def clean_tree():
    require(subprocess.check_output(['git','status','--porcelain','--untracked-files=normal'],cwd=ROOT)==b'',
            'Production requires a clean committed tree. Commit/push is never automatic.')

def release():
    ui.stage('Проверка версии и чистоты рабочего дерева')
    clean_tree()
    ui.stage('Сборка Linux-релиза')
    p=subprocess.run(['/bin/bash',str(ROOT/'scripts/build-linux.sh')],cwd=ROOT,check=True,stdout=subprocess.PIPE,text=True)
    print(p.stdout,end='');line=next(x for x in p.stdout.splitlines() if x.startswith('Linux release: '))
    path=ROOT/line.split(': ',1)[1];m=load(path/'release.json');require(m['modified'] is False,'Dirty release refused')
    require(sha(path/'budget')==m['files'][0]['sha256'],'Release checksum failed');return path,m

def local_state(instance):
    LOCAL.mkdir(mode=0o700,exist_ok=True);require(LOCAL.stat().st_mode&0o077==0 and not LOCAL.is_symlink(),'Private local administration directory required')
    return LOCAL/(instance+'.json')

def client_context(device='mac-1'):
    require(NAME.fullmatch(device),'Invalid device')
    pki=Path.home()/'.simple-budget-pki'
    for name in (device+'.crt',device+'.key'):
        regular(pki/name,64<<10)
    require((pki/(device+'.key')).stat().st_mode&0o077==0,'Client key must be private')
    ca='/etc/ssl/cert.pem' if Path('/etc/ssl/cert.pem').exists() else None
    manual=secret_store.SESSION is None or secret_store.SESSION.mode=='manual'
    for attempt in range(1,4):
        context=ssl.create_default_context(cafile=ca);context.minimum_version=ssl.TLSVersion.TLSv1_2
        prompt='Пароль закрытого ключа '+device+(' (попытка '+str(attempt)+' из 3)' if attempt>1 else '')+': '
        try:
            context.load_cert_chain(pki/(device+'.crt'),pki/(device+'.key'),password=lambda:secret_store.password('client',prompt,device))
            return context
        except ssl.SSLError as e:
            reason=(getattr(e,'reason',None) or '').upper().replace(' ','_')
            # Some Python/OpenSSL builds expose this error only as text (reason=None).
            # Match that specific load_cert_chain failure, never arbitrary TLS errors.
            pem_text=re.fullmatch(r'\[SSL\] PEM lib(?: \(_ssl\.c:\d+\))?',str(e))
            if reason not in ('PEM_LIB','BAD_DECRYPT') and not (not reason and pem_text):raise
            if not manual or attempt==3:
                raise Refused('Не удалось открыть клиентский ключ '+device+'. Проверьте его пароль и файл ключа. Если операция уже началась, продолжите через status/recover.') from None
            print('Не удалось открыть клиентский ключ. Повторите именно пароль ключа '+device+' (не sudo и не пароль приложения).',file=sys.stderr)

def app_connection(device='mac-1', password=None):
    return client_context(device),password or secret_store.password('app','Пароль приложения: ')

def https_accept(expected,device,instance='production'):
    ui.stage('Проверка HTTPS и готовности приложения')
    context,password=app_connection(device)
    try:
        for attempt in range(1,4):
            # Retry only the read-only proof, never the administrative operation.
            # Reuse the unlocked TLS context; each HTTP connection is closed.
            require(instance in ('production','acceptance'),'Invalid HTTPS target')
            port=8443 if instance=='acceptance' else 443
            origin='https://'+IP+(':8443' if port==8443 else '')
            c=http.client.HTTPSConnection(IP,port,context=context,timeout=30)
            try:
                c.request('GET','/api/ready',headers={'X-Budget-Admin':'1','Authorization':'Basic '+base64.b64encode(('budget:'+password).encode()).decode(),'Origin':origin})
                r=c.getresponse()
                if r.status==200:
                    data=json.loads(r.read(16384))
                    require(data['revision']==expected['revision'] and data['schema_version']==expected['schema_version'] and data['modified'] is False,'HTTPS release/schema mismatch')
                    return expected['revision']
                reason={401:'application authentication rejected; check the application password (not the SSH or client-key password)',
                        403:'Host/Origin or access policy rejected the request',
                        429:'authentication attempts are rate limited',
                        502:'proxy could not reach the application',
                        503:'application is not ready or access remains in maintenance'}.get(r.status,'unexpected HTTP response')
                if r.status!=401 or attempt==3:
                    raise Refused('Protected HTTPS readiness failed: HTTP '+str(r.status)+'; '+reason)
            finally:c.close()
            print('Пароль приложения не принят (HTTP 401). Операция сохранена; повторите только пароль приложения.',file=sys.stderr)
            password=getpass.getpass('Пароль приложения (попытка '+str(attempt+1)+' из 3): ')
    finally:del password



def acceptance(t,instance,device):
    require(instance=='acceptance','Synthetic checks must never target production')
    state=load(local_state(instance));status,_=t.request('status',instance)
    require(status.get('synthetic') is True and status['installation_id']==state['installation_id'],'Synthetic installation identity required')
    proxy=status.get('proxy',{})
    require(proxy.get('instance')=='acceptance' and proxy.get('mode')=='open' and proxy.get('configuration_matches') is True,'Public route must select the synthetic installation')
    context,password=app_connection(device)
    auth='Basic '+base64.b64encode(('budget:'+password).encode()).decode()
    def request(method,path,body=None,headers=None,ctx=context):
        c=http.client.HTTPSConnection(IP,8443,context=ctx,timeout=30)
        try:
            h={'Host':IP+':8443','X-Budget-Admin':'1','Authorization':auth,'Origin':'https://'+IP+':8443','Content-Type':'application/json'};h.update(headers or {})
            c.request(method,path,json.dumps(body).encode() if body is not None else None,h)
            r=c.getresponse();data=r.read(2<<20);return r.status,data
        finally:c.close()
    code,data=request('GET','/api/ready');require(code==200,'HTTPS readiness failed')
    require(request('GET','/api/ready',headers={'X-Budget-Admin':'1','Authorization':'Basic aW52YWxpZDppbnZhbGlk'})[0]==401,'App password bypass')
    require(request('POST','/api/planner/tasks',{'title':'must-not-exist'},headers={'Origin':'https://foreign.invalid'})[0]==403,'Foreign Origin accepted')
    require(request('GET','/api/ready',headers={'Host':'foreign.invalid','X-Forwarded-Host':IP})[0] in (403,404,421),'Foreign Host accepted')
    no_cert=ssl.create_default_context(cafile='/etc/ssl/cert.pem' if Path('/etc/ssl/cert.pem').exists() else None)
    try:
        request('GET','/api/ready',ctx=no_cert)
    except (ssl.SSLError,ConnectionResetError):pass
    else:raise Refused('Server accepted a request without a client certificate')
    code,data=request('POST','/api/planner/projects',{'title':'Synthetic VPS acceptance '+uuid.uuid4().hex[:8]});require(code in (200,201),'Synthetic project creation failed')
    project=json.loads(data)
    code,data=request('POST','/api/planner/tasks',{'title':'Synthetic calendar check','project_id':project['id'],'planned_date':'2026-12-04'});require(code in (200,201),'Synthetic task creation failed')
    task=json.loads(data);require(request('GET','/api/planner/tasks/'+task['id'])[0]==200,'Synthetic task read failed')
    backup,_=t.request('backup',instance);ident=backup['manifest']['backup_id']
    code,_=request('GET','/api/backups/status');require(code==200,'Backup metadata failed')
    destination=download_verified_backup(context,auth,ident,state['installation_id'])
    del password
    atomic(LOCAL/'acceptance-result.json',{'at':utc(),'installation_id':state['installation_id'],'project_id':project['id'],'task_id':task['id'],'archive':str(destination),'checks':['https-mtls','app-password','host-origin','synthetic-get-post','backup-stop-start','http-download']})
    print('Synthetic HTTPS checks passed. Linux reboot/rollback, Android and automatic certificate renewal remain separate acceptance gates.')

def require_download_response(response):
    # Caddy and the upstream can each emit no-store. HTTPResponse.getheader
    # combines these fields as "no-store, no-store"; retain the same policy,
    # allowing repetitions/case/OWS only, never a missing or weaker directive.
    raw=response.getheader('Cache-Control') or ''
    directives=[x.strip().lower() for x in raw.split(',')]
    require(response.status==200 and directives and all(x=='no-store' for x in directives),
            'Protected download failed: HTTP '+str(response.status)+'; Cache-Control='+repr(raw[:160]))
    require((response.getheader('Content-Disposition') or '').split(';',1)[0].strip().lower()=='attachment',
            'Backup response must be an attachment')


def download_verified_backup(context,auth,ident,installation_id):
    require(BACKUP_ID.fullmatch(ident or ''),'Invalid backup ID')
    require(ID.fullmatch(installation_id or ''),'Invalid installation ID')
    destination=LOCAL/('acceptance-'+ident+'.tar.gz')
    partial=destination.with_suffix(destination.suffix+'.partial')
    require(not destination.exists() and not partial.exists(),'Download file already exists; verify it before retrying')
    c=http.client.HTTPSConnection(IP,8443,context=context,timeout=90)
    created=False
    try:
        c.request('GET','/api/backups/'+ident+'/full',headers={'X-Budget-Admin':'1','Authorization':auth,'Host':IP+':8443'})
        response=c.getresponse();require_download_response(response)
        with open(partial,'xb') as f:
            created=True;os.chmod(partial,0o600)
            while True:
                chunk=response.read(1024*1024)
                if not chunk:break
                f.write(chunk)
            f.flush();os.fsync(f.fileno())
        metadata=json.loads(subprocess.check_output([str(local_binary()),'admin','backup','verify','--archive',str(partial)]))
        require(metadata.get('installation_id')==installation_id and metadata.get('backup_id')==ident,'Downloaded archive identity mismatch')
        # Publish without overwriting another download; unlink the temporary
        # name immediately, leaving a normal single-link verified archive.
        os.link(partial,destination);partial.unlink();created=False
        atomic(LOCAL/('download-'+ident+'.json'),{'at':utc(),'installation_id':installation_id,'backup_id':ident,'archive':str(destination),'size':destination.stat().st_size,'sha256':sha(destination),'checks':['https-mtls','authenticated-download','no-store','attachment','archive-integrity','installation-id','backup-id']})
        print('Verified HTTPS archive:',destination)
        return destination
    finally:
        c.close()
        if created:partial.unlink(missing_ok=True)


def acceptance_download(instance,device,ident):
    require(instance=='acceptance','Download retry targets the synthetic installation only')
    state=load(local_state(instance));require(state.get('instance')=='acceptance','Synthetic local identity required')
    context,password=app_connection(device)
    auth='Basic '+base64.b64encode(('budget:'+password).encode()).decode()
    try:download_verified_backup(context,auth,ident,state['installation_id'])
    finally:del password


class RevocationProbe:
    def __init__(self, context, pending=False):
        import socket
        self.socket=None;self.session=None;self.context=context
        if pending:return
        self.context=context
        self.socket=context.wrap_socket(socket.create_connection((IP,443),timeout=15),server_hostname=IP)
        self.socket.sendall(('GET /api/ready HTTP/1.1\r\nHost: '+IP+'\r\nConnection: keep-alive\r\n\r\n').encode())
        response=http.client.HTTPResponse(self.socket);response.begin();response.read(65536)
        require(response.status in (200,401,503),'Device is not currently accepted')
        self.session=self.socket.session
        require(not self.session.has_ticket,'Unexpected TLS tickets: inspect live Caddy before revocation')
    def verify_revoked(self):
        import socket
        try:
            if self.socket is not None:self.socket.sendall(b'GET / HTTP/1.1\r\nHost: '+IP.encode()+b'\r\n\r\n')
            if self.socket is not None:require(not self.socket.recv(1),'Old authenticated TLS connection survived revocation')
        except (ConnectionResetError,BrokenPipeError):pass
        except OSError as e:raise Refused('Old TLS connection closure was not proven') from e
        finally:
            if self.socket is not None:self.socket.close()
        for session in (None,self.session):
            try:
                with self.context.wrap_socket(socket.create_connection((IP,443),timeout=15),server_hostname=IP,session=session) as s:
                    s.sendall(b'GET / HTTP/1.1\r\nHost: '+IP.encode()+b'\r\n\r\n')
                    data=s.recv(1024)
                    raise Refused('Revoked certificate was accepted' if data else 'TLS peer closed without a certificate rejection alert')
            except ssl.SSLError as e:
                require(re.search(r'ALERT_(BAD_CERTIFICATE|UNKNOWN_CA|CERTIFICATE_UNKNOWN|CERTIFICATE_REQUIRED|ACCESS_DENIED)$',str(getattr(e,'reason',''))),
                        'TLS failure did not prove revoked-certificate rejection')
                continue
            # A TCP reset/timeout/refusal does not prove correct mTLS revocation.
            except OSError as e:raise Refused('Could not verify revoked-device TLS refusal') from e

def install(t):
    clean_tree()
    with tempfile.TemporaryDirectory(prefix='budget-infrastructure-') as tmp:
        bundle=Path(tmp);manifest={}
        names=['common.py','remote.py','bootstrap.py','caddy_policy.py','admin_session.py']
        for sub,paths in [('ops',[ROOT/'scripts/ops'/x for x in names]),('systemd',list((ROOT/'deploy/systemd').iterdir()))]:
            for source in paths:
                name=sub+'/'+source.name;manifest[name]=sha(source)
        token=uuid.uuid4().hex;t.command(['mkdir','-p','-m','700','.simple-budget-incoming/'+token+'/ops','.simple-budget-incoming/'+token+'/systemd'])
        for name in manifest:
            source=ROOT/('scripts/'+name if name.startswith('ops/') else 'deploy/'+name)
            # Only fixed allowlisted source names from our clean repository are transferred.
            subprocess.run([*t.scp,str(source),'budget:.simple-budget-incoming/'+token+'/'+name],check=True)
        atomic(bundle/'bundle.json',manifest);t.upload(bundle/'bundle.json',token,'bundle.json')
        t.command(['sudo','/usr/bin/python3','/home/budgetadmin/.simple-budget-incoming/'+token+'/ops/bootstrap.py'],tty=True)

def confirm(label):
    require(input(label+'\nВведите ПОДТВЕРЖДАЮ: ')=='ПОДТВЕРЖДАЮ','Cancelled')

def _main(argv=None):
    parser=argparse.ArgumentParser()
    parser.add_argument('action');parser.add_argument('--instance',default=os.environ.get('INSTANCE') or 'acceptance',choices=['acceptance','production'])
    parser.add_argument('--archive',default=os.environ.get('ARCHIVE') or None);parser.add_argument('--device',default=os.environ.get('DEVICE') or 'mac-1')
    parser.add_argument('--backup-id',default=os.environ.get('BACKUP_ID') or None);parser.add_argument('--decision',default=os.environ.get('DECISION') or None)
    parser.add_argument('--source',default=os.environ.get('LOCAL_DATA') or None);parser.add_argument('--name',default=os.environ.get('NAME') or None)
    args=parser.parse_args(argv);action=args.action
    state_path=LOCAL/(args.instance+'.json')
    identity=load(state_path).get('installation_id') if state_path.exists() else None
    secret_store.SESSION=secret_store.Operation(args.instance,args.device,(os.environ.get('AUTH') or 'manual'),identity)
    if action.startswith('keychain-'):
        secret_store.manage(action,args.instance,args.device,(os.environ.get('SECRET') or 'app'),identity);return
    if secret_store.SESSION.mode=='keychain':
        # Resolve local authorization before a deployment can enter maintenance.
        if action in ('deploy','restore','rollback','recover','acceptance','acceptance-download','acceptance-http'):
            secret_store.password('client','Пароль закрытого ключа: ',args.device)
            secret_store.password('app','Пароль приложения: ')
        elif action=='client-revoke':secret_store.password('client','Пароль закрытого ключа: ',args.device)
        elif action in ('initialize','migrate-data','acceptance-tls'):
            raise Refused('New installation/test identity requires explicit AUTH=manual; register secrets afterwards')
    if action=='release-linux':release();return
    if action in ('client-issue','client-renew','pki-backup'):
        import pki; pki.execute(action,args.device,args.name);return
    ui.stage('Подключение к серверу') if action!='overview' else None
    t=Transport();t.operation=action;t.instance=args.instance
    if action=='overview':
        p=subprocess.run([*t.ssh,'budget','/usr/local/sbin/budget-admin overview'],capture_output=True,text=True,timeout=25)
        if p.returncode and p.stderr:print(p.stderr.strip(),file=sys.stderr)
        require(p.returncode==0,'Не удалось получить состояния окружений по SSH (exit '+str(p.returncode)+')')
        print(json.dumps(json.loads(p.stdout)));return
    if action not in ('server-install','repair-initialization'):
        from session_transport import check_protocol
        check_protocol(t) # before creating a pending operation or uploading any mutation request
    if action=='acceptance-http':
        import http_acceptance
        http_acceptance.execute(t,args.instance,args.device);return
    if action=='acceptance-tls':
        import tls_acceptance
        tls_acceptance.execute(t,args.instance,args.device);return
    if action=='server-install':install(t);return
    if action=='acceptance':acceptance(t,args.instance,args.device);return
    if action=='acceptance-download':acceptance_download(args.instance,args.device,args.backup_id);return
    statefile=local_state(args.instance)
    state=load(statefile) if statefile.exists() else {}
    pending_file=LOCAL/('pending-'+args.instance+'.json')
    pending=load(pending_file) if pending_file.exists() else {}
    if pending and action in ('stg-setup','stg-start','stg-stop','deploy','promote','stg-reset','initialize','migrate-data','restore','rollback'):
        raise Refused('Reconcile the previous operation with status and recover before another change')
    fields={};files={};token=None;probe=None
    if action in ('stg-setup','stg-start','stg-stop','stg-reset'):
        require(args.instance=='acceptance','Выберите STG: INSTANCE=acceptance')
        if action=='stg-setup':
            require(args.decision in (None,'','finish','rollback'),'DECISION должен быть finish или rollback')
            confirm(('Восстановить прежнюю конфигурацию Caddy и остановить STG' if args.decision=='rollback' else 'Настроить STG :8443 (Caddy и firewall)')+', ID '+str(state.get('installation_id'))+'? PROD останется на :443.')
        if action=='stg-reset':confirm('Сбросить все синтетические данные STG? ID '+str(state.get('installation_id'))+'. Будет создан новый демонстрационный набор; PROD не меняется.')
        fields={'installation_id':state.get('installation_id'),'confirm':True}
        if action=='stg-setup':fields['decision']=args.decision or 'finish'
    if action=='promote':
        require(args.instance=='production','Продвижение допускается только в PROD')
        p=subprocess.run([*t.ssh,'budget','/usr/local/sbin/budget-admin overview'],capture_output=True,text=True,timeout=25)
        require(p.returncode==0,'Не удалось прочитать релиз STG')
        stg=json.loads(p.stdout)['acceptance'];digest=stg.get('sha256','')
        require(stg.get('service')=='running' and SHA.fullmatch(digest),'STG должен работать с проверенным релизом')
        confirm('Установить в PROD ровно релиз STG '+str(stg.get('revision'))+' SHA-256 '+digest+'? ID '+str(state.get('installation_id'))+'. Подтвердите завершённую проверку STG.')
        fields={'installation_id':state.get('installation_id'),'confirm':True,'release_sha256':digest}
    if action=='repair-initialization':
        require(args.instance=='acceptance' and pending.get('action')=='initialize' and pending.get('installation_id')==state.get('installation_id'),'Repair requires a pending synthetic initialization')
        confirm('Исправить release незавершённой acceptance-установки? Данные и пароль сохранятся.')
        rel,m=release();files={'budget':rel/'budget','release.json':rel/'release.json','repair.py':ROOT/'scripts/ops/repair_initialization.py'}
        fields={'confirm':True,'operation_id':pending['request_id']}
    elif action in ('initialize','migrate-data'):
        if state.get('installation_id'):require(action=='initialize','Existing local identity: use status/recover rather than importing over it')
        confirm('Создать отдельную '+args.instance+' установку на '+IP+'?')
        rel,m=release();files={'budget':rel/'budget','release.json':rel/'release.json'}
        if action=='migrate-data':
            require(args.instance=='production' and args.archive,'Use production and ARCHIVE from explicit offline legacy-export')
            # Verify locally with a separate native helper, never ./budget.
            native=local_binary();v=json.loads(subprocess.check_output([str(native),'admin','backup','verify','--archive',str(Path(args.archive).resolve())]))
            ident=v['installation_id'];files['backup.tar.gz']=Path(args.archive).resolve()
        else:ident=state.get('installation_id') or uuid.uuid4().hex
        fields={'installation_id':ident,'confirm':True}
        state={'installation_id':ident,'instance':args.instance};atomic(statefile,state)
        action='initialize'
    elif action=='deploy':
        require(state.get('installation_id'),'Initialize/import first');rel,m=release();files={'budget':rel/'budget','release.json':rel/'release.json'};action='prepare'
    elif action in ('restore','rollback','recover','cleanup'):
        if action=='restore':
            require(args.archive and state.get('installation_id'),'ARCHIVE and existing installation required')
            metadata=json.loads(subprocess.check_output([str(local_binary()),'admin','backup','verify','--archive',str(Path(args.archive).resolve())]))
            require(metadata['installation_id']==state['installation_id'],'Archive installation ID mismatch')
            summary={k:metadata.get(k) for k in ('backup_id','installation_id','created_at','reason')}
            summary['schema_version']=metadata['sqlite']['schema_version']
            print(json.dumps(summary,ensure_ascii=False,indent=2))
        confirm('Сервер '+IP+', '+args.instance+', installation ID '+str(state.get('installation_id','неизвестен'))+'. Операция '+action+'; восстановление/откат заменяет данные выбранным поколением.')
        fields={'confirm':True}
        if action=='restore':
            require(args.archive,'ARCHIVE required');files={'backup.tar.gz':Path(args.archive).resolve()}
            if os.environ.get('ALLOW_CORRUPT_LIVE')=='yes':
                confirm('Разрешить восстановление повреждённой базы, если свежая копия live не создаётся? Прежний каталог будет сохранён.')
                fields['allow_corrupt_live']=True
        if action=='recover':
            require(args.decision in ('finish','rollback'),'DECISION=finish|rollback required');fields['decision']=args.decision
            if pending:fields['operation_id']=pending['request_id']
        if action=='cleanup':fields.update({'kind':os.environ.get('KIND','backup'),'backup_id':args.backup_id,'name':args.name})
    elif action=='client-add':
        require(NAME.fullmatch(args.device),'Invalid device');files={'client.crt':Path.home()/'.simple-budget-pki'/(args.device+'.crt')};fields={'device':args.device}
    elif action=='client-revoke':
        listing,_=t.request('client-list',args.instance);record=next((x for x in listing if x['device']==args.device),None)
        require(record and (record['state']=='allowed' or record.get('revocation_pending')),'Device not allowed or pending')
        confirm('Отозвать '+args.device+' SHA-256 '+record['fingerprint']+'? Все текущие HTTPS-соединения будут закрыты.')
        fields={'device':args.device,'fingerprint':record['fingerprint'],'confirm':True}
        probe=RevocationProbe(client_context(args.device),record.get('revocation_pending',False))
    elif action=='fetch-backup':
        if not args.backup_id:
            status,_=t.request('status',args.instance);args.backup_id=status['backup']['latest']['manifest']['backup_id']
        fields={'backup_id':args.backup_id}
    require(action in {'stg-reset','stg-setup','stg-start','stg-stop','promote','repair-initialization','initialize','prepare','restore','rollback','recover','cleanup','client-add','client-revoke','fetch-backup','status','logs','backup','tls-status','client-list','pki-export','secure-page'},'Unknown action')
    if state.get('installation_id'):fields['installation_id']=state['installation_id']
    token=uuid.uuid4().hex
    # Save token before SSH. Lost reply is reconciled, never blindly replayed.
    stateful=action in ('initialize','prepare','promote','stg-reset','restore','rollback','recover')
    if stateful and action!='recover':atomic(pending_file,{'action':action,'instance':args.instance,'request_id':token,'installation_id':state.get('installation_id')})
    try:result,token=t.request(action,args.instance,fields,files,token)
    except BaseException:
        if probe is not None and probe.socket is not None:probe.socket.close()
        raise
    if action=='repair-initialization':
        t.request('clean-incoming',args.instance,token=token)
        print(json.dumps(result,ensure_ascii=False,indent=2))
        print('Release repaired; continue with make recover ENV=production INSTANCE=acceptance DECISION=finish DEVICE='+args.device)
        return
    if probe is not None:
        try:probe.verify_revoked()
        except BaseException:
            t.request('access-stop',args.instance);raise
        result['device_probe']='old connection closed; new/resumption handshake rejected'
    if isinstance(result,dict) and result.get('result')=='awaiting_https_acceptance':
        rev=https_accept(result['version'],args.device,args.instance)
        result,_=t.request('commit',args.instance,{'installation_id':state['installation_id'],'https_verified_revision':rev},token=result['request_id'])
    if action=='fetch-backup':
        dest=local_backups.archive_destination(args.instance,args.backup_id)
        with tempfile.TemporaryDirectory(prefix='.download-',dir=dest.parent) as tmp:
            partial=Path(tmp)/'archive.tar.gz';t.download(token,'download.tar.gz',partial)
            require(sha(partial)==result['sha256'],'Downloaded checksum mismatch')
            metadata=json.loads(subprocess.check_output([str(local_binary()),'admin','backup','verify','--archive',str(partial)]))
            require(metadata['installation_id']==state.get('installation_id') and metadata['backup_id']==args.backup_id,'Downloaded identity mismatch')
            created=local_backups.publish(partial,dest,result['sha256'])
        print(('Проверенная локальная копия: ' if created else 'Идентичная проверенная копия уже существует: ')+str(dest))
    if action in ('client-add','client-revoke'):
        registry_result,_=t.request('pki-export',args.instance);atomic(LOCAL/'device-registry.json',registry_result)
    if action=='pki-export':
        registry=LOCAL/'device-registry.json';atomic(registry,result);print('Public PKI registry:',registry)
    show_result(action,result,args.instance)
    if stateful:pending_file.unlink(missing_ok=True)
    t.request('clean-incoming',args.instance,token=token)

def show_result(action,result,instance):
    if action=='status':
        result=dict(result,version_comparison=version_status.inspect(ROOT,result.get('release') or {}))
    if os.environ.get('OUTPUT')=='json' or ui.verbose() or action in ('pki-export','logs'):
        print(json.dumps(result,ensure_ascii=False,indent=2));return
    print('Сервер '+IP+' / '+('PROD' if instance=='production' else 'STG'))
    if action=='status':
        print('Приложение: '+str(result.get('service','не инициализировано'))+'; Caddy: '+str(result.get('caddy_active')))
        version_status.display(result['version_comparison'],result.get('release') or {})
        print('Installation ID: '+str(result.get('installation_id','—')))
        if instance=='acceptance' and 'staging_isolation' in result:
            print('Изоляция STG: '+('проверена' if result['staging_isolation'].get('passed') is True else 'НЕ ПОДТВЕРЖДЕНА — проверьте OUTPUT=json'))
        backup=result.get('backup',{});latest=backup.get('latest') or {};manifest=latest.get('manifest',{})
        print('Последняя копия: '+str(manifest.get('backup_id','нет'))+'; автоматизация: '+str(backup.get('automatic','неизвестно'))+'; следующий запуск: '+str(backup.get('next_at','—')))
        if any(result.get(k) for k in ('deployment_journal','restore_journal','backup_operation_pending','revocation_pending')):
            print('Есть незавершённая операция. Проверьте OUTPUT=json и используйте штатный recover; не повторяйте изменение.')
        if result.get('stg_setup_state') in ('preparing','recovering'):
            print('Настройка STG прервана. Выполните stg-setup INSTANCE=acceptance DECISION=rollback перед повторной настройкой.')
        print('Свободно на диске: '+str(result.get('disk',{}).get('free_bytes','—'))+' байт; TLS: '+str(result.get('tls',{}).get('status','—')))
    elif action=='client-list':
        for item in result:
            state={'allowed':'доступ разрешён','revoked':'отозвано'}.get(item.get('state'),'неизвестно')
            print(str(item.get('device','—'))+' — '+state+'; до '+str(item.get('not_after','—')))
    elif action=='backup':
        print('Проверенная копия: '+str(result.get('manifest',{}).get('backup_id','—'))+'; размер: '+str(result.get('size','—')))
    elif action=='tls-status':
        print(json.dumps(result,ensure_ascii=False,indent=2))
    else:
        code=result.get('result',result.get('state','выполнено'))
        labels={'stg_started':'STG запущен; автозапуск выключен','stg_stopped':'STG остановлен; release и данные сохранены','stg_configured_stopped':'STG настроен и остановлен; установите совместимый release','committed':'обновление подтверждено','already_current':'установлен этот же релиз'}
        print('Результат: '+str(labels.get(code,code)))
        if code=='stg_setup_rolled_back':
            print('Прежняя конфигурация Caddy восстановлена; STG остановлен. Данные и PROD-сервис не изменены. Правило firewall 8443 сохранено, listener отсутствует.')

def local_binary():
    # Isolated administrative helper. Never replaces the user's ./budget.
    LOCAL.mkdir(mode=0o700,exist_ok=True)
    path=LOCAL/'budget-admin-helper'
    subprocess.run(['go','build','-o',str(path),'.'],cwd=ROOT,check=True)
    return path

def main(argv=None):
    try:return _main(argv)
    finally:
        failed=sys.exc_info()[0] is not None
        cleanup_error=None
        try:
            for t in ACTIVE_TRANSPORTS[:]:
                try:t.close()
                except Exception as e:
                    print('SSH session close failed; check status before another change.',file=sys.stderr)
                    cleanup_error=e
                finally:ACTIVE_TRANSPORTS.remove(t)
        finally:
            if secret_store.SESSION:
                try:secret_store.SESSION.close()
                finally:secret_store.SESSION=None
        if cleanup_error and not failed:raise cleanup_error

if __name__=='__main__':
    try:main()
    except (KeyboardInterrupt,EOFError):
        print('Операция отменена. Если началось изменение сервера, выполните status/recover.',file=sys.stderr);sys.exit(130)
    except (Refused,OSError,ValueError,KeyError,subprocess.CalledProcessError) as e:
        detail=str(e)
        if isinstance(e,subprocess.CalledProcessError) and not ui.verbose():
            detail=Path(e.cmd[0]).name+': код завершения '+str(e.returncode)+'. Диагностика команды показана выше.'
        print('Ошибка. Этап: '+ui.LAST_STAGE+'. '+detail+'\nДля прерванного изменения: status, затем recover. Подробности: VERBOSE=1.',file=sys.stderr);sys.exit(1)
