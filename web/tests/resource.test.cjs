const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const r = loadTypeScript('src/lib/resource.ts');
const cases = loadTypeScript('src/lib/cases.ts');
const importCopy = loadTypeScript('src/lib/copy/importCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

// Synthetic values only: 2001 dates, web-a.example.test hosts, invented rule text.
const T0 = Date.parse('2001-02-03T04:00:00Z');
const MIN = 60000;
const iso = (ms) => new Date(ms).toISOString();
const HOST = 'web-a.example.test';
const CASE_ID = 'abcd2345abcd2345abcd234567';

const resourceJson = (extra = {}) => ({
  from: iso(T0), to: iso(T0 + 60 * MIN), partial: false,
  hosts: [{ host: HOST, summary: ['Host CPU peaked at 91% at 04:20.'], coverage: [{ metric: 'host.cpu', fraction: 1 }, { metric: 'redis.mem', fraction: 0 }] }],
  rules: [{ id: 'RC3', host: HOST, title: 'Host CPU past a limit', wording: 'Host CPU was at or above 85% for 4 minutes during a stall.', label: 'Possible overlap', thresholds: ['85% for 3 minutes'], evidence: ['ev-1', 'ev-2'], episode: { start: iso(T0 + 20 * MIN), end: iso(T0 + 25 * MIN) } }],
  gaps: [], markers: [{ kind: 'restart', edge: 'start', host: HOST, t: iso(T0 + 10 * MIN), text: 'Service started' }],
  episodes: [{ start: iso(T0 + 20 * MIN), end: iso(T0 + 25 * MIN) }], ...extra
});
const point = (t, mean, count = 1) => ({ t, min: mean, mean, max: mean, count });
const seriesJson = (series, extra = {}) => ({ from: iso(T0), to: iso(T0 + 60 * MIN), bucketMs: 6000, buckets: 600, partial: false, scanned: 10, series, ...extra });
const item = (sel, points, extra = {}) => ({ sel, unit: 'percent', unitMixed: false, count: points ? points.length : 0, points, ...extra });

/* ------------------------------------------------------------------ shape checks */

test('parseResource accepts the documented shape and turns times into milliseconds', () => {
  const view = r.parseResource(resourceJson());
  assert.ok(view);
  assert.equal(view.fromMs, T0);
  assert.equal(view.hosts[0].host, HOST);
  assert.deepEqual(view.hosts[0].processes, []);
  assert.equal(view.rules[0].episode.start, T0 + 20 * MIN);
  assert.equal(view.markers[0].t, T0 + 10 * MIN);
  assert.deepEqual(view.episodes, [{ start: T0 + 20 * MIN, end: T0 + 25 * MIN }]);
});

test('parseResource refuses what is not the documented shape, and never accepts a null array', () => {
  for (const bad of [null, [], 'x', {}, resourceJson({ partial: 'no' }), resourceJson({ from: 'not a time' }), resourceJson({ hosts: null }), resourceJson({ rules: null }),
    resourceJson({ gaps: null }), resourceJson({ markers: null }), resourceJson({ episodes: null }), resourceJson({ hosts: [{ host: HOST, summary: 'text', coverage: [] }] }),
    resourceJson({ hosts: [{ host: HOST, summary: [], coverage: [{ metric: 'x', fraction: 'high' }] }] }), resourceJson({ gaps: [1] }),
    resourceJson({ rules: [{ id: 'RC3', host: HOST, title: 't', wording: 'w', label: 'l', thresholds: [], evidence: null, episode: null }] }),
    resourceJson({ episodes: [{ start: iso(T0 + MIN), end: iso(T0) }] })]) {
    assert.equal(r.parseResource(bad), null);
  }
});

test('a rule with no stall period, a text or object thresholds field, and per-process selectors are accepted', () => {
  const view = r.parseResource(resourceJson({
    hosts: [{ host: HOST, summary: [], coverage: [], processes: [{ name: 'worker-a', sel: { 'proc.cpu': 'kind=counter;host=h', bogus: 'x' } }, { name: 'no-selectors', sel: {} }, 'junk'] }],
    rules: [
      { id: 'RC1', host: HOST, title: 'a', wording: 'w', label: 'l', thresholds: '85%', evidence: [], episode: null },
      { id: 'RC2', host: HOST, title: 'b', wording: 'w', label: 'l', thresholds: { cpu: 85 }, evidence: [], episode: null }
    ]
  }));
  assert.ok(view);
  assert.deepEqual(view.rules[0].thresholds, ['85%']);
  assert.deepEqual(view.rules[1].thresholds, ['cpu: 85']);
  assert.equal(view.rules[0].episode, null);
  assert.deepEqual(view.hosts[0].processes, [{ name: 'worker-a', sel: { 'proc.cpu': 'kind=counter;host=h' } }]);
});

test('parseSeries accepts the documented shape and treats null points as none', () => {
  const view = r.parseSeries(seriesJson([item('a', [point(T0, 5)]), item('b', null, { count: 0 })]));
  assert.ok(view);
  assert.equal(view.bucketMs, 6000);
  assert.equal(view.series[0].points[0].mean, 5);
  assert.deepEqual(view.series[1].points, []);
  for (const bad of [null, {}, seriesJson(null), seriesJson([{ sel: 'a', count: 1, points: [{ t: 'x' }] }]), seriesJson([], { bucketMs: 'x' }), seriesJson([], { partial: 1 }), seriesJson([], { from: 'nope' })]) {
    assert.equal(r.parseSeries(bad), null);
  }
});

/* ------------------------------------------------------------------ the two routes (mocked fetch; no network) */

function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), headers: init.headers instanceof Headers ? init.headers : new Headers(init.headers) });
    const answer = await handler(String(url));
    return new Response(JSON.stringify(answer.body ?? {}), { status: answer.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

test('fetchResource and fetchSeries build the documented requests and check the answers', async () => {
  const calls = mockServer((url) => (url.includes('/resource') ? { body: resourceJson() } : { body: seriesJson([item('a', [point(T0, 1)])]) }));
  const view = await cases.fetchResource(CASE_ID, { fromMs: T0, toMs: T0 + 60 * MIN, episodes: [{ start: T0 + MIN, end: T0 + 2 * MIN }] });
  assert.equal(view.hosts.length, 1);
  const first = new URL(calls[0].url, 'http://x');
  assert.equal(first.pathname, '/api/cases/' + CASE_ID + '/resource');
  assert.equal(first.searchParams.get('from'), '2001-02-03T04:00:00.000Z');
  assert.equal(first.searchParams.get('ep'), '2001-02-03T04:01:00.000Z/2001-02-03T04:02:00.000Z');
  assert.equal(calls[0].headers.get('X-Audit-Token'), 'session-token-1');

  await cases.fetchSeries(CASE_ID, { fromMs: T0, toMs: T0 + 60 * MIN, bucketMs: 6000.7, sels: ['kind=metric;host=h;field=value', 'kind=counter;field=value'] });
  const second = new URL(calls[1].url, 'http://x');
  assert.equal(second.pathname, '/api/cases/' + CASE_ID + '/series');
  assert.equal(second.searchParams.get('bucket'), '6000', 'the bucket is whole milliseconds');
  assert.deepEqual(second.searchParams.getAll('sel'), ['kind=metric;host=h;field=value', 'kind=counter;field=value']);
});

test('a server refusal keeps its reason and message; a wrong shape becomes "unreadable"', async () => {
  mockServer(() => ({ status: 400, body: { error: 'bad', reason: 'too-many-buckets', message: 'Too many. Use a bucket of at least 87 ms.' } }));
  await assert.rejects(cases.fetchSeries(CASE_ID, { fromMs: T0, toMs: T0 + MIN, bucketMs: 1, sels: ['x'] }), (error) => error.reason === 'too-many-buckets' || error.status === 400);
  mockServer(() => ({ body: { hosts: null } }));
  await assert.rejects(cases.fetchResource(CASE_ID, { fromMs: T0, toMs: T0 + MIN }), (error) => error.reason === 'unreadable');
  await assert.rejects(cases.fetchSeries(CASE_ID, { fromMs: T0, toMs: T0 + MIN, bucketMs: 10, sels: ['x'] }), (error) => error.reason === 'unreadable');
});

/* ------------------------------------------------------------------ buckets, windows and zoom */

test('the bucket is the window span divided by 600, whole milliseconds, never under the least', () => {
  assert.equal(r.bucketFor(60 * MIN), 6000);
  assert.equal(r.bucketFor(60 * MIN + 1), 6001);
  assert.equal(r.bucketFor(1000), 2);
  assert.equal(r.bucketFor(10), 1);
  assert.equal(r.bucketFor(0), 1);
  assert.equal(r.bucketFor(60 * MIN, 9000), 9000);
  assert.equal(r.bucketFor(24 * 60 * MIN * 100), Math.ceil((24 * 60 * MIN * 100) / 600));
  assert.ok(Number.isInteger(r.bucketFor(123456789)));
  assert.equal(r.leastBucketFrom('This window is long. Use a bucket of at least 87 ms.'), 87);
  assert.equal(r.leastBucketFrom('nothing here'), null);
  assert.equal(r.bucketText(250), '250 ms');
  assert.equal(r.bucketText(6000), '6 s');
  assert.equal(r.bucketText(150000), '2.5 min');
});

test('a window change refetches /series with a bucket of span / 600', async () => {
  const calls = [];
  const fetcher = async (request) => { calls.push(request); return r.parseSeries(seriesJson([], { bucketMs: request.bucketMs })); };
  await r.readSeries(fetcher, { fromMs: T0, toMs: T0 + 60 * MIN }, ['a', 'b']);
  await r.readSeries(fetcher, { fromMs: T0 + 10 * MIN, toMs: T0 + 16 * MIN }, ['a', 'b']);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].bucketMs, 6000);
  assert.equal(calls[1].bucketMs, 600);
  assert.deepEqual(calls[1].sels, ['a', 'b']);
  assert.equal(calls[1].fromMs, T0 + 10 * MIN);
});

