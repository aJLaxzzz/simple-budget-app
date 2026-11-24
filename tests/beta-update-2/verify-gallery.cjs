// Verify the transferable evidence pack without serving it or using user data.
const { chromium } = require('@playwright/test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL, fileURLToPath } = require('node:url');
const { spawnSync } = require('node:child_process');

(async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'budget-beta-gallery-'));
  let browser;
  try {
    const unpack = spawnSync('python3', ['-c',
      'import sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); assert z.testzip() is None; z.extractall(sys.argv[2])',
      path.resolve('artifacts/beta-update-2.zip'), dir], { encoding: 'utf8' });
    assert.equal(unpack.status, 0, unpack.stderr);
    const root = path.join(dir, 'beta-update-2');
    const manifest = JSON.parse(fs.readFileSync(path.join(root, 'manifest.json')));
    for (const item of manifest.scenarios) for (const key of ['before', 'after', 'comparison']) {
      if (item[key]) assert.ok(fs.existsSync(path.join(root, item[key])), item[key]);
    }
    browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome' });
    const page = await browser.newPage();
    const errors = [], external = [];
    page.on('pageerror', e => errors.push(e.message));
    page.on('request', r => { if (/^https?:/.test(r.url())) external.push(r.url()); });
    await page.goto(pathToFileURL(path.join(root, 'index.html')).href);
    await page.locator('img').evaluateAll(images => images.forEach(img => { img.loading = 'eager'; }));
    await page.waitForFunction(() => [...document.images].every(img => img.complete && img.naturalWidth > 0));
    const links = await page.locator('a[href]').evaluateAll(items => items.map(a => a.href));
    for (const link of links) {
      const url = new URL(link);
      assert.equal(url.protocol, 'file:');
      assert.ok(fs.existsSync(fileURLToPath(url)), link);
    }
    assert.deepEqual(errors, []);
    assert.deepEqual(external, []);
    const result = { scenarios: manifest.scenarios.length, gallery_images: await page.locator('img').count(),
      local_links: links.length, pageerrors: errors.length, external_requests: external.length,
      crc: 'ok', extracted_file_gallery: 'ok' };
    fs.writeFileSync('artifacts/beta-update-2/archive-validation.json', JSON.stringify(result, null, 2) + '\n');
    console.log(JSON.stringify(result));
  } finally {
    if (browser) await browser.close();
    fs.rmSync(dir, { recursive: true, force: true });
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
