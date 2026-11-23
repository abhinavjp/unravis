const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// BL-495: "Show only" and the clickable table rows are real buttons. Source text checks over the whole web/src tree, so a new clickable row
// that is only a <tr> with a click handler (no role, no keyboard, no name) fails here.

const src = path.resolve(__dirname, '..', 'src');
function svelteFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? svelteFiles(path.join(dir, e.name)) : e.name.endsWith('.svelte') ? [path.join(dir, e.name)] : []));
}
const files = svelteFiles(src).map((f) => ({ name: path.relative(src, f).replace(/\\/g, '/'), text: fs.readFileSync(f, 'utf8').replace(/<style[\s\S]*?<\/style>/, '') }));
// opening tags of one element kind, across lines
const tagsOf = (text, tag) => [...text.matchAll(new RegExp('<' + tag + '\\b[^<>]*?(?:\\{[^{}]*\\}[^<>]*?)*>', 'g'))].map((m) => m[0]);

test('"Show only" in the request volume table is a real button with a name that starts with the visible words', () => {
  const rv = files.find((f) => f.name === 'components/RequestVolume.svelte').text;
  assert.match(rv, /<button type="button" class="quiet-button row-action" aria-label=\{'Show only '/);
  assert.match(rv, /\}\}>Show only<\/button>/);
  assert.doesNotMatch(rv, /<(div|span|td|tr)\b[^>]*Show only/);
});

test('no table row is a focus stop of its own: a row is never tabindex="0" with a click handler', () => {
  for (const f of files) {
    for (const tag of tagsOf(f.text, 'tr')) {
      assert.doesNotMatch(tag, /tabindex/, `${f.name}: a <tr> must not take focus; put a <button> in the row`);
      assert.doesNotMatch(tag, /onkeydown|on:keydown/, `${f.name}: a <tr> must not carry a key handler; a <button> answers Enter and Space`);
    }
  }
});

test('a row that answers a mouse press has a real button in the same file (keyboard, name, pressed state)', () => {
  for (const f of files) {
    const clickable = tagsOf(f.text, 'tr').filter((t) => /\bonclick\s*=/.test(t));
    if (!clickable.length) continue;
    assert.match(f.text, /<button type="button" class="row-btn"/, `${f.name}: a clickable <tr> needs a row button`);
  }
});

test('the slow call lists use a button that keeps its press to itself (one press, one selection)', () => {
  for (const name of ['components/CallNearby.svelte', 'components/CallAllSources.svelte']) {
    const text = files.find((f) => f.name === name).text;
    assert.match(text, /event\.stopPropagation\(\);\s*onSelect\(/, `${name}: stopPropagation before onSelect`);
  }
});
