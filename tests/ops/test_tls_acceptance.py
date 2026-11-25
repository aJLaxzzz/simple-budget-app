import sys
from pathlib import Path
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
import mac
import tls_acceptance
from common import Refused


class TLSAcceptanceTests(unittest.TestCase):
    def test_scope_rejected_before_remote_call(self):
        for instance,device in [('production','acceptance-tls-device'),('acceptance','mac-1'),('acceptance','phone'),('acceptance','acceptance-device')]:
            transport=Mock()
            with self.assertRaises(Refused):tls_acceptance.execute(transport,instance,device)
            transport.request.assert_not_called()

    def test_closed_socket_timeout_is_not_proof(self):
        probe=mac.RevocationProbe.__new__(mac.RevocationProbe)
        probe.socket=Mock();probe.socket.recv.side_effect=TimeoutError('synthetic timeout')
        probe.context=Mock();probe.session=None
        with self.assertRaisesRegex(Refused,'closure was not proven'):probe.verify_revoked()
        probe.socket.close.assert_called_once();probe.context.wrap_socket.assert_not_called()

    def test_live_data_on_old_socket_is_refused(self):
        probe=mac.RevocationProbe.__new__(mac.RevocationProbe)
        probe.socket=Mock();probe.socket.recv.return_value=b'H'
        probe.context=Mock();probe.session=None
        with self.assertRaisesRegex(Refused,'survived'):probe.verify_revoked()
        probe.socket.close.assert_called_once();probe.context.wrap_socket.assert_not_called()

    def test_revoked_device_is_never_readded(self):
        transport=Mock();transport.request.return_value=({'synthetic':True,'installation_id':'a'*32,'service':'running','proxy':{'mode':'open'},'clients':[{'device':'acceptance-tls-device','state':'revoked'}]},'token')
        with patch.object(mac,'local_state',return_value='unused'),patch.object(mac,'load',return_value={'instance':'acceptance','installation_id':'a'*32}):
            with self.assertRaisesRegex(Refused,'never be re-enabled'):tls_acceptance.execute(transport,'acceptance','acceptance-tls-device')
        transport.request.assert_called_once_with('status','acceptance')

    def test_negative_probe_cannot_pass_on_reset_eof_or_local_trust_failure(self):
        import ssl
        for failure in (ConnectionResetError('reset'),ssl.SSLError('local trust failed'),None):
            probe=mac.RevocationProbe.__new__(mac.RevocationProbe)
            probe.socket=None;probe.session=None;probe.context=Mock()
            connection=Mock();connection.recv.return_value=b''
            wrapped=Mock();wrapped.__enter__=Mock(return_value=connection);wrapped.__exit__=Mock(return_value=False)
            probe.context.wrap_socket.return_value=wrapped
            if failure:probe.context.wrap_socket.side_effect=failure
            with patch('socket.create_connection',return_value=Mock()):
                with self.assertRaises(Refused):probe.verify_revoked()
