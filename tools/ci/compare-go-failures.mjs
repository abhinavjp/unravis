// Compares `go test -json` output with docs/desktop/baseline-go-tests.txt.
//   node tools/ci/compare-go-failures.mjs --json t.json --baseline docs/desktop/baseline-go-tests.txt
//   node tools/ci/compare-go-failures.mjs --json t.json --baseline <file> --write   (rewrite the baseline)
// Failure names: "<package>.<TestName>" (subtests included). A package-level failure with no
// failing test (build failure, timeout), or a package whose test binary panicked (later tests never ran),
// is "pkg.<package>". It may be listed in the baseline like any other entry, so a known-crashing package
// is explicit; a package that starts crashing is new.
// Exit 0: no failure outside the baseline. Exit 1: prints "NEW FAILURE <name>" per line.
// A baseline line that now passes prints a ::notice:: and is NOT an error (trim by hand).
// Known flaky tests live in docs/ci/flaky-tests.txt (see Quarantine below); each needs a BL-id and an end date.
// (Spec 08 names this tool Compare-GoFailures.ps1; it is Node so it runs where pwsh is absent. See docs/desktop/ci-audit.md.)
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const args = process.argv.slice(2);
const opt = (n) => { const i = args.indexOf(n); return i < 0 ? null : args[i + 1]; };
let baselineFile = opt('--baseline');
const jsonFile = opt('--json'), write = args.includes('--write');
if (!jsonFile || !baselineFile) { console.error('usage: --json <file> --baseline <file> [--write] [--out summary.md]'); process.exit(2); }

// The baseline holds the failures of the machine CI runs on (Linux: the Windows-only tests fail there). The local
// pipeline runs this step on a Windows PC, where a different set fails (privileges, the startup folder, ...), so
// there the sibling "<name>.windows.txt" is the baseline when it exists. Same rule either way: only a failure that
// is not in the baseline is new. Without that file a Windows run compares with the Linux baseline, as before.
// --os pins the host for the tests.
const hostOS = opt('--os') || process.platform;
if (!write && hostOS === 'win32' && baselineFile && !/\.windows\.txt$/.test(baselineFile)) {
  const alt = baselineFile.replace(/\.txt$/, '.windows.txt');
  if (fs.existsSync(alt)) { console.log(`Windows host: comparing with ${alt}`); baselineFile = alt; }
}

const testOutput = new Map(); // "<package>.<Test>" -> its output chunks
const failedTests = new Set(), passedTests = new Set(), pkgFail = new Set(), pkgsWithFailedTest = new Set(), pkgPanic = new Set();
for (const line of fs.readFileSync(jsonFile, 'utf8').split('\n')) {
  if (!line.startsWith('{')) continue;
  let e; try { e = JSON.parse(line); } catch { continue; }
  if (!e.Package) continue;
  if (e.Action === 'output' && e.Test) { const k = `${e.Package}.${e.Test}`; if (!testOutput.has(k)) testOutput.set(k, []); testOutput.get(k).push(e.Output || ''); }
  if (e.Action === 'output' && /^(panic:|FAIL\t.*\[build failed\])/.test(e.Output || '')) pkgPanic.add(e.Package);
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
const baseline = new Set(fs.existsSync(baselineFile) ? fs.readFileSync(baselineFile, 'utf8').split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('#')) : []);
// Quarantine (docs/ci/THREAD-GATE.md section 3): docs/ci/flaky-tests.txt lists tests that are flaky or depend on the host.
// One line each: "<package>.<Test> <BL-id> until=<YYYY-MM-DD> <reason>". A quarantined failure is reported, not new. A line
// without a BL-id or a valid until date, or past its date, quarantines nothing (a ::warning:: says so), so a test that is
// not fixed in time fails the gate again. --quarantine <file> picks another file, --today pins the date for the tests.
const quarantineFile = opt('--quarantine') || fileURLToPath(new URL('../../docs/ci/flaky-tests.txt', import.meta.url));
const today = opt('--today') || new Date().toISOString().slice(0, 10);
const quarantined = new Map(); // name -> "<BL-id>, until <date>"
if (fs.existsSync(quarantineFile)) {
  for (const raw of fs.readFileSync(quarantineFile, 'utf8').split('\n')) {
    const line = raw.trim(); if (!line || line.startsWith('#')) continue;
    const m = /^(\S+)\s+(BL-\d+)\s+until=(\d{4}-\d{2}-\d{2})\s+\S/.exec(line);
    if (!m) { console.log(`::warning::${quarantineFile}: not a valid quarantine line, ignored: ${line}`); continue; }
    if (m[3] < today) { console.log(`::warning::quarantine of ${m[1]} ended on ${m[3]}: fix ${m[2]} or renew the date`); continue; }
    quarantined.set(m[1], `${m[2]}, until ${m[3]}`);
  }
}
// A test whose only complaint is that its t.TempDir() could not be removed (a virus scanner, the search indexer or a child
// process still holds a file on a Windows PC) did everything it checks: report it, do not fail the gate on it.
const cleanupOnly = (f) => {
  const lines = (testOutput.get(f) || []).join('').split('\n').map((l) => l.trim()).filter((l) => l && !/^(=== |--- |PASS$|FAIL$)/.test(l));
  return lines.length > 0 && lines.every((l) => /TempDir RemoveAll cleanup/.test(l));
};
const cleanup = failures.filter((f) => !baseline.has(f) && !quarantined.has(f) && cleanupOnly(f));
for (const f of cleanup) console.log(`::notice::only the temp folder cleanup failed (the host kept a file open), not counted: ${f}`);
const held = failures.filter((f) => !baseline.has(f) && quarantined.has(f));
const fresh = failures.filter((f) => !baseline.has(f) && !quarantined.has(f) && !cleanup.includes(f));
for (const f of held) console.log(`::notice::quarantined failure (${quarantined.get(f)}): ${f}`);
for (const [q, why] of quarantined) if (passedTests.has(q)) console.log(`::notice::quarantined test passes now (${why}), drop its line if it stays green: ${q}`);
const stale = [...baseline].filter((b) => passedTests.has(b));
for (const s of stale) console.log(`::notice::baseline entry now passes, trim it from ${baselineFile}: ${s}`);
for (const f of fresh) {
  console.log(`NEW FAILURE ${f}`);
  // The last lines of the failing test's own output, so the log says why (the JSON itself is not kept).
  const lines = (testOutput.get(f) || []).join('').split('\n').filter((l) => l.trim()).slice(-40);
  for (const l of lines) console.log(`    | ${l}`);
}
const summary = `### Go failures vs baseline\n\n| | count |\n|---|---|\n| failing now | ${failures.length} |\n| in baseline | ${baseline.size} |\n| new | ${fresh.length} |\n| quarantined and failing | ${held.length} |\n| temp-folder cleanup only | ${cleanup.length} |\n| baseline now passing | ${stale.length} |\n`;
if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary + (fresh.length ? '\n' + fresh.map((f) => `- NEW FAILURE \`${f}\``).join('\n') + '\n' : ''));
const out = opt('--out'); if (out) fs.writeFileSync(out, summary);
console.log(`${failures.length} failing, ${baseline.size} in baseline, ${fresh.length} new`);
process.exit(fresh.length ? 1 : 0);
