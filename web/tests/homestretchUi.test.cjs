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
  for (const f of ['app.js', 'util.js', 'appearance.js', 'detail.js', 'ops.js']) {
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

test('the pages read only the program routes; the one write is Data.act, and no page file writes', () => {
  const data = read('data.js');
  const pages = read('app.js') + read('ops.js') + read('detail.js') + read('util.js');
  assert.doesNotMatch(pages, /method:\s*['"]/i, 'only data.js may choose a method');
  const posts = [...data.matchAll(/method:\s*['"](\w+)['"]/g)].map((m) => m[1]);
  assert.deepEqual(posts, ['POST'], 'data.js has exactly one POST (the action route)');
  assert.ok(data.includes("'/api/action/'"), 'the POST goes to the action route only');
  for (const route of ['/api/projects', '/api/overview', '/api/runs', '/api/run', '/api/run/log', '/api/machines', '/api/snapshot', '/api/queue', '/api/policy', '/api/holds', '/api/activity']) assert.ok(data.includes(route), route);
  assert.doesNotMatch(read('ops.js'), /\bfetch\(/, 'ops.js goes through Data only');
});

// ---------- the queue, rules, holds and activity pages, with fixture data ----------
const NOW = '2026-11-27T10:00:00Z';
function ops(extra) {
  const calls = []; let renders = 0; const answers = []; let reply = { ok: true, message: 'Done.' };
  const main = {};
  const sb = {
    document: { querySelector: (q) => (q === '#main' ? main : null), querySelectorAll: () => [] }, Date, Math, isFinite, String, Array, Object, JSON, Promise, setTimeout, navigator: { clipboard: { writeText: () => Promise.resolve() } },
    render: () => { renders++; }, route: () => ({ name: 'machines' }), go: () => {},
    D: { loaded: true, pid: 'unravis', projects: [{ id: 'unravis', name: 'Unravis' }, { id: 'brain', name: 'Brain' }], ops: { queue: null, policy: null, holds: null, activity: null, err: {}, old: false, loaded: true } },
    Data: { act: async (id, project, args) => { calls.push(JSON.parse(JSON.stringify({ id, project, args }))); return answers.length ? answers.shift() : reply; }, refreshOps: async () => {} },
  };
  vm.createContext(sb);
  vm.runInContext(read('util.js') + '\n' + read('ops.js') + '\n;globalThis.__ops = Ops;', sb, { filename: 'ops.js' });
  Object.assign(sb.D.ops, extra || {});
  const click = async (data, tag) => {
    const t = { tagName: tag || 'BUTTON', dataset: data, closest: (q) => (q === '[data-ops]' ? t : null) };
    main.onclick({ target: t });
    for (let i = 0; i < 6; i++) await new Promise((r) => setImmediate(r));
  };
  const type = (key, value, checked) => { const t = { dataset: { in: key }, value, checked }; main.oninput({ target: t }); };
  sb.__ops.bind();
  return { sb, Ops: sb.__ops, calls, answers, setReply: (r) => { reply = r; }, click, type, renders: () => renders };
}
const req = (o) => Object.assign({ id: 'q1', kind: 'pr', repo: 'abhinavjp/unravis', label: 'PR 321: tidy the footer', workflow: 'ci.yml', state: 'queued', priority: 1, priorityLabel: 'Normal', enqueuedAt: NOW, number: 321, sha: '0123456789abcdef' }, o);
const queueFixture = () => ({
  ciFound: true, now: NOW, paused: false, counts: { queued: 2, running: 1, waiting: 1, done: 2 },
  running: [req({ id: 'r1', state: 'running', claimedBy: 'bp-pc-7', claimedAt: NOW, label: 'develop push' })],
  queued: [req({ id: 'a1', label: 'PR 400: <img src=x onerror=alert(1)>', why: { code: 'ready', text: 'A machine is free and will take it next.' } }),
    req({ id: 'a2', label: 'PR 401', waiting: true, labels: ['windows', 'gpu'], why: { code: 'no-machine', text: 'No machine has the labels windows and gpu.' } })],
  done: [req({ id: 'd1', state: 'done', conclusion: 'success', runId: '9001', finishedAt: NOW }), req({ id: 'd2', state: 'done', conclusion: 'superseded', display: 'Replaced by a newer push.', finishedAt: NOW })],
});

test('queue page: every job says where it is or why it waits, with real buttons, and text from the store is escaped', () => {
  const { Ops } = ops({ queue: queueFixture() });
  const h = Ops.viewQueue();
  assert.match(h, /Now and next/); assert.match(h, /bp-pc-7/);
  assert.match(h, /A machine is free and will take it next\./);
  assert.match(h, /No machine has the labels windows and gpu\./);
  assert.match(h, /Needs a machine with/);
  assert.match(h, /Add a machine/);
  for (const w of ['Move to top', 'Cancel', 'Retry', 'Pause the queue', 'Starts next', 'Waiting', 'Replaced']) assert.ok(h.includes(w), w);
  assert.match(h, /Pull requests from forks never run on your machines/);
  assert.doesNotMatch(h, /<img src=x/, 'a hostile job name is escaped');
  assert.doesNotMatch(h, /\sstyle=/);
  assert.match(h, /data-ops="retry" data-id="9001"/, 'Retry carries the run id of the finished job');
  assert.match(h, /No job has finished|Recently finished/);
});

test('queue page: a paused queue says so and offers Resume; an empty queue reads well', () => {
  const q = queueFixture(); q.paused = true; q.pausedReason = 'Paused from this page.';
  let h = ops({ queue: q }).Ops.viewQueue();
  assert.match(h, /The queue is paused/); assert.match(h, /Resume the queue/); assert.doesNotMatch(h, /Pause the queue/);
  const empty = { ciFound: true, now: NOW, paused: false, counts: { queued: 0, running: 0, waiting: 0, done: 0 }, running: [], queued: [], done: [] };
  h = ops({ queue: empty }).Ops.viewQueue();
  assert.match(h, /Nothing is waiting or running/); assert.match(h, /No job has finished here yet/);
});

test('pages say why there is nothing to show: loading, no store, an old program, a failed read', () => {
  assert.match(ops({ loaded: false }).Ops.viewQueue(), /Loading/);
  assert.match(ops({ old: true }).Ops.viewHolds(), /older than this page/);
  assert.match(ops({ queue: { ciFound: false } }).Ops.viewQueue(), /No check records found/);
  assert.match(ops({ activity: null, err: { activity: 'The activity log could not be opened.' } }).Ops.viewActivity(), /could not be opened/);
  const part = Object.assign(queueFixture(), { error: 'The queue file is half written.' });
  assert.match(ops({ queue: part }).Ops.viewQueue(), /half written/);
});

const policyFixture = () => ({
  ciFound: true, now: NOW,
  projects: [{ project: 'abhinavjp/unravis', mode: 'only', machines: ['bp-pc-7'], text: 'Only bp-pc-7', explicit: true, impact: { queued: 2, stranded: [], noMachine: 0, machines: [] }, impactLine: '' }, { project: 'abhinavjp/brain', mode: 'any', machines: [], text: 'Any machine', explicit: false, impact: { queued: 0, stranded: [], noMachine: 0, machines: [] }, impactLine: '' }],
  machines: [{ name: 'bp-pc-7', state: 'ready', labels: ['windows'], slots: 2, running: 1, lastSeen: NOW, seenAgo: '1 min ago', drainAsked: false, denied: [] }, { name: 'bp-pc-9', state: 'draining', labels: ['windows'], slots: 1, running: 0, lastSeen: NOW, seenAgo: '2 min ago', drainAsked: true, denied: ['abhinavjp/unravis'] }],
});

test('machine rules: the matrix shows allowed and not allowed in words, the drawer drains and allows', async () => {
  const t = ops({ policy: policyFixture() });
  const m = t.Ops.machinesExtra();
  assert.match(m, /Who may build what/); assert.match(m, /Allowed/); assert.match(m, /Not allowed/); assert.match(m, /the default/);
  assert.match(m, /Labels decide|labels/i);
  assert.match(t.Ops.machineCell({ id: 'bp-pc-9' }), /Draining/);
  assert.match(t.Ops.machineCell({ id: 'bp-pc-7' }), /Rules and drain/);
  await t.click({ ops: 'drawer-open', name: 'bp-pc-7' });
  let d = t.Ops.machinesExtra();
  assert.match(d, /role="dialog"/); assert.match(d, /Drain this machine/); assert.match(d, /Projects this machine may build/);
  await t.click({ ops: 'm-drain', name: 'bp-pc-7' });
  d = t.Ops.machinesExtra();
  assert.match(d, /finishes the 1 job it is running, takes nothing new, then stops/);
  assert.match(d, /unravis-ci serve/, 'the drain copy says how to bring the machine back');
  await t.click({ ops: 'm-drain-yes', name: 'bp-pc-7' });
  assert.deepEqual(t.calls.map((c) => [c.id, c.project, c.args]), [['machines.drain', 'unravis', { name: 'bp-pc-7' }]]);
  t.Ops.state.drawer = 'bp-pc-9';
  assert.match(t.Ops.machinesExtra(), /asked to drain/);
  await t.click({ ops: 'm-resume', name: 'bp-pc-9' });
  assert.equal(t.calls[1].id, 'machines.resume');
});

test('machine rules: Set policy runs a dry run first, shows the stranded jobs, and saves only with base and the same ids', async () => {
  const t = ops({ policy: policyFixture() });
  await t.click({ ops: 'pol-open', project: 'abhinavjp/brain' });
  await t.click({ ops: 'pol-mode', mode: 'only' });
  assert.match(t.Ops.machinesExtra(), /Pick at least one machine/, 'an empty Only list cannot be checked');
  await t.click({ ops: 'pol-mach', name: 'bp-pc-9' });
  t.answers.push({ ok: false, status: 409, message: 'Refused: 2 queued jobs would have no machine.', dryRun: true, impact: { queued: 3, stranded: ['q7', 'q8'], strandedCount: 2, noMachine: 0, base: 'BASE1', line: '2 queued jobs would have no machine.' } });
  await t.click({ ops: 'pol-check' });
  assert.deepEqual(t.calls[0], { id: 'policy.set', project: 'unravis', args: { project: 'abhinavjp/brain', mode: 'only', machines: ['bp-pc-9'], dryRun: true } });
  let h = t.Ops.machinesExtra();
  assert.match(h, /2 queued jobs would have/); assert.match(h, /q7/); assert.match(h, /I understand they will wait/);
  assert.match(h, /data-ops="save"[^>]*disabled/, 'Save stays off until the stranding is acknowledged');
  t.type('ack', '', true);
  t.Ops.state.edit.ack = true;
  t.answers.push({ ok: true, message: 'Policy for abhinavjp/brain is now Only bp-pc-9.' });
  await t.click({ ops: 'save' });
  assert.deepEqual(t.calls[1].args, { project: 'abhinavjp/brain', mode: 'only', machines: ['bp-pc-9'], confirm: true, base: 'BASE1', stranded: ['q7', 'q8'] });
  assert.equal(t.Ops.state.edit, null); assert.equal(t.Ops.state.note.kind, 'ok');
  assert.match(t.Ops.state.note.text, /now Only bp-pc-9/);
});

test('machine rules: a rule that moved since the dry run is shown again, not saved over; refusals are shown in plain words', async () => {
  const t = ops({ policy: policyFixture() });
  await t.click({ ops: 'pol-open', project: 'abhinavjp/unravis' });
  await t.click({ ops: 'pol-mode', mode: 'any' });
  t.answers.push({ ok: true, message: 'Dry run.', impact: { queued: 0, stranded: [], strandedCount: 0, base: 'B1', line: 'No queued job loses its machine.' } });
  await t.click({ ops: 'pol-check' });
  assert.match(t.Ops.machinesExtra(), /No queued job loses its machine/);
  t.answers.push({ ok: false, status: 409, message: 'Refused: the rules changed.', impact: { stranded: ['z'], strandedCount: 1, base: 'B2', line: '1 queued job would have no machine.' } });
  await t.click({ ops: 'save' });
  assert.equal(t.Ops.state.edit.impact.base, 'B2'); assert.equal(t.Ops.state.edit.moved, true);
  assert.match(t.Ops.state.note.text, /rules changed while you were looking/);
  t.answers.push({ ok: false, status: 403, message: 'This page has no action key. Open the link printed by unravis-releases.', error: 'no-key' });
  await t.click({ ops: 'save' });
  assert.equal(t.Ops.state.note.kind, 'bad'); assert.match(t.Ops.state.note.text, /no action key/);
});

test('queue actions send the registry ids with the right arguments, and a refusal is shown', async () => {
  const t = ops({ queue: queueFixture() });
  await t.click({ ops: 'top', id: 'a2' });
  await t.click({ ops: 'pause' });
  await t.click({ ops: 'resume' });
  await t.click({ ops: 'cancel', id: 'a1' });
  assert.match(t.Ops.viewQueue(), /Take this job out of the queue/);
  t.type('reason', 'wrong branch');
  await t.click({ ops: 'cancel-yes', id: 'a1' });
  await t.click({ ops: 'retry', id: '9001' });
  assert.match(t.Ops.viewQueue(), /Run this job again/);
  await t.click({ ops: 'retry-yes', id: '9001' });
  assert.deepEqual(t.calls.map((c) => [c.id, c.args]), [['queue.top', { id: 'a2' }], ['queue.pause', {}], ['queue.resume', {}], ['queue.cancel', { id: 'a1', reason: 'wrong branch' }], ['queue.retry', { run: '9001' }]]);
  assert.ok(t.calls.every((c) => c.project === 'unravis'));
  t.setReply({ ok: false, status: 404, message: 'There is no such action.' });
  await t.click({ ops: 'pause' });
  assert.equal(t.Ops.state.note.kind, 'bad');
  assert.match(t.Ops.state.note.text, /older|no such action|cannot do that/i);
  t.setReply({ ok: false, status: 409, message: 'That job already finished.', error: 'finished' });
  await t.click({ ops: 'top', id: 'a2' });
  assert.match(t.Ops.state.note.text, /already finished/);
});

const holdFixture = () => ({
  ciFound: true, now: NOW,
  active: [{ id: 'h1', project: 'unravis', reason: 'Demo on Friday', owner: 'nachi', state: 'active', start: NOW, end: '2026-11-28T10:00:00Z' }],
  upcoming: [{ id: 'h2', project: 'all', reason: 'Year-end freeze', owner: 'abhinav', state: 'upcoming', start: '2026-12-20T00:00:00Z', end: '2027-01-02T00:00:00Z' }],
  lifted: [{ id: 'h0', project: 'brain', reason: 'Old', owner: 'nachi', state: 'lifted', start: NOW, end: NOW, liftedBy: 'abhinav', liftReason: 'demo moved' }], ended: [],
});

test('holds: the page, the Overview line, lift with a reason, and start a hold with the chosen length', async () => {
  const t = ops({ holds: holdFixture() });
  const h = t.Ops.viewHolds();
  assert.match(h, /Promote is held for Unravis/); assert.match(h, /Demo on Friday/); assert.match(h, /Owner: nachi/);
  assert.match(h, /Lift it/); assert.match(h, /Cancel it/); assert.match(h, /All projects/); assert.match(h, /lifted by abhinav: demo moved/);
  assert.match(h, /only stops Promote/);
  const line = t.Ops.holdLine();
  assert.match(line, /Promote is held/); assert.match(line, /nachi/); assert.match(line, /Demo on Friday/);
  assert.equal(ops({ holds: Object.assign(holdFixture(), { active: [] }) }).Ops.holdLine(), '');
  await t.click({ ops: 'lift', id: 'h1' });
  t.type('reason', 'demo moved');
  await t.click({ ops: 'lift-yes', id: 'h1' });
  assert.deepEqual(t.calls[0], { id: 'hold.lift', project: 'unravis', args: { id: 'h1', reason: 'demo moved' } });
  const f = { target: { closest: (q) => (q === '[data-ops="hold-form"]' ? {} : null) }, preventDefault() {} };
  t.type('hold-reason', '   ');
  t.sb.document.querySelector = () => null;
  t.sb.__ops.state.hold.hours = '3d';
  t.type('hold-project', 'all');
  t.type('hold-hours', '3d');
  t.type('hold-reason', 'Audit week');
  const main = {}; t.sb.document.querySelector = (q) => (q === '#main' ? main : null);
  t.Ops.bind(); main.onsubmit(f);
  for (let i = 0; i < 6; i++) await new Promise((r) => setImmediate(r));
  assert.deepEqual(t.calls[1], { id: 'hold.add', project: 'unravis', args: { all: true, until: '3d', reason: 'Audit week' } });
});

test('holds: a hold without a reason is not sent', async () => {
  const t = ops({ holds: holdFixture() });
  const main = {}; t.sb.document.querySelector = (q) => (q === '#main' ? main : null); t.Ops.bind();
  main.onsubmit({ target: { closest: () => ({}) }, preventDefault() {} });
  assert.equal(t.calls.length, 0); assert.match(t.Ops.state.note.text, /Say why/);
});

test('activity: newest first, from where, a source filter, outcome and escaped text', async () => {
  const entries = [
    { time: '2026-11-27T09:00:00Z', actor: 'cli', person: 'abhinav', action: 'queue.pause', reason: 'deploy' },
    { time: '2026-11-27T09:30:00Z', actor: 'ui', person: 'nachi', action: 'policy.set', target: 'abhinavjp/unravis', outcome: 'refused', detail: '<b>2 stranded</b>' },
    { time: '2026-11-27T09:40:00Z', actor: 'claude', action: 'hold.add', target: 'h1', reason: 'Demo' }];
  const t = ops({ activity: { ciFound: true, now: NOW, skipped: 1, order: 'oldest-first', entries } });
  let h = t.Ops.viewActivity();
  assert.ok(h.indexOf('Add a hold') < h.indexOf('Pause the queue'), 'newest entry first');
  assert.match(h, /Set a machine rule/); assert.match(h, /refused/); assert.doesNotMatch(h, /<b>2 stranded/);
  assert.match(h, /1 line of the log could not be read and was left out/);
  assert.match(h, /not recorded/, 'an entry with no person says so');
  await t.click({ ops: 'actor', actor: 'cli' });
  h = t.Ops.viewActivity();
  assert.match(h, /Pause the queue/); assert.doesNotMatch(h, /Set a machine rule/);
  await t.click({ ops: 'actor', actor: 'all' });
  const empty = ops({ activity: { ciFound: true, now: NOW, skipped: 0, entries: [] } }).Ops.viewActivity();
  assert.match(empty, /No change has been made yet/);
});

test('tab numbers and the Overview queue card come from the answers, and stay blank until there is one', () => {
  assert.deepEqual(Object.assign({}, ops({}).Ops.counts()), { queue: '', holds: '', activity: '' });
  const t = ops({ queue: queueFixture(), holds: holdFixture(), activity: { ciFound: true, entries: [{}, {}] } });
  assert.deepEqual(Object.assign({}, t.Ops.counts()), { queue: 3, holds: 2, activity: 2 });
  assert.match(t.Ops.queueCard(), /1<\/b> running, <b>2<\/b> queued, <b>1<\/b> waiting for a machine/);
});

test('every Ops button action in the markup has a handler, and the key shortcuts match the tabs', () => {
  const src = read('ops.js');
  const used = new Set([...src.matchAll(/data-ops="([\w-]+)"/g)].map((m) => m[1]));
  const handled = new Set([...src.matchAll(/case '([\w-]+)'/g)].map((m) => m[1]));
  const lone = [...used].filter((u) => !handled.has(u) && u !== 'hold-form');
  assert.deepEqual(lone, []);
  const app = read('app.js');
  assert.match(app, /q: 'queue'/); assert.match(app, /h: 'holds'/); assert.match(app, /a: 'activity'/);
  assert.match(read('index.html'), /<script src="ops.js"><\/script>/);
});
