'use strict';
// Local acceptance worker. Secrets arrive only over stdin, never argv/env/files.
// Native Node TLS + HTTP/2, no npm dependency and no executable on the VPS.
const tls=require('node:tls'),http=require('node:http'),http2=require('node:http2');
const fs=require('node:fs'),readline=require('node:readline');
const delay=ms=>new Promise(r=>setTimeout(r,ms));
function check(ok,code){if(!ok)throw new Error(code);}
const variants=['TLSv1.2','TLSv1.3'].flatMap(version=>['http/1.1','h2'].map(alpn=>({version,alpn})));
const certificateAlert=e=>/^ERR_SSL_.*ALERT_(BAD_CERTIFICATE|UNKNOWN_CA|CERTIFICATE_UNKNOWN|CERTIFICATE_REQUIRED|ACCESS_DENIED)$/.test(e?.code||'');

function open(options,v,session){
  const socket=tls.connect({...options,minVersion:v.version,maxVersion:v.version,ALPNProtocols:[v.alpn],session});
  const ch={socket,version:v.version,alpn:v.alpn,sessions:[],closed:false,agent:null,h2:null};
  socket.on('session',s=>ch.sessions.push(s));
  socket.on('close',()=>{ch.closed=true;});
  // Keep errors observed even between requests; no unhandled process exit.
  socket.on('error',()=>{});
  ch.destroy=()=>{ch.h2?.destroy();ch.agent?.destroy();socket.destroy();};
  return ch;
}

function request(ch,host,timeout=10000){
  return new Promise((resolve,reject)=>{
    let done=false,bytes=0,req;
    const timer=setTimeout(()=>finish(new Error('PROBE_TIMEOUT')),timeout);
    function finish(err,status){if(done)return;done=true;clearTimeout(timer);ch.socket.off('error',onError);err?reject(err):resolve(status);}
    const onError=e=>finish(e);ch.socket.on('error',onError);
    function connected(){
      try{
        check(ch.socket.authorized,'SERVER_CERTIFICATE_NOT_TRUSTED');
        check(ch.socket.getProtocol()===ch.version&&ch.socket.alpnProtocol===ch.alpn,'NEGOTIATION_MISMATCH');
        if(ch.alpn==='h2'){
          ch.h2=http2.connect('https://'+host,{createConnection:()=>ch.socket});
          ch.h2.on('error',onError);
          req=ch.h2.request({':method':'GET',':path':'/api/ready',':authority':host});
          let status;
          req.on('response',headers=>{status=headers[':status'];});
          req.on('data',data=>{bytes+=data.length;if(bytes>16384)finish(new Error('RESPONSE_TOO_LARGE'));});
          req.on('end',()=>finish(null,status));req.on('error',onError);req.end();
        }else{
          ch.agent=new http.Agent({keepAlive:true});ch.agent.createConnection=()=>ch.socket;
          req=http.request({host,path:'/api/ready',method:'GET',agent:ch.agent,headers:{Host:host,Connection:'keep-alive'}},res=>{
            res.on('data',data=>{bytes+=data.length;if(bytes>16384)finish(new Error('RESPONSE_TOO_LARGE'));});
            res.on('end',()=>finish(null,res.statusCode));res.on('error',onError);
          });req.on('error',onError);req.end();
        }
      }catch(e){finish(e);}
    }
    ch.socket.once('secureConnect',connected);
  });
}

class Matrix {
  constructor(options,host){this.options=options;this.host=host;this.channels=[];this.armed=false;}
  async prepare(){
    try{
      for(const v of variants){
        const ch=open(this.options,v);this.channels.push(ch);
        const status=await request(ch,this.host);
        check(status===401,'APP_PASSWORD_NOT_REQUIRED_OR_NOT_READY');
      }
      // TLS1.3 tickets arrive after secureConnect; keep reading actual responses.
      await delay(1000);
      for(const ch of this.channels){
        check(!ch.closed,'PRE_REVOKE_CONNECTION_CLOSED');
        check(!ch.socket.getTLSTicket(),'UNEXPECTED_SESSION_TICKET');
        check(ch.version!=='TLSv1.3'||ch.sessions.length===0,'UNEXPECTED_TLS13_SESSION_TICKET');
        ch.session=ch.version==='TLSv1.2'?ch.socket.getSession():undefined;
      }
      return this.channels.map(ch=>({tls:ch.version,alpn:ch.alpn,http_without_password:401,ticket_observed:false,
        resumable_tls13_session:false,tls12_session_state:!!ch.session}));
    }catch(e){this.close();throw e;}
  }
  arm(){check(this.channels.length===4&&this.channels.every(ch=>!ch.closed),'OLD_CHANNEL_ALREADY_CLOSED');this.armed=true;return {armed:true};}
  async verify(){
    check(this.armed,'PROBE_NOT_ARMED');
    const end=Date.now()+5000;
    while(this.channels.some(ch=>!ch.closed)&&Date.now()<end)await delay(50);
    check(this.channels.every(ch=>ch.closed),'OLD_CONNECTION_NOT_CLOSED');
    const out=[];
    for(const original of this.channels){
      const modes=original.session?[['new',undefined],['session_state',original.session]]:[['new',undefined]];
      for(const [mode,session] of modes){
        const ch=open(this.options,original,session);
        try{
          await request(ch,this.host);
          throw new Error('REVOKED_CERTIFICATE_ACCEPTED');
        }catch(e){
          // No EOF, socket reset, timeout, TCP refusal, local trust or protocol
          // failure is accepted as proof of server-side certificate rejection.
          check(certificateAlert(e),'NO_CERTIFICATE_REJECTION_ALERT');
          out.push({tls:original.version,alpn:original.alpn,attempt:mode,certificate_alert:e.code});
        }finally{ch.destroy();}
      }
    }
    return {old_connections_closed:4,negative_connections:out,tls13_resumption:'no ticket issued; resumable session unavailable'};
  }
  close(){for(const ch of this.channels)ch.destroy();}
}

async function main(){
  let matrix;
  const reader=readline.createInterface({input:process.stdin,crlfDelay:Infinity});
  try{
    for await(const line of reader){
      let result;
      try{
        const command=JSON.parse(line);
        if(command.action==='prepare'){
          check(!matrix,'ALREADY_PREPARED');
          const options={host:'171.22.237.16',port:443,servername:'',rejectUnauthorized:true,
            cert:fs.readFileSync(command.cert),key:fs.readFileSync(command.key),passphrase:command.passphrase};
          if(fs.existsSync('/etc/ssl/cert.pem'))options.ca=fs.readFileSync('/etc/ssl/cert.pem');
          matrix=new Matrix(options,'171.22.237.16');result=await matrix.prepare();
        }else if(command.action==='arm')result=matrix.arm();
        else if(command.action==='verify')result=await matrix.verify();
        else if(command.action==='close'){matrix?.close();break;}
        else throw new Error('UNKNOWN_ACTION');
        process.stdout.write(JSON.stringify({ok:true,result})+'\n');
      }catch(e){
        // Never include TLS/OpenSSL messages: only our safe symbolic codes.
        const code=/^[A-Z][A-Z0-9_]{0,90}$/.test(e.message)?e.message:'TLS_PROBE_FAILED';
        process.stdout.write(JSON.stringify({ok:false,code})+'\n');
      }
    }
  }finally{matrix?.close();}
}
module.exports={Matrix,certificateAlert};
if(require.main===module)main().catch(()=>{process.stderr.write('TLS_PROBE_FAILED\n');process.exitCode=1;});
