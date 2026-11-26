// Self-test of tools/whatsnew (the What's new fragments, the notes compiler, the picture plan). No network, no browser.
//   node tools/ci/test-whatsnew.mjs
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildEntry, compareVersions, isReusePath, loadFragments, mergeNotes, planShots, plainSubjects, problemsInEntry, validateFragment, versionFolder } from '../whatsnew/lib.mjs';
import { compile } from '../whatsnew/compile-notes.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
let n = 0;
const test = (name, fn) => { fn(); n++; console.log('ok - ' + name); };
const frag = (name, data) => ({ name, file: name + '.json', data: { schema: 1, ...data } });

test('version folders: release candidates and final releases', () => {
  assert.equal(versionFolder('0.1.0-rc.64'), 'rc64');
  assert.equal(versionFolder('v0.1.0-rc.7'), 'rc7');
  assert.equal(versionFolder('1.2.3'), 'v1-2-3');
  assert.throws(() => versionFolder('dev'), /not X\.Y\.Z/);
});

test('reuse paths follow the same rule as the app reader', () => {
  assert.ok(isReusePath('/whatsnew/rc62/ai-access.jpg'));
  assert.ok(isReusePath('/whatsnew/rc5/a..b.png'));
  for (const bad of ['/whatsnew/../x.png', '/whatsnew/rc5/..', 'https://x.example/a.png', '/other/a.png', '/whatsnew//a.png', '/whatsnew/a b.png', '/whatsnew/a.png?x=1', '/whatsnew/%2e%2e/x.png', 7, null]) assert.ok(!isReusePath(bad), String(bad));
});

test('a fragment is checked: schema, words, picture kinds, screen ids, steps, odd characters', () => {
  const ids = new Set(['home', 'timeline']);
  const ok = { schema: 1, headline: 'Charts are clearer', highlights: [{ label: 'Charts', text: 'Axes now show real units.', image: { screen: 'home' }, before: { screen: 'home' } }, { text: 'A note.' }], changes: ['one'] };
  assert.deepEqual(validateFragment(ok, 'a', ids), []);
  assert.deepEqual(validateFragment({ schema: 1 }, 'empty', ids), []);
  const bad = (f, re) => assert.match(validateFragment({ schema: 1, ...f }, 'x', ids).join('\n'), re);
  assert.match(validateFragment({ schema: 2 }, 'x', ids).join(), /schema/);
  assert.match(validateFragment([], 'x', ids).join(), /JSON object/);
  bad({ highlights: [{ text: '' }] }, /text/);
  bad({ highlights: [{ text: 'a', before: { screen: 'home' } }] }, /needs an "image"/);
  bad({ highlights: [{ text: 'a', image: { screen: 'nope' } }] }, /no screen "nope"/);
  bad({ highlights: [{ text: 'a', image: { screen: 'home', steps: [{ click: 'x' }] } }] }, /exactly one/);
  bad({ highlights: [{ text: 'a', image: { steps: [{ shout: 'x' }] } }] }, /must be one of/);
  bad({ highlights: [{ text: 'a', image: { steps: [] } }] }, /at least one step/);
  bad({ highlights: [{ text: 'a', image: { screen: 'home', theme: 'pink' } }] }, /theme/);
  bad({ highlights: [{ text: 'a', image: { screen: 'home', wait: 99999 } }] }, /wait/);
  bad({ highlights: [{ text: 'a', image: { screen: 'home', clip: ' ' } }] }, /clip/);
  bad({ highlights: [{ text: 'use <b>bold</b>' }] }, /no < or >/);
  bad({ highlights: [{ text: 'a'.repeat(601) }] }, /text/);
  bad({ highlights: [{ text: 'a', label: 'l'.repeat(41) }] }, /label/);
  bad({ changes: ['x'.repeat(301)] }, /changes/);
  assert.deepEqual(validateFragment({ schema: 1, highlights: [{ text: 'a', image: '/whatsnew/rc62/ai-access.jpg' }] }, 'x', ids), []);
  assert.deepEqual(validateFragment({ schema: 1, highlights: [{ text: 'a', image: { steps: [{ goto: '#x' }, { aside: [{ click: 'b' }], when: 'wide' }] } }] }, 'x', ids), []);
});

