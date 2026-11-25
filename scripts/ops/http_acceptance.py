"""Read-only public HTTPS acceptance, including streamed two-part Range download."""
import base64
import getpass
import hashlib
import http.client
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
from common import IP, BACKUP_ID, require, atomic, utc


def download_headers(response, expected_status):
    require(response.status==expected_status,'Download returned HTTP '+str(response.status))
    policy=(response.getheader('Cache-Control') or '').split(',')
    require(policy and all(x.strip().lower()=='no-store' for x in policy),'Download cache policy changed')
    require((response.getheader('Content-Disposition') or '').split(';',1)[0].lower()=='attachment','Download is not an attachment')


def stream_download(connect, path, destination, size, checksum, ranges=False):
    require(0<size<=64<<20,'Acceptance download exceeds bounded synthetic fixture size')
    digest=hashlib.sha256();count=0
    parts=[(0,min(1023,size-1)),(min(1024,size),size-1)] if ranges else [(0,size-1)]
    with open(destination,'xb') as output:
        for start,end in parts:
            if start>end:continue
            c,r=connect('GET',path,headers={'Range':'bytes='+str(start)+'-'+str(end)} if ranges else {})
            try:
                download_headers(r,206 if ranges else 200)
                if ranges:
                    require(r.getheader('Content-Range')=='bytes '+str(start)+'-'+str(end)+'/'+str(size),'Unexpected Content-Range')
                    require(r.getheader('ETag')=='"'+checksum+'"','Archive ETag differs from verified receipt')
                remaining=end-start+1
                while remaining:
                    chunk=r.read(min(65536,remaining))
                    require(chunk,'Download was truncated')
                    output.write(chunk);digest.update(chunk);count+=len(chunk);remaining-=len(chunk)
                require(not r.read(1),'Unexpected extra download bytes')
            finally:c.close()
    require(count==size and digest.hexdigest()==checksum,'Download checksum mismatch')


