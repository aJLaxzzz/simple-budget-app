#!/usr/bin/env python3
"""Explicit synthetic VPS acceptance. Secrets stay in memory/private child stdin.
Uses existing status/backup/restore protocols; never deploys or changes access config.
"""
import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import getpass
import http.client
from http.cookies import SimpleCookie
import json
import os
from pathlib import Path
import ssl
import signal
import subprocess
import sys
import tarfile
import tempfile
import time

from common import IP, Refused, atomic, load, regular, require, sha, utc
import mac
from http_acceptance import stream_download
from local_backups import archive_destination, publish

REVISION = '37b5dcde4f3b34850e163c4f59ce48252341833e'
IDENTITY = '6919a32cbb5e4cd09a2a00f691e32812'
DIGEST = 'd0ac7d80027080025c621545eba5d5e041d9ea3564af4dfd55e322921831a6b8'
ORIGIN = 'https://' + IP + ':8443'
COOKIE = '__Host-budget-stg-session'


def check_target(status):
    require(status.get('instance') == 'acceptance' and status.get('synthetic') is True
            and status.get('installation_id') == IDENTITY, 'Wrong synthetic installation; no tests started')
    release = status.get('release', {})
    require(release.get('revision') == REVISION and release.get('modified') is False
            and release.get('schema_version') == 14
            and release.get('files', [{}])[0].get('sha256') == DIGEST, 'Unapproved installed release')
    proxy = status.get('proxy', {})
    require(proxy.get('instance') == 'acceptance' and proxy.get('mode') == 'open'
            and proxy.get('configuration_matches') is True, 'Acceptance proxy is not verified/open')
    require(not any(status.get(k) for k in ('deployment_journal', 'restore_journal', 'backup_operation_pending')),
            'Pending administrative operation; use status/recover first')


def checked_cookie(headers, remember):
    jar = SimpleCookie(); jar.load(headers.get('set-cookie', ''))
    require(COOKIE in jar, 'Login did not set session cookie')
    c = jar[COOKIE]
    require(c['secure'] and c['httponly'] and c['samesite'].lower() == 'strict'
            and c['path'] == '/' and not c['domain'], 'Session cookie attributes failed')
    require(c['max-age'] == '2592000' if remember else not c['max-age'] and not c['expires'],
            'Session cookie lifetime failed')
    return COOKIE + '=' + c.value


class Client:
    def __init__(self, context):
        self.context = context

    def connect(self, method, path, body=None, headers=None):
        c = http.client.HTTPSConnection(IP, 8443, context=self.context, timeout=25)
        h = {'Origin': ORIGIN, 'Content-Type': 'application/json'}
        h.update(headers or {})
        try:
            c.request(method, path, json.dumps(body).encode() if body is not None else None, h)
            return c, c.getresponse()
        except BaseException:
            c.close(); raise

    def request(self, method, path, body=None, headers=None):
        c, r = self.connect(method, path, body, headers)
        try:
            data = r.read((2 << 20) + 1)
            require(len(data) <= 2 << 20, 'Acceptance response exceeds bound')
            return r.status, dict((k.lower(), v) for k, v in r.getheaders()), data
        finally:
            c.close()


class Stream:
    def __init__(self, connection, response):
        self.connection = connection; self.response = response

    def close(self):
        self.response.close(); self.connection.close()


def frame(response):
    lines = []; size = 0
    while True:
        line = response.readline(8193)
        if not line:
            return None
        size += len(line)
        require(size <= 8192, 'SSE frame exceeds bound')
        if line in (b'\n', b'\r\n'):
            break
        lines.append(line.decode().strip())
    event = next((s[7:] for s in lines if s.startswith('event: ')), '')
    data = json.loads(next((s[6:] for s in lines if s.startswith('data: ')), '{}'))
    return event, data


