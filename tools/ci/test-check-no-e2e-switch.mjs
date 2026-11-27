// Self-test of tools/ci/check-no-e2e-switch.mjs (RV-SEC-7, W-41). Builds the real command twice (normal and with -tags e2e) in a
// temporary folder; no network.
//   node tools/ci/test-check-no-e2e-switch.mjs
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { deflateRawSync } from 'node:zlib';
import { MARKERS, zipEntries } from './check-no-e2e-switch.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
const script = join(here, 'check-no-e2e-switch.mjs');
let n = 0;
const test = (name, fn) => {
  fn();
  n++;
  console.log('ok - ' + name);
};

const check = (...paths) => spawnSync(process.execPath, [script, ...paths], { encoding: 'utf8' });

// A minimal zip writer: entries are [name, Buffer, 'store' | 'deflate'].
function makeZip(entries) {
  const locals = [];
  const central = [];
  let offset = 0;
  for (const [name, data, how] of entries) {
    const body = how === 'deflate' ? deflateRawSync(data) : data;
    const nameBuf = Buffer.from(name, 'utf8');
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(how === 'deflate' ? 8 : 0, 8);
    local.writeUInt32LE(body.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBuf.length, 26);
    const cd = Buffer.alloc(46);
    cd.writeUInt32LE(0x02014b50, 0);
    cd.writeUInt16LE(20, 4);
    cd.writeUInt16LE(20, 6);
    cd.writeUInt16LE(how === 'deflate' ? 8 : 0, 10);
    cd.writeUInt32LE(body.length, 20);
    cd.writeUInt32LE(data.length, 24);
    cd.writeUInt16LE(nameBuf.length, 28);
    cd.writeUInt32LE(offset, 42);
    locals.push(local, nameBuf, body);
    central.push(cd, nameBuf);
    offset += local.length + nameBuf.length + body.length;
  }
  const cdBuf = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(cdBuf.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, cdBuf, end]);
}

const work = mkdtempSync(join(tmpdir(), 'no-e2e-'));
try {
  const clean = Buffer.from('MZ plain program text, nothing special here');
  const dirty = Buffer.concat([clean, Buffer.from('\0' + MARKERS[0] + '\0')]);

  test('zip reader returns stored and deflated entries', () => {
    const got = zipEntries(makeZip([['a.exe', clean, 'store'], ['b.exe', dirty, 'deflate']]));
    assert.deepEqual(got.map((e) => e.name), ['a.exe', 'b.exe']);
    assert.ok(got[1].data.equals(dirty));
  });

  test('a clean exe passes', () => {
    writeFileSync(join(work, 'clean.exe'), clean);
    const r = check(join(work, 'clean.exe'));
    assert.equal(r.status, 0, r.stderr);
  });

  for (const m of MARKERS) {
    test('an exe with ' + m + ' fails and names the file', () => {
      writeFileSync(join(work, 'bad.exe'), Buffer.concat([clean, Buffer.from(m)]));
      const r = check(join(work, 'bad.exe'));
      assert.equal(r.status, 1);
      assert.match(r.stderr, /bad\.exe contains /);
      assert.ok(r.stderr.includes(m));
    });
  }

  test('the Windows (UTF-16) spelling counts too', () => {
    writeFileSync(join(work, 'wide.exe'), Buffer.concat([clean, Buffer.from(MARKERS[0], 'utf16le')]));
    assert.equal(check(join(work, 'wide.exe')).status, 1);
  });

  test('a marker inside a stored or deflated zip entry fails', () => {
    for (const how of ['store', 'deflate']) {
      writeFileSync(join(work, 'rel.zip'), makeZip([['ok.exe', clean, 'store'], ['unravis.exe', dirty, how]]));
      const r = check(join(work, 'rel.zip'));
      assert.equal(r.status, 1, how);
      assert.match(r.stderr, /rel\.zip!unravis\.exe contains/);
    }
    writeFileSync(join(work, 'rel.zip'), makeZip([['unravis.exe', clean, 'deflate']]));
    assert.equal(check(join(work, 'rel.zip')).status, 0);
  });

  test('a folder is searched below its subfolders, and only .exe .dll .zip are read', () => {
    const d = join(work, 'tree');
    mkdirSync(join(d, 'sub'), { recursive: true });
    writeFileSync(join(d, 'notes.txt'), dirty);
    writeFileSync(join(d, 'sub', 'a.exe'), clean);
    assert.equal(check(d).status, 0);
    writeFileSync(join(d, 'sub', 'b.dll'), dirty);
    const r = check(d);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /b\.dll/);
  });

  test('nothing to scan, a missing path and a damaged zip all fail (a check that looked at nothing is not a pass)', () => {
    const empty = join(work, 'empty');
    mkdirSync(empty);
    assert.equal(check(empty).status, 2);
    assert.equal(check().status, 2);
    assert.equal(check(join(work, 'nope.exe')).status, 1);
    writeFileSync(join(work, 'broken.zip'), Buffer.from('this is not a zip file at all'));
    const r = check(join(work, 'broken.zip'));
    assert.equal(r.status, 1);
    assert.match(r.stderr, /cannot be opened/);
  });

  // The real thing: the same source built without and with the e2e tag.
  const build = (out, tags) => {
    const args = ['build', ...(tags ? ['-tags', tags] : []), '-o', out, './cmd/unravis'];
    const r = spawnSync('go', args, { cwd: repoRoot, encoding: 'utf8' });
    assert.equal(r.status, 0, 'go ' + args.join(' ') + ': ' + r.stderr);
  };
  const exe = '.exe'; // the check reads files by their .exe name on every OS
  test('a normal build of cmd/unravis passes', () => {
    build(join(work, 'real-normal' + exe), '');
    const r = check(join(work, 'real-normal' + exe));
    assert.equal(r.status, 0, r.stderr);
  });
  test('the same build with -tags e2e fails', () => {
    build(join(work, 'real-e2e' + exe), 'e2e');
    const r = check(join(work, 'real-e2e' + exe));
    assert.equal(r.status, 1);
    assert.match(r.stderr, /real-e2e\.exe contains UNRAVIS_E2E_ALLOW_IMAGE/);
  });

  test('the release workflow and the published-release check both run the scan', () => {
    for (const f of ['release.yml', 'verify-release.yml']) {
      const text = readFileSync(join(repoRoot, '.github', 'workflows', f), 'utf8');
      assert.ok(text.includes('tools/ci/check-no-e2e-switch.mjs'), f + ' does not run tools/ci/check-no-e2e-switch.mjs');
    }
  });
} finally {
  rmSync(work, { recursive: true, force: true });
}
console.log(n + ' passed');
