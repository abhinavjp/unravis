const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Hub fixes, batch B: the Sources list pairs the wizard's import-0 tags with their files by name (C1, P2), a folder pick names its source
// by the file alone (C5), host saves are serialised (C3), a stored zip entry whose size lies is damaged (P1), and the Sources list
// re-reads the computers on every read (C2).

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

const model = loadTypeScript('src/lib/sourcesList.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');
const kinds = loadTypeScript('src/lib/fileKinds.ts');
const files = loadTypeScript('src/lib/wizardFiles.svelte.ts');
const create = loadTypeScript('src/lib/wizardCreate.svelte.ts');
const draft = loadTypeScript('src/lib/wizard.svelte.ts');
const hosts = loadTypeScript('src/lib/wizardHosts.svelte.ts');
const zipRead = loadTypeScript('src/lib/zipRead.ts');

const imp = (id, importer, over = {}) => ({ id, importer, fileName: importer + '-' + id + '.log', hostLabel: '', tzRule: 'utc-field', status: 'ok', rows: 10, ...over });
const tag = (id, kind, over = {}) => ({ id, importId: 0, kind, label: kind + id, env: '', portal: '', api: '', classification: '', ...over });
const byKey = (rows) => Object.fromEntries(rows.map((row) => [row.key, row]));

/* ------------------------------------------------------------------ C5: a folder pick names its source by the file alone */

test('C5: addStagedFiles labels the source with the base name, not the folder path', () => {
  draft.setSources([]);
  create.resetCreate();
  const rows = kinds.stageFiles([{ name: 'u_ex261020.log', size: 100, webkitRelativePath: 'Prod/W3SVC1/u_ex261020.log' }]);
  assert.equal(rows[0].name, 'Prod/W3SVC1/u_ex261020.log', 'the staging-table display keeps the path');
  const result = files.addStagedFiles(rows);
  assert.equal(result.added, 1);
  assert.equal(draft.wizard.sources[0].label, 'u_ex261020.log');
});

/* ------------------------------------------------------------------ C1: import-0 tags pair with their file by name */

test('C1: two files of one kind pair with their tags by name, and the environment filter follows', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'u_ex1.log', env: 'Production' }), tag(12, 'iis-w3c', { label: 'u_ex2.log', env: 'UAT' })],
    [imp(1, 'iis-w3c', { fileName: 'u_ex1.log' }), imp(2, 'iis-w3c', { fileName: 'u_ex2.log' })], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, 'Production');
  assert.equal(byKey(rows).f2.env, 'UAT');
  assert.deepEqual(model.envNames(rows), ['Production', 'UAT']);
  assert.deepEqual(model.filterByEnv(rows, 'UAT').map((row) => row.key), ['f2']);
});

test('C1: the files listed in the opposite order of the tags still pair by name', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'u_ex1.log', env: 'Production' }), tag(12, 'iis-w3c', { label: 'u_ex2.log', env: 'UAT' })],
    [imp(2, 'iis-w3c', { fileName: 'u_ex2.log' }), imp(1, 'iis-w3c', { fileName: 'u_ex1.log' })], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, 'Production');
  assert.equal(byKey(rows).f2.env, 'UAT');
});

test('C1: two files with the same base name are not guessed at', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'u_ex1.log', env: 'Production' }), tag(12, 'iis-w3c', { label: 'u_ex1.log', env: 'UAT' })],
    [imp(1, 'iis-w3c', { fileName: 'u_ex1.log' }), imp(2, 'iis-w3c', { fileName: 'u_ex1.log' })], null);
  assert.equal(rows.length, 4, 'both files and both tags keep their own rows');
  assert.ok(rows.every((row) => (row.isFile ? row.env === '' : true)), 'no file took an environment by guessing');
});

test('C1: with one file failed, the ok one pairs by label only and the other tag keeps its row', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'u_ex1.log', env: 'Production' }), tag(12, 'iis-w3c', { label: 'u_ex2.log', env: 'UAT' })],
    [imp(1, 'iis-w3c', { fileName: 'u_ex1.log' }), imp(2, 'iis-w3c', { fileName: 'u_ex2.log', status: 'failed' })], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, 'Production');
  assert.equal(byKey(rows).t12.env, 'UAT');
  assert.equal(byKey(rows).f2, undefined);
});

