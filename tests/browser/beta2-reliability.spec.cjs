const {test,expect}=require('../browser-fixtures.cjs');
const {randomUUID}=require('node:crypto');
test('beta2 quick completion retries committed response exactly and resolves a real 409',async({page,request})=>{
 const create=async title=>{const r=await request.post('/api/planner/tasks',{data:{title,planned_date:'2026-11-24'}});expect(r.ok()).toBeTruthy();return r.json();};
 const get=async id=>(await request.get('/api/planner/tasks/'+id)).json();
 const t=await create('Оплатить интернет · потеря ответа '+randomUUID());
 await page.goto('/#/plans/tasks?q='+encodeURIComponent(t.title));let calls=[];
 await page.route('**/api/planner/tasks/'+t.id+'/complete',async route=>{calls.push(route.request().postDataJSON());if(calls.length===1){const r=await route.fetch();expect(r.ok()).toBeTruthy();await route.abort();}else await route.continue();});
 await page.locator('[data-task-id="'+t.id+'"] .pl-check').click();await expect(page.locator('#planner-toast')).toContainText('Не удалось завершить');expect((await get(t.id)).status).toBe('completed');
 await page.locator('[data-task-id="'+t.id+'"] .pl-check').click();await expect(page.getByRole('button',{name:'Отменить завершение'})).toBeVisible();expect(calls).toHaveLength(2);expect(calls[0]).toEqual(calls[1]);const history=await (await request.get('/api/planner/history?object_type=task&object_id='+t.id)).json();expect(history.items.filter(x=>x.action==='completed')).toHaveLength(1);
 const other=await create('Купить продукты · конкурентное изменение '+randomUUID());await page.goto('/#/plans/tasks?q='+encodeURIComponent(other.title));let changed=false;
 await page.route('**/api/planner/tasks/'+other.id+'/complete',async route=>{if(!changed){changed=true;const r=await request.patch('/api/planner/tasks/'+other.id,{data:{expected_revision:other.revision,description:'Добавлено с другого устройства'}});expect(r.ok()).toBeTruthy();}await route.continue();});
 await page.locator('[data-task-id="'+other.id+'"] .pl-check').click();const dialog=page.getByRole('dialog',{name:'Завершение задачи',exact:true});await expect(dialog).toBeVisible();expect((await get(other.id)).status).toBe('planned');await expect(page.getByRole('button',{name:'Отменить завершение'})).toHaveCount(0);
 await dialog.getByRole('button',{name:'Завершить задачу',exact:true}).click();await expect(dialog).toHaveCount(0);expect((await get(other.id)).status).toBe('completed');expect((await get(other.id)).description).toBe('Добавлено с другого устройства');
});
