const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// The What, Request details and Chart groups of the More filters panel. Source-level pins; everything here is synthetic.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const what = read('src/components/filters/WhatGroup.svelte');
const details = read('src/components/filters/DetailsGroup.svelte');
const chart = read('src/components/filters/ChartGroup.svelte');
const styleOf = (src) => src.slice(src.indexOf('<style>'));
const count = (src, needle) => src.split(needle).length - 1;

test('WhatGroup drives the unknown-kind checkbox from query.kind and never autoruns', () => {
  assert.match(what, /id="kind-unknown"[^>]*checked=\{query\.kind === 'http_unknown'\}/);
  assert.match(what, /unknownKindToggle\(e\.currentTarget\.checked\)/);
  assert.doesNotMatch(what, /onScopeChange|id="kind"/);
  assert.match(what, /syncOperationDraft\(/);
  assert.match(what, /\$effect\(/);
  for (const id of ['outcome', 'rf-completion', 'controller', 'action', 'rf-operation', 'rf-operation-options']) assert.match(what, new RegExp(`id="${id}"`));
  assert.match(what, /Only HTTP requests whose polling type is unknown/);
  assert.match(what, /Suggestions come from the loaded results\./);
  assert.match(what, /Not applied\. Write the operation as Controller\.Action, with a name on both sides of the dot\./);
});

test('DetailsGroup keeps the list caps and the over-limit notices', () => {
  assert.match(details, /splitList\(text\)\.slice\(0, MAX_FILTER_VALUES\)/);
  assert.match(details, /\.slice\(0, MAX_USER_IDS\)/);
  assert.ok(count(details, 'syncListDraft(') >= 4);
  for (const id of ['over-user-ids', 'over-ui-versions', 'over-user-ips', 'over-status-codes']) assert.match(details, new RegExp(`data-testid="${id}"`));
  assert.equal(count(details, '>exact match<'), 2);
  assert.equal(count(details, '>not case sensitive<'), 2);
  for (const id of ['rf-user-ids', 'rf-ui-versions', 'rf-user-ips', 'rf-status-codes', 'rf-agent', 'rf-url', 'rf-audit-id']) assert.match(details, new RegExp(`id="${id}"`));
  assert.match(details, /Ignored, not whole numbers from 1 to 2147483647: /);
  assert.match(details, /Narrow to records that match all of these\. Separate several values with commas; leave a box empty to ignore it\./);
  assert.doesNotMatch(details, /Exact match\.|Not case sensitive\./);
});

test('validation messages stay next to their own field', () => {
  const cases = [['rf-user-ids', details], ['rf-audit-id', details], ['rf-operation', what]];
  for (const [id, src] of cases) {
    const start = src.indexOf(`id="${id}"`);
    assert.ok(start > 0, id);
    const rest = src.slice(start + 1);
    const ends = [rest.indexOf('<label'), rest.indexOf('class="control-group"')].filter(n => n >= 0);
    const slice = rest.slice(0, Math.min(...ends));
    assert.match(slice, /aria-invalid=/, id);
    assert.match(slice, /role="alert"/, id);
    assert.match(slice, new RegExp(`aria-describedby=\\{[^}]*${id}-error`), id);
    assert.match(slice, new RegExp(`id="${id}-error"`), id);
  }
});

test('ChartGroup keeps the chart controls and their limits', () => {
  for (const id of ['metric', 'bucket', 'latency-target']) assert.match(chart, new RegExp(`id="${id}"`));
  assert.match(chart, /min="10" max="86400"/);
  assert.match(chart, /intervalNote\.clear\(\)/);
  assert.match(chart, /10–86,400 seconds; no more than 120 points\./);
  assert.match(chart, /Adds a chart line compared with P95\./);
  assert.match(chart, /metricLabels/);
  assert.match(chart, /placeholder="No target"/);
  assert.match(chart, /role="status"/);
  assert.doesNotMatch(chart, /Total request/);
});

test('all three field groups are flat, token-only and use known hints', () => {
  const allowed = ['status-class', 'incomplete-request', 'response-time', 'time-slice', 'p95'];
  for (const [name, src] of [['WhatGroup', what], ['DetailsGroup', details], ['ChartGroup', chart]]) {
    assert.doesNotMatch(src, /<details/, name);
    assert.doesNotMatch(styleOf(src), /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/, name);
    assert.match(src, /<FilterGroup /, name);
    for (const match of src.matchAll(/<Hint termId="([^"]+)"/g)) assert.ok(allowed.includes(match[1]), `${name} ${match[1]}`);
  }
  assert.match(what, /<Hint termId="status-class"/);
  assert.match(what, /<Hint termId="incomplete-request"/);
  assert.match(chart, /<Hint termId="response-time"/);
  assert.match(chart, /<Hint termId="time-slice"/);
  assert.match(chart, /<Hint termId="p95"/);
});
