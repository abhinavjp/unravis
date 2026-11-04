const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Hub fixes, batch C: an app-log tag pairs with the importer the server detected in the file (F9), a tag whose label is the name of a
// file in the case (failed, duplicate, ...) is never handed to another file by the singleton fallback (F8), a real failure after Stop
// is still recorded before the queue stops (F19), and every zip entry is checked against the CRC-32 the directory declares (F6).

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

const model = loadTypeScript('src/lib/sourcesList.ts');
const cases = loadTypeScript('src/lib/cases.ts');
const zipRead = loadTypeScript('src/lib/zipRead.ts');

const imp = (id, importer, over = {}) => ({ id, importer, fileName: importer + '-' + id + '.log', hostLabel: '', tzRule: 'utc-field', status: 'ok', rows: 10, ...over });
const tag = (id, kind, over = {}) => ({ id, importId: 0, kind, label: kind + id, env: '', portal: '', api: '', classification: '', ...over });
const byKey = (rows) => Object.fromEntries(rows.map((row) => [row.key, row]));

// Synthetic values only.
const record = (extra = {}) => ({ id: 3, importer: 'iis-w3c', fileName: 'web-a.log', sha256: 'f'.repeat(64), bytes: 17, rows: 5, rejected: 0, hostLabel: 'web-a', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2001-02-03T04:05:06Z', status: 'ok', supplied: {}, ...extra });
const answer = (extra = {}, rec = {}) => ({ importer: 'iis-w3c', score: 0.9, tied: [], report: null, record: record(rec), ...extra });
const item = (key, importer = '') => ({ key, file: new File(['x'], key + '.log'), importer, host: '' });

/* ------------------------------------------------------------------ F9: an app-log tag pairs with the importer the server detected */

test('hubfixC F9: an app-log tag folds into the file the server read as serilog-clef, by name', () => {
  const rows = model.buildSourceList(
    [tag(11, 'app-log', { label: 'app.log', env: 'Production' })],
    [imp(1, 'serilog-clef', { fileName: 'app.log' })], null);
  assert.equal(rows.length, 1, 'no separate tag row');
  assert.equal(rows[0].key, 'f1');
  assert.equal(rows[0].env, 'Production');
});

test('hubfixC F9: with no name match the app-log tag still folds into the one unclaimed app-log file', () => {
  const rows = model.buildSourceList(
    [tag(11, 'app-log', { label: 'applog1', env: 'Production' })],
    [imp(1, 'nlog-json', { fileName: 'service-2026-11-04.log' })], null);
  assert.equal(rows.length, 1, 'no separate tag row');
  assert.equal(rows[0].key, 'f1');
  assert.equal(rows[0].env, 'Production');
});

test('hubfixC F9: an iis-w3c tag never pairs with a serilog-clef import', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'u_ex1.log', env: 'Production' })],
    [imp(1, 'serilog-clef', { fileName: 'u_ex1.log' })], null);
  assert.equal(rows.length, 2, 'the tag keeps its own row');
  assert.equal(byKey(rows).f1.env, '');
  assert.equal(byKey(rows).t11.env, 'Production');
});

/* ------------------------------------------------------------------ F8: a tag that names a file in the case is never handed to another file */

test('hubfixC F8: the tag of a failed import keeps its row and its environment is not given to another file', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'a.log', env: 'Production' })],
    [imp(1, 'iis-w3c', { fileName: 'a.log', status: 'failed' }), imp(2, 'iis-w3c', { fileName: 'c.log' })], null);
  assert.equal(rows.length, 2);
  assert.equal(byKey(rows).f2.env, '', 'the other file gets no environment by guessing');
  assert.equal(byKey(rows).f1, undefined, 'a failed import is not a row');
  assert.equal(byKey(rows).t11.env, 'Production');
});

test('hubfixC F8: a tag whose label names no file still folds into the one unclaimed file of the kind', () => {
  const rows = model.buildSourceList(
    [tag(11, 'iis-w3c', { label: 'iis-w3c1', env: 'Production' })],
    [imp(1, 'iis-w3c', { fileName: 'u_ex261104.log' })], null);
  assert.equal(rows.length, 1, 'no separate tag row');
  assert.equal(rows[0].key, 'f1');
  assert.equal(rows[0].env, 'Production');
});