test('selectors are sent in requests of at most 50, and the answers are joined', async () => {
  const sels = Array.from({ length: 120 }, (_, i) => 'sel-' + i);
  const sizes = [];
  const fetcher = async (request) => { sizes.push(request.sels.length); return r.parseSeries(seriesJson(request.sels.map((sel) => item(sel, [point(T0, 1)])), { partial: sizes.length === 2 })); };
  const read = await r.readSeries(fetcher, { fromMs: T0, toMs: T0 + 60 * MIN }, sels);
  assert.deepEqual(sizes, [50, 50, 20]);
  assert.equal(read.items.size, 120);
  assert.equal(read.partial, true, 'one partial answer makes the whole read partial');
});

test('a refusal that names a least bucket is retried once with that bucket; other refusals are not retried', async () => {
  const calls = [];
  const refusal = Object.assign(new Error('Too many buckets. Use a bucket of at least 87 ms.'), { reason: 'too-many-buckets' });
  const fetcher = async (request) => { calls.push(request.bucketMs); if (calls.length === 1) throw refusal; return r.parseSeries(seriesJson([], { bucketMs: request.bucketMs })); };
  const read = await r.readSeries(fetcher, { fromMs: T0, toMs: T0 + 6000 }, ['a']);
  assert.deepEqual(calls, [10, 87]);
  assert.equal(read.bucketMs, 87);

  let count = 0;
  const other = Object.assign(new Error('The selector is wrong.'), { reason: 'bad-selector' });
  await assert.rejects(r.readSeries(async () => { count++; throw other; }, { fromMs: T0, toMs: T0 + MIN }, ['a']), /selector/);
  assert.equal(count, 1);
});

