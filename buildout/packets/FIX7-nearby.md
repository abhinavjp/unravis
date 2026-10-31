# Packet FIX7: fix the Nearby-activity state bugs and two Step 3/4 copy bugs

Do exactly this. Touch only the files named below. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). The parts load in sorted filename order into one concatenated script and share the runtime state object `W`. No new globals except `nearbyPicked` (a field of `W`, not a top-level global) and the parameter `noClose` of the existing function `nearbyPanel`. No new files. Files touched: `parts/20-shell.js`, `parts/30-steps-0-4.js`, `parts/31-steps-5-9.js`, `parts/42-tools-b.js`.

## 1. `parts/20-shell.js` — `nearbyRow` starts unpicked
`W.nearbyRow:0` means a call is "picked" from the start, even though nothing was clicked. Change the initial value to `null` and add `nearbyPicked:false` beside it (edit 1).

## 2. `parts/30-steps-0-4.js` — `nearbyPanel` is null-safe and takes a `noClose` parameter
`nearbyPanel()` reads `CALLS[W.nearbyRow]` directly; with `W.nearbyRow` now possibly `null` this would read `CALLS[null]` (`undefined`) and crash on `c[0]`. Guard it: when `W.nearbyRow` is `null`, use `CALLS[0]` (edit 6). The panel's own lane output (the hard-coded `bars`/`sqlLane` arrays) is untouched, so the default Around 09:14 view is pixel-identical to today; only the header moment can differ, and the Step 4 wrapper in `42-tools-b.js` (already shipped, unchanged by this packet) rewrites that header to the Around time whenever `W.nearbyPicked` is falsy. `slowCallsTable`'s row-highlight test `i === W.nearbyRow` is already null-safe (a number is never `=== null`), so it needs no edit.

Add an optional parameter `noClose` to `nearbyPanel` (edit 6) and use it to suppress the panel's own Close button, which does nothing on the Nearby activity tool page because that tool's body always forces `W.nearbyOpen = true` right back open (edit 7). Step 3's own call to `nearbyPanel()` (no argument) keeps the Close button.

The "Nearby activity" button on a Slowest-calls row sets `W.nearbyRow`/`W.nearbyOpen` but never marked the call as picked; add `W.nearbyPicked=true;` to that onclick (edit 5).

## 3. `parts/42-tools-b.js` — `TOOL_BODIES['nearby-activity']` sets nothing but `nearbyPicked`, and hides its own Close button
The tool body currently forces `W.nearbyRow = 0` on a fresh open. Replace that: never write `W.nearbyRow`; only compute whether a call is picked (`W.nearbyRow != null`), and pass `true` for `nearbyPanel`'s new `noClose` parameter since this page already has no live Close affordance (edit 10).

## 4. `parts/30-steps-0-4.js` + `parts/31-steps-5-9.js` — the pool-limit note no longer hard-codes 100
Step 4's note and Step 6's first finding both hard-code the pool limit as `100`. Both must read `W.frame.poolLimit` (trimmed): when non-empty, name it; when empty, say no pool limit was entered. Step 4 uses the same two full sentences already used by the `calls-in-flight` tool body in `parts/42-tools-b.js` line 11 (unedited, shown here only as the wording reference):
```
'<p class="note">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>'
'<p class="note">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>'
```
(edit 8). Step 6's finding uses the short forms (edit 9):
```
'Calls in flight peaked at 34 of the pool limit ' + W.frame.poolLimit.trim() + ': the pool was not filled.'
'Calls in flight peaked at 34; no pool limit was entered.'
```

## 5. `parts/30-steps-0-4.js` only — `&rsquo;` inside `inert(...)` string literals
`inert(what)` stores `what` into `W.flash`; `flashLine()` then renders `esc(W.flash)`, and `esc` (in `parts/10-data.js`) escapes `&` to `&amp;` but does not touch apostrophes. A literal `&rsquo;` written inside an `inert('...')` argument is therefore displayed verbatim as the text `&rsquo;s`, not as an apostrophe. There are exactly three such calls in this file (all read `inert('Opens the case&rsquo;s Add data flow')`, in three different surrounding lines). Replace `&rsquo;` with a literal apostrophe in all three, written as an escaped quote `\'` inside the already single-quoted JS string passed to `inert(`, keeping the outer HTML attribute (double-quoted) and the JS string (single-quoted) both valid (edits 2, 3, 4). Nothing else in the file changes; the two unrelated `&rsquo;` occurrences inside `dt(...)` wireframe-notes text (lines with "Add data opens the case&rsquo;s Add data flow (inert)." and "the app&rsquo;s series colours.") are plain documentation copy, are never passed through `inert`/`esc`, and must be left exactly as they are.

## Blocks

Apply every block below, in order. Each `SEARCH` is byte-for-byte the current file content and occurs exactly once (verified with `grep -c -F`, or a literal substring count for the two multi-line blocks).

````
### parts/20-shell.js
<<<<<<< SEARCH
  slowShown:true, nearbyRow:0, nearbyOpen:true, nbAround:'2026-10-26T09:14', nbBefore:5, nbAfter:5, nbTouched:false,
=======
  slowShown:true, nearbyRow:null, nearbyPicked:false, nearbyOpen:true, nbAround:'2026-10-26T09:14', nbBefore:5, nbAfter:5, nbTouched:false,
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
    + (cta ? '<div class="row"><button class="btn sm" onclick="inert(\'Opens the case&rsquo;s Add data flow\')">' + cta + '</button></div>' : '');
