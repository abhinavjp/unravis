const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The sources step of the create wizard (TSK-18): the pure model, the store with fake calls, the copy, and the shape of the components.

const root = path.resolve(__dirname, '..');
const cache = new Map();
// One shared module cache, so the store and this test see the same `wizard` object.
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const model = loadTypeScript('src/lib/wizardSources.ts');
const store = loadTypeScript('src/lib/wizardSources.svelte.ts');
const draft = loadTypeScript('src/lib/wizard.svelte.ts');
const held = loadTypeScript('src/lib/wizardConnections.ts');
const copy = loadTypeScript('src/lib/copy/wizardSourcesCopy.ts');
const help = loadTypeScript('src/lib/copy/helpCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { wizard } = draft;
const { sourcesView } = store;

let counter = 0;
function finding(over = {}) {
  counter++;
  return {
    id: 'f' + String(counter).padStart(8, '0'), detector: 'iis', kind: 'log-set', importer: 'iis-w3c', host: 'PC1', display: 'W3SVC' + counter, confidence: 0.9,
    shows: 'discover.iis.shows', how: '', sizeBytes: 10, files: 2, oldest: '', newest: '', needsAdmin: false, cost: 'small', verified: true, unreadable: '', suggest: null, ...over
  };
}
const unverified = (over = {}) => finding({ verified: false, importer: '', unreadable: 'discover.wrongformat.shows', ...over });
const blank = () => { draft.resetWizard(); };
const source = (kind, ref, extra = {}) => ({ ...model.makeSource(kind, ref, ref), ...extra });

// ---- tags

test('cleanTag trims, collapses, drops control characters and cuts to the limit', () => {
  assert.equal(model.cleanTag('  Portal   A\t\n'), 'Portal A');
  assert.equal(model.cleanTag('a\u0000b'), 'a b');
  assert.equal(model.cleanTag('x'.repeat(200)).length, model.TAG_MAX);
  assert.equal(model.cleanTag('y'.repeat(200), model.ENV_TAG_MAX).length, model.ENV_TAG_MAX);
  assert.equal(model.cleanTag(undefined), '');
});

test('text that looks like a connection string or a password is recognised', () => {
  for (const text of ['mongodb://u:p@host/db', 'password=abc', 'pwd: x', 'token=1', 'user:pw@host']) assert.equal(model.looksLikeSecret(text), true, text);
  for (const text of ['Orders', 'Portal A', 'Client A production', 'v2 API (new)']) assert.equal(model.looksLikeSecret(text), false, text);
});

test('tagProblem: blank is fine, a secret is refused, an environment must be one of step 1 when step 1 names some', () => {
  const envs = ['Client A production', 'Client D sandbox'];
  assert.equal(model.tagProblem('portal', '', envs), null);
  assert.equal(model.tagProblem('env', '', envs), null);
  assert.equal(model.tagProblem('api', 'password=x', envs), 'secret');
  assert.equal(model.tagProblem('env', 'Client Z', envs), 'unknown-env');
  assert.equal(model.tagProblem('env', 'client a PRODUCTION', envs), null);
  assert.equal(model.tagProblem('env', 'anything at all', []), null);
  assert.equal(model.tagProblem('portal', 'whatever portal', envs), null);
});

test('canonicalEnv uses the spelling of step 1', () => {
  assert.equal(model.canonicalEnv('client a production', ['Client A production']), 'Client A production');
  assert.equal(model.canonicalEnv('Other', ['Client A production']), 'Other');
});

// ---- the list of chosen sources

test('a source is added once, removed by key, and the list stops at 200', () => {
  const a = source('discover', 'one');
  const first = model.addSource([], a);
  assert.equal(first.ok, true);
  assert.deepEqual(model.addSource(first.list, a), { ok: false, reason: 'duplicate' });
  assert.equal(model.removeSource(first.list, model.sourceKey(a)).length, 0);
  let list = [];
  for (let i = 0; i < model.MAX_SOURCES; i++) list = model.addSource(list, source('discover', 'r' + i)).list;
  assert.equal(list.length, 200);
  assert.deepEqual(model.addSource(list, source('discover', 'extra')), { ok: false, reason: 'full' });
  assert.equal(model.collapseDuplicates([a, { ...a, label: 'again' }]).length, 1);
});

test('a new source starts with every tag blank', () => {
  const made = model.sourceFromFinding(finding({ display: 'W3SVC1', suggest: { portal: 'Portal A', api: 'Orders' } }));
  assert.equal(made.env, '');
  assert.equal(made.portal, '');
  assert.equal(made.api, '');
  assert.equal(made.kind, 'discover');
});

test('setTag cleans the value, keeps the other sources, and refuses a secret', () => {
  const a = source('discover', 'a');
  const b = source('discover', 'b');
  const next = model.setTag([a, b], model.sourceKey(a), 'portal', '  Portal   A ');
  assert.equal(next[0].portal, 'Portal A');
  assert.equal(next[1].portal, '');
  const refused = model.setTag(next, model.sourceKey(a), 'api', 'mongodb://u:p@h');
  assert.equal(refused[0].api, '');
  assert.equal(model.setTag([a], model.sourceKey(a), 'env', 'prod', ['Prod'])[0].env, 'Prod');
});

test('only a verified readable finding can be ticked', () => {
  const ok = finding();
  const bad = unverified();
  const steps = finding({ kind: 'export-needed', verified: false, importer: '', how: 'discover.x.how' });
  assert.equal(model.canTick(ok), true);
  assert.equal(model.canTick(bad), false);
  assert.equal(model.canTick(steps), false);
  assert.equal(model.toggleFinding([], ok, true).length, 1);
  assert.equal(model.toggleFinding([], bad, true).length, 0);
  assert.equal(model.toggleFinding([], steps, true).length, 0);
  const ticked = model.toggleFinding([], ok, true);
  assert.equal(model.toggleFinding(ticked, ok, false).length, 0);
  assert.deepEqual(model.chosenFindingIds(ticked), [ok.id]);
});

test('entryState and galleryGroups: ready first, unverified shown, steps shown, live-series and Quartz hidden', () => {
  const ready = finding({ display: 'ready' });
  const bad = unverified({ display: 'bad' });
  const steps = finding({ kind: 'export-needed', verified: false, importer: '', how: 'discover.x.how', detector: 'sql-server' });
  const live = finding({ kind: 'live-series', detector: 'procsnap', importer: '' });
  const quartz = finding({ detector: 'quartz', importer: 'quartz-log' });
  assert.equal(model.entryState(ready), 'ready');
  assert.equal(model.entryState(bad), 'unverified');
  assert.equal(model.entryState(steps), 'steps');
  assert.equal(model.canLocate(bad), true);
  assert.equal(model.canLocate(steps), false);
  assert.equal(model.canLocate(ready), false);
  const groups = model.galleryGroups([bad, steps, live, quartz, ready], ['web', 'windows', 'app', 'databases', 'live']);
  const all = groups.flatMap((g) => g.entries.map((e) => e.finding));
  assert.ok(!all.includes(live));
  assert.ok(!all.includes(quartz));
  assert.ok(all.includes(ready) && all.includes(bad) && all.includes(steps));
  const web = groups.find((g) => g.id === 'web');
  assert.equal(web.entries[0].finding, ready);
  assert.equal(web.entries[1].finding, bad);
  assert.ok(!groups.some((g) => g.id === 'live'));
});

test('a new scan drops a chosen source it no longer offers as readable, and counts it', () => {
  const keep = finding();
  const gone = finding();
  const list = [model.sourceFromFinding(keep), model.sourceFromFinding(gone), source('sql-server', 'net:sql-server:db:1433')];
  const result = model.reconcileWithScan(list, [keep, { ...gone, verified: false, importer: '' }]);
  assert.equal(result.dropped, 1);
  assert.equal(result.list.length, 2);
  assert.ok(result.list.some((s) => s.kind === 'sql-server'));
});

test('reconcileEnvs sets back an environment that step 1 no longer names; with no names nothing changes', () => {
  const a = source('discover', 'a', { env: 'Old' });
  const b = source('discover', 'b', { env: 'client a production' });
  const c = source('discover', 'c', { env: '' });
  const result = model.reconcileEnvs([a, b, c], ['Client A production']);
  assert.equal(result.cleared, 1);
  assert.equal(result.list[0].env, '');
  assert.equal(result.list[1].env, 'Client A production');
  const free = model.reconcileEnvs([a], []);
  assert.equal(free.cleared, 0);
  assert.equal(free.list[0].env, 'Old');
});

// ---- chips

test('chips offer the scan guess and names used elsewhere, never fill them in', () => {
  const a = source('discover', 'a');
  const b = source('discover', 'b', { portal: 'Portal B', api: 'Billing' });
  const chips = model.chipsFor(a, { portal: 'Portal A', api: 'Orders' }, [a, b], ['Prod', 'Test']);
  assert.deepEqual(chips.env, ['Prod', 'Test']);
  assert.deepEqual(chips.portal, ['Portal A', 'Portal B']);
  assert.deepEqual(chips.api, ['Orders', 'Billing']);
  assert.equal(a.portal, '');
  assert.equal(a.api, '');
  const filled = model.chipsFor({ ...a, portal: 'portal a', env: 'prod' }, { portal: 'Portal A', api: '' }, [a], ['Prod']);
  assert.deepEqual(filled.portal, []);
  assert.deepEqual(filled.env, []);
});

test('a suggestion that looks like a secret is never offered; a manual source has none', () => {
  const a = source('discover', 'a');
  assert.deepEqual(model.chipsFor(a, { portal: 'password=x', api: '' }, [a], []).portal, []);
  assert.equal(model.suggestionFor(source('sql-server', 'net:x'), [finding()]), null);
  const f = finding({ suggest: { portal: 'P', api: 'A' } });
  assert.deepEqual(model.suggestionFor(model.sourceFromFinding(f), [f]), { portal: 'P', api: 'A' });
});

test('tagPath joins what is set with an arrow', () => {
  assert.equal(model.tagPath({ env: 'E', portal: 'P', api: 'A' }), 'E › P › A');
  assert.equal(model.tagPath({ env: 'E', portal: '', api: 'A' }), 'E › A');
  assert.equal(model.tagPath({ env: '', portal: '', api: '' }), '');
  assert.equal(model.tagPath({ env: 'E', portal: 'P', api: '' }, false), 'P');
});

// ---- addresses

test('parseAddress reads host, host:port and [v6]:port, and uses the default port', () => {
  assert.deepEqual(model.parseAddress('db1', 1433), { ok: true, host: 'db1', port: 1433 });
  assert.deepEqual(model.parseAddress(' db1.corp.local:14330 ', 1433), { ok: true, host: 'db1.corp.local', port: 14330 });
  assert.deepEqual(model.parseAddress('[::1]:6380', 6379), { ok: true, host: '::1', port: 6380 });
  assert.deepEqual(model.parseAddress('[::1]', 6379), { ok: true, host: '::1', port: 6379 });
});

test('parseAddress reads the SQL Server host,port form only when asked', () => {
  assert.deepEqual(model.parseAddress('192.168.4.2,1655', 1433, true), { ok: true, host: '192.168.4.2', port: 1655 });
  assert.deepEqual(model.parseAddress(' db1 , 14330 ', 1433, true), { ok: true, host: 'db1', port: 14330 });
  assert.equal(model.parseAddress('192.168.4.2,1655', 1433).problem, 'bad-host');
  assert.equal(model.parseAddress('a,b,c', 1433, true).problem, 'bad-host');
  assert.equal(model.parseAddress('h,99999', 1433, true).problem, 'bad-port');
});

test('parseAddress refuses an empty text, a connection string, a bad host and a bad port', () => {
  assert.equal(model.parseAddress('', 1).problem, 'empty');
  assert.equal(model.parseAddress('   ', 1).problem, 'empty');
  for (const text of ['sqlserver://u:p@h', 'user:pw@host', 'host/path', 'redis://h:1']) assert.equal(model.parseAddress(text, 1).problem, 'secret', text);
  for (const text of ['two words', 'a:b:c', '-bad', 'bad-']) assert.equal(model.parseAddress(text, 1).problem, 'bad-host', text);
  for (const text of ['h:0', 'h:70000', 'h:x', 'h:123456']) assert.equal(model.parseAddress(text, 1).problem, 'bad-port', text);
});

test('an address ref reads back; another ref does not', () => {
  const ref = model.addressRef('sql-server', 'DB1', 1433);
  assert.deepEqual(model.parseAddressRef(ref), { kind: 'sql-server', host: 'db1', port: 1433 });
  assert.deepEqual(model.parseAddressRef(model.addressRef('redis', '::1', 6379)), { kind: 'redis', host: '::1', port: 6379 });
  assert.equal(model.parseAddressRef('mongo:abcdefgh1234/db'), null);
  assert.equal(model.parseAddressRef('net:sql-server:h:99999'), null);
  assert.equal(model.addressText('::1', 5), '[::1]:5');
});

test('reach answers are kept beside the list and flag a source without removing it', () => {
  const a = source('sql-server', 'net:sql-server:a:1433');
  const key = model.sourceKey(a);
  const reach = model.withReach({}, key, model.reachFrom({ reachable: false, reason: 'timed-out' }));
  assert.equal(model.isFlagged(reach, key), true);
  assert.equal(model.flaggedCount([a], reach), 1);
  assert.equal(model.isFlagged(model.withReach({}, key, model.reachFrom({ reachable: true, reason: '' })), key), false);
  assert.equal(model.isFlagged(model.withoutReach(reach, key), key), false);
});

test('file sources of one kind stay apart', () => {
  const one = model.fileSource('iis-logs', 'From A', model.nextFileNumber([], 'iis-logs'));
  const n = model.nextFileNumber([one], 'iis-logs');
  assert.equal(n, 2);
  assert.notEqual(model.sourceKey(one), model.sourceKey(model.fileSource('iis-logs', 'From B', n)));
});

// ---- MongoDB

test('a database becomes a source only if the test listed it and it was classified; unused removes it', () => {
  const conn = { id: 'abcdefgh1234', databases: ['audit1', 'misc'] };
  assert.deepEqual(model.classifyDatabase([], conn, 'nope', 'audit'), { ok: false, reason: 'not-tested' });
  assert.deepEqual(model.classifyDatabase([], conn, 'misc', ''), { ok: false, reason: 'needs-classification' });
  assert.deepEqual(model.classifyDatabase([], conn, 'misc', 'bogus'), { ok: false, reason: 'needs-classification' });
  const audit = model.classifyDatabase([], conn, 'audit1', 'audit');
  assert.equal(audit.ok, true);
  assert.equal(audit.list[0].ref, 'mongo:abcdefgh1234/audit1');
  assert.equal(model.databaseClass(audit.list, conn.id, 'audit1'), 'audit');
  const other = model.classifyDatabase(audit.list, conn, 'audit1', 'other');
  assert.equal(other.list.length, 1);
  assert.equal(model.databaseClass(other.list, conn.id, 'audit1'), 'other');
  assert.equal(model.auditDatabaseCount(audit.list), 1);
  assert.equal(model.auditDatabaseCount(other.list), 0);
  assert.equal(model.classifyDatabase(other.list, conn, 'audit1', 'unused').list.length, 0);
  assert.deepEqual(model.parseDatabaseRef('mongo:abcdefgh1234/a/b'), { connectionId: 'abcdefgh1234', database: 'a/b' });
  assert.equal(model.parseDatabaseRef('net:redis:x:1'), null);
});

test('removing a connection drops only its databases', () => {
  const conn = { id: 'abcdefgh1234', databases: ['d'] };
  const list = model.classifyDatabase([source('sql-server', 'net:sql-server:a:1')], conn, 'd', 'audit').list;
  assert.equal(list.length, 2);
  assert.deepEqual(model.removeConnectionSources(list, conn.id).map((s) => s.kind), ['sql-server']);
});

test('hostLabel shows hosts only, never the user name or password', () => {
  assert.equal(model.hostLabel('mongodb://admin:s3cret@db1:27017/x?y=1'), 'db1:27017');
  assert.equal(model.hostLabel('mongodb+srv://u:p%40ss@cluster.example.net/db'), 'cluster.example.net');
  assert.equal(model.hostLabel('mongodb://u:p@a:1,b:2,c:3/db'), 'a:1 and 2 more');
  assert.equal(model.hostLabel('db1'), 'db1');
  assert.equal(model.hostLabel('mongodb://u:p/ss@host/db'), 'host');
  assert.ok(!model.hostLabel('mongodb://admin:s3cret@db1').includes('s3cret'));
});

test('redactMessage removes the typed secrets and any address', () => {
  const out = model.redactMessage('login failed for hunter22 at mongodb://u:p@h/db', ['hunter22']);
  assert.ok(!out.includes('hunter22'));
  assert.ok(!out.includes('mongodb://'));
});

// ---- the store, with fake calls

const readyClient = (scan, calls = []) => ({
  capabilities: async () => ({ supported: true, elevated: false, os: 'windows' }),
  startScan: async () => { calls.push('start'); return 'a'.repeat(32); },
  getScan: async () => scan,
  cancelScan: async () => { calls.push('cancel'); }
});
const doneScan = (findings, over = {}) => ({ state: 'done', startedAt: new Date().toISOString(), findings, gaps: [], host: { label: 'PC1', proposed: false, tz: '', cores: 4, aliases: [] }, progress: [], ...over });

test('startScan keeps the findings; only a verified one can be ticked; a tick is a source with blank tags', async () => {
  blank();
  const ok = finding({ suggest: { portal: 'Portal A', api: 'Orders' } });
  const bad = unverified();
  await store.startScan({ client: readyClient(doneScan([ok, bad])) });
  assert.equal(sourcesView.phase, 'done');
  assert.equal(sourcesView.findings.length, 2);
  assert.equal(store.tickFinding(bad.id, true), false);
  assert.equal(wizard.sources.length, 0);
  assert.equal(store.tickFinding(ok.id, true), true);
  assert.equal(wizard.sources.length, 1);
  assert.equal(wizard.sources[0].portal, '');
  assert.equal(wizard.sources[0].api, '');
  assert.equal(store.tickFinding(ok.id, false), false);
  assert.equal(wizard.sources.length, 0);
});

test('a cancelled or failed scan says so in plain words and leaves the list alone', async () => {
  blank();
  await store.startScan({ client: readyClient(doneScan([], { state: 'cancelled' })) });
  assert.equal(sourcesView.phase, 'idle');
  assert.notEqual(sourcesView.info, '');
  await store.startScan({ client: readyClient(doneScan([], { state: 'failed' })) });
  assert.notEqual(sourcesView.problem, '');
  assert.equal(sourcesView.findings.length, 0);
});

test('a second scan drops a chosen source the new scan cannot read, and says how many', async () => {
  blank();
  const ok = finding();
  await store.startScan({ client: readyClient(doneScan([ok])) });
  store.tickFinding(ok.id, true);
  await store.startScan({ client: readyClient(doneScan([{ ...ok, verified: false, importer: '' }])) });
  assert.equal(wizard.sources.length, 0);
  assert.match(sourcesView.info, /1/);
});

test('locateSource replaces the finding with the verified one, which can then be ticked', async () => {
  blank();
  const bad = unverified();
  await store.startScan({ client: readyClient(doneScan([bad])) });
  const fixed = { ...bad, verified: true, importer: 'iis-w3c', unreadable: '' };
  const seen = [];
  const outcome = await store.locateSource(bad.id, 'D:\\logs', { locate: async (scanId, findingId, p) => { seen.push([scanId, findingId, p]); return { ok: true, files: 3, finding: fixed }; } });
  assert.deepEqual(outcome, { ok: true, files: 3 });
  assert.equal(seen[0][2], 'D:\\logs');
  assert.equal(store.tickFinding(bad.id, true), true);
  const refused = await store.locateSource(bad.id, 'D:\\x', { locate: async () => ({ ok: false, files: 0, reason: 'no-log-files' }) });
  assert.equal(refused.ok, false);
  assert.equal(refused.reason, 'no-log-files');
  const thrown = await store.locateSource(bad.id, 'D:\\x', { locate: async () => { throw new Error('boom'); } });
  assert.equal(thrown.ok, false);
});

test('an address is added even when it cannot be reached, and is flagged', async () => {
  blank();
  const outcome = await store.addByAddress('sql-server', 'db1', { probe: async () => ({ reachable: false, reason: 'timed-out' }) });
  assert.equal(outcome.ok, true);
  assert.equal(outcome.reached, false);
  assert.equal(wizard.sources.length, 1);
  assert.equal(wizard.sources[0].ref, 'net:sql-server:db1:1433');
  assert.equal(model.isFlagged(sourcesView.reach, outcome.key), true);
  assert.equal(store.addressOfSource(wizard.sources[0].ref), 'db1:1433');
  assert.equal(await store.checkReach(outcome.key, { probe: async () => ({ reachable: true, reason: '' }) }), true);
  assert.equal(model.isFlagged(sourcesView.reach, outcome.key), false);
  const twice = await store.addByAddress('sql-server', 'DB1:1433', { probe: async () => ({ reachable: true, reason: '' }) });
  assert.deepEqual(twice, { ok: false, problem: 'duplicate' });
  store.removeChosen(outcome.key);
  assert.equal(wizard.sources.length, 0);
  assert.equal(sourcesView.reach[outcome.key], undefined);
});

test('a probe that fails to answer still keeps the source, flagged', async () => {
  blank();
  const outcome = await store.addByAddress('redis', 'cache1', { probe: async () => { throw new Error('offline'); } });
  assert.equal(outcome.ok, true);
  assert.equal(outcome.reached, null);
  assert.equal(wizard.sources.length, 1);
  assert.equal(model.isFlagged(sourcesView.reach, outcome.key), true);
});

test('a bad address is refused with a reason and adds nothing', async () => {
  blank();
  assert.equal((await store.addByAddress('redis', 'redis://u:p@h', { probe: async () => assert.fail('no probe') })).problem, 'secret');
  assert.equal((await store.addByAddress('redis', '', { probe: async () => assert.fail('no probe') })).problem, 'empty');
  assert.equal(wizard.sources.length, 0);
});

test('addFile adds a file source, a second of the same kind is a second source', () => {
  blank();
  assert.equal(store.addFile('iis-logs', 'From PC 2').ok, true);
  assert.equal(store.addFile('iis-logs', 'From PC 3').ok, true);
  assert.equal(wizard.sources.length, 2);
  assert.notEqual(wizard.sources[0].ref, wizard.sources[1].ref);
});

const mongoInput = { uri: 'mongodb://admin:hunter22@db1:27017', username: 'admin', password: 'hunter22', authSource: '', tlsMode: 'uri', caCertificate: '' };

test('a tested Mongo connection is held by id; the draft never carries the password or the address', async () => {
  blank();
  const outcome = await store.testMongo(mongoInput, '', { testConnection: async () => ({ databases: ['audit1', 'misc'] }) });
  assert.equal(outcome.ok, true);
  assert.equal(sourcesView.connections.length, 1);
  assert.equal(sourcesView.connections[0].host, 'db1:27017');
  assert.equal(held.heldConnection(outcome.id).password, 'hunter22');
  assert.equal(store.classifyMongo(outcome.id, 'audit1', 'audit'), null);
  assert.equal(store.classifyMongo(outcome.id, 'misc', 'other'), null);
  assert.equal(store.classifyMongo(outcome.id, 'ghost', 'audit'), 'not-tested');
  assert.equal(store.classifyMongo(outcome.id, 'misc', ''), 'needs-classification');
  assert.equal(store.classifyMongo('unknownid1234', 'misc', 'audit'), 'not-tested');
  assert.equal(wizard.sources.length, 2);
  const text = JSON.stringify(wizard.sources) + JSON.stringify(sourcesView);
  assert.ok(!text.includes('hunter22'));
  assert.ok(!text.includes('mongodb://'));
  assert.equal(store.classifyMongo(outcome.id, 'misc', 'unused'), null);
  assert.equal(wizard.sources.length, 1);
  store.removeConnection(outcome.id);
  assert.equal(wizard.sources.length, 0);
  assert.equal(held.heldConnection(outcome.id), null);
  assert.equal(sourcesView.connections.length, 0);
});

test('a failed Mongo test says it plainly without the typed secrets, and holds nothing', async () => {
  blank();
  const outcome = await store.testMongo(mongoInput, '', { testConnection: async () => { throw new Error('auth failed for admin hunter22 at mongodb://admin:hunter22@db1'); } });
  assert.equal(outcome.ok, false);
  assert.ok(!outcome.text.includes('hunter22'));
  assert.ok(!outcome.text.includes('mongodb://'));
  assert.equal(sourcesView.connections.length, 0);
  assert.equal(held.heldCount(), 0);
});

test('testing a connection again refreshes its list and drops a database that is gone', async () => {
  blank();
  const first = await store.testMongo(mongoInput, '', { testConnection: async () => ({ databases: ['a', 'b'] }) });
  store.classifyMongo(first.id, 'a', 'audit');
  store.classifyMongo(first.id, 'b', 'audit');
  const again = await store.testMongo(held.heldConnection(first.id), first.id, { testConnection: async () => ({ databases: ['a'] }) });
  assert.equal(again.id, first.id);
  assert.equal(sourcesView.connections.length, 1);
  assert.deepEqual(wizard.sources.map((s) => s.label), ['a']);
});

test('reconcileEnvironments sets back an environment step 1 no longer names', () => {
  blank();
  wizard.envText = 'Prod';
  draft.setSources([source('discover', 'a', { env: 'Old' }), source('discover', 'b', { env: 'prod' })]);
  assert.equal(store.reconcileEnvironments(), 1);
  assert.equal(wizard.sources[0].env, '');
  assert.equal(wizard.sources[1].env, 'Prod');
  assert.notEqual(sourcesView.info, '');
});

test('setSourceTag refuses a value that looks like a password and keeps the old tag', () => {
  blank();
  draft.setSources([source('discover', 'a', { portal: 'Portal A' })]);
  const key = model.sourceKey(wizard.sources[0]);
  assert.equal(store.setSourceTag(key, 'portal', 'password=x'), false);
  assert.equal(wizard.sources[0].portal, 'Portal A');
  assert.equal(store.setSourceTag(key, 'portal', ' Portal B '), true);
  assert.equal(wizard.sources[0].portal, 'Portal B');
});

test('resetWizard clears the draft, the scan, the checks and the held connections', async () => {
  blank();
  await store.startScan({ client: readyClient(doneScan([finding()])) });
  await store.addByAddress('redis', 'cache1', { probe: async () => ({ reachable: false, reason: 'refused' }) });
  await store.testMongo(mongoInput, '', { testConnection: async () => ({ databases: ['a'] }) });
  assert.ok(held.heldCount() > 0);
  draft.resetWizard();
  assert.equal(wizard.sources.length, 0);
  assert.equal(sourcesView.findings.length, 0);
  assert.equal(sourcesView.phase, 'idle');
  assert.deepEqual(sourcesView.reach, {});
  assert.equal(sourcesView.connections.length, 0);
  assert.equal(held.heldCount(), 0);
});

test('the scan is stale after an hour', async () => {
  blank();
  await store.startScan({ client: readyClient(doneScan([finding()], { startedAt: new Date(Date.now() - 2 * 3600 * 1000).toISOString() })) });
  assert.equal(store.scanIsStale(), true);
  await store.startScan({ client: readyClient(doneScan([finding()])) });
  assert.equal(store.scanIsStale(), false);
});

// ---- copy

test('the sources copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(copy.wizardSourcesCopyEntries(), deck.terms), []);
});

