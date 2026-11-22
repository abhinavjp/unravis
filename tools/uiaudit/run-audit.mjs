// One command: build the demo (if needed), run every screen in four looks, write report/REPORT.md, exit 1 on new errors.
//   node run-audit.mjs                       everything
//   node run-audit.mjs --only p1-home,p9-wizard-1
//   node run-audit.mjs --report-only         rebuild REPORT.md from the last run's data files
// Environment: UNRAVIS_EXE (demo exe; built with Go when unset), UNRAVIS_SETUP_EXE (capture build of the setup window; built when Go is there),
// PW_CHROMIUM (a Chromium to use instead of the Playwright one).
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { classify, loadResults, renderReport, tally, verdict } from './lib/report.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..', '..');
const args = process.argv.slice(2);
const flag = (n) => args.includes(n);
const value = (n) => { const i = args.indexOf(n); return i >= 0 ? args[i + 1] : undefined; };

const reportDir = resolve(value('--report-dir') ?? join(here, 'report'));
const dataDir = join(reportDir, 'data');
const exeSuffix = process.platform === 'win32' ? '.exe' : '';
const sh = (cmd, a, opt = {}) => spawnSync(cmd, a, { stdio: 'inherit', shell: process.platform === 'win32', ...opt });

if (!flag('--report-only')) {
  rmSync(reportDir, { recursive: true, force: true });
  mkdirSync(dataDir, { recursive: true });
  mkdirSync(join(reportDir, 'shots'), { recursive: true });

  const build = join(reportDir, '.build');
  mkdirSync(build, { recursive: true });
  const haveGo = spawnSync('go', ['version'], { shell: process.platform === 'win32' }).status === 0;
  if (!process.env.UNRAVIS_EXE) {
    if (!haveGo) { console.error('Set UNRAVIS_EXE to a demo-capable unravis exe, or install Go so it can be built.'); process.exit(2); }
    const out = join(build, 'unravis-demo' + exeSuffix);
    console.log('Building the demo app ...');
    if (sh('go', ['build', '-o', out, './cmd/audit-explorer'], { cwd: repo }).status !== 0) process.exit(2);
    process.env.UNRAVIS_EXE = out;
  }
  if (!process.env.UNRAVIS_SETUP_EXE && haveGo) {
    const out = join(build, 'unravis-setup-capture' + exeSuffix);
    console.log('Building the setup window (capture build) ...');
    if (sh('go', ['build', '-tags', 'capture', '-o', out, './cmd/unravis-setup'], { cwd: repo }).status === 0) process.env.UNRAVIS_SETUP_EXE = out;
    else console.log('The setup window did not build here: its screens will be skipped.');
  }
  process.env.UIAUDIT_REPORT_DIR = reportDir;
  if (value('--only')) process.env.UIAUDIT_ONLY = value('--only');
  const pw = sh('npx', ['playwright', 'test', '--trace', 'off'], { cwd: here });
  if (pw.status !== 0 && !existsSync(dataDir)) process.exit(2);
}

const baseline = JSON.parse(readFileSync(join(here, 'baseline.json'), 'utf8'));
const results = classify(loadResults(dataDir), baseline);
const t = tally(results);
const now = new Date();
const pkg = JSON.parse(readFileSync(join(here, 'node_modules', '@playwright', 'test', 'package.json'), 'utf8'));
const md = renderReport({
  results,
  baseline,
  run: { when: now.toISOString().slice(0, 16).replace('T', ' ') + ' UTC', build: 'demo build of this checkout', demoNow: '2026-10-20 09:00 UTC', browser: 'Chromium via Playwright ' + pkg.version }
});
writeFileSync(join(reportDir, 'REPORT.md'), md);
writeFileSync(join(reportDir, 'summary.json'), JSON.stringify({ verdict: verdict(t), ...t }, null, 1));
console.log('\nReport: ' + join(reportDir, 'REPORT.md'));
console.log('Verdict ' + verdict(t) + ': ' + t.newError + ' new errors, ' + t.newWarn + ' new warnings, ' + t.unreachableNew + ' newly unreachable, ' + t.knownError + '+' + t.knownWarn + ' known.');
process.exit(verdict(t) === 'PASS' ? 0 : 1);
