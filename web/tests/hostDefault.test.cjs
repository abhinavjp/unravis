const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// Which computer a file goes under when nobody said: the sniffed name, the only entry, this PC, `this-pc`. And the stores that apply it.

const def = loadTypeScript('src/lib/hostDefault.ts');
const hosts = loadTypeScript('src/lib/wizardHosts.svelte.ts');
const create = loadTypeScript('src/lib/wizardCreate.svelte.ts');
const wiz = loadTypeScript('src/lib/wizard.svelte.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');
const { CaseApiError } = loadTypeScript('src/lib/cases.ts');
const sourcesStore = loadTypeScript('src/lib/wizardSources.svelte.ts');

const entry = (label, tz = '') => ({ label, aliases: [], role: '', tz, logicalCores: null, runtime: '' });
const W3C = (name) => `#Software: Microsoft Internet Information Services 10.0\r\n#Fields: date time s-ip cs-method s-computername sc-status\r\n2026-11-20 10:00:01 10.0.0.1 GET ${name} 200\r\n`;

/** A stand-in for GET and PUT /api/hosts. */
function fakeApi(start = [], opts = {}) {
  let list = start.map((e) => ({ ...e }));
  const api = {
    puts: 0,
    gets: 0,
    async get() { api.gets++; if (opts.getFails) throw new Error('down'); return list.map((e) => ({ ...e })); },
    async put(next) {
      api.puts++;
      if (opts.putStatus) throw new CaseApiError('locked', opts.putStatus, 'locked');
      list = next.map((e) => ({ ...e }));
      return list.map((e) => ({ ...e }));
    },
    labels: () => list.map((e) => e.label)
  };
  return api;
}

// ---- pure rules

test('sniffW3cComputer reads s-computername from the first row under #Fields', () => {
  assert.equal(def.sniffW3cComputer(W3C('WEB01')), 'WEB01');
  assert.equal(def.sniffW3cComputer(W3C('-')), '', 'a dash means the log keeps no name');
  assert.equal(def.sniffW3cComputer('#Fields: date time cs-method\n2026-11-20 10:00:01 GET\n'), '', 'no such column');
  assert.equal(def.sniffW3cComputer('2026-11-20 10:00:01 GET\n'), '', 'rows with no #Fields line');
  assert.equal(def.sniffW3cComputer('#Fields: date time s-computername\n'), '', 'header only');
  assert.equal(def.sniffW3cComputer(''), '');
  assert.equal(def.sniffW3cComputer('#fields: date s-ComputerName\n2026-11-20 web-9\n'), 'web-9', 'case does not matter');
});

test('cleanHostLabel and listedLabel', () => {
  assert.equal(def.cleanHostLabel('  WEB01 '), 'WEB01');
  assert.equal(def.cleanHostLabel('bad name'), '');
  assert.equal(def.cleanHostLabel('-x'), '');
  assert.equal(def.cleanHostLabel('a'.repeat(254)), '');
  assert.equal(def.cleanHostLabel(undefined), '');
  assert.equal(def.listedLabel('web01', [entry('WEB01')]), 'WEB01');
  assert.equal(def.listedLabel('web02', [entry('WEB01')]), '');
  assert.equal(def.listedLabel('web01', null), '');
});

test('chooseHostLabel: the file, then the only entry, then this PC, then this-pc', () => {
  const none = [];
  assert.equal(def.chooseHostLabel({ sniffed: 'WEB07', entries: none, thisPc: 'LAPTOP' }), 'WEB07');
  assert.equal(def.chooseHostLabel({ sniffed: 'web01', entries: [entry('WEB01'), entry('WEB02')], thisPc: '' }), 'WEB01', 'the listed spelling');
  assert.equal(def.chooseHostLabel({ sniffed: '', entries: [entry('ONLY')], thisPc: 'LAPTOP' }), 'ONLY');
  assert.equal(def.chooseHostLabel({ sniffed: '', entries: [entry('A'), entry('B')], thisPc: 'LAPTOP' }), 'LAPTOP', 'two entries: not a guess between them');
  assert.equal(def.chooseHostLabel({ sniffed: '', entries: null, thisPc: 'laptop' }), 'laptop');
  assert.equal(def.chooseHostLabel({ sniffed: '', entries: none, thisPc: '' }), 'this-pc');
  assert.equal(def.chooseHostLabel({ sniffed: 'not valid!', entries: none, thisPc: '' }), 'this-pc', 'a name the list refuses is not used');
  assert.equal(def.chooseHostLabel({ sniffed: '', entries: [entry('THIS-PC'), entry('X')], thisPc: '' }), 'THIS-PC');
});

