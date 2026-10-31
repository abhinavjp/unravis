# Packet FIX11: healthy evidence chip reads "Looked normal" (owner wording ruling)

Do exactly this. Do not explore the repo. Make the single edit below; create no files. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`) — this packet changes one string literal and adds no code. No new globals.

## Contract
`parts/12-evidence-c.js` defines the global function `evChip(v)`. After the edit its full body is exactly:

```js
function evChip(v) {
  if (v === 'ok') return 'Looked normal';
  if (v === 'watch') return 'Worth a look';
  if (v === 'bad') return 'Likely cause';
  return 'Not collected';
}
```

Return values: `evChip('ok')` -> `'Looked normal'`, `evChip('watch')` -> `'Worth a look'`, `evChip('bad')` -> `'Likely cause'`, anything else (including `'none'`) -> `'Not collected'`. Nothing else in the file changes; every other line stays byte-for-byte as it is.

Do NOT change any other use of the word `Normal`. The following mean the normal (reference) period, not a card status, and stay untouched: the reference-window chart keys and legends (`parts/11-charts-a.js` line with `? 'Normal' : 'Incident'`, `parts/12-evidence-b.js`, `parts/10-data.js`), the table column headings (`parts/30-steps-0-4.js` — both `<th>Normal</th>` headings stay; `parts/42-tools-b.js`), the bar labels in `parts/30-steps-0-4.js`, help copy (`parts/41-tools-a.js`, `parts/43-tools-c.js`), and the local variable names in `parts/12-evidence-f.js`. `parts/12-evidence-d.js` and `parts/14-deps-data.js` already say `Looked normal`; leave them.

Owner pre-check (already done; listed so you do not repeat it): a grep of every `parts/*.js` and `parts/*.html` for `'Normal'`, `>Normal<` and `Normal</span>` found no other place that prints a card or finding status chip for status `ok` by a separate literal (the chip text comes only from `evChip`, used in `parts/13-why-b.js` and `parts/12-evidence-d.js`), and a grep of every `data/chk-*.js` found no assertion that expects the old chip text `Normal` for a card status (`chk-axis.js` and `chk-fix1.js` match only chart legend keys; `chk-fix1.js`, `chk-s4.js`, `chk-cpu.js`, `chk-s1A.js` have no chip-text expectation). So no other file is edited.

## Edit to `parts/12-evidence-c.js` (function `evChip`, line 2; the SEARCH occurs exactly once)
SEARCH `  if (v === 'ok') return 'Normal';` -> REPLACE `  if (v === 'ok') return 'Looked normal';`

## Check (the owner applies the block and runs the full check; you only run this)
```
grep -c -F "  if (v === 'ok') return 'Normal';" parts/12-evidence-c.js
```
Expected: `1` before applying, `0` after. After applying, the owner runs `node ../packets/data/chk-fix11.js` from `buildout/wireframes`, which prints PASS.

## Block format
Edit: `### parts/12-evidence-c.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). There are no new files.

## Blocks
### parts/12-evidence-c.js
<<<<<<< SEARCH
  if (v === 'ok') return 'Normal';
=======
  if (v === 'ok') return 'Looked normal';
>>>>>>> REPLACE

## Output
The block in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