test('the copy builds its sentences and never echoes a connection string', () => {
  assert.match(copy.yourSourcesText(3), /3/);
  assert.match(copy.reachedText('db1:1433'), /db1:1433/);
  assert.match(copy.unreachedText('db1:1433', 'timed-out'), /db1:1433/);
  assert.equal(copy.kindLabel('nonsense'), copy.KIND_LABELS.file);
  const safe = copy.safeServerLine('bad login hunter22 mongodb://u:p@h', ['hunter22']);
  assert.ok(!safe.includes('hunter22') && !safe.includes('mongodb://'));
  assert.equal(copy.safeServerLine('', []), copy.SOURCES_TEXT.mongoFailed);
});

test('the step has a help entry with a plain one-sentence line', () => {
  const entry = help.help['wizard-sources'];
  assert.ok(entry);
  assert.notEqual(entry.plain, entry.simple);
  assert.equal(entry.plain.split(/[.!?]\s/).length, 1);
});

// ---- the components

const COMPONENTS = ['SourceGallery', 'SourceTagRow', 'LocateFolder', 'ReachableCheck', 'MongoClassify'];

test('no component stores anything in the browser', () => {
  for (const name of COMPONENTS) {
    const text = read('src/components/wizard/' + name + '.svelte');
    assert.doesNotMatch(text, /localStorage|sessionStorage|indexedDB|document\.cookie/, name);
    assert.doesNotMatch(text, /console\.(log|info|debug)/, name);
  }
});

