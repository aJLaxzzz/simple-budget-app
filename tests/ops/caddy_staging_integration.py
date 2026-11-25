"""Real Caddy cookie/mTLS/streaming isolation. Synthetic PKI, loopback only.
CADDY_BIN=/path/to/caddy python3 tests/ops/caddy_staging_integration.py
Never connects to the VPS or reads user PKI/data.
"""
import copy
import http.client
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import IP
import caddy_policy as cp
from test_operations import config,CLIENTS

def free_port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]

class Backend(BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args):pass
    def do_GET(self):
        if self.path=='/events':
            self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
            self.wfile.write(b'data: synthetic\n\n');self.wfile.flush();time.sleep(1)
            self.close_connection=True;return
        if self.path=='/sw.js':body=b'self.addEventListener("install",()=>self.skipWaiting());';content='application/javascript'
        elif self.path=='/':body=b'<html><body>Synthetic</body></html>';content='text/html'
        else:body=json.dumps({'cookie':self.headers.get('Cookie','')}).encode();content='application/json'
        self.send_response(200)
        for name in ('__Host-budget-session','__Host-budget-stg-session','arbitrary'):
            self.send_header('Set-Cookie',name+'=synthetic; Secure; HttpOnly; Path=/')
        self.send_header('Clear-Site-Data','"cookies", "storage"')
        self.send_header('Content-Type',content);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)

