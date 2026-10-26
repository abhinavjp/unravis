const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The case workspace: what a case saves about its MongoDB connection and filters, and how opening the case brings them back.

const root = path.resolve(__dirname, '..');
const cache = new Map();
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

const W = loadTypeScript('src/lib/caseWorkspace.ts');
const S = loadTypeScript('src/lib/sourceSaved.ts');
const time = loadTypeScript('src/lib/time.ts');
const copy = loadTypeScript('src/lib/copy/caseRestoreCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const src = (id, database, extra = {}) => ({ id, label: database, database, collection: 'ApiAuditLog', selectionKey: 'key-' + database, connectionLabel: 'prod', ...extra });
const recent = (id, extra = {}) => ({ id, label: 'prod', uri: 'mongodb://db.example:27017', username: 'reader', authSource: 'admin', tlsMode: 'system', caCertificate: '', collection: 'ApiAuditLog', hasCredentials: true, lastUsedAt: '', ...extra });
const queryFor = (ids) => time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ids);

function build(sources, selected, extra = {}) {
  return W.buildWorkspace({
    query: queryFor(selected ?? sources.map((s) => s.id)), sources, zone: 'UTC', targetMs: null, intent: null,
    savedIdOf: () => 'rec1', previous: null, ...extra
  });
}

// ---- the forbidden-key guard ----

test('the guard finds every key the server refuses, at any depth, however it is spelled', () => {
  for (const key of ['password', 'Passwd', 'SECRET', 'token', 'uri', 'URL', 'host', 'username', 'User_Name', 'user', 'connection-string', 'connectionString', 'caCertificate', 'ca_certificate', 'api-key', 'apiKey',
    'pwd', 'pass', 'hostname', 'Host Name', 'address', 'credential', 'Credentials', 'auth', 'authSource', 'Auth.Source', 'auth_source', 'cert', 'certificate', 'privateKey', 'private key']) {
    assert.equal(W.findForbiddenKey({ a: [{ b: { [key]: 1 } }] }), key, key);
  }
  assert.equal(W.findForbiddenKey({ databases: [{ name: 'orders', selectionKey: 'k' }], userIds: [1], urlContains: 'x', passes: 2, authors: 1 }), null);
  assert.ok(W.findForbiddenKey({ 'Foo Bar': 1, 'foo_bar': 2 }), 'two keys that spell the same once normalised are refused');
  let deep = {};
  for (let i = 0; i < 30; i++) deep = { n: deep };
  assert.ok(W.findForbiddenKey(deep), 'a very deep value counts as refused');
});

test('a built workspace never holds a password, address, host or user name', () => {
  const workspace = build([src('a', 'orders'), src('b', 'billing')]);
  assert.equal(W.findForbiddenKey(workspace), null);
  const text = JSON.stringify(workspace);
  assert.doesNotMatch(text, /mongodb:|db\.example|reader|password/i);
  assert.deepEqual(workspace.mongo, { savedConnectionId: 'rec1', databases: [{ name: 'orders', selectionKey: 'key-orders', collection: 'ApiAuditLog' }, { name: 'billing', selectionKey: 'key-billing', collection: 'ApiAuditLog' }] });
});

// ---- build and parse ----

test('build then parse gives the same workspace back', () => {
  const workspace = build([src('a', 'orders'), src('b', 'billing')], ['b']);
  const back = W.parseWorkspace(JSON.parse(JSON.stringify(workspace)));
  assert.deepEqual(back, workspace);
  assert.equal(back.v, 1);
  assert.deepEqual(back.investigation.selectedSources.map((item) => item.selectionKey), ['key-billing']);
});

test('only the connection that owns a selected source is kept, and sources with no known saved connection are left out', () => {
  const sources = [src('a', 'orders'), src('b', 'billing'), src('c', 'extra')];
  const ids = { a: 'rec1', b: 'rec2', c: '' };
  const workspace = build(sources, ['b'], { savedIdOf: (s) => ids[s.id] });
  assert.equal(workspace.mongo.savedConnectionId, 'rec2');
  assert.deepEqual(workspace.mongo.databases.map((d) => d.name), ['billing']);
});

