const {test,expect}=require('../browser-fixtures.cjs');
test('STG marker is visible in the application without changing the route',async({page})=>{
 await page.route('**/api/environment',route=>route.fulfill({json:{staging:true}}));
 await page.goto('/#/plans');
 await expect(page.locator('#stg-banner')).toBeVisible();
 await expect(page.locator('#stg-banner')).toHaveAttribute('title','Тестовое окружение · Только синтетические данные');
 await expect(page).toHaveTitle(/\[STG\]/);
 await expect(page).toHaveURL(/#\/plans/);
 for(const width of [1440,390]){await page.setViewportSize({width,height:900});const badge=page.locator('#stg-banner');await expect(badge).toBeVisible();const box=await badge.boundingBox();expect(box.height).toBeLessThanOrEqual(30);expect(box.x).toBeGreaterThanOrEqual(0);expect(box.x+box.width).toBeLessThanOrEqual(width);expect(await badge.evaluate(n=>getComputedStyle(n).position)).not.toBe('fixed');await page.screenshot({path:'artifacts/ux31-add3/stg-'+width+'.png'});}

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