// ---- saveHosts

test('saveHosts adds the missing names in one write and skips names already there (any case)', async () => {
  hosts.resetHosts();
  const api = fakeApi([entry('WEB01')]);
  const out = await hosts.saveHosts(['web01', 'WEB02', 'web02', 'WEB03'], api);
  assert.deepEqual(out, { ok: true, labels: ['WEB01', 'WEB02', 'WEB03'] });
  assert.equal(api.puts, 1);
  assert.deepEqual(api.labels(), ['WEB01', 'WEB02', 'WEB03']);
  const again = await hosts.saveHosts(['WEB02'], api);
  assert.equal(again.ok, true);
  assert.equal(api.puts, 1, 'nothing new: no write');
});

test('saveHosts refuses a bad name before any call, and reports a locked key', async () => {
  hosts.resetHosts();
  const api = fakeApi();
  assert.deepEqual(await hosts.saveHosts(['ok', 'not valid'], api), { ok: false, reason: 'label' });
  assert.equal(api.gets, 0);
  const locked = fakeApi([], { putStatus: 423 });
  assert.deepEqual(await hosts.saveHosts(['WEB01'], locked), { ok: false, reason: 'locked' });
  const down = fakeApi([], { getFails: true });
  assert.deepEqual(await hosts.saveHosts(['WEB01'], down), { ok: false, reason: 'failed' });
});

// ---- autoPickHosts

function setup(kinds, host = null) {
  wiz.resetWizard();
  create.resetCreate();
  hosts.resetHosts();
  sourcesStore.sourcesView.host = host;
  const list = kinds.map((kind, i) => sources.makeSource(kind, `file:${kind}:${i}`, `f${i}`));
  wiz.setSources(list);
  return list.map(sources.sourceKey);
}
const hold = (key, name, text) => create.pickFile(key, new File([text], name));

test('autoPickHosts: an IIS log that names its computer goes under that computer, added to the list', async () => {
  const [k] = setup(['iis-w3c']);
  hold(k, 'u_ex1.log', W3C('WEB07'));
  const api = fakeApi([entry('OTHER'), entry('SECOND')]);
  await create.autoPickHosts(undefined, api);
  assert.equal(create.pickedHost(k), 'WEB07');
  assert.deepEqual(api.labels(), ['OTHER', 'SECOND', 'WEB07']);
  assert.deepEqual(create.hostsMissing(), []);
});

test('autoPickHosts: a nameless IIS log gets the only computer, else this PC, else this-pc', async () => {
  let [k] = setup(['iis-w3c']);
  hold(k, 'u_ex1.log', W3C('-'));
  await create.autoPickHosts(undefined, fakeApi([entry('ONLY')]));
  assert.equal(create.pickedHost(k), 'ONLY');

  [k] = setup(['iis-w3c'], { label: 'LAPTOP-1' });
  hold(k, 'u_ex1.log', W3C('-'));
  const api = fakeApi([]);
  await create.autoPickHosts(undefined, api);
  assert.equal(create.pickedHost(k), 'LAPTOP-1');
  assert.deepEqual(api.labels(), ['LAPTOP-1']);

  [k] = setup(['iis-httperr']);
  hold(k, 'httperr1.log', 'x');
  await create.autoPickHosts(undefined, fakeApi([]));
  assert.equal(create.pickedHost(k), 'this-pc', 'HTTPERR names no computer and gets the default');
  assert.deepEqual(create.hostsMissing(), []);
});

