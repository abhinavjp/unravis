# Packet FIX4: fix the shell, the data helper and the suspect link

Do exactly this. Do not explore the repo, do zero analysis: every SEARCH string below was copied byte-for-byte from the current file and occurs exactly once (`grep -c -F` verified). Make only the edits below, to `parts/10-data.js`, `parts/20-shell.js`, `parts/40-manual-home.js` and `parts/10-data4-suspects.js`, and to no other file. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). One new global only: `chars` (defined in `parts/40-manual-home.js`, above its three callers in the same file). Do not read `W` at load time.

## Contract

### Edit 1 — `parts/10-data.js`, `function esc(s)`
`esc` currently escapes only `&` and `<`, yet its output is used inside double-quoted value attributes (`value="' + esc(x) + '"`), so `"` and `>` can break out. Extend the `.replace` chain to also escape `>` to `&gt;` and `"` to `&quot;`, in the same order and single-line style as the `esc` inside `tlChart`/`stackedArea` in `parts/11-charts-a.js`. The exact resulting line (whole function on one line, no added spaces) is:

    function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }

Nothing else in the file changes.

### Edit 2 — `parts/20-shell.js`, `function runSteps()`
A new main run must clear the old comparison. `W.fix` is declared earlier in this same file as `fix:{ day:'2026-10-28', route:'', verdictOn:false, recorded:false, problem:'' }`; `verdictOn` means "comparison shown" and `recorded` means "recorded under What we learned". Insert this exact statement as the second line of `runSteps()`, between the `for` line and `W.ranSteps = true;`:

    if (W.fix) { W.fix.verdictOn = false; W.fix.recorded = false; }

The whole function then reads (everything else unchanged):

    function runSteps(){
      for (var n = 1; n <= 10; n++) W.stepState[n] = STEPS[n].st === 'Waiting' ? 'Ready' : STEPS[n].st;
      if (W.fix) { W.fix.verdictOn = false; W.fix.recorded = false; }
      W.ranSteps = true;
      W.flash = 'Reading the data. This can take a little while. In the wireframe the run finishes at once and every step shows its Ready data again.';
      render();
    }

### Edit 3 — `parts/40-manual-home.js`, add `chars` and use it
`cleanViewName`, `viewNameTooLong` and `noteTooLong` use the ES2015 `Array.from`. Replace it with a local ES5 helper `chars(str)` that returns an array of code points: loop over the string and push each character, but when a high surrogate (`U+D800..U+DBFF`) is followed by a low surrogate (`U+DC00..U+DFFF`), push the two characters together as one element and skip the low one. Define `chars` immediately above the three functions and call it in all three. The exact new text is:

    function chars(str){
      var s = String(str), out = [], i, cu, next;
      for (i = 0; i < s.length; i++) {
        cu = s.charCodeAt(i);
        if (cu >= 0xD800 && cu <= 0xDBFF && i + 1 < s.length) {
          next = s.charCodeAt(i + 1);
          if (next >= 0xDC00 && next <= 0xDFFF) { out.push(s.substr(i, 2)); i++; continue; }
        }
        out.push(s.charAt(i));
      }
      return out;
    }
    function cleanViewName(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).slice(0, MAX_VIEW_NAME).join(''); }
    function viewNameTooLong(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).length > MAX_VIEW_NAME; }
    function noteTooLong(raw){ return chars(String(raw).trim()).length > MAX_ITEM_TEXT; }

`chars` returns the same element count `Array.from` did, so behaviour is otherwise identical.

### Edit 4 — `parts/10-data4-suspects.js`, per-entry evidence step
Give each `SUSPECTS5` entry a numeric `step` field (the step that holds its evidence) and make the "Open the evidence" link call `go(<s.step>)`. Assignments: "Blocking on the Orders tables in SQL Server" = `7` (SQL / blocking / waits evidence is on step 7); "Release 2026.10.26.2 of Orders.Api" = `5` (the changes table on step 5, per `CHANGES0`); "Queueing before the app (outside time)" = `6` (App tier, step 6); "Index maintenance on the Orders table" = `5` (it is listed in the step-5 changes table as `CHANGES0` 08:40). Add `step:N,` directly after each `name:'…',`. Keep the `typeof go` guard and all other markup; the link template becomes:

    + '<p><a href="#"' + (typeof go === 'function' ? ' onclick="go(' + s.step + ');return false;"' : '') + '>Open the evidence</a></p></article>';