test('C1: a name over the label limit is cut the same way on both sides and still pairs', () => {
  const longName = 'u_' + 'x'.repeat(100) + '.log';
  const cut = sources.cleanTag(longName, sources.LABEL_MAX);
  assert.equal([...cut].length, sources.LABEL_MAX);
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: cut, env: 'Production' }), tag(12, 'iis-w3c', { label: 'u_other.log', env: 'UAT' })],
    [imp(1, 'iis-w3c', { fileName: longName }), imp(2, 'iis-w3c', { fileName: 'u_other.log' })], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, 'Production');
  assert.equal(byKey(rows).f2.env, 'UAT');
});

test('C1: a two-file wizard run, tagged before the imports exist, lists each file with its environment', () => {
  draft.setSources([]);
  create.resetCreate();
  const result = files.addStagedFiles(kinds.stageFiles([
    { name: 'u_ex260101.log', size: 100, webkitRelativePath: 'Prod/W3SVC1/u_ex260101.log' },
    { name: 'u_ex260102.log', size: 100, webkitRelativePath: 'Uat/W3SVC2/u_ex260102.log' }
  ]));
  assert.equal(result.added, 2);
  const [a, b] = draft.wizard.sources;
  // The tags exactly as wizardSubmit posts them at step 3, before the file imports of step 4.
  const tags = [
    { id: 21, importId: 0, kind: a.kind, label: a.label, env: 'Production', portal: a.portal, api: a.api, classification: '' },
    { id: 22, importId: 0, kind: b.kind, label: b.label, env: 'UAT', portal: b.portal, api: b.api, classification: '' }
  ];
  const imports = [
    imp(1, 'iis-w3c', { fileName: 'u_ex260101.log', rows: 10 }),
    imp(2, 'iis-w3c', { fileName: 'u_ex260102.log', rows: 20 })
  ];
  const rows = model.buildSourceList(tags, imports, null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, 'Production');
  assert.equal(byKey(rows).f2.env, 'UAT');
});

/* ------------------------------------------------------------------ P2: a tag tied to a missing import is never folded into another file */

test('P2: a tag whose import is gone keeps its own row and the file takes no environment', () => {
  const rows = model.buildSourceList([tag(31, 'iis-w3c', { importId: 7, label: 'gone', env: 'Production' })], [imp(1, 'iis-w3c')], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f1.env, '');
  assert.equal(byKey(rows).t31.env, 'Production');
});

/* ------------------------------------------------------------------ C3: host saves are serialised */

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function slowApi(failFirstPut = false) {
  let stored = [];
  let puts = 0;
  const calls = [];
  return {
    calls,
    stored: () => stored,
    async get() { calls.push('get'); await sleep(5); return stored.map((entry) => ({ ...entry })); },
    async put(list) { calls.push('put'); await sleep(5); puts++; if (failFirstPut && puts === 1) throw new Error('x'); stored = list; return list; }
  };
}

test('C3: two saveHost calls at once run one after another and both computers land', async () => {
  hosts.resetHosts();
  const api = slowApi();
  const [first, second] = await Promise.all([hosts.saveHost('WEB01', 'UTC', false, api), hosts.saveHost('WEB02', 'Asia/Dubai', false, api)]);
  assert.deepEqual(first, { ok: true, label: 'WEB01' });
  assert.deepEqual(second, { ok: true, label: 'WEB02' });
  assert.deepEqual(api.calls, ['get', 'put', 'get', 'put'], 'the second read starts only after the first write');
  assert.deepEqual(api.stored().map((entry) => entry.label), ['WEB01', 'WEB02']);
});

test('C3: a failed save does not block the next one', async () => {
  hosts.resetHosts();
  const api = slowApi(true);
  const [first, second] = await Promise.all([hosts.saveHost('WEB01', 'UTC', false, api), hosts.saveHost('WEB02', 'UTC', false, api)]);
  assert.deepEqual(first, { ok: false, reason: 'failed' });
  assert.deepEqual(second, { ok: true, label: 'WEB02' });
  assert.deepEqual(api.stored().map((entry) => entry.label), ['WEB02']);
});

