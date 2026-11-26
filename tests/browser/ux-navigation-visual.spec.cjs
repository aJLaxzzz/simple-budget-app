const {test,expect}=require('../browser-fixtures.cjs');
const fs=require('node:fs');
let project;
const phase=process.env.UX_PHASE||'after';
const today=()=>new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Moscow',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
function day(n){const d=new Date(today()+'T12:00:00Z');d.setUTCDate(d.getUTCDate()+n);return d.toISOString().slice(0,10);}
test.beforeAll(async({request})=>{
 async function create(type,data){const r=await request.post('/api/planner/'+type,{data});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
 project=await create('projects',{title:'Дом и личные дела'});
 const parent=await create('tasks',{title:'Подготовить квартиру к выходным',project_id:project.id,planned_date:day(0)});
 await create('tasks',{title:'Разобрать полку и отдать ненужные вещи',project_id:project.id,parent_task_id:parent.id,planned_date:day(1)});
 for(const [title,n] of [['Записаться на стрижку',-2],['Забрать заказ',0],['Купить продукты на неделю',1],['Запланировать поездку',12]])await create('tasks',{title,planned_date:day(n)});
 await create('tasks',{title:'Продумать освещение рабочего места',project_id:project.id});
 await create('tasks',{title:'Оплатить интернет',due_date:day(0)});
});
for(const width of [1440,1024,768,390])test('UX visual '+width,async({page})=>{
 await page.setViewportSize({width,height:1000});await page.goto('/#/plans/tasks');await expect(page.getByRole('heading',{name:'Все задачи',exact:true})).toBeVisible();await expect(page.locator('[data-project-group*="'+project.id+'"]').first()).toBeVisible();
 if(phase==='after'){
  await expect(page.getByRole('navigation',{name:'Разделы планировщика'}).getByRole('link')).toHaveCount(4);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
 }
 fs.mkdirSync('artifacts/ux2-stage1/'+phase,{recursive:true});await page.screenshot({path:'artifacts/ux2-stage1/'+phase+'/tasks-'+width+'.png',fullPage:true});
});