test('the view window follows the panel, then the zoom on screen, then the queried window', () => {
  const query = { start: iso(T0), end: iso(T0 + 60 * MIN) };
  const slots = [{ start: T0 + 10 * MIN, end: T0 + 11 * MIN }, { start: T0 + 11 * MIN, end: T0 + 12 * MIN }];
  assert.deepEqual(r.viewWindow({ local: null, zoomed: false, slots, query }), { fromMs: T0, toMs: T0 + 60 * MIN });
  assert.deepEqual(r.viewWindow({ local: null, zoomed: true, slots, query }), { fromMs: T0 + 10 * MIN, toMs: T0 + 12 * MIN });
  assert.deepEqual(r.viewWindow({ local: { fromMs: 1, toMs: 2 }, zoomed: true, slots, query }), { fromMs: 1, toMs: 2 });
  assert.equal(r.viewWindow({ local: null, zoomed: false, slots: [], query: { start: 'x', end: 'y' } }), null);
});

test('a brush picks the slots it covers, and a short drag is a click', () => {
  const slots = [0, 1, 2, 3, 4].map((i) => ({ start: T0 + i * MIN, end: T0 + (i + 1) * MIN }));
  assert.deepEqual(r.slotRange(slots, T0 + 90000, T0 + 3 * MIN + 1000), [1, 3]);
  assert.deepEqual(r.slotRange(slots, T0 + 3 * MIN + 1000, T0 + 90000), [1, 3], 'either direction');
  assert.equal(r.slotRange(slots, T0 + 10 * MIN, T0 + 11 * MIN), null);
  assert.equal(r.isBrush(5), false);
  assert.equal(r.isBrush(-6), true);
});

