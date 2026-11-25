#!/usr/bin/env python3
"""One-shot VPS acceptance. Fixed synthetic instance; no production/Caddy writes.

Run with sudo python3 -I after review. Uses installed administrative modules and
the installed clean ELF. The only live mutation is a normal manual backup. All
load, corruption and restore checks run in a fresh, separate /var/tmp directory.
Never prints credentials, HTTP bodies or financial/planner rows.
"""
import base64
import fcntl
import hashlib
import http.client
import json
import os
from pathlib import Path
import pwd
import secrets
import socket
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time

EXPECTED_ID = '6919a32cbb5e4cd09a2a00f691e32812'


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def database_digest(path):
    """Offline only: canonical contents including IDs/history/BLOBs, not DB bytes."""
    require(path.is_file() and not path.is_symlink(), 'Snapshot missing or linked')
    h = hashlib.sha256()
    counts = {}
    with sqlite3.connect(path.as_uri() + '?mode=ro&immutable=1', uri=True) as db:
        require(db.execute('PRAGMA integrity_check').fetchall() == [('ok',)], 'SQLite integrity')
        require(db.execute('PRAGMA foreign_key_check').fetchall() == [], 'SQLite foreign keys')
        tables = db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").fetchall()
        for (name,) in tables:
            quoted = '"' + name.replace('"', '""') + '"'
            columns = len(db.execute('SELECT * FROM ' + quoted + ' LIMIT 0').description)
            rows = db.execute('SELECT * FROM ' + quoted + ' ORDER BY ' + ','.join(str(i+1) for i in range(columns)))
            count = 0
            h.update(json.dumps(name).encode())
            for row in rows:
                encoded = [({'blob': x.hex()} if isinstance(x, bytes) else x) for x in row]
                h.update(json.dumps(encoded, ensure_ascii=True, separators=(',', ':')).encode())
                count += 1
            counts[name] = count
    return {'sha256': h.hexdigest(), 'table_counts': counts}


def rss(pid, field='VmRSS'):
    try:
        for line in Path('/proc/' + str(pid) + '/status').read_text().splitlines():
            if line.startswith(field + ':'):
                return int(line.split()[1])
    except (FileNotFoundError, ProcessLookupError):
        pass
    return 0


def invoke(args, *, env=None, uid=None, gid=None, cwd=None, expected=0, timeout=180):
    """Bounded child; safe error contains executable name/exit only."""
    started = time.monotonic()
    options = dict(user=uid, group=gid, extra_groups=[]) if uid is not None else {}
    process = subprocess.Popen([str(x) for x in args], stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, env=env, cwd=cwd, **options)
    peak = 0
    try:
        while True:
            peak = max(peak, rss(process.pid, 'VmHWM'))
            try:
                out, err = process.communicate(timeout=0.05)
                break
            except subprocess.TimeoutExpired:
                if time.monotonic() - started > timeout:
                    raise RuntimeError('Timed out: ' + Path(args[0]).name)
        require((process.returncode == 0) if expected == 0 else (process.returncode != 0),
                'Unexpected exit from ' + Path(args[0]).name + ': ' + str(process.returncode))
        return out, {'seconds': round(time.monotonic()-started, 3),
                     'sampled_peak_rss_kib': peak, 'exit': process.returncode}
    finally:
        if process.poll() is None:
            process.kill()
            process.communicate()


def access_as(uid, gid, entries):
    code = 'import os,json,sys; print(json.dumps([os.access(p,m) for p,m in json.loads(sys.argv[1])]))'
    out, _ = invoke(['/usr/bin/python3', '-I', '-c', code, json.dumps(entries)], uid=uid, gid=gid)
    return json.loads(out)


def api(port, password, method, path, body=None, expected=200):
    c = http.client.HTTPConnection('127.0.0.1', port, timeout=30)
    started = time.monotonic()
    try:
        headers = {'Host': '171.22.237.16', 'Origin': 'https://171.22.237.16',
                   'X-Budget-Admin':'1','Authorization': 'Basic ' + base64.b64encode(('budget:'+password).encode()).decode()}
        if body is not None:
            headers['Content-Type'] = 'application/json'
        if method not in ('GET','HEAD') and path.startswith(('/api/months','/api/categories')):
            c.request('GET','/api/months',headers=headers); current=c.getresponse(); current.read(); headers['If-Match']=current.getheader('X-Budget-Revision','')
        c.request(method, path, body=None if body is None else json.dumps(body), headers=headers)
        response = c.getresponse()
        raw = response.read(16 << 20)
        require(response.status in ([expected] if isinstance(expected, int) else expected),
                'HTTP assertion: ' + method + ' ' + path.split('?')[0] + ' status ' + str(response.status))
        return (json.loads(raw) if raw else None), round((time.monotonic()-started)*1000, 2)
    finally:
        c.close()


