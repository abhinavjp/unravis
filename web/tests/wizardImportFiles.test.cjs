const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The file side of the create guide: which computer wrote a file (packet P4), the per-file result table and its words (P5), the actions at the
// end (P10), and a dropped folder. Pure parts run as they are; the stores run with a stand-in for the $state rune.

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
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

const zone = loadTypeScript('src/lib/zoneChoice.ts');
const zoneCopy = loadTypeScript('src/lib/copy/zoneChoiceCopy.ts');
const hosts = loadTypeScript('src/lib/wizardHosts.svelte.ts');
const drop = loadTypeScript('src/lib/dropEntries.ts');
const kinds = loadTypeScript('src/lib/fileKinds.ts');
const kindsCopy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
const copy = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');
const discover = loadTypeScript('src/lib/discover.ts');
const discoverCopy = loadTypeScript('src/lib/copy/discoverCopy.ts');
const sub = loadTypeScript('src/lib/wizardSubmit.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');
const create = loadTypeScript('src/lib/wizardCreate.svelte.ts');
const wiz = loadTypeScript('src/lib/wizard.svelte.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const entry = (label, tz = '') => ({ label, aliases: [], role: '', tz, logicalCores: null, runtime: '' });

// ---- zone and computer rules

test('hostProblem: what each kind still needs', () => {
  const list = [entry('WEB01', 'Asia/Dubai'), entry('WEB02')];
  assert.equal(zone.hostProblem('iis-logs', '', list), '', 'W3C says its own computer');
  assert.equal(zone.hostProblem('iis-httperr', '', list), 'pick', 'HTTPERR needs a computer');
  assert.equal(zone.hostProblem('iis-httperr', 'WEB02', list), '', 'HTTPERR needs no zone, its times are UTC');
  assert.equal(zone.hostProblem('iis-httperr', 'GONE', list), 'gone');
  assert.equal(zone.hostProblem('iis-httperr', 'GONE', null), '', 'the list is not known yet: a pick is trusted');
  assert.equal(zone.hostProblem('sql-errorlog', 'WEB02', list), 'no-zone', 'a local-clock kind needs the computer zone');
  assert.equal(zone.hostProblem('sql-errorlog', 'WEB01', list), '');
  assert.equal(zone.hostProblem('app-log', '', list), '', 'optional');
});

test('labelProblem and zoneNameProblem use the server rules', () => {
  assert.equal(zone.labelProblem('', null), 'empty');
  assert.equal(zone.labelProblem('-x', null), 'chars');
  assert.equal(zone.labelProblem('a'.repeat(254), null), 'long');
  assert.equal(zone.labelProblem('web01', [{ label: 'WEB01' }]), 'taken');
  assert.equal(zone.labelProblem('WEB03', [{ label: 'WEB01' }]), '');
  assert.equal(zone.zoneNameProblem(''), 'empty');
  assert.equal(zone.zoneNameProblem('local'), 'unknown');
  assert.equal(zone.zoneNameProblem('Not/AZone'), 'unknown');
  assert.equal(zone.zoneNameProblem('Asia/Dubai'), '');
  assert.equal(zone.zoneNameProblem('UTC'), '');
  assert.equal(zone.zoneOf('WEB01', [entry('WEB01', ' Asia/Dubai ')]), 'Asia/Dubai');
  assert.ok(zone.zoneSuggestions()[0] === 'UTC');
});

test('HTTPERR is UTC (the importer labels it) and still needs a computer', () => {
  assert.equal(kinds.timeZoneRule('iis-httperr'), 'utc');
  assert.equal(kinds.hostNeed('iis-httperr'), 'host');
  assert.equal(kinds.hostNeed('iis-logs'), 'none');
  assert.equal(kinds.hostNeed('sql-errorlog'), 'host-tz');
  assert.equal(kinds.hostNeed('app-log'), 'optional');
});

test('needingHost lists only usable staged files that still lack a computer', () => {
  const st = (id, choice, host, refusal = '') => ({ id, file: {}, name: 'f' + id, size: 1, suggested: choice, choice, changed: false, refusal, host });
  const list = [st(1, 'iis-httperr', ''), st(2, 'iis-httperr', 'WEB01'), st(3, '', ''), st(4, 'iis-logs', '')];
  const ids = kinds.needingHost(list).map((item) => item.id);
  assert.deepEqual(ids, [1]);
});

// ---- the list of computers

function hostsApi(start = []) {
  let stored = start;
  const calls = [];
  return {
    calls,
    async get() { calls.push('get'); return stored.map((e) => ({ ...e })); },
    async put(list) { calls.push('put:' + list.map((e) => e.label + '=' + e.tz).join(',')); stored = list; return list; }
  };
}

test('loadHosts reads once, and many rows share one read', async () => {
  hosts.resetHosts();
  const api = hostsApi([entry('WEB01', 'UTC')]);
  await Promise.all([hosts.loadHosts(false, api), hosts.loadHosts(false, api), hosts.loadHosts(false, api)]);
  await hosts.loadHosts(false, api);
  assert.deepEqual(api.calls, ['get']);
  assert.equal(hosts.hostsView.entries.length, 1);
});

test('loadHosts that fails says so and can be tried again', async () => {
  hosts.resetHosts();
  await hosts.loadHosts(false, { get: async () => { throw new Error('x'); }, put: async () => [] });
  assert.equal(hosts.hostsView.failed, true);
  assert.equal(hosts.hostsView.entries, null);
  await hosts.loadHosts(true, hostsApi([entry('A', 'UTC')]));
  assert.equal(hosts.hostsView.failed, false);
  assert.equal(hosts.hostsView.entries.length, 1);
});

test('saveHost reads the list again first and never replaces a name that is there', async () => {
  hosts.resetHosts();
  const api = hostsApi([entry('WEB01', 'UTC')]);
  const taken = await hosts.saveHost('web01', 'UTC', false, api);
  assert.deepEqual(taken, { ok: false, reason: 'taken' });
  assert.ok(!api.calls.some((c) => c.startsWith('put')));
  const ok = await hosts.saveHost('WEB02', 'Asia/Dubai', false, api);
  assert.deepEqual(ok, { ok: true, label: 'WEB02' });
  assert.equal(api.calls.at(-1), 'put:WEB01=UTC,WEB02=Asia/Dubai');
  assert.equal(hosts.hostsView.entries.length, 2);
});

test('saveHost checks the name and the zone before any call, and a zone may be left out when the kind labels UTC', async () => {
  hosts.resetHosts();
  const api = hostsApi([]);
  assert.deepEqual(await hosts.saveHost('bad name', 'UTC', false, api), { ok: false, reason: 'label' });
  assert.deepEqual(await hosts.saveHost('WEB01', '', false, api), { ok: false, reason: 'zone' });
  assert.deepEqual(await hosts.saveHost('WEB01', 'Nowhere/Land', false, api), { ok: false, reason: 'zone' });
  assert.deepEqual(api.calls, []);
  assert.deepEqual(await hosts.saveHost('WEB01', '', true, api), { ok: true, label: 'WEB01' });
});

test('saveHost reports a locked key and a failed save in their own words', async () => {
  const { CaseApiError } = loadTypeScript('src/lib/cases.ts');
  hosts.resetHosts();
  const locked = await hosts.saveHost('WEB01', 'UTC', false, { get: async () => [], put: async () => { throw new CaseApiError('locked', 423, 'locked'); } });
  assert.deepEqual(locked, { ok: false, reason: 'locked' });
  assert.match(zoneCopy.saveFailText('locked'), /master key/i);
  const failed = await hosts.saveHost('WEB01', 'UTC', false, { get: async () => [], put: async () => { throw new Error('x'); } });
  assert.deepEqual(failed, { ok: false, reason: 'failed' });
});

test('saveHostZone keeps the rest of the entry and refuses a name that is not there', async () => {
  hosts.resetHosts();
  const api = hostsApi([{ ...entry('WEB01'), role: 'web', logicalCores: 4 }]);
  assert.deepEqual(await hosts.saveHostZone('NOPE', 'UTC', api), { ok: false, reason: 'failed' });
  assert.deepEqual(await hosts.saveHostZone('WEB01', 'Asia/Dubai', api), { ok: true, label: 'WEB01' });
  assert.equal(hosts.hostsView.entries[0].role, 'web');
  assert.equal(hosts.hostsView.entries[0].logicalCores, 4);
  assert.equal(hosts.hostsView.entries[0].tz, 'Asia/Dubai');
});

// ---- the copy

test('the zone words read plainly', () => {
  assert.equal(zoneCopy.hostOptionText({ label: 'WEB01', tz: 'Asia/Dubai' }), 'WEB01 (Asia/Dubai)');
  assert.equal(zoneCopy.hostOptionText({ label: 'WEB01', tz: '' }), 'WEB01');
  assert.match(zoneCopy.hostProblemText('pick', 'host'), /does not say which computer/);
  assert.match(zoneCopy.hostProblemText('pick', 'host-tz'), /time zone/);
  assert.equal(zoneCopy.hostProblemText('', 'host'), '');
  assert.equal(zoneCopy.hostSummaryText('', ''), '');
  assert.match(zoneCopy.hostSummaryText('WEB01', 'UTC'), /WEB01, time zone UTC/);
});

test('the warning for files that need a computer counts them', () => {
  assert.match(kindsCopy.hostWarnText(1), /^1 file needs a computer named/);
  assert.match(kindsCopy.hostWarnText(3), /^3 files need a computer named/);
  assert.match(kindsCopy.dropUnreadableText(1), /1/);
});

test('the result-table words: status badge, counts, reasons and the host kinds', () => {
  assert.equal(copy.fileStatusText({ status: 'done', outcome: { kind: 'added', rows: 4 } }), copy.CREATE_TEXT.statusRead);
  assert.equal(copy.fileStatusText({ status: 'done', outcome: { kind: 'duplicate' } }), copy.CREATE_TEXT.statusNothing);
  assert.equal(copy.fileStatusText({ status: 'running', outcome: null }), copy.CREATE_TEXT.statusReading);
  assert.equal(copy.fileStatusText({ status: 'failed', outcome: { kind: 'refused', reason: 'unknown-host' } }), copy.CREATE_TEXT.statusNot);
  assert.equal(copy.fileStatusText({ status: 'skipped', outcome: null }), copy.CREATE_TEXT.statusNot);
  assert.equal(copy.fileStatusText({ status: 'waiting', outcome: null }), copy.CREATE_TEXT.statusWaiting);
  assert.equal(copy.countCell(1234), '1,234');
  assert.equal(copy.countCell(0), '0');
  assert.equal(copy.countCell(null), copy.CREATE_TEXT.noCount);
  assert.equal(copy.isHostReason('unknown-host'), true);
  assert.equal(copy.isHostReason('tz-unlabelled'), true);
  assert.equal(copy.isHostReason('tz-mismatch'), true);
  assert.equal(copy.isHostReason('empty-file'), false);
  assert.equal(copy.tryAgainLabel('a.log'), 'Try a.log again');
  assert.equal(copy.leaveOutLabel('a.log'), 'Leave a.log out');
});

test('resultText stays on the screen with files, and keeps the old text without', () => {
  assert.equal(copy.resultText(2, 2, 0), copy.CREATE_TEXT.allDone);
  assert.equal(copy.resultText(2, 2, 0, true), copy.CREATE_TEXT.readyStay);
  assert.notEqual(copy.CREATE_TEXT.readyStay, copy.CREATE_TEXT.allDone);
  assert.match(copy.resultText(1, 3, 2, true), /1 of 3 sources were read/);
});

test('the row text says how many rows were read and how many skipped', () => {
  assert.match(discoverCopy.rowOutcomeText({ kind: 'added', rows: 10, skipped: 2 }), /10.*2 skipped/);
  assert.doesNotMatch(discoverCopy.rowOutcomeText({ kind: 'added', rows: 10 }), /skipped/);
});

test('outcomeFromJob carries the skipped count only when there is one', () => {
  const job = (record) => ({ state: 'done', answer: { record } });
  assert.deepEqual(discover.outcomeFromJob(job({ status: 'ok', rows: 8, rejected: 3 })), { kind: 'added', rows: 8, skipped: 3 });
  assert.deepEqual(discover.outcomeFromJob(job({ status: 'ok', rows: 8, rejected: 0 })), { kind: 'added', rows: 8 });
  assert.deepEqual(discover.outcomeFromJob(job({ status: 'unknown-host', rows: 0, rejected: 0 })), { kind: 'refused', reason: 'unknown-host' });
});

test('the zone, file-kind and create copy passes the copy lint', () => {
  for (const entries of [zoneCopy.zoneChoiceCopyEntries(), kindsCopy.fileKindsCopyEntries(), copy.wizardCreateCopyEntries()]) {
    assert.ok(entries.length > 10);
    const problems = lintScreenStrings(entries, deck.terms).map((f) => f.id + ': ' + f.message);
    assert.deepEqual(problems, []);
  }
});

// ---- the run: the computer goes to the server, and only the asked files run

function fileApi(outcomes = {}) {
  const log = [];
  return {
    log,
    async createCase() { log.push('createCase'); return { id: 'case1' }; },
    async putEnvironments(id, envs) { return envs; },
    async postSourceTag(id, tag) { return tag; },
    async testConnection() { return {}; },
    async addAuditSource() { return {}; },
    async confirmHost() { return {}; },
    async startImports() { return []; },
    async watchJob() { return { kind: 'added', rows: 1 }; },
    async importFile(id, f, onProgress, importer, host) { log.push('importFile:' + f.name + '|' + (importer ?? '') + '|' + (host ?? '')); return outcomes[f.name] ?? { kind: 'added', rows: 5, skipped: 1 }; }
  };
}
const fsrc = (label, ref, kind) => sources.makeSource(kind, ref, label);
const baseInput = (over) => ({ title: 'T', envs: [], sources: [], window: { from: '2001-01-01T00:00:00Z', to: '2001-01-02T00:00:00Z' }, scanId: '', host: null, files: {}, ackText: 'ok', ...over });

test('submitWizard sends the computer of a file to the server and sends none for a file without one', async () => {
  const a = fsrc('a', 'file:iis-httperr:1', 'iis-httperr');
  const b = fsrc('b', 'file:iis-logs:1', 'iis-logs');
  const ka = sources.sourceKey(a), kb = sources.sourceKey(b);
  const api = fileApi();
  await sub.submitWizard(baseInput({ sources: [a, b], files: { [ka]: { name: 'h.log' }, [kb]: { name: 'w.log' } }, hosts: { [ka]: 'WEB01' } }), api, sub.blankSubmitState());
  assert.deepEqual(api.log.filter((l) => l.startsWith('importFile')).sort(), ['importFile:h.log||WEB01', 'importFile:w.log||']);
});

test('submitWizard with only= runs just those files', async () => {
  const a = fsrc('a', 'file:iis-httperr:1', 'iis-httperr');
  const b = fsrc('b', 'file:iis-logs:1', 'iis-logs');
  const ka = sources.sourceKey(a), kb = sources.sourceKey(b);
  const api = fileApi();
  const state = sub.blankSubmitState();
  const inp = baseInput({ sources: [a, b], files: { [ka]: { name: 'h.log' }, [kb]: { name: 'w.log' } }, hosts: { [ka]: 'WEB01' } });
  await sub.submitWizard(inp, api, state);
  api.log.length = 0;
  state.imports[ka].status = 'failed';
  await sub.submitWizard({ ...inp, only: [ka] }, api, state);
  assert.deepEqual(api.log.filter((l) => l.startsWith('importFile')), ['importFile:h.log||WEB01']);
});

// ---- the stores: holding a computer, blockers, retry list, leaving a file out

test('a held file that needs a computer blocks Create until one is named; naming it clears the block', () => {
  wiz.resetWizard();
  create.resetCreate();
  hosts.resetHosts();
  const a = fsrc('a', 'file:iis-httperr:1', 'iis-httperr');
  wiz.setSources([a]);
  const key = sources.sourceKey(a);
  create.pickFile(key, { name: 'h.log', size: 1 }, 'iis-httperr');
  assert.deepEqual(create.hostsMissing(), [key]);
  create.setAck(true);
  assert.ok(create.blockers().includes('zone'));
  create.pickHost(key, 'WEB01');
  assert.equal(create.pickedHost(key), 'WEB01');
  assert.deepEqual(create.hostsMissing(), []);
  assert.ok(!create.blockers().includes('zone'));
  const input = create.buildInput();
  assert.equal(input.hosts[key], 'WEB01');
});

test('a file that needs no computer never blocks, and a host given when adding is kept', () => {
  wiz.resetWizard();
  create.resetCreate();
  const w = fsrc('w', 'file:iis-logs:1', 'iis-logs');
  const h = fsrc('h', 'file:iis-httperr:1', 'iis-httperr');
  wiz.setSources([w, h]);
  create.pickFile(sources.sourceKey(w), { name: 'w.log', size: 1 });
  create.pickFile(sources.sourceKey(h), { name: 'h.log', size: 1 }, 'iis-httperr', 'WEB09');
  assert.deepEqual(create.hostsMissing(), []);
  assert.equal(create.pickedHost(sources.sourceKey(h)), 'WEB09');
  create.pickHost(sources.sourceKey(h), '');
  assert.equal(create.pickedHost(sources.sourceKey(h)), '');
});

test('failedFileKeys lists failed and skipped files, and leaveOutFile takes one out of the list, the held files and the run', () => {
  wiz.resetWizard();
  create.resetCreate();
  const a = fsrc('a', 'file:iis-logs:1', 'iis-logs');
  const b = fsrc('b', 'file:iis-logs:2', 'iis-logs');
  const c = fsrc('c', 'file:iis-logs:3', 'iis-logs');
  wiz.setSources([a, b, c]);
  const [ka, kb, kc] = [a, b, c].map(sources.sourceKey);
  for (const k of [ka, kb, kc]) create.pickFile(k, { name: k, size: 1 });
  create.createView.submit.imports[ka] = { status: 'done', percent: null, outcome: { kind: 'added', rows: 1 }, reason: '' };
  create.createView.submit.imports[kb] = { status: 'failed', percent: null, outcome: { kind: 'refused', reason: 'empty-file' }, reason: 'empty-file' };
  create.createView.submit.imports[kc] = { status: 'skipped', percent: null, outcome: null, reason: 'no-file' };
  assert.deepEqual(create.failedFileKeys().sort(), [kb, kc].sort());
  create.leaveOutFile(kb);
  assert.deepEqual(create.failedFileKeys(), [kc]);
  assert.equal(create.pickedFile(kb), null);
  assert.equal(create.createView.submit.imports[kb], undefined);
  assert.deepEqual(wiz.wizard.sources.map(sources.sourceKey), [ka, kc]);
});

// ---- a dropped folder

const fakeFile = (name) => ({ name, size: 3 });
const fileEntry = (name, fail = false) => ({ isFile: true, isDirectory: false, name, file: (ok, bad) => (fail ? bad(new Error('x')) : ok(fakeFile(name))) });
function dirEntry(name, children, batch = 2) {
  return {
    isFile: false, isDirectory: true, name,
    createReader() {
      let at = 0;
      return { readEntries(ok) { const part = children.slice(at, at + batch); at += batch; ok(part); } };
    }
  };
}
const item = (e) => ({ kind: 'file', webkitGetAsEntry: () => e });

test('a dropped folder is walked, in batches, with the folder path on every file', async () => {
  const tree = dirEntry('logs', [fileEntry('a.log'), fileEntry('b.log'), fileEntry('c.log'), dirEntry('W3SVC1', [fileEntry('u_ex1.log'), fileEntry('u_ex2.log'), fileEntry('u_ex3.log')])]);
  const result = await drop.filesFromDrop([item(tree)], []);
  assert.equal(result.folder, true);
  assert.equal(result.truncated, false);
  assert.equal(result.unreadable, 0);
  assert.deepEqual(result.files.map((f) => f.webkitRelativePath || f.name), ['logs/a.log', 'logs/b.log', 'logs/c.log', 'logs/W3SVC1/u_ex1.log', 'logs/W3SVC1/u_ex2.log', 'logs/W3SVC1/u_ex3.log']);
});

test('a dropped single file keeps its plain name, and loose files are kept next to a folder', async () => {
  const result = await drop.filesFromDrop([item(fileEntry('one.log')), item(dirEntry('d', [fileEntry('x.log')]))], []);
  assert.deepEqual(result.files.map((f) => f.webkitRelativePath || f.name), ['one.log', 'd/x.log']);
  assert.equal(result.folder, true);
});

test('files that cannot be read are counted, not lost silently', async () => {
  const tree = dirEntry('d', [fileEntry('ok.log'), fileEntry('bad.log', true)]);
  const result = await drop.filesFromDrop([item(tree)], []);
  assert.equal(result.files.length, 1);
  assert.equal(result.unreadable, 1);
});

test('a folder reader that fails is counted as unreadable', async () => {
  const broken = { isFile: false, isDirectory: true, name: 'd', createReader: () => ({ readEntries: (ok, bad) => bad(new Error('x')) }) };
  const result = await drop.filesFromDrop([item(broken)], []);
  assert.equal(result.files.length, 0);
  assert.equal(result.unreadable, 1);
  assert.equal(result.folder, true);
});

test('the walk stops at the limit and says so', async () => {
  const many = Array.from({ length: drop.MAX_DROPPED_FILES + 5 }, (_, i) => fileEntry('f' + i + '.log'));
  const result = await drop.filesFromDrop([item(dirEntry('big', many, 100))], []);
  assert.equal(result.files.length, drop.MAX_DROPPED_FILES);
  assert.equal(result.truncated, true);
});

test('the walk stops at the depth limit', async () => {
  let node = dirEntry('leaf', [fileEntry('deep.log')]);
  for (let i = 0; i < drop.MAX_DROP_DEPTH + 2; i++) node = dirEntry('d' + i, [node]);
  const result = await drop.filesFromDrop([item(node)], []);
  assert.equal(result.truncated, true);
  assert.equal(result.files.length, 0);
});

test('a browser with no entry API gives the plain file list', async () => {
  const plain = [fakeFile('a.log'), fakeFile('b.log')];
  const result = await drop.filesFromDrop([{ kind: 'file' }], plain);
  assert.equal(result.files.length, 2);
  assert.equal(result.folder, false);
  const none = await drop.filesFromDrop(null, null);
  assert.deepEqual(none.files, []);
});

test('the entries are taken before anything is awaited', async () => {
  let taken = 0;
  const items = [{ kind: 'file', webkitGetAsEntry: () => { taken++; return fileEntry('a.log'); } }, { kind: 'file', webkitGetAsEntry: () => { taken++; return fileEntry('b.log'); } }];
  const pending = drop.filesFromDrop(items, []);
  assert.equal(taken, 2, 'both entries were taken in the same turn the drop arrived');
  await pending;
});

// ---- the screens

test('the progress screen has the table, the row actions and the four actions, and the panel and review have the choice', () => {
  const progress = read('src/components/wizard/CreateProgress.svelte');
  for (const text of ['CREATE_TEXT.colFile', 'CREATE_TEXT.colKind', 'CREATE_TEXT.colRows', 'CREATE_TEXT.colSkipped', 'CREATE_TEXT.colResult', 'CREATE_TEXT.rowTry', 'CREATE_TEXT.rowLeaveOut', 'CREATE_TEXT.openTimeline', 'CREATE_TEXT.retryFailed', 'CREATE_TEXT.addMore']) {
    assert.ok(progress.includes(text), text);
  }
  assert.match(progress, /<table aria-label=/);
  assert.match(progress, /scope="col"/);
  assert.match(progress, /ZoneChoice/);
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /onTimeline=\{openTimeline\}/);
  assert.match(landing, /onAddMore=\{addMore\}/);
  assert.match(landing, /openTracing\(\)/);
  assert.match(landing, /openCaseImport\(\)/);
  assert.match(read('src/components/wizard/ImportFilesPanel.svelte'), /<ZoneChoice/);
  assert.match(read('src/components/wizard/ReviewStep.svelte'), /<ZoneChoice/);
});

test('the panel never reads a typed folder path: files reach the server only as File objects through the api boundary', () => {
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  // The panel reaches no network module and has no text box to type a path into: its only inputs pick files.
  assert.doesNotMatch(panel, /from '[^']*[aA]pi[^']*'/, 'no api module is imported');
  assert.doesNotMatch(panel, /\bfetch\(|XMLHttpRequest/);
  const inputs = panel.match(/<input\b[^\r\n]*/g) ?? [];
  assert.equal(inputs.length, 3, 'the files input, the folder input and the type cards input (one line, drawn for each card) are the only inputs');
  assert.ok(inputs.every((tag) => /type="file"/.test(tag)), 'every input is a file picker');
  // The boundary: the one call that sends a file takes a File, never a path.
  assert.match(read('src/lib/wizardSubmit.ts'), /importFile\(caseId: string, file: File,/);
});