test('with no source that names its connection, the connection already on record stays', () => {
  const previous = W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'old', databases: [{ name: 'orders' }] } });
  const kept = build([], [], { savedIdOf: () => '', previous });
  assert.deepEqual(kept.mongo, { savedConnectionId: 'old', databases: [{ name: 'orders' }] });
  assert.equal(build([], [], { savedIdOf: () => '' }).mongo, undefined);
});

test('parse never throws and drops what it cannot use', () => {
  for (const garbage of [null, undefined, 0, 'x', [], [1, 2], true, () => 1]) assert.equal(W.parseWorkspace(garbage), null);
  assert.deepEqual(W.parseWorkspace({}), { v: 1 });
  assert.equal(W.parseWorkspace({ v: 2, mongo: { savedConnectionId: 'a', databases: [{ name: 'x' }] } }), null, 'a newer version is not guessed at');
  assert.deepEqual(W.parseWorkspace({ v: 1, mongo: 'nope', investigation: 12 }), { v: 1 });
  assert.deepEqual(W.parseWorkspace({ v: 1, mongo: { savedConnectionId: '', databases: [{ name: 'x' }] } }), { v: 1 });
  assert.deepEqual(W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'a', databases: [{ name: 5 }, null, { name: '' }, 'z'] } }), { v: 1 });
  const bounded = W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'a', databases: Array.from({ length: 30 }, (_, i) => ({ name: 'db' + i, selectionKey: 'k' + i })) } });
  assert.equal(bounded.mongo.databases.length, W.MAX_WORKSPACE_DATABASES);
  const duplicated = W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'a', databases: [{ name: 'x' }, { name: 'x' }] } });
  assert.equal(duplicated.mongo.databases.length, 1);
  const cyclic = { v: 1 }; cyclic.investigation = cyclic;
  assert.doesNotThrow(() => W.parseWorkspace(cyclic));
  const badInvestigation = W.parseWorkspace({ v: 1, investigation: { version: 1, query: 7 } });
  assert.equal(badInvestigation.investigation, undefined);
});

test('removing a database from the kept workspace drops it, and the connection with its last one', () => {
  const workspace = { v: 1, mongo: { savedConnectionId: 'a', databases: [{ name: 'orders', selectionKey: 'k1' }, { name: 'billing', selectionKey: 'k2' }] } };
  const one = W.pruneWorkspace(workspace, { database: 'orders', selectionKey: 'k1' });
  assert.deepEqual(one.mongo.databases, [{ name: 'billing', selectionKey: 'k2' }]);
  const none = W.pruneWorkspace(one, { database: 'billing', selectionKey: 'k2' });
  assert.equal(none.mongo, undefined);
  assert.equal(W.pruneWorkspace(null, { database: 'x' }), null);
});

// ---- saving ----

test('saving is off in the demo, with no case, before the app is ready, while restoring, and for a case that was not restored', () => {
  const ok = { demo: false, caseId: 'c1', ready: true, restoring: false, restoredCaseId: 'c1' };
  assert.equal(W.workspaceSaveAllowed(ok), true);
  assert.equal(W.workspaceSaveAllowed({ ...ok, demo: true }), false);
  assert.equal(W.workspaceSaveAllowed({ ...ok, caseId: '', restoredCaseId: '' }), false);
  assert.equal(W.workspaceSaveAllowed({ ...ok, ready: false }), false);
  assert.equal(W.workspaceSaveAllowed({ ...ok, restoring: true }), false);
  assert.equal(W.workspaceSaveAllowed({ ...ok, restoredCaseId: 'c0' }), false);
});

function fakeTimers() {
  let next = 1; const pending = new Map();
  return {
    setTimer: (run, ms) => { const id = next++; pending.set(id, { run, ms }); return id; },
    clearTimer: (id) => { pending.delete(id); },
    get count() { return pending.size; },
    fire() { const all = [...pending.values()]; pending.clear(); all.forEach((entry) => entry.run()); },
    delays: () => [...pending.values()].map((entry) => entry.ms)
  };
}
const settle = () => new Promise((resolve) => setImmediate(resolve));

