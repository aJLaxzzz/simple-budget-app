const {validateTestDir,testEnvironment}=require('./test-storage.cjs');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const dir = validateTestDir(process.env.PLANNER_TEST_DIR);
if (!dir || !path.basename(dir).startsWith('budget-planner-ui-')) throw Error('Temporary test directory required');
fs.writeFileSync(path.join(dir, 'data.json'), '{"months":[]}');
fs.writeFileSync(path.join(dir, 'categories.json'), '{"expense":[],"income":[]}');
const binary = path.join(dir, 'budget-test-server');
const build = spawnSync('go', ['build', '-o', binary, '.'], { stdio: 'inherit', env: testEnvironment() });
if (build.status !== 0) process.exit(build.status || 1);
const server = spawn(binary, [], {
  cwd: dir,
  stdio: 'inherit',
  env: testEnvironment({ BUDGET_ADDR: '127.0.0.1:31371', BUDGET_DATA_DIR: dir, BUDGET_PASSWORD: '', BUDGET_TLS_CERT: '', BUDGET_TLS_KEY: '', BUDGET_ALLOWED_HOSTS: '' })
});
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code || 0));
