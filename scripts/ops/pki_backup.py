"""Create/verify/mount-check/detach then publish AES-256 PKI images."""
import getpass
import json
import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import time
from common import *
from local_backups import dmg_destination, publish
import secret_store

def registry_snapshot(path):
    st=regular(path,1<<20,os.getuid())
    require(st.st_mode&0o077==0 and 0<=time.time()-st.st_mtime<86400,
            'Run make pki-export ENV=production first: registry must be private and less than 24 hours old')
    value=load(path)
    require(set(value)=={'format','clients'} and value['format']==1 and isinstance(value['clients'],dict) and value['clients'],'Invalid device registry')
    for name,item in value['clients'].items():
        require(NAME.fullmatch(name) and item.get('state') in ('allowed','revoked') and SHA.fullmatch(item.get('fingerprint','')),'Invalid registry entry')
        require(not item.get('revocation_pending'),'Finish pending revocation before PKI backup')
    return value

def hdi(args, password=None):
    p=subprocess.run(['/usr/bin/hdiutil',*args],input=(password.encode()+b'\0') if password is not None else None,
                     stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=300,
                     env=secret_store.clean_environment())
    require(p.returncode==0,'hdiutil '+args[0]+' failed (exit '+str(p.returncode)+'); image not published')
    return p.stdout

def create(root,explicit=None):
    root=Path(root);destination=dmg_destination(explicit)
    require(root.resolve()!=destination.parent.resolve() and root.resolve() not in destination.resolve().parents,'Backup cannot be inside PKI')
    registry=Path.home()/'.simple-budget-admin/device-registry.json';value=registry_snapshot(registry)
    password=secret_store.password('pki','Пароль восстановления PKI DMG (минимум 12, рекомендуется 16): ')
    if not secret_store.SESSION or secret_store.SESSION.mode=='manual':
        require(password==getpass.getpass('Повторите пароль восстановления: '),'Passwords do not match')
    require(len(password)>=12 and len(password.encode())<=4096 and not any(c in password for c in '\0\r\n'),'Invalid recovery password')
    print('Сохраните пароль восстановления отдельно от этого Mac. Создание и проверка PKI-копии…')
    try:
        with tempfile.TemporaryDirectory(prefix='.pki-',dir=destination.parent) as temp:
            staging=Path(temp)/'files';staging.mkdir(mode=0o700)
            expected={}
            for source in root.iterdir():
                require(source.suffix in ('.key','.crt','.csr','.cnf','.p12'),'Unexpected PKI file')
                data=read(source,1<<20,os.getuid())
                if source.suffix=='.key':
                    require(data.startswith(b'-----BEGIN ENCRYPTED PRIVATE KEY-----') or
                            (data.startswith(b'-----BEGIN RSA PRIVATE KEY-----') and b'Proc-Type: 4,ENCRYPTED' in data and b'DEK-Info:' in data),
                            'Unencrypted or unsupported private key refused')
                    require(source.stat().st_mode&0o077==0,'Private key permissions required')
                if source.suffix=='.p12':
                    # Reject unencrypted/empty-password containers without extracting a key.
                    probe=subprocess.run(['/usr/bin/openssl','pkcs12','-in',str(source),'-info','-noout','-passin','stdin'],
                        input=b'\n',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
                    require(probe.returncode!=0,'Empty-password PKCS#12 refused')
                copy_verified(source,staging/source.name,mode=0o600,limit=1<<20)
                expected[source.name]=sha(staging/source.name)
            require('ca.key' in expected and 'ca.crt' in expected,'Existing CA files required')
            atomic(staging/'device-registry.json',value);expected['device-registry.json']=sha(staging/'device-registry.json')
            image=Path(temp)/'unpublished.dmg'
            hdi(['create','-encryption','AES-256','-stdinpass','-srcfolder',str(staging),'-format','UDZO',str(image)],password)
            os.chmod(image,0o600)
            hdi(['verify','-stdinpass',str(image)],password)
            # A fixed private mountpoint lets finally detach even if plist parsing fails.
            mount=Path(temp)/'mounted';mount.mkdir(mode=0o700)
            attached=False
            try:
                attached=True
                raw=hdi(['attach','-readonly','-nobrowse','-owners','on','-stdinpass','-mountpoint',str(mount),'-plist',str(image)],password)
                info=plistlib.loads(raw)
                require(any(Path(x['mount-point']).resolve()==mount.resolve() for x in info.get('system-entities',[]) if x.get('mount-point')),'Unexpected image mount')
                require(all((mount/name).is_file() for name in expected),'PKI image is incomplete')
                for name,digest in expected.items():require(sha(mount/name)==digest,'PKI image content mismatch')
            finally:
                if attached:
                    # Failure is surfaced; do not silently leave a mounted image or publish it.
                    p=subprocess.run(['/usr/bin/hdiutil','detach',str(mount)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
                    if p.returncode!=0 and os.path.ismount(mount):
                        hdi(['detach','-force',str(mount)])
                    require(not os.path.ismount(mount),'Image still mounted; detach it before retrying')
            publish(image,destination)
        print('Проверенная зашифрованная PKI-копия:',destination)
        return destination
    finally:password=None
