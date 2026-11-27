const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const path = require('node:path');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const { stripParts, techCards, usedLine } = loadTypeScript('src/lib/blackboxStatus.ts');
const { blackboxStatusCopyEntries } = loadTypeScript('src/lib/copy/blackboxStatusCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const when = (iso) => 'at ' + iso;
const GB = 1024 * 1024 * 1024;
const settings = (extra = {}) => ({ name: 'web-a', targets: [], ...extra });
const item = (facts, thisPc = true) => ({ id: 'a', name: 'web-a', thisPc, facts: { name: 'web-a', host: 'h', intervalSeconds: 10, state: 'running', files: 1, samples: 5, gaps: 0, ...facts } });
const pc = (extra = {}) => ({ available: true, scanned: true, installed: true, running: true, listed: true, sqlInstalled: true, sqlRunning: true, settings: settings(), ...extra });

test('used of the cap reads only what was read', () => {
  assert.equal(usedLine(item({ bytes: 412 * 1024 * 1024, maxGB: 5 })), '412.0 MB used of 5 GB');
  assert.equal(usedLine(item({ bytes: 2 * GB, maxGB: 7.5 })), '2.0 GB used of 7.5 GB');
  assert.equal(usedLine(item({ bytes: 2048 })), '2 KB recorded');
  assert.equal(usedLine(item({ bytes: GB, maxGB: 2.25 })), '1.0 GB used of 2.25 GB', 'a fractional cap is not rounded to one decimal');
  assert.equal(usedLine(item({})), '', 'no size read, no line');
  assert.equal(usedLine({ id: 'x', thisPc: false }), '', 'no facts, no line');
});

test('the strip lists size, start and keep window, and leaves out what is missing', () => {
  assert.deepEqual(stripParts(item({ bytes: 2048, maxGB: 5, keepDays: 14 }), '26 Nov 18:55'), ['2 KB used of 5 GB', 'Recording since 26 Nov 18:55', 'Kept for 14 days']);
  assert.deepEqual(stripParts(item({}), ''), []);
  assert.deepEqual(stripParts(item({ keepDays: 1 }), ''), ['Kept for 1 day']);
});

test('SQL card follows the recorder status and the service, never invents a good read', () => {
  const card = (facts, p) => techCards(item(facts), p, when).find((c) => c.id === 'sql');
  const good = card({ sql: { enabled: true, state: 'on', lastOK: '2026-11-27T09:00:00Z' } }, pc());
  assert.equal(good.tone, 'good'); assert.equal(good.word, 'collecting'); assert.match(good.sub, /at 2026-11-27T09:00:00Z/);
  assert.equal(card({ sql: { enabled: true, state: 'on' } }, pc()).word, 'waiting', 'on with no good read yet is waiting, not collecting');
  assert.equal(card({ sql: { enabled: true, state: 'denied' } }, pc()).word, 'needs access');
  assert.equal(card({ sql: { enabled: false, state: 'auto-off' } }, pc()).tone, 'bad');
  assert.equal(card({ sql: { enabled: false, state: 'off' } }, pc({ sqlInstalled: false })).word, 'off');
  const running = card({ sql: { enabled: false, state: 'off' } }, pc());
  assert.equal(running.word, 'waiting', 'service installed and running must not read as "not set up"');
  assert.ok(!/not set up/.test(running.note));
  const retry = card({ sql: { enabled: true, state: 'backing-off', lastOK: '2026-11-27T09:00:00Z', failures: 3 } }, pc());
  assert.equal(retry.word, 'retrying');
  assert.match(retry.note, /3 in a row/);
  assert.match(retry.sub, /Last good read/);
  const remoteOff = techCards(item({ sql: { enabled: false, state: 'off' } }, false), pc(), when).find((c) => c.id === 'sql');
  assert.ok(!/below|this PC/.test(remoteOff.note), 'a recorder elsewhere is not pointed at a control that is not on its page');
  const remoteDenied = techCards(item({ sql: { enabled: true, state: 'denied' } }, false), pc(), when).find((c) => c.id === 'sql');
  assert.ok(!/below/.test(remoteDenied.note));
  assert.equal(card({}, pc({ sqlInstalled: false })).word, 'off', 'service not installed');
  assert.equal(card({}, pc({ sqlRunning: false })).word, 'stopped', 'service installed, stopped');
  assert.equal(card({}, pc()), undefined, 'service running but the recorder reports nothing: no card, no claim');
  // Another server: only its own status page counts, never this PC's service.
  const other = techCards(item({ sql: { enabled: true, state: 'on', lastOK: '2026-11-27T09:00:00Z' } }, false), pc({ sqlInstalled: false }), when);
  assert.equal(other.find((c) => c.id === 'sql').word, 'collecting');
  assert.deepEqual(techCards(item({}, false), pc(), when), [], 'another server with no SQL report has no cards');
});

test('Redis and the watched sites come from this PC settings only', () => {
  const cards = techCards(item({}), pc({ settings: settings({ redis: { profile: 'deep', port: 6380 }, targets: [{ kind: 'appPool', name: 'a' }, { kind: 'appPool', name: 'b' }, { kind: 'service', name: 's' }] }) }), when);
  const redis = cards.find((c) => c.id === 'redis');
  assert.equal(redis.word, 'set to record'); assert.equal(redis.sub, 'Port 6380, Deep set'); assert.match(redis.note, /not shown here yet/);
  assert.equal(cards.find((c) => c.id === 'sites').note, '2 app pools and 1 service get their own processor and memory figures.');
  const off = techCards(item({}), pc({ settings: settings({ redis: {}, collectors: { 'redis.info': { on: false } } }) }), when).find((c) => c.id === 'redis');
  assert.equal(off.word, 'switched off');
  assert.equal(techCards(item({}), pc({ settings: settings() }), when).find((c) => c.id === 'sites').word, 'whole machine only');
  // The recorder leaves "targets" out when none are picked (Go omitempty), so a missing list must read as none.
  const noList = settings(); delete noList.targets;
  assert.equal(techCards(item({}), pc({ settings: noList }), when).find((c) => c.id === 'sites').word, 'whole machine only');
  assert.equal(techCards(item({}), pc({ settings: undefined }), when).some((c) => c.id === 'redis' || c.id === 'sites'), false);
  assert.equal(techCards(item({}, false), pc({ settings: settings({ redis: {} }) }), when).some((c) => c.id === 'redis'), false, 'another server never shows this PC settings');
});

test('no card exists for anything Unravis cannot read', () => {
  const ids = techCards(item({ sql: { enabled: true, state: 'on', lastOK: 'x' } }), pc({ settings: settings({ redis: {} }) }), when).map((c) => c.id);
  assert.deepEqual(ids, ['sql', 'redis', 'sites']);
});

test('the copy passes the copy lint and has no em dash', () => {
  const entries = blackboxStatusCopyEntries();
  assert.ok(entries.length > 20);
  for (const e of entries) assert.equal(e.text.includes('—'), false, e.id);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('the panel shows the cards in the Status view', () => {
  const src = fs.readFileSync(path.join(__dirname, '..', 'src', 'components', 'BlackboxPanel.svelte'), 'utf8');
  assert.match(src, /<StatusCards \{item\} \{thisPc\} \/>/);
});