test('C3: saveHost and saveHostZone together still run in order', async () => {
  hosts.resetHosts();
  const api = slowApi();
  const [added, zoned] = await Promise.all([hosts.saveHost('WEB01', 'UTC', false, api), hosts.saveHostZone('WEB01', 'Asia/Dubai', api)]);
  assert.deepEqual(added, { ok: true, label: 'WEB01' });
  assert.deepEqual(zoned, { ok: true, label: 'WEB01' });
  assert.deepEqual(api.calls, ['get', 'put', 'get', 'put']);
  assert.equal(api.stored().length, 1);
  assert.equal(api.stored()[0].tz, 'Asia/Dubai');
});

/* ------------------------------------------------------------------ P1: a stored zip entry whose size lies is damaged */

function crc32(buffer) {
  let crc = ~0;
  for (const byte of buffer) { crc ^= byte; for (let k = 0; k < 8; k++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1)); }
  return ~crc >>> 0;
}

// A minimal zip writer: [{ name, data, method: 0 | 8, flags }]
function makeZip(entries) {
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name, 'utf8');
    const data = Buffer.from(entry.data);
    const method = entry.method ?? 8;
    const body = method === 8 ? zlib.deflateRawSync(data) : data;
    const flags = (entry.flags ?? 0) | 0x800;
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0); local.writeUInt16LE(20, 4); local.writeUInt16LE(flags, 6); local.writeUInt16LE(method, 8);
    local.writeUInt32LE(crc32(data), 14); local.writeUInt32LE(body.length, 18); local.writeUInt32LE(data.length, 22); local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0); central.writeUInt16LE(20, 4); central.writeUInt16LE(20, 6); central.writeUInt16LE(flags, 8); central.writeUInt16LE(method, 10);
    central.writeUInt32LE(crc32(data), 16); central.writeUInt32LE(body.length, 20); central.writeUInt32LE(data.length, 24); central.writeUInt16LE(name.length, 28); central.writeUInt32LE(offset, 42);
    locals.push(local, name, body);
    centrals.push(central, name);
    offset += 30 + name.length + body.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10); end.writeUInt32LE(directory.length, 12); end.writeUInt32LE(offset, 16);
  return new File([Buffer.concat([...locals, directory, end])], 'logs.zip');
}

test('P1: a stored entry whose declared unpacked size differs from its stored size is skipped as damaged', async () => {
  const zip = makeZip([{ name: 'stored.log', data: 'hello stored\n', method: 0 }, { name: 'fine.log', data: 'fine\n', method: 0 }]);
  const bytes = Buffer.from(await zip.arrayBuffer());
  // The first central record: make the uncompressed size (offset 24) one more than the compressed size (offset 20). Still a small size.
  const at = bytes.indexOf(Buffer.from([0x50, 0x4b, 0x01, 0x02]));
  assert.ok(at > 0, 'central directory found');
  bytes.writeUInt32LE(bytes.readUInt32LE(at + 20) + 1, at + 24);
  const result = await zipRead.readZip(new File([bytes], 'logs.zip'));
  assert.equal(result.problem, '');
  assert.deepEqual(result.files.map((file) => file.name), ['fine.log']);
  assert.deepEqual(result.skipped, [{ name: 'stored.log', reason: 'damaged' }]);
});

test('P1: an untouched stored entry still comes back', async () => {
  const result = await zipRead.readZip(makeZip([{ name: 'plain.log', data: 'plain\n', method: 0 }]));
  assert.equal(result.problem, '');
  assert.deepEqual(result.skipped, []);
  assert.deepEqual(result.files.map((file) => file.name), ['plain.log']);
  assert.equal(await result.files[0].text(), 'plain\n');
});

/* ------------------------------------------------------------------ C2: the Sources list re-reads the computers on every read */

test('C2: the Sources list forces the host read, so a computer or zone saved elsewhere is seen', () => {
  const component = read('src/components/sources/SourcesList.svelte');
  assert.match(component, /loadHosts\(true\)/);
});
