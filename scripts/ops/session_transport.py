"""Mac side of a single TTY sudo session; transfers still use verified SSH."""
import json
import os
import pty
import select
import sys
import termios
import tty
import time
from pathlib import Path
import shlex
import subprocess
import tempfile
import uuid
from common import *

# Fixed, unprivileged wait program. Arguments are validated IDs/sequence only.
WAIT='''import json,os,stat,sys,time
p='.simple-budget-incoming/'+sys.argv[1]+'/reply.json'
seq=int(sys.argv[2]); end=time.monotonic()+float(sys.argv[3])
while time.monotonic()<end:
 try:
  s=os.lstat(p)
  if not stat.S_ISREG(s.st_mode) or s.st_nlink!=1 or s.st_size>4096: sys.exit(3)
  with open(p) as f: d=json.load(f)
  if d.get('sequence')==seq or d.get('state')=='expired':
   print(json.dumps(d));sys.exit(0)
 except (FileNotFoundError,ValueError): pass
 time.sleep(.2)
sys.exit(4)
'''
def check_protocol(transport):
    if getattr(transport,'_session_protocol',False) is True:return
    p=subprocess.run([*transport.ssh,'budget','/usr/local/sbin/budget-admin session-protocol'],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    require(p.returncode==0 and p.stdout.strip()==b'1',
            'Server session protocol not installed or SSH unavailable. Obtain approval for server-install, or use SUDO_MODE=legacy explicitly. No mutation started.')
    transport._session_protocol=True

class Session:
    def __init__(self,transport,operation,instance,identity):
        self.t=transport;self.operation=operation;self.instance=instance;self.identity=identity
        self.token=uuid.uuid4().hex;self.sequence=0;self.process=None;self.closed=False;self.master=None
    def wait(self,timeout=2800,interactive=False):
        require(ID.fullmatch(self.token),'Invalid session token')
        argv=['/usr/bin/python3','-I','-c',WAIT,self.token,str(self.sequence),str(timeout)]
        # A distinct private PTY owns the persistent SSH stdin. It MUST NOT keep
        # reading the user's terminal while local getpass/Touch ID runs.
        if self.master is None:
            p=subprocess.run([*self.t.ssh,'budget',shlex.join(argv)],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=None,timeout=timeout+20)
            require(p.returncode==0,'SSH session reply unavailable; check status/recover, do not repeat the mutation')
            data=p.stdout
        else:
            waiter=subprocess.Popen([*self.t.ssh,'-o','BatchMode=yes','budget',shlex.join(argv)],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE)
            terminal=None;settings=None;data=bytearray();end=time.monotonic()+timeout+20
            try:
                if interactive:
                    require(sys.stdin.isatty(),'An interactive terminal is required for manual remote sudo')
                    terminal=sys.stdin.fileno();settings=termios.tcgetattr(terminal);tty.setraw(terminal)
                while waiter.poll() is None:
                    require(time.monotonic()<end,'SSH session wait timed out; check status/recover')
                    sources=[self.master,waiter.stdout]
                    if terminal is not None:sources.append(terminal)
                    ready,_,_=select.select(sources,[],[],.2)
                    for source in ready:
                        if source==terminal:
                            chunk=os.read(terminal,4096)
                            if not chunk or b'\x03' in chunk:raise KeyboardInterrupt()
                            os.write(self.master,chunk) # manually typed input, never a cached sudo password
                        elif source==self.master:
                            try:chunk=os.read(self.master,4096)
                            except OSError:chunk=b''
                            if chunk:os.write(sys.stdout.fileno(),chunk)
                        else:
                            data.extend(os.read(waiter.stdout.fileno(),4096))
                            require(len(data)<=4096,'Oversized session reply')
                data.extend(waiter.stdout.read(4097))
                require(waiter.returncode==0 and len(data)<=4096,'SSH session reply unavailable; check status/recover')
            finally:
                if settings is not None:termios.tcsetattr(terminal,termios.TCSADRAIN,settings)
                if waiter.poll() is None:waiter.terminate()
                try:waiter.wait(timeout=3)
                except subprocess.TimeoutExpired:waiter.kill();waiter.wait()
                waiter.stdout.close()
        result=json.loads(data)
        require(result.get('sequence')==self.sequence and result.get('exit')==0,'Administrative session failed or expired; use status/recover')
        return result
    def start(self):
        # Compatibility negotiation is read-only and does not request sudo.
        check_protocol(self.t)
        self.t.command(['mkdir','-p','-m','700','.simple-budget-incoming/'+self.token])
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'request.json';atomic(path,{'action':'session','instance':self.instance,'request_id':self.token,
                'operation':self.operation,'installation_id':self.identity});self.t.upload(path,self.token,'request.json')
        print('SSH: одна административная сессия для '+self.instance+'. Введите sudo-пароль в терминале.')
        require(sys.stdin.isatty(),'Run this command in your terminal for interactive sudo')
        self.master,slave=pty.openpty()
        try:
            self.process=subprocess.Popen([*self.t.ssh,'-tt','budget',shlex.join(['sudo','/usr/local/sbin/budget-admin','session',self.instance,self.token])],stdin=slave,stdout=slave,stderr=slave)
        finally:os.close(slave)
        try:self.wait(120,interactive=True)
        except BaseException:self.close(abort=True);raise
    def execute(self,action,token):
        if self.process is None:self.start()
        require(self.process.poll() is None,'Administrative TTY session ended; use status/recover')
        self.sequence+=1
        self.send({'sequence':self.sequence,'action':action,'token':token})
        self.wait(interactive=action=='initialize')
    def send(self,value):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'command.json';atomic(path,value)
            # SCP a separate file then rename, so root never parses a partially transferred command.
            self.t.upload(path,self.token,'next.json')
            self.t.command(['mv','.simple-budget-incoming/'+self.token+'/next.json','.simple-budget-incoming/'+self.token+'/command.json'])
    def close(self,abort=False):
        if self.closed:return
        self.closed=True
        if self.process is None:return
        try:
            if not abort and self.process.poll() is None:
                self.sequence+=1;self.send({'sequence':self.sequence,'action':'close'});self.wait(15)
        finally:
            if self.process.poll() is None:self.process.terminate()
            try:self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:self.process.kill();self.process.wait()
            if self.master is not None:os.close(self.master);self.master=None
