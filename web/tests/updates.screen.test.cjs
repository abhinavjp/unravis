const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The Updates page and the What's new window: the notes file, the version order, the virtual list maths, the page states and the words.

const notes = loadTypeScript('src/lib/updateNotes.ts');
const virt = loadTypeScript('src/lib/virtualList.ts');
const page = loadTypeScript('src/lib/updatesPage.ts');
const copy = loadTypeScript('src/lib/copy/updatesCopy.ts');
const root = path.resolve(__dirname, '..');
const bundled = JSON.parse(fs.readFileSync(path.join(root, 'public/releases/notes.json'), 'utf8'));

const rel = (version, extra = {}) => ({ version, date: '2026-11-01', headline: 'h', highlights: [{ text: 'a' }], changes: ['x'], ...extra });

test('versions compare as numbers, ignore a leading v, and put a final release after its candidates', () => {
  assert.ok(notes.compareVersions('0.1.0-rc.9', '0.1.0-rc.10') < 0);
  assert.equal(notes.compareVersions('v0.1.0-rc.5', '0.1.0-rc.5'), 0);
  assert.ok(notes.compareVersions('0.1.0', '0.1.0-rc.99') > 0);
  assert.equal(notes.compareVersions('dev', '0.1.0'), 0);
  assert.equal(notes.shortVersion('v0.1.0-rc.62'), 'rc.62');
  assert.equal(notes.shortVersion('1.2.3'), '1.2.3');
  assert.equal(notes.parseableVersion('dev'), false);
});

test('picture paths: dot-dot segments, encoded dots and odd characters are refused, plain names with two dots are kept', () => {
  const out = notes.parseNotes({ schema: 1, releases: [rel('0.1.0-rc.5', { highlights: [
    { text: 'a', image: '/whatsnew/rc5/a..b.png' }, { text: 'b', image: '/whatsnew/rc5/..' }, { text: 'c', image: '/whatsnew/%2e%2e/x.png' }, { text: 'd', image: '/whatsnew/rc5/../x.png' }
  ] })] });
  const kinds = out[0].highlights.map((h) => notes.highlightKind(h));
  assert.deepEqual(kinds, ['screenshot', 'note', 'note', 'note']);
});

test('the notes file is read defensively: bad entries, other-site pictures and paths outside /whatsnew/ are dropped', () => {
  assert.deepEqual(notes.parseNotes(null), []);
  assert.deepEqual(notes.parseNotes({ schema: 2, releases: [] }), []);
  const out = notes.parseNotes({ schema: 1, releases: [
    rel('0.1.0-rc.5', { highlights: [{ text: 'ok', image: '/whatsnew/rc5/a.png', before: '/whatsnew/rc5/b.png' }, { text: 'bad', image: 'https://evil.example/x.png' }, { text: 'up', image: '/whatsnew/../secret.png' }, { text: '' }, 7] }),
    rel('v0.1.0-rc.7'), rel('0.1.0-rc.7'), { version: 'nope' }, null
  ] });
  assert.deepEqual(out.map((r) => r.version), ['0.1.0-rc.7', '0.1.0-rc.5']);
  const h = out[1].highlights;
  assert.equal(h.length, 3);
  assert.equal(notes.highlightKind(h[0]), 'before-after');
  assert.equal(notes.highlightKind(h[1]), 'note');
  assert.equal(h[1].image, undefined);
  assert.equal(h[2].image, undefined);
});

test('the tour covers what changed since the version last read, newest first, and only the installed one on a first run', () => {
  const all = notes.parseNotes({ schema: 1, releases: ['60', '61', '62', '63', '64'].map((n) => rel('0.1.0-rc.' + n)) });
  assert.deepEqual(notes.versionsSince(all, '0.1.0-rc.60', '0.1.0-rc.63').map((r) => notes.shortVersion(r.version)), ['rc.63', 'rc.62', 'rc.61']);
  assert.deepEqual(notes.versionsSince(all, '', '0.1.0-rc.63').map((r) => r.version), ['0.1.0-rc.63']);
  assert.deepEqual(notes.versionsSince(all, '0.1.0-rc.63', '0.1.0-rc.63').map((r) => r.version), ['0.1.0-rc.63']);
  assert.equal(notes.versionsSince(all, '0.1.0-rc.60', '0.1.0-rc.64', 2).length, 2);
  assert.equal(notes.shouldAutoOpen('0.1.0-rc.60', '0.1.0-rc.63'), true);
  assert.equal(notes.shouldAutoOpen('', '0.1.0-rc.63'), false);
  assert.equal(notes.shouldAutoOpen('0.1.0-rc.63', '0.1.0-rc.63'), false);
  assert.equal(notes.shouldAutoOpen('0.1.0-rc.60', 'dev'), false);
});

test('slides: one per picture, then one text slide per version for the plain notes', () => {
  const slides = notes.buildSlides([
    rel('0.1.0-rc.2', { highlights: [{ text: 'p', image: '/whatsnew/a.png' }, { text: 'q' }, { text: 'r', image: '/whatsnew/c.png', before: '/whatsnew/b.png' }] }),
    rel('0.1.0-rc.1', { highlights: [] })
  ]);
  assert.deepEqual(slides.map((s) => s.kind), ['screenshot', 'before-after', 'note', 'note']);
  assert.deepEqual(slides[2].lines, ['q']);
  assert.match(slides[3].text, /behind the scenes/);
});

