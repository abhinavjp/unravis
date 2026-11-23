// Compares a Playwright JSON report of the e2e suite (tools/e2e) with tools/ci/e2e-linux-known-failures.txt.
//   node tools/ci/compare-e2e-failures.mjs --json report.json --baseline tools/ci/e2e-linux-known-failures.txt [--out summary.md]
// Failure name: "<spec file> > <test title>". A test that fails, times out or is interrupted counts. A skipped test does not.
// A test that passed only on a retry (flaky) counts as a failure too: the suite runs with no retries, so a flaky test is a bug to fix.
// A test with no result at all (never started: the run was interrupted) or with an interrupted result counts as a failure. An error
// in the report itself (for example the demo app failing to stop at the end) is "RUN ERROR" and fails the run, listed or not.
// --max-skipped N fails the run when more than N tests skipped (a change that makes many tests skip must not stay green).
// Exit 0: no failure outside the baseline. Exit 1: prints "NEW FAILURE <name>" per line. Exit 2: bad input, or a report with no
// tests in it (an empty run must not look green). A baseline line that now passes prints a ::notice:: and is NOT an error (trim by hand).
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';

/** Walks the report and returns every test with its file, title and final status. */
export function readReport(report) {
  const tests = [];
  // Describe titles are part of the name, so two tests with one title in different groups stay apart. The file-level suite has the file name as title.
  const walk = (suite, file, path) => {
    const f = suite.file || file;
    for (const spec of suite.specs || []) {
      for (const t of spec.tests || []) {
        const results = t.results || [];
        let status = t.status;
        // A test that never started (no result) or was interrupted did not pass, whatever its status says.
        if ((status === 'skipped' && results.length === 0) || results.some((r) => r.status === 'interrupted')) status = 'unexpected';
        tests.push({ name: [spec.file || f, ...path, spec.title].join(' > '), status });
      }
    }
    for (const child of suite.suites || []) walk(child, f, child.title ? [...path, child.title] : path);
  };
  for (const suite of report.suites || []) walk(suite, suite.file || '', []);
  return tests;
}

export function compare(report, baselineText, maxSkipped = Infinity) {
  const tests = readReport(report);
  const baseline = new Set(baselineText.split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('#')));
  const bad = new Set(['unexpected', 'flaky']);
  const failures = tests.filter((t) => bad.has(t.status)).map((t) => t.name).sort();
  const passed = new Set(tests.filter((t) => t.status === 'expected').map((t) => t.name));
  const fresh = failures.filter((f) => !baseline.has(f));
  const stale = [...baseline].filter((b) => passed.has(b));
  const count = (s) => tests.filter((t) => t.status === s).length;
  const errors = (report.errors || []).map((e) => String(e.message || e).split('\n')[0]);
  return { total: tests.length, passed: count('expected'), skipped: count('skipped'), failures, fresh, stale, baselineSize: baseline.size, errors, tooManySkipped: count('skipped') > maxSkipped };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = process.argv.slice(2);
  const opt = (n) => { const i = args.indexOf(n); return i < 0 ? null : args[i + 1]; };
  const jsonFile = opt('--json'), baselineFile = opt('--baseline'), out = opt('--out');
  const maxSkipped = opt('--max-skipped') === null ? Infinity : Number(opt('--max-skipped'));
  if (!jsonFile || !baselineFile) { console.error('usage: --json <report.json> --baseline <file> [--out summary.md] [--max-skipped N]'); process.exit(2); }
  let report;
  try { report = JSON.parse(fs.readFileSync(jsonFile, 'utf8')); } catch (e) { console.error(`cannot read ${jsonFile}: ${e.message}`); process.exit(2); }
  const baselineText = fs.existsSync(baselineFile) ? fs.readFileSync(baselineFile, 'utf8') : '';
  const r = compare(report, baselineText, maxSkipped);
  if (r.total === 0) { console.error('The report has no tests: the run did not happen.'); process.exit(2); }
  for (const s of r.stale) console.log(`::notice::baseline entry now passes, trim it from ${baselineFile}: ${s}`);
  for (const f of r.fresh) console.log(`NEW FAILURE ${f}`);
  for (const e of r.errors) console.log(`RUN ERROR ${e}`);
  if (r.tooManySkipped) console.log(`TOO MANY SKIPPED ${r.skipped} tests skipped, the limit is ${maxSkipped}`);
  const summary = `### e2e failures vs baseline\n\n| | count |\n|---|---|\n| tests | ${r.total} |\n| passed | ${r.passed} |\n| skipped | ${r.skipped} |\n| failing now | ${r.failures.length} |\n| in baseline | ${r.baselineSize} |\n| new | ${r.fresh.length} |\n| baseline now passing | ${r.stale.length} |\n`;
  if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary + (r.fresh.length ? '\n' + r.fresh.map((f) => `- NEW FAILURE \`${f}\``).join('\n') + '\n' : '') + (r.errors.length ? '\n' + r.errors.map((e) => `- RUN ERROR ${e}`).join('\n') + '\n' : '') + (r.tooManySkipped ? `\n- TOO MANY SKIPPED: ${r.skipped}, limit ${maxSkipped}\n` : ''));
  if (out) fs.writeFileSync(out, summary);
  console.log(`${r.total} tests, ${r.failures.length} failing, ${r.baselineSize} in baseline, ${r.fresh.length} new`);
  process.exit(r.fresh.length || r.errors.length || r.tooManySkipped ? 1 : 0);
}
