const {test,expect}=require('../browser-fixtures.cjs');
test('STG marker is visible in the application without changing the route',async({page})=>{
 await page.route('**/api/environment',route=>route.fulfill({json:{staging:true}}));
 await page.goto('/#/plans');
 await expect(page.locator('#stg-banner')).toBeVisible();
 await expect(page.locator('#stg-banner')).toContainText('Только синтетические данные');
 await expect(page).toHaveTitle(/\[STG\]/);
 await expect(page).toHaveURL(/#\/plans/);
});
test('PROD does not show the test marker',async({page})=>{
 await page.route('**/api/environment',route=>route.fulfill({json:{staging:false}}));
 const metadata=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/environment');
 await page.goto('/#/plans/inbox');
 await metadata;
 await expect(page.getByRole('heading',{name:'Входящие',exact:true})).toBeVisible();
 await expect(page.locator('#stg-banner')).toHaveCount(0);
 await expect(page).not.toHaveTitle(/\[STG\]/);
});
