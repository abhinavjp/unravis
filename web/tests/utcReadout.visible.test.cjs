const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// BL-505: the UTC times of the two Investigation timeline windows are visible text under the pickers, not clipped or tooltip-only.

const paper = fs.readFileSync(path.resolve(__dirname, '..', 'src', 'components', 'TracingPaper.svelte'), 'utf8');
const metaline = paper.match(/<p class="metaline"[^\n]*<\/p>/)?.[0] ?? '';
const style = paper.slice(paper.indexOf('<style'));
const rule = (selector) => style.match(new RegExp(selector.replace(/\./g, '\\.') + '\\s*\\{([^}]*)\\}'))?.[1] ?? null;

test('the readout holds both UTC strings, the "compared with" word and the zone name, none of them visually hidden', () => {
  assert.ok(metaline, 'the metaline paragraph exists');
  assert.match(metaline, /data-testid="utc-echo-incident"[^>]*>\{echoUtc\(fields\.incidentStart, fields\.incidentEnd\)\}/);
  assert.match(metaline, /data-testid="utc-echo-reference"[^>]*>\{echoUtc\(fields\.referenceStart, fields\.referenceEnd\)\}/);
  assert.ok(metaline.includes('{WINDOW_TEXT.metaCompared}'), 'the word between the two windows is drawn');
  assert.ok(metaline.includes('<b>{timeZone.zone}</b>'), 'the zone name is drawn');
  assert.ok(!/class="utc-echo[^"]*(echo-hidden|visually-hidden)/.test(metaline), 'neither UTC string is clipped away');
  assert.ok(!/<span[^>]*visually-hidden[^>]*>[^<]*(metaCompared|echoUtc)/.test(metaline), 'nor the word between them');
  assert.ok(!paper.includes('echo-hidden'), 'the clipping class is gone from the component');
});

test('the UTC text and the tooltip read the same window fields, and the text is UTC whatever zone is shown', () => {
  assert.match(paper, /const echoUtc = \(start: string, end: string\): string => \(Number\.isFinite\(parseFieldUtc\(start\)\) && Number\.isFinite\(parseFieldUtc\(end\)\) \? `UTC \$\{start\}Z – \$\{end\}Z`/);
  const calls = metaline.match(/echoUtc\(fields\.(incidentStart, fields\.incidentEnd|referenceStart, fields\.referenceEnd)\)/g) ?? [];
  assert.equal(calls.length, 4, 'the tooltip and the two drawn strings each read the incident and the comparison boxes');
  assert.ok(!/timeZone\.zone[^}]*echoUtc|echoUtc[^)]*timeZone/.test(paper), 'echoUtc does not take the shown zone');
});

test('the .utc-echo rule draws text in the flow: no clipping, no absolute position, no nowrap that could push a phone sideways', () => {
  const css = rule('.utc-echo');
  assert.ok(css, 'the rule exists');
  assert.ok(!/position\s*:\s*absolute|clip-path|overflow\s*:\s*hidden|white-space\s*:\s*nowrap|width\s*:\s*1px/.test(css), css);
  assert.match(css, /font-family:\s*var\(--mono\)/);
});

test('the metaline keeps its own wrapping, so a long line breaks inside the card', () => {
  assert.match(rule('.metaline'), /overflow-wrap:\s*anywhere/);
});

test('the readout stays one line on a desktop: no seconds, and the "reads and stores UTC" sentence is not drawn (it is in the tooltip and read by screen readers)', () => {
  assert.ok(!paper.includes(':00Z'), 'no seconds in the echo');
  assert.ok(!/class="visually-hidden"[^>]*>[^<]*metaZoneTail/.test(metaline));
  assert.match(metaline, /<span class="visually-hidden"> \{WINDOW_TEXT\.metaStoresUtc\}<\/span>/, 'the sentence is still in the paragraph for screen readers');
  assert.match(metaline, /title=\{[^}]*WINDOW_TEXT\.metaStoresUtc\}/, 'and in the tooltip');
  assert.ok(!metaline.includes('{WINDOW_TEXT.metaStoresUtc}</p>'), 'not as drawn text');
});

test('a box that holds no real moment (empty or a day that does not exist) reads "not set", not a made-up UTC time', () => {
  assert.match(paper, /import \{[^}]*\bparseFieldUtc\b[^}]*\} from '\.\.\/lib\/tracing\/windows'/);
  assert.match(paper, /parseFieldUtc\(start\)[\s\S]*?parseFieldUtc\(end\)[\s\S]*?: `UTC \$\{WINDOW_TEXT\.metaUnset\}`/);
});
