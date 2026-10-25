const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Clumsy-user tests for the More filters panel: the group rail, the counts, the typed range boxes and the polling-type checkbox.
// Everything runs the pure modules with hostile input. All data is synthetic.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const rail = loadTypeScript('src/lib/filterRail.ts');
const depth = loadTypeScript('src/lib/depth.ts');
const inv = loadTypeScript('src/lib/investigation.ts');
const time = loadTypeScript('src/lib/time.ts');

const zeros = () => ({ what: 0, details: 0, ranges: 0, chart: 0 });
const GROUPS = ['what', 'details', 'ranges', 'chart'];
const baseQuery = () => time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);

// ---------------------------------------------------------------------------------------------------------------------
// The group rail
// ---------------------------------------------------------------------------------------------------------------------
test('the panel opens on When when nothing is in use, whatever the counts look like', () => {
  assert.equal(rail.firstActiveGroup(zeros()), 'when');
  // Counts that are missing, zero, negative or not numbers are not "in use".
  for (const bad of [undefined, null, 0, -1, -1e9, NaN, -Infinity]) {
    assert.equal(rail.firstActiveGroup({ what: bad, details: bad, ranges: bad, chart: bad }), 'when', String(bad));
  }
  assert.equal(rail.firstActiveGroup({}), 'when', 'a counts object with no keys');
});

test('the panel opens on the first group in rail order that holds a filter: all, several, or only the last', () => {
  assert.equal(rail.firstActiveGroup({ what: 1, details: 1, ranges: 1, chart: 1 }), 'what');
  assert.equal(rail.firstActiveGroup({ what: 5, details: 9, ranges: 3, chart: 2 }), 'what');
  assert.equal(rail.firstActiveGroup({ what: 0, details: 4, ranges: 0, chart: 1 }), 'details');
  assert.equal(rail.firstActiveGroup({ what: 0, details: 0, ranges: 2, chart: 7 }), 'ranges');
  assert.equal(rail.firstActiveGroup({ what: 0, details: 0, ranges: 0, chart: 1 }), 'chart');
  assert.equal(rail.firstActiveGroup({ what: 1e9, details: 0, ranges: 0, chart: 0 }), 'what');
  // Whatever is in the counts, the answer is always a group the rail actually has.
  const ids = rail.RAIL_GROUPS.map((group) => group.id);
  for (let mask = 0; mask < 16; mask++) {
    const counts = Object.fromEntries(GROUPS.map((id, bit) => [id, (mask >> bit) & 1]));
    const out = rail.firstActiveGroup(counts);
    assert.ok(ids.includes(out), String(mask));
    assert.equal(out, mask === 0 ? 'when' : GROUPS[[0, 1, 2, 3].find((bit) => (mask >> bit) & 1)]);
  }
  // A "when" count can never pull the panel there: it is not a counted group.
  assert.equal(rail.firstActiveGroup({ when: 9, what: 0, details: 0, ranges: 0, chart: 0 }), 'when');
  assert.equal(rail.firstActiveGroup({ when: 9, what: 0, details: 0, ranges: 0, chart: 3 }), 'chart');
});

test('railTarget wraps at both ends, jumps with Home and End, and lets every other key through', () => {
  const total = 5;
  assert.equal(rail.railTarget('ArrowDown', 4, total), 0);
  assert.equal(rail.railTarget('ArrowRight', 4, total), 0);
  assert.equal(rail.railTarget('ArrowUp', 0, total), 4);
  assert.equal(rail.railTarget('ArrowLeft', 0, total), 4);
  assert.equal(rail.railTarget('ArrowDown', 1, total), 2);
  assert.equal(rail.railTarget('ArrowUp', 3, total), 2);
  for (const index of [0, 2, 4]) { assert.equal(rail.railTarget('Home', index, total), 0); assert.equal(rail.railTarget('End', index, total), 4); }
  for (const key of ['Escape', 'Tab', 'Enter', ' ', '', 'a', 'PageUp', 'PageDown', 'arrowdown', 'ArrowDown ', 'Backspace', 'Delete']) assert.equal(rail.railTarget(key, 2, total), null, JSON.stringify(key));
  // Hammering Down all the way round returns to where it started, and Up undoes it.
  let index = 0;
  for (let n = 0; n < 5 * 7; n++) index = rail.railTarget('ArrowDown', index, total);
  assert.equal(index, 0);
  for (let n = 0; n < 5 * 7 + 2; n++) index = rail.railTarget('ArrowUp', index, total);
  assert.equal(index, 3);
});

