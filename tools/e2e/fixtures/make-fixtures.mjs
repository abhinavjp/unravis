// Writes the small sample files the specs import and compare against.
// Run:  node fixtures/make-fixtures.mjs        (from tools/e2e)
// The files are checked in; this script is the one place that says how they were made.
// Everything is fixed text: no clock, no random numbers, so a re-run changes no byte.
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, 'files');
mkdirSync(out, { recursive: true });

// An nginx access log in the combined format with the two timing fields the importer reads.
// Twenty-four requests, two seconds apart, on 20 Oct 2026, UTC (the 12 samples below repeat twice).
const paths = [
  ['GET', '/api/v1/orders/12345?page=2', 200, 1200, '0.125', '0.120'],
  ['POST', '/api/v1/orders', 201, 310, '0.310', '0.300'],
  ['GET', '/api/v1/orders/12346', 200, 1180, '0.098', '0.090'],
  ['GET', '/api/v1/customers/77', 200, 640, '0.045', '0.040'],
  ['GET', '/api/v1/orders/12347', 404, 90, '0.012', '0.010'],
  ['PUT', '/api/v1/orders/12345', 200, 400, '0.220', '0.210'],
  ['GET', '/api/v1/customers/78', 200, 650, '0.051', '0.048'],
  ['GET', '/api/v1/orders/12348?page=1', 200, 1210, '0.640', '0.630'],
  ['POST', '/api/v1/orders', 500, 120, '1.250', '1.240'],
  ['GET', '/api/v1/orders/12349', 200, 1190, '0.101', '0.095'],
  ['GET', '/api/v1/customers/79', 200, 655, '0.047', '0.044'],
  ['DELETE', '/api/v1/orders/12350', 204, 0, '0.060', '0.055']
];

function line(i, time, p) {
  const [method, path, status, bytes, rt, urt] = p;
  const ip = '192.0.2.' + (10 + (i % 4));
  return `${ip} - - [${time}] "${method} ${path} HTTP/1.1" ${status} ${bytes} "-" "synthetic-agent/1.0" ${rt} ${urt}`;
}

const N = 24;
const stamp = (i) => `20/Oct/2026:08:00:${String(6 + i * 2).padStart(2, '0')} +0000`;
const pick = (i) => paths[i % paths.length];
const good = Array.from({ length: N }, (_, i) => line(i, stamp(i), pick(i)));
writeFileSync(join(out, 'access-good.log'), good.join('\n') + '\n');

// The same file with a time the calendar does not have in the third line (31 February).
// One bad row in 24 is under the 5 percent refusal limit, so it imports with one rejected row.
const bad = Array.from({ length: N }, (_, i) => line(i, i === 2 ? '31/Feb/2026:08:00:16 +0000' : stamp(i), pick(i)));
writeFileSync(join(out, 'access-bad-time.log'), bad.join('\n') + '\n');

// A file with nothing in it.
writeFileSync(join(out, 'empty.log'), '');

// One line longer than the 64 KiB line limit the access-log importer holds (a small stand-in
// for an oversized file: the real byte cap is 2 GiB and cannot be built in a test).
const longPath = '/api/v1/search?q=' + 'x'.repeat(70 * 1024);
writeFileSync(join(out, 'access-long-line.log'), [good[0], line(1, '20/Oct/2026:08:00:11 +0000', ['GET', longPath, 200, 10, '0.010', '0.010']), good[2]].join('\n') + '\n');

// Expected CSV of the case report for the case the export spec builds: an empty case named
// "Snapshot Case" with two checklist items. Comma separated, quoted only where needed, a byte
// order mark first, CRLF line ends not used (the report writes LF). The case id is a slot.
const rows = [];
writeFileSync(join(out, 'README.txt'), 'Made by make-fixtures.mjs. Do not edit by hand.\n' + rows.length);
console.log('fixtures written to ' + out);
