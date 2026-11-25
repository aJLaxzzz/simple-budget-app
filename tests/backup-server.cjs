'use strict';
const fs=require('node:fs'),path=require('node:path'),net=require('node:net');const {execFile}=require('node:child_process');const {promisify}=require('node:util');const run=promisify(execFile);const {once}=require('node:events');const {createTestDir,validateTestDir,testEnvironment}=require('./test-storage.cjs');
async function backupServer(){
 const root=createTestDir('budget-backup-ui-'),live=path.join(root,'live'),archives=path.join(root,'archives');fs.mkdirSync(live,{mode:0o700});fs.mkdirSync(archives,{mode:0o700});
 const binary=path.join(validateTestDir(process.env.PLANNER_TEST_DIR),'budget-test-server');const environment=testEnvironment();
 const init=JSON.parse((await run(binary,['admin','init','--data-dir',live,'--environment','test','--confirm'],{env:environment})).stdout);const id=init.installation_id;
 const socket=net.createServer().listen(0,'127.0.0.1');await once(socket,'listening');const port=socket.address().port;await new Promise(r=>socket.close(r));const baseURL='http://127.0.0.1:'+port,password='isolated-backup-browser-password-123456789';
 const env=testEnvironment({BUDGET_DATA_DIR:live,BUDGET_ADDR:'127.0.0.1:'+port,BUDGET_PASSWORD:password,BUDGET_INSTALLATION_ID:id,GOMAXPROCS:'1'});
 const controller=path.join(root,'controller.cjs'),state=path.join(root,'service.json');
 // A synthetic controller exercises actual stop/restart, not systemd or a user process.
 fs.writeFileSync(controller,`#!/usr/bin/env node
 const fs=require('node:fs'),{spawn}=require('node:child_process');
 const id=${JSON.stringify(id)},state=${JSON.stringify(state)},binary=${JSON.stringify(binary)},live=${JSON.stringify(live)},url=${JSON.stringify(baseURL)},archives=${JSON.stringify(archives)},env=${JSON.stringify(env)};
 const action=process.argv[2];if(process.argv[3]!==id)process.exit(8);
 function pid(){try{return JSON.parse(fs.readFileSync(state)).pid}catch{return 0}}
 function alive(){const p=pid();if(!p)return false;try{process.kill(p,0);return true}catch{return false}}
 async function wait(test){for(let i=0;i<300;i++){if(await test())return;await new Promise(r=>setTimeout(r,100))}throw Error('controller timeout')}
 (async()=>{
 if(action==='status'){process.exitCode=alive()?0:3;return}if(action==='stopped'){process.exitCode=alive()?3:0;return}
 if(action==='stop'){if(alive())process.kill(pid(),'SIGTERM');await wait(()=>!alive());return}
 if(action==='start'){if(alive())return;const log=fs.openSync(${JSON.stringify(path.join(root,'server.log'))},'a',0o600);if(fs.existsSync(archives+'/repository.json'))env.BUDGET_BACKUP_DIR=archives;const child=spawn(binary,[],{cwd:live,env,stdio:['ignore',log,log],detached:true});fs.writeFileSync(state,JSON.stringify({pid:child.pid}),{mode:0o600});child.unref();await wait(async()=>{try{return await new Promise(resolve=>require('node:http').get(url+'/api/ready',{headers:{'X-Budget-Admin':'1',Authorization:'Basic '+Buffer.from('budget:'+env.BUDGET_PASSWORD).toString('base64')}},r=>{r.resume();resolve(r.statusCode===200)}).on('error',()=>resolve(false)))}catch{return false}});return}
 throw Error('invalid controller action');
 })().catch(()=>process.exitCode=1);
 `,{mode:0o700});
 const control=action=>run(controller,[action,id],{env:environment,timeout:90000});
 const admin=async(args)=>JSON.parse((await run(binary,['admin','backup',...args,'--environment','test','--data-dir',live,'--backup-dir',archives,'--installation-id',id],{env:environment,timeout:90000,maxBuffer:1024*1024})).stdout);
 try {
 await control('start');
 const auth='Basic '+Buffer.from('budget:'+password).toString('base64');const api=async(type,data)=>new Promise((resolve,reject)=>{const req=require('node:http').request(baseURL+'/api/planner/'+type,{method:data?'POST':'GET',headers:{'X-Budget-Admin':'1',Authorization:auth,'Content-Type':'application/json'}},r=>{let raw='';r.on('data',b=>raw+=b);r.on('end',()=>{if(r.statusCode>=400)return reject(Error('fixture API '+r.statusCode));try{resolve(JSON.parse(raw))}catch(e){reject(e)}})});req.on('error',reject);req.end(data?JSON.stringify(data):undefined)});
 const p=await api('projects',{title:'Домашние планы',color_id:'teal'});await api('tasks',{title:'Проверить запас воды',project_id:p.id,planned_date:'2026-12-02'});
 const firstPID=JSON.parse(fs.readFileSync(state)).pid;
 const receipt=await admin(['create','--reason','daily','--controller',controller]);
 const secondPID=JSON.parse(fs.readFileSync(state)).pid;if(firstPID===secondPID)throw Error('backup did not restart the synthetic application');
 return {baseURL,password,root,live,archives,id,receipt,admin,async restart(){await control('stop');await control('start')},async snapshot(){return admin(['create','--reason','manual','--controller',controller])},async restore(archive){return admin(['restore','--archive',archive,'--scope','full','--confirm','--controller',controller])},async close(){await control('stop')}};
 } catch(error) { await control('stop').catch(()=>{}); throw error; }
}
module.exports={backupServer};