test('railTarget with nothing to select, one item, or a selection that is not on the rail never returns a bad index', () => {
  for (const total of [0, -1, -100]) for (const key of ['ArrowDown', 'ArrowUp', 'Home', 'End']) assert.equal(rail.railTarget(key, 0, total), null, `${key} ${total}`);
  for (const key of ['ArrowDown', 'ArrowRight', 'ArrowUp', 'ArrowLeft', 'Home', 'End']) assert.equal(rail.railTarget(key, 0, 1), 0, 'one item ' + key);
  // The selected index can be -1 (findIndex found nothing) or past the end after the rail changed under the user. Indices below -1 are not
  // reachable from findIndex, and railTarget does not clamp them (ArrowDown from -3 gives -2), so they are left out on purpose.
  for (const total of [1, 2, 5]) for (const index of [-1, 0, total - 1, total, total + 4, 1000]) for (const key of ['ArrowDown', 'ArrowRight', 'ArrowUp', 'ArrowLeft', 'Home', 'End']) {
    const out = rail.railTarget(key, index, total);
    assert.ok(Number.isInteger(out) && out >= 0 && out < total, `${key} from ${index} of ${total} gave ${out}`);
  }
  assert.equal(rail.railTarget('ArrowDown', -1, 5), 0, 'no selection, Down lands on the first');
  assert.equal(rail.railTarget('ArrowUp', -1, 5), 3, 'no selection, Up is one before the end (documented wrap)');
});

test('the rail has the five groups, in order, each once, and the When group is the only one that is not counted', () => {
  assert.deepEqual(rail.RAIL_GROUPS.map((group) => group.id), ['when', 'what', 'details', 'ranges', 'chart']);
  assert.equal(new Set(rail.RAIL_GROUPS.map((group) => group.label)).size, 5);
  for (const group of rail.RAIL_GROUPS) assert.ok(group.label.trim().length > 0);
  assert.deepEqual(Object.keys(depth.panelGroupCounts([])), GROUPS);
});

// ---------------------------------------------------------------------------------------------------------------------
// Counts
// ---------------------------------------------------------------------------------------------------------------------
test('panelGroupCounts of no chips is all zeros and does not share state between calls', () => {
  assert.deepEqual(depth.panelGroupCounts([]), zeros());
  const first = depth.panelGroupCounts([]);
  first.what = 99;
  assert.deepEqual(depth.panelGroupCounts([]), zeros(), 'a caller editing the answer cannot change the next one');
  assert.deepEqual(depth.panelGroupCounts(depth.filterChips(baseQuery())), zeros(), 'the default query holds no filters');
});

test('panelGroupCounts copes with a huge chip list and still adds up', () => {
  const ids = ['kind', 'outcome', 'controller', 'action', 'operation', 'completion', 'userIds', 'uiVersions', 'userIps', 'statusCodes', 'userAgentContains', 'urlContains', 'auditId', 'metric', 'observedMs', 'apiTimeMs', 'auditCreateTimeMs', 'requestKb', 'responseKb', 'incompleteAgeMs'];
  const chips = Array.from({ length: 100_000 }, (_, n) => ({ id: ids[n % ids.length], group: 'record', label: '', value: '', patch: {} }));
  const counts = depth.panelGroupCounts(chips);
  assert.equal(Object.values(counts).reduce((a, c) => a + c, 0), 100_000);
  assert.deepEqual(counts, { what: 30_000, details: 35_000, ranges: 30_000, chart: 5_000 });
  assert.equal(depth.filtersInUseText(chips.length), '100000 filters in use');
});