test('the panel zoom buttons keep the window inside the queried window', () => {
  const whole = { fromMs: T0, toMs: T0 + 60 * MIN };
  assert.deepEqual(r.stepWindow(whole, whole, 'in'), { fromMs: T0 + 15 * MIN, toMs: T0 + 45 * MIN });
  assert.deepEqual(r.stepWindow(whole, whole, 'out'), whole);
  const inner = { fromMs: T0 + 10 * MIN, toMs: T0 + 20 * MIN };
  assert.deepEqual(r.stepWindow(inner, whole, 'later'), { fromMs: T0 + 15 * MIN, toMs: T0 + 25 * MIN });
  assert.deepEqual(r.stepWindow({ fromMs: T0, toMs: T0 + 10 * MIN }, whole, 'earlier'), { fromMs: T0, toMs: T0 + 10 * MIN });
});

/* ------------------------------------------------------------------ selectors */

test('selectors follow the documented grammar and are never built from an unsafe name', () => {
  const cand = { kind: 'counter', attrs: [['object', 'Processor'], ['counter', '% Processor Time']], mul: 1 };
  assert.equal(r.selectorFor(HOST, cand), 'kind=counter;host=web-a.example.test;attr.object=Processor;attr.counter=% Processor Time;field=value');
  assert.equal(r.selectorFor('bad;host', cand), null);
  assert.equal(r.selectorFor('a=b', cand), null);
  assert.equal(r.selectorFor('h'.repeat(65), cand), null);
  assert.equal(r.selectorFor('', cand), null);
  const all = r.hostSelectors(HOST);
  assert.equal(new Set(all).size, all.length, 'each selector once');
  for (const sel of all) {
    const attrs = sel.split(';').filter((part) => part.startsWith('attr.'));
    assert.ok(attrs.length <= 4, sel);
    assert.ok(attrs.every((part) => part.split('=')[1].length <= 64), sel);
  }
  assert.ok(r.chunkSelectors(all).every((chunk) => chunk.length <= 50));
  assert.equal(r.chunkSelectors(Array.from({ length: 101 }, (_, i) => i)).length, 3);
});

/* ------------------------------------------------------------------ strips, gaps and the empty state */

