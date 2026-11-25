import io
from pathlib import Path
import sys
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'scripts/ops'))
import live_acceptance as live
from common import Refused


class LiveAcceptanceTests(unittest.TestCase):
    def target(self):
        return {'instance': 'acceptance', 'synthetic': True, 'installation_id': live.IDENTITY,
                'release': {'revision': live.REVISION, 'modified': False, 'schema_version': 14,
                            'files': [{'sha256': live.DIGEST}]},
                'proxy': {'instance': 'acceptance', 'mode': 'open', 'configuration_matches': True}}

    def test_exact_synthetic_target_and_release_required(self):
        live.check_target(self.target())
        edits = [('instance', 'production'), ('synthetic', False), ('installation_id', '0'*32),
                 ('deployment_journal', True), ('restore_journal', True), ('backup_operation_pending', True)]
        for key, value in edits:
            status = self.target(); status[key] = value
            with self.subTest(key=key), self.assertRaises(Refused): live.check_target(status)
        for group, key, value in [('release', 'modified', True), ('release', 'revision', 'wrong'),
                                 ('release', 'schema_version', 15), ('proxy', 'instance', 'production'),
                                 ('proxy', 'mode', 'maintenance'), ('proxy', 'configuration_matches', False)]:
            status = self.target(); status[group][key] = value
            with self.subTest(key=key), self.assertRaises(Refused): live.check_target(status)
        status = self.target(); status['release']['files'][0]['sha256'] = '0'*64
        with self.assertRaises(Refused): live.check_target(status)

    def test_cookie_security_and_lifetime(self):
        base = live.COOKIE + '=synthetic-token; Path=/; Secure; HttpOnly; SameSite=Strict'
        self.assertEqual(live.checked_cookie({'set-cookie': base}, False), live.COOKIE+'=synthetic-token')
        live.checked_cookie({'set-cookie': base+'; Max-Age=2592000'}, True)
        for value, remember in [(base.replace('; Secure', ''), False), (base.replace('; HttpOnly', ''), False),
                                (base.replace('Strict', 'None'), False), (base+'; Domain=example.org', False),
                                (base+'; Max-Age=2592000', False), (base+'; Max-Age=2592001', True), (base, True)]:
            with self.subTest(value=value), self.assertRaises(Refused):
                live.checked_cookie({'set-cookie': value}, remember)

    def test_sse_parser_bounds_and_eof(self):
        self.assertEqual(live.frame(io.BytesIO(b'event: heartbeat\ndata: {}\n\n')), ('heartbeat', {}))
        self.assertIsNone(live.frame(io.BytesIO(b'')))
        with self.assertRaises(Refused): live.frame(io.BytesIO(b'x'*8193))

    def test_stream_closes_response_as_well_as_connection(self):
        c, r = Mock(), Mock()
        live.Stream(c, r).close()
        c.close.assert_called_once(); r.close.assert_called_once()

    def test_production_refused_before_io(self):
        with patch.dict('os.environ', {'ENV': 'production', 'INSTANCE': 'production', 'SUDO_MODE': 'session'}), \
                patch.object(live.mac, 'local_state') as state, self.assertRaises(Refused):
            live.run(False)
        state.assert_not_called()

    def test_browser_secrets_only_on_pipe_and_private_process_group(self):
        child = Mock(returncode=0); child.communicate.return_value = (b'{"passed":true}', b'')
        with patch.object(live.subprocess, 'Popen', return_value=child) as popen:
            self.assertEqual(live.browser_probe({'password':'synthetic-secret'}), (0, {'passed': True}))
        args, kwargs = popen.call_args
        self.assertNotIn('synthetic-secret', str(args))
        self.assertNotIn('synthetic-secret', str(kwargs['env']))
        self.assertTrue(kwargs['start_new_session'])
        self.assertIn(b'synthetic-secret', child.communicate.call_args.args[0])

    def test_cancel_terminates_only_owned_browser_group(self):
        child = Mock(pid=123456); child.communicate.side_effect = [KeyboardInterrupt(), (b'', b'')]
        with patch.object(live.subprocess, 'Popen', return_value=child), patch.object(live.os, 'killpg') as kill:
            with self.assertRaises(KeyboardInterrupt): live.browser_probe({'password':'synthetic'})
        kill.assert_called_once_with(123456, live.signal.SIGTERM)

    def test_http_request_secret_not_in_exception_and_connection_closed(self):
        c = Mock(); c.request.side_effect = OSError('synthetic request failure')
        with patch.object(live.http.client, 'HTTPSConnection', return_value=c), self.assertRaises(OSError):
            live.Client(Mock()).connect('POST', '/api/auth/login', {'password': 'synthetic secret'})
        c.close.assert_called_once()


if __name__ == '__main__': unittest.main()
