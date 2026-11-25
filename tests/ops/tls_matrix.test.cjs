'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {execFileSync,spawn}=require('node:child_process'),readline=require('node:readline');
const {Matrix,certificateAlert}=require('../../scripts/ops/tls_matrix.cjs');

test('TLS probe accepts only peer certificate alerts, never timeouts/reset/local trust errors',()=>{
  for(const code of ['ERR_SSL_SSLV3_ALERT_BAD_CERTIFICATE','ERR_SSL_TLSV1_ALERT_UNKNOWN_CA','ERR_SSL_TLSV13_ALERT_CERTIFICATE_REQUIRED'])assert.equal(certificateAlert({code}),true);
  for(const code of ['ECONNRESET','ETIMEDOUT','ECONNREFUSED','UNABLE_TO_VERIFY_LEAF_SIGNATURE','ERR_SSL_TLSV1_ALERT_PROTOCOL_VERSION'])assert.equal(certificateAlert({code}),false);
});

test('real TLS1.2/1.3 h1/h2, closed channels, no tickets and rejected replay; offline is not proof',{timeout:90000},async()=>{
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'budget-tls-matrix-'));let child,matrix;
  try{
    const openssl=args=>execFileSync('/usr/bin/openssl',args,{cwd:root,stdio:'ignore'});
    for(const name of ['ca','other'])openssl(['req','-x509','-sha256','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN='+name,'-keyout',name+'.key','-out',name+'.crt']);
    fs.writeFileSync(path.join(root,'server.ext'),'subjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n');
    fs.writeFileSync(path.join(root,'client.ext'),'extendedKeyUsage=clientAuth\n');
    for(const [name,serial] of [['server','11'],['client','12']]){
      openssl(['req','-new','-newkey','rsa:2048','-nodes','-subj','/CN='+name,'-keyout',name+'.key','-out',name+'.csr']);
      openssl(['x509','-req','-sha256','-in',name+'.csr','-CA','ca.crt','-CAkey','ca.key','-set_serial',serial,'-days','2','-extfile',name+'.ext','-out',name+'.crt']);
    }
    fs.copyFileSync(path.join(__dirname,'tls_matrix_server.go.txt'),path.join(root,'server.go'));
    execFileSync('go',['build','-o',path.join(root,'server'),path.join(root,'server.go')],{stdio:'pipe'});
    child=spawn(path.join(root,'server'),['server.crt','server.key','ca.crt','other.crt'],{cwd:root,stdio:['pipe','pipe','ignore']});
    const lines=readline.createInterface({input:child.stdout})[Symbol.asyncIterator]();
    const next=async()=>{const result=await lines.next();assert.equal(result.done,false);return result.value;};
    const address=await next();const port=Number(address.split(':')[1]);
    const options={host:'127.0.0.1',port,servername:'',rejectUnauthorized:true,ca:fs.readFileSync(path.join(root,'ca.crt')),cert:fs.readFileSync(path.join(root,'client.crt')),key:fs.readFileSync(path.join(root,'client.key'))};
    matrix=new Matrix(options,'127.0.0.1');
    const prepared=await matrix.prepare();assert.equal(prepared.length,4);
    assert.deepEqual(new Set(prepared.map(p=>p.tls+'/'+p.alpn)),new Set(['TLSv1.2/http/1.1','TLSv1.2/h2','TLSv1.3/http/1.1','TLSv1.3/h2']));
    matrix.arm();child.stdin.write('revoke\n');assert.equal(await next(),address);
    const result=await matrix.verify();assert.equal(result.old_connections_closed,4);assert.equal(result.negative_connections.length,6);
    assert.equal(result.negative_connections.every(c=>certificateAlert({code:c.certificate_alert})),true);
    child.stdin.write('stop\n');assert.equal(await next(),'stopped');
    await assert.rejects(()=>matrix.verify(),/NO_CERTIFICATE_REJECTION_ALERT/);
  }finally{
    matrix?.close();child?.stdin.end();if(child&&!child.exitCode)child.kill();
    fs.rmSync(root,{recursive:true,force:true});
  }
});
