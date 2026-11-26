import contextlib
import ssl
import shutil
import subprocess
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'scripts/ops'))
import mac
from common import Refused


class ClientPasswordRetryTests(unittest.TestCase):
    def error(self, reason='PEM_LIB'):
        error = ssl.SSLError('synthetic encrypted key error'); error.reason = reason
        return error

    @contextlib.contextmanager
    def fixture(self, results, mode='manual'):
        with tempfile.TemporaryDirectory() as root, contextlib.ExitStack() as stack:
            pki = Path(root)/'.simple-budget-pki'; pki.mkdir(mode=0o700)
            for name in ('mac-1.key', 'mac-1.crt'):
                (pki/name).write_text('synthetic encrypted fixture'); (pki/name).chmod(0o600)
            contexts = []
            for result in results:
                context = Mock()
                def load(*args, password, result=result):
                    password()
                    if result is not None: raise result
                context.load_cert_chain.side_effect = load; contexts.append(context)
            stack.enter_context(patch.object(mac.Path, 'home', return_value=Path(root)))
            stack.enter_context(patch.object(mac.secret_store, 'SESSION', SimpleNamespace(mode=mode)))
            prompt = stack.enter_context(patch.object(mac.secret_store, 'password', return_value='synthetic-secret'))
            factory = stack.enter_context(patch.object(mac.ssl, 'create_default_context', side_effect=contexts))
            output = stack.enter_context(patch('builtins.print'))
            yield contexts, prompt, factory, output

    def test_typo_reprompts_and_creates_fresh_tls_context(self):
        with self.fixture([self.error(), None]) as (contexts, prompt, factory, output):
            self.assertIs(mac.client_context(), contexts[1])
            self.assertEqual(prompt.call_count, 2)
            self.assertIn('попытка 2 из 3', prompt.call_args.args[1])
            self.assertEqual(factory.call_count, 2)
            self.assertNotIn('synthetic-secret', str(output.call_args_list))
            self.assertEqual(contexts[1].minimum_version, ssl.TLSVersion.TLSv1_2)

    def test_three_failures_stop_without_secret_in_error(self):
        with self.fixture([self.error() for _ in range(3)]) as (_, prompt, factory, _):
            with self.assertRaises(Refused) as raised: mac.client_context()
            self.assertIn('status/recover', str(raised.exception))
            self.assertNotIn('synthetic-secret', str(raised.exception))
            self.assertEqual(prompt.call_count, 3); self.assertEqual(factory.call_count, 3)

    def test_openssl_text_without_reason_retries_but_other_errors_do_not(self):
        error=ssl.SSLError(524297,'[SSL] PEM lib (_ssl.c:4065)');error.reason=None
        with self.fixture([error,None]) as (contexts,prompt,_,_):
            self.assertIs(mac.client_context(),contexts[1]);self.assertEqual(prompt.call_count,2)
        for text in ('[SSL] unknown error (_ssl.c:4065)','PEM lib in an unrelated message'):
            error=ssl.SSLError(text);error.reason=None
            with self.fixture([error]) as (_,prompt,_,_):
                with self.assertRaises(ssl.SSLError):mac.client_context()
                prompt.assert_called_once()

    @unittest.skipUnless(shutil.which('openssl'),'openssl required for encrypted synthetic key')
    def test_native_encrypted_key_wrong_password_then_success(self):
        with tempfile.TemporaryDirectory(prefix='budget-client-retry-') as tmp:
            pki=Path(tmp)/'.simple-budget-pki';pki.mkdir(mode=0o700)
            key=pki/'mac-1.key';cert=pki/'mac-1.crt'
            subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-keyout',str(key),'-out',str(cert),'-days','1','-subj','/CN=synthetic-retry-test','-passout','stdin'],input=b'synthetic-correct-passphrase\n',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
            key.chmod(0o600)
            with patch.object(mac.Path,'home',return_value=Path(tmp)),patch.object(mac.secret_store,'SESSION',SimpleNamespace(mode='manual')),patch.object(mac.secret_store,'password',side_effect=['synthetic-wrong-passphrase','synthetic-wrong-passphrase','synthetic-correct-passphrase']) as prompt,patch('builtins.print') as output:
                self.assertIsInstance(mac.client_context(),ssl.SSLContext)
                self.assertEqual(prompt.call_count,3)
                self.assertNotIn('synthetic-correct-passphrase',str(output.call_args_list))

    def test_keychain_error_never_falls_back_to_manual(self):
        with self.fixture([self.error()], 'keychain') as (_, prompt, factory, _):
            with self.assertRaises(Refused): mac.client_context()
            prompt.assert_called_once(); factory.assert_called_once()

    def test_cancel_and_other_tls_errors_are_not_retried(self):
        for error in (KeyboardInterrupt(), self.error('CERTIFICATE_VERIFY_FAILED')):
            with self.subTest(error=type(error)), self.fixture([error]) as (_, prompt, factory, _):
                with self.assertRaises(type(error)): mac.client_context()
                prompt.assert_called_once(); factory.assert_called_once()


if __name__ == '__main__': unittest.main()
