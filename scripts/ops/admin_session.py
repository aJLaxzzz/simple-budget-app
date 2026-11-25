"""One sudo invocation, a bounded fixed-operation broker. No shell and no credential cache.
The broker never holds operations.lock: every existing handler acquires it itself.
"""
import os
from pathlib import Path
import pwd
import subprocess
import time
from common import *

PLANS={
 'deploy':{'prepare','commit'}, 'initialize':{'initialize','commit'},
 'migrate-data':{'initialize','commit'}, 'restore':{'restore','commit'},
 'rollback':{'rollback','commit'}, 'recover':{'recover','commit'},
 'fetch-backup':{'status','fetch-backup'},
 'client-add':{'client-add','pki-export'},
 'client-revoke':{'client-list','client-revoke','access-stop','pki-export'},
 'acceptance':{'status','backup'},
 'acceptance-http':{'status'},
 'acceptance-tls':{'status','client-add','client-revoke','access-stop','pki-export'},
}
for _action in ('status','logs','backup','tls-status','client-list','pki-export','cleanup','secure-page'):
    PLANS[_action]={_action}

class SessionPolicy:
    def __init__(self,operation,instance,identity):
        require(operation in PLANS and instance in ('acceptance','production'),'Invalid session plan')
        require(ID.fullmatch(identity or ''),'Session requires installation ID')
        self.allowed=PLANS[operation]|{'clean-incoming'};self.instance=instance;self.identity=identity
        self.tokens=set();self.changed=False
    def validate(self,command,request):
        require(set(command)=={'sequence','action','token'} and ID.fullmatch(command.get('token','')),'Invalid session command')
        action=command['action'];token=command['token']
        require(action in self.allowed and request.get('action')==action and request.get('instance')==self.instance,'Action outside approved session plan')
        require(request.get('installation_id',self.identity)==self.identity,'Session installation mismatch')
        if action in ('commit','clean-incoming'):require(token in self.tokens,'Unrelated operation token refused')
        # Only one data-changing deployment generation per session.
        if action in ('initialize','prepare','restore','rollback','recover'):
            require(not self.changed,'Repeated mutation refused; use a new explicit recovery');self.changed=True
        self.tokens.add(token)


def serve(instance,token,root='/',run=subprocess.run,clock=time.monotonic,sleep=time.sleep):
    from remote import request_for
    p=Paths(instance,root)
    incoming,req=request_for(token,'session',instance)
    require(set(req)=={'action','instance','request_id','operation','installation_id'},'Invalid session fields')
    policy=SessionPolicy(req['operation'],instance,req['installation_id'])
    if req['operation'] not in ('initialize','migrate-data'):
        require(p.identity()==policy.identity,'Wrong installed identity')
    uid=pwd.getpwnam(ADMIN).pw_uid;gid=pwd.getpwnam(ADMIN).pw_gid
    def reply(value):
        atomic(incoming/'reply.json',value);os.chown(incoming/'reply.json',uid,gid)
    started=clock();last=started;sequence=0
    reply({'sequence':0,'exit':0,'state':'ready','protocol':1})
    try:
        while clock()-started<1800 and clock()-last<120:
            path=incoming/'command.json'
            if not path.exists():sleep(.2);continue
            cmd=load(path,4096,uid)
            require(type(cmd.get('sequence')) is int,'Invalid sequence')
            if cmd['sequence']==sequence:sleep(.2);continue
            require(cmd['sequence']==sequence+1 and sequence<32,'Replayed/out-of-order session command')
            sequence+=1;last=clock()
            if cmd=={'sequence':sequence,'action':'close'}:
                reply({'sequence':sequence,'exit':0,'state':'closed'});return
            _,request=request_for(cmd.get('token',''),cmd.get('action',''),instance)
            policy.validate(cmd,request)
            print('Этап: '+cmd['action']+' / '+instance,flush=True)
            result=run(['/usr/local/sbin/budget-admin',cmd['action'],instance,cmd['token']],timeout=2700,stdout=subprocess.PIPE)
            reply({'sequence':sequence,'exit':result.returncode,'state':'done'})
            if result.returncode==0:
                response=load(p.at('/home/budgetadmin/.simple-budget-incoming')/cmd['token']/'result.json') if cmd['action']!='clean-incoming' else {}
                if isinstance(response,dict) and response.get('result')=='awaiting_https_acceptance':
                    pending=response.get('request_id','')
                    require(ID.fullmatch(pending),'Invalid pending request ID');policy.tokens.add(pending)
            last=clock()
            if result.returncode: return # no replay or fallback after a failed mutation
        reply({'sequence':sequence,'exit':1,'state':'expired'})
    finally:
        # Only our three non-secret protocol files. Never remove operation evidence.
        for name in ('request.json','command.json'):
            path=incoming/name
            if path.exists():regular(path,16384,uid);path.unlink()
        # Keep reply for a racing client; stale unprivileged session directory is harmless.
