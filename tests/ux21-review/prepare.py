from pathlib import Path
R='artifacts/ux21-final-verification'
for name in ['capture','coverage-extra']:
 s=Path('tests/final-review/'+name+'.spec.cjs').read_text().replace('artifacts/final-ux-review',R).replace('/screenshots/','/after-screenshots/').replace("'screenshots/","'after-screenshots/").replace('`screenshots/','`after-screenshots/').replace('/manifest.json','/captured.json')
 s=s.replace("name:'Сохранить',exact:true","name:/^(Сохранить|Завершить|Пропустить|Восстановить|Отложить)/")
 s=s.replace("'Архивировать серию'","'Прекратить повторение'").replace("'Новая опорная дата'","'Дата, от которой считать дальше'")
 # History now loads on disclosure; a second button no longer exists.
 s=s.replace("await ed().getByRole('button',{name:'Показать историю',exact:true}).last().click();", "await expect(ed().locator('.pl-history')).toBeVisible();")
 s=s.replace("await ed.getByRole('button',{name:'Показать историю',exact:true}).last().click();", "await expect(ed.locator('.pl-history')).toBeVisible();")
 s=s.replace("await button('Показать историю',ed()).click();", "await expect(ed().locator('.pl-history')).toBeVisible();")
 s=s.replace("await page.getByRole('button',{name:'Показать историю',exact:true}).click();", "await page.locator('summary').filter({hasText:'История повторения'}).click();")
 # Respect subsection split without changing import semantics.
 s=s.replace("await shot('08-icons','custom-icons-empty'", "await go('plans/settings?section=appearance');await shot('08-icons','custom-icons-empty'")
 s=s.replace("await go('plans/settings');const images", "await go('plans/settings?section=appearance');const images")
 s=s.replace("await go('plans/settings');await page.getByLabel('Загрузить иконки')", "await go('plans/settings?section=appearance');await page.getByLabel('Загрузить иконки')")
 s=s.replace("await button('Сохранить',f).click();", "await f.locator('button[type=submit]').click();")
 if name=='capture':
  s=s.replace("const button=(name,host=page)=>host.getByRole('button',{name,exact:true});", "const button=(name,host=page)=>name==='Сохранить'?host.locator('button[type=submit]'):host.getByRole('button',{name,exact:true});")
  s=s.replace("await page.locator('.pl-occurrence').first().getByRole('button',{name:action,exact:true}).click();", "const card=page.locator('.pl-occurrence').first();if(!await card.getByRole('button',{name:action,exact:true}).count()){await card.getByRole('button',{name:'Другие действия'}).click();await button(action,page.getByRole('dialog',{name:'Действия с проблемой'})).click();}else await card.getByRole('button',{name:action,exact:true}).click();")
  s=s.replace("await card.getByRole('button',{name:'Отложить напоминание',exact:true}).click();", "await card.getByRole('button',{name:'Другие действия'}).click();await button('Отложить напоминание',page.getByRole('dialog',{name:'Действия с проблемой'})).click();")
  s=s.replace("await expect(page.getByText(/Черновик сохранён. Повторите предпросмотр/)).toBeVisible();", "await expect(page.getByRole('alert').last()).toContainText('Черновик сохранён');")
  # No incidental toast is removed from DOM: wait for application's natural expiry.
  s=s.replace("current=id;await settle();const width", "current=id;await settle();await expect(page.locator('#planner-toast')).toHaveCount(0,{timeout:7000});await page.mouse.move(2,2);const width")
  s=s.replace("dialogs:[...document.querySelectorAll('dialog[open]')].map(e=>({label:e.getAttribute('aria-label'),height:e.getBoundingClientRect().height,scrollHeight:e.scrollHeight}))", "scrollY:scrollY,content:Object.fromEntries(['.pl-task','.pc-month','.pc-time-controls','.pl-project-card'].map(q=>[q,document.querySelector(q)?.getBoundingClientRect().y??null])),dialogs:[...document.querySelectorAll('dialog[open]')].map(e=>({label:e.getAttribute('aria-labelledby')?.split(' ').map(id=>document.getElementById(id)?.textContent||'').join(' ')||e.getAttribute('aria-label'),height:e.getBoundingClientRect().height,scrollHeight:e.scrollHeight,focusInside:e.contains(document.activeElement)}))")
  s=s.replace("'captured; visual review recorded in UX-REVIEW.md'","'captured; pending independent comparison'")
 else:
  s=s.replace("async function shot(category,id,title,steps=[]){await page.waitForLoadState('networkidle');await page.waitForTimeout(300);", "async function shot(category,id,title,steps=[]){await page.waitForLoadState('networkidle');await page.waitForTimeout(300);await expect(page.locator('#planner-toast')).toHaveCount(0,{timeout:7000});")
 Path('tests/ux21-review/'+name+'.spec.cjs').write_text(s)
