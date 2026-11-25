'use strict';
const fs=require('node:fs'),path=require('node:path'),net=require('node:net');
const {spawn}=require('node:child_process');
const {once}=require('node:events');
const {createTestDir,validateTestDir,testEnvironment}=require('./test-storage.cjs');
// Reuse only the compiled test executable, never its database or the working app.
async function isolatedServer(prefix='budget-load-') {
 const dir=createTestDir(prefix);validateTestDir(dir);
 for(const [name,value]of Object.entries({'data.json':{months:[]},'categories.json':{expense:[],income:[]}}))fs.writeFileSync(path.join(dir,name),JSON.stringify(value),{flag:'wx',mode:0o600});
 const reservation=net.createServer();reservation.listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(r=>reservation.close(r));
 const binary=path.join(validateTestDir(process.env.PLANNER_TEST_DIR),'budget-test-server');
 const child=spawn(binary,[],{cwd:dir,env:testEnvironment({BUDGET_DATA_DIR:dir,BUDGET_ADDR:'127.0.0.1:'+port,GOMAXPROCS:'1'}),stdio:['ignore','pipe','pipe']});
 let output='';child.stderr.on('data',b=>{output+=b;output=output.slice(-16000);});
 const stopped=new Promise(resolve=>child.once('exit',(code,signal)=>resolve({code,signal})));
 try{
  await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Isolated test startup timeout: '+output)),15000);child.stderr.on('data',()=>{if(output.includes('listener ready')){clearTimeout(timer);resolve();}});child.once('error',e=>{clearTimeout(timer);reject(e);});child.once('exit',code=>{clearTimeout(timer);reject(Error('Test server exited '+code+': '+output));});});
 }catch(e){child.kill('SIGTERM');await stopped;throw e;}
 return {dir,baseURL:'http://127.0.0.1:'+port,child,async stop(){if(child.exitCode===null)child.kill('SIGTERM');const result=await stopped;if(result.code!==0)throw Error('Unclean isolated shutdown: '+JSON.stringify(result)+' '+output);}};
}
module.exports={isolatedServer};
