# Packet S1 (D3 folded in): plain Step 1 copy and a Step 3 API picker

Goal: fix owner issues 2 and 4. Step 1 says what the screen is for and explains the gap in plain words. Step 3 says it is an average across the APIs chosen and lets the user pick all / one / several APIs.
Target file (only): `parts/30-steps-0-4.js`. Depends on: nothing. Source files are CRLF; you only emit blocks, the applier handles line endings.
You may not touch any other file. `W` is defined in `parts/20-shell.js`; do NOT add fields there: read new fields with a fallback, e.g. `(W.apiMode || 'all')`.

## 1. Context (read-only facts; do not explore)
- `ROUTES` rows: `[name, calls, incP95, refP95, changePct, changeMs, 'bad'|'grey', label]`. 12 rows.
- `PARTS` items: `{k:'A'|'C'|'P'|'O', label, normal, inc, share, tested}`; `share` is a fraction of the extra time.
- `CALLS` rows: `[api, 'HH:MM:SS', ms]`.
- In `30-steps-0-4.js`: `step1(st)` (about lines 88-110), `window.stepNotes1`, `partsTable()`, `partsBars()`, `slowCallsTable(withNearby)`, `step3(st)`, `window.stepNotes3`. Helpers already exist: `nutshell(text)`, `reasonsBox(array)`, `dt(label, html)`, `stateSeg(n)`, `fmtMs`, `fmtPct`, `render()`.
- Buttons in a button strip use `<div class="row">` containing `<button class="btn sm">`; the quick-window strip uses `<div class="picks" role="group" aria-label="...">`. Reuse those classes. No new CSS.

## 2. Step 1 edits (copy; use these strings exactly)
1. In `step1`, replace the `nutshell(...)` text with: `Both windows hold calls. One 13-minute stretch (09:31 to 09:44) has no data and is left out. The clocks agree.`
2. Replace the `<p class="lead" ...>Check that the data can be trusted before reading what it says.</p>` line text with: `This step checks that the data is good enough to draw conclusions from: that both windows hold enough calls, that no stretch of time is missing, and that the clocks of the sources agree. If something here is wrong, read the later steps with care.`
3. In the "Time gaps" card, replace the note `1 long: those minutes are not tested.` with `1 long gap: those minutes are left out.`
4. Replace the gap list item `26 Oct 09:31 to 26 Oct 09:44 UTC, 13 minutes, incident window. Long gap: these minutes are Cannot test yet.` with `26 Oct 09:31 to 09:44 UTC (13 minutes, incident window): no calls were recorded. These minutes are left out of every comparison, so a change inside them cannot be seen.`
5. In the `reasonsBox([...])` of step1, replace the first string with `The minutes 09:31 to 09:44 have no recorded calls, so they are left out of the comparison.`
6. In `window.stepNotes1`, change the `Chart` dt text to `None. Four trust cards, a Sources table and a list of gaps.`

## 3. Step 3 edits (API picker)
State (read with fallbacks, write on click): `W.apiMode` is `'all'` | `'one'` | `'several'` (default `'all'`); `W.apiOne` is a route name (default `ROUTES[0][0]`); `W.apiSet` is an array of route names (default `[]`, meaning all when mode is `'several'` and empty).

Add these functions above `function step3`:
- `selRoutes()` returns the selected `ROUTES` rows: all rows for mode `'all'`, the row named `W.apiOne` for `'one'`, rows whose name is in `W.apiSet` for `'several'` (all rows if `W.apiSet` is empty or missing).
- `selParts()` returns a new array of copies of `PARTS` for the selection: if the selection is all 12 rows return `PARTS` itself. Otherwise `fi = mean(incP95 of selected) / mean(incP95 of all)`, `fn = mean(refP95 of selected) / mean(refP95 of all)`; each part gets `normal = Math.max(1, Math.round(p.normal * fn))`, `inc = Math.max(1, Math.round(p.inc * fi))`; then `share = max(0, inc - normal) / total` where `total` is the sum over parts of `max(0, inc - normal)` (all shares 0 if `total` is 0).
- `apiPicker()` returns the picker markup, placed in `step3` right after the lead paragraph and before `partsTable()`:
  - A `<div class="picks" role="group" aria-label="APIs included">` holding the note `APIs included` and three buttons: `All APIs`, `One API`, `Several`. Each sets `W.apiMode` and calls `render()`. The active one gets the extra class `primary`.
  - Mode `'one'`: a `<label class="wf">API<select class="wf" onchange="W.apiOne=this.value;render()">` with one `<option>` per route (selected one marked).
  - Mode `'several'`: one `<label class="wf"><input type="checkbox" ...> name</label>` per route, checked when in `W.apiSet`, `onchange` toggles the name in `W.apiSet` then `render()`.
  - A line `<p class="note">` that reads `These figures are the average of the calls of: All 12 APIs.` (mode all), `...of: Orders.Create.` (one), or `...of: <comma list>.` / `...of: All 12 APIs.` (several).

