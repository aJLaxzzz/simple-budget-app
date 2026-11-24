const {test,expect}=require('../browser-fixtures.cjs');const {randomUUID}=require('node:crypto');const fs=require('node:fs');
test('beta2 controlled comparison',async({page,request})=>{const target=process.env.BETA2_BEFORE?'before':'comparison-after';
 const api=async(url,data)=>{const r=await request.post('/api/planner/'+url,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();};
 const p=await api('projects',{title:'ДР'});for(const [title,date]of [['ДР Алины','2026-11-24'],['ДР Ульяны','2027-11-04'],['ДР папы','2027-10-28']])await api('series',{request_id:randomUUID(),first_date:date,task:{title,project_id:p.id},rule:{schema_version:1,mode:'schedule',unit:'years',interval:1,allowed_days:{mode:'any'},adjustment:'none'}});
 await api('tasks',{title:'Подготовить подарки',planned_date:'2026-12-05'});
 fs.mkdirSync('artifacts/beta-update-2/'+target,{recursive:true});for(const width of [1440,390]){await page.setViewportSize({width,height:900});await page.goto('/#/plans/tasks');await expect(page.locator('.pl-task')).not.toHaveCount(0);await page.screenshot({path:`artifacts/beta-update-2/${target}/tasks-${width}.png`,animations:'disabled'});await page.goto('/#/plans/projects/'+p.id);await expect(page.locator('.pl-project-overview')).toBeVisible();await page.screenshot({path:`artifacts/beta-update-2/${target}/project-${width}.png`,animations:'disabled'});}
});
