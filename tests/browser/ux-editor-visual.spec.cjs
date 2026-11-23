const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs');
const phase=process.env.UX_EDITOR_PHASE||'after';let task,project;
test.beforeAll(async({request})=>{
 async function create(type,data){const r=await request.post('/api/planner/'+type,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
 project=await create('projects',{title:'Дом · подготовка к выходным',description:'Небольшие шаги, чтобы освободить выходные для отдыха.',start_date:'2026-11-21',due_date:'2026-12-20'});
 task=await create('tasks',{title:'Подготовить квартиру к выходным и разобрать рабочее место',description:'Разобрать бумаги, освободить полки и подготовить список покупок.',priority:'high',project_id:project.id,planned_date:'2026-12-05',due_date:'2026-12-06'});
 await create('tasks',{title:'Разобрать полку с документами',project_id:project.id,parent_task_id:task.id});
 await create('sessions',{task_id:task.id,date:'2026-12-05',start_time:'10:00',end_time:'11:00',timezone:'Europe/Moscow'});
});
for(const width of [1440,1024,768,390,360])test('editor visual '+width,async({page})=>{
 await page.setViewportSize({width,height:1000});const dir='artifacts/ux2-stage2/'+phase;fs.mkdirSync(dir,{recursive:true});
 await page.goto('/#/plans/projects/'+project.id);await expect(page.getByRole('heading',{name:project.title,exact:true})).toBeVisible();await page.screenshot({path:dir+'/project-'+width+'.png',fullPage:true,animations:'disabled'});
 await page.getByRole('button',{name:task.title,exact:true}).click();const editor=page.getByRole('dialog',{name:'Редактор задачи',exact:true});await expect(editor).toBeVisible();await expect(editor.getByLabel('Название',{exact:true})).toHaveValue(task.title);await page.screenshot({path:dir+'/editor-'+width+'.png',fullPage:true,animations:'disabled'});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
 if(phase==='after'){await editor.locator('.pl-editor-actions').getByRole('button',{name:'Действия с задачей',exact:true}).click();await expect(page.getByRole('dialog',{name:'Действия с задачей',exact:true})).toBeVisible();expect((await page.getByRole('dialog',{name:'Действия с задачей',exact:true}).boundingBox()).height).toBeLessThan(800);await page.screenshot({path:dir+'/menu-'+width+'.png',fullPage:true,animations:'disabled'});}
});