Change `partsTable()`, `partsBars()` and the slow-calls table to use the selection:
- In `partsTable()` and `partsBars()` replace every use of `PARTS` with `selParts()` called once into a local `var parts = selParts();`.
- In `slowCallsTable`, filter `CALLS` to rows whose `c[0]` is in `selRoutes().map(r => r[0])`; keep the original index `i` for the existing `W.nearbyRow` logic by mapping `CALLS.map((c,i)=>[c,i])` before filtering. If the filter leaves no rows show `<p class="note">No slow call in this selection.</p>` in place of the table.
- Replace the fixed `nutshell('Outside time (O) holds 85 % ...')` and the fixed note `Outside time (O) grew the most: it holds 85 % of the extra time.` in `step3` with text computed from `selParts()`: find the part with the largest `share`; nutshell `<label> holds <round(share*100)> % of the extra time: ...` using the existing sentence tail only when that part is `O` (`the wait is before or beside the app code, not in the audit or the payload write.`), otherwise the tail `most of the extra time is inside the app.`; the note repeats `<label> grew the most: it holds <n> % of the extra time.` If every share is 0 the nutshell is `This selection did not slow down.` and the note is omitted.
- In the `lead` paragraph of `step3` append the sentence ` The figures are the average of the APIs you select below.`

In `window.stepNotes3` add a `dt('API picker', ...)` entry before `Buttons`: `Chooses which APIs the time split averages: All APIs (default), One API (a drop-down) or Several (check boxes). The table, the bars, the nutshell and the slowest calls follow the choice (live). Wireframe figures for a single API are scaled from the all-API split.`

## 4. Do not touch
Any other function; `step0`, `step2`, `step4`, `nearbyPanel`; any other file. Keep the existing ES5 style (`var`, `function`), no template literals, no arrow functions.

## 5. Checks (run from `buildout/wireframes` after applying your blocks; report the output)
1. `node build-guided.js` exits 0.
2. `node -e "const vm=require('vm'),fs=require('fs');const c={window:{}};c.W={};vm.createContext(c);const src=['10-data.js','20-shell.js','30-steps-0-4.js'].map(f=>fs.readFileSync('parts/'+f,'utf8')).join('\n');try{vm.runInContext(src+';this.s3=step3;this.s1=step1;this.W=W;',c)}catch(e){console.log('FAIL load '+e.message);process.exit(1)};const all=c.s3('Ready');c.W.apiMode='one';c.W.apiOne='Orders.Get';const one=c.s3('Ready');c.W.apiMode='several';c.W.apiSet=['Orders.Create','Orders.List'];const sev=c.s3('Ready');const s1=c.s1('Ready');console.log(all.includes('All 12 APIs')&&one.includes('of: Orders.Get')&&sev.includes('Orders.Create, Orders.List')&&all!==one&&s1.includes('no calls were recorded')?'PASS':'FAIL')"` prints `PASS`. If the DOM-free load fails because a global is missing from the stub, say which; do not edit other files to fix it.

## 6. Output
Return ONLY the SEARCH/REPLACE blocks, one per edit, in this exact format, then a 3-line status. Each SEARCH text is copied from the file as it is now and occurs exactly once; keep each block small (one statement or one function).

```
### parts/30-steps-0-4.js
<<<<<<< SEARCH
old text
=======
new text
>>>>>>> REPLACE
```
Status lines: `Status: PASS|FAIL`, `Checks: <the two outputs>`, `Concerns: <none or one line>`.