test('missing buckets become gaps in the line, not a joined line', () => {
  const points = [point(T0, 1), point(T0 + 1000, 2), point(T0 + 10000, 3)];
  assert.deepEqual(r.toPoints(points, 1000), [[T0, 1], [T0 + 1000, 2], [T0 + 2000, null], [T0 + 10000, 3]]);
  assert.deepEqual(r.toPoints([], 1000), []);
  assert.deepEqual(r.toPoints([point(T0, 5)], 1000, 2), [[T0, 10]]);
});

test('no data gives no strips (the empty state), and a strip with data keeps catalogue order', () => {
  assert.deepEqual(r.buildStrips(HOST, new Map(), 1000), []);
  const cpu = r.selectorFor(HOST, r.STRIP_DEFS[0].lines[0].cands[0]);
  const gc = r.selectorFor(HOST, r.STRIP_DEFS.find((d) => d.id === 'runtime-gc').lines[0].cands[0]);
  const items = new Map([[gc, item(gc, [point(T0, 4)])], [cpu, item(cpu, [point(T0, 50), point(T0 + 1000, 60)])]]);
  const strips = r.buildStrips(HOST, items, 1000);
  assert.deepEqual(strips.map((s) => s.id), ['host-cpu', 'runtime-gc']);
  assert.equal(strips[0].max, 100);
  assert.equal(strips[0].lines[0].points.length, 2);
});

test('free memory is shown in GiB, and the first spelling with data wins', () => {
  const [mb, kb] = r.STRIP_DEFS.find((d) => d.id === 'host-mem').lines[0].cands;
  const mbSel = r.selectorFor(HOST, mb), kbSel = r.selectorFor(HOST, kb);
  const items = new Map([[kbSel, item(kbSel, [point(T0, 1048576)])], [mbSel, item(mbSel, [point(T0, 2048)])]]);
  const strips = r.buildStrips(HOST, items, 1000);
  assert.equal(strips[0].lines[0].points[0][1], 2, '2048 MB is 2 GiB, from the first spelling');
});

test('log entries become a per-minute rate summed over levels', () => {
  const def = r.STRIP_DEFS.find((d) => d.id === 'log-rate').lines[0];
  const warn = r.selectorFor(HOST, def.cands[0]), error = r.selectorFor(HOST, def.cands[3]);
  const items = new Map([[warn, item(warn, [point(T0, 3, 2)])], [error, item(error, [point(T0, 6, 1)])]]);
  const line = r.buildStrips(HOST, items, 60000)[0].lines[0];
  assert.deepEqual(line.points, [[T0, 12]], '3 x 2 + 6 x 1 entries in a one-minute bucket');
});

test('the API strip uses only windows in view with a P95', () => {
  const view = { fromMs: T0, toMs: T0 + 10 * MIN };
  const strip = r.apiStrip([{ at: iso(T0 + MIN), p95Ms: 120 }, { at: iso(T0 + 2 * MIN), p95Ms: null }, { at: iso(T0 + 99 * MIN), p95Ms: 5 }], view, 1000);
  assert.deepEqual(strip.lines[0].points, [[T0 + MIN, 120]]);
  assert.equal(r.apiStrip([], view, 1000), null);
});

test('the API strip joins windows that sit a minute apart even when a bucket is a few seconds', () => {
  const view = { fromMs: T0, toMs: T0 + 10 * MIN };
  const strip = r.apiStrip([1, 2, 3, 4].map((m) => ({ at: iso(T0 + m * MIN), p95Ms: 100 + m })), view, 6000);
  assert.equal(strip.lines[0].points.length, 4);
  assert.ok(strip.lines[0].points.every((p) => p[1] != null));
});