def run():
    require(sys.platform == 'linux' and os.geteuid() == 0, 'Run on VPS with sudo')
    sys.path.insert(0, '/usr/local/lib/simple-budget/ops')
    from common import Paths, load, lock, space, atomic, sha
    from remote import Admin
    p = Paths('acceptance')
    identity = load(p.etc / 'identity.json')
    require(identity.get('synthetic') is True and p.identity() == EXPECTED_ID, 'Only expected synthetic acceptance installation allowed')
    user = pwd.getpwnam(p.user)
    report = {'format': 1, 'installation_id': EXPECTED_ID, 'observed_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), 'checks': {}}
    with lock(p.global_state / 'operations.lock'):
        require(not p.journal.exists() and not (p.state / '.live.restore.json').exists()
                and not (p.backups / 'operation.json').exists(), 'Pending recovery: stop acceptance')
        a = Admin(p)
        require(a.service_state() == 'running', 'Acceptance must already be running')
        report['release'] = a.manifest(p.release())
        a.ready()
        report['tls_policies'] = a.proxy_guard()
        space(p.backups, 256 << 20)
        before_config = sha(p.at('/etc/caddy/simple-budget.json'))
        before_registry = sha(p.global_state / 'clients.json')
        # Access probes use real UID/GID without privilege; no attempts to modify files.
        protected = [p.release(), p.release()/'budget', p.backups, p.state,
                     p.etc/'credentials', p.backups/'pins', p.backups/'staging',
                     p.at('/etc/caddy/pki'), p.global_state]
        require(not any(access_as(user.pw_uid, user.pw_gid, [(str(x), os.W_OK) for x in protected])), 'Web UID can write protected path')
        private = [p.etc/'credentials/app-password', p.backups/'pins', p.backups/'staging', p.at('/etc/caddy/pki')]
        require(not any(access_as(user.pw_uid, user.pw_gid, [(str(x), os.R_OK) for x in private])), 'Web UID can read administrative secrets/staging')
        require(access_as(user.pw_uid, user.pw_gid, [(str(p.live), os.W_OK)])[0], 'Web UID cannot write live')
        require(user.pw_shell in ('/usr/sbin/nologin', '/bin/false'), 'Unexpected interactive shell')
        groups = os.getgrouplist(p.user, user.pw_gid)
        require(groups == [user.pw_gid], 'Unexpected supplemental service groups')
        report['checks']['os_permissions'] = 'passed'
        for name in (p.state, p.live/'planner.db.lock'):
            fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW)
            try:
                try:
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    pass
                else:
                    raise RuntimeError('Expected live flock not held')
            finally:
                os.close(fd)
        report['checks']['live_parent_and_sqlite_flock'] = 'passed'
        _, report['maintenance_refusal'] = invoke([a.binary(), 'admin', 'check', '--data-dir', p.live,
                                                    '--installation-id', EXPECTED_ID], expected=1, timeout=10)
        pid = int(a.system('show', p.unit, '--property=MainPID', '--value').stdout)
        report['idle_rss_kib'] = {'app': rss(pid), 'caddy': rss(int(a.system('show', 'caddy.service', '--property=MainPID', '--value').stdout))}
        # Controller is the installed real systemd stop/start/readiness implementation.
        samples = []
        stop_poll = threading.Event()
        def observe():
            while not stop_poll.is_set():
                try:
                    with socket.create_connection(('127.0.0.1', 3002), timeout=0.3):
                        up = True
                except OSError:
                    up = False
                samples.append((time.monotonic(), up)); stop_poll.wait(0.05)
        thread = threading.Thread(target=observe, daemon=True); thread.start()
        start = time.monotonic()
        try:
            receipt = a.backup('manual', False, True)
        finally:
            stop_poll.set(); thread.join(timeout=2)
        a.ready()
        down = [t for t, up in samples if not up]
        report['live_backup'] = {'id': receipt['manifest']['backup_id'], 'sha256': receipt['sha256'],
                                'seconds': round(time.monotonic()-start, 3),
                                'observed_tcp_unavailable_span_seconds': round(max(down)-min(down), 3) if down else 0,
                                'poll_interval_seconds': 0.05, 'sample_count': len(samples)}
        archive = a.archive(receipt['manifest']['backup_id'])
        require(access_as(user.pw_uid, user.pw_gid, [(str(archive), os.R_OK), (str(archive), os.W_OK), (str(archive.parent), os.W_OK)]) == [True, False, False], 'Published backup permissions')
        with tempfile.TemporaryDirectory(prefix='budget-runtime-', dir='/var/tmp') as temp:
            root = Path(temp); os.chmod(root, 0o755)
            sandbox(root, a.binary(), archive, user, report)
        require(sha(p.at('/etc/caddy/simple-budget.json')) == before_config and sha(p.global_state/'clients.json') == before_registry, 'Unexpected proxy/registry change')
        a.ready()
        report['checks']['acceptance_running_after_probes'] = 'passed'
        target = p.global_state/'acceptance-results'; target.mkdir(mode=0o700, exist_ok=True)
        atomic(target/('runtime-'+time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'.json'), report)
    print(json.dumps(report, indent=2))


