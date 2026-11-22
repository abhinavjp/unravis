// Self-test of tools/ci/fast-scope.mjs (pure parts). Run: node tools/ci/test-fast-scope.mjs
import assert from 'node:assert/strict';
import { classify, withDependents } from './fast-scope.mjs';

const dirs = new Set(['.', 'cmd/unravis-lite', 'internal/store', 'internal/investigate', 'internal/server']);
const c = (files) => classify(files, dirs);
const keys = (x) => ({ notes: x.notes, all: x.all, web: x.web, dirs: [...x.dirs].sort() });

// Notes only: nothing to run. A note in docs/user is not a note.
assert.deepEqual(keys(c(['docs/ci/RELEASE-TRAIN.md', '.forge/x/y.md', 'README.md', 'LICENSE'])), { notes: true, all: false, web: false, dirs: [] });
assert.equal(c(['docs/user/guide.md']).notes, false);
assert.deepEqual(keys(c(['docs/user/guide.md'])).dirs, ['.', 'internal/investigate']);
// Baseline file: every package (it changes how failures are judged).
assert.equal(c(['docs/desktop/baseline-go-tests.txt']).all, true);
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
const o = c(['desktop/src/main.rs', 'tools/e2e/x.mjs', 'tools/ci/x.mjs', '.github/dependabot.yml']);
assert.deepEqual(keys(o), { notes: false, all: false, web: false, dirs: [] });
// A .go file under tools is still Go.
assert.equal(c(['tools/release/x.go']).gofiles.length, 1);
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
