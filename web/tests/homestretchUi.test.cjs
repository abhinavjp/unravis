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
