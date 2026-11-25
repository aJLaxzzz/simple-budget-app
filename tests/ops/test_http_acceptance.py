import hashlib
import io
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import Mock

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts/ops'))
from http_acceptance import stream_download, execute
from common import Refused


class HTTPAcceptanceTests(unittest.TestCase):
    def fixture(self, raw, mutate=None):
        digest=hashlib.sha256(raw).hexdigest();connections=[];requests=[]
        def connect(method,path,headers):
            span=headers.get('Range');start,end=(int(x) for x in span[6:].split('-')) if span else (0,len(raw)-1)
            response=Mock(status=206 if span else 200)
            values={'Cache-Control':'no-store, no-store','Content-Disposition':'attachment; filename="fixture.tar.gz"','Content-Range':'bytes '+str(start)+'-'+str(end)+'/'+str(len(raw)),'ETag':'"'+digest+'"'}
            data=raw[start:end+1]
            if mutate:data=mutate(response,values,data)
            response.getheader.side_effect=values.get;response.read.side_effect=io.BytesIO(data).read
            c=Mock();connections.append(c);requests.append(headers);return c,response
        return connect,digest,connections,requests
    def test_two_ranges_reassemble_exact_verified_bytes(self):
        raw=b'a'*1024+b'last-part'
        connect,digest,connections,requests=self.fixture(raw)
        with tempfile.TemporaryDirectory() as tmp:
            target=Path(tmp)/'archive';stream_download(connect,'/fixture',target,len(raw),digest,True)
            self.assertEqual(target.read_bytes(),raw)
        self.assertEqual(requests,[{'Range':'bytes=0-1023'},{'Range':'bytes=1024-1032'}])
        for c in connections:c.close.assert_called_once()
    def test_invalid_status_policy_range_hash_and_truncation_refused(self):
        def change_header(key,value):
            def mutate(r,h,d):h[key]=value;return d
            return mutate
        def status(r,h,d):r.status=200;return d
        mutations=[status,change_header('Cache-Control','public'),change_header('Content-Disposition','inline'),change_header('Content-Range','bytes 1-2/3'),change_header('ETag','"foreign"'),lambda r,h,d:d[:-1],lambda r,h,d:b'x'+d[1:],lambda r,h,d:d+b'extra']
        for mutate in mutations:
            with self.subTest(mutate=mutate),tempfile.TemporaryDirectory() as tmp:
                raw=b'a'*1500;connect,digest,connections,_=self.fixture(raw,mutate)
                with self.assertRaises(Refused):stream_download(connect,'/fixture',Path(tmp)/'archive',len(raw),digest,True)
                for c in connections:c.close.assert_called_once()
    def test_scope_and_size_guards_before_io(self):
        transport=Mock()
        for instance,device in [('production','mac-1'),('acceptance','acceptance-tls-device')]:
            with self.assertRaises(Refused):execute(transport,instance,device)
        transport.request.assert_not_called()
        connect=Mock()
        for size in (0,-1,65<<20):
            with self.assertRaises(Refused):stream_download(connect,'/fixture',Path('/unused'),size,'x',True)
        connect.assert_not_called()
