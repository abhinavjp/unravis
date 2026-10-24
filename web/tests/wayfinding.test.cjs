const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function load(relativePath) {
  const source = fs.readFileSync(path.join(root, relativePath), 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', output)(require, module, module.exports);
  return module.exports;
}

const { collapseCrumbs, nextMenuIndex, parseViewHash, formatViewHash, sameLevel, createViewHistory } = load('src/lib/wayfinding.ts');

const crumb = (id) => ({ id, label: id });
const TOKEN = 'link-session-token-0123456789abcdef+/=';

test('collapseCrumbs keeps short trails whole', () => {
  for (const ids of [[], ['a'], ['a', 'b'], ['a', 'b', 'c']]) {
    const trail = ids.map(crumb);
    const result = collapseCrumbs(trail);
    assert.deepEqual(result.visible, trail);
    assert.deepEqual(result.hidden, []);
    assert.equal(result.gapAfter, -1);
  }
});

test('collapseCrumbs hides the middle and keeps first and last', () => {
  const trail = ['a', 'b', 'c', 'd'].map(crumb);
  const result = collapseCrumbs(trail);
  assert.deepEqual(result.visible.map((c) => c.id), ['a', 'd']);
  assert.deepEqual(result.hidden.map((c) => c.id), ['b', 'c']);
  assert.equal(result.gapAfter, 0);
  const wide = collapseCrumbs(['a', 'b', 'c', 'd', 'e'].map(crumb), 2);
  assert.deepEqual(wide.visible.map((c) => c.id), ['a', 'd', 'e']);
  assert.deepEqual(wide.hidden.map((c) => c.id), ['b', 'c']);
});

test('parseViewHash treats anything else as the first screen', () => {
  for (const hash of ['', '#', '#t=abc', '#/nonsense', '#/op?lens=p95', '#/record?c=A&a=b', '#section']) {
    assert.equal(parseViewHash(hash), null, hash);
  }
});

test('views round trip through the address', () => {
  const views = [
    { type: 'where', lens: 'p95' },
    { type: 'where', lens: 'requests', compare: true },
    { type: 'all', lens: 'client', tab: 'failures' },
    { type: 'op', lens: 'server', controller: 'Orders', action: 'Get /x?y=1&z', opKind: 'route', tab: 'slowest' },
    { type: 'op', controller: 'Orders', action: 'List' },
    { type: 'record', lens: 'completion', controller: 'Orders', action: 'List', sourceId: 'src one', recordId: 'a/b#c', tab: 'incomplete' }
  ];
  for (const view of views) {
    assert.deepEqual(parseViewHash(formatViewHash(view)), view, JSON.stringify(view));
  }
});

test('parseViewHash rejects values it does not recognise', () => {
  assert.equal(parseViewHash('#/where?lens=bogus').lens, 'requests');
  assert.equal(parseViewHash('#/all?tab=<script>').tab, undefined);
  assert.equal(parseViewHash('#/all?tab=' + 'a'.repeat(40)).tab, undefined);
  assert.equal(parseViewHash('#/op?c=' + 'x'.repeat(400) + '&a=y'), null);
});

test('writing a view keeps the session token and a null view keeps only the token', () => {
  const withToken = '#t=' + encodeURIComponent(TOKEN);
  const written = formatViewHash({ type: 'where', lens: 'p95' }, withToken);
  assert.ok(written.startsWith('#/where?'));
  assert.equal(new URLSearchParams(written.slice(written.indexOf('?') + 1)).get('t'), TOKEN);
  assert.deepEqual(parseViewHash(written), { type: 'where', lens: 'p95' });
  assert.equal(formatViewHash(null, written), withToken);
  assert.equal(formatViewHash(null, withToken), withToken);
  assert.equal(formatViewHash(null, ''), '');
  assert.equal(formatViewHash(null, '#/where?lens=p95'), '');
  assert.ok(!formatViewHash({ type: 'all' }, '').includes('t='));
});

test('sameLevel ignores tab, lens figure and compare, not the place', () => {
  const op = { type: 'op', lens: 'p95', controller: 'C', action: 'A', tab: 'failures' };
  assert.ok(sameLevel(op, { ...op, tab: 'slowest', lens: 'client' }));
  assert.ok(!sameLevel(op, { ...op, action: 'B' }));
  assert.ok(!sameLevel(op, { ...op, type: 'record', sourceId: 's', recordId: 'r' }));
  assert.ok(sameLevel({ type: 'where', lens: 'p95' }, { type: 'where', lens: 'server', compare: true }));
  assert.ok(sameLevel(null, null));
  assert.ok(!sameLevel(null, { type: 'where', lens: 'p95' }));
});

// A small stand-in for the browser's location and history.
function fakeBrowser(hash = '') {
  const entries = [{ hash, state: null }];
  let at = 0;
  const loc = { pathname: '/', search: '', get hash() { return entries[at].hash; } };
  const calls = [];
  const hist = {
    get state() { return entries[at].state; },
    pushState(state, _t, url) { entries.length = at + 1; entries.push({ hash: url.slice(url.indexOf('#')), state }); at++; calls.push('push'); },
    replaceState(state, _t, url) { entries[at] = { hash: url.includes('#') ? url.slice(url.indexOf('#')) : '', state }; calls.push('replace'); },
    back() { calls.push('back'); }
  };
  return { loc, hist, calls, entries, get at() { return at; }, pop(to) { at = to; } };
}

const where = { type: 'where', lens: 'requests' };
const op = { type: 'op', lens: 'requests', controller: 'C', action: 'A' };

test('going deeper pushes steps and same-level changes replace', () => {
  const b = fakeBrowser();
  const h = createViewHistory(b.loc, b.hist);
  assert.equal(h.sync(null), 'none');
  assert.equal(h.sync(where), 'push');
  assert.equal(h.sync({ ...where, lens: 'p95' }), 'replace');
  assert.equal(h.sync(op), 'push');
  assert.equal(h.sync({ ...op, tab: 'slowest' }), 'replace');
  assert.equal(b.entries.length, 3);
  assert.deepEqual(b.calls, ['push', 'replace', 'push', 'replace']);
  assert.equal(b.entries[0].hash, '');
});

test('stepping up goes back one step when the entry before is the target', () => {
  const b = fakeBrowser();
  const h = createViewHistory(b.loc, b.hist);
  h.sync({ type: 'where', lens: 'p95' });
  h.sync({ type: 'op', lens: 'p95', controller: 'C', action: 'A' });
  assert.equal(h.sync({ type: 'where', lens: 'p95' }), 'back');
  assert.equal(b.calls.at(-1), 'back');
  b.pop(1);
  assert.equal(h.pop(), undefined);
  assert.deepEqual(h.view, { type: 'where', lens: 'p95' });
});

test('stepping up pushes when the entry before is not the target', () => {
  const b = fakeBrowser();
  const h = createViewHistory(b.loc, b.hist);
  h.sync(op);
  assert.equal(h.sync(where), 'push');
});

test('the browser Back and Forward buttons are reported as views to show', () => {
  const b = fakeBrowser();
  const h = createViewHistory(b.loc, b.hist);
  h.sync(where);
  h.sync(op);
  b.pop(0);
  assert.equal(h.pop(), null);
  b.pop(1);
  assert.deepEqual(h.pop(), where);
  b.pop(2);
  assert.deepEqual(h.pop(), op);
});

test('a page opened on a view starts from that view and keeps the token while syncing', () => {
  const start = formatViewHash({ type: 'op', lens: 'p95', controller: 'C', action: 'A' }, '#t=' + encodeURIComponent(TOKEN));
  const b = fakeBrowser(start);
  const h = createViewHistory(b.loc, b.hist);
  assert.deepEqual(h.view, { type: 'op', lens: 'p95', controller: 'C', action: 'A' });
  assert.equal(h.sync({ type: 'op', lens: 'p95', controller: 'C', action: 'A' }), 'none');
  assert.equal(h.sync({ type: 'op', lens: 'p95', controller: 'C', action: 'A', tab: 'slowest' }), 'replace');
  assert.ok(b.loc.hash.includes('t=' + encodeURIComponent(TOKEN)));
  assert.equal(h.sync({ type: 'where', lens: 'p95' }), 'push');
  assert.ok(b.loc.hash.includes('t=' + encodeURIComponent(TOKEN)));
});

test('a record in the all-operations list needs no operation names, but one name alone is refused', () => {
  const view = { type: 'record', lens: 'p95', sourceId: 's', recordId: 'r', tab: 'slowest' };
  assert.deepEqual(parseViewHash(formatViewHash(view)), view);
  assert.equal(parseViewHash('#/record?c=A&s=s&id=r'), null);
  assert.equal(parseViewHash('#/record?c=A&a=B&s=s'), null);
});

test('nextMenuIndex moves with arrows, wraps and ignores other keys', () => {
  assert.equal(nextMenuIndex('ArrowDown', -1, 3), 0);
  assert.equal(nextMenuIndex('ArrowDown', 2, 3), 0);
  assert.equal(nextMenuIndex('ArrowUp', 0, 3), 2);
  assert.equal(nextMenuIndex('ArrowUp', -1, 3), 2);
  assert.equal(nextMenuIndex('Home', 2, 3), 0);
  assert.equal(nextMenuIndex('End', 0, 3), 2);
  assert.equal(nextMenuIndex('a', 0, 3), null);
  assert.equal(nextMenuIndex('ArrowDown', 0, 0), null);
});
