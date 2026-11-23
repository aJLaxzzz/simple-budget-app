const { defineConfig } = require('@playwright/test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
// Never start the application against the repository's user data.
process.env.PLANNER_TEST_DIR ||= fs.mkdtempSync(path.join(os.tmpdir(), 'budget-planner-ui-'));
module.exports = defineConfig({
  testDir: './tests/browser',
  fullyParallel: false,
  workers: 1,
  timeout: 30000,
  use: {
    baseURL: 'http://127.0.0.1:31371',
    browserName: 'chromium',
    channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome',
    viewport: { width: 1440, height: 1000 },
    timezoneId: 'America/Los_Angeles',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'node tests/browser-server.cjs',
    url: 'http://127.0.0.1:31371',
    reuseExistingServer: false,
    timeout: 120000
  }
});