test('a chip with an id the table does not know is counted under details, never thrown, including ids that look like object keys', () => {
  const logged = [];
  const original = console.error;
  console.error = (...args) => logged.push(args.join(' '));
  try {
    for (const id of ['clumsy-mystery', '', 'constructor', '__proto__', 'toString', 'hasOwnProperty', 'valueOf', 'KIND', ' kind', 'kind ']) {
      assert.deepEqual(depth.panelGroupCounts([{ id, group: 'record', label: '', value: '', patch: {} }]), { what: 0, details: 1, ranges: 0, chart: 0 }, JSON.stringify(id));
    }
    // One unknown chip anywhere in a long list is still counted, and the known chips keep their groups.
    const good = { id: 'kind', group: 'kind', label: '', value: '', patch: {} };
    assert.deepEqual(depth.panelGroupCounts([good, good, { ...good, id: 'nope' }, good]), { what: 3, details: 1, ranges: 0, chart: 0 });
    assert.ok(logged.length >= 1, 'the unknown ids were reported');
  } finally { console.error = original; }
});

test('the count on the button and in the panel agree for every combination of filters, even with blank and padded text', () => {
  const query = baseQuery();
  Object.assign(query, { controller: '   ', action: '\t', userAgentContains: '   ', urlContains: '', auditId: ' ', operation: ' ', userIds: [], uiVersions: [], statusCodes: [], userIps: [] });
  assert.deepEqual(depth.filterChips(query), [], 'blank or padded text is not a filter');
  assert.equal(depth.moreFiltersCount(query), 0);
  Object.assign(query, { controller: '  Orders  ', observedMs: { min: 5 }, outcome: 'client_error' });
  const chips = depth.filterChips(query);
  assert.equal(depth.moreFiltersCount(query), chips.length);
  assert.equal(Object.values(depth.panelGroupCounts(chips)).reduce((a, c) => a + c, 0), chips.length);
  assert.equal(chips.find((chip) => chip.id === 'controller').value, 'Orders');
});

test('filtersInUseText says no, one and many in words, whole numbers only', () => {
  assert.equal(depth.filtersInUseText(0), 'No filters in use');
  assert.equal(depth.filtersInUseText(1), '1 filter in use');
  assert.equal(depth.filtersInUseText(2), '2 filters in use');
  assert.equal(depth.filtersInUseText(10), '10 filters in use');
  assert.equal(depth.filtersInUseText(1_000_000), '1000000 filters in use');
  assert.equal(depth.filtersInUseText(1.0), '1 filter in use');
  assert.equal(depth.filtersInUseText(-0), 'No filters in use', 'minus zero reads as zero');
  for (const n of [0, 1, 2, 3, 20]) assert.doesNotMatch(depth.filtersInUseText(n), /\b1 filters\b|\bNaN\b|undefined/);
  // Each live count the panel can show round-trips through the text.
  const query = baseQuery();
  assert.equal(depth.filtersInUseText(depth.moreFiltersCount(query)), 'No filters in use');
  query.outcome = 'failure';
  assert.equal(depth.filtersInUseText(depth.moreFiltersCount(query)), '1 filter in use');
  query.metric = 'api';
  assert.equal(depth.filtersInUseText(depth.moreFiltersCount(query)), '2 filters in use');
});

// ---------------------------------------------------------------------------------------------------------------------
// Typed range boxes
// ---------------------------------------------------------------------------------------------------------------------
test('a typed bound that is not a plain non-negative decimal gets a reason and is never applied', () => {
  const limit = inv.MAX_RANGE_MS;
  const bad = {
    abc: /is not a number/, '1e9': /is not a number/, '1E3': /is not a number/, '0x10': /is not a number/, '1,000': /is not a number/, '1 2': /is not a number/,
    Infinity: /is not a number/, '-Infinity': /is not a number/, NaN: /is not a number/, '+5': /is not a number/, '--5': /is not a number/, '5-': /is not a number/,
    '.': /is not a number/, '-': /is not a number/, '１２': /is not a number/, '5ms': /is not a number/,
    '-5': /is negative/, '-0': /is negative/, '-0.5': /is negative/,
    '1000000001': /above the limit/, ['9'.repeat(400)]: /above the limit/
  };
  for (const [text, pattern] of Object.entries(bad)) {
    assert.match(inv.rangeBoundProblem(text, limit), pattern, JSON.stringify(text.slice(0, 20)));
    assert.equal(inv.buildRange(text, '', limit).range, undefined, 'min ' + text.slice(0, 20));
    assert.equal(inv.buildRange('', text, limit).range, undefined, 'max ' + text.slice(0, 20));
    assert.equal(inv.buildRange(text, '', limit).problems.length, 1);
  }
  // The message quotes what was typed but a very long entry is not a reason to break the box.
  assert.doesNotThrow(() => inv.rangeBoundProblem('9'.repeat(100_000), limit));
  assert.match(inv.rangeBoundProblem('x'.repeat(50) + '<script>', limit), /is not a number/);
});

