const base=require('../../playwright.config.cjs');const path=require('node:path');
module.exports={...base,webServer:{...base.webServer,cwd:path.resolve(__dirname,'../..')},testDir:__dirname,testMatch:'*.capture.cjs',use:{...base.use,timezoneId:'Europe/Moscow',viewport:{width:1440,height:900},locale:'ru-RU'},outputDir:path.resolve('test-results/beta2-capture')};
