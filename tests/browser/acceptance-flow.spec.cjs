const {test,expect,revealAction}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
const ruRule={schema_version:1,mode:'completion',unit:'days',interval:45,allowed_days:{mode:'ru_production'},adjustment:'next',timezone:'Europe/Moscow'};
async function post(request,path,data,status=200){const r=await request.post('/api/planner/'+path,{data});expect(r.status(),await r.text()).toBe(status);return r.json();}
async function get(request,path){const r=await request.get('/api/planner/'+path);expect(r.ok()).toBeTruthy();return r.json();}
test('verified years imported explicitly; haircut 45 actual days crosses year without duplicate branch',async({page,request})=>{
 for(const year of [2026,2027]){
  await page.goto('/#/plans/settings');await page.getByRole('button',{name:'Проверить и импортировать '+year,exact:true}).click();await page.getByRole('button',{name:'Проверить файл и изменения'}).click();await expect(page.getByText(/Изменятся дней:/)).toBeVisible();await page.getByRole('form',{name:'Импорт календаря'}).getByRole('checkbox').check();await page.getByRole('form',{name:'Импорт календаря'}).locator('button[type=submit]').click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 }
 const doc=await get(request,'production-calendar/bundled/2027');expect(doc.days.find(d=>d.date==='2027-02-20').kind).toBe('shortened');expect(doc.days.find(d=>d.date==='2027-02-22').kind).toBe('nonworking');
 await page.goto('/#/plans/series');await page.getByRole('button',{name:'+ Регулярное дело'}).click();const f=page.getByRole('form',{name:'Регулярное дело'});await f.getByLabel('Название',{exact:true}).fill('Стрижка · официальные рабочие дни');await f.getByLabel('Повторять',{exact:true}).selectOption('completion');await f.getByLabel('Допустимые дни',{exact:true}).selectOption('ru_production');await f.getByLabel('Первая дата выполнения').fill('2026-11-01');await expect(f.locator('.pl-preview-row')).toHaveCount(1);await f.locator('button[type=submit]').click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 const id=page.url().split('/series/')[1];let series=await get(request,'series/'+id);expect(series.occurrences).toHaveLength(3);const first=series.occurrences[0];expect(first.base_date).toBe('2026-11-01');expect(first.adjusted_date).toBe('2026-11-02');
 await page.locator('.pl-occurrence').first().getByRole('button',{name:'Перенести',exact:true}).click();let action=page.getByRole('form',{name:'Действие с выполнением'});await action.getByLabel('Новая дата').fill('2026-11-16');await action.locator('button[type=submit]').click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 await page.locator('.pl-occurrence').first().getByRole('button',{name:'Выполнить',exact:true}).click();action=page.getByRole('form',{name:'Действие с выполнением'});await action.locator('input[type=date]').fill('2026-11-16');await action.locator('button[type=submit]').click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 series=await get(request,'series/'+id);expect(series.occurrences).toHaveLength(4);expect(series.occurrences[1].base_date).toBe('2026-12-31');expect(series.occurrences[1].adjusted_date).toBe('2027-01-11');expect(series.occurrences[0].task.completed_date).toBe('2026-11-16');
 await page.locator('.pl-occurrence').nth(1).getByRole('button',{name:'Пропустить',exact:true}).click();await page.getByRole('form',{name:'Действие с выполнением'}).locator('button[type=submit]').click();await expect(page.locator('.pl-dialog')).toHaveCount(0);await page.getByRole('button',{name:'Дата, от которой считать дальше',exact:true}).click();await page.getByRole('form',{name:'Дата, от которой считать дальше'}).getByLabel('Дата, от которой считать дальше',{exact:true}).fill('2026-11-17');await page.getByRole('button',{name:'Посмотреть изменения'}).click();await page.getByRole('button',{name:'Применить изменения',exact:true}).click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 series=await get(request,'series/'+id);expect(series.occurrences.filter(o=>o.task.status==='planned')).toHaveLength(3);expect(series.occurrences.filter(o=>o.task.status==='planned'&&!o.task.dynamic)).toHaveLength(1);
 await revealAction(page,'Восстановить');await page.locator('.pl-occurrence').nth(1).getByRole('button',{name:'Восстановить',exact:true}).click();await page.getByRole('form',{name:'Действие с выполнением'}).locator('button[type=submit]').click();await expect(page.getByRole('form',{name:'Действие с выполнением'}).getByRole('alert')).toBeVisible();series=await get(request,'series/'+id);expect(series.occurrences.filter(o=>o.task.status==='planned')).toHaveLength(3);expect(series.occurrences.filter(o=>o.task.status==='planned'&&!o.task.dynamic)).toHaveLength(1);
 expect((await get(request,'history?object_type=series&object_id='+id)).items.length).toBeGreaterThan(3);
});
test('project deadline attention and independent task series block project closure safely',async({page,request})=>{
 const p=await post(request,'projects',{title:'Проект с независимой серией '+randomUUID(),due_date:'2020-01-01'},201);
 const task=await post(request,'tasks',{title:'Независимое дело',project_id:p.id},201);
 const series=await post(request,'series',{request_id:randomUUID(),existing_task_id:task.id,expected_revision:task.revision,first_date:'2026-11-19',rule:{...ruRule,mode:'schedule',allowed_days:{mode:'any'},adjustment:'none'}},201);
 await page.goto('/#/plans/attention?tab=overdue');const card=page.locator('.pl-occurrence').filter({has:page.getByRole('heading',{name:p.title,exact:true})});await expect(card).toBeVisible();await card.getByRole('button',{name:'Другие действия'}).click();await page.getByRole('dialog',{name:'Действия с проблемой'}).getByRole('button',{name:'Завершить проект',exact:true}).click();await page.locator('dialog').locator('button[type=submit]').click();await expect(page.locator('dialog').getByRole('alert')).toContainText(/серии/);expect((await get(request,'projects/'+p.id)).status).toBe('planned');expect((await get(request,'series/'+series.id)).state).toBe('active');
});
for(const kind of ['task','project','session'])test('lost create response safely retries ordinary '+kind,async({page,request})=>{
 let form,url,title='Без дубля '+randomUUID();
 if(kind==='task'){await page.goto('/#/plans/inbox');form=page.getByRole('form',{name:'Быстрое создание задачи'});await form.getByRole('textbox').fill(title);url='tasks';}
 if(kind==='project'){await page.goto('/#/plans/projects?new=project');form=page.getByRole('form',{name:'Редактор проекта'});await form.getByLabel('Название проекта').fill(title);url='project-drafts/apply';}
 let task;
 if(kind==='session'){task=await post(request,'tasks',{title},201);await page.goto('/#/plans/tasks?task='+task.id);await page.getByRole('tab',{name:'Периоды работы',exact:true}).click();await page.getByRole('button',{name:'+ Период работы',exact:true}).click();form=page.getByRole('form',{name:'Редактор периода работы'});url='sessions';}
 const bodies=[];let first=true;
 await page.route('**/api/planner/'+url,async r=>{if(r.request().method()!=='POST')return r.continue();bodies.push(r.request().postDataJSON());if(first){first=false;await r.fetch();await r.abort('failed');}else await r.continue();});
 await form.locator('button[type=submit]').click();await expect(form.getByRole('alert')).toContainText('Нет связи');await form.locator('button[type=submit]').click();
 if(kind==='task')await expect(page.getByRole('button',{name:title,exact:true})).toBeVisible();else if(kind==='project')await expect(page.getByRole('heading',{name:title,exact:true})).toBeVisible();else await expect(page.locator('.pl-session')).toHaveCount(1);
 expect(bodies).toHaveLength(2);expect(bodies[0]).toEqual(bodies[1]);expect(bodies[0].request_id).toBeTruthy();const items=(await get(request,(kind==='project'?'projects':url)+(task?'?task_id='+task.id:''))).items;expect(kind==='session'?items:items.filter(x=>x.title===title)).toHaveLength(1);
});

test('project history remains accessible after editing and archiving',async({page,request})=>{
 const p=await post(request,'projects',{title:'История проекта '+randomUUID()},201);
 await request.patch('/api/planner/projects/'+p.id,{data:{expected_revision:1,description:'Изменение для истории'}});
 await post(request,'projects/'+p.id+'/archive',{expected_revision:2});
 await page.goto('/#/plans/projects/'+p.id);
 await page.locator('.pl-disclosure > summary').filter({hasText:'История'}).click();
 await expect(page.locator('.pl-history details')).toHaveCount(3);
 await expect(page.locator('.pl-history')).toContainText('Создано');
 await page.locator('.pl-history summary').filter({hasText:'Изменено'}).click();
 await expect(page.locator('.pl-history')).toContainText('Изменение для истории');
});
