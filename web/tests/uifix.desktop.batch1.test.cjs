const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// BL-473, 474, 476, 477, 478: layout, size and colour fixes the UI audit found. What the page really looks like (sizes, positions,
// computed colours, in both themes) is measured in a browser by tools/e2e/specs/ui-layout.spec.ts (UIFIX-01 to 08) and by tools/uiaudit.
// Only two source facts are kept here, because a browser check cannot tell them from a rule that happens to win today.
const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');

test('BL-474: the global "i after ?" rule comes after the touch/narrow rule, so it wins on equal specificity', () => {
  const css = read('src/components/FieldHelp.svelte');
  assert.ok(css.indexOf('.hint-wrap ~ .field-help-toggle') > css.indexOf('pointer: coarse'));
});

test('BL-476: dimmed Daily rows never use opacity (it lowers the contrast of every cell colour at once)', () => {
  assert.doesNotMatch(read('src/components/DailyTable.svelte'), /tr\.low[^{]*\{[^}]*opacity/);
});