test('the process strip has a grey "all other processes" band only for percent-of-host CPU with host CPU present', () => {
  const hostCpu = { id: 'host.cpu', name: 'Host CPU', points: [[T0, 80], [T0 + 1000, 90]] };
  const refs = [{ name: 'worker-a', sel: 'a' }, { name: 'worker-b', sel: 'b' }];
  const make = (unit) => new Map([['a', item('a', [point(T0, 30), point(T0 + 1000, 40)], { unit })], ['b', item('b', [point(T0, 20), point(T0 + 1000, 10)], { unit })]]);
  const strip = r.processStrip('proc.cpu', refs, make('percent-host'), 1000, hostCpu);
  const band = strip.lines.find((line) => line.band);
  assert.equal(strip.lines.length, 3);
  assert.deepEqual(band.points, [[T0, 30], [T0 + 1000, 40]]);
  assert.equal(band.name, 'All other processes');
  assert.equal(r.processStrip('proc.cpu', refs, make('percent-host'), 1000, null).lines.some((line) => line.band), false, 'no host CPU, no band');
  assert.equal(r.processStrip('proc.cpu', refs, make('percent-core'), 1000, hostCpu).lines.some((line) => line.band), false, 'other units, no band');
  assert.equal(r.processStrip('proc.ws', refs, make('percent-host'), 1000, hostCpu).lines.some((line) => line.band), false, 'only CPU has the band');
  assert.equal(r.processStrip('proc.cpu', refs, new Map(), 1000, hostCpu), null);
  assert.equal(r.processSelectors(Array.from({ length: 9 }, (_, i) => ({ name: 'p' + i, sel: { 'proc.cpu': 's' + i } })), 'proc.cpu').length, 5);
  assert.deepEqual(r.processSelectors([{ name: 'p', sel: { 'proc.ws': 's' } }], 'proc.cpu'), []);
});

test('strips are ordered API, host CPU and memory, processes, then runtime, stores and logs', () => {
  const mk = (id) => ({ id, title: id, unit: '', lines: [] });
  const order = r.orderStrips(mk('api-p95'), [mk('host-cpu'), mk('runtime-gc'), mk('host-mem'), mk('log-rate')], mk('processes')).map((s) => s.id);
  assert.deepEqual(order, ['api-p95', 'host-cpu', 'host-mem', 'processes', 'runtime-gc', 'log-rate']);
  assert.deepEqual(r.orderStrips(null, [], null), []);
});

/* ------------------------------------------------------------------ chips, rows and coverage */

test('one chip per fired rule, with the server wording and label unchanged and a plain hint', () => {
  const view = r.parseResource(resourceJson());
  const chips = r.chipsFor(view.rules, null);
  assert.equal(chips.length, 1);
  assert.equal(chips[0].text, 'Host CPU past a limit');
  assert.equal(chips[0].wording, 'Host CPU was at or above 85% for 4 minutes during a stall.');
  assert.equal(chips[0].label, 'Possible overlap');
  assert.equal(chips[0].hint, r.RULE_HINTS.RC3);
  assert.equal(chips[0].host, HOST);
  assert.match(chips[0].aria, /Possible overlap/);
  assert.deepEqual(r.chipsFor(view.rules, 'other-host.example.test'), []);
  assert.deepEqual(r.chipsFor([], null), []);
  const unknown = r.chipsFor([{ ...view.rules[0], id: 'RC99' }], null);
  assert.equal(unknown[0].hint, r.FALLBACK_HINT, 'a rule id the panel does not know still gets a hint');
  assert.equal(r.ruleHint('toString'), r.FALLBACK_HINT, 'inherited names are not rule ids');
  const keys = r.chipsFor([view.rules[0], view.rules[0]], null).map((c) => c.key);
  assert.equal(new Set(keys).size, 2, 'chip keys are unique');
});

test('rows behind a click are the rules whose stall period overlaps the time', () => {
  const view = r.parseResource(resourceJson({ rules: [
    { id: 'RC3', host: HOST, title: 'a', wording: 'w', label: 'l', thresholds: [], evidence: [], episode: { start: iso(T0 + 20 * MIN), end: iso(T0 + 25 * MIN) } },
    { id: 'RC1', host: HOST, title: 'b', wording: 'w', label: 'l', thresholds: [], evidence: [], episode: null }
  ] }));
  assert.equal(r.rulesAt(view.rules, T0 + 22 * MIN, 0, null).length, 1);
  assert.equal(r.rulesAt(view.rules, T0 + 18 * MIN, 3 * MIN, null).length, 1, 'a window that reaches into the period');
  assert.equal(r.rulesAt(view.rules, T0 + 30 * MIN, MIN, null).length, 0);
  assert.equal(r.rulesAt(view.rules, T0 + 22 * MIN, 0, 'other').length, 0);
  assert.equal(r.rulesOfEpisode(view.rules, view.episodes[0], null).length, 1);
  assert.deepEqual(r.episodeAt(view.episodes, T0 + 21 * MIN), view.episodes[0]);
  assert.equal(r.episodeAt(view.episodes, T0), null);
});