test('an empty or blank bound is nothing, and the limits themselves are allowed', () => {
  const limit = inv.MAX_RANGE_MS;
  for (const text of ['', ' ', '   ', '\t', '\n', ' \t ']) {
    assert.equal(inv.rangeBoundProblem(text, limit), '', JSON.stringify(text));
    assert.deepEqual(inv.buildRange(text, text, limit), { range: undefined, problems: [] }, JSON.stringify(text));
  }
  for (const text of ['0', '0.0', '.5', '5.', '007', ' 12 ', '1000000000', '999999999.999']) assert.equal(inv.rangeBoundProblem(text, limit), '', JSON.stringify(text));
  assert.deepEqual(inv.buildRange(' 12 ', '', limit).range, { min: 12 });
  assert.deepEqual(inv.buildRange('0', '0', limit).range, { min: 0, max: 0 }, 'zero to zero is a real range');
  assert.deepEqual(inv.buildRange('', '5.', limit).range, { max: 5 });
  assert.match(inv.rangeBoundProblem('10000001', inv.MAX_RANGE_KB), /above the limit of 10,000,000/);
  assert.equal(inv.rangeBoundProblem('10000000', inv.MAX_RANGE_KB), '');
});

test('a minimum above the maximum applies neither, and one good bound next to a bad one still applies', () => {
  const limit = inv.MAX_RANGE_MS;
  const swapped = inv.buildRange('9', '3', limit);
  assert.equal(swapped.range, undefined);
  assert.deepEqual(swapped.problems, ['The minimum is above the maximum, so neither is applied']);
  assert.deepEqual(inv.buildRange('3', '3', limit).range, { min: 3, max: 3 }, 'equal bounds are fine');
  assert.deepEqual(inv.buildRange('3.0000001', '3', limit).problems.length, 1);
  assert.equal(inv.buildRange('3.0000001', '3', limit).range, undefined);
  const mixed = inv.buildRange('5', 'abc', limit);
  assert.deepEqual(mixed.range, { min: 5 });
  assert.equal(mixed.problems.length, 1);
  assert.match(mixed.problems[0], /^Maximum: /);
  const both = inv.buildRange('-5', 'abc', limit);
  assert.equal(both.range, undefined);
  assert.equal(both.problems.length, 2);
  assert.match(both.problems[0], /^Minimum: /);
  // A bad minimum is not compared against a good maximum: it is dropped, not "above" anything.
  assert.deepEqual(inv.buildRange('abc', '5', limit), { range: { max: 5 }, problems: ['Minimum: "abc" is not a number'] });
});

