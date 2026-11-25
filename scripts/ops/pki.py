"""Opt-in local issuance only. Never reads or modifies the real PKI during tests."""
import os
from pathlib import Path
import subprocess
import tempfile
import uuid
import shutil
import time
from common import *

def execute(action,device,new_name=None):
    root=Path.home()/'.simple-budget-pki'
    require(root.is_dir() and not root.is_symlink() and root.stat().st_mode&0o077==0,'Existing private ~/.simple-budget-pki required; no CA is created')
    require(NAME.fullmatch(device),'Invalid device name')
    if action=='pki-backup':
        destination=Path(input('Путь нового зашифрованного .dmg вне репозитория: ')).expanduser().absolute()
        require(not destination.exists() and destination.suffix=='.dmg','New .dmg path required')
        require(root not in destination.parents,'PKI backup must be outside PKI directory')
        repository=Path(__file__).resolve().parents[2]
        require(repository not in destination.parent.resolve().parents and destination.parent.resolve()!=repository,'PKI backup must never enter the repository or its artifacts')
        registry=Path.home()/'.simple-budget-admin/device-registry.json'
        require(registry.exists() and time.time()-registry.stat().st_mtime<86400,'Run make pki-export first; current public allowlist/revocation registry is required')
        with tempfile.TemporaryDirectory(prefix='budget-pki-backup-') as temp:
            staging=Path(temp)
            for source in root.iterdir():
                require(source.is_file() and source.suffix in ('.key','.crt','.csr','.cnf','.p12'),'Unexpected PKI entry; inspect before export')
                regular(source,1024*1024);copy_verified(source,staging/source.name,mode=0o600,limit=1024*1024)
            copy_verified(registry,staging/'device-registry.json',mode=0o600,limit=1024*1024)
            subprocess.run(['/usr/bin/hdiutil','create','-encryption','AES-256','-srcfolder',str(staging),'-format','UDZO',str(destination)],check=True)
        return
    regular(root/'ca.crt',64<<10);regular(root/'ca.key',64<<10)
    require((root/'ca.key').stat().st_mode&0o077==0,'CA key must be private')
    require(b'ENCRYPTED' in read(root/'ca.key',64<<10),'CA private key must be encrypted')
    name=new_name if action=='client-renew' else device
    require(name and NAME.fullmatch(name),'Renewal requires NAME=new-versioned-device-name')
    for ext in ('key','csr','crt','p12'):require(not (root/(name+'.'+ext)).exists(),'Never overwrite an existing device identity')
    old=os.umask(0o077)
    try:
        openssl='/usr/bin/openssl'
        def call(args):subprocess.run([openssl,*map(str,args)],check=True)
        call(['genpkey','-algorithm','RSA','-pkeyopt','rsa_keygen_bits:3072','-aes-256-cbc','-out',root/(name+'.key')])
        call(['req','-new','-key',root/(name+'.key'),'-subj','/CN='+name,'-out',root/(name+'.csr')])
        with tempfile.TemporaryDirectory(prefix='.issue-',dir=root) as tmp:
            ext=Path(tmp)/'client.ext';ext.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=clientAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n')
            call(['x509','-req','-in',root/(name+'.csr'),'-CA',root/'ca.crt','-CAkey',root/'ca.key','-set_serial','0x'+uuid.uuid4().hex,'-days','365','-sha256','-extfile',ext,'-out',root/(name+'.crt')])
        call(['verify','-purpose','sslclient','-CAfile',root/'ca.crt',root/(name+'.crt')])
        call(['pkcs12','-export','-inkey',root/(name+'.key'),'-in',root/(name+'.crt'),'-certfile',root/'ca.crt','-out',root/(name+'.p12'),'-name',name])
    finally:os.umask(old)
    print('Удостоверение создано локально:',name)
    print('Импортируйте .p12 на устройство, затем client-add. Старое удостоверение не отозвано автоматически.')
