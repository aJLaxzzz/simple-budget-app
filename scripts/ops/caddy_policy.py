"""Strict validation of the already installed IP/mTLS Caddy configuration.
No ACME account/key operations. JSON is derived from the operator's Caddyfile.
"""
import copy
from common import IP, require

ACME_ROOT='/var/lib/caddy/ip-acme-production'
CA='/etc/caddy/pki/ca.crt'

def leaves(policy):
    a=policy.get('client_authentication',{})
    require(a.get('mode')=='require_and_verify', 'Every TLS policy must require verified mTLS')
    require(a.get('ca')=={'provider':'file','pem_files':[CA]}, 'Unexpected client CA pool')
    v=a.get('verifiers',[])
    require(len(v)==1 and v[0].get('verifier')=='leaf', 'Exact leaf verifier required')
    loaders=v[0].get('leaf_certs_loaders',[])
    require(len(loaders)==1 and loaders[0].get('loader')=='file', 'File leaf allowlist required')
    files=loaders[0].get('files',[])
    require(files and len(files)==len(set(files)), 'Empty/duplicate allowlist')
    return set(files)

def walk(obj):
    if isinstance(obj,dict):
        yield obj
        for v in obj.values(): yield from walk(v)
    elif isinstance(obj,list):
        for v in obj: yield from walk(v)

def validate(config, expected, managed=False):
    require(set(config.get('apps',{}))=={'http','tls'}, 'Unexpected Caddy application/module')
    require(config.get('storage')=={'module':'file_system','root':ACME_ROOT}, 'ACME storage changed')
    servers=config.get('apps',{}).get('http',{}).get('servers',{})
    require(servers, 'HTTP servers missing')
    dual=any(':8443' in s.get('listen',[]) for s in servers.values())
    tls_policies=[]; hosts=[]; fallback=False; ports=[]; fallback_route=False
    for server in servers.values():
        listen=server.get('listen',[]);ports.extend(listen)
        require(all(x in (':80',':443',':8443') for x in listen), 'Unexpected listener')
        require(not server.get('logs'), 'Access logging requires separate privacy review')
        if ':443' in listen or ':8443' in listen:
            require(set(server.get('protocols',[]))=={'h1','h2'}, 'Only h1/h2 allowed')
            policies=server.get('tls_connection_policies',[])
            require(policies, 'TLS policy missing')
            for policy in policies:
                require(leaves(policy)==set(expected), 'Allowlist differs between TLS policies')
                require(not policy.get('insecure_secrets_log'), 'TLS key logging forbidden')
                tls_policies.append(policy)
            fallback=any(not p.get('match') for p in policies)
            require(fallback,'Each HTTPS listener requires protected fallback TLS')
        else:
            require(':80' in listen and not server.get('tls_connection_policies'), 'Unexpected cleartext server')
            for node in walk(server.get('routes',[])):
                if 'handler' in node:
                    require(node['handler'] in ('static_response','subroute'), 'HTTP must not expose application')
                    if node['handler']=='static_response': require(node.get('status_code')==404,'HTTP fallback must be 404')
        for route in server.get('routes',[]):
            if (':443' in listen or ':8443' in listen) and not route.get('match'):
                fallback_route=True
                for node in walk(route.get('handle',[])):
                    if 'handler' in node:
                        require(node['handler'] in ('static_response','subroute','headers'),'Unmatched Host must not reach application')
                        if node['handler']=='static_response':require(node.get('status_code')==404,'HTTPS fallback must be 404')
            for matcher in route.get('match',[]):
                if 'host' in matcher: hosts.extend(matcher['host'])
    require(sorted(ports)==([':443',':80',':8443'] if dual else [':443',':80']) and fallback_route,'Expected one HTTP and HTTPS listener with fallback 404')
    require(tls_policies and fallback, 'Protected default TLS policy required')
    require(hosts==([IP,IP] if dual else [IP]), 'Exactly one canonical HTTP Host route required')
    automation=config.get('apps',{}).get('tls',{}).get('automation',{}).get('policies',[])
    require(automation, 'ACME automation missing')
    for policy in automation:
        issuers=policy.get('issuers',[])
        require(issuers and all(i.get('module')=='acme' and i.get('ca')=='https://acme-v02.api.letsencrypt.org/directory'
                and i.get('profile')=='shortlived' and i.get('challenges',{}).get('tls-alpn',{}).get('disabled') is True
                for i in issuers), 'Unexpected issuer/profile/challenge or issuer fallback')
    if dual:
        validate_isolation(servers)
    if managed:
        require(config.get('admin',{}).get('disabled') is True, 'Administrative HTTP API must be disabled')
        require(config['apps']['tls'].get('session_tickets',{}).get('disabled') is True, 'Session tickets must be explicitly disabled')
    return {'tls_policies':len(tls_policies),'allowlist':sorted(expected),'session_tickets_explicitly_disabled':config.get('apps',{}).get('tls',{}).get('session_tickets',{}).get('disabled',False)}

