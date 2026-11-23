const {test,expect}=require('../browser-fixtures.cjs');const {randomUUID}=require('node:crypto');
// The immutable audit retains the original evidence. This regression must now
// pass without an exception; it never rewrites the historical manifest or PNG.
test('DEF-01 regression: template date survives cancelled close',async({page,request})=>{
 const response=await request.post('/api/planner/templates',{data:{request_id:randomUUID(),snapshot:{title:'Генеральная уборка',tasks:[{key:'room',title:'Пропылесосить',planned_offset:0}]}}});expect(response.ok()).toBeTruthy();const {template}=await response.json();
 await page.goto('/#/plans/templates/'+template.id);await page.getByRole('button',{name:'Создать проект',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Создать проект из шаблона',exact:true});await dialog.getByLabel('Начало проекта').fill('2026-12-20');await page.keyboard.press('Escape');await page.getByRole('button',{name:'Продолжить редактирование'}).click();await expect(dialog.getByLabel('Начало проекта')).toHaveValue('2026-12-20');
});
