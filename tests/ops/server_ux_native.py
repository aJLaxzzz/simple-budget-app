"""macOS integration: synthetic encrypted keys/DMG only; never real Keychain secrets or VPS."""
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from common import atomic,sha,Refused
import pki_backup as pb
import secret_store as ss

with tempfile.TemporaryDirectory(prefix='budget-ux-native-') as tmp:
    home=Path(tmp);root=home/'.simple-budget-pki';root.mkdir(mode=0o700)
    secret='Synthetic-test-only-16!'
    subprocess.run(['/usr/bin/openssl','genpkey','-algorithm','RSA','-pkeyopt','rsa_keygen_bits:2048',
                    '-aes-256-cbc','-pass','stdin','-out',str(root/'ca.key')],input=(secret+'\n').encode(),
                   check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    (root/'ca.key').chmod(0o600)
    subprocess.run(['/usr/bin/openssl','req','-new','-x509','-sha256','-key',str(root/'ca.key'),'-passin','stdin',
                    '-subj','/CN=Synthetic-local-backup-test','-days','1','-out',str(root/'ca.crt')],
                   input=(secret+'\n').encode(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    (root/'ca.crt').chmod(0o600)
    local=home/'.simple-budget-admin';local.mkdir(mode=0o700)
    atomic(local/'device-registry.json',{'format':1,'clients':{'synthetic':{'state':'revoked','fingerprint':'a'*64}}})
    before=sha(root/'ca.key')
    with patch.object(Path,'home',return_value=home),patch.object(pb.secret_store,'password',return_value=secret),patch.object(pb.getpass,'getpass',return_value=secret):
        result=pb.create(root)
        assert sha(root/'ca.key')==before
        info=plistlib.loads(pb.hdi(['isencrypted','-plist',str(result)]))
        assert info['encrypted'] is True,info
        try:pb.hdi(['verify','-stdinpass',str(result)],'incorrect-synthetic-password')
        except Refused:pass
        else:raise AssertionError('Wrong DMG password accepted')
        assert not any(x.name.startswith('.pki-') for x in result.parent.iterdir())
        # Same explicit filename must refuse, never overwrite.
        digest=sha(result)
        try:pb.create(root,str(result))
        except Refused:pass
        else:raise AssertionError('Existing archive overwritten')
        assert sha(result)==digest
    attachments=plistlib.loads(pb.hdi(['info','-plist']))
    assert not any(str(home) in str(x) for x in attachments.get('images',[])),'Image left attached'
print('PASS: real AES-encrypted synthetic DMG create/verify/open/detach; wrong password; no overwrite; source unchanged')
print('Native helper capabilities (no biometric prompt or real secret access):')
print(json.dumps(ss.capabilities(),sort_keys=True))
