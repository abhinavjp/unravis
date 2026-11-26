// Fails when a shipped binary carries the test-only switch (RV-SEC-7, W-41).
//
// The `e2e` build tag adds a way to make the pipe checks accept one more program: it reads UNRAVIS_E2E_ALLOW_IMAGE and defines
// pipesec.TestAllow (internal/pipesec/testallow_e2e.go). A normal build has neither. A release that contains either was built with
// -tags e2e, so anyone who can set an environment variable of the person's user could make the app trust their program.
//
//   node tools/ci/check-no-e2e-switch.mjs <file-or-folder>...
//
// A folder is searched for .exe and .dll files and for .zip files (their entries are read too, so the release zips are covered; the setup
// exe holds its payload raw, so the exe scan sees inside it). Exit codes: 0 clean, 1 a marker found or a file that could not be read,
// 2 nothing to scan (a check that looked at nothing is not a pass).
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { inflateRawSync } from 'node:zlib';

// The environment variable name, and the Go symbol of the function that the e2e build adds.
export const MARKERS = ['UNRAVIS_E2E_ALLOW_IMAGE', 'pipesec.TestAllow'];

const SCANNED = /\.(exe|dll|zip)$/i;

/** The markers found in a buffer, as plain text (UTF-8 and UTF-16 spellings both count). */
export function markersIn(buf) {
  const found = [];
  for (const m of MARKERS) {
    if (buf.includes(Buffer.from(m, 'latin1')) || buf.includes(Buffer.from(m, 'utf16le'))) found.push(m);
  }
  return found;
}

/** The entries of a zip as { name, data } (stored and deflated entries). Throws on anything it cannot read, so a check never passes by skipping. */
export function zipEntries(buf) {
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 22 - 0xffff); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error('not a zip file (no end record)');
  const count = buf.readUInt16LE(eocd + 10);
  const cdSize = buf.readUInt32LE(eocd + 12);
  const cdOffset = buf.readUInt32LE(eocd + 16);
  if (count === 0xffff || cdSize === 0xffffffff || cdOffset === 0xffffffff) throw new Error('zip64 files are not supported by this check');
  const out = [];
  let p = cdOffset;
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error('damaged zip directory');
    const method = buf.readUInt16LE(p + 10);
    const csize = buf.readUInt32LE(p + 20);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const local = buf.readUInt32LE(p + 42);
    const name = buf.toString('utf8', p + 46, p + 46 + nameLen);
    p += 46 + nameLen + extraLen + commentLen;
    if (name.endsWith('/')) continue;
    if (buf.readUInt32LE(local) !== 0x04034b50) throw new Error('damaged zip entry ' + name);
    const start = local + 30 + buf.readUInt16LE(local + 26) + buf.readUInt16LE(local + 28);
    const raw = buf.subarray(start, start + csize);
    if (method === 0) out.push({ name, data: raw });
    else if (method === 8) out.push({ name, data: inflateRawSync(raw) });
    else throw new Error('zip entry ' + name + ' uses an unsupported method ' + method);
  }
  return out;
}

/** One finding per file (or zip entry) that carries a marker, plus one per file that could not be read. */
export function scanFile(path) {
  const label = path.replace(/\\/g, '/');
  let buf;
  try {
    buf = readFileSync(path);
  } catch (e) {
    return [{ file: label, problem: 'cannot be read: ' + e.message }];
  }
  if (!/\.zip$/i.test(path)) {
    const m = markersIn(buf);
    return m.length ? [{ file: label, problem: 'contains ' + m.join(' and ') }] : [];
  }
  let entries;
  try {
    entries = zipEntries(buf);
  } catch (e) {
    return [{ file: label, problem: 'cannot be opened: ' + e.message }];
  }
  const out = [];
  for (const { name, data } of entries) {
    const m = markersIn(data);
    if (m.length) out.push({ file: label + '!' + name, problem: 'contains ' + m.join(' and ') });
  }
  return out;
}

function filesUnder(path) {
  if (!existsSync(path)) return null;
  if (!statSync(path).isDirectory()) return [path];
  const out = [];
  for (const e of readdirSync(path, { withFileTypes: true })) {
    const p = join(path, e.name);
    if (e.isDirectory()) out.push(...filesUnder(p));
    else if (SCANNED.test(e.name)) out.push(p);
  }
  return out;
}

/** Scans every given file or folder. Returns { scanned, findings }; a path that does not exist is a finding. */
export function scanPaths(paths) {
  const findings = [];
  let scanned = 0;
  for (const p of paths) {
    const files = filesUnder(p);
    if (files === null) {
      findings.push({ file: p, problem: 'does not exist' });
      continue;
    }
    for (const f of files) {
      scanned++;
      findings.push(...scanFile(f));
    }
  }
  return { scanned, findings };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const paths = process.argv.slice(2);
  if (paths.length === 0) {
    console.error('usage: node tools/ci/check-no-e2e-switch.mjs <file-or-folder>...');
    process.exit(2);
  }
  const { scanned, findings } = scanPaths(paths);
  for (const f of findings) console.error('FAIL ' + f.file + ' ' + f.problem);
  if (findings.length) {
    console.error('A shipped program must not carry the test-only switch (build without -tags e2e).');
    process.exit(1);
  }
  if (scanned === 0) {
    console.error('No .exe, .dll or .zip found in ' + paths.map((p) => basename(p) || p).join(', ') + ': nothing was checked.');
    process.exit(2);
  }
  console.log('ok - ' + scanned + ' file(s) checked, none carries the test-only switch');
}