test('the bundled notes file parses, is newest first and keeps every release', () => {
  const list = notes.parseNotes(bundled);
  assert.equal(list.length, bundled.releases.length);
  for (let i = 1; i < list.length; i++) assert.ok(notes.compareVersions(list[i - 1].version, list[i].version) > 0);
  for (const r of list) assert.ok(r.headline && (r.highlights.length || r.changes.length), r.version + ' has some notes');
});

test('virtual list: offsets, lookup and a window that stays small for a long history', () => {
  const heights = Array.from({ length: 1000 }, () => 100);
  const offs = virt.offsets(heights);
  assert.equal(offs[1000], 100000);
  assert.equal(virt.indexAt(offs, 0), 0);
  assert.equal(virt.indexAt(offs, 250), 2);
  assert.equal(virt.indexAt(offs, 999999), 999);
  const w = virt.windowRange(offs, 50000, 600);
  assert.ok(w.end - w.start < 20 && w.start <= 495 && w.end >= 506);
  assert.deepEqual(virt.windowRange(virt.offsets([]), 0, 600), { start: 0, end: 0 });
  assert.deepEqual(virt.windowRange(offs, 0, 600), { start: 0, end: 12 });
});

test('the page phase follows the update server: checking, up to date, available, ready, installing, failed', () => {
  const r = (status, install, more = {}) => ({ status, current: '0.1.0-rc.62', latest: '0.1.0-rc.63', url: '', install, ...more });
  assert.equal(page.updatePhase(null, false), 'none');
  assert.equal(page.updatePhase(r('latest'), true), 'checking');
  assert.equal(page.updatePhase(r('latest'), false), 'uptodate');
  assert.equal(page.updatePhase(r('dev'), false), 'dev');
  assert.equal(page.updatePhase(r('unreachable'), false), 'error');
  assert.equal(page.updatePhase(r('available'), false), 'available');
  assert.equal(page.updatePhase(r('available', { state: 'downloading' }), false), 'installing');
  assert.equal(page.updatePhase(r('available', { state: 'ready' }), false), 'ready');
  assert.equal(page.updatePhase(r('available', { state: 'ready' }, { applyAuto: true }), false), 'autowait');
  assert.equal(page.updatePhase(r('available', { state: 'failed' }), false), 'failed');
  assert.equal(page.updatePhase(r('latest', { state: 'launched' }), false), 'installing');
  assert.equal(page.installStep(r('available', { state: 'downloading' }), false), 1);
  assert.equal(page.installStep(r('available', { state: 'ready' }), true), 2);
  assert.equal(page.installStep(r('available'), false), 0);
});

test('the chip shows the short version, or what needs attention', () => {
  const r = (status, install) => ({ status, current: '0.1.0-rc.62', latest: '0.1.0-rc.63', url: '', install });
  assert.deepEqual(page.chipState(null), { text: '', pip: 'none' });
  assert.deepEqual(page.chipState(r('latest')), { text: 'rc.62', pip: 'none' });
  assert.equal(page.chipState(r('available')).pip, 'ready');
  assert.equal(page.chipState(r('available', { state: 'downloading' })).pip, 'busy');
  assert.equal(page.chipState({ status: 'dev', current: 'dev', latest: '', url: '' }).text, '');
});

test('next check, dates and last checked read in plain words', () => {
  const now = new Date('2026-11-26T12:00:00Z');
  assert.equal(page.nextCheckNote('2026-11-26T11:50:00Z', 60, now), 'Next check in about 50 minutes.');
  assert.equal(page.nextCheckNote('2026-11-26T08:00:00Z', 60, now), 'Next check is due now.');
  assert.equal(page.nextCheckNote(undefined, 60, now), '');
  assert.equal(page.formatNoteDate('2026-11-26'), '26 Nov 2026');
  assert.equal(page.formatNoteDate('soon'), 'soon');
  assert.equal(page.checkedLabel(undefined), copy.UPD_TEXT.neverChecked);
});

test('the words are plain: no dashes, no jargon, short sentences', () => {
  for (const line of copy.updatesSentences()) {
    assert.ok(line.trim().length > 0);
    assert.doesNotMatch(line, /[\u2013\u2014]/, 'no long dash: ' + line);
    assert.doesNotMatch(line, /\b(OTA|manifest|payload|hash|sha-?256|ed25519)\b/i, 'no jargon: ' + line);
  }
});

test('the app wires the page: the version chip, the More menu, the lazy panels and the start check', () => {
  const read = (f) => fs.readFileSync(path.join(root, f), 'utf8').replace(/\r\n/g, '\n');
  const header = read('src/components/AppHeader.svelte');
  const app = read('src/App.svelte');
  assert.match(header, /data-testid="version-chip"/);
  assert.match(header, /updatesEntry, whatsNewEntry/);
  assert.match(app, /panels\.sidePanel === 'updates'/);
  assert.match(app, /checkWhatsNewOnStart/);
  assert.match(app, /whatsNew\.open/);
});
