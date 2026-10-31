# Packet FIX9: the Slowest calls tool filter and the Step 3 API picker split apart, with a copy button each way

Do exactly this. Do not explore the repo. Touch only the three files named below, in the order shown; make no new files and define no new globals. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). The parts load in sorted filename order into one concatenated script and share the runtime state object `W`; `parts/41-tools-a.js` loads after `parts/30-steps-0-4.js`, and `ROUTES`, `CALLS` and `esc` come from `parts/10-data.js`, so all of them are available when any function below runs. Each SEARCH text occurs exactly once in its file; verify with `grep -c -F` (or equivalent) before replacing.

## Background

Today the Slowest calls tool and the API picker in Step 3 share one filter: `openCallsFor(api)` (the "The calls" button in the Slowest APIs tool) writes `W.apiMode = 'one'; W.apiOne = api;`, which silently changes the Step 3 picker, and `slowCallsTable` always reads `selRoutes()`, i.e. the picker. The owner ruling: each place keeps its own filter. The tool gets its own state key `W.callsApi` (empty string = every API), and each place offers a button that copies its filter to the other place. Nothing is copied unless a button is pressed.

## 1. `parts/20-shell.js` — seed the tool filter

Add `callsApi:''` to the initial `W`, on its own line right after the line that holds the other tool state (`slowShown`, `nearbyRow`, ...). Empty string means every API.

## 2. `parts/30-steps-0-4.js` — `slowCallsTable` takes an optional names list

Change the signature to `slowCallsTable(withNearby, names)`. When `names` is an array use it as the list of API names; otherwise use `selRoutes().map(function(r){ return r[0]; })` exactly as today. The body keeps using a local; rename it `apiNames` so the parameter name `names` is free. Step 3's call site stays `slowCallsTable(true)` — no second argument — so Step 3 still follows the picker, byte for byte.

## 3. `parts/30-steps-0-4.js` — the Step 3 picker gets a copy button

In `apiPicker`, after the picker controls (the mode buttons plus the conditional one-API select or several check boxes) and just before the summary line `var who = 'All 12 APIs';`, append a quiet button and a note. The button copies the picker into the tool filter: when `(W.apiMode||'all')==='one'` it sets `W.callsApi` to `W.apiOne || ROUTES[0][0]`; every other mode (`all`, `several`) copies as `''` (all APIs). Then `W.slowPg=0; render()`. The note reads: `Each place keeps its own filter. This button copies this one to the Slowest calls tool.` Plain ASCII, no apostrophes, no HTML entities.

## 4. `parts/41-tools-a.js` — `openCallsFor` writes only the tool filter

`openCallsFor(api)` now sets `W.callsApi = api; W.slowPg = 0; W.slowShown = true;` and still opens the `slowest-calls` tool. It no longer touches `W.apiMode` or `W.apiOne`.

## 5. `parts/41-tools-a.js` — the `slowest-calls` body gets its own filter row and a copy button

Above the table add a small filter row: a `label.wf` "API" wrapping a `select.wf` whose `onchange` is `W.callsApi=this.value;W.slowPg=0;render()`, with an `All APIs` option (value empty, selected when `W.callsApi` is empty) and one option per route name (value and text through `esc`, `selected` when the name equals `W.callsApi`). Under it a quiet button `Use this filter in Step 3 too` whose onclick copies the tool filter to the picker: when `W.callsApi` is non-empty set `W.apiMode='one'; W.apiOne=W.callsApi;`, else set `W.apiMode='all'` (leave `W.apiOne` as it is); then `render()`. Under the button the note: `Each place keeps its own filter. This button copies this one to Step 3.` The table call becomes `slowCallsTable(false, callNames)` where `callNames` is `[W.callsApi]` when `W.callsApi` is non-empty, else `ROUTES.map(function(r){ return r[0]; })`. Everything after the table (the `W.slowShown` conditional, `TA_callBars()`, the two notes) stays as it is.

## Blocks

### parts/20-shell.js
<<<<<<< SEARCH
  slowShown:true, nearbyRow:null, nearbyPicked:false, nearbyOpen:true, nbAround:'2026-10-26T09:14', nbBefore:5, nbAfter:5, nbTouched:false,
