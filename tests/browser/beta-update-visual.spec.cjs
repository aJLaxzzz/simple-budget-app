const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');const fs=require('node:fs');const path=require('node:path');
const phase=process.env.BETA_VISUAL_PHASE||'after';const owned=new Set();
test.afterAll(async({request})=>{for(const id of owned){const p=await (await request.get('/api/planner/series/'+id+'/delete-preview')).json();const r=await request.post('/api/planner/series/'+id+'/delete',{data:{expected_revision:p.revision,preview_token:p.preview_token,confirm:true,request_id:randomUUID()}});expect(r.ok(),await r.text()).toBeTruthy();}owned.clear();});
for(const width of [1440,390])test('beta targeted comparison '+width,async({page,request})=>{
 await page.setViewportSize({width,height:900});await page.clock.setFixedTime(new Date('2026-11-16T09:00:00+03:00'));
 const title='Полить домашние растения '+width;
 const r=await request.post('/api/planner/series',{data:{request_id:randomUUID(),first_date:'2026-11-16',task:{title,priority:'normal',icon_id:'lucide:leaf',color_id:'green'},rule:{schema_version:1,mode:'schedule',unit:'days',interval:2,allowed_days:{mode:'any'},adjustment:'none'}}});expect(r.ok()).toBeTruthy();const series=await r.json();owned.add(series.id);
 const shot=async name=>{const p=path.resolve('artifacts/beta-update-1/'+phase+'/'+name+'-'+width+'.png');fs.mkdirSync(path.dirname(p),{recursive:true});await page.screenshot({path:p,animations:'disabled'});};
 await page.goto('/#/plans/tasks?q='+encodeURIComponent(title));await expect(page.locator('.pl-task').first()).toBeVisible();await shot('frequent-tasks');
 await page.goto('/#/plans/tasks?view=regular');await expect(page.getByText(title,{exact:true}).first()).toBeVisible();if(phase==='after')await expect(page.locator('.pl-series-card').first()).not.toContainText('null');await shot('recurring-cards');
 await page.goto('/#/plans/tasks?task='+series.occurrences[0].task.id);await expect(page.getByRole('dialog',{name:'Редактор задачи'})).toBeVisible();await page.getByRole('button',{name:'Оформление',exact:true}).click();if(phase==='after')await page.getByRole('button',{name:'Только это выполнение',exact:true}).click();await expect(page.getByRole('button',{name:'Дом',exact:true})).toBeVisible();await shot('palette');await page.getByRole('searchbox',{name:'Поиск иконки'}).fill('зуб');await shot('dental-icons');await page.getByRole('button',{name:'Готово',exact:true}).click();
 await page.getByRole('dialog',{name:'Редактор задачи'}).getByRole('button',{name:'Действия с задачей'}).click();await expect(page.getByRole('dialog',{name:'Действия с задачей'})).toBeVisible();await shot('task-menu');
 await page.goto('/#/plans/tasks');await page.getByRole('button',{name:'Создать',exact:true}).click();await page.getByRole('button',{name:'Создать задачу',exact:true}).click();await expect(page.getByRole('dialog',{name:'Новая задача',exact:true})).toBeVisible();await shot('new-task');
});
