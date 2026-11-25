'use strict';
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const markerName='installation.json';
function validateTestDir(dir){
 if(!dir||!path.isAbsolute(dir))throw Error('Absolute isolated test directory required');
 for(let p=path.resolve(dir);;p=path.dirname(p)){
  if(fs.lstatSync(p).isSymbolicLink()&&!['/tmp','/var'].includes(p))throw Error('Test directory symlink refused');
  if(p===path.dirname(p))break;
 }
 const real=fs.realpathSync(dir),root=fs.realpathSync(os.tmpdir()),relative=path.relative(root,real);
 if(!relative||relative==='..'||relative.startsWith('..'+path.sep)||path.isAbsolute(relative))throw Error('Tests require a directory below system temp');
 for(let p=real;;p=path.dirname(p)){
  const file=path.join(p,markerName);
  if(fs.existsSync(file)){
   const st=fs.lstatSync(file);if(!st.isFile()||st.nlink!==1||st.size>4096)throw Error('Unsafe storage marker');
   const m=JSON.parse(fs.readFileSync(file,'utf8'));
   if(m.environment!=='test'||m.version!==1)throw Error('Production or unknown storage refused by tests');
  }
  if(p===path.dirname(p))break;
 }
 const m=JSON.parse(fs.readFileSync(path.join(real,markerName),'utf8'));
 if(m.environment!=='test'||m.version!==1||!/^([a-f0-9]{32})$/.test(m.installation_id))throw Error('Valid test marker required');
 for(const name of ['data.json','categories.json','planner.db','planner.db-wal','planner.db-shm','planner.db.lock','planner.sqlite','planner.sqlite-wal','planner.sqlite-shm','planner.sqlite.lock','budget-test-server']){
  const file=path.join(real,name);
  try {const st=fs.lstatSync(file);if(!st.isFile()||st.nlink!==1)throw Error('Unsafe test file');}catch(e){if(e.code!=='ENOENT')throw e;}
 }
 return real;
}
function createTestDir(prefix='budget-planner-ui-'){
 const dir=fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()),prefix));
 fs.writeFileSync(path.join(dir,markerName),JSON.stringify({version:1,environment:'test',installation_id:crypto.randomBytes(16).toString('hex')}),{flag:'wx',mode:0o600});
 return validateTestDir(dir);
}
function configuredTestDir(prefix){return process.env.PLANNER_TEST_DIR?validateTestDir(process.env.PLANNER_TEST_DIR):createTestDir(prefix);}
function testEnvironment(extra={}){
 const env={...process.env};for(const key of Object.keys(env))if(key.startsWith('BUDGET_')||['CREDENTIALS_DIRECTORY','BETA31_SNAPSHOT','FINAL_REVIEW_DIR','FINAL_REVIEW_STATIC'].includes(key))delete env[key];
 return {...env,BUDGET_ENV:'test',...extra};
}
module.exports={validateTestDir,createTestDir,configuredTestDir,testEnvironment};