def metrics():
    # Fixed read-only commands; no root shell, credentials or application data.
    command = """/usr/bin/python3 -I -c 'import json,subprocess,pathlib
p=subprocess.check_output(["systemctl","show","simple-budget-acceptance.service","-p","MainPID","-p","MemoryCurrent","-p","MemoryPeak","-p","NRestarts","-p","ActiveState"],text=True)
d=dict(x.split("=",1) for x in p.splitlines())
s=pathlib.Path("/proc/"+d["MainPID"]+"/status").read_text()
d["rss_kib"]=int(next(x.split()[1] for x in s.splitlines() if x.startswith("VmRSS:")))
d["backend_connections"]=len(subprocess.check_output(["ss","-Htn","state","established","(","sport","=",":3002",")"],text=True).splitlines())
d["memory_events"]=pathlib.Path("/sys/fs/cgroup/system.slice/simple-budget-acceptance.service/memory.events").read_text()
print(json.dumps(d))'"""
    raw = subprocess.check_output(['/usr/bin/ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                                   '-o', 'HostKeyAlgorithms=ssh-ed25519', '-o', 'ConnectTimeout=15', 'budget', command], timeout=30)
    return json.loads(raw)


def browser_probe(payload):
    # Secrets cross an anonymous pipe, never command arguments/environment/files.
    env = {k: v for k, v in os.environ.items() if k in ('HOME', 'PATH', 'TMPDIR', 'LANG', 'USER')}
    child = subprocess.Popen(['node', str(Path(__file__).with_name('live_acceptance.cjs'))],
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             env=env, start_new_session=True)
    try:
        output, _ = child.communicate(json.dumps(payload).encode(), timeout=150)
        return child.returncode, json.loads(output)
    except BaseException:
        # Only our newly created process group, including its isolated Chrome.
        try: os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError: pass
        try: child.communicate(timeout=3)
        except subprocess.TimeoutExpired:
            try: os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            child.communicate()
        raise


def run(restore):
    require(os.environ.get('ENV') == 'production' and os.environ.get('INSTANCE') == 'acceptance',
            'Explicit ENV=production INSTANCE=acceptance required')
    require(os.environ.get('SUDO_MODE') == 'session', 'Use SUDO_MODE=session')
    require(load(mac.local_state('acceptance')).get('installation_id') == IDENTITY, 'Local identity mismatch')
    device = os.environ.get('DEVICE', 'mac-1')
    require(device == 'mac-1', 'This probe uses only the existing mac-1 identity')
    pki = Path.home() / '.simple-budget-pki'
    regular(pki / 'mac-1.crt', 65536, os.getuid())
    require(regular(pki / 'mac-1.key', 65536, os.getuid()).st_mode & 0o077 == 0, 'Private client key required')
    key_password = getpass.getpass('Пароль закрытого ключа mac-1: ')
    context = ssl.create_default_context(cafile='/etc/ssl/cert.pem')
    context.load_cert_chain(pki / 'mac-1.crt', pki / 'mac-1.key', password=key_password)
    client = Client(context)
    password = getpass.getpass('Пароль приложения acceptance: ')
    for attempt in range(3):
        machine = {'X-Budget-Admin': '1', 'Authorization': 'Basic ' + base64.b64encode(('budget:' + password).encode()).decode()}
        code, _, data = client.request('GET', '/api/ready', headers=machine)
        if code == 200:
            require(json.loads(data).get('revision') == REVISION, 'HTTPS release mismatch'); break
        require(code == 401 and attempt < 2, 'Administrative readiness failed: HTTP ' + str(code))
        password = getpass.getpass('Пароль не принят. Повторите пароль приложения: ')
    report = {'at': utc(), 'installation_id': IDENTITY, 'revision': REVISION, 'checks': []}
    report_path = mac.LOCAL / 'acceptance-live-result.json'
    streams = []; sessions = []; transport = mac.Transport(); transport.operation = 'acceptance'
    def passed(name):
        report['checks'].append(name); atomic(report_path, report); print('PASS: ' + name, flush=True)
    def expect(code, method, path, body=None, headers=None):
        actual, h, data = client.request(method, path, body, headers)
        require(actual == code, method + ' ' + path.split('?')[0] + ': HTTP ' + str(actual) + ', expected ' + str(code))
        return h, data
    def login(remember=False):
        h, data = expect(200, 'POST', '/api/auth/login', {'username': 'budget', 'password': password, 'remember': remember})
        cookie = checked_cookie(h, remember)
        headers = {'Cookie': cookie, 'X-CSRF-Token': json.loads(data)['csrf']}
        sessions.append(headers); return headers
    def stream(headers):
        c, r = client.connect('GET', '/api/events', headers=headers)
        owned = Stream(c, r); streams.append(owned)
        require(r.status == 200 and r.getheader('Content-Type') == 'text/event-stream', 'SSE response failed')
        initial = frame(r); require(initial and initial[0] == 'change', 'Initial SSE frame not streamed')
        return owned, r, initial[1]
    try:
        status, token = transport.request('status', 'acceptance'); check_target(status)
        transport.request('clean-incoming', 'acceptance', token=token)
        passed('synthetic identity, exact release and administrative Basic Auth')
        expect(200, 'GET', '/login.html')
        expect(303, 'GET', '/', headers={'Origin': '', 'Sec-Fetch-Site': 'cross-site', 'Sec-Fetch-Mode': 'navigate', 'Sec-Fetch-Dest': 'document'})
        h, _ = expect(401, 'GET', '/api/ready'); require('www-authenticate' not in h, 'Browser Basic challenge returned')
        expect(401, 'GET', '/api/ready', headers=dict(machine, **{'Sec-Fetch-Site': 'same-origin', 'Sec-Fetch-Mode': 'cors'}))
        expect(401, 'GET', '/api/events', headers=machine)
        expect(403, 'POST', '/api/auth/login', {}, {'Origin': 'https://foreign.invalid'})
        first = login(); second = login(True)
        expect(403, 'POST', '/api/auth/revoke', {}, {'Cookie': first['Cookie']})
        expect(403, 'POST', '/api/auth/revoke', {}, dict(first, Origin='https://foreign.invalid'))
        passed('HTML entry, cross-site navigation, cookie lifetime, CSRF and browser/admin separation')
        started = time.monotonic(); c, r, before = stream(first)
        report['sse_initial_seconds'] = round(time.monotonic() - started, 3)
        deadline = time.monotonic() + 15
        while True:
            beat = frame(r)
            require(beat is not None, 'SSE closed before heartbeat')
            if beat[0] == 'heartbeat': break
            require(time.monotonic() < deadline, 'Heartbeat not streamed through Caddy')
        passed('real Caddy SSE initial frame and heartbeat')
        started = time.monotonic()
        backup, token = transport.request('backup', 'acceptance')
        transport.request('clean-incoming', 'acceptance', token=token)
        transport.close()
        report['backup_seconds'] = round(time.monotonic() - started, 3)
        require(frame(r) is None, 'Old SSE did not close on service shutdown'); c.close()
        expect(200, 'GET', '/api/auth/session', headers=first)
        c, r, after = stream(first)
        require(before['generation'] != after['generation'], 'Restart did not change synchronization generation'); c.close()
        passed('backup stop/start, session persistence, SSE reconnect and changed generation')
        manifest = backup['manifest']; ident = manifest['backup_id']
        require(manifest['installation_id'] == IDENTITY, 'Backup identity mismatch')
        target = archive_destination('acceptance', ident)
        with tempfile.TemporaryDirectory(prefix='.acceptance-', dir=target.parent) as tmp:
            archive = Path(tmp) / 'archive.tar.gz'
            def connect(method, path, headers):
                return client.connect(method, path, headers=dict(machine, **headers))
            stream_download(connect, '/api/backups/' + ident + '/full', archive, backup['size'], backup['sha256'], True)
            verified = json.loads(subprocess.check_output([str(mac.local_binary()), 'admin', 'backup', 'verify', '--archive', str(archive)]))
            require(verified['installation_id'] == IDENTITY and verified['backup_id'] == ident, 'Independent archive verify mismatch')
            with tarfile.open(archive, 'r:gz') as tar:
                require(not any('web-auth' in m.name for m in tar.getmembers()), 'Auth session file in backup')
            os.chmod(archive, 0o600)
            publish(archive, target, backup['sha256'])
        report['archive'] = str(target); report['archive_sha256'] = backup['sha256']
        passed('Range download, independent native verify and auth sidecar exclusion')
        print('Проверка двух браузеров и черновиков; только синтетическая acceptance.', flush=True)
        payload = {'origin': ORIGIN, 'password': password, 'keyPassword': key_password, 'pki': str(pki)}
        child_exit, browser_result = browser_probe(payload)
        report['browser'] = browser_result; atomic(report_path, report)
        require(child_exit == 0, 'Browser acceptance failed; no automatic restore. See stage/archive in acceptance-live-result.json')
        require(browser_result.get('passed') is True, 'Browser probe did not pass')
        report['browser'] = browser_result; payload.clear(); key_password = None
        passed('two real HTTPS browser contexts: live updates, offline reconciliation and preserved financial/category drafts')
        # A no-op category replacement preserves contents while exercising concurrent CAS.
        headers, categories = expect(200, 'GET', '/api/categories', headers=first)
        revision = headers['x-budget-revision']; category_data = json.loads(categories)
        with ThreadPoolExecutor(max_workers=2) as executor:
            codes = list(executor.map(lambda _: client.request('PUT', '/api/categories', category_data, dict(first, **{'If-Match': revision}))[0], range(2)))
        require(sorted(codes) == [200, 409], 'Concurrent finance CAS did not reject stale writer')
        passed('simultaneous financial revision: exactly one commit and one HTTP 409')
        time.sleep(2); report['resources_before_churn'] = metrics()
        for _ in range(4):
            batch = [stream(first)[0] for _ in range(8)]
            for conn in batch: conn.close()
            time.sleep(.3)
        time.sleep(3); report['resources_after_churn'] = metrics()
        b = report['resources_before_churn']; a = report['resources_after_churn']
        require(b['MainPID'] == a['MainPID'] and a['NRestarts'] == b['NRestarts'], 'Unexpected app restart during SSE churn')
        require(a['backend_connections'] <= b['backend_connections'] + 2, 'Connections did not return near baseline; investigate before declaring no leak')
        require(a['rss_kib'] - b['rss_kib'] < 64 * 1024, 'Unexpected RSS growth during bounded SSE churn')
        require(a['memory_events'] == b['memory_events'], 'Memory pressure/OOM counters changed during SSE churn')
        passed('32 bounded SSE opens/closes; backend connections return near baseline')
        _, data = expect(200, 'GET', '/api/auth/session', headers=second); session_id = json.loads(data)['id']
        c, r, _ = stream(second)
        expect(200, 'POST', '/api/auth/revoke', {'id': session_id}, first)
        event = frame(r); require(event and event[0] == 'auth-required' and frame(r) is None, 'Revoked SSE remained open')
        expect(401, 'GET', '/api/auth/session', headers=second)
        expect(401, 'GET', '/api/ready', headers=dict(machine, Cookie=second['Cookie']))
        passed('revoked session closes existing SSE; stale cookie cannot fall back to Basic')
        if restore:
            print('Далее штатный restore созданного снимка acceptance: тестовые изменения будут отменены. Подтверждение и пароли вводятся в обычной команде.', flush=True)
            subprocess.run([sys.executable, str(Path(__file__).with_name('mac.py')), 'restore', '--instance', 'acceptance',
                            '--device', device, '--archive', str(target)], check=True)
            expect(401, 'GET', '/api/auth/session', headers=first)
            expect(200, 'GET', '/api/ready', headers=machine)
            passed('existing administrative restore, HTTPS proof and session invalidation')
        else:
            report['restore'] = 'pending; synthetic browser fixtures remain until explicit restore'
        report['completed_at'] = utc(); atomic(report_path, report)
        print(json.dumps(report, ensure_ascii=False, indent=2))
        print('Результаты: ' + str(report_path))
    finally:
        for conn in streams: conn.close()
        for headers in sessions:
            try: client.request('POST', '/api/auth/logout', {}, headers)
            except Exception: pass
        transport.close()
        password = key_password = None


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--restore', action='store_true', help='Invoke existing explicitly confirmed restore after tests')
    args = parser.parse_args()
    try: run(args.restore)
    except (KeyboardInterrupt, EOFError):
        print('Проверка отменена; при начавшемся restore используйте status/recover.', file=sys.stderr); sys.exit(130)
    except Exception as error:
        # Never print request bodies, subprocess stderr or browser assertion dumps.
        print('Проверка остановлена: ' + (str(error) if isinstance(error, Refused) else type(error).__name__) +
              '. Результаты и путь снимка: ~/.simple-budget-admin/acceptance-live-result.json', file=sys.stderr)
        sys.exit(1)
