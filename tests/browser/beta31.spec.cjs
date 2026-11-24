const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');const fs=require('node:fs');const path=require('node:path');
test.use({viewport:{width:1440,height:900},timezoneId:'Europe/Moscow',locale:'ru-RU'});
async function api(request,url,data,method=data?'post':'get'){const r=await request[method]('/api/planner/'+url,data?{data}:{});expect(r.ok(),await r.text()).toBeTruthy();return r.json();}
const rule={schema_version:1,mode:'schedule',unit:'weeks',interval:1,allowed_days:{mode:'any'},adjustment:'none'};
test('Beta31 confirmed legacy keep dates, repair, calendar and duplicate deletion',async({page,request})=>{
 test.setTimeout(90000);const folder=path.resolve('artifacts/beta-update-3.1');fs.mkdirSync(folder,{recursive:true});const shots=[];
 async function shot(name){if(!process.env.BETA31_SCREENSHOTS)return;await page.evaluate(()=>document.fonts.ready);await page.addStyleTag({content:'*,*::before,*::after{animation:none!important;transition:none!important}'});await page.locator('.pl-toast').evaluateAll(nodes=>nodes.forEach(n=>n.remove()));await page.screenshot({path:path.join(folder,name+'.png'),animations:'disabled'});shots.push(name+'.png');}
 let source=await api(request,'projects',{title:'Домашние дела',start_date:'2026-11-27',due_date:'2026-11-29',color_id:'teal',icon_id:'lucide:house'});
 for(let i=0;i<3;i++)await api(request,'tasks',{title:['Загрузить посудомойку','Пропылесосить','Помыть полы'][i],project_id:source.id,planned_date:'2026-11-'+(27+i),due_date:'2026-11-'+(27+i)});
 source=await api(request,'projects/'+source.id);
 const input={expected_revision:source.revision,anchor_date:'2026-11-27',first_date:'2026-12-08',rule};
 const preview=await api(request,'projects/'+source.id+'/template-preview',input);
 await api(request,'projects/'+source.id+'/save-template',{...input,preview_token:preview.preview_token,request_id:randomUUID()});
 source=await api(request,'projects/'+source.id);const sid=source.repeat_series_id;let series=await api(request,'series/'+sid+'?limit=100');
 const command={action:'change_rule',expected_revision:series.revision,rule,from_cycle:0,first_date:'2026-11-27',tasks_date_decision:'keep'};
 const collision=await api(request,'series/'+sid+'/preview',command);expect(collision.conflicts.join(' ')).toContain('исходным проектом');
 const blocked=await request.post('/api/planner/series/'+sid+'/change-rule',{data:{...command,preview_token:collision.preview_token,request_id:randomUUID()}});expect(blocked.status()).toBe(409);
 // Build the already-existing legacy overlap using ordinary API edits; the new
 // schedule command now refuses to introduce it. No database mocks are used.
 source=await api(request,'projects/'+source.id);await api(request,'projects/'+source.id,{expected_revision:source.revision,start_date:'2026-11-20'},'patch');
 const change=await api(request,'series/'+sid+'/preview',command);await api(request,'series/'+sid+'/change-rule',{...command,preview_token:change.preview_token,request_id:randomUUID()});
 source=await api(request,'projects/'+source.id);await api(request,'projects/'+source.id,{expected_revision:source.revision,start_date:'2026-11-27'},'patch');
 await page.goto('/#/plans/calendar?mode=month&date=2026-12-01');await expect(page.locator('.pc-month')).toBeVisible();await shot('calendar-before-1440');
 const before=await api(request,'series/'+sid+'?limit=100');const originalIDs=before.occurrences.map(o=>o.project.id);
 await page.goto('/#/plans/projects/'+source.id);await page.getByRole('button',{name:'Настроить повторение',exact:true}).click();await page.getByRole('tab',{name:'Дополнительно'}).click();await page.getByRole('button',{name:'Проверить и исправить будущие выполнения',exact:true}).click();await page.getByRole('button',{name:'Проверить даты',exact:true}).click();
 await expect(page.getByRole('button',{name:'Исправить безопасные даты'})).toBeVisible();await expect(page.getByText(/Совпадающий период/)).toBeVisible();await shot('repair-preview-1440');
 await page.setViewportSize({width:390,height:844});await expect(page.getByRole('button',{name:'Исправить безопасные даты'})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();await shot('repair-preview-390');await page.setViewportSize({width:1440,height:900});
 // A stale preview never silently applies. Inputs remain available for recalculation.
 let once=true;await page.route('**/dates-apply',async route=>{if(once){once=false;await route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({error:{code:'preview_changed',message:'Повторите проверку дат'}})});}else await route.continue();});
 await page.getByRole('button',{name:'Исправить безопасные даты'}).click();await expect(page.getByRole('dialog').getByRole('alert')).toContainText('Повторите проверку');await page.getByRole('button',{name:'Проверить даты',exact:true}).click();await page.getByRole('button',{name:'Исправить безопасные даты'}).click();await expect(page.locator('.pl-dialog')).toHaveCount(0);
 const after=await api(request,'series/'+sid+'?limit=100');expect(after.occurrences.map(o=>o.project.id)).toEqual(originalIDs);
 const events=(await api(request,'calendar?from=2026-11-27&to=2027-01-31')).items;
 for(const o of after.occurrences){const p=o.project;const start=new Date(p.start_date+'T12:00:00Z');for(const t of p.tasks){const offset=['Загрузить посудомойку','Пропылесосить','Помыть полы'].indexOf(t.title);const expected=new Date(start);expected.setUTCDate(expected.getUTCDate()+offset);expect(t.planned_date).toBe(expected.toISOString().slice(0,10));const event=events.find(e=>e.task_id===t.id&&e.type==='task');if(t.planned_date<='2027-01-31'){expect(event).toBeTruthy();expect(event.date).toBe(t.planned_date);}}}
 await page.goto('/#/plans/calendar?mode=month&date=2026-12-01');await expect(page.locator('.pc-month')).toBeVisible();await shot('calendar-after-1440');
 await page.goto('/#/plans/projects/'+source.id);await page.getByRole('button',{name:'Настроить повторение',exact:true}).click();await page.getByRole('tab',{name:'Дополнительно'}).click();await page.getByRole('button',{name:'Проверить и исправить будущие выполнения',exact:true}).click();await page.getByRole('button',{name:'Проверить даты',exact:true}).click();await expect(page.getByText(/Безопасных изменений нет/)).toBeVisible();await shot('repair-noop-1440');
 await page.getByRole('button',{name:'Проверить удаление отдельного выполнения'}).click();const deletion=page.getByRole('dialog',{name:'Удалить навсегда',exact:true});await expect(deletion.getByLabel('Область удаления')).toHaveValue('occurrence');await expect(deletion.getByRole('button',{name:'Удалить навсегда',exact:true})).toBeEnabled();await shot('duplicate-confirmation-1440');await deletion.getByRole('button',{name:'Удалить навсегда',exact:true}).click();await expect(deletion).toHaveCount(0);
 expect((await api(request,'projects/'+source.id)).id).toBe(source.id);
 const current=await api(request,'series/'+sid);const checked=await api(request,'series/'+sid+'/dates-preview',{expected_revision:current.revision});expect(checked.duplicates).toHaveLength(0);expect(checked.rows.flatMap(r=>r.changes).filter(c=>c.safe)).toHaveLength(0);
 await page.goto('/#/plans/calendar?mode=month&date=2026-12-01');await expect(page.locator('.pc-month')).toBeVisible();await shot('calendar-deduplicated-1440');
 await page.goto('/#/plans/series/'+sid+'?source=project');await expect(page.getByRole('dialog',{name:'Настроить повторение',exact:true})).toBeVisible();await expect(page.getByLabel('Даты задач проектов')).toHaveValue('shift');await page.getByLabel('Даты задач проектов').selectOption('keep');await expect(page.getByText(/Даты уже созданных задач останутся/)).toBeVisible();
 const cleanup=await api(request,'series/'+sid+'/delete-preview');expect(cleanup.blockers).toHaveLength(0);await api(request,'series/'+sid+'/delete',{expected_revision:cleanup.revision,preview_token:cleanup.preview_token,confirm:true,request_id:randomUUID()});
 if(process.env.BETA31_SCREENSHOTS)fs.writeFileSync(path.join(folder,'manifest.json'),JSON.stringify({scenario:'Обезличенное воспроизведение изменения расписания с keep; данные созданы API, не скопированы из пользовательской базы',screenshots:shots,expectedNegative:'Ожидаемые HTTP 409: запрет совпадения периода с исходным проектом и намеренный конфликт для проверки повторного предпросмотра'},null,2));
});