test('the saver waits, and the last write wins', async () => {
  const timers = fakeTimers(); const sent = [];
  const saver = W.createWorkspaceSaver({ send: async (id, ws) => { sent.push([id, ws]); }, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  saver.schedule('c1', { v: 1, n: 1 }); saver.schedule('c1', { v: 1, n: 2 }); saver.schedule('c1', { v: 1, n: 3 });
  assert.equal(timers.count, 1, 'one timer, not three');
  assert.deepEqual(timers.delays(), [W.SAVE_DELAY_MS]);
  assert.equal(sent.length, 0, 'nothing is sent before the pause ends');
  timers.fire(); await settle();
  assert.deepEqual(sent, [['c1', { v: 1, n: 3 }]]);
});

test('the saver skips a workspace equal to the one last sent, and sends again once it differs', async () => {
  const timers = fakeTimers(); const sent = [];
  const saver = W.createWorkspaceSaver({ send: async (id, ws) => { sent.push(ws); }, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  saver.schedule('c1', { v: 1 }); timers.fire(); await settle();
  saver.schedule('c1', { v: 1 }); timers.fire(); await settle();
  assert.equal(sent.length, 1);
  saver.schedule('c1', { v: 1, investigation: { x: 1 } }); timers.fire(); await settle();
  assert.equal(sent.length, 2);
  saver.forget(); saver.schedule('c1', { v: 1, investigation: { x: 1 } }); timers.fire(); await settle();
  assert.equal(sent.length, 3, 'forget makes the next save always go');
});

test('the saver asks again when it fires: a save queued before a restore or a case change is dropped', async () => {
  const timers = fakeTimers(); const sent = []; let allowed = true;
  const saver = W.createWorkspaceSaver({ send: async (id, ws) => { sent.push(ws); }, allowed: () => allowed, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  saver.schedule('c1', { v: 1 }); allowed = false; timers.fire(); await settle();
  assert.equal(sent.length, 0);
  allowed = true; saver.schedule('c1', { v: 1 }); saver.cancel(); assert.equal(timers.count, 0); assert.equal(saver.waiting, false);
});

test('flush sends what waits now; no case means nothing is scheduled', async () => {
  const timers = fakeTimers(); const sent = [];
  const saver = W.createWorkspaceSaver({ send: async (id, ws) => { sent.push([id, ws]); }, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  saver.schedule('', { v: 1 }); assert.equal(timers.count, 0);
  saver.schedule('c1', { v: 1, n: 1 }); saver.flush(); await settle();
  assert.deepEqual(sent, [['c1', { v: 1, n: 1 }]]);
  assert.equal(timers.count, 0);
});

test('a failed save is ignored quietly, then tried again on the next change', async () => {
  const timers = fakeTimers(); let calls = 0; let told = 0;
  const saver = W.createWorkspaceSaver({
    send: async () => { calls++; throw new Error('503'); }, sent: () => { told++; }, setTimer: timers.setTimer, clearTimer: timers.clearTimer
  });
  saver.schedule('c1', { v: 1 }); assert.doesNotThrow(() => timers.fire()); await settle();
  saver.schedule('c1', { v: 1 }); timers.fire(); await settle();
  assert.equal(calls, 2, 'the same workspace is sent again because the first never went through');
  assert.equal(told, 0);
  const sync = W.createWorkspaceSaver({ send: () => { throw new Error('sync'); }, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  sync.schedule('c1', { v: 1 }); assert.doesNotThrow(() => timers.fire());
});

// ---- restoring ----

const plan = (over) => W.planRestore({ workspace: W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'rec1', databases: [{ name: 'orders', selectionKey: 'key-orders' }] } }), sources: [], recents: [recent('rec1')], sessionIds: new Set(), ...over });

test('restore: a case with no MongoDB connection has nothing to reconnect', () => {
  assert.equal(plan({ workspace: { v: 1 } }).kind, 'none');
  assert.equal(plan({ workspace: null }).kind, 'none');
});

test('restore: databases that are already connected need nothing', () => {
  assert.equal(plan({ sources: [src('a', 'orders')] }).kind, 'connected');
  assert.equal(plan({ sources: [{ id: 'z', database: 'orders' }] }).kind, 'connected', 'matched by name when a key is missing');
});

test('restore with stored credentials reconnects from the saved connection', () => {
  const result = plan({});
  assert.equal(result.kind, 'connect');
  assert.equal(result.recent.id, 'rec1');
  assert.deepEqual(result.databases, [{ name: 'orders', selectionKey: 'key-orders' }]);
  assert.equal(plan({ recents: [recent('rec1', { username: '', hasCredentials: false })] }).kind, 'connect', 'no user name means nothing to sign in with');
});

test('restore without stored credentials asks for the sign-in, unless this session holds them', () => {
  const bare = [recent('rec1', { hasCredentials: false })];
  const asks = plan({ recents: bare });
  assert.equal(asks.kind, 'signin');
  assert.equal(asks.recent.id, 'rec1');
  assert.equal(plan({ recents: bare, sessionIds: new Set(['rec1']) }).kind, 'connect');
});

test('restore when the saved connection is gone says so', () => {
  const result = plan({ recents: [recent('other')] });
  assert.equal(result.kind, 'missing');
  assert.deepEqual(result.databases.map((d) => d.name), ['orders']);
});

test('restore only asks for the databases that are missing', () => {
  const workspace = W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'rec1', databases: [{ name: 'orders', selectionKey: 'key-orders' }, { name: 'billing', selectionKey: 'key-billing' }] } });
  const result = plan({ workspace, sources: [src('a', 'orders')] });
  assert.deepEqual(result.databases.map((d) => d.name), ['billing']);
});

// ---- which saved connection a source came from ----

function memoryStore() { const data = new Map(); return { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => { data.set(k, v); }, data }; }

test('the page remembers a source\'s saved connection by id only, and forgets it with the source', () => {
  S.resetSourceSaved();
  const store = memoryStore();
  S.registerSourceSaved({ id: 's1', connectionId: 'c1' }, 'rec1', store);
  assert.equal(S.savedIdFor({ id: 's1' }, [], store), 'rec1');
  assert.equal(S.savedIdFor({ id: 'other', connectionId: 'c1' }, [], store), 'rec1', 'the source\'s connection also names it');
  assert.doesNotMatch(store.data.get(S.SOURCE_SAVED_KEY), /mongodb|password|reader/);
  S.forgetSourceSaved({ id: 's1' }, store);
  assert.equal(S.savedIdFor({ id: 's1' }, [], store), '');
  S.resetSourceSaved();
});

test('a source with no registry entry is matched by connection name only when that is unambiguous', () => {
  S.resetSourceSaved();
  const store = memoryStore();
  const one = [recent('rec1'), recent('rec2', { label: 'staging' })];
  assert.equal(S.savedIdFor({ id: 'x', connectionLabel: 'prod' }, one, store), 'rec1');
  const two = [recent('rec1'), recent('rec3')];
  assert.equal(S.savedIdFor({ id: 'x', connectionLabel: 'prod' }, two, store), '');
  assert.equal(S.savedIdFor({ id: 'x' }, one, store), '');
  S.resetSourceSaved();
});

test('the registry is bounded and survives a store that throws', () => {
  S.resetSourceSaved();
  const store = memoryStore();
  for (let i = 0; i < 100; i++) S.registerSourceSaved({ id: 's' + i }, 'rec', store);
  assert.ok(Object.keys(JSON.parse(store.data.get(S.SOURCE_SAVED_KEY))).length <= 64);
  S.resetSourceSaved();
  const broken = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } };
  assert.doesNotThrow(() => S.registerSourceSaved({ id: 'a' }, 'rec', broken));
  assert.equal(S.savedIdFor({ id: 'a' }, [], broken), 'rec');
  S.resetSourceSaved();
});

test('the details just used find their saved record, by address, user, collection and the other settings, never by name alone', () => {
  const list = [recent('new', { uri: 'mongodb://other', label: 'other' }), recent('rec1'), recent('rec2', { collection: 'Other' })];
  assert.equal(S.matchRecent({ uri: ' mongodb://db.example:27017 ', username: 'reader' }, list).id, 'rec1');
  assert.equal(S.matchRecent({ uri: 'mongodb://db.example:27017', username: 'reader', collection: 'Other' }, list).id, 'rec2', 'a different collection is a different record');
  assert.equal(S.matchRecent({ uri: 'mongodb://db.example:27017', username: 'reader', collection: 'Missing' }, list), null);
  assert.equal(S.matchRecent({ uri: 'mongodb://db.example:27017', username: 'reader', authSource: 'other' }, list), null);
  assert.equal(S.matchRecent({ uri: 'mongodb://db.example:27017', username: 'reader', tlsMode: 'strict' }, list), null);
  assert.equal(S.matchRecent({ uri: 'mongodb://nowhere', username: 'x' }, list), null, 'a similar name is not a match');
  assert.equal(S.suggestedConnectionLabel('mongodb://user:pw@db.example:27017/x'), 'db.example');
});

test('forgetting a saved record forgets the sources registered under it, and no others', () => {
  S.resetSourceSaved();
  const store = memoryStore();
  S.registerSourceSaved({ id: 's1' }, 'rec1', store); S.registerSourceSaved({ id: 's2' }, 'rec2', store);
  S.forgetSavedRecord('rec1', store);
  assert.equal(S.savedIdFor({ id: 's1' }, [], store), '');
  assert.equal(S.savedIdFor({ id: 's2' }, [], store), 'rec2');
  S.resetSourceSaved();
});

// ---- never replacing a saved record (and so its stored password) ----

const details = { uri: 'mongodb://db.example:27017', username: 'reader', collection: 'ApiAuditLog' };
function resolver(over = {}) {
  const calls = { refresh: 0, create: 0 };
  const input = {
    details, recents: [], savedId: undefined,
    refresh: async () => { calls.refresh++; return []; },
    create: async () => { calls.create++; return recent('made'); },
    ...over
  };
  return { calls, run: () => S.resolveSavedRecent(input) };
}

test('a saved record that exists is reused and never saved over', async () => {
  const held = resolver({ recents: [recent('rec1')] });
  const result = await held.run();
  assert.equal(result.recent.id, 'rec1'); assert.equal(result.created, false);
  assert.deepEqual(held.calls, { refresh: 0, create: 0 });
});

test('a stale list is read again, and a record found then is reused, not replaced', async () => {
  const held = resolver({ recents: [], refresh: async () => [recent('rec1')] });
  const result = await held.run();
  assert.equal(result.recent.id, 'rec1'); assert.equal(held.calls.create, 0);
});

test('the held saved id wins, but only while that record is still in the list', async () => {
  const both = [recent('rec1'), recent('rec9', { uri: 'mongodb://elsewhere' })];
  assert.equal((await resolver({ recents: both, savedId: 'rec9' }).run()).recent.id, 'rec9');
  const forgotten = resolver({ recents: [recent('rec1')], savedId: 'gone' });
  assert.equal((await forgotten.run()).recent.id, 'rec1', 'a forgotten id falls back to the address match');
  const nothing = resolver({ recents: [], savedId: 'gone' });
  const made = await nothing.run();
  assert.equal(made.created, true); assert.equal(nothing.calls.create, 1, 'no record at all: one is made');
});

test('when the saved records cannot be read nothing is made, since one might exist', async () => {
  const nullList = resolver({ refresh: async () => null });
  assert.equal((await nullList.run()).recent, null); assert.equal(nullList.calls.create, 0);
  const thrown = resolver({ refresh: async () => { throw new Error('503'); } });
  assert.equal((await thrown.run()).recent, null); assert.equal(thrown.calls.create, 0);
  const failing = resolver({ create: async () => { throw new Error('boom'); } });
  assert.deepEqual(await failing.run(), { recent: null, created: false, recents: [] });
});

test('a record is created exactly once when none matches, and a different collection counts as none', async () => {
  const other = [recent('rec1', { collection: 'Other' })];
  const none = resolver({ recents: other, refresh: async () => other });
  const result = await none.run();
  assert.equal(none.calls.create, 1); assert.equal(result.created, true); assert.equal(result.recents[0].id, 'made');
});

test('the wizard and the saved-connection note only save a connection when nothing matches', () => {
  const wizard = read('src/lib/wizardSubmitApi.ts');
  assert.match(wizard, /resolveSavedRecent\(/);
  assert.equal((wizard.match(/rememberConnection\(/g) ?? []).length, 1, 'one call, inside create');
  assert.doesNotMatch(wizard, /savedForConnection/, 'no id cache that can name a forgotten record');
  const state = read('src/lib/appState.svelte.ts');
  const note = state.slice(state.indexOf('async function noteSavedConnection'), state.indexOf('export async function addSource'));
  assert.match(note, /resolveSavedRecent\(/);
  assert.equal((note.match(/rememberConnection\(/g) ?? []).length, 1);
  assert.match(state, /forgetSavedRecord\(recent\.id\)/);
});

// ---- addresses typed into a filter are not remembered ----

const withFilters = (filters) => {
  const query = queryFor(['a']);
  return { ...query, ...filters };
};

test('a filter holding an address is left out, the rest of the save still goes through', () => {
  const query = withFilters({ urlContains: 'https://x.example/orders', userAgentContains: 'curl', userIps: ['10.0.0.1', 'http://bad'] });
  const workspace = build([src('a', 'orders')], ['a'], { query });
  assert.ok(workspace, 'the save is not refused');
  const text = JSON.stringify(workspace);
  assert.doesNotMatch(text, /:\/\//);
  assert.match(text, /curl/, 'the other filters stay');
  assert.match(text, /10\.0\.0\.1/, 'a list keeps its other items');
  assert.equal(W.findAddressText(workspace), null);
});

test('a database, key or collection name that holds an address is not saved', () => {
  const workspace = build([src('a', 'mongodb://evil'), src('b', 'billing', { selectionKey: 'x://y' }), src('c', 'ok', { collection: 'http://z' }), src('d', 'fine')]);
  assert.deepEqual(workspace.mongo.databases.map((d) => d.name), ['fine']);
  assert.equal(W.findAddressText({ a: [{ b: 'ftp://x' }] }), 'ftp://x');
  assert.equal(W.findAddressText({ a: ['plain', { b: 3 }] }), null);
  assert.deepEqual(W.withoutAddressText({ a: 'x://y', b: ['p', 'q://r'], c: { d: 'e://f', g: 1 } }), { b: ['p'], c: { g: 1 } });
});

test('the saved collection of each database round-trips, and parse keeps only a sane one', () => {
  const workspace = build([src('a', 'orders', { collection: 'Custom' })]);
  assert.equal(workspace.mongo.databases[0].collection, 'Custom');
  assert.equal(W.parseWorkspace(JSON.parse(JSON.stringify(workspace))).mongo.databases[0].collection, 'Custom');
  assert.equal(W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'a', databases: [{ name: 'x', collection: 5 }] } }).mongo.databases[0].collection, undefined);
  assert.equal(W.parseWorkspace({ v: 1, mongo: { savedConnectionId: 'a', databases: [{ name: 'x' }] } }).mongo.databases[0].collection, undefined, 'an older workspace has none');
});

// ---- a change reverted inside the pause is not saved ----

test('a change set back to what the case holds cancels the save that waits', async () => {
  const timers = fakeTimers(); const sent = [];
  const saver = W.createWorkspaceSaver({ send: async (id, ws) => { sent.push(ws); }, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
  const baseline = { v: 1, investigation: { x: 'X' } };
  W.queueWorkspace(saver, 'c1', baseline, { v: 1, investigation: { x: 'Y' } });
  assert.equal(timers.count, 1);
  W.queueWorkspace(saver, 'c1', baseline, { v: 1, investigation: { x: 'X' } });
  assert.equal(timers.count, 0, 'the older change is dropped');
  assert.equal(saver.waiting, false);
  timers.fire(); await settle();
  assert.equal(sent.length, 0, 'Y is never sent');
  W.queueWorkspace(saver, 'c1', baseline, { v: 1, investigation: { x: 'Z' } });
  timers.fire(); await settle();
  assert.deepEqual(sent, [{ v: 1, investigation: { x: 'Z' } }]);
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state.slice(state.indexOf('function queueWorkspaceSave'), state.indexOf('function applyWorkspaceInvestigation')), /queueWorkspace\(workspaceSaver, activeCaseId, workspaceBaseline, workspace\)/);
});

// ---- the case opened from the import panel ----

test('choosing a case on a working screen starts its restore, once, and a freshly made case does not', () => {
  const state = read('src/lib/appState.svelte.ts');
  const set = state.slice(state.indexOf('export function setActiveCase'), state.indexOf('export function caseErased'));
  assert.match(set, /if \(activeCaseId === id\) return/, 'the same case is not started twice');
  assert.match(set, /options\.restore !== false && id && \(screen === 'running' \|\| screen === 'case'\) && info && !info\.demo\) void restoreCaseWorkspace\(id\)/);
  const created = state.slice(state.indexOf('export async function openCreatedCase'), state.indexOf('export function startCreate'));
  assert.match(created, /setActiveCase\(id, \{ restore: false \}\)/);
});

// ---- a restore that cannot finish ----

test('the time limit is about twenty seconds', () => {
  assert.ok(W.RESTORE_TIMEOUT_MS > 0 && W.RESTORE_TIMEOUT_MS <= 20000);
});

test('a refused login asks for the sign-in, a network or time-out failure says MongoDB could not be reached', () => {
  const err = (props, name = 'ApiError') => Object.assign(new Error('x'), { name }, props);
  assert.equal(W.classifyRestoreFailure([err({ stage: 'authentication', status: 400 })], true), 'signin');
  assert.equal(W.classifyRestoreFailure([err({ status: 401 })], false), 'signin');
  assert.equal(W.classifyRestoreFailure([err({ stage: 'connection', status: 502 })], true), 'unreachable');
  assert.equal(W.classifyRestoreFailure([err({ stage: 'tls' })], true), 'unreachable');
  assert.equal(W.classifyRestoreFailure([new TypeError('Failed to fetch')], true), 'unreachable');
  assert.equal(W.classifyRestoreFailure([err({}, 'TimeoutError')], true), 'unreachable');
  assert.equal(W.classifyRestoreFailure([err({ status: 503 })], true), 'unreachable');
  assert.equal(W.classifyRestoreFailure([err({ stage: 'validation' })], true), 'failed');
  assert.equal(W.classifyRestoreFailure([err({ stage: 'discovery' })], true), 'failed');
  assert.equal(W.classifyRestoreFailure([err({ stage: 'connection' }), err({ stage: 'authentication' })], true), 'signin', 'a login problem wins');
  assert.equal(W.classifyRestoreFailure([new Error('odd')], true), 'signin');
  assert.equal(W.classifyRestoreFailure([new Error('odd')], false), 'failed');
  assert.equal(W.classifyRestoreFailure([], false), 'failed');
});

test('the restore has a time limit, a Cancel, a retry, and every way out lets the saved filters go on', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /setTimeout\(\(\) => expireRestore\(run\), RESTORE_TIMEOUT_MS\)/);
  assert.match(state, /function expireRestore/);
  assert.match(state, /classifyRestoreFailure\(failures, recent\.username !== ''\)/);
  assert.match(state, /export function cancelRestore/);
  assert.match(state, /export async function retryRestore/);
  assert.match(state, /cancel: cancelRestore/);
  const cancel = state.slice(state.indexOf('export function cancelRestore'), state.indexOf('export function setActiveCase'));
  assert.match(cancel, /completePendingRestore\(\)/, 'dismissing the strip carries on with the saved filters');
  assert.match(state, /if \(count > sourceCountSeen && restoreWaiting\(restorePhase\)\) completePendingRestore\(\)/, 'any source added ends the waiting');
  const manual = state.slice(state.indexOf('export async function addSource'), state.indexOf('export async function deleteSource'));
  assert.match(manual, /completePendingRestore\(\)/);
  assert.match(read('src/components/CaseReconnect.svelte'), /restore\.cancel/);
  assert.match(read('src/components/CaseReconnect.svelte'), /restore\.retry/);
  assert.match(read('src/components/Landing.svelte'), /restore\.cancel/);
  assert.match(read('src/App.svelte'), /restore\.needsAttention \|\| restore\.working/);
});

test('the new reconnect words pass the screen-text rules and name the failure plainly', () => {
  assert.deepEqual(lintScreenStrings(copy.caseRestoreCopyEntries(), deck.terms), []);
  assert.match(copy.CASE_RESTORE_TEXT.unreachable, /could not|did not answer/i);
  assert.ok(copy.CASE_RESTORE_TEXT.cancelButton && copy.CASE_RESTORE_TEXT.tryAgainButton);
});

// ---- a result that arrives after the case changed ----

test('a database that connects after the case changed is neither listed nor registered', () => {
  const state = read('src/lib/appState.svelte.ts');
  const loop = state.slice(state.indexOf('for (const database of plan.databases)'), state.indexOf('if (added === 0)'));
  const after = loop.slice(loop.indexOf('await api.addSource(input)')).split('continue; }')[1];
  const guard = after.indexOf('if (!alive()) return;');
  assert.ok(guard > -1 && guard < after.indexOf('sources = [...sources, source]') && guard < after.indexOf('registerSourceSaved(source, recent.id)'), 'alive() is checked right after the add, before the source is listed or registered');
});

test('restore uses the collection each source read, not the saved record\'s', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /collection: database\.collection \|\| recent\.collection \|\| 'ApiAuditLog'/);
  assert.match(read('src/lib/wizardSubmitApi.ts'), /heldCollection\(connectionId\)/);
});

