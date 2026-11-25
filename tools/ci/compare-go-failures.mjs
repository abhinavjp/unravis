// Compares `go test -json` output with docs/desktop/baseline-go-tests.txt.
//   node tools/ci/compare-go-failures.mjs --json t.json --baseline docs/desktop/baseline-go-tests.txt
//   node tools/ci/compare-go-failures.mjs --json t.json --baseline <file> --write   (rewrite the baseline)
// Failure names: "<package>.<TestName>" (subtests included). A package-level failure with no
// failing test (build failure, timeout), or a package whose test binary panicked (later tests never ran),
// is "pkg.<package>". It may be listed in the baseline like any other entry, so a known-crashing package
// is explicit; a package that starts crashing is new.
// Exit 0: no failure outside the baseline. Exit 1: prints "NEW FAILURE <name>" per line.
// A baseline line that now passes prints a ::notice:: and is NOT an error (trim by hand).
// On Windows (the local CI runner) <baseline>.windows.txt, if present, is added to the baseline: tests that fail on a normal,
// non-elevated Windows account (owner SID, Windows paths) and pass or skip on Linux. The Linux entries are another OS's list
// there, so "now passes" notices are not printed on Windows.
// (Spec 08 names this tool Compare-GoFailures.ps1; it is Node so it runs where pwsh is absent. See docs/desktop/ci-audit.md.)
import fs from 'node:fs';

const args = process.argv.slice(2);
const opt = (n) => { const i = args.indexOf(n); return i < 0 ? null : args[i + 1]; };
const jsonFile = opt('--json'), baselineFile = opt('--baseline'), write = args.includes('--write');
if (!jsonFile || !baselineFile) { console.error('usage: --json <file> --baseline <file> [--write] [--out summary.md]'); process.exit(2); }

const failedTests = new Set(), passedTests = new Set(), pkgFail = new Set(), pkgsWithFailedTest = new Set(), pkgPanic = new Set();
for (const line of fs.readFileSync(jsonFile, 'utf8').split('\n')) {
  if (!line.startsWith('{')) continue;
  let e; try { e = JSON.parse(line); } catch { continue; }
  if (!e.Package) continue;
  if (e.Action === 'output' && /^(panic: |FAIL\t.*\[build failed\])/.test(e.Output || '')) pkgPanic.add(e.Package);
  if (e.Action === 'fail' && e.Test) { failedTests.add(`${e.Package}.${e.Test}`); pkgsWithFailedTest.add(e.Package); }
  else if (e.Action === 'pass' && e.Test) passedTests.add(`${e.Package}.${e.Test}`);
  else if (e.Action === 'fail' && !e.Test) pkgFail.add(e.Package);
}
// Parent tests fail when a subtest fails; only a package fail with no failed test is a package-level problem.
// A panic or timeout in a package (even with failed tests) is always package-level: tests after it never ran.
const pkgLevel = [...pkgFail].filter((p) => !pkgsWithFailedTest.has(p) || pkgPanic.has(p)).map((p) => `pkg.${p}`);
const failures = [...failedTests, ...pkgLevel].sort();

if (write) {
  fs.writeFileSync(baselineFile, failures.map((f) => f + '\n').join(''));
  console.log(`wrote ${failures.length} entries to ${baselineFile}`);
  process.exit(0);
}
const readList = (f) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8').split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('#')) : []);
const onWindows = process.platform === 'win32';
const winFile = baselineFile.replace(/\.txt$/, '') + '.windows.txt';
const baseline = new Set([...readList(baselineFile), ...(onWindows ? readList(winFile) : [])]);
const fresh = failures.filter((f) => !baseline.has(f));
const stale = onWindows ? [] : [...baseline].filter((b) => passedTests.has(b));
for (const s of stale) console.log(`::notice::baseline entry now passes, trim it from ${baselineFile}: ${s}`);
for (const f of fresh) console.log(`NEW FAILURE ${f}`);
const summary = `### Go failures vs baseline\n\n| | count |\n|---|---|\n| failing now | ${failures.length} |\n| in baseline | ${baseline.size} |\n| new | ${fresh.length} |\n| baseline now passing | ${stale.length} |\n`;
if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary + (fresh.length ? '\n' + fresh.map((f) => `- NEW FAILURE \`${f}\``).join('\n') + '\n' : ''));
const out = opt('--out'); if (out) fs.writeFileSync(out, summary);
console.log(`${failures.length} failing, ${baseline.size} in baseline, ${fresh.length} new`);
process.exit(fresh.length ? 1 : 0);