=======
  slowShown:true, nearbyRow:null, nearbyPicked:false, nearbyOpen:true, nbAround:'2026-10-26T09:14', nbBefore:5, nbAfter:5, nbTouched:false,
  callsApi:'',
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
function slowCallsTable(withNearby){
  var names = selRoutes().map(function(r){ return r[0]; });
  var rows = CALLS.map(function(c, i){ return [c, i]; }).filter(function(p){ return names.indexOf(p[0][0]) !== -1; });
=======
function slowCallsTable(withNearby, names){
  var apiNames = (names instanceof Array) ? names : selRoutes().map(function(r){ return r[0]; });
  var rows = CALLS.map(function(c, i){ return [c, i]; }).filter(function(p){ return apiNames.indexOf(p[0][0]) !== -1; });
>>>>>>> REPLACE

### parts/30-steps-0-4.js
<<<<<<< SEARCH
  if (mode === 'several') {
    var set = W.apiSet || [];
    out += ROUTES.map(function(r){
      return '<label class="wf"><input type="checkbox" ' + (set.indexOf(r[0]) !== -1 ? 'checked' : '') + ' onchange="toggleApi(\'' + r[0] + '\')"> ' + r[0] + '</label>';
    }).join('');
  }
  var who = 'All 12 APIs';
=======
  if (mode === 'several') {
    var set = W.apiSet || [];
    out += ROUTES.map(function(r){
      return '<label class="wf"><input type="checkbox" ' + (set.indexOf(r[0]) !== -1 ? 'checked' : '') + ' onchange="toggleApi(\'' + r[0] + '\')"> ' + r[0] + '</label>';
    }).join('');
  }
  out += '<div><button class="quiet" onclick="W.callsApi=(W.apiMode||\'all\')===\'one\'?(W.apiOne||ROUTES[0][0]):\'\';W.slowPg=0;render()">Use this filter in the Slowest calls tool too</button></div>'
    + '<p class="note">Each place keeps its own filter. This button copies this one to the Slowest calls tool.</p>';
  var who = 'All 12 APIs';
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
function openCallsFor(api){
  W.apiMode = 'one';
  W.apiOne = api;
  W.slowPg = 0;
  W.slowShown = true;
  openTool('slowest-calls');
}
=======
function openCallsFor(api){
  W.callsApi = api;
  W.slowPg = 0;
  W.slowShown = true;
  openTool('slowest-calls');
}
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
TOOL_BODIES['slowest-calls'] = function(){
  return slowCallsTable(false) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
    + TA_callBars()
    + '<p class="note">One bar per listed call: the taller the bar, the longer the call took. Read the exact start times in the table.</p>' : '');
};
=======
TOOL_BODIES['slowest-calls'] = function(){
  var callNames = W.callsApi ? [W.callsApi] : ROUTES.map(function(r){ return r[0]; });
  var callsFilter = '<div class="pair"><label class="wf">API<select class="wf" onchange="W.callsApi=this.value;W.slowPg=0;render()">'
    + '<option value=""' + (W.callsApi ? '' : ' selected') + '>All APIs</option>'
    + ROUTES.map(function(r){ return '<option value="' + esc(r[0]) + '"' + (r[0] === W.callsApi ? ' selected' : '') + '>' + esc(r[0]) + '</option>'; }).join('')
    + '</select></label></div>'
    + '<div><button class="quiet" onclick="if(W.callsApi){W.apiMode=\'one\';W.apiOne=W.callsApi;}else{W.apiMode=\'all\';}render()">Use this filter in Step 3 too</button></div>'
    + '<p class="note">Each place keeps its own filter. This button copies this one to Step 3.</p>';
  return callsFilter + slowCallsTable(false, callNames) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
    + TA_callBars()
    + '<p class="note">One bar per listed call: the taller the bar, the longer the call took. Read the exact start times in the table.</p>' : '');
};
>>>>>>> REPLACE

## Check (the owner applies the blocks and runs the full check; you only run this)

From `buildout/wireframes`:

```
node --check parts/20-shell.js && node --check parts/30-steps-0-4.js && node --check parts/41-tools-a.js
```

Expected: no output, no syntax errors. After apply, `node ../packets/data/chk-fix9.js` must print `PASS` — it loads `parts/10-data.js`, `parts/10-data2-calls.js`, `parts/30-pager.js`, `parts/20-shell.js`, `parts/30-steps-0-4.js` and `parts/41-tools-a.js` in a vm with a stubbed DOM, checks the initial `W.callsApi`, drives `openCallsFor`, the filter select state, both copy-button onclick strings in both directions and every picker mode, asserts the tool table filters by `W.callsApi` while Step 3 still follows the picker (and Step 3's output is identical when only `callsApi` changes), ES5-scans and `new Function`-parses the three edited files, and checks no HTML entity sits inside an `inert('...')` literal. `data/chk-fix7.js`, `chk-fix8.js`, `chk-s1A.js`, `chk-s1B.js`, `chk-s2a.js` and `chk-s3.js` must still print `PASS`; none of their assertions touch the changed text.

## Block format
No new files. Each edit is `### <file>` then `<<<<<<< SEARCH`, the old text, `=======`, the new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line), in the order shown above.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
