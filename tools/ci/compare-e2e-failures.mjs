// Compares a Playwright JSON report of the e2e suite (tools/e2e) with tools/ci/e2e-linux-known-failures.txt.
//   node tools/ci/compare-e2e-failures.mjs --json report.json --baseline tools/ci/e2e-linux-known-failures.txt [--out summary.md]
// Failure name: "<spec file> > <test title>". A test that fails, times out or is interrupted counts. A skipped test does not.
// A test that passed only on a retry (flaky) counts as a failure too: the suite runs with no retries, so a flaky test is a bug to fix.
// Exit 0: no failure outside the baseline. Exit 1: prints "NEW FAILURE <name>" per line. Exit 2: bad input, or a report with no
// tests in it (an empty run must not look green). A baseline line that now passes prints a ::notice:: and is NOT an error (trim by hand).
import fs from 'node:fs';

/** Walks the report and returns every test with its file, title and final status. */
export function readReport(report) {
  const tests = [];
  const walk = (suite, file) => {
    const f = suite.file || file;
    for (const spec of suite.specs || []) {
      for (const t of spec.tests || []) tests.push({ name: `${spec.file || f} > ${spec.title}`, status: t.status });
    }
    for (const child of suite.suites || []) walk(child, f);
  };
  for (const suite of report.suites || []) walk(suite, suite.file || '');
  return tests;
}

export function compare(report, baselineText) {
  const tests = readReport(report);
  const baseline = new Set(baselineText.split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('#')));
  const bad = new Set(['unexpected', 'flaky']);
  const failures = tests.filter((t) => bad.has(t.status)).map((t) => t.name).sort();
  const passed = new Set(tests.filter((t) => t.status === 'expected').map((t) => t.name));
  const fresh = failures.filter((f) => !baseline.has(f));
  const stale = [...baseline].filter((b) => passed.has(b));
  const count = (s) => tests.filter((t) => t.status === s).length;
  return { total: tests.length, passed: count('expected'), skipped: count('skipped'), failures, fresh, stale, baselineSize: baseline.size };
}

if (process.argv[1] && import.meta.url === new URL(process.argv[1], 'file://').href) {
  const args = process.argv.slice(2);
  const opt = (n) => { const i = args.indexOf(n); return i < 0 ? null : args[i + 1]; };
  const jsonFile = opt('--json'), baselineFile = opt('--baseline'), out = opt('--out');
  if (!jsonFile || !baselineFile) { console.error('usage: --json <report.json> --baseline <file> [--out summary.md]'); process.exit(2); }
  let report;
  try { report = JSON.parse(fs.readFileSync(jsonFile, 'utf8')); } catch (e) { console.error(`cannot read ${jsonFile}: ${e.message}`); process.exit(2); }
  const baselineText = fs.existsSync(baselineFile) ? fs.readFileSync(baselineFile, 'utf8') : '';
  const r = compare(report, baselineText);
  if (r.total === 0) { console.error('The report has no tests: the run did not happen.'); process.exit(2); }
  for (const s of r.stale) console.log(`::notice::baseline entry now passes, trim it from ${baselineFile}: ${s}`);
  for (const f of r.fresh) console.log(`NEW FAILURE ${f}`);
  const summary = `### e2e failures vs baseline\n\n| | count |\n|---|---|\n| tests | ${r.total} |\n| passed | ${r.passed} |\n| skipped | ${r.skipped} |\n| failing now | ${r.failures.length} |\n| in baseline | ${r.baselineSize} |\n| new | ${r.fresh.length} |\n| baseline now passing | ${r.stale.length} |\n`;
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary + (r.fresh.length ? '\n' + r.fresh.map((f) => `- NEW FAILURE \`${f}\``).join('\n') + '\n' : ''));
  if (out) fs.writeFileSync(out, summary);
  console.log(`${r.total} tests, ${r.failures.length} failing, ${r.baselineSize} in baseline, ${r.fresh.length} new`);
  process.exit(r.fresh.length ? 1 : 0);
}