// ---- the words ----

test('the sign-in sentence names the database', () => {
  assert.equal(copy.signInText(['orders']), 'This case used MongoDB database orders. Sign in to reconnect.');
  assert.match(copy.signInText(['orders', 'billing', 'audit']), /databases orders, billing and audit\./);
  assert.match(copy.signInText([]), /a MongoDB database/);
});

test('the reconnect copy passes the screen-text rules', () => {
  assert.deepEqual(lintScreenStrings(copy.caseRestoreCopyEntries(), deck.terms), []);
});

// ---- wiring ----

test('opening a case restores its workspace, and the first-run connect step waits for it', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /\(move\.screen === 'running' \|\| move\.screen === 'case'\) && activeCaseId && restoredCaseId !== activeCaseId[\s\S]{0,160}restoreCaseWorkspace\(activeCaseId\)/);
  assert.match(state, /api\.caseWorkspace\(id\)/);
  assert.match(state, /savedConnectionId: recent\.id/);
  assert.match(state, /restorePhase === 'working' \|\| !info/, 'the landing run waits for the restore');
  const app = read('src/App.svelte');
  assert.ok(app.indexOf('restore.active') > -1 && app.indexOf('restore.active') < app.indexOf('<Landing mode="connect"'), 'the case prompt comes before the connect step');
  assert.match(app, /Landing mode="restoring"/);
  assert.match(read('src/components/Landing.svelte'), /mode === 'restoring'/);
});

