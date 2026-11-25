// Real local isolated production-mode fixture; never touches the VPS or user data.
const {backupServer}=require('../backup-server.cjs');
const path=require('node:path');
const {execFileSync}=require('node:child_process');
const {createTestDir}=require('../test-storage.cjs');
process.env.PLANNER_TEST_DIR=createTestDir('budget-live-probe-');
execFileSync('go',['build','-o',path.join(process.env.PLANNER_TEST_DIR,'budget-test-server'),'.'],{cwd:path.resolve(__dirname,'../..')});
const {runBrowser}=require('../../scripts/ops/live_acceptance.cjs');
(async()=>{
 const server=await backupServer();
 try{console.log(JSON.stringify(await runBrowser({origin:server.baseURL,password:server.password}),null,2));}
 finally{await server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