test('syncRangeDraft keeps what the user is typing while it means the applied range, and rewrites it only when it does not', () => {
  const limit = inv.MAX_RANGE_MS;
  const keep = (draft, applied) => assert.deepEqual(inv.syncRangeDraft(draft, applied, limit), draft, JSON.stringify(draft));
  // Rejected or half-typed text survives while nothing is applied: the box must not eat what was typed.
  keep({ min: 'abc', max: '' }, undefined);
  keep({ min: '1e9', max: '' }, undefined);
  keep({ min: '-5', max: ' ' }, undefined);
  keep({ min: '', max: '' }, undefined);
  keep({ min: ' ', max: ' ' }, undefined);
  keep({ min: '9', max: '3' }, undefined);
  keep({ min: '5.', max: '' }, { min: 5 });
  keep({ min: '  7 ', max: '' }, { min: 7 });
  keep({ min: '5', max: 'abc' }, { min: 5 });
  // A restored or cleared query reaches the boxes, whatever they held.
  assert.deepEqual(inv.syncRangeDraft({ min: 'abc', max: '' }, { min: 3 }, limit), { min: '3', max: '' });
  assert.deepEqual(inv.syncRangeDraft({ min: '9', max: '3' }, { min: 1, max: 2 }, limit), { min: '1', max: '2' });
  assert.deepEqual(inv.syncRangeDraft({ min: '5', max: '6' }, undefined, limit), { min: '', max: '' }, 'cleared');
  assert.deepEqual(inv.syncRangeDraft({ min: '', max: '' }, { min: 0, max: 0 }, limit), { min: '0', max: '0' });
  assert.deepEqual(inv.syncRangeDraft({ min: '', max: '' }, { max: 1_000_000_000 }, limit), { min: '', max: '1000000000' });
  // The rewritten text is itself a draft that builds to the applied range (so it settles instead of flickering).
  for (const applied of [{ min: 3 }, { max: 9 }, { min: 0, max: 0 }, { min: 1.5, max: 2.25 }, { max: 1_000_000_000 }]) {
    const text = inv.syncRangeDraft({ min: '?', max: '?' }, applied, limit);
    assert.deepEqual(inv.buildRange(text.min, text.max, limit).range, applied, JSON.stringify(applied));
    assert.deepEqual(inv.syncRangeDraft(text, applied, limit), text, 'settled ' + JSON.stringify(applied));
  }
});

test('a hand-edited saved range with a tiny bound is written into the box as a plain decimal the box accepts (fixed; was exponent text)', () => {
  // serializeRecordFilters accepts any finite number from 0 to the limit, and String(1e-7) is "1e-7", which the typed-text rules refuse.
  // The box now gets a plain decimal, so the saved bound round-trips.
  const applied = inv.serializeRecordFilters({ observedMs: { min: 1e-7 } }).observedMs;
  assert.deepEqual(applied, { min: 1e-7 });
  const draft = inv.syncRangeDraft({ min: '', max: '' }, applied, inv.MAX_RANGE_MS);
  assert.deepEqual(draft, { min: '0.0000001', max: '' });
  assert.deepEqual(inv.buildRange(draft.min, draft.max, inv.MAX_RANGE_MS).range, { min: 1e-7 });
});

test('range conflicts with the completion filter are explained, and an empty range is not a conflict', () => {
  assert.equal(inv.rangeConflict({}), '');
  assert.equal(inv.rangeConflict({ completion: 'incomplete' }), '');
  assert.equal(inv.rangeConflict({ observedMs: {}, completion: 'incomplete' }), '', 'a range with no bound says nothing');
  assert.match(inv.rangeConflict({ observedMs: { min: 1 }, completion: 'incomplete' }), /only describes complete/);
  assert.match(inv.rangeConflict({ incompleteAgeMs: { max: 1 }, completion: 'complete' }), /only describes incomplete/);
  assert.match(inv.rangeConflict({ observedMs: { min: 1 }, incompleteAgeMs: { min: 1 } }), /cannot be combined/);
  assert.equal(inv.rangeConflict({ observedMs: { min: 1 }, completion: 'all' }), '');
  assert.equal(inv.rangeConflict({ requestKb: { min: 1 }, completion: 'invalid' }), '', 'a size range goes with any completion');
});