## Blocks

### parts/10-data.js
<<<<<<< SEARCH
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;'); }
=======
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
>>>>>>> REPLACE

### parts/20-shell.js
<<<<<<< SEARCH
  for (var n = 1; n <= 10; n++) W.stepState[n] = STEPS[n].st === 'Waiting' ? 'Ready' : STEPS[n].st;
  W.ranSteps = true;
=======
  for (var n = 1; n <= 10; n++) W.stepState[n] = STEPS[n].st === 'Waiting' ? 'Ready' : STEPS[n].st;
  if (W.fix) { W.fix.verdictOn = false; W.fix.recorded = false; }
  W.ranSteps = true;
>>>>>>> REPLACE

### parts/40-manual-home.js
<<<<<<< SEARCH
function cleanViewName(raw){ return Array.from(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).slice(0, MAX_VIEW_NAME).join(''); }
function viewNameTooLong(raw){ return Array.from(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).length > MAX_VIEW_NAME; }
function noteTooLong(raw){ return Array.from(String(raw).trim()).length > MAX_ITEM_TEXT; }
=======
function chars(str){
  var s = String(str), out = [], i, cu, next;
  for (i = 0; i < s.length; i++) {
    cu = s.charCodeAt(i);
    if (cu >= 0xD800 && cu <= 0xDBFF && i + 1 < s.length) {
      next = s.charCodeAt(i + 1);
      if (next >= 0xDC00 && next <= 0xDFFF) { out.push(s.substr(i, 2)); i++; continue; }
    }
    out.push(s.charAt(i));
  }
  return out;
}
function cleanViewName(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).slice(0, MAX_VIEW_NAME).join(''); }
function viewNameTooLong(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).length > MAX_VIEW_NAME; }
function noteTooLong(raw){ return chars(String(raw).trim()).length > MAX_ITEM_TEXT; }
>>>>>>> REPLACE

### parts/10-data4-suspects.js
<<<<<<< SEARCH
  { name:'Blocking on the Orders tables in SQL Server',
=======
  { name:'Blocking on the Orders tables in SQL Server', step:7,
>>>>>>> REPLACE

### parts/10-data4-suspects.js
<<<<<<< SEARCH
  { name:'Release 2026.10.26.2 of Orders.Api',
=======
  { name:'Release 2026.10.26.2 of Orders.Api', step:5,
>>>>>>> REPLACE

### parts/10-data4-suspects.js
<<<<<<< SEARCH
  { name:'Queueing before the app (outside time)',
=======
  { name:'Queueing before the app (outside time)', step:6,
>>>>>>> REPLACE

### parts/10-data4-suspects.js
<<<<<<< SEARCH
  { name:'Index maintenance on the Orders table',
=======
  { name:'Index maintenance on the Orders table', step:5,
>>>>>>> REPLACE

### parts/10-data4-suspects.js
<<<<<<< SEARCH
        + '<p><a href="#"' + (typeof go === 'function' ? ' onclick="go(7);return false;"' : '') + '>Open the evidence</a></p></article>';
=======
        + '<p><a href="#"' + (typeof go === 'function' ? ' onclick="go(' + s.step + ');return false;"' : '') + '>Open the evidence</a></p></article>';
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)
After the blocks are applied, from `buildout/wireframes`:

    node ../packets/data/chk-fix4.js

Expected `PASS`. The check also ES5-scans and `new Function(src)`-parses all four edited files.

## Block format
No new files. Each edit is `### parts/<file>` then `<<<<<<< SEARCH`, the old text, `=======`, the new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line), in the order shown.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).

## Note (out of scope for this packet)
`data/chk-s8.js` hard-asserts the literal `onclick="go(7);return false;"` for every card. After this edit the release card renders `go(5)` and the queueing card `go(6)`, so `chk-s8.js` must be updated to expect each card's own `step`. It is not in this packet's edited-file list, so this packet cannot itself keep it green.