test('coverage dots say how much data there is in words', () => {
  assert.equal(r.coverageDot({ metric: 'host.cpu', fraction: 1 }).level, 'full');
  assert.equal(r.coverageDot({ metric: 'host.cpu', fraction: 0.6 }).level, 'most');
  assert.equal(r.coverageDot({ metric: 'host.cpu', fraction: 0.1 }).level, 'some');
  assert.equal(r.coverageDot({ metric: 'host.cpu', fraction: 0 }).level, 'none');
  assert.equal(r.coverageDot({ metric: 'host.cpu', fraction: 0.5 }).text, 'Host CPU: 50% of the window');
  assert.equal(r.coverageDot({ metric: 'redis.mem', fraction: 0 }).text, 'Redis memory: No data');
  assert.equal(r.coverageDot({ metric: 'made.up', fraction: 1 }).text, 'made.up: 100% of the window');
});

/* ------------------------------------------------------------------ escaping */

const hostile = '<img src=x onerror=alert(1)>"\'&';

test('escapeHTML escapes the five characters', () => {
  assert.equal(r.escapeHTML(hostile), '&lt;img src=x onerror=alert(1)&gt;&quot;&#39;&amp;');
  assert.equal(r.escapeHTML('plain'), 'plain');
});

test('the chart tooltip escapes every name and value, and the option has one grid per strip', () => {
  const pal = { text: '#111111', accent: '#2457c5', accentSoft: '#9db6ea', warn: '#96600a', bad: '#b02a3d', pending: '#5b6878', text2: '#4d5a6b', axisLine: '#d5dbe3', grid: '#e6eaf0', empty: '#eee', heat: [], series: ['#146b58', '#2457c5'], surface: '#ffffff' };
  const strips = [
    { id: 'a', title: 'First', unit: '%', max: 100, lines: [{ id: 'l1', name: hostile, points: [[T0, 1]] }] },
    { id: 'b', title: 'Second', unit: 'GiB', lines: [{ id: 'l2', name: 'Free memory', points: [[T0, 2]] }, { id: 'o', name: 'All other processes', points: [[T0, 2]], band: true }] }
  ];
  const view = { fromMs: T0, toMs: T0 + 60 * MIN };
  const markers = [{ kind: 'restart', edge: 'start', host: HOST, t: T0 + MIN, text: hostile }, { kind: 'restart', edge: 'stop', host: HOST, t: T0 + 90 * MIN, text: 'outside the window' }];
  const option = r.stripsOption({ strips, view, episodes: [{ start: T0 + 5 * MIN, end: T0 + 9 * MIN }, { start: T0 + 80 * MIN, end: T0 + 85 * MIN }], markers, zone: 'UTC', pal });
  assert.equal(option.grid.length, 2);
  assert.equal(option.xAxis.length, 2);
  assert.equal(option.xAxis[0].min, view.fromMs);
  assert.equal(option.xAxis[0].max, view.toMs, 'every strip is on the one window');
  const carriers = option.series.filter((s) => s.markArea);
  assert.equal(carriers.length, 2, 'bands and markers are drawn on every strip');
  assert.equal(carriers[0].markArea.data.length, 1, 'a stall period outside the window is left out');
  assert.equal(carriers[0].markLine.data.length, 1, 'a marker outside the window is left out');
  const html = option.tooltip.formatter([{ axisValue: T0, seriesName: hostile, value: [T0, 1], marker: '<span></span>' }, { axisValue: T0, seriesName: 'gap', value: [T0, null], marker: '' }]);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;img/);
  assert.equal(option.tooltip.formatter([{ axisValue: T0, seriesName: 'x', value: [T0, null], marker: '' }]), '');
  assert.ok(r.chartHeight(2) > r.chartHeight(1));
});