test('the picture plan names every picture from the fragment, the position and the kind', () => {
  const f1 = frag('charts', { highlights: [{ text: 'a' }, { text: 'b', image: { screen: 'home' }, before: { screen: 'home' } }] });
  const f2 = frag('export', { highlights: [{ text: 'c', image: '/whatsnew/rc60/chart-axes.jpg' }, { text: 'd', image: { steps: [{ goto: '#x' }] } }] });
  const { shots, resolved } = planShots([f1, f2], '0.1.0-rc.64');
  assert.deepEqual(shots.map((s) => s.path), ['/whatsnew/rc64/charts-2.jpg', '/whatsnew/rc64/charts-2-before.jpg', '/whatsnew/rc64/export-2.jpg']);
  assert.equal(shots[0].file, 'web/public/whatsnew/rc64/charts-2.jpg');
  assert.deepEqual(shots.map((s) => s.role), ['image', 'before', 'image']);
  assert.equal(resolved.get(f2.data.highlights[0]).image, '/whatsnew/rc60/chart-axes.jpg');
});

test('an entry reads the three kinds: note, screenshot, before and after; the headline falls back', () => {
  const f1 = frag('b-second', { headline: 'Second wins? no', highlights: [{ text: 'Plain note' }] });
  const f2 = frag('a-first', { headline: '  First wins  ', highlights: [{ label: 'Charts', text: 'Shot', image: { screen: 'home' }, alt: 'A chart' }, { text: 'Both', image: { screen: 'home' }, before: { screen: 'home' } }], changes: ['extra'] });
  const e = buildEntry({ version: '0.1.0-rc.64', date: '2026-11-27', fragments: [f2, f1], subjects: ['extra', 'Fix the thing'] });
  assert.equal(e.headline, 'First wins');
  assert.deepEqual(e.highlights.map((h) => [!!h.image, !!h.before]), [[true, false], [true, true], [false, false]]);
  assert.equal(e.highlights[0].label, 'Charts');
  assert.equal(e.highlights[0].alt, 'A chart');
  assert.deepEqual(e.changes, ['extra', 'Fix the thing']);
  const quiet = buildEntry({ version: '0.1.0-rc.65', date: '2026-11-28', fragments: [], subjects: [] });
  assert.equal(quiet.headline, 'Improvements and fixes behind the scenes');
  assert.deepEqual(quiet.highlights, []);
  const fromNote = buildEntry({ version: '0.1.0-rc.65', date: '2026-11-28', fragments: [frag('n', { highlights: [{ text: 'Plain note here' }] })] });
  assert.equal(fromNote.headline, 'Plain note here');
  assert.equal(buildEntry({ version: '0.1.0-rc.65', date: '2026-11-28', fragments: [], subjects: ['x'.repeat(400)] }).changes[0].length, 300);
});

test('commit subjects: merges, CI-only and bookkeeping lines go; pull request numbers go; repeats collapse', () => {
  assert.deepEqual(plainSubjects(['Merge pull request #25 from x/y', 'CI: pin an action', 'Rebuild web files for promotion', 'Backlog: tick BL-1', 'Charts get real units (#123)', 'Charts get real units', '', 'Fix export']), ['Charts get real units', 'Fix export']);
});

test('versions order as numbers and a final release comes after its candidates', () => {
  assert.ok(compareVersions('0.1.0-rc.9', '0.1.0-rc.10') < 0);
  assert.ok(compareVersions('0.1.0', '0.1.0-rc.99') > 0);
  assert.equal(compareVersions('v0.1.0-rc.5', '0.1.0-rc.5'), 0);
});