test('autoPickHosts: many files, one write, and each file keeps its own computer', async () => {
  const keys = setup(['iis-w3c', 'iis-w3c', 'iis-httperr', 'iis-logs']);
  hold(keys[0], 'u1.log', W3C('WEB01'));
  hold(keys[1], 'u2.log', W3C('WEB02'));
  hold(keys[2], 'h.log', 'x');
  hold(keys[3], 'n.log', 'x');
  const api = fakeApi([]);
  await create.autoPickHosts(undefined, api);
  assert.equal(create.pickedHost(keys[0]), 'WEB01');
  assert.equal(create.pickedHost(keys[1]), 'WEB02');
  assert.equal(create.pickedHost(keys[2]), 'this-pc');
  assert.equal(create.pickedHost(keys[3]), '', 'a kind that needs no computer is left alone');
  assert.equal(api.puts, 1);
  assert.deepEqual(create.hostSummary().map((r) => r.label).sort(), ['WEB01', 'WEB02', 'this-pc']);
});

test('autoPickHosts keeps a computer the person already picked', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  create.pickHost(k, 'MINE');
  const api = fakeApi([entry('A'), entry('B')]);
  await create.autoPickHosts(undefined, api);
  assert.equal(create.pickedHost(k), 'MINE');
  assert.equal(api.gets, 0, 'nothing to pick: no call');
});

test('autoPickHosts: a list that cannot be read picks nothing and says so; the file still blocks until a pick', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  await create.autoPickHosts(undefined, fakeApi([], { getFails: true }));
  assert.equal(create.pickedHost(k), '');
  assert.equal(create.hostsAuto.failed, true);
  assert.equal(create.hostsAuto.working, 0);
  assert.deepEqual(create.hostsMissing(), [k]);
});

test('autoPickHosts: a list that cannot be saved picks nothing', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  await create.autoPickHosts(undefined, fakeApi([], { putStatus: 423 }));
  assert.equal(create.pickedHost(k), '');
  assert.equal(create.hostsAuto.failed, true);
});

test('a pick in progress blocks Create case; a failed list counts the held file as missing', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  let release;
  const slow = fakeApi([]);
  const realGet = slow.get;
  let first = true;
  slow.get = () => { if (!first) return realGet(); first = false; return new Promise((resolve) => { release = () => resolve(realGet()); }); };
  const run = create.autoPickHosts(undefined, slow);
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(create.hostsAuto.working, 1);
  assert.ok(create.blockers().includes('computer-busy'), 'Create case waits while the computer is being picked');
  release();
  await run;
  assert.equal(create.hostsAuto.working, 0);
  assert.ok(!create.blockers().includes('computer-busy'));

  const [j] = setup(['iis-httperr']);
  hold(j, 'h.log', 'x');
  await create.autoPickHosts(undefined, fakeApi([], { getFails: true }));
  assert.equal(create.hostsAuto.failed, true);
  assert.deepEqual(create.hostsMissing(), [j], 'a failed list leaves the file asking for a pick, never silently filed');
});

test('a reset while the pick runs drops its late result', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  let release;
  const slow = fakeApi([]);
  const realGet = slow.get;
  let first = true;
  slow.get = () => { if (!first) return realGet(); first = false; return new Promise((resolve) => { release = () => resolve(realGet()); }); };
  const run = create.autoPickHosts(undefined, slow);
  await new Promise((r) => setTimeout(r, 0));
  create.resetCreate();
  release();
  await run;
  assert.equal(create.pickedHost(k), '', 'the old run must not file anything after a reset');
  assert.equal(create.hostsAuto.working, 0, 'and must not leave the busy count off by one');
  assert.equal(create.hostsAuto.failed, false);
});

test('a log that names its computer but cannot be added is left to the server, not to a picker that could not change it', async () => {
  const [k] = setup(['iis-w3c']);
  hold(k, 'u_ex1.log', W3C('WEB07'));
  await create.autoPickHosts(undefined, fakeApi([entry('OTHER')], { putStatus: 500 }));
  assert.equal(create.hostsAuto.failed, true);
  assert.deepEqual(create.hostsMissing(), [], 'Create case stays possible: the refusal row explains and offers Try again');
  const [h] = setup(['iis-httperr']);
  hold(h, 'h.log', 'x');
  await create.autoPickHosts(undefined, fakeApi([entry('OTHER'), entry('B')], { putStatus: 500 }));
  assert.deepEqual(create.hostsMissing(), [h], 'a nameless log still asks for a pick when the list cannot be saved');
});

