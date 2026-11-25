const {validateTestDir,testEnvironment}=require('../test-storage.cjs');
const fs=require('node:fs'),path=require('node:path'),{spawn}=require('node:child_process');
const dir=validateTestDir(process.env.PLANNER_TEST_DIR);if(!dir||!path.basename(dir).startsWith('budget-planner-ui-'))throw Error('Isolated directory required');
fs.writeFileSync(path.join(dir,'data.json'),'{"months":[]}');fs.writeFileSync(path.join(dir,'categories.json'),'{"expense":[],"income":[]}');
const child=spawn('go',['test','./internal/planner','-run','^TestFinalReviewServer$','-count=1','-timeout=90m'],{stdio:'inherit',env:testEnvironment({FINAL_REVIEW_DIR:dir,FINAL_REVIEW_STATIC:path.resolve('static')})});
for(const sig of ['SIGINT','SIGTERM'])process.on(sig,()=>child.kill(sig));child.on('exit',code=>process.exit(code||0));
