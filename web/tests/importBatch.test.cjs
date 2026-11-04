const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      for (const ext of ['.ts', '.svelte.ts']) {
        if (fs.existsSync(path.join(path.dirname(filename), request + ext))) return loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + ext)));
      }
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const cases = loadTypeScript('src/lib/cases.ts');
const copy = loadTypeScript('src/lib/copy/importCopy.ts');
const readiness = loadTypeScript('src/lib/readiness.ts');
const zipRead = loadTypeScript('src/lib/zipRead.ts');

// Synthetic values only.
const record = (extra = {}) => ({ id: 3, importer: 'iis-w3c', fileName: 'web-a.log', sha256: 'f'.repeat(64), bytes: 17, rows: 5, rejected: 0, hostLabel: 'web-a', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2001-02-03T04:05:06Z', status: 'ok', supplied: {}, ...extra });
const answer = (extra = {}, rec = {}) => ({ importer: 'iis-w3c', score: 0.9, tied: [], report: null, record: record(rec), ...extra });
const item = (key, importer = '') => ({ key, file: new File(['x'], key + '.log'), importer, host: '' });
const noSleep = async () => {};

/* ------------------------------------------------------------------ verdicts */

test('an import with every row kept is Used', () => {
  const outcome = cases.classifyAnswer(answer());
  assert.equal(outcome.verdict, 'used');
  assert.equal(outcome.rows, 5);
});

test('an import that left some rows out is Partly used, with the most common row reason', () => {
  const report = { errors: [{ reason: 'bad-time' }, { reason: 'bad-time' }, { reason: 'short-row' }] };
  const outcome = cases.classifyAnswer(answer({ report }, { rejected: 3 }));
  assert.equal(outcome.verdict, 'partly');
  assert.equal(outcome.rowReason, 'bad-time');
  assert.equal(outcome.rejected, 3);
});

test('a duplicate, a quarantined file and a file with no rows are Rejected', () => {
  const dup = cases.classifyAnswer(answer({}, { status: 'duplicate' }));
  assert.deepEqual([dup.verdict, dup.reason], ['rejected', 'duplicate']);
  const quarantined = cases.classifyAnswer(answer({}, { status: 'quarantined', reason: 'no-time-column' }));
  assert.equal(quarantined.verdict, 'rejected');
  assert.equal(quarantined.reason, 'no-time-column');
  const empty = cases.classifyAnswer(answer({}, { rows: 0, rejected: 4 }));
  assert.equal(empty.verdict, 'rejected');
  assert.equal(empty.reason, 'no-rows');
  assert.equal(cases.classifyAnswer(answer({ record: null })).reason, 'import-failed');
});

test('a refusal is Rejected with its reason; busy, slow and unreachable ones may pass on another try', () => {
  const refused = cases.classifyFailure(new cases.CaseApiError('no', 422, 'importer-required'));
  assert.equal(refused.verdict, 'rejected');
  assert.equal(refused.transient, false);
  assert.equal(cases.classifyFailure(new cases.CaseApiError('busy', 409, 'case-import-running')).transient, true);
  assert.equal(cases.classifyFailure(new cases.CaseApiError('down', 503, '')).transient, true);
  const lost = cases.classifyFailure(new TypeError('Failed to fetch'));
  assert.equal(lost.reason, 'unreachable');
  assert.equal(lost.transient, true);
});

test('the tally counts each verdict', () => {
  assert.deepEqual(cases.tallyBatch([{ verdict: 'used' }, { verdict: 'used' }, { verdict: 'partly' }, { verdict: 'rejected' }]), { used: 2, partly: 1, rejected: 1 });
  assert.equal(copy.batchSummaryText({ used: 2, partly: 1, rejected: 0 }), 'Finished: 2 used, 1 partly used.');
  assert.equal(copy.batchSummaryText({ used: 0, partly: 0, rejected: 0 }), '');
});

/* ------------------------------------------------------------------ the queue */

test('files go one after the other, in the order given', async () => {
  const seen = [];
  let running = 0;
  let most = 0;
  const done = await cases.runImportQueue([item('a'), item('b'), item('c')], async (it) => {
    running++; most = Math.max(most, running);
    await new Promise((resolve) => setTimeout(resolve, 5));
    seen.push(it.key);
    running--;
    return answer();
  });
  assert.deepEqual(seen, ['a', 'b', 'c']);
  assert.deepEqual(done.map((entry) => entry.key), ['a', 'b', 'c']);
  assert.equal(most, 1);
});

test('one file failing does not stop the others', async () => {
  const starts = [];
  const done = await cases.runImportQueue([item('a'), item('b'), item('c'), item('d')], async (it) => {
    if (it.key === 'b') throw new cases.CaseApiError('no', 422, 'importer-required');
    if (it.key === 'c') throw new TypeError('Failed to fetch');
    return answer();
  }, { onStart: (key) => starts.push(key) });
  assert.deepEqual(starts, ['a', 'b', 'c', 'd']);
  assert.deepEqual(done.map((entry) => entry.outcome.verdict), ['used', 'rejected', 'rejected', 'used']);
});

test('a busy case makes the same file wait and go again, then it is Used', async () => {
  let tries = 0;
  const waits = [];
  const done = await cases.runImportQueue([item('a')], async () => {
    tries++;
    if (tries < 3) throw new cases.CaseApiError('busy', 409, 'case-import-running');
    return answer();
  }, { sleep: async (ms) => { waits.push(ms); }, busyWaitMs: 7 });
  assert.equal(tries, 3);
  assert.deepEqual(waits, [7, 7]);
  assert.equal(done[0].outcome.verdict, 'used');
});

test('a case that stays busy ends as a Rejected file that can be tried again, and the queue goes on', async () => {
  let tries = 0;
  const done = await cases.runImportQueue([item('a'), item('b')], async (it) => {
    if (it.key === 'a') { tries++; throw new cases.CaseApiError('busy', 409, 'case-import-running'); }
    return answer();
  }, { sleep: noSleep, busyRetries: 2 });
  assert.equal(tries, 3);
  assert.equal(done[0].outcome.verdict, 'rejected');
  assert.equal(done[0].outcome.transient, true);
  assert.equal(done[1].outcome.verdict, 'used');
});

test('stopping keeps the finished files and starts no more', async () => {
  const stop = new AbortController();
  const starts = [];
  const done = await cases.runImportQueue([item('a'), item('b'), item('c')], async (it) => {
    if (it.key === 'a') stop.abort();
    return answer();
  }, { signal: stop.signal, onStart: (key) => starts.push(key) });
  assert.deepEqual(starts, ['a']);
  assert.deepEqual(done.map((entry) => entry.key), ['a']);
});

test('an abort while a file is going leaves that file without a verdict', async () => {
  const stop = new AbortController();
  const done = await cases.runImportQueue([item('a'), item('b')], async (it) => {
    if (it.key === 'b') { stop.abort(); throw new DOMException('stopped', 'AbortError'); }
    return answer();
  }, { signal: stop.signal });
  assert.deepEqual(done.map((entry) => entry.key), ['a']);
});

/* ------------------------------------------------------------------ the words */

test('every verdict has a plain reason, and a refusal is told in the app words', () => {
  assert.equal(copy.batchReasonText(cases.classifyAnswer(answer())), 'Kept 5 rows.');
  const partly = cases.classifyAnswer(answer({ report: { errors: [{ reason: 'bad-time' }] } }, { rows: 4, rejected: 1 }));
  assert.match(copy.batchReasonText(partly), /^Kept 4 rows\. 1 row was left out\./);
  const dup = cases.classifyAnswer(answer({}, { status: 'duplicate' }));
  assert.equal(copy.batchReasonText(dup), copy.errorText({ status: 0, reason: 'duplicate', importId: 3, message: '' }));
  assert.ok(copy.batchReasonText(cases.classifyAnswer(answer({}, { rows: 0 }))).length > 10);
  assert.deepEqual(['used', 'partly', 'rejected'].map(copy.verdictLabel), ['Used', 'Partly used', 'Rejected']);
});

test('the batch copy is part of the copy lint list', () => {
  const entries = copy.importCopyEntries().filter((entry) => entry.id.startsWith('import.batch'));
  assert.ok(entries.length >= 20);
});

/* ------------------------------------------------------------------ the screens */

test('the drawer embeds the batch section only for an open case, keyed by the case', () => {
  const panel = read('src/lib/components/ImportPanel.svelte');
  assert.match(panel, /import ImportBatch from '\.\/ImportBatch\.svelte'/);
  assert.match(panel, /\{#if selected && !selected\.closed\}\s*\{#key selected\.id\}\s*<ImportBatch caseId=\{selected\.id\}/);
});

test('the batch sends the type only when the person changed it, and acknowledges the notice', () => {
  const batch = read('src/lib/components/ImportBatch.svelte');
  assert.match(batch, /importer: row\.changed \? importerFor\(row\.choice\) : ''/);
  assert.match(batch, /ackTextFor\(\)/);
  assert.match(batch, /runImportQueue\(/);
  assert.match(batch, /type="checkbox" bind:checked=\{acknowledged\}/);
});

test('the panel in drawer mode opens zips, keeps the list and offers Try again; the wizard path is unchanged', () => {
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.match(panel, /readZip\(file\)/);
  assert.match(panel, /BATCH_TEXT\.tryAgain/);
  assert.match(panel, /addStagedFiles\(ready\)/);
  assert.match(panel, /<ZoneChoice /);
  assert.match(panel, /webkitdirectory/);
  assert.match(panel, /ondrop=/);
});

/* ------------------------------------------------------------------ one readiness vocabulary */

test('the readiness words are the four of the plan', () => {
  assert.deepEqual([...readiness.READINESS_WORDS], ['Ready', 'Partly ready', 'Cannot test yet', 'Failed']);
});

test('a found source is Ready only when it was verified; the rest cannot be tested yet', () => {
  assert.equal(readiness.foundReadiness('ready'), 'Ready');
  assert.equal(readiness.foundReadiness('unverified'), 'Cannot test yet');
  assert.equal(readiness.foundReadiness('steps'), 'Cannot test yet');
});

test('a word the page does not know is never shown as a pass', () => {
  for (const word of readiness.READINESS_WORDS) assert.equal(readiness.toReadinessWord(word), word);
  for (const other of ['', 'ok', 'READY', null, undefined, 3, 'Not checked']) assert.equal(readiness.toReadinessWord(other), 'Cannot test yet');
});

test('the wizard chips use the shared words and keep a tooltip', () => {
  const gallery = read('src/components/wizard/SourceGallery.svelte');
  assert.match(gallery, /foundReadiness\(entry\.state\)/);
  assert.match(gallery, /title=\{chipTipText\(entry\.state\)\}/);
  const wizardCopy = loadTypeScript('src/lib/copy/wizardSourcesCopy.ts');
  for (const state of ['ready', 'unverified', 'steps']) assert.ok(wizardCopy.chipTipText(state).length > 10);
});

/* ------------------------------------------------------------------ the zip reader */

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

test('a zip of logs becomes files, with deflate and stored entries, folders and junk left out', async () => {
  const zip = makeZip([
    { name: 'day1/u_ex261020.log', data: 'a b c\n'.repeat(100) },
    { name: 'day1/', data: '', method: 0 },
    { name: 'day2/httperr1.log', data: 'hello\n', method: 0 },
    { name: '__MACOSX/day1/._u_ex261020.log', data: 'junk' },
    { name: 'Thumbs.db', data: 'junk' }
  ]);
  const result = await zipRead.readZip(zip);
  assert.equal(result.problem, '');
  assert.deepEqual(result.files.map((file) => file.name), ['u_ex261020.log', 'httperr1.log']);
  assert.equal(result.files[0].size, 600);
  assert.equal(await result.files[1].text(), 'hello\n');
  assert.equal(result.files[0].webkitRelativePath, 'logs.zip/day1/u_ex261020.log');
  assert.deepEqual(result.skipped, []);
});

test('a zip with a path that climbs out, or an encrypted entry, skips those and keeps the rest', async () => {
  const result = await zipRead.readZip(makeZip([
    { name: '../evil.log', data: 'x' },
    { name: 'C:/evil.log', data: 'x' },
    { name: 'locked.log', data: 'x', flags: 1 },
    { name: 'fine.log', data: 'ok' }
  ]));
  assert.deepEqual(result.files.map((file) => file.name), ['fine.log']);
  assert.deepEqual(result.skipped.map((skip) => skip.reason).sort(), ['encrypted', 'unsafe-path', 'unsafe-path']);
});

test('a file that is not a zip, or a damaged one, is reported and never throws', async () => {
  assert.equal((await zipRead.readZip(new File(['just text, not a zip at all, long enough to look at'], 'x.zip'))).problem, 'not-zip');
  const good = makeZip([{ name: 'a.log', data: 'hello hello hello hello' }]);
  const bytes = Buffer.from(await good.arrayBuffer());
  bytes.writeUInt32LE(bytes.readUInt32LE(bytes.length - 6) + 5000, bytes.length - 6);
  const broken = await zipRead.readZip(new File([bytes], 'bad.zip'));
  assert.ok(broken.problem === 'damaged' || broken.files.length === 0);
});

test('only names ending in .zip are tried as zips', () => {
  assert.equal(zipRead.isZipName('Logs.ZIP'), true);
  assert.equal(zipRead.isZipName('logs.zip.txt'), false);
  assert.equal(zipRead.safeZipPath('a\\b/../c'), null);
  assert.equal(zipRead.safeZipPath('a\\b.log'), 'a/b.log');
});
