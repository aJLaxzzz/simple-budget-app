const {test}=require('node:test');const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const {createTestDir,validateTestDir,testEnvironment}=require('./test-storage.cjs');
test('test storage refuses production marker and symlink without modifying it',()=>{
 const dir=createTestDir();assert.equal(validateTestDir(dir),dir);
 const marker=path.join(dir,'installation.json');const value=JSON.parse(fs.readFileSync(marker));value.environment='production';fs.writeFileSync(marker,JSON.stringify(value));const before=fs.readFileSync(marker);
 assert.throws(()=>validateTestDir(dir),/Production/);assert.deepEqual(fs.readFileSync(marker),before);
 const alias=path.join(createTestDir(),'alias');fs.symlinkSync(dir,alias);assert.throws(()=>validateTestDir(alias),/symlink/);
});
test('test storage requires marker, private temp root and safe files',()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'budget-guard-'));assert.throws(()=>validateTestDir(dir));assert.throws(()=>validateTestDir(process.cwd()));
 const safe=createTestDir();fs.symlinkSync(__filename,path.join(safe,'data.json'));assert.throws(()=>validateTestDir(safe),/Unsafe/);
});
test('test environment does not inherit production configuration or systemd credentials',()=>{
 const old=process.env.BUDGET_PASSWORD_FILE;const cred=process.env.CREDENTIALS_DIRECTORY;
 try{process.env.BUDGET_PASSWORD_FILE='/private-production-secret';process.env.CREDENTIALS_DIRECTORY='/private-credentials';const env=testEnvironment();assert.equal(env.BUDGET_PASSWORD_FILE,undefined);assert.equal(env.CREDENTIALS_DIRECTORY,undefined);assert.equal(env.BUDGET_ENV,'test');}
 finally{if(old===undefined)delete process.env.BUDGET_PASSWORD_FILE;else process.env.BUDGET_PASSWORD_FILE=old;if(cred===undefined)delete process.env.CREDENTIALS_DIRECTORY;else process.env.CREDENTIALS_DIRECTORY=cred;}
});
