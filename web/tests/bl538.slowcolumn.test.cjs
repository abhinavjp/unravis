const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// BL-538: the first column of every `.volume-table` list (API name, time window) had only `overflow-wrap: anywhere`, so the table gave it its smallest
// width (about 71 px) and the words broke after a few letters. The browser spec UIFIX-53 measures the page; these pin the source it is built from.
// BL-506: the Date box of the When filter is marked invalid together with the Time box when the pair is refused (a date change that lands in the hour the clocks skip).

const read = (file) => fs.readFileSync(path.resolve(__dirname, '..', file), 'utf8');
const rule = (css, selector) => {
  const start = css.indexOf(selector + ' {');
  assert.ok(start >= 0, 'rule ' + selector + ' exists');
  return css.slice(start, css.indexOf('}', start));
};

test('BL-538: the first column of a .volume-table list has a minimum width, and the slowest-request list a larger one', () => {
  const css = read('src/app.css');
  assert.match(rule(css, '.volume-table tbody th'), /min-width: 9rem;/);
  assert.match(rule(css, '.traffic-slowness .slow-result .volume-table tbody th'), /min-width: 11rem;/);
});

test('BL-538: the Started and API / audit time cells of the slowest list may wrap at their spaces; each time keeps its unit with it', () => {
  const css = read('src/app.css');
  assert.match(css, /\.traffic-slowness \.slow-result \.volume-table tbody td:first-child, \.traffic-slowness \.slow-result \.volume-table tbody td:nth-child\(5\) \{ white-space: normal; \}/);
  assert.match(css, /\.traffic-slowness \.keep-together \{ white-space: nowrap; \}/);
  const view = read('src/components/TrafficSlowness.svelte');
  assert.match(view, /<td><span class="keep-together">\{ms\(row\.apiTimeMs\)\} \/<\/span> <span class="keep-together">\{ms\(row\.auditCreateTimeMs\)\}<\/span><\/td>/);
});

test('BL-538: only these two components use .volume-table, so the shared rule is checked on the two lists that exist', () => {
  const users = fs.readdirSync(path.resolve(__dirname, '../src/components')).filter((f) => f.endsWith('.svelte') && /class="volume-table"/.test(read('src/components/' + f)));
  assert.deepEqual(users.sort(), ['RequestVolume.svelte', 'TrafficSlowness.svelte']);
});

test('BL-506: the Date box is marked invalid with the Time box when the pair is refused, but not when only the time text is bad; the chosen date stays in the box', () => {
  const field = read('src/components/DateTimeField.svelte');
  assert.match(field, /const dateRefused = \$derived\(invalid && !bad\);/);
  assert.match(field, /<input id=\{`\$\{id\}-date`\} type="date"[^>]*aria-invalid=\{dateRefused \? 'true' : undefined\}[^>]*aria-describedby=\{dateRefused && errorId \? errorId : undefined\}/);
  // the date box is never reset to the query's date by the refusal: its value attribute follows the query only, and the next valid time sends the box's own date
  assert.match(field, /const dateNow = \(\) => dateBox\?\.value \?\? value\.slice\(0, 10\);/);
  assert.match(field, /value=\{value\.slice\(0, 10\)\}/);
});
