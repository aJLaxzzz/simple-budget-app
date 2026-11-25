"""Interactive Mac-only TLS1.2/1.3 + HTTP/1.1/2 revocation acceptance."""
import getpass
import json
import os
from pathlib import Path
import selectors
import shutil
import subprocess
import sys
from common import IP, Refused, require, regular, read, atomic, utc


class MatrixWorker:
    def __init__(self, device):
        root=Path.home()/'.simple-budget-pki'
        require(root.is_dir() and not root.is_symlink() and root.stat().st_mode&0o077==0,'Private local PKI required')
        cert=root/(device+'.crt');key=root/(device+'.key')
        regular(cert,64<<10);regular(key,64<<10)
        require(key.stat().st_mode&0o077==0 and b'ENCRYPTED' in read(key,64<<10),'Encrypted private device key required')
        node=shutil.which('node');require(node,'Local Node is required for the HTTP/2 acceptance probe')
        self.process=subprocess.Popen([node,str(Path(__file__).with_name('tls_matrix.cjs'))],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,bufsize=1,
                                      env={'PATH':os.environ.get('PATH','/usr/bin:/bin'),'LANG':'en_US.UTF-8'})
        try:
            password=getpass.getpass('Пароль закрытого ключа '+device+': ')
            try:self.prepared=self.send({'action':'prepare','cert':str(cert),'key':str(key),'passphrase':password},timeout=60)
            finally:del password
        except BaseException:self.close();raise
    def send(self, command, timeout=45):
        try:
            self.process.stdin.write(json.dumps(command)+'\n');self.process.stdin.flush()
            with selectors.DefaultSelector() as selector:
                selector.register(self.process.stdout,selectors.EVENT_READ)
                require(selector.select(timeout),'TLS worker response timed out')
                line=self.process.stdout.readline(16384)
            require(line and len(line)<16384,'TLS worker returned no bounded result')
            response=json.loads(line)
            require(response.get('ok') is True,'TLS acceptance failed: '+str(response.get('code','INVALID_RESULT')))
            return response['result']
        except (OSError,ValueError):raise Refused('TLS worker communication failed') from None
    def close(self):
        if self.process.poll() is None:
            try:self.process.stdin.close()
            except OSError:pass
            try:self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:self.process.kill();self.process.wait()
        self.process.stdout.close()


def execute(transport, instance, device):
    from mac import LOCAL, local_state, load, confirm
    import pki
    require(instance=='acceptance' and device=='acceptance-tls-device','Only INSTANCE=acceptance DEVICE=acceptance-tls-device is allowed')
    state=load(local_state(instance));require(state.get('instance')=='acceptance','Synthetic local identity required')
    status,_=transport.request('status',instance)
    require(status.get('synthetic') is True and status.get('installation_id')==state.get('installation_id')
            and status.get('service')=='running' and status.get('proxy',{}).get('mode')=='open'
            and not any(status.get(k) for k in ('deployment_journal','restore_journal','backup_operation_pending','revocation_pending')),'Synthetic installation is not ready for TLS acceptance')
    record=next((c for c in status['clients'] if c['device']==device),None)
    require(not record or record['state']=='allowed','Revoked device will never be re-enabled; inspect completed acceptance evidence')
    cert=Path.home()/'.simple-budget-pki'/(device+'.crt')
    if not cert.exists():
        print('Создаётся отдельное временное удостоверение '+device+'. mac-1 и phone не меняются.')
        pki.execute('client-issue',device)
    if record is None:
        record,_=transport.request('client-add',instance,{'device':device},{'client.crt':cert})
    worker=None;revoked=False
    try:
        worker=MatrixWorker(device)
        print(json.dumps({'before_revocation':worker.prepared},ensure_ascii=False,indent=2))
        confirm('Отозвать тестовое '+device+' SHA-256 '+record['fingerprint']+'? Четыре проверяемых соединения и остальные HTTPS-соединения будут закрыты.')
        worker.send({'action':'arm'})
        result,_=transport.request('client-revoke',instance,{'device':device,'fingerprint':record['fingerprint'],'confirm':True})
        revoked=True
        try:proof=worker.send({'action':'verify'},timeout=90)
        except BaseException:
            transport.request('access-stop',instance)
            raise
        registry,_=transport.request('pki-export',instance)
        atomic(LOCAL/'device-registry.json',registry)
        evidence={'at':utc(),'instance':instance,'installation_id':state['installation_id'],'device':device,
                  'fingerprint':record['fingerprint'],'before_revocation':worker.prepared,'after_revocation':proof,
                  'server_result':result,'real_devices':{n:registry['clients'][n]['state'] for n in ('mac-1','phone')}}
        require(all(v=='allowed' for v in evidence['real_devices'].values()),'Real device registry changed')
        atomic(LOCAL/'acceptance-tls-result.json',evidence)
        print(json.dumps(evidence,ensure_ascii=False,indent=2))
        print('TLS matrix passed. Evidence: '+str(LOCAL/'acceptance-tls-result.json'))
    except BaseException:
        if not revoked:print('Проверка не завершена. Тестовое удостоверение может оставаться allowed; mac-1/phone не изменены.',file=sys.stderr)
        raise
    finally:
        if worker:worker.close()
