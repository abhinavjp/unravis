/**
 * Unpacks a .zip chosen on the import drawer, in the page, so the server never gets an archive (it refuses them by their first bytes).
 * Only the two methods every zip tool writes are read: stored and deflate. Anything else is skipped with a reason, never guessed.
 * Safe by design: the file list is read from the central directory, a path with .. or a drive is skipped, the number of files and the
 * bytes they unpack to are capped, and the real unpacked size is counted as it streams, so a lying header cannot unpack more than it said,
 * and the bytes are checked against the CRC-32 the directory declares, so a changed byte is damaged, not evidence.
 * Pure of the DOM except for File, Blob and DecompressionStream, which a test gets from node. Nothing is sent anywhere from here.
 */

export type ZipSkipReason = 'folder' | 'unsafe-path' | 'encrypted' | 'unsupported' | 'too-big' | 'damaged' | 'junk';

export interface ZipSkip { name: string; reason: ZipSkipReason }

export interface ZipResult {
  files: File[];
  skipped: ZipSkip[];
  /** The zip holds more files than MAX_ZIP_FILES; only the first ones were unpacked. */
  truncated: boolean;
  /** The zip as a whole could not be read: 'not-zip' (no directory at the end), 'damaged' (the directory is wrong), 'too-big' (over the total cap). */
  problem: '' | 'not-zip' | 'damaged' | 'too-big';
}

/** Most files one zip may bring. A day of logs for a few servers is a few hundred. */
export const MAX_ZIP_FILES = 1000;
/** Most bytes one file may unpack to, and most bytes all of them may unpack to together. They are held in memory until imported. */
export const MAX_ZIP_ENTRY_BYTES = 512 * 1024 * 1024;
export const MAX_ZIP_TOTAL_BYTES = 1024 * 1024 * 1024;

const SIG_EOCD = 0x06054b50;
const SIG_CENTRAL = 0x02014b50;
const SIG_LOCAL = 0x04034b50;
/** The end record is 22 bytes and may be followed by a comment of up to 65535 bytes. */
const EOCD_SEARCH = 22 + 65535;

const text = new TextDecoder('utf-8');
const latin = new TextDecoder('windows-1252');

/** True for a name that should be tried as a zip: it ends in .zip (any case). */
export const isZipName = (name: string): boolean => /\.zip$/i.test(name.trim());

const slashed = (name: string): string => name.replace(/\\/g, '/');

/** The clean relative path inside the zip, or null when it climbs out (.., a drive letter, a leading slash). */
export function safeZipPath(raw: string): string | null {
  const path = slashed(raw);
  if (path === '' || /^[a-zA-Z]:/.test(path) || path.startsWith('/')) return null;
  const parts = path.split('/').filter((part) => part !== '' && part !== '.');
  if (parts.length === 0 || parts.some((part) => part === '..')) return null;
  return parts.join('/');
}

const isJunk = (path: string): boolean => path.startsWith('__MACOSX/') || /(^|\/)(\.DS_Store|Thumbs\.db|desktop\.ini)$/i.test(path);

interface Entry {
  rawName: string;
  flags: number;
  method: number;
  /** The CRC-32 of the unpacked bytes, as the central directory declares it. */
  crc: number;
  compressed: number;
  size: number;
  offset: number;
  modified: number;
}

/** DOS date and time (as zip stores them, local time with no zone) as a millisecond stamp. 0 when the fields are not a date. */
function dosTime(date: number, time: number): number {
  const year = 1980 + (date >> 9), month = ((date >> 5) & 15) - 1, day = date & 31;
  const hour = time >> 11, minute = (time >> 5) & 63, second = (time & 31) * 2;
  const stamp = new Date(year, month, day, hour, minute, second).getTime();
  return Number.isFinite(stamp) ? stamp : 0;
}

async function bytesOf(blob: Blob): Promise<Uint8Array> {
  return new Uint8Array(await blob.arrayBuffer());
}