def sandbox(root, binary, source, user, report):
    state = root/'state'; state.mkdir(mode=0o750); os.chown(state, 0, user.pw_gid)
    live = state/'live'; live.mkdir(mode=0o700); os.chown(live, user.pw_uid, user.pw_gid)
    backups = root/'backups'; backups.mkdir(mode=0o750); os.chown(backups, 0, user.pw_gid)
    password = secrets.token_urlsafe(24)
    credential = root/'password'; credential.write_text(password); os.chmod(credential,0o600); os.chown(credential,user.pw_uid,user.pw_gid)
    base = ['--data-dir', live, '--backup-dir', backups, '--installation-id', EXPECTED_ID]
    def cli(action, extra=(), expected=0):
        out, metrics = invoke([binary, 'admin', 'backup', action, *base, *extra], expected=expected)
        return (json.loads(out) if out and expected==0 else None), metrics
    restored, report['isolated_restore'] = cli('restore', ['--archive', source, '--scope', 'full', '--confirm'])
    baseline = database_digest(live/'planner.db')
    report['restored_baseline'] = baseline
    # Non-empty synthetic financial fixtures only in the disposable clone.
    (live/'data.json').write_text('{"months":[{"id":"runtime-month","month":11,"year":2026,"income":100,"incomes":[],"expenses":[]}]}')
    (live/'categories.json').write_text('{"expense":[{"id":"runtime-category","name":"Synthetic","subcategories":[]}],"income":[]}')
    financial = {n: hashlib.sha256((live/n).read_bytes()).hexdigest() for n in ('data.json','categories.json')}
    listener=socket.socket(); listener.bind(('127.0.0.1',0)); port=listener.getsockname()[1]; listener.close()
    env={'PATH':'/usr/bin:/bin','TZ':'Europe/Moscow','GOMAXPROCS':'1','GOMEMLIMIT':'192MiB',
         'BUDGET_ENV':'production','BUDGET_ADDR':'127.0.0.1:'+str(port),'BUDGET_DATA_DIR':str(live),
         'BUDGET_BACKUP_DIR':str(backups),'BUDGET_INSTALLATION_ID':EXPECTED_ID,
         'BUDGET_PASSWORD_FILE':str(credential),'BUDGET_PUBLIC_ORIGIN':'https://171.22.237.16',
         'BUDGET_ALLOWED_HOSTS':'171.22.237.16','BUDGET_PROXY_MODE':'loopback-https','BUDGET_MIGRATIONS':'check-only'}
    # Fail-closed process checks use only the separate restored installation.
    for name, changes in [('no_password', {'BUDGET_PASSWORD_FILE':''}),
                          ('wrong_identity', {'BUDGET_INSTALLATION_ID':'0'*32}),
                          ('missing_live', {'BUDGET_DATA_DIR':str(root/'absent')}),
                          ('test_vs_production', {'BUDGET_ENV':'test'})]:
        _, metrics = invoke([binary], env=dict(env,**changes), uid=user.pw_uid,gid=user.pw_gid,cwd=root,expected=1,timeout=10)
        report['checks']['startup_'+name] = metrics
    require(not (root/'absent').exists(), 'Startup created missing storage')
    process=None
    def start():
        nonlocal process
        t=time.monotonic()
        process=subprocess.Popen([str(binary)],cwd=root,env=env,user=user.pw_uid,group=user.pw_gid,extra_groups=[],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        for _ in range(100):
            require(process.poll() is None,'Isolated application exited during startup')
            try:
                api(port,password,'GET','/api/ready');return round(time.monotonic()-t,3)
            except OSError:
                time.sleep(0.05)
        raise RuntimeError('Isolated readiness timeout')
    def stop():
        nonlocal process
        if process is not None:
            process.terminate()
            try: process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill();process.wait();raise RuntimeError('Graceful shutdown timeout')
            require(process.returncode==0,'Isolated graceful shutdown failed')
            process=None
    try:
        report['isolated_start_seconds']=start()
        created=[]; first=None; t=time.monotonic()
        for i in range(100):
            project,_=api(port,password,'POST','/api/planner/projects',{'title':'Runtime load '+str(i)},(200,201))
            for j in range(10):
                body={'title':'Load task '+str(j),'project_id':project['id'],'planned_date':'2026-12-19'}
                if j%2: body['parent_task_id']=created[-1]
                task,_=api(port,password,'POST','/api/planner/tasks',body,(200,201))
                if first is None: first=task
                created.append(task['id'])
        report['load']={'projects_added':100,'tasks_added':1000,'seed_seconds':round(time.monotonic()-t,3),'api':{}}
        for path in ('/tasks?status=active','/projects','/calendar?from=2026-12-01&to=2026-12-31'):
            data,ms=api(port,password,'GET','/api/planner'+path)
            report['load']['api'][path]={'milliseconds':ms,'response_bytes':len(json.dumps(data).encode())}
            if path.startswith('/tasks'):
                require(set(created).issubset({x['id'] for x in data['items']}),'Load tasks missing')
        # Work periods and custom icons must survive along with IDs and recurrence.
        api(port,password,'POST','/api/planner/sessions',{'task_id':first['id'],'date':'2026-12-19','start_time':'10:00','end_time':'11:00'},(200,201))
        import struct, zlib
        def chunk(kind, data):
            return struct.pack('!I',len(data))+kind+data+struct.pack('!I',zlib.crc32(kind+data)&0xffffffff)
        png=b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('!2I5B',2,2,8,6,0,0,0))+chunk(b'IDAT',zlib.compress((b'\0'+b'\xff\0\0\xff'*2)*2))+chunk(b'IEND',b'')
        api(port,password,'POST','/api/planner/icons',{'name':'Runtime synthetic icon','data':base64.b64encode(png).decode()},(200,201))
        materialize_start=time.monotonic()
        for i in range(20):
            api(port,password,'POST','/api/planner/series',{'request_id':'runtime-series-'+secrets.token_hex(8),'task':{'title':'Runtime recurrence '+str(i)},'first_date':'2026-11-01','rule':{'schema_version':1,'mode':'schedule','unit':'days','interval':1,'allowed_days':{'mode':'any'},'adjustment':'none'}},(200,201))
        for _ in range(100):
            result,_=api(port,password,'POST','/api/planner/materialize',{})
            require(not any(x.get('error') for x in result.get('results',[])),'Materializer per-series error')
            if not result.get('pending') and not result.get('next_after_series_id'): break
        else: raise RuntimeError('Materializer did not converge')
        report['load']['twenty_series_materialization_seconds']=round(time.monotonic()-materialize_start,3)
        report['load']['app_peak_rss_kib']=rss(process.pid,'VmHWM')
        report['load']['wal_bytes']=(live/'planner.db-wal').stat().st_size
        stop()
        expected=database_digest(live/'planner.db')
        report['load']['offline_contents']=expected
        snapshot, report['load_backup']=cli('create',['--reason','manual'])
        copy=backups/'manual'/snapshot['manifest']['backup_id']/'backup.tar.gz'
        _,report['load_verify']=invoke([binary,'admin','backup','verify','--archive',copy])
        require(financial=={n:hashlib.sha256((live/n).read_bytes()).hexdigest() for n in financial},'Finance changed during load')
        # No SQL update of the running DB: corruption is confined to stopped clone.
        (live/'planner.db').write_bytes(b'isolated synthetic corrupt SQLite')
        result,report['corrupt_restore']=cli('restore',['--archive',copy,'--scope','full','--confirm'])
        require(database_digest(live/'planner.db')==expected,'Restored rows/IDs/history differ')
        require(Path(result['previous_data']).is_dir(),'Previous corrupt generation not retained')
        require(financial=={n:hashlib.sha256((live/n).read_bytes()).hexdigest() for n in financial},'Restored finance differs')
        report['restart_seconds']=start()
        for ident in (created[0],created[-1]):
            data,_=api(port,password,'GET','/api/planner/tasks/'+ident);require(data['id']==ident,'ID missing after restart')
        stop()
        report['checks']['isolated_corrupt_restore_and_restart']='passed'
        # Real small filesystem in a private mount namespace, never fills root FS.
        small=root/'small';small.mkdir(mode=0o700)
        code='import subprocess,sys; subprocess.run(["/usr/bin/mount","--make-rprivate","/"],check=True); subprocess.run(["/usr/bin/mount","-t","tmpfs","-o","size=16m,nr_inodes=128,mode=0700","tmpfs",sys.argv[1]],check=True); p=subprocess.run(sys.argv[2:],capture_output=True); sys.exit(0 if p.returncode!=0 and b"insufficient free space" in p.stderr else 1)'
        _,report['small_filesystem_refusal']=invoke(['/usr/bin/unshare','--mount','/usr/bin/python3','-I','-c',code,small,binary,'admin','backup','create','--data-dir',live,'--backup-dir',small,'--installation-id',EXPECTED_ID,'--reason','manual'])
        report['checks']['small_filesystem_refusal']='passed'
    finally:
        stop()


if __name__ == '__main__':
    try:
        run()
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        print('Acceptance stopped: '+str(error), file=sys.stderr)
        sys.exit(1)