test('the components render text, never markup, and name no variable "state"', () => {
  for (const file of ['src/components/ResourcePanel.svelte', 'src/components/resource/StripChart.svelte', 'src/components/resource/HostLines.svelte', 'src/components/resource/RuleChips.svelte',
    'src/components/resource/RuleDrawer.svelte', 'src/components/resource/CoverageDots.svelte', 'src/lib/resource.ts']) {
    const source = read(file);
    assert.doesNotMatch(source, /\{@html/, file);
    assert.doesNotMatch(source, /\b(?:let|const|var)\s+state\b/, file);
    assert.doesNotMatch(source, /https?:\/\//, file + ' names no outside address');
    assert.doesNotMatch(source, /\bfetch\(/, file + ' reads only through cases.ts');
  }
  assert.match(read('src/lib/components/CaseTools.svelte'), /<ResourcePanel \{caseId\}/);
});

/* ------------------------------------------------------------------ copy */

test('every fixed sentence, hint, name and refusal text passes the copy lint', () => {
  const view = r.parseResource(resourceJson());
  const strings = [
    ...Object.entries(r.resourceCopy).map(([k, v]) => ({ id: 'resource.' + k, text: v })),
    ...Object.entries(r.RULE_HINTS).map(([k, v]) => ({ id: 'hint.' + k, text: v })),
    { id: 'hint.fallback', text: r.FALLBACK_HINT },
    ...Object.entries(r.METRIC_NAMES).map(([k, v]) => ({ id: 'metric.' + k, text: v })),
    ...r.PROC_METRICS.map((m) => ({ id: 'proc.' + m.id, text: m.label })),
    { id: 'summary.chart', text: r.stripsSummary(HOST, [{ id: 'a', title: 'Host CPU', unit: '%', lines: [] }], view.episodes, view.markers, { fromMs: T0, toMs: T0 + 60 * MIN }, 'UTC') },
    { id: 'coverage.some', text: r.coverageDot({ metric: 'host.cpu', fraction: 0.5 }).text },
    { id: 'coverage.none', text: r.coverageDot({ metric: 'host.cpu', fraction: 0 }).text },
    // The label is the server's own text and is shown as sent, so the panel's own words are linted around a neutral one.
    { id: 'chip.aria', text: r.chipsFor([{ ...view.rules[0], label: 'Seen in this window' }], null)[0].aria },
    ...r.STRIP_DEFS.map((d) => ({ id: 'strip.' + d.id, text: d.title })),
    ...['bad-bucket', 'bad-selector', 'too-many-series', 'too-many-buckets', 'bad-episode'].map((reason) => ({ id: 'reason.' + reason, text: importCopy.reasonText(reason) }))
  ];
  assert.ok(strings.every((entry) => typeof entry.text === 'string' && entry.text.length > 0), 'every string is present');
  assert.deepEqual(lintScreenStrings(strings, deck.terms), []);
});

test('no panel copy uses a causal word', () => {
  const causal = /\b(?:leak|leaks|leaking|starv\w*|saturat\w*|caus\w*|because|due to|waiting on|blocked by|bottleneck|root cause|culprit|responsible|result of)\b/i;
  const all = [...Object.values(r.resourceCopy), ...Object.values(r.RULE_HINTS), r.FALLBACK_HINT, ...Object.values(r.METRIC_NAMES), ...r.STRIP_DEFS.map((d) => d.title)];
  for (const text of all) assert.doesNotMatch(text, causal, text);
  for (const reason of ['bad-bucket', 'bad-selector', 'too-many-series', 'too-many-buckets', 'bad-episode']) assert.doesNotMatch(importCopy.reasonText(reason), causal, reason);
});