test('a saved filter object full of junk loads as no filters, not as a crash or a half filter', () => {
  const junk = {
    userIds: [0, -1, 1.5, '7', NaN, Infinity, 2_147_483_648, null], uiVersions: [1, null, '', '  '], userIps: 'not a list', statusCodes: [{}, []],
    userAgentContains: 42, urlContains: '   ', auditId: null, completion: 'bogus', operation: 'NoDot',
    observedMs: { min: -1, max: NaN }, apiTimeMs: { min: 10, max: 5 }, auditCreateTimeMs: 'x', requestKb: [1, 2], responseKb: { min: 1e12 }, incompleteAgeMs: {}
  };
  assert.deepEqual(inv.serializeRecordFilters(junk), {});
  assert.deepEqual(inv.serializeRecordFilters(null), {});
  assert.deepEqual(inv.serializeRecordFilters(undefined), {});
  assert.deepEqual(inv.serializeRecordFilters({}), {});
  const query = { ...baseQuery(), ...junk };
  assert.deepEqual(depth.filterChips(query), [], 'junk adds no chips');
  // Good values next to junk survive and nothing else does.
  const mixed = inv.serializeRecordFilters({ ...junk, userIds: [0, 7, 7, 9], observedMs: { min: 5 } });
  assert.deepEqual(mixed, { userIds: [7, 9], observedMs: { min: 5 } });
  // More than 50 user ids and more than 20 list values are cut, not rejected.
  assert.equal(inv.serializeRecordFilters({ userIds: Array.from({ length: 80 }, (_, n) => n + 1) }).userIds.length, inv.MAX_USER_IDS);
  assert.equal(inv.serializeRecordFilters({ statusCodes: Array.from({ length: 80 }, (_, n) => String(200 + n)) }).statusCodes.length, inv.MAX_FILTER_VALUES);
});

// ---------------------------------------------------------------------------------------------------------------------
// The polling-type checkbox
// ---------------------------------------------------------------------------------------------------------------------
test('the polling-type checkbox sets and clears the kind both ways, over and over, and the chip comes and goes with it', () => {
  const query = baseQuery();
  assert.equal(depth.unknownKindToggle(true), 'http_unknown');
  assert.equal(depth.unknownKindToggle(false), 'http');
  for (let n = 0; n < 6; n++) {
    query.kind = depth.unknownKindToggle(n % 2 === 0);
    const chips = depth.filterChips(query);
    assert.equal(chips.length, n % 2 === 0 ? 1 : 0, 'click ' + n);
    if (n % 2 === 0) { assert.equal(chips[0].id, 'kind'); assert.equal(depth.panelGroupCounts(chips).what, 1); }
  }
  // Removing the chip returns to All types, not to HTTP, and the checkbox then reads as unchecked.
  query.kind = depth.unknownKindToggle(true);
  const chip = depth.filterChips(query)[0];
  depth.applyChipPatch(query, chip);
  assert.equal(query.kind, 'all');
  assert.deepEqual(depth.filterChips(query), []);
  assert.notEqual(query.kind, 'http_unknown');
  // Checking again after that works as if for the first time.
  query.kind = depth.unknownKindToggle(true);
  assert.deepEqual(depth.filterChips(query).map((c) => c.id), ['kind']);
  // The other request types never make a chip.
  for (const kind of ['all', 'synchronous', 'http', 'asynchronous']) { query.kind = kind; assert.deepEqual(depth.filterChips(query), [], kind); }
});

test('removing every chip, in any order, empties the panel and never touches a field the chip does not own', () => {
  const full = () => Object.assign(baseQuery(), {
    kind: 'http_unknown', outcome: 'failure', metric: 'api', controller: 'Orders', action: 'Get', operation: 'Orders.Get', completion: 'pending',
    userIds: [7], uiVersions: ['1.2'], userIps: ['10.0.0.1'], statusCodes: ['500'], userAgentContains: 'bot', urlContains: '/x', auditId: 'abc',
    observedMs: { min: 1 }, requestKb: { max: 9 }
  });
  const count = depth.filterChips(full()).length;
  assert.ok(count >= 15);
  for (const reverse of [false, true]) {
    const query = full();
    const sources = query.sourceIds, start = query.start, bucket = query.bucketSeconds;
    let chips = depth.filterChips(query);
    while (chips.length) {
      const chip = reverse ? chips[chips.length - 1] : chips[0];
      const before = chips.length;
      depth.applyChipPatch(query, chip);
      chips = depth.filterChips(query);
      assert.equal(chips.length, before - 1, chip.id + ' removes exactly one chip');
      assert.ok(!chips.some((other) => other.id === chip.id));
    }
    assert.equal(depth.moreFiltersCount(query), 0);
    assert.deepEqual(depth.panelGroupCounts(chips), zeros());
    assert.equal(rail.firstActiveGroup(depth.panelGroupCounts(chips)), 'when');
    assert.deepEqual([query.sourceIds, query.start, query.bucketSeconds], [sources, start, bucket], 'the time windows and sources are not filters');
  }
});