def execute(transport, instance, device):
    from mac import LOCAL, ROOT, load, local_state, app_connection, local_binary
    require(instance=='acceptance' and device in ('mac-1','phone'),'Only synthetic acceptance with an existing real device is supported')
    state=load(local_state(instance));status,_=transport.request('status',instance)
    require(status.get('synthetic') is True and status.get('installation_id')==state.get('installation_id'),'Wrong synthetic installation')
    proxy=status.get('proxy',{})
    require(proxy.get('instance')==instance and proxy.get('mode')=='open' and proxy.get('configuration_matches') is True,'Synthetic proxy not open/verified')
    context,password=app_connection(device)
    evidence={'at':utc(),'installation_id':state['installation_id'],'device':device,'checks':[]}
    def connect(method,path,body=None,headers=None):
        c=http.client.HTTPSConnection(IP,443,context=context,timeout=30)
        h={'Host':IP,'Origin':'https://'+IP,'X-Budget-Admin':'1','Authorization':'Basic '+base64.b64encode(('budget:'+password).encode()).decode(),'Content-Type':'application/json'}
        h.update(headers or {})
        try:c.request(method,path,body,h);return c,c.getresponse()
        except BaseException:c.close();raise
    def request(method,path,body=None,headers=None):
        c,r=connect(method,path,body,headers)
        try:
            data=r.read(2<<20)
            require(not r.read(1),'Unexpected oversized acceptance response')
            return r.status,data
        finally:c.close()
    def expect(label,method,path,code,body=None,headers=None):
        actual,data=request(method,path,body,headers)
        require(actual in (code if isinstance(code,tuple) else (code,)),label+': HTTP '+str(actual))
        evidence['checks'].append({'check':label,'status':actual})
        return data
    try:
        for attempt in range(3):
            code,data=request('GET','/api/ready')
            if code==200:break
            require(code==401 and attempt<2,'HTTPS readiness failed: HTTP '+str(code))
            print('Пароль приложения не принят. Повторите только пароль приложения.')
            password=getpass.getpass('Пароль приложения (попытка '+str(attempt+2)+' из 3): ')
        ready=json.loads(data);expected=status['release']
        require(ready.get('revision')==expected['revision'] and ready.get('schema_version')==expected['schema_version'] and ready.get('modified') is False,'HTTPS release/schema mismatch')
        evidence['revision']=ready['revision'];evidence['schema_version']=ready['schema_version']
        metadata=json.loads(expect('backup_status','GET','/api/backups/status',200))
        latest=metadata['latest'];ident=latest['manifest']['backup_id']
        require(BACKUP_ID.fullmatch(ident) and latest['manifest']['installation_id']==state['installation_id'],'Backup identity differs')
        paths=['/','/js/api.js','/css/style.css','/api/ready','/api/planner/tasks','/api/backups/status',
               '/api/backups/'+ident+'/full','/api/backups/'+ident+'/planner']
        for path in paths:
            expect('unauthorized '+path,'GET',path,401,headers={'X-Budget-Admin':'1','Authorization':''})
        for path in paths[:3]:expect('embedded '+path,'GET',path,200)
        forwarded={'X-Forwarded-For':'127.0.0.1','X-Real-IP':'127.0.0.1','X-Forwarded-User':'budget','Forwarded':'for=127.0.0.1;proto=https;host='+IP}
        expect('forwarded cannot authorize','GET','/api/ready',401,headers=dict(forwarded,Authorization=''))
        expect('forwarded cannot repair Origin','POST','/api/planner/tasks',403,body=b'{}',headers=dict(forwarded,Origin='https://foreign.invalid'))
        expect('forwarded cannot repair Host','GET','/api/ready',(403,404,421),headers={'Host':'foreign.invalid','X-Forwarded-Host':IP})
        expect('canonical Origin accepted despite forwarded proto','GET','/api/ready',200,headers={'X-Forwarded-Proto':'http'})
        expect('Content-Type enforced','POST','/api/planner/tasks',415,body=b'{}',headers={'Content-Type':'text/plain'})
        expect('cross-site blocked','GET','/api/ready',403,headers={'Sec-Fetch-Site':'cross-site'})
        expect('bad password rejected','GET','/api/ready',401,headers={'X-Budget-Admin':'1','Authorization':'Basic '+base64.b64encode(b'budget:incorrect-synthetic-password').decode()})
        for path in ('/planner.db','/data.json','/categories.json','/web-auth.json','/backups/','/staging/',
                     '/api/backups/../staging/x','/api/backups/'+ident+'/restore'):
            expect('private route '+path,'GET',path,(400,403,404))
        expect('HTTP restore unavailable','POST','/api/backups/'+ident+'/restore',405,body=b'{}')
        saved=LOCAL/'acceptance-result.json'
        baseline=ROOT/'artifacts/server-acceptance/verified-http-download.json'
        if not saved.exists() and baseline.exists():
            original=load(baseline)
            require(original['installation_id']==state['installation_id'],'Saved snapshot identity differs')
            for ident_task in original['tasks_ids']:
                task=json.loads(expect('snapshot task survived restore/update/reboot','GET','/api/planner/tasks/'+ident_task,200))
                require(task['id']==ident_task and task['project_id'] in original['projects_ids'],'Snapshot task identity differs')
            evidence['original_task_ids_preserved']=len(original['tasks_ids'])
        if saved.exists():
            original=load(saved)
            require(original['installation_id']==state['installation_id'],'Saved fixture identity differs')
            task=json.loads(expect('original task survived restore/update/reboot','GET','/api/planner/tasks/'+original['task_id'],200))
            require(task['id']==original['task_id'] and task['project_id']==original['project_id'],'Original task identity differs')
            evidence['original_task_id_preserved']=True
        with tempfile.TemporaryDirectory(prefix='budget-http-acceptance-') as tmp:
            archive=Path(tmp)/'backup.tar.gz'
            stream_download(connect,'/api/backups/'+ident+'/full',archive,latest['size'],latest['sha256'],ranges=True)
            manifest=json.loads(subprocess.check_output([str(local_binary()),'admin','backup','verify','--archive',str(archive)]))
            require(manifest['backup_id']==ident and manifest['installation_id']==state['installation_id'],'Range archive verification identity mismatch')
            planner=next(x for x in manifest['files'] if x['name']=='planner.db');db=Path(tmp)/'planner.db'
            stream_download(connect,'/api/backups/'+ident+'/planner',db,planner['size'],planner['sha256'])
            with sqlite3.connect(db.as_uri()+'?mode=ro&immutable=1',uri=True) as connection:
                require(connection.execute('PRAGMA integrity_check').fetchall()==[('ok',)] and not connection.execute('PRAGMA foreign_key_check').fetchall(),'Planner download integrity failed')
            evidence['downloads']={'backup_id':ident,'range_parts':2,'archive_size':latest['size'],'archive_sha256':latest['sha256'],'planner_size':planner['size'],'planner_sha256':planner['sha256'],'native_verify':'passed','sqlite_integrity_fk':'passed'}
        atomic(LOCAL/'acceptance-http-result.json',evidence)
        print(json.dumps(evidence,ensure_ascii=False,indent=2))
        print('HTTPS checks passed; no restore, service restart or application data changes requested.')
    finally:del password
