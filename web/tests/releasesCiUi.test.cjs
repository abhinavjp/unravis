const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// The Overview and Runs tabs of the Releases app (cmd/unravis-releases/ui/ci.js). The page is plain JavaScript with no build
// step, so these tests load it into a sandbox with a stub page and check the logic that has no DOM: routes, the address of
// a page, the query sent to the program, the wording of a run, and the rules the page keeps (no HTML strings, no
// hard coded channel or step names in the files that draw the page).

const repo = path.resolve(__dirname, '..', '..');
const ui = (f) => fs.readFileSync(path.join(repo, 'cmd/unravis-releases/ui', f), 'utf8').replace(/\r\n/g, '\n');

function load(hash) {
  const stub = () => new Proxy(function () {}, { get: (t, k) => (k === 'then' ? undefined : k === 'classList' ? { add() {}, remove() {}, toggle() {} } : k === 'dataset' ? {} : () => stub()), set: () => true });
  const fakeEl = { hidden: false, value: '', dataset: {}, classList: { add() {}, remove() {}, toggle() {} }, addEventListener() {}, append() {}, replaceChildren() {}, setAttribute() {}, removeAttribute() {}, querySelector: () => null, remove() {} };
  const sandbox = {
    console, URLSearchParams, Date, JSON, Math, Set, Map, Promise, encodeURIComponent, decodeURIComponent, setTimeout, clearTimeout,
    location: { hash: hash || '' }, history: { replaceState() {} },
    document: { hidden: false, addEventListener() {}, querySelector: () => fakeEl, getElementById: () => fakeEl },
    window: { addEventListener() {} },
    $: () => fakeEl,
    el: (tag, cls, text) => ({ tag, cls, text }),
    when: (s) => String(s), ago: (s) => 'ago:' + s, dur: (n) => n + 's', clock: (s) => String(s),
    getJSON: () => Promise.reject(new Error('no program')),
    viewer() {}, mountViewer() {},
    STEPS: ['A', 'B'], CHANNELS: ['rc', 'stable'], snap: null, section: 'releases', NEXT: 'next', ui: {}, selected: '', render() {},
    stub,
  };
  vm.createContext(sandbox);
  // let and const at the top of a script are not properties of the sandbox, so the checks run inside it.
  vm.runInContext(ui('ci.js') + '\n;globalThis.__t = { parseHash, hashFor, apiUrl, runTitle, ciLogUrl, logKey, took, ci, first, runWhen, CI_STATUS, CI_STATUS_FILTER, plainErr };', sandbox, { filename: 'ci.js' });
  return sandbox.__t;
}

test('hash routes: the page, the id and the filters are read from the address', () => {
  const t = load();
  const r = t.parseHash.call(null);
  assert.equal(r.tab, '');
  const sb = load('#/runs/20251101-120000-ab12?status=failed&project=x');
  assert.equal(sb.parseHash().tab, 'runs');
  assert.equal(sb.parseHash().id, '20251101-120000-ab12');
  assert.equal(sb.parseHash().q.get('status'), 'failed');
  assert.equal(sb.parseHash().q.get('project'), 'x');
  assert.equal(load('#/overview').parseHash().tab, 'overview');
  assert.equal(load('#/releases/v1.2.3').parseHash().id, 'v1.2.3');
});

test('hash routes: a broken escape in the address does not throw', () => {
  const r = load('#/runs/%E0%A4%A').parseHash();
  assert.equal(r.tab, 'runs');
  assert.equal(typeof r.id, 'string');
});

test('addresses: the first project is the default and is left out; empty filters are left out', () => {
  const t = load();
  t.ci.projects = [{ id: 'a' }, { id: 'b' }]; t.ci.pid = 'a';
  assert.equal(t.hashFor('runs'), '#/runs');
  assert.equal(t.hashFor('runs', '', { status: 'failed', branch: '' }), '#/runs?status=failed');
  assert.equal(t.hashFor('runs', 'r1', {}, 'b'), '#/runs/r1?project=b');
  assert.equal(t.hashFor('runs', 'a/b c'), '#/runs/a%2Fb%20c');
});

test('program calls: the project is always sent, empty filters are not', () => {
  const t = load();
  t.ci.projects = [{ id: 'a' }]; t.ci.pid = 'a';
  assert.equal(t.apiUrl('/api/runs', { status: 'failed', pr: '', limit: 30 }), '/api/runs?project=a&status=failed&limit=30');
});

test('log address: the viewer key carries the project, run, job and step, and survives odd characters', () => {
  const t = load();
  t.ci.projects = [{ id: 'a' }]; t.ci.pid = 'a';
  const key = t.logKey('run 1', 'job-x', 3);
  assert.ok(key.startsWith('ci:'));
  const u = new URL('http://x' + t.ciLogUrl(key, 40, 5000));
  assert.equal(u.pathname, '/api/run/log');
  assert.equal(u.searchParams.get('project'), 'a');
  assert.equal(u.searchParams.get('id'), 'run 1');
  assert.equal(u.searchParams.get('job'), 'job-x');
  assert.equal(u.searchParams.get('step'), '3');
  assert.equal(u.searchParams.get('from'), '40');
  assert.equal(u.searchParams.get('n'), '5000');
});

test('run wording: a pull request, a push and a plain run read in plain words', () => {
  const t = load();
  assert.equal(t.runTitle({ event: 'pull_request', pr: 12, branch: 'feat', base: 'master' }), 'Pull request #12 · feat into master');
  assert.equal(t.runTitle({ event: 'push', branch: 'master' }), 'Push to master');
  assert.equal(t.runTitle({ workflow: 'CI' }), 'CI');
});

test('status words: all seven filter words exist, and every status has a sign, a colour family and an explanation', () => {
  const t = load();
  assert.deepEqual(Array.from(t.CI_STATUS_FILTER, (x) => x[1]), ['Any status', 'Passed', 'Failed', 'Running', 'Waiting', 'Stopped', 'Not recorded here']);
  for (const k of Object.keys(t.CI_STATUS)) { const [g, c, tip] = t.CI_STATUS[k]; assert.ok(g && c && tip.length > 10, k); }
});

test('the files that draw the page: no HTML strings, no hard coded release names or step counts', () => {
  const ci = ui('ci.js');
  assert.ok(!/\.innerHTML|insertAdjacentHTML|outerHTML|document\.write/.test(ci), 'ci.js builds the page with textContent only');
  const app = ui('app.js') + ui('detail.js');
  assert.ok(!/\\d\\\/7/.test(app), 'the step header pattern no longer says 7');
  assert.ok(!/channel: 'rc or stable'/.test(app));
  assert.ok(!/'stable' \? 'stable' : 'rc'/.test(app));
});

test('font sizes in the new stylesheet stay on the page scale', () => {
  const css = ui('ci.css');
  for (const m of css.matchAll(/font(?:-size)?:\s*(?:\d+\s+)?(\d+)px/g)) assert.ok([12, 13, 14, 20].includes(Number(m[1])), m[0]);
});

test('errors: a lost connection is said in plain words, a program message is kept', () => {
  const t = load();
  assert.match(t.plainErr(new TypeError('Failed to fetch')), /^Could not reach the Releases program/);
  assert.match(t.plainErr(new Error('')), /^Could not reach/);
  assert.equal(t.plainErr(new Error('That run is not in this CI store.')), 'That run is not in this CI store.');
});