=======
    + (cta ? '<div class="row"><button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">' + cta + '</button></div>' : '');
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
  if (st === 'Cannot test yet') pre = '<p class="notice">This case has no source yet, so there is nothing to read. Add data first. <button class="btn sm" onclick="inert(\'Opens the case&rsquo;s Add data flow\')">Add data</button></p>';
=======
  if (st === 'Cannot test yet') pre = '<p class="notice">This case has no source yet, so there is nothing to read. Add data first. <button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">Add data</button></p>';
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
    + '<div class="row"><button class="btn sm" onclick="go(0)">Change the window</button><button class="btn sm" onclick="inert(\'Opens the case&rsquo;s Add data flow\')">Add data</button></div></div>' : '';
=======
    + '<div class="row"><button class="btn sm" onclick="go(0)">Change the window</button><button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">Add data</button></div></div>' : '';
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render()">Nearby activity</button></td>' : '');
=======
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render()">Nearby activity</button></td>' : '');
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
function nearbyPanel(){
  var c = CALLS[W.nearbyRow];
=======
function nearbyPanel(noClose){
  var c = CALLS[W.nearbyRow == null ? 0 : W.nearbyRow];
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
  return '<section class="card nearby" aria-label="Nearby activity"><div class="sthead"><h4>Nearby activity &middot; ' + c[0] + ' at ' + c[1] + '</h4><span class="sp"></span><button class="btn sm" onclick="W.nearbyOpen=false;render()">Close</button></div>'
=======
  return '<section class="card nearby" aria-label="Nearby activity"><div class="sthead"><h4>Nearby activity &middot; ' + c[0] + ' at ' + c[1] + '</h4><span class="sp"></span>' + (noClose ? '' : '<button class="btn sm" onclick="W.nearbyOpen=false;render()">Close</button>') + '</div>'
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
  + '<p class="note">Calls in flight peaked at 34 of the 100 entered as the pool limit. The pool was not filled.</p>'
=======
  + (W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class="note">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>' : '<p class="note">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>')
>>>>>>> REPLACE

### parts/31-steps-5-9.js
<<<<<<< SEARCH
  + findingsList(['Calls in flight peaked at 34 of the pool limit 100: the pool was not filled.', 'Outside time (O) rose from 150 ms to 1,584 ms per call.', 'Threads were not the limit: the work waited before it started.'])
=======
  + findingsList([(W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? 'Calls in flight peaked at 34 of the pool limit ' + W.frame.poolLimit.trim() + ': the pool was not filled.' : 'Calls in flight peaked at 34; no pool limit was entered.'), 'Outside time (O) rose from 150 ms to 1,584 ms per call.', 'Threads were not the limit: the work waited before it started.'])
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
TOOL_BODIES['nearby-activity'] = function(){
  W.nearbyOpen = true;
  if (W.nearbyRow == null) { W.nearbyRow = 0; W.nearbyPicked = false; } else { W.nearbyPicked = true; }
  return nearbyPanel() + (typeof nearView === 'function' ? nearView() : '');
};
=======
TOOL_BODIES['nearby-activity'] = function(){
  W.nearbyOpen = true;
  W.nearbyPicked = W.nearbyRow != null;
  return nearbyPanel(true) + (typeof nearView === 'function' ? nearView() : '');
};
>>>>>>> REPLACE
````

## Check (you run it; the owner applies the blocks and runs the full check)

```
node --check parts/20-shell.js && node --check parts/30-steps-0-4.js && node --check parts/31-steps-5-9.js && node --check parts/42-tools-b.js && node ../packets/data/chk-fix7.js
```

`chk-fix7.js` loads `10-data.js`, `10-data2-calls.js`, `10-data3-near.js`, `10-data5-toolsA.js`, `11-charts-c.js`, `20-shell.js`, `30-frame-helpers.js`, `30-pager.js`, `30-steps-0-4.js`, `31-steps-5-9.js`, `41-tools-a.js`, `42-tools-b.js` in a vm with a stub `document`/`addEventListener`/`history` (as `chk-fix4.js` does for `20-shell.js`); asserts the default `W.nearbyRow === null` and `W.nearbyPicked === false`; renders `TOOL_BODIES['nearby-activity']()` at the default and checks the header shows `09:14` and no call name or " at "; extracts the real "Nearby activity" row-button `onclick` string from `slowCallsTable(true)`, evaluates it with `vm.runInContext`, and checks the header then shows the picked call's name and moment and `W.nearbyPicked === true`; checks the tool page has no `Close</button>` while `step3('Ready')`'s own panel still has one; renders `step4('Ready')` and `step6('Ready')` with `W.frame.poolLimit` set to `''` and to a value, checking both wordings each time; checks `30-steps-0-4.js` has no `inert(` call left containing `&rsquo;` (the two unrelated `dt(...)` copy occurrences are untouched); ES5-scans and `new Function`s all four edited files; and greps the wiring (new default literals, `nearbyPanel(noClose)`, `nearbyPanel(true)`, the removed hard-coded `100` text in both files, the removed old `nearbyRow = 0` tool-body line). `data/chk-fix5.js`, `chk-b9.js`, `chk-s1A.js`, `chk-w1.js`, `chk-s2a.js`, `chk-s3.js` assert nothing about `nearbyRow`'s default, the Nearby/Step4/Step6 pool wording, or the three `inert(` strings, so none of their assertions are touched by this packet and all keep passing unedited.

## Output

The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
