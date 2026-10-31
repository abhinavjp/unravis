# Packet FIX8: the Slowest APIs delta sign, the inert entity, and the emoji-safe note

Do exactly this. Touch only the files named below. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). No new files, no new globals. The parts load in sorted filename order into one concatenated script and share the runtime state object `W`. `parts/41-tools-a.js` loads after `parts/30-steps-0-4.js`, so the helper `signed` is already defined when the tool body runs.

Two of the edits are to sibling check files (`data/chk-fix4.js`, `data/chk-s1A.js`); their block headings are relative to `buildout/wireframes` and read `### ../packets/data/<file>`.

## 1. `parts/41-tools-a.js` — sign in the Slowest APIs Change cell

The `slowest-apis` tool body prints the delta as `' (+' + fmtCount(r[5]) + ' ms)'`, so a negative delta reads `(+-2 ms)`. Use the helper the Step 2 table already uses: replace the fragment

    fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td>'

with

    fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'

`signed(fmtCount(r[5]))` is `'-2'` for a delta of -2 and `'+2'` for +2, exactly as `byApiTable` in `parts/30-steps-0-4.js` writes `' (' + signed(fmtCount(r[5])) + ' ms)'`.

## 2. `parts/41-tools-a.js` — the inert entity

The `time-split` body builds a button whose `inert(...)` argument contains the HTML entity `&rsquo;`. `inert` puts that text in `W.flash`, and `render` prints it through `esc`, so the reader sees the literal text `&amp;rsquo;`. Replace the entity with a plain apostrophe written as `\'` inside the surrounding single-quoted JS string; the HTML attribute is double-quoted (`onclick="..."`), so the markup stays valid. The argument becomes `inert('Opens the case\'s Add data flow')`, and the flash line reads `case's`.

Only `parts/41-tools-a.js` has such a literal. `parts/43-tools-c.js` has no `inert(` string at all (verified), so it is not edited. Do not edit `parts/30-steps-0-4.js`; its three identical literals are out of scope.

## 3. `parts/40-manual-home.js` — code-point-safe note text, and drop the note maxlength

`addToolNote` stores the note with `String(W.noteDraft).trim().slice(0, MAX_ITEM_TEXT)`, which cuts UTF-16 units; a 300-emoji note is 600 units, so it is cut to 150 emoji and can leave a lone surrogate. `noteTooLong` already counts code points with the local `chars` helper. Store the code points instead:

    text:chars(String(W.noteDraft).trim()).slice(0, MAX_ITEM_TEXT).join('')

This is the only `.slice(0, MAX_...)` of draft text in the file; `cleanViewName` already truncates with `chars(...).slice(0, MAX_VIEW_NAME).join('')`.

Also remove ` maxlength="400"` from the note input. The too-long message ("An item holds up to 300 characters.") and the disabled Add button already stop an over-long item, and the raw `maxlength` counts UTF-16 units. The input's value and `oninput` handler stay as they are.

## 4. `../packets/data/chk-fix4.js` — the `chars(` count

`parts/40-manual-home.js` gains one more `chars(` call (item 3), so `chk-fix4`'s assertion `eq('chars call count', cnt(mh, 'chars('), 4);` must expect `5` to keep the check green. Change only that number.

## 5. `../packets/data/chk-s1A.js` — the `signed` stub

`chk-s1A` loads `parts/41-tools-a.js` but not `parts/30-steps-0-4.js`, and renders the `slowest-apis` body (lines 51-72). Item 1 makes that body call `signed`, which the real app always has (all parts load into one script). Give the bare harness the same helper next to its other stubs: add

    c.signed = function (n) { n = String(n); return n.charAt(0) === '-' ? n : '+' + n; };

immediately after the `c.byApiTable = ...` stub line. No other assertion changes.

## Blocks

### parts/41-tools-a.js
<<<<<<< SEARCH
fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td>'
=======
fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
inert(\'Opens the case&rsquo;s Add data flow\')
=======
inert(\'Opens the case\'s Add data flow\')
>>>>>>> REPLACE

### parts/40-manual-home.js
<<<<<<< SEARCH
text:String(W.noteDraft).trim().slice(0, MAX_ITEM_TEXT)
=======
text:chars(String(W.noteDraft).trim()).slice(0, MAX_ITEM_TEXT).join('')
>>>>>>> REPLACE

### parts/40-manual-home.js
<<<<<<< SEARCH
maxlength="400" value="' + esc(W.noteDraft)
=======
value="' + esc(W.noteDraft)
>>>>>>> REPLACE

### ../packets/data/chk-fix4.js
<<<<<<< SEARCH
eq('chars call count', cnt(mh, 'chars('), 4);
=======
eq('chars call count', cnt(mh, 'chars('), 5);
>>>>>>> REPLACE

### ../packets/data/chk-s1A.js
<<<<<<< SEARCH
c.byApiTable = function () { return '<div class="tscroll">BY-API</div>'; };
=======
c.byApiTable = function () { return '<div class="tscroll">BY-API</div>'; };
c.signed = function (n) { n = String(n); return n.charAt(0) === '-' ? n : '+' + n; };
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)

From `buildout/wireframes`:

```
node --check parts/41-tools-a.js && node --check parts/40-manual-home.js && node ../packets/data/chk-fix8.js
```

Expected `PASS`. `chk-fix8.js` loads `parts/10-data.js`, `parts/30-steps-0-4.js` and `parts/41-tools-a.js` in a vm, renders the Slowest APIs table with a -2 ms row and a +2 ms row, ES5-scans and `new Function`-parses both edited parts, checks the escaped inert literal and that no entity sits on an `inert(` line, and loads `parts/40-manual-home.js` to store a 300-emoji note and assert 300 code points with no lone surrogate. After apply, `data/chk-fix4.js`, `chk-fix5.js`, `chk-s1A.js` and `chk-b9.js` must still print `PASS`.

## Block format
No new files. Each edit is `### <file>` then `<<<<<<< SEARCH`, the old text, `=======`, the new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line), in the order shown. The two check-file headings are relative to `buildout/wireframes`.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
