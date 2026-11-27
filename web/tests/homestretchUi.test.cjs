const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Homestretch, the Releases app page (cmd/unravis-releases/ui). It is plain JavaScript with no build step and is served with a
// strict Content-Security-Policy (no inline script, no style="" attribute, no data: image), so these tests check the rules the
// page must keep and load the small helper file into a sandbox to check its words and times.

const repo = path.resolve(__dirname, '..', '..');
const dir = path.join(repo, 'cmd/unravis-releases/ui');
const read = (f) => fs.readFileSync(path.join(dir, f), 'utf8').replace(/\r\n/g, '\n');
const html = read('index.html');

test('index.html names Homestretch and every file it loads exists', () => {
  assert.match(html, /<title>Homestretch<\/title>/);
  const refs = [...html.matchAll(/(?:src|href)="([^"#]+)"/g)].map((m) => m[1]).filter((u) => !/^https?:/.test(u));
  assert.ok(refs.length >= 8, 'few files referenced');
  for (const f of refs) assert.ok(fs.existsSync(path.join(dir, f)), f);
});

test('csp: no inline script, no style attribute, no data: address anywhere in the page files', () => {
  assert.doesNotMatch(html, /<script(?![^>]*\bsrc=)[^>]*>/);
  assert.doesNotMatch(html, /\son[a-z]+=/);
  for (const f of fs.readdirSync(dir).filter((n) => /\.(js|css|html)$/.test(n))) {
    const t = read(f).split(String.fromCharCode(10)).filter((l) => !/^\s*\/\//.test(l)).join(String.fromCharCode(10));
    assert.doesNotMatch(t, /\sstyle="/, f + ' writes a style attribute');
    assert.doesNotMatch(t, /\sstyle='/, f + ' writes a style attribute');
    assert.doesNotMatch(t, /url\(\s*['"]?data:/, f + ' uses a data: address');
  }
});

test('every icon the page draws exists in the sprite', () => {
  const have = new Set([...html.matchAll(/<symbol id="i-([\w-]+)"/g)].map((m) => m[1]));
  const used = new Set();
  for (const f of ['app.js', 'util.js', 'appearance.js', 'detail.js']) {
    for (const m of read(f).matchAll(/\bico\('([\w-]+)'/g)) used.add(m[1]);
  }
  for (const s of ['passed', 'failed', 'running', 'waiting', 'stopped', 'skipped', 'unknown']) used.add(s);
  const missing = [...used].filter((n) => !have.has(n));
  assert.deepEqual(missing, []);
});

test('page files are valid JavaScript', () => {
  for (const f of fs.readdirSync(dir).filter((n) => n.endsWith('.js'))) {
    assert.doesNotThrow(() => new vm.Script(read(f), { filename: f }), f);
  }
});

test('the release status words: withdrawn, stopped at the failed step, verify failed', () => {
  const sb = { document: { getElementById: () => null, createElement: () => ({}) } };
  vm.createContext(sb);
  vm.runInContext(read('legacy-core.js') + '\n;globalThis.__t = LegacyCore;', sb, { filename: 'legacy-core.js' });
  const c = sb.__t;
  assert.deepEqual(Array.from(c.status({ withdrawn: true })), ['Withdrawn', 'warn']);
  assert.deepEqual(Array.from(c.status({ verify: 'failed' })), ['Verify failed', 'bad']);
  assert.deepEqual(Array.from(c.status({ steps: [{ name: 'Sign', state: 'failed' }] })), ['Stopped at Sign', 'bad']);
  assert.deepEqual(Array.from(c.status({ verify: 'passed', onFeed: true })), ['Live, verified', 'ok']);
  assert.equal(c.STEPS.length, 7);
  assert.equal(c.link('https://evil.example/x', 't'), null, 'only the project\'s own GitHub links are made clickable');
});

test('util: text is escaped, durations and ages read in plain words', () => {
  const sb = { document: { querySelector: () => null, querySelectorAll: () => [] }, Date, Math, isFinite, String, Array };
  vm.createContext(sb);
  vm.runInContext(read('util.js') + '\n;globalThis.__t = { esc, dur, rel, norm, subject, short, wfName, md };', sb, { filename: 'util.js' });
  const t = sb.__t;
  assert.equal(t.esc('<a href="x">&\'</a>'), '&lt;a href=&quot;x&quot;&gt;&amp;&#39;&lt;/a&gt;');
  assert.equal(t.dur(null), '–');
  assert.equal(t.dur(42000), '42 s');
  assert.equal(t.dur(125000), '2 min 5 s');
  assert.equal(t.dur(3 * 3600e3 + 120e3), '3 h 2 min');
  assert.equal(t.norm('not-recorded'), 'unknown');
  assert.equal(t.rel(Date.now() - 5000), 'just now');
  assert.equal(t.rel(Date.now() - 2 * 86400e3), '2 days ago');
  assert.equal(t.rel(''), 'not recorded');
  assert.equal(t.subject({ pr: 12 }), 'PR 12');
  assert.equal(t.subject({ branch: 'develop' }), 'Push to develop');
  assert.equal(t.wfName('fast-gate.yml'), 'Fast gate');
  assert.equal(t.short('0123456789abcdef'), '0123456');
  assert.doesNotMatch(t.md('# <b>x</b>\n- **y** `<i>`'), /<b>x|<i>/, 'markdown from a run summary is escaped first');
});

test('the theme system: every attribute the page sets has a rule layer and a saved default', () => {
  const css = read('tokens.css') + read('appearance.css') + read('components.css');
  assert.match(css, /@layer reset, tokens, style, palette, font, density, overrides, components/);
  for (const a of ['data-style', 'data-palette', 'data-font', 'data-density', 'data-mode']) assert.match(html, new RegExp(a + '="'), a);
  assert.match(read('appearance-boot.js'), /rm-appearance/);
});

test('the machine, the run and the log views read only the program routes, never write', () => {
  const js = read('data.js') + read('app.js');
  // The one write the page makes is Data.act(): a POST to /api/action/<id>. Everything else is read-only.
  const writes = js.match(/method:\s*['"](POST|PUT|PATCH|DELETE)['"]/gi) || [];
  assert.deepEqual(writes, ["method: 'POST'"], 'exactly one write call');
  assert.match(js, /fetch\('\/api\/action\/' \+ encodeURIComponent\(id\),\s*\{\s*method: 'POST'/, 'the write is the action route');
  for (const route of ['/api/projects', '/api/overview', '/api/runs', '/api/run', '/api/run/log', '/api/machines', '/api/snapshot']) assert.ok(js.includes(route), route);
});

// W-05: the page says when its data is old. util.js and data.js run in a sandbox with a fake clock and a fake fetch, so the
// real Data.tick / loadSlow / loadFast and the real chip and note words are the code under test.
function dataSandbox() {
  const clock = { t: Date.parse('2026-11-27T14:00:00') };
  class FakeDate extends Date { static now() { return clock.t; } }
  const net = { snapshot: 'ok', fast: 'ok' };
  const json = (body, status = 200) => ({ ok: status < 400, status, json: async () => body });
  const fetch = async (url) => {
    const p = String(url).split('?')[0];
    if (p === '/api/projects') return json({ projects: [{ id: 'u', name: 'Unravis', ciFound: true }] });
    if (p === '/api/snapshot') { if (net.snapshot === 'down') throw new TypeError('Failed to fetch'); return json({ at: 'x', releases: [{ version: 'rc.1' }], feeds: [] }); }
    if (net.fast === 'down') throw new TypeError('Failed to fetch');
    if (p === '/api/overview') return json({ now: 1, branches: [] });
    if (p === '/api/runs') return json({ runs: [], total: 0 });
    return json({ machines: [], updated: 0 });
  };
  const sb = { document: { querySelector: () => null, querySelectorAll: () => [], visibilityState: 'visible', addEventListener() {} }, Date: FakeDate, Math, isFinite, String, Array, Set, JSON, Object, Promise, Error, TypeError, URLSearchParams,
    fetch, setInterval: () => 0, addEventListener() {}, history: {}, sessionStorage: {}, localStorage: { getItem: () => '', setItem() {} }, location: { hash: '', pathname: '/', search: '' } };
  vm.createContext(sb);
  vm.runInContext(read('util.js') + '\n' + read('data.js') + '\n;globalThis.__t = { D, Data, Fresh, POLL, plainReason };', sb, { filename: 'data.js' });
  const kinds = [];
  sb.__t.Data.start((k) => kinds.push(k));
  return { ...sb.__t, clock, net, kinds };
}
const settle = () => new Promise((r) => setTimeout(r, 5));
const tick = async (h, slow) => { await h.Data.tick(slow); await settle(); };

test('W-05 loadSlow: a failed releases read keeps the old snapshot and its read time, and says why in plain words', async () => {
  const h = dataSandbox(); await settle();
  const first = h.D.snapshotAt; assert.ok(first > 0 && h.D.snapshot && h.D.snapshot.releases.length === 1);
  h.clock.t += 31000; h.net.snapshot = 'down'; await tick(h, true);
  assert.ok(h.D.snapshot, 'the old snapshot stays');
  assert.equal(h.D.snapshotAt, first, 'the read time does not move on a failed read');
  assert.match(h.D.snapshotErr, /could not reach the unravis-releases program on this PC/);
  assert.doesNotMatch(h.D.snapshotErr, /Failed to fetch/);
  h.clock.t += 31000; h.net.snapshot = 'ok'; await tick(h, true);
  assert.equal(h.D.snapshotErr, ''); assert.ok(h.D.snapshotAt > first, 'a good read moves the time and clears the error');
});

test('W-05 loadFast: a failed runs read keeps the read time; a failure that starts or ends redraws once, a repeat does not', async () => {
  const h = dataSandbox(); await settle();
  const at = h.D.at; assert.ok(at > 0);
  h.kinds.length = 0; h.clock.t += 4000; h.net.fast = 'down'; await tick(h, false);
  assert.equal(h.D.at, at); assert.match(h.D.err, /could not reach/); assert.deepEqual(Array.from(h.kinds), ['error']);
  h.kinds.length = 0; h.clock.t += 4000; await tick(h, false);
  assert.deepEqual(Array.from(h.kinds), ['tick'], 'the same failure again only repaints the header');
  h.kinds.length = 0; h.net.fast = 'ok'; await tick(h, false);
  assert.equal(h.D.err, ''); assert.deepEqual(Array.from(h.kinds), ['data'], 'recovery redraws so the note goes away');
});

test('W-05 words of a failed read: the server answer is kept, a request that never arrived is explained', () => {
  const h = dataSandbox();
  assert.equal(h.plainReason(Object.assign(new Error('The server answered 502.'), { status: 502 })), 'The server answered 502.');
  assert.doesNotMatch(h.plainReason(new TypeError('Failed to fetch')), /fetch/i);
});

test('W-05 note: names the time of the old read and the reason; Releases shows both reads, other pages only runs and machines', async () => {
  const h = dataSandbox(); await settle();
  assert.equal(h.Fresh.noteHtml('releases', h.clock.t), '', 'nothing failed: no note');
  const at = h.Fresh.clock(h.D.snapshotAt);
  h.clock.t += 61000; h.net.snapshot = 'down'; await tick(h, true);
  const rel = h.Fresh.noteHtml('releases', h.clock.t);
  assert.ok(rel.includes(`<b>Showing releases as read at ${at}.</b>`), rel);
  assert.match(rel, /Could not refresh: Homestretch could not reach the unravis-releases program on this PC\. It may have been closed\./);
  assert.match(rel, /data-check/, 'a Check again button');
  assert.equal(h.Fresh.noteHtml('overview', h.clock.t), '', 'the releases read is not a problem of the Overview page');
  h.net.fast = 'down'; await tick(h, true);
  const both = h.Fresh.noteHtml('releases', h.clock.t);
  assert.match(both, /Showing releases as read at .+ and runs and machines as read at .+\./);
  assert.equal((both.match(/class="stale-note"/g) || []).length, 1, 'one merged note, not two');
  assert.match(h.Fresh.noteHtml('machines', h.clock.t), /Showing runs and machines as read at/);
});

test('W-05 chip: shows the older read time and warns after 3 polls of either read (12 s runs, 90 s releases)', async () => {
  const h = dataSandbox(); await settle();
  assert.equal(h.POLL.warnAfter * h.POLL.fast, 12000); assert.equal(h.POLL.warnAfter * h.POLL.slow, 90000);
  const t0 = h.clock.t;
  assert.equal(h.Fresh.chip(t0 + 5000).stale, false);
  assert.match(h.Fresh.chip(t0 + 5000).text, /^Updated /);
  assert.equal(h.Fresh.chip(t0 + 12000).stale, false, 'exactly 3 poll periods is still fine');
  assert.equal(h.Fresh.chip(t0 + 13000).stale, true, 'runs older than 3 fast polls');
  // the runs read is fresh again at t0+80 s, but the releases read is 80 s old: still quiet; at 91 s it warns
  h.D.at = t0 + 80000;
  assert.equal(h.Fresh.chip(t0 + 81000).stale, false);
  const old = h.Fresh.chip(t0 + 91000);
  assert.equal(old.stale, true); assert.equal(old.text, 'Data is 2 min old', 'the age of the older read (releases)');
  assert.match(old.title, /Releases read at .+\. Runs and machines read at .+\./);
  assert.equal(h.Fresh.chip(t0 + 200000).text, 'Data is 3 min old');
});

test('W-05 render: the page draws the note under the Releases heading, the chip with its warning style, and re-reads on Check again', () => {
  const app = read('app.js');
  assert.match(app, /Fresh\.noteHtml\('releases', Date\.now\(\)\)/);
  assert.match(app, /Fresh\.chip\(Date\.now\(\)\)/);
  assert.match(app, /\$\$\('\[data-check\]', main\)\.forEach/);
  assert.match(read('components.css'), /\.stale-note\{/);
  assert.match(read('components.css'), /\.fresh\.stale\{color:var\(--warn\)/);
});