test('pickHostForAll sets one computer on every held file that uses one', async () => {
  const keys = setup(['iis-httperr', 'iis-w3c', 'iis-logs']);
  for (const k of keys) hold(k, 'x.log', 'x');
  create.pickHostForAll('WEB09');
  assert.equal(create.pickedHost(keys[0]), 'WEB09');
  assert.equal(create.pickedHost(keys[1]), 'WEB09');
  assert.equal(create.pickedHost(keys[2]), '');
  assert.deepEqual(create.hostSummary(), [{ label: 'WEB09', count: 2 }]);
});

// ---- the backstop after a refusal

function runApi(refuse) {
  const log = [];
  return {
    log,
    async createCase() { return { id: 'c1' }; },
    async putEnvironments(id, envs) { return envs; },
    async postSourceTag(id, tag) { return tag; },
    async testConnection() { return {}; },
    async addAuditSource() { return {}; },
    async confirmHost() { return {}; },
    async startImports() { return []; },
    async watchJob() { return { kind: 'added', rows: 1 }; },
    async importFile(id, f, onProgress, importer, host) {
      log.push(f.name + '|' + (host ?? ''));
      return refuse(f.name, host ?? '');
    }
  };
}

test('createNow mends a file refused for its computer once, without asking, and reads it again', async () => {
  const [k] = setup(['iis-w3c']);
  hold(k, 'u_ex1.log', W3C('-'));
  create.setAck(true);
  // The wizard's own pick is skipped here to stand for a file that got through with no pick.
  const hostsApi = fakeApi([entry('A'), entry('B')]);
  // Like the server: the file's own computer is accepted once it is in the list.
  const api = runApi(() => (hostsApi.labels().includes('WEB05') ? { kind: 'added', rows: 3 } : { kind: 'refused', reason: 'unknown-host', serverName: 'WEB05' }));
  const result = await create.createNow(api, undefined, hostsApi);
  assert.ok(result && result.caseId);
  assert.equal(create.createView.submit.imports[k].status, 'done');
  assert.ok(hostsApi.labels().includes('WEB05'));
  assert.equal(api.log.length, 2, 'refused once, then read again once');
});

test('createNow does not loop on a file that is refused again', async () => {
  const [k] = setup(['iis-httperr']);
  hold(k, 'h.log', 'x');
  create.pickHost(k, 'this-pc');
  create.setAck(true);
  hosts.hostsView.entries = [entry('this-pc')];
  const api = runApi(() => ({ kind: 'refused', reason: 'unknown-host' }));
  const hostsApi = fakeApi([entry('this-pc')]);
  await create.createNow(api, undefined, hostsApi);
  // First run, one mend, one re-read. Never a third.
  assert.ok(api.log.length <= 2, 'tried at most twice, got ' + api.log.length);
});

// ---- recognising a file by its content when its name says nothing

const { sniffKind } = loadTypeScript('src/lib/sniffKind.ts');
const copyCreate = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');

test('sniffKind: an IIS web log, an HTTP.sys log and a log4net block log are recognised without their names', () => {
  assert.equal(sniffKind('#Software: Microsoft Internet Information Services 10.0\r\n#Fields: date time s-computername\r\n'), 'iis-w3c');
  assert.equal(sniffKind('﻿#Fields: date time cs-uri-stem time-taken\n2026-10-20 08:00:06 /a 12\n'), 'iis-w3c');
  assert.equal(sniffKind('#Software: Microsoft HTTP API 2.0\n#Fields: date time c-ip s-reason\n'), 'iis-httperr');
  assert.equal(sniffKind('Logger name: A.B\nType: INFO\nServer Time: 2026-10-19 09:57:07.013\n'), 'app-log');
});

test('sniffKind: prose, CSV and an empty head are left unrecognised', () => {
  assert.equal(sniffKind('Just some notes about the outage.\nNothing else.'), '');
  assert.equal(sniffKind('a,b,c\n1,2,3\n'), '');
  assert.equal(sniffKind(''), '');
  assert.equal(sniffKind('#Fields: a b c\n'), '');
});

test('a source with no environment, portal or API reads as one plain line, not three "not named"', () => {
  assert.equal(copyCreate.tagLine({ env: '', portal: '', api: '' }), 'Not tied to an environment, portal or API.');
  assert.equal(copyCreate.tagLine({ env: 'Production', portal: '', api: '' }), 'Production > not named > not named');
});
