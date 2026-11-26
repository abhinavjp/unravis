// Self-test of tools/ci/fast-scope.mjs (pure parts). Run: node tools/ci/test-fast-scope.mjs
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { classify, withDependents, CROSS_WEB, HEAVY_DOCS, NOTES } from './fast-scope.mjs';

const dirs = new Set(['.', 'cmd/unravis-lite', 'cmd/unravis-setup', 'cmd/audit-explorer', 'internal/store', 'internal/investigate', 'internal/server', 'internal/evidence', 'internal/setup']);
const c = (files) => classify(files, dirs);
const keys = (x) => ({ notes: x.notes, all: x.all, web: x.web, dirs: [...x.dirs].sort() });

// Notes only: nothing to run. A note in docs/user is not a note.
assert.deepEqual(keys(c(['docs/ci/RELEASE-TRAIN.md', '.forge/x/y.md', 'README.md', 'LICENSE'])), { notes: true, all: false, web: false, dirs: [] });
assert.equal(c(['docs/user/guide.md']).notes, false);
assert.deepEqual(keys(c(['docs/user/guide.md'])).dirs, ['.', 'internal/investigate']);
// Baseline file: every package (it changes how failures are judged).
assert.equal(c(['docs/desktop/baseline-go-tests.txt']).all, true);
assert.equal(c(['docs/desktop/baseline-go-race.txt']).all, true);
// The quarantine file is read by compare-go-failures.mjs (linux-gates, go-race, the fast gate): it is a gate input, not a note.
assert.equal(c(['docs/ci/flaky-tests.txt']).all, true);
assert.equal(c(['docs/ci/flaky-tests.txt']).notes, false);
assert.equal(c(['docs/ci/THREAD-GATE.md']).notes, true);    // the other files of docs/ci stay notes
// The HEAVY rule is case-insensitive (a Docs/ spelling of a gate input still widens); the NOTES rule is case-sensitive, so a path that
// only looks like a note on a case-insensitive disk (Docs/pkg/a.go, .FORGE/x.go) runs the heavy jobs on the case-sensitive Linux runner.
assert.equal(c(['Docs/Desktop/baseline-go-tests.txt']).all, true);
assert.equal(c(['DOCS/ci/Flaky-Tests.txt']).all, true);
assert.equal(c(['Docs/user/guide.md']).notes, false);
assert.deepEqual(keys(c(['Docs/user/guide.md'])).dirs, ['.', 'internal/investigate']);
for (const f of ['Docs/ci/RELEASE-TRAIN.md', 'README.MD', 'license', 'Notice', 'Docs/pkg/a.go', '.FORGE/x.go']) assert.equal(c([f]).notes, false, `${f} is not a note (case-sensitive)`);
// ci.yml (job `changes`) and fast-scope.mjs must agree: compare the two grep patterns with the exports, and on sample paths.
{
  const yml = fs.readFileSync(new URL('../../.github/workflows/ci.yml', import.meta.url), 'utf8');
  const pats = [...yml.matchAll(/grep (-E\w*) '([^']+)'; then/g)].filter((m) => m[2].includes('docs/user/') || m[2].includes('LICENSE'));
  assert.equal(pats.length, 2, 'the heavy and the notes grep of ci.yml');
  assert.match(pats[0][1], /i/, `heavy grep flags ${pats[0][1]} must be case-insensitive`);
  assert.doesNotMatch(pats[1][1], /i/, `notes grep flags ${pats[1][1]} must stay case-sensitive`);
  assert.equal(HEAVY_DOCS.source.replaceAll('\\/', '/'), pats[0][2], 'heavy pattern differs between ci.yml and fast-scope.mjs');
  assert.equal(NOTES.source.replaceAll('\\/', '/'), pats[1][2], 'notes pattern differs between ci.yml and fast-scope.mjs');
  assert.ok(HEAVY_DOCS.flags.includes('i') && !NOTES.flags.includes('i'));
  const heavy = new RegExp(pats[0][2], 'i'), notes = new RegExp(pats[1][2]);
  for (const f of ['Docs/pkg/a.go', '.FORGE/x.go', 'README.MD','docs/ci/flaky-tests.txt', 'Docs/Desktop/baseline-go-tests.txt', 'docs/desktop/baseline-go-race.txt', 'docs/user/a.md', 'DOCS/USER/a.md', 'docs/ci/THREAD-GATE.md', 'Docs/x.md', 'README.md', 'internal/a.go', '.forge/a']) {
    const inYml = heavy.test(f) ? 'code' : notes.test(f) ? 'notes' : 'code';
    const inScope = c([f]).notes ? 'notes' : 'code';
    assert.equal(inYml, inScope, `ci.yml and fast-scope.mjs disagree on ${f}`);
  }
}
// A Go file maps to its package; a testdata file too; the root counts only for files directly in the root.
assert.deepEqual(keys(c(['internal/store/q.go'])).dirs, ['internal/store']);
assert.deepEqual(keys(c(['internal/store/testdata/a.json'])).dirs, ['internal/store']);
assert.deepEqual(keys(c(['cmd/unravis-lite/ui/index.html'])).dirs, ['cmd/unravis-lite']);
assert.deepEqual(keys(c(['assets.go'])).dirs, ['.']);
assert.equal(c(['some/new/dir/x.go']).all, true);          // unknown path: widen
assert.equal(c(['mystery.bin']).all, false);                // root file with a root package: the root package
assert.deepEqual(keys(c(['mystery.bin'])).dirs, ['.']);
// Wide files.
for (const f of ['go.mod', 'go.sum', '.golangci.yml', 'build.ps1']) assert.equal(c([f]).all, true, f);
// Web: the source is web only; the committed build also runs the root package.
assert.deepEqual(keys(c(['web/src/App.svelte'])), { notes: false, all: false, web: true, dirs: [] });
assert.deepEqual(keys(c(['web/dist/index.html'])).dirs, ['.']);
// Outside Go: no Go packages, not notes, not web (the rest of the checks still run).
const o = c(['desktop/src/main.rs', 'tools/ci/x.mjs', '.github/dependabot.yml']);
assert.deepEqual(keys(o), { notes: false, all: false, web: false, dirs: [] });
// A .go file under tools is still Go.
assert.equal(c(['tools/release/x.go']).gofiles.length, 1);
assert.deepEqual(keys(c(['contracts/v1/schema/info.schema.json'])), { notes: false, all: false, web: false, dirs: ['internal/server'] });
// Tests that read other directories (found in the review of PR 126): web/src also tests the Go packages that read it; setup code also tests the gate test.
assert.deepEqual(keys(c(['web/src/lib/copy/wordings.ts'])), { notes: false, all: false, web: true, dirs: ['internal/evidence', 'internal/server'] });
assert.deepEqual(keys(c(['internal/setup/plan.go'])).dirs, ['cmd/audit-explorer', 'internal/setup']);
assert.deepEqual(keys(c(['cmd/unravis-setup/main.go'])).dirs, ['cmd/audit-explorer', 'cmd/unravis-setup']);
assert.equal(c(['internal/server/routes.go']).web, true);   // web/tests/guidedKept reads it
assert.equal(c(['internal/store/q.go']).web, false);
assert.equal(CROSS_WEB.test('tools/e2e/archive/guided/x.json'), true);
// guided.ui.test.cjs reads these two (sweep 2026-11-22 finding 3); a Go-only change to them must run the web job. test-web-scope.mjs checks every read.
for (const f of ['internal/audit/models.go', 'internal/casestore/plan.go']) {
  assert.equal(CROSS_WEB.test(f), true, f);
  assert.equal(c([f]).web, true, f);
}
assert.equal(c(['internal/audit/other.go']).web, false);
assert.equal(c(['docs/user/guide.md']).web, true);
// Windows separators.
assert.deepEqual(keys(c(['internal\\store\\q.go'])).dirs, ['internal/store']);

// Dependents: the changed package, everything importing it, and what imports those.
const g = new Map([['m/store', []], ['m/server', ['m/store']], ['m/cmd', ['m/server']], ['m/other', ['m/util']], ['m/util', []]]);
assert.deepEqual(withDependents(g, ['m/store']), ['m/cmd', 'm/server', 'm/store']);
assert.deepEqual(withDependents(g, ['m/util']), ['m/other', 'm/util']);
assert.deepEqual(withDependents(g, []), []);
// A cycle through test imports must not loop forever.
assert.deepEqual(withDependents(new Map([['a', ['b']], ['b', ['a']]]), ['a']), ['a', 'b']);
console.log('fast-scope tests ok');