test('only a verified source has an enabled checkbox', () => {
  const text = read('src/components/wizard/SourceGallery.svelte');
  assert.match(text, /type="checkbox"[^>]*disabled=\{entry\.state !== 'ready'\}/s);
  assert.match(text, /<LocateFolder/);
});

test('the help note is used once, in the gallery', () => {
  const uses = COMPONENTS.map((name) => (read('src/components/wizard/' + name + '.svelte').match(/<HelpNote id="wizard-sources"/g) ?? []).length);
  assert.deepEqual(uses, [1, 0, 0, 0, 0]);
});

test('chips are buttons that set a tag; nothing fills a tag in on its own', () => {
  const text = read('src/components/wizard/SourceTagRow.svelte');
  assert.match(text, /<button type="button" class="chip"[^>]*onclick=\{\(\) => useChip\(/);
  assert.match(text, /function useChip[^}]*setSourceTag/s);
  assert.doesNotMatch(text, /\$effect/);
  assert.match(text, /chipsFor/);
});

test('the Mongo password field is a password input that is cleared after a test', () => {
  const text = read('src/components/wizard/MongoClassify.svelte');
  assert.match(text, /type="password"/);
  assert.match(text, /password = ''/);
  assert.doesNotMatch(text, /(?<!bind:value=)\{(?:password|uri)\}/);
});

test('Enter in a step field does not move the wizard on', () => {
  for (const name of ['SourceTagRow', 'LocateFolder', 'ReachableCheck', 'MongoClassify']) {
    assert.match(read('src/components/wizard/' + name + '.svelte'), /stopPropagation/, name);
  }
});

test('the landing screen mounts the gallery in the create wizard', () => {
  assert.match(read('src/components/Landing.svelte'), /<CreateWizard onLeave=\{goHome\} busy=\{[^}]+\}>\s*\{#snippet sources\(\)\}<SourceGallery \/>\{\/snippet\}/);
});

test('the store keeps no password in the shared draft model', () => {
  const text = read('src/lib/wizardSources.svelte.ts') + read('src/lib/wizardSources.ts');
  assert.doesNotMatch(text, /localStorage|sessionStorage/);
  assert.doesNotMatch(text, /console\./);
});

test('a tested MongoDB connection holds its audit collection, and a blank one means ApiAuditLog', async () => {
  blank();
  const first = await store.testMongo(mongoInput, '', { testConnection: async () => ({ databases: ['a'] }) }, ' Audit2 ');
  assert.equal(held.heldCollection(first.id), 'Audit2');
  const second = await store.testMongo(mongoInput, '', { testConnection: async () => ({ databases: ['a'] }) });
  assert.equal(held.heldCollection(second.id), 'ApiAuditLog');
  held.releaseConnection(first.id);
  assert.equal(held.heldCollection(first.id), 'ApiAuditLog');
});

test('a case made by the create guide adopts its new sources and starts the analysis', () => {
  const state = fs.readFileSync(path.join(root, 'src/lib/appState.svelte.ts'), 'utf8');
  const body = state.slice(state.indexOf('export async function openCreatedCase'));
  const fn = body.slice(0, body.indexOf('\n}\n'));
  assert.match(fn, /api\.sources\(\)/, 'reloads the source list the guide changed on the server');
  assert.match(fn, /query\.sourceIds = /, 'selects the new sources');
  assert.match(fn, /autoRan = false/, 'lets the landing run start by itself');
  assert.match(fn, /!hasSources\) openSidePanel\('import'\)/, 'opens the import drawer only when there is nothing to analyse');
  const landing = fs.readFileSync(path.join(root, 'src/components/Landing.svelte'), 'utf8');
  assert.match(landing, /openCreatedCase\(id\)/);
});

/* ------------------------------------------------------------------ SQL Server form (SSMS-style, stored only) */

const sqlForm = loadTypeScript('src/lib/sqlConnection.ts');

test('sql form: the SSMS details become one request, and a password goes only when asked to be remembered', () => {
  const form = { ...sqlForm.blankSqlForm(), server: ' 192.168.4.2,1655 ', username: ' sa ', password: 'secret', database: ' ', trustServerCertificate: true };
  const off = sqlForm.sqlInput(form);
  assert.equal(off.server, '192.168.4.2,1655');
  assert.equal(off.username, 'sa');
  assert.equal(off.password, '');
  assert.equal(off.rememberPassword, false);
  assert.equal(off.encrypt, 'mandatory');
  assert.equal(off.trustServerCertificate, true);
  const on = sqlForm.sqlInput({ ...form, rememberPassword: true });
  assert.equal(on.password, 'secret');
  assert.equal(on.rememberPassword, true);
});

test('sql form: Windows login sends no user name or password', () => {
  const body = sqlForm.sqlInput({ ...sqlForm.blankSqlForm(), server: 'db1', auth: 'windows', username: 'x', password: 'y', rememberPassword: true });
  assert.equal(body.username, '');
  assert.equal(body.password, '');
  assert.equal(body.rememberPassword, false);
});

test('sql form: a missing server or login is named before anything is sent', () => {
  assert.equal(sqlForm.sqlFormProblem(sqlForm.blankSqlForm()), 'server');
  assert.equal(sqlForm.sqlFormProblem({ ...sqlForm.blankSqlForm(), server: 'db1' }), 'username');
  assert.equal(sqlForm.sqlFormProblem({ ...sqlForm.blankSqlForm(), server: 'db1', auth: 'windows' }), null);
  assert.equal(sqlForm.sqlFormProblem({ ...sqlForm.blankSqlForm(), server: 'db1', username: 'sa' }), null);
});

test('sql form: choosing a kept entry fills everything but the password', () => {
  const form = sqlForm.formFromSaved({ id: 'a', label: '192.168.4.2,1655', server: '192.168.4.2,1655', host: '192.168.4.2', port: 1655, auth: 'sql', username: 'sa', database: 'Audit', encrypt: 'strict', trustServerCertificate: true, hasPassword: true, savedAt: '' });
  assert.equal(form.server, '192.168.4.2,1655');
  assert.equal(form.label, '');
  assert.equal(form.password, '');
  assert.equal(form.rememberPassword, true);
  assert.equal(form.encrypt, 'strict');
});

test('sql form: the component adds the server through the address path and never puts a password in the draft', () => {
  const component = read('src/components/wizard/SqlServerForm.svelte');
  assert.match(component, /addByAddress\('sql-server', shown\)/);
  assert.match(component, /api\.saveSqlConnection\(sqlInput\(form\)\)/);
  assert.match(component, /type="password"/);
  assert.doesNotMatch(component, /wizard\.sources|setSources/);
  assert.match(read('src/components/wizard/SourceGallery.svelte'), /<SqlServerForm \/>/);
});

test('sql form: its copy passes the screen-text lint', () => {
  const keys = Object.keys(copy.SOURCES_TEXT).filter((k) => k.startsWith('sql'));
  assert.ok(keys.length >= 20);
  const problems = lintScreenStrings(keys.map((k) => ({ id: 'wizard.sources.' + k, text: copy.SOURCES_TEXT[k] })));
  assert.deepEqual(problems, []);
});

/* ------------------------------------------------------------------ SQL test request, Mongo checks hint, Health checks */

test('sql test: a typed password always wins, and a kept one is used only while the login is unchanged', () => {
  const kept = sqlForm.formFromSaved({ id: 'k1', label: 'x', server: 'db1', host: 'db1', port: 1433, auth: 'sql', username: 'sa', database: '', encrypt: 'mandatory', trustServerCertificate: false, hasPassword: true, savedAt: '' });
  assert.equal(sqlForm.sqlTestInput(kept).savedId, 'k1');
  assert.equal(sqlForm.usesKeptPassword(kept), true);
  assert.equal(sqlForm.sqlTestInput({ ...kept, password: 'typed' }).savedId, undefined);
  assert.equal(sqlForm.sqlTestInput({ ...kept, password: 'typed' }).password, 'typed');
  for (const change of [{ server: 'other' }, { username: 'root' }, { auth: 'windows' }]) {
    assert.equal(sqlForm.sqlTestInput({ ...kept, ...change }).savedId, undefined, JSON.stringify(change));
  }
  const windows = sqlForm.sqlTestInput({ ...kept, auth: 'windows', username: 'x', password: 'y' });
  assert.equal(windows.username, '');
  assert.equal(windows.password, '');
});

test('sql test: a change to any connection field makes an earlier database list stale', () => {
  const base = { ...sqlForm.blankSqlForm(), server: 'db1', username: 'sa' };
  const key = sqlForm.sqlConnectionKey(base);
  assert.equal(sqlForm.sqlConnectionKey({ ...base, database: 'Other', password: 'p', label: 'l' }), key);
  for (const change of [{ server: 'db2' }, { username: 'x' }, { encrypt: 'optional' }, { trustServerCertificate: true }, { auth: 'windows' }]) {
    assert.notEqual(sqlForm.sqlConnectionKey({ ...base, ...change }), key, JSON.stringify(change));
  }
});

test('mongo wizard: the issue-checks hint points to Health checks and says nothing runs by itself', () => {
  assert.match(copy.SOURCES_TEXT.mongoChecksHint, /Health checks/);
  assert.match(copy.SOURCES_TEXT.mongoChecksHint, /until you press Run checks/);
  assert.match(read('src/components/wizard/MongoClassify.svelte'), /SOURCES_TEXT\.mongoChecksHint/);
});

const health = loadTypeScript('src/lib/health.ts');
const healthCopy = loadTypeScript('src/lib/copy/healthCopy.ts');

test('health: a report from the server is read defensively', () => {
  assert.equal(health.toReport(null), null);
  assert.equal(health.toReport('x'), null);
  const report = health.toReport({ kind: 'mongo', label: 'L', findings: [{ id: 'a', title: 'T', status: 'weird', rows: [[1, 'x']] }, null, 5], overall: 'bad' });
  assert.equal(report.kind, 'mongo');
  assert.equal(report.overall, 'bad');
  assert.equal(report.findings.length, 1);
  assert.equal(report.findings[0].status, 'unavailable');
  assert.deepEqual(report.findings[0].rows, [['', 'x']]);
  assert.equal(health.toRun(undefined).id, 0);
  assert.equal(health.toRun({ id: 3, kind: 'x', status: 'ok' }).kind, 'sql');
});

test('health: findings that need a look come first and the counts add up', () => {
  const f = (id, status) => ({ id, title: id, status, summary: '', why: '', columns: [], rows: [] });
  const report = { kind: 'sql', label: '', database: '', version: '', startedAt: '', finishedAt: '', overall: 'bad', findings: [f('a', 'ok'), f('b', 'unavailable'), f('c', 'bad'), f('d', 'note'), f('e', 'warn'), f('f', 'bad')] };
  assert.deepEqual(health.sortedFindings(report).map((x) => x.id), ['c', 'f', 'e', 'd', 'a', 'b']);
  assert.deepEqual(health.countByStatus(report), { ok: 1, note: 1, warn: 1, bad: 2, unavailable: 1 });
});

test('health: the panel runs checks only on a press and sends no free-text query', () => {
  const panel = read('src/lib/components/HealthPanel.svelte');
  assert.match(panel, /onclick=\{run\}/);
  assert.doesNotMatch(panel, /\$effect/);
  assert.match(read('src/lib/components/CaseTools.svelte'), /<HealthPanel \{caseId\} \{onGone\} \/>/);
  assert.match(panel, /type="password"/);
});

test('health: its copy passes the screen-text lint', () => {
  assert.deepEqual(lintScreenStrings(healthCopy.healthCopyEntries()), []);
  assert.deepEqual(lintScreenStrings([{ id: 'wizard.sources.mongoChecksHint', text: copy.SOURCES_TEXT.mongoChecksHint }]), []);
});