/* ------------------------------------------------------------------ F19: a real failure after Stop is still recorded */

test('hubfixC F19: a real error after the signal was aborted is recorded, and the next file is not started', async () => {
  const stop = new AbortController();
  const starts = [];
  const done = await cases.runImportQueue([item('a'), item('b')], async (it) => {
    if (it.key === 'a') { stop.abort(); throw new cases.CaseApiError('nope', 422, 'importer-required'); }
    return answer();
  }, { signal: stop.signal, onStart: (key) => starts.push(key) });
  assert.deepEqual(starts, ['a'], 'b was never started');
  assert.equal(done.length, 1);
  assert.equal(done[0].key, 'a');
  assert.equal(done[0].outcome.verdict, 'rejected');
  assert.equal(done[0].outcome.reason, 'importer-required');
});

test('hubfixC F19: a request that is itself aborted still ends the queue with no outcome for the file', async () => {
  const stop = new AbortController();
  const done = await cases.runImportQueue([item('a'), item('b')], async (it) => {
    if (it.key === 'a') { stop.abort(); throw new DOMException('stopped', 'AbortError'); }
    return answer();
  }, { signal: stop.signal });
  assert.deepEqual(done, []);
});

/* ------------------------------------------------------------------ F6: the CRC-32 of every zip entry is checked */

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

test('hubfixC F6: a stored entry with a flipped byte in its body is damaged, the other entries still come back', async () => {
  const zip = makeZip([{ name: 'stored.log', data: 'hello stored\n', method: 0 }, { name: 'fine.log', data: 'fine\n', method: 0 }]);
  const bytes = Buffer.from(await zip.arrayBuffer());
  // The first entry is at offset 0: flip one byte of its data (after the 30-byte local header and the name) and leave the central record alone.
  bytes[30 + Buffer.from('stored.log', 'utf8').length] ^= 0xff;
  const result = await zipRead.readZip(new File([bytes], 'logs.zip'));
  assert.equal(result.problem, '');
  assert.deepEqual(result.files.map((file) => file.name), ['fine.log']);
  assert.deepEqual(result.skipped, [{ name: 'stored.log', reason: 'damaged' }]);
});

test('hubfixC F6: a deflated entry whose declared CRC-32 was changed is damaged', async () => {
  const zip = makeZip([{ name: 'packed.log', data: 'packed data packed data\n', method: 8 }, { name: 'fine.log', data: 'fine\n', method: 0 }]);
  const bytes = Buffer.from(await zip.arrayBuffer());
  // The first central record: change the CRC-32 (offset 16) only, so the inflated bytes no longer match what the directory declares.
  const at = bytes.indexOf(Buffer.from([0x50, 0x4b, 0x01, 0x02]));
  assert.ok(at > 0, 'central directory found');
  bytes.writeUInt32LE((bytes.readUInt32LE(at + 16) ^ 0xff) >>> 0, at + 16);
  const result = await zipRead.readZip(new File([bytes], 'logs.zip'));
  assert.equal(result.problem, '');
  assert.deepEqual(result.files.map((file) => file.name), ['fine.log']);
  assert.deepEqual(result.skipped, [{ name: 'packed.log', reason: 'damaged' }]);
});

test('hubfixC F6: untouched stored and deflated entries still come back with the right text', async () => {
  const result = await zipRead.readZip(makeZip([
    { name: 'plain.log', data: 'plain\n', method: 0 },
    { name: 'packed.log', data: 'a b c\n'.repeat(50), method: 8 }
  ]));
  assert.equal(result.problem, '');
  assert.deepEqual(result.skipped, []);
  assert.deepEqual(result.files.map((file) => file.name), ['plain.log', 'packed.log']);
  assert.equal(await result.files[0].text(), 'plain\n');
  assert.equal(await result.files[1].text(), 'a b c\n'.repeat(50));
});

test('hubfixC F6: an empty stored entry (CRC-32 0) still comes back', async () => {
  const result = await zipRead.readZip(makeZip([{ name: 'empty.log', data: '', method: 0 }]));
  assert.equal(result.problem, '');
  assert.deepEqual(result.skipped, []);
  assert.deepEqual(result.files.map((file) => file.name), ['empty.log']);
  assert.equal(result.files[0].size, 0);
});