test('saving the workspace is guarded, quiet, and starts from the same places', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /api\.saveCaseWorkspace\(caseId, workspace\)/);
  const queue = state.slice(state.indexOf('function queueWorkspaceSave'), state.indexOf('function applyWorkspaceInvestigation'));
  assert.match(queue, /if \(!saveAllowed\(activeCaseId\)\) return/);
  assert.match(state, /demo: !!info\?\.demo/);
  const analyse = state.slice(state.indexOf('export async function runAnalysis'), state.indexOf('export function cancelAnalysis'));
  assert.match(analyse, /queueWorkspaceSave\(\)/);
  assert.match(state.slice(state.indexOf('export async function deleteSource'), state.indexOf('export async function runAnalysis')), /queueWorkspaceSave\(\)/);
  assert.match(state.slice(state.indexOf('export function setActiveCase'), state.indexOf('export function caseErased')), /workspaceSaver\.flush\(\)/);
  assert.match(read('src/lib/api.ts'), /saveCaseWorkspace[\s\S]{0,160}method: 'PUT'/);
});

test('nothing sent to the case can carry a login: the workspace builder is the only thing that is saved', () => {
  const file = read('src/lib/caseWorkspace.ts') + read('src/lib/sourceSaved.ts');
  assert.doesNotMatch(file, /password\s*[:=]/i);
  const state = read('src/lib/appState.svelte.ts');
  const sends = state.match(/saveCaseWorkspace\([^)]*\)/g) ?? [];
  assert.equal(sends.length, 1);
});