/** Reads the file list of the zip. Returns a problem when the file is not a readable zip. */
async function readDirectory(file: Blob): Promise<{ entries: Entry[]; problem: ZipResult['problem'] }> {
  const size = file.size;
  if (size < 22) return { entries: [], problem: 'not-zip' };
  const tailStart = Math.max(0, size - EOCD_SEARCH);
  const tail = await bytesOf(file.slice(tailStart, size));
  const view = new DataView(tail.buffer, tail.byteOffset, tail.byteLength);
  let at = -1;
  for (let i = tail.length - 22; i >= 0; i--) {
    if (view.getUint32(i, true) === SIG_EOCD) { at = i; break; }
  }
  if (at < 0) return { entries: [], problem: 'not-zip' };
  const count = view.getUint16(at + 10, true);
  const length = view.getUint32(at + 12, true);
  const start = view.getUint32(at + 16, true);
  // A zip64 archive (or one with more than 65534 files) marks these with all ones. It is not read.
  if (count === 0xffff || length === 0xffffffff || start === 0xffffffff) return { entries: [], problem: 'damaged' };
  if (start + length > size) return { entries: [], problem: 'damaged' };
  const directory = await bytesOf(file.slice(start, start + length));
  const dv = new DataView(directory.buffer, directory.byteOffset, directory.byteLength);
  const entries: Entry[] = [];
  let pos = 0;
  for (let i = 0; i < count; i++) {
    if (pos + 46 > directory.length || dv.getUint32(pos, true) !== SIG_CENTRAL) return { entries, problem: 'damaged' };
    const flags = dv.getUint16(pos + 8, true);
    const nameLength = dv.getUint16(pos + 28, true), extraLength = dv.getUint16(pos + 30, true), commentLength = dv.getUint16(pos + 32, true);
    if (pos + 46 + nameLength > directory.length) return { entries, problem: 'damaged' };
    const nameBytes = directory.subarray(pos + 46, pos + 46 + nameLength);
    // Bit 11 says the name is UTF-8; otherwise it is the old DOS code page, which windows-1252 reads close enough for a file name.
    const rawName = (flags & 0x800 ? text : latin).decode(nameBytes);
    entries.push({
      rawName, flags, method: dv.getUint16(pos + 10, true), modified: dosTime(dv.getUint16(pos + 14, true), dv.getUint16(pos + 12, true)),
      crc: dv.getUint32(pos + 16, true), compressed: dv.getUint32(pos + 20, true), size: dv.getUint32(pos + 24, true), offset: dv.getUint32(pos + 42, true)
    });
    pos += 46 + nameLength + extraLength + commentLength;
  }
  return { entries, problem: '' };
}

/** A transform that fails once more than `limit` bytes have passed, so a header that understates the size cannot unpack more. */
function limiter(limit: number): TransformStream<Uint8Array, Uint8Array> {
  let seen = 0;
  return new TransformStream({
    transform(chunk, controller) {
      seen += chunk.byteLength;
      if (seen > limit) { controller.error(new RangeError('zip-entry-too-big')); return; }
      controller.enqueue(chunk);
    }
  });
}

/** The table for the CRC-32 every zip entry declares (the standard zip/PNG one, polynomial 0xedb88320), built once at load. */
const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let i = 0; i < 256; i++) {
    let c = i;
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
    table[i] = c >>> 0;
  }
  return table;
})();

/** Feeds one chunk into a running CRC-32 (started at 0xffffffff; the final value is `(running ^ 0xffffffff) >>> 0`). */
function crcStep(running: number, chunk: Uint8Array): number {
  for (let i = 0; i < chunk.byteLength; i++) running = CRC_TABLE[(running ^ chunk[i]) & 0xff] ^ (running >>> 8);
  return running >>> 0;
}

/** A transform that passes the unpacked bytes through and, at the end, fails the stream when their CRC-32 is not the one the zip declared. */
function crcCheck(crc: number): TransformStream<Uint8Array, Uint8Array> {
  let running = 0xffffffff;
  return new TransformStream({
    transform(chunk, controller) {
      running = crcStep(running, chunk);
      controller.enqueue(chunk);
    },
    flush(controller) {
      if (((running ^ 0xffffffff) >>> 0) !== (crc >>> 0)) controller.error(new Error('zip-crc-mismatch'));
    }
  });
}

