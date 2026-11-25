#!/usr/bin/env python3
"""Test real Linux HTTP session invalidation using only a disposable installation.
No changes to systemd/Caddy, the acceptance live directory, or existing sessions.
Cookie transport is directly over loopback, not a browser/Caddy acceptance test.
"""
import base64
import hashlib
import http.client
from http.cookies import SimpleCookie
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tarfile
import tempfile
import time

BINARY = Path('/opt/simple-budget-acceptance/releases/37b5dcde4f3b-d0ac7d8002708002/budget')
DIGEST = 'd0ac7d80027080025c621545eba5d5e041d9ea3564af4dfd55e322921831a6b8'
HOST = '171.22.237.16'


def main():
    assert os.uname().sysname == 'Linux' and os.geteuid() != 0
    assert hashlib.sha256(BINARY.read_bytes()).hexdigest() == DIGEST
    clean_env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'LANG')}
    process = None
    with tempfile.TemporaryDirectory(prefix='budget-cookie-restore-') as temp:
        root = Path(temp); live = root/'live'; live.mkdir(mode=0o700)
        repo = root/'backups'; repo.mkdir(mode=0o700)
        def admin(*args):
            result = subprocess.run([str(BINARY), 'admin', *map(str, args)], env=clean_env,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
            assert result.returncode == 0, 'Synthetic administrative operation failed: ' + str(args[:2])
            return json.loads(result.stdout)
        identity = admin('init', '--data-dir', live, '--environment', 'production', '--confirm')['installation_id']
        password = secrets.token_urlsafe(32)
        credential = root/'password'; fd = os.open(credential, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as f: f.write(password)
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0)); port = listener.getsockname()[1]
        assert port not in (3001, 3002, 2019, 443, 80)
        environment = dict(clean_env, BUDGET_ENV='production', BUDGET_ADDR='127.0.0.1:'+str(port),
                           BUDGET_DATA_DIR=str(live), BUDGET_PASSWORD_FILE=str(credential),
                           BUDGET_INSTALLATION_ID=identity, BUDGET_MIGRATIONS='check-only',
                           BUDGET_ALLOWED_HOSTS=HOST, BUDGET_PUBLIC_ORIGIN='https://'+HOST,
                           BUDGET_PROXY_MODE='loopback-https', GOMEMLIMIT='128MiB', GOMAXPROCS='1')
        machine = {'X-Budget-Admin':'1', 'Authorization':'Basic '+base64.b64encode(('budget:'+password).encode()).decode()}
        def request(method, path, body=None, headers=None):
            connection = http.client.HTTPConnection('127.0.0.1', port, timeout=5)
            try:
                h = {'Host':HOST, 'Origin':'https://'+HOST, 'Content-Type':'application/json'}; h.update(headers or {})
                connection.request(method, path, json.dumps(body).encode() if body is not None else None, h)
                response = connection.getresponse(); data = response.read(32769)
                assert len(data) <= 32768
                return response.status, dict((k.lower(),v) for k,v in response.getheaders()), data
            finally: connection.close()
        def start():
            nonlocal process
            process = subprocess.Popen([str(BINARY)], cwd=live, env=environment,
                                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            deadline = time.monotonic()+15
            while time.monotonic() < deadline:
                assert process.poll() is None, 'Synthetic server exited during startup'
                try:
                    if request('GET','/api/ready',headers=machine)[0] == 200: return
                except OSError: pass
                time.sleep(.05)
            raise AssertionError('Synthetic readiness timed out')
        def stop():
            nonlocal process
            if process is not None:
                process.terminate()
                try: process.communicate(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill(); process.communicate(); raise AssertionError('Synthetic graceful shutdown failed')
                assert process.returncode == 0, 'Synthetic server shutdown failed'
                process = None
        def login():
            code, headers, _ = request('POST','/api/auth/login',{'username':'budget','password':password,'remember':True})
            assert code == 200
            cookie = SimpleCookie(); cookie.load(headers['set-cookie']); c = cookie['__Host-budget-session']
            assert c['secure'] and c['httponly'] and c['samesite']=='Strict'
            return {'Cookie':'__Host-budget-session='+c.value}
        checks = []
        try:
            start(); old = login()
            assert request('GET','/api/auth/session',headers=old)[0] == 200
            stop(); start()
            assert request('GET','/api/auth/session',headers=old)[0] == 200
            checks.append('same cookie accepted after ordinary restart')
            stop()
            options = ['--environment','production','--data-dir',live,'--backup-dir',repo,'--installation-id',identity]
            receipt = admin('backup','create','--reason','manual',*options)
            archive = repo/'manual'/receipt['manifest']['backup_id']/'backup.tar.gz'
            with tarfile.open(archive,'r:gz') as tar:
                assert not any('web-auth' in entry.name for entry in tar)
            checks.append('backup excludes session storage')
            start(); assert request('GET','/api/auth/session',headers=old)[0] == 200; stop()
            for scope in ('full','planner'):
                admin('backup','restore','--archive',archive,'--scope',scope,'--confirm',*options)
                assert not (live/'web-auth.json').exists()
                with sqlite3.connect((live/'planner.db').as_uri()+'?mode=ro',uri=True) as db:
                    assert db.execute('PRAGMA journal_mode').fetchone()==('wal',)
                    assert db.execute('PRAGMA integrity_check').fetchone()==('ok',)
                start()
                for endpoint in ('/api/auth/session','/api/events'):
                    assert request('GET',endpoint,headers=old)[0] == 401
                assert request('GET','/api/ready',headers=dict(machine,**old))[0] == 401
                assert request('GET','/api/ready',headers=machine)[0] == 200
                old = login(); assert request('GET','/api/auth/session',headers=old)[0] == 200
                checks.append(scope+' restore: old cookie/SSE rejected, no Basic fallback, new login works, WAL intact')
                stop()
            print(json.dumps({'passed':True,'binary_sha256':DIGEST,'scope':'Linux isolated production-mode process, direct loopback HTTP',
                              'existing_acceptance_changed':False,'checks':checks}))
        finally:
            stop()


if __name__ == '__main__':
    try: main()
    except Exception as error:
        # Never print credentials, cookies, subprocess output or traceback locals.
        print(json.dumps({'passed':False,'error':str(error) if isinstance(error,AssertionError) else type(error).__name__}))
        raise SystemExit(1)
