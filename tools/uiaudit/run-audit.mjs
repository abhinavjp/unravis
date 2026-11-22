// One command: build the demo (if needed), run every screen in four looks, write report/REPORT.md, exit 1 on new errors.
//   node run-audit.mjs                       everything
//   node run-audit.mjs --only p1-home,p9-wizard-1
//   node run-audit.mjs --report-only         rebuild REPORT.md from the last run's data files
// Environment: UNRAVIS_EXE (demo exe; built with Go when unset), UNRAVIS_SETUP_EXE (capture build of the setup window; built when Go is there),
// UNRAVIS_RELEASES_EXE (Releases dashboard), UNRAVIS_LITE_FIXTURE_EXE (tools/uiaudit/litefixture), both built when Go is there,
// PW_CHROMIUM (a Chromium to use instead of the Playwright one).
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { VARIANTS, classify, loadResults, renderReport, tally, verdict } from './lib/report.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..', '..');
const args = process.argv.slice(2);
const flag = (n) => args.includes(n);
const value = (n) => { const i = args.indexOf(n); return i >= 0 ? args[i + 1] : undefined; };

const reportDir = resolve(value('--report-dir') ?? join(here, 'report'));
const dataDir = join(reportDir, 'data');
const exeSuffix = process.platform === 'win32' ? '.exe' : '';
// Only npx needs a shell on Windows (it is a .cmd file). go is called without one, so paths with spaces stay whole.
const sh = (cmd, a, opt = {}) => spawnSync(cmd, a, { stdio: 'inherit', shell: cmd === 'npx' && process.platform === 'win32', ...opt });

let playwrightFailed = false;
if (!flag('--report-only')) {
  // Windows can hold a file for a moment after a process ends (an exe, a scanner): a few tries before giving up.
  try { rmSync(reportDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 }); } catch (e) { console.error('Could not clear ' + reportDir + ': ' + e.message + '\nA program from an earlier run may still be running. Close it and run again.'); process.exit(2); }
  mkdirSync(dataDir, { recursive: true });
  mkdirSync(join(reportDir, 'shots'), { recursive: true });

  const build = join(reportDir, '.build');
  mkdirSync(build, { recursive: true });
  const haveGo = spawnSync('go', ['version']).status === 0;
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
  if (!process.env.UNRAVIS_RELEASES_EXE && haveGo) {
    const out = join(build, 'unravis-releases' + exeSuffix);
    console.log('Building the Releases dashboard ...');
    if (sh('go', ['build', '-o', out, './cmd/unravis-releases'], { cwd: repo }).status === 0) process.env.UNRAVIS_RELEASES_EXE = out;
    else console.log('The Releases dashboard did not build here: its screens will be reported as not reached.');
  }
  if (!process.env.UNRAVIS_LITE_FIXTURE_EXE && haveGo) {
    const out = join(build, 'litefixture' + exeSuffix);
    console.log('Building the Lite stand-in ...');
    if (sh('go', ['build', '-o', out, './tools/uiaudit/litefixture'], { cwd: repo }).status === 0) process.env.UNRAVIS_LITE_FIXTURE_EXE = out;
    else console.log('The Lite stand-in did not build here: its screens will be reported as not reached.');
  }
  process.env.UNRAVIS_LITE_UI ??= join(repo, 'cmd', 'unravis-lite', 'ui');
  process.env.UIAUDIT_REPORT_DIR = reportDir;
  if (value('--only')) process.env.UIAUDIT_ONLY = value('--only');
  const pw = sh('npx', ['playwright', 'test', '--trace', 'off'], { cwd: here });
  playwrightFailed = pw.status !== 0;
}

const baseline = JSON.parse(readFileSync(join(here, 'baseline.json'), 'utf8'));
const loaded = loadResults(dataDir);

// Every screen must have a result in all four looks. A look with no data file (a timeout, a crash, a setup that never started)
// is a gap and is reported as not reached, never left out of the count.
const screensWanted = [
  ...JSON.parse(readFileSync(join(repo, 'tools', 'screens', 'screens.json'), 'utf8')).screens,
  ...JSON.parse(readFileSync(join(here, 'extra-screens.json'), 'utf8')).screens
].filter((x) => !value('--only') || value('--only').split(',').map((t) => t.trim()).includes(x.id));
const have = new Set(loaded.map((r) => r.id + '|' + r.variant));
for (const sc of screensWanted) {
  for (const v of VARIANTS) {
    if (have.has(sc.id + '|' + v)) continue;
    const [size, scheme] = v.split('-');
    loaded.push({ id: sc.id, phase: sc.phase, note: sc.note ?? '', variant: v, size, scheme, status: 'unreachable', reason: 'no result was written for this look (a timeout or a crash)' });
  }
}
if (!screensWanted.length || !loaded.length) { console.error('No screens to check (is --only right?).'); process.exit(2); }
const results = classify(loaded, baseline);
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
if (playwrightFailed) {
  console.error('Playwright itself reported a failure (setup, or the clean-up steps of a screen). Read its output above: this run is not trustworthy.');
  process.exit(2);
}
process.exit(verdict(t) === 'PASS' ? 0 : 1);