async function unpack(file: Blob, entry: Entry): Promise<Blob | 'damaged'> {
  const header = await bytesOf(file.slice(entry.offset, entry.offset + 30));
  const dv = new DataView(header.buffer, header.byteOffset, header.byteLength);
  if (header.length < 30 || dv.getUint32(0, true) !== SIG_LOCAL) return 'damaged';
  const from = entry.offset + 30 + dv.getUint16(26, true) + dv.getUint16(28, true);
  if (from + entry.compressed > file.size) return 'damaged';
  const body = file.slice(from, from + entry.compressed);
  // A stored entry is its own unpacked bytes: a header that declares a different size is lying, so it is damaged.
  if (entry.method === 0) {
    if (entry.compressed !== entry.size) return 'damaged';
    // The bytes are checked against the CRC-32 the directory declared, one chunk at a time: a flipped byte is damage, not evidence.
    let running = 0xffffffff;
    const reader = body.stream().getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      running = crcStep(running, value);
    }
    return ((running ^ 0xffffffff) >>> 0) === (entry.crc >>> 0) ? body : 'damaged';
  }
  try {
    const stream = body.stream().pipeThrough(new DecompressionStream('deflate-raw')).pipeThrough(limiter(entry.size)).pipeThrough(crcCheck(entry.crc));
    const blob = await new Response(stream).blob();
    // The header said how big it would be; a different size means the data is not what the zip claims.
    return blob.size === entry.size ? blob : 'damaged';
  } catch {
    return 'damaged';
  }
}

/** The base name of a path inside the zip. */
const leaf = (path: string): string => path.slice(path.lastIndexOf('/') + 1);

/**
 * Unpacks a zip into files. The list shows `archive.zip/inner/path.log` for each (as a folder pick shows a path), and each file keeps its
 * own name for the kind guess. Never throws: a zip that cannot be read comes back with a `problem`.
 */
export async function readZip(zip: File): Promise<ZipResult> {
  const out: ZipResult = { files: [], skipped: [], truncated: false, problem: '' };
  let directory: Awaited<ReturnType<typeof readDirectory>>;
  try { directory = await readDirectory(zip); } catch { out.problem = 'damaged'; return out; }
  if (directory.problem) { out.problem = directory.problem; return out; }
  let total = 0;
  for (const entry of directory.entries) {
    const path = safeZipPath(entry.rawName);
    if (/[\\/]$/.test(entry.rawName)) { continue; }
    if (path === null) { out.skipped.push({ name: entry.rawName, reason: 'unsafe-path' }); continue; }
    if (isJunk(path)) { continue; }
    if (out.files.length >= MAX_ZIP_FILES) { out.truncated = true; break; }
    if (entry.flags & 1) { out.skipped.push({ name: path, reason: 'encrypted' }); continue; }
    if (entry.method !== 0 && entry.method !== 8) { out.skipped.push({ name: path, reason: 'unsupported' }); continue; }
    if (entry.size === 0xffffffff || entry.compressed === 0xffffffff) { out.skipped.push({ name: path, reason: 'unsupported' }); continue; }
    if (entry.size > MAX_ZIP_ENTRY_BYTES) { out.skipped.push({ name: path, reason: 'too-big' }); continue; }
    if (total + entry.size > MAX_ZIP_TOTAL_BYTES) { out.problem = 'too-big'; break; }
    let blob: Blob | 'damaged';
    try { blob = await unpack(zip, entry); } catch { blob = 'damaged'; }
    if (blob === 'damaged') { out.skipped.push({ name: path, reason: 'damaged' }); continue; }
    total += blob.size;
    const made = new File([blob], leaf(path), { lastModified: entry.modified || zip.lastModified });
    try { Object.defineProperty(made, 'webkitRelativePath', { value: zip.name + '/' + path, configurable: true }); } catch { /* the plain name is used */ }
    out.files.push(made);
  }
  return out;
}