test('merging into the notes file replaces the same version, keeps newest first and starts a file when there is none', () => {
  const mk = (version) => ({ version, date: '2026-11-01', headline: 'h', highlights: [], changes: [] });
  const file = { schema: 1, releases: [mk('0.1.0-rc.9'), mk('0.1.0-rc.5')] };
  const merged = mergeNotes(file, { ...mk('0.1.0-rc.10'), headline: 'new' });
  assert.deepEqual(merged.releases.map((r) => r.version), ['0.1.0-rc.10', '0.1.0-rc.9', '0.1.0-rc.5']);
  const again = mergeNotes(merged, { ...mk('0.1.0-rc.10'), headline: 'newer' });
  assert.equal(again.releases.length, 3);
  assert.equal(again.releases[0].headline, 'newer');
  assert.deepEqual(mergeNotes(null, mk('0.1.0-rc.1')).releases.map((r) => r.version), ['0.1.0-rc.1']);
  assert.equal(file.releases.length, 2, 'the input is not changed');
});

test('an entry the app would drop parts of is refused', () => {
  const e = { version: '0.1.0-rc.64', date: '2026-11-27', headline: 'h', highlights: [{ text: 'a', image: '/whatsnew/rc64/a.jpg' }, { text: 'b', image: '/whatsnew/rc64/b.jpg', before: '/whatsnew/rc64/b0.jpg' }], changes: [] };
  assert.deepEqual(problemsInEntry(e, () => true), []);
  assert.match(problemsInEntry(e, (p) => p !== '/whatsnew/rc64/b0.jpg').join(), /b0\.jpg" does not exist/);
  assert.match(problemsInEntry({ ...e, version: 'dev' }, () => true).join(), /version/);
  assert.match(problemsInEntry({ ...e, date: 'soon' }, () => true).join(), /date/);
  assert.match(problemsInEntry({ ...e, headline: '' }, () => true).join(), /headline/);
});

// A throw-away repository folder: fragments, notes file, pictures.
const temp = mkdtempSync(join(tmpdir(), 'unravis-whatsnew-test-'));
const make = (name) => {
  const root = join(temp, name);
  for (const d of ['docs/whatsnew/fragments', 'web/public/releases', 'web/public/whatsnew/rc64', 'tools/screens']) mkdirSync(join(root, d), { recursive: true });
  writeFileSync(join(root, 'tools/screens/screens.json'), JSON.stringify({ screens: [{ id: 'home', steps: [] }] }));
  writeFileSync(join(root, 'web/public/releases/notes.json'), JSON.stringify({ schema: 1, releases: [{ version: '0.1.0-rc.63', date: '2026-11-20', headline: 'old', highlights: [], changes: ['x'] }] }));
  return root;
};
const opts = (root, extra = {}) => ({ version: '0.1.0-rc.64', date: '2026-11-27', sinceTag: '', consume: false, checkFiles: true, dry: false, repo: root, fragments: join(root, 'docs/whatsnew/fragments'), notes: join(root, 'web/public/releases/notes.json'), ...extra });
const readNotes = (root) => JSON.parse(readFileSync(join(root, 'web/public/releases/notes.json'), 'utf8'));

test('compile: fragments become a new newest entry, the fragments are consumed, a second run changes nothing', () => {
  const root = make('ok');
  writeFileSync(join(root, 'docs/whatsnew/fragments/charts.json'), JSON.stringify({ schema: 1, headline: 'Clearer charts', highlights: [{ text: 'Axes show units.', image: { screen: 'home' } }, { text: 'Small note.' }] }));
  writeFileSync(join(root, 'web/public/whatsnew/rc64/charts-1.jpg'), 'x');
  const r = compile(opts(root, { consume: true }));
  assert.equal(r.changed, true);
  const file = readNotes(root);
  assert.deepEqual(file.releases.map((x) => x.version), ['0.1.0-rc.64', '0.1.0-rc.63']);
  assert.equal(file.releases[0].highlights[0].image, '/whatsnew/rc64/charts-1.jpg');
  assert.deepEqual(readdirSync(join(root, 'docs/whatsnew/fragments')), []);
  const text = readFileSync(join(root, 'web/public/releases/notes.json'), 'utf8');
  assert.ok(text.endsWith('}\n'));
  const again = compile(opts(root, { consume: true }));
  assert.equal(again.changed, false);
  assert.equal(readFileSync(join(root, 'web/public/releases/notes.json'), 'utf8'), text);
});

test('compile: a missing picture, a bad fragment and a dry run', () => {
  const root = make('bad');
  writeFileSync(join(root, 'docs/whatsnew/fragments/charts.json'), JSON.stringify({ schema: 1, highlights: [{ text: 'Axes.', image: { screen: 'home' } }] }));
  assert.throws(() => compile(opts(root)), /does not exist in web\/public/);
  const before = readFileSync(join(root, 'web/public/releases/notes.json'), 'utf8');
  assert.equal(compile(opts(root, { checkFiles: false, dry: true })).changed, true);
  assert.equal(readFileSync(join(root, 'web/public/releases/notes.json'), 'utf8'), before, 'a dry run writes nothing');
  assert.ok(existsSync(join(root, 'docs/whatsnew/fragments/charts.json')));
  writeFileSync(join(root, 'docs/whatsnew/fragments/Bad Name.json'), '{}');
  writeFileSync(join(root, 'docs/whatsnew/fragments/broken.json'), '{nope');
  writeFileSync(join(root, 'docs/whatsnew/fragments/ghost.json'), JSON.stringify({ schema: 1, highlights: [{ text: 'x', image: { screen: 'missing' } }] }));
  assert.throws(() => compile(opts(root)), (e) => /Bad Name/.test(e.message) && /not valid JSON/.test(e.message) && /no screen "missing"/.test(e.message));
});

test('compile: a release with no fragments still gets an honest entry', () => {
  const root = make('quiet');
  const r = compile(opts(root));
  assert.equal(r.changed, true);
  assert.equal(readNotes(root).releases[0].headline, 'Improvements and fixes behind the scenes');
});

test('the notes file in the app and the screens list still match what the tools expect', () => {
  const notes = JSON.parse(readFileSync(join(repoRoot, 'web/public/releases/notes.json'), 'utf8'));
  assert.equal(notes.schema, 1);
  for (const r of notes.releases) for (const h of r.highlights) for (const role of ['image', 'before']) if (h[role]) {
    assert.ok(isReusePath(h[role]), r.version + ' ' + h[role]);
    assert.ok(existsSync(join(repoRoot, 'web/public', h[role])), 'picture exists: ' + h[role]);
  }
  const screens = JSON.parse(readFileSync(join(repoRoot, 'tools/screens/screens.json'), 'utf8')).screens;
  assert.ok(Array.isArray(screens) && screens.every((s) => typeof s.id === 'string'));
  assert.deepEqual(loadFragments(join(repoRoot, 'docs/whatsnew/fragments'), new Set(screens.map((s) => s.id))).map((f) => f.name).filter((x) => x === '__none__'), []);
});

test('the capture tool knows exactly the step kinds that tools/screens does', () => {
  const spec = readFileSync(join(repoRoot, 'tools/screens/capture.spec.ts'), 'utf8');
  const mine = readFileSync(join(repoRoot, 'tools/whatsnew/capture.mjs'), 'utf8');
  for (const key of ['goto', 'click', 'fill', 'select', 'waitFor', 'scrollTo', 'theme', 'aside']) {
    assert.match(spec, new RegExp("'" + key + "' in"), 'capture.spec.ts handles ' + key);
    assert.match(mine, new RegExp("'" + key + "' in"), 'capture.mjs handles ' + key);
  }
});

rmSync(temp, { recursive: true, force: true });
console.log(n + ' whatsnew tests passed');
