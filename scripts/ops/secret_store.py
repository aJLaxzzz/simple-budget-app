"""Opt-in DP Keychain. Secrets travel only over an inherited private socketpair."""
import getpass
import json
import os
from pathlib import Path
import socket
import subprocess
from common import *

HELPER=Path(__file__).resolve().parents[2]/'build/server-ux/budget-secrets'
SESSION=None

def scope(instance,kind,device,identity):
    require(instance in ('acceptance','production') and ID.fullmatch(identity or ''),'Known installation ID required for saved secrets')
    require(kind in ('app','client','pki') and NAME.fullmatch(device),'Invalid secret purpose/device')
    return '/'.join((IP,instance,identity,kind,device if kind=='client' else 'default'))

def capabilities():
    require(HELPER.exists(),'Run make keychain-build first')
    regular(HELPER,32<<20,os.getuid())
    raw=subprocess.check_output([str(HELPER),'status'],env=clean_environment(),timeout=30)
    return json.loads(raw)

def clean_environment():
    # No injection/debug/key logging settings inherited by the native helper.
    return {k:v for k,v in os.environ.items() if k in ('HOME','USER','LOGNAME','TMPDIR','LANG','PATH')}

class Store:
    def __init__(self):
        require(capabilities().get('storage_available') is True,
                'Biometric Data Protection Keychain unavailable. Use AUTH=manual; no secret was saved/read.')
        self.sock,child=socket.socketpair();self.sock.settimeout(150)
        try:
            self.process=subprocess.Popen([str(HELPER),'channel',str(child.fileno())],pass_fds=(child.fileno(),),
                stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,env=clean_environment())
        finally:child.close()
        self.stream=self.sock.makefile('rwb',buffering=0)
    def request(self,op,account,value=None):
        payload={'op':op,'account':account}
        if value is not None:payload['value']=value
        self.stream.write(json.dumps(payload).encode()+b'\n')
        line=self.stream.readline(16385)
        require(line.endswith(b'\n') and len(line)<=16384,'Native authorization session ended')
        result=json.loads(line)
        require('error' not in result,'Touch ID cancelled or unavailable; operation stopped. Retry explicitly with AUTH=manual.')
        require(result.get('status')==0,'Keychain item unavailable/locked or access refused (status '+str(result.get('status'))+'). Use explicit manual mode or register again.')
        if op=='get':require(isinstance(result.get('value'),str) and 0<len(result['value'].encode())<=4096,'Invalid Keychain item')
        return result.get('value')
    def close(self):
        self.stream.close();self.sock.close()
        try:self.process.wait(timeout=3)
        except subprocess.TimeoutExpired:self.process.terminate();self.process.wait(timeout=3)

class Operation:
    def __init__(self,instance,device,mode='manual',identity=None):
        require(mode in ('manual','keychain'),'AUTH must be manual or keychain')
        self.instance=instance;self.device=device;self.mode=mode;self.identity=identity;self.store=None;self.cache={}
    def get(self,kind,prompt,device=None):
        if self.mode=='manual':return getpass.getpass(prompt)
        account=scope(self.instance,kind,device or self.device,self.identity)
        if account in self.cache:return self.cache[account]
        if self.store is None:
            print('macOS: требуется биометрическое разрешение для этой операции.',file=__import__('sys').stderr)
            self.store=Store()
        value=self.store.request('get',account);self.cache[account]=value;return value
    def close(self):
        self.cache.clear()
        if self.store:self.store.close();self.store=None

def password(kind,prompt,device=None):
    return SESSION.get(kind,prompt,device) if SESSION else getpass.getpass(prompt)

def manage(action,instance,device,kind,identity):
    if action=='keychain-status':
        print(json.dumps(capabilities(),ensure_ascii=False,indent=2));return
    account=scope(instance,kind,device,identity)
    require(capabilities().get('storage_available') is True,'Biometric storage unavailable on this build; use AUTH=manual. No secret requested.')
    require(action in ('keychain-register','keychain-delete','keychain-check'),'Unknown Keychain action')
    value=None
    if action=='keychain-register':
        value=getpass.getpass('Секрет для '+instance+'/'+kind+' (ввод скрыт): ')
        require(value==getpass.getpass('Повторите секрет: '),'Secrets do not match')
        require(0<len(value.encode())<=4096 and not any(c in value for c in '\r\n\0'),'Invalid secret')
        if kind in ('app','pki'):require(len(value)>=12,'Minimum 12 characters; recommend 16 or more')
    if action=='keychain-delete':
        require(input('Удалить сохранённый секрет '+account+'? Введите УДАЛИТЬ: ')=='УДАЛИТЬ','Cancelled')
    store=Store()
    try:store.request({'keychain-register':'put','keychain-delete':'delete','keychain-check':'get'}[action],account,value)
    finally:store.close();value=None
    print('Keychain: операция выполнена; значение не выводится.')