def render(base, expected, mode, port):
    require(mode in ('closed','trial','open','test-page'), 'Invalid proxy mode')
    require(port in (3001,3002), 'Unknown upstream')
    out=copy.deepcopy(base)
    # Update all policies from the current registry, never from an old config backup.
    for server in out['apps']['http']['servers'].values():
        for policy in server.get('tls_connection_policies',[]):
            policy['client_authentication']['verifiers']=[{'verifier':'leaf','leaf_certs_loaders':[{'loader':'file','files':sorted(expected)}]}]
        for route in server.get('routes',[]):
            if route.get('match')==[{'host':[IP]}]:
                proxy={'handler':'reverse_proxy','upstreams':[{'dial':'127.0.0.1:'+str(port)}]}
                headers={'handler':'headers','response':{'set':{'Cache-Control':['no-store'],'X-Content-Type-Options':['nosniff'],'X-Frame-Options':['DENY']}}}
                closed={'handler':'static_response','status_code':503,'body':'Maintenance. Please try again later.','headers':{'Retry-After':['60']}}
                actions=[headers,proxy if mode=='open' else closed]
                if mode=='test-page': actions=[headers,{'handler':'static_response','status_code':200,'body':'Simple Budget: secure device access works!'}]
                if mode=='trial': actions=[headers,{'handler':'subroute','routes':[{'match':[{'path':['/api/ready'],'method':['GET','HEAD']}],'handle':[proxy],'terminal':True},{'handle':[closed]}]}]
                route['handle']=actions; route['terminal']=True
    out['admin']={'disabled':True}
    out['apps']['tls']['session_tickets']={'disabled':True}
    validate(out,expected,True)
    return out

def cookie_proxy(port, with_cookie):
    name='__Host-budget-stg-session' if port==3002 else '__Host-budget-session'
    # Rebuild Cookie from one parsed value. Never forward the browser's full
    # Cookie header (cookies themselves have no port boundary).
    request={'set':{'Cookie':[name+'={http.request.cookie.'+name+'}']}} if with_cookie else {'delete':['Cookie']}
    # Every Set-Cookie value is confined to this environment's sole cookie name,
    # including multiple headers from a faulty/adversarial staging backend.
    response={'delete':['Clear-Site-Data','Set-Cookie2'],
              'replace':{'Set-Cookie':[{'search_regexp':'^[^=]*=','replace':name+'='}]}}
    return {'handler':'reverse_proxy','upstreams':[{'dial':'127.0.0.1:'+str(port)}],
            'flush_interval':-1,'headers':{'request':request,'response':response}}

def isolated_handlers(port):
    name='__Host-budget-stg-session' if port==3002 else '__Host-budget-session'
    return [{'handler':'subroute','routes':[
        {'match':[{'header_regexp':{'Cookie':{'pattern':r'(^|;\s*)'+name+r'='}}}],
         'handle':[cookie_proxy(port,True)],'terminal':True},
        {'handle':[cookie_proxy(port,False)],'terminal':True}]}]

def browser_boundary():
    # Ports are same-site, but not same-origin. Enforce this at the trusted
    # proxy too, including while PROD still runs a pre-STG release.
    navigation={'method':['GET'],'path':['/','/index.html','/login.html'],
                'header':{'Sec-Fetch-Mode':['navigate'],'Sec-Fetch-Dest':['document']}}
    return {'handler':'subroute','routes':[{'match':[{
        'header':{'Sec-Fetch-Site':['same-site','cross-site']},'not':[navigation]}],
        'handle':[{'handler':'static_response','status_code':403,'body':'Cross-origin access denied.',
                   'headers':{'Cache-Control':['no-store']}}],'terminal':True}]}

def staging_handlers():
    gate=lambda name:{'file':{'root':'/etc/caddy','try_files':[name]}}
    return [browser_boundary(),{'handler':'subroute','routes':[
        {'match':[gate('stg-access-test-page')],'handle':[{'handler':'static_response','status_code':200,
          'body':'Simple Budget STG: secure device access works!','headers':{'Cache-Control':['no-store']}}],'terminal':True},
        {'match':[gate('stg-access-open')],'handle':isolated_handlers(3002),'terminal':True},
        {'match':[dict(gate('stg-access-trial'),path=['/api/ready'],method=['GET','HEAD'])],
         'handle':isolated_handlers(3002),'terminal':True},
        {'handle':[{'handler':'static_response','status_code':503,'body':'STG stopped or updating.',
                    'headers':{'Cache-Control':['no-store'],'Retry-After':['30']}}]}]}]

def render_dual(base, expected, mode='open'):
    out=render(base,expected,mode,3001)
    servers=out['apps']['http']['servers']
    production=next(s for s in servers.values() if ':443' in s['listen'])
    staging=copy.deepcopy(production)
    staging['listen']=[':8443']
    # Both listeners share the existing IP certificate/ACME storage.
    for server,port in ((production,3001),(staging,3002)):
        # IP clients omit SNI. All TLS policies require mTLS and Host is checked
        # independently by the canonical route and the application.
        server['strict_sni_host']=False
        route=next(r for r in server['routes'] if r.get('match')==[{'host':[IP]}])
        if port==3001:
            for node in list(walk(route['handle'])):
                if node.get('handler')=='reverse_proxy':
                    node.clear();node.update(isolated_handlers(port)[0])
            route['handle'].insert(0,browser_boundary())
        else:
            route['handle']=staging_handlers()
    servers['staging']=staging
    validate(out,expected,True)
    return out

def validate_isolation(servers):
    for server in servers.values():
        if ':80' in server['listen']:continue
        port=3002 if ':8443' in server['listen'] else 3001
        routes=[r for r in server['routes'] if r.get('match')==[{'host':[IP]}]]
        require(len(routes)==1 and routes[0].get('terminal') is True,'Canonical route must be terminal')
        require(routes[0]['handle'][0]==browser_boundary(),'Cross-port browser boundary changed')
        if port==3002:require(routes[0]['handle']==staging_handlers(),'STG routes/gates/cookie boundary changed')
        for node in walk(server.get('routes',[])):
            if node.get('handler')=='reverse_proxy':
                require(node in (cookie_proxy(port,True),cookie_proxy(port,False)),
                        'Proxy cookie isolation/upstream contract changed')
