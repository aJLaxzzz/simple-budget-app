from pathlib import Path
p=Path('tests/browser/ux21.spec.cjs');s=p.read_text()
R='artifacts/ux21-final-verification'
s=s.replace("test.use({viewport:{width:1440,height:900},timezoneId:'Europe/Moscow'});", "test.use({viewport:{width:1440,height:900},timezoneId:'Europe/Moscow'});\ntest.beforeEach(async({page})=>{await page.clock.setFixedTime(new Date('2026-11-18T09:00:00+03:00'));});\nconst fs=require('node:fs');fs.mkdirSync('"+R+"/after-screenshots/11-ux21-fixes',{recursive:true});\nasync function snap(page,name){await page.waitForLoadState('networkidle');await page.evaluate(()=>document.fonts.ready);await page.screenshot({path:'"+R+"/after-screenshots/11-ux21-fixes/'+name+'-1440.png',animations:'disabled'});}")
a=s.index('const today=');b=s.index('\nconst day=',a);s=s[:a]+"const today=()=> '2026-11-18';"+s[b:]
s=s.replace("artifacts/ux21/",R+'/after-screenshots/11-ux21-fixes/')
s=s.replace("await expect(guard).toBeVisible();await guard", "await expect(guard).toBeVisible();await snap(page,'def01-unsaved-guard');await guard")
s=s.replace("expect(child.planned_date).toBe(day(13));", "expect(child.planned_date).toBe(day(13));await snap(page,'def01-created-project');fs.writeFileSync('"+R+"/checks/def01.json',JSON.stringify({same_request:bodies[0].request_id===bodies[1].request_id,project_count:projects.items.length,start_date:project.start_date,child_date:child.planned_date,parent_link:child.parent_task_id===root.id},null,2));")
s=s.replace("await page.goto('/#/plans/calendar?date='+today()+'&project='+p.id+'&completed=1&cancelled=1');", "await page.goto('/#/plans/calendar?date='+today()+'&project='+p.id+'&completed=1&cancelled=1');await snap(page,'filters-active');")
s=s.replace("await expect(page.getByRole('button',{name:'Сбросить фильтры',exact:true})).toHaveCount(0);", "await expect(page.getByRole('button',{name:'Сбросить фильтры',exact:true})).toHaveCount(0);await snap(page,'filters-reset-default');")
s=s.replace("await expect(page.getByText('Выполнено 1 из 2',{exact:true})).toBeVisible();", "await expect(page.getByText('Выполнено 1 из 2',{exact:true})).toBeVisible();await snap(page,'project-skipped-fifty-percent');")
s=s.replace("expect(dates.every(d=>d.includes('Выполнение'))).toBeTruthy();", "expect(dates.every(d=>d.includes('Выполнение'))).toBeTruthy();await snap(page,'dated-project-instances');")
s=s.replace("await expect(page.locator('.pl-progress-label')).toHaveCount(0);", "await expect(page.locator('.pl-progress-label')).toHaveCount(0);await snap(page,'empty-completed-project');")
s=s.replace("expect(historyCalls).toBe(1);", "expect(historyCalls).toBe(1);await snap(page,'history-immediate');")
s=s.replace("await page.keyboard.press('Escape');await expect(editor.getByRole('button',{name:'Действия с задачей',exact:true})).toBeFocused();", "await snap(page,'dialog-focus-trapped');await page.keyboard.press('Escape');await expect(editor.getByRole('button',{name:'Действия с задачей',exact:true})).toBeFocused();")
s=s.replace("await page.goto('/#/plans/settings');await expect(page.locator('#planner-toast')).toHaveCount(0);", "await snap(page,'notification-object-specific');await page.goto('/#/plans/settings');await expect(page.locator('#planner-toast')).toHaveCount(0);await snap(page,'notification-cleared-on-navigation');")
s=s.replace("await expect(card.getByRole('button',{name:'Перенести',exact:true})).toBeVisible();", "await expect(card.getByRole('button',{name:'Перенести',exact:true})).toBeVisible();await snap(page,'attention-primary');")
s=s.replace("await menu.getByRole('button',{name:'Отменить',exact:true}).click();", "await snap(page,'attention-secondary-menu');await menu.getByRole('button',{name:'Отменить',exact:true}).click();")
# Audit test: report observations even if geometry assertion exposes a regression.
Path('tests/ux21-review/fixes.spec.cjs').write_text(s)