def run():
    binary=os.environ['CADDY_BIN'];backends=[];process=None
    with tempfile.TemporaryDirectory(prefix='budget-stg-caddy-') as tmp:
        root=Path(tmp)
        def openssl(*args):subprocess.run(['openssl',*args],cwd=root,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        openssl('req','-x509','-newkey','rsa:2048','-nodes','-keyout','ca.key','-out','ca.crt','-days','1','-subj','/CN=Synthetic test CA')
        for name,purpose in (('server','serverAuth'),('client','clientAuth'),('denied','clientAuth')):
            openssl('req','-newkey','rsa:2048','-nodes','-keyout',name+'.key','-out',name+'.csr','-subj','/CN='+name)
            ext=root/(name+'.ext');ext.write_text('basicConstraints=CA:FALSE\nextendedKeyUsage='+purpose+'\nsubjectAltName=IP:'+IP+',IP:127.0.0.1\n')
            openssl('x509','-req','-in',name+'.csr','-CA','ca.crt','-CAkey','ca.key','-CAcreateserial','-out',name+'.crt','-days','1','-extfile',name+'.ext')
        try:
            for _ in range(2):
                server=ThreadingHTTPServer(('127.0.0.1',0),Backend);server.daemon_threads=True
                threading.Thread(target=server.serve_forever,daemon=True).start();backends.append(server)
            production_port,staging_port,plain_port=free_port(),free_port(),free_port()
            out=cp.render_dual(config(),CLIENTS)
            out['storage']['root']=str(root/'storage')
            out['apps']['tls']={'certificates':{'load_files':[{'certificate':str(root/'server.crt'),'key':str(root/'server.key')}]},'session_tickets':{'disabled':True}}
            for server in out['apps']['http']['servers'].values():
                old=server['listen'][0];server['listen']=['127.0.0.1:'+str({':443':production_port,':8443':staging_port,':80':plain_port}[old])]
                server['automatic_https']={'disable':True}
                for policy in server.get('tls_connection_policies',[]):
                    policy['default_sni']='127.0.0.1'
                    policy['client_authentication']['ca']['pem_files']=[str(root/'ca.crt')]
                    policy['client_authentication']['verifiers'][0]['leaf_certs_loaders'][0]['files']=[str(root/'client.crt')]
                for node in cp.walk(server):
                    if node.get('host')==[IP]:node['host']=['127.0.0.1']
                    if node.get('handler')=='reverse_proxy':
                        i=1 if node['upstreams'][0]['dial'].endswith(':3002') else 0
                        node['upstreams'][0]['dial']='127.0.0.1:'+str(backends[i].server_port)
                    if 'file' in node and isinstance(node['file'],dict):node['file']['root']=str(root)
            (root/'stg-access-open').write_text('1')
            path=root/'caddy.json';path.write_text(json.dumps(out))
            env=dict(os.environ,HOME=str(root),XDG_DATA_HOME=str(root/'xdg-data'),XDG_CONFIG_HOME=str(root/'xdg-config'))
            subprocess.run([binary,'validate','--config',str(path)],env=env,check=True,capture_output=True)
            with (root/'caddy.log').open('wb') as logs:
                process=subprocess.Popen([binary,'run','--config',str(path)],env=env,stdout=logs,stderr=logs)
                for _ in range(100):
                    try:
                        with socket.create_connection(('127.0.0.1',production_port),timeout=.1):break
                    except OSError:time.sleep(.05)
                else:raise RuntimeError('Caddy did not start')
                def request(port,client='client',path='/echo',cookie=None,extra=None):
                    context=ssl.create_default_context(cafile=str(root/'ca.crt'))
                    if client:context.load_cert_chain(root/(client+'.crt'),root/(client+'.key'))
                    conn=http.client.HTTPSConnection('127.0.0.1',port,context=context,timeout=3)
                    headers={'Host':'127.0.0.1'}
                    if cookie:headers['Cookie']=cookie
                    headers.update(extra or {})
                    conn.request('GET',path,headers=headers);return conn,conn.getresponse()
                for port,name in ((production_port,'__Host-budget-session'),(staging_port,'__Host-budget-stg-session')):
                    for denied in (None,'denied'):
                        try:
                            c,r=request(port,denied);r.read();c.close()
                        except ssl.SSLError:pass
                        else:raise AssertionError('mTLS allowed unauthorized device')
                    c,r=request(port,cookie='other=x; __Host-budget-session=prod-secret; __Host-budget-stg-session=stg-secret')
                    assert r.status==200, (port==staging_port, r.status, r.read(), (root/'caddy.log').read_text())
                    assert json.loads(r.read())['cookie']==name+'='+('prod-secret' if port==production_port else 'stg-secret')
                    assert all(value.startswith(name+'=') for key,value in r.getheaders() if key.lower()=='set-cookie')
                    assert r.getheader('Clear-Site-Data') is None;c.close()
                    c,r=request(port);assert json.loads(r.read())['cookie']=='';c.close()
                for port in (production_port,staging_port):
                    c,r=request(port,extra={'Sec-Fetch-Site':'same-site'});assert r.status==403;r.read();c.close()
                    c,r=request(port,path='/',extra={'Sec-Fetch-Site':'cross-site','Sec-Fetch-Mode':'navigate','Sec-Fetch-Dest':'document'});assert r.status==200;r.read();c.close()
                t=time.monotonic();c,r=request(staging_port,path='/events')
                assert r.readline()==b'data: synthetic\n' and time.monotonic()-t<.9;c.close()
                subprocess.run(['node',str(Path(__file__).with_name('staging_origins.cjs')),str(root),str(production_port),str(staging_port)],check=True,timeout=60)
                pid=process.pid;(root/'stg-access-open').unlink()
                c,r=request(staging_port);assert r.status==503;r.read();c.close()
                c,r=request(production_port);assert r.status==200;r.read();c.close()
                (root/'stg-access-trial').write_text('1')
                c,r=request(staging_port,path='/api/ready');assert r.status==200;r.read();c.close()
                c,r=request(staging_port,path='/api/planner/tasks');assert r.status==503;r.read();c.close()
                assert process.pid==pid and process.poll() is None
                print('PASS: real Caddy mTLS on both listeners; request/response cookie confinement; SSE streaming; STG gate without Caddy restart; PROD stays available')
        finally:
            if process is not None:process.terminate();process.wait(timeout=15)
            for backend in backends:backend.shutdown();backend.server_close()

if __name__=='__main__':run()
