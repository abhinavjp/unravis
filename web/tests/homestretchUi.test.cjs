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

test('times in the future read as how far away they are, not "just now"', () => {
  const soon = new Date(Date.now() + 3 * 3600e3 + 60e3).toISOString();
  const h = ops({ holds: Object.assign(holdFixture(), { active: [{ id: 'h1', project: 'unravis', reason: 'Demo', owner: 'nachi', state: 'active', start: NOW, end: soon }] }) });
  assert.match(h.Ops.viewHolds(), /\(in 3 h\)/);
  assert.match(h.Ops.holdLine(), /\(in 3 h\)/);
  assert.doesNotMatch(h.Ops.holdLine(), /just now/);
  const far = new Date(Date.now() + 3 * 86400e3 + 3600e3).toISOString();
  const f = ops({ holds: Object.assign(holdFixture(), { active: [{ id: 'h1', project: 'unravis', reason: 'Demo', owner: 'nachi', state: 'active', start: NOW, end: far }] }) });
  assert.match(f.Ops.holdLine(), /\(in 3 days\)/);
});

test('holds: a lifted hold shows when it was lifted, not its planned end; narrow tables carry cell labels', () => {
  const fx = holdFixture(); fx.lifted[0].end = '2099-01-01T00:00:00Z'; fx.lifted[0].liftedAt = NOW;
  const h = ops({ holds: fx }).Ops.viewHolds();
  assert.doesNotMatch(h, /2099/, 'the planned end of a lifted hold is not shown as when it ended');
  assert.match(h, /Ended or lifted/);
  assert.match(h, /class="tbl qt"/); assert.match(h, /data-l="Lifted"/); assert.match(h, /data-l="Why"/);
  const a = ops({ activity: { ciFound: true, now: NOW, entries: [{ time: NOW, actor: 'ui', person: 'nachi', action: 'hold.add', target: 'h1' }] } }).Ops.viewActivity();
  assert.match(a, /class="tbl qt"/); for (const l of ['When', 'From', 'Who', 'On', 'Why']) assert.ok(a.includes(`data-l="${l}"`), l);
});

test('machines: the machine names in the grid open the drawer, and this PC finds its rules under the name "local"', () => {
  const pol = policyFixture(); pol.machines = [{ name: 'local', state: 'ready', labels: [], slots: 1, running: 0, lastSeen: NOW, seenAgo: '1 min ago', drainAsked: false, denied: [] }];
  const t = ops({ policy: pol });
  assert.match(t.Ops.machinesExtra(), /class="thbtn" data-ops="drawer-open" data-name="local"/);
  assert.match(t.Ops.machineCell({ id: 'm-9ebd7193', self: true }), /Rules and drain/, 'the enrolled id of this PC is matched to local');
  assert.match(t.Ops.machineCell({ id: 'm-other', self: false }), /Who may build what, below/);
});

test('server messages start with a capital letter and the impact headline is not repeated', async () => {
  const t = ops({ queue: queueFixture() });
  t.setReply({ ok: false, status: 409, message: 'that job already finished.', error: 'finished' });
  await t.click({ ops: 'top', id: 'a2' });
  assert.match(t.Ops.state.note.text, /^That job already finished/);
  t.setReply({ ok: false, status: 409, message: 'abhinavjp/unravis: any machine -> any except these: local', error: 'x' });
  await t.click({ ops: 'top', id: 'a2' });
  assert.match(t.Ops.state.note.text, /^abhinavjp\/unravis/, 'a project path is not capitalised');
  const pol = policyFixture();
  const s = ops({ policy: pol });
  s.answers.push({ ok: false, status: 409, message: 'Refused.', dryRun: true, impact: { queued: 3, base: 'b1', line: 'Unique-headline-xyz: 2 queued jobs could not run.', stranded: ['q1', 'q2'], strandedCount: 2, noMachine: 0 } });
  await s.click({ ops: 'pol-open', project: 'abhinavjp/unravis' });
  await s.click({ ops: 'pol-mode', mode: 'only' });
  await s.click({ ops: 'pol-mach', name: 'bp-pc-9' });
  await s.click({ ops: 'pol-check' });
  const d = s.Ops.machinesExtra();
  assert.equal((d.match(/2 queued jobs would have no machine/g) || []).length, 1, 'the headline appears once');
  assert.doesNotMatch(d, /Unique-headline-xyz/, 'the server line is not repeated under its own headline');
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

test('W-05 wiring: the page source draws the note under the Releases heading, the chip with its warning style, and re-reads on Check again', () => {
  const app = read('app.js');
  assert.match(app, /Fresh\.noteHtml\('releases', Date\.now\(\)\)/);
  assert.match(app, /Fresh\.chip\(Date\.now\(\)\)/);
  assert.match(app, /\$\$\('\[data-check\]', main\)\.forEach/);
  assert.match(read('components.css'), /\.stale-note\{/);
  assert.match(read('components.css'), /\.fresh\.stale\{color:var\(--warn\)/);
});
