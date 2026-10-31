# Packet FIX5: Slowest-APIs row links, the Minimum calls rule, and the Nearby header moment

Do exactly this. Touch only the files named below. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). The parts load in sorted filename order into one concatenated script and share the runtime state object `W`. One new global, and only one: `openCallsFor`. No new files.

## 1. `parts/41-tools-a.js` — new global `openCallsFor`, and the row button that calls it

In the `slowest-apis` body, the row map's last cell button currently reads `<button class="quiet" onclick="openTool('slowest-calls')">The calls</button>`; it opens the calls tool unfiltered although the caption says every row links to the calls behind it. Change it to call a new global `openCallsFor(api)` (block 1). Define `openCallsFor` immediately before `function slowestApiFiltered(){` (block 2):

```
function openCallsFor(api){
  W.apiMode = 'one';
  W.apiOne = api;
  W.slowPg = 0;
  W.slowShown = true;
  openTool('slowest-calls');
}
```

`openCallsFor` sets the Slowest calls tool's own API filter to the single API (`W.apiMode='one'` and `W.apiOne=<api>`, the fields `selRoutes()` reads), resets its run state (`W.slowShown=true`, `W.slowPg=0`) so the table redraws, then calls `openTool('slowest-calls')`. In the row button the API name goes into the attribute as `esc(r[0])` with single quotes JS-escaped by `.replace(/'/g, "\\'")`.

## 2. `parts/41-tools-a.js` — the Minimum calls rule

`slowestApiFiltered` uses `var min = Number(W.minCalls); if (!(min > 0)) min = 0;` while the `slowest-apis` form uses `var min = Number(W.minCalls); if (!(min >= 0)) min = 20;`. Make `slowestApiFiltered` use the form's rule (block 3): the only changed substring is `if (!(min > 0)) min = 0;` becoming `if (!(min >= 0)) min = 20;`. Now a value that is not a number or is negative is 20 in both places, matching the box.

## 3. `parts/42-tools-b.js` — the Nearby panel header moment

The `nearby-activity` body opens the panel with `W.nearbyRow = 0` when no call was chosen, so the header names `CALLS[0]`'s moment (Orders.Create at 09:24:11) while the "Around (UTC)" box (`W.nbAround`, default 09:14) and the lane window show 09:14.

- Block 4 replaces the body's two opening lines: on a fresh open (no call chosen) it sets `W.nearbyRow = 0` and `W.nearbyPicked = false`; when a call was already chosen (the row's Nearby activity button has put `W.nearbyRow`), it sets `W.nearbyPicked = true`.
- Block 5 appends a wrapper at the end of the file that leaves `nearbyPanel()`'s own output in place but, when no call is picked, rewrites the header moment to the `W.nbAround` time as `HH:MM` with no call name. `W.nearbyPicked` is falsy by default; it is read only when the panel renders, never at load. If the shown row's own `CALLS[W.nearbyRow]` moment already equals the Around `HH:MM` the header already agrees, so it is left unchanged.

## 4. `data/chk-s1A.js` — the two assertions that named the old text

`chk-s1A` asserts the old row button text and the old escaped `openTool('slowest-calls')` wiring; both change with item 1, so its two assertion lines are updated (blocks 6 and 7; the heading path is relative to `buildout/wireframes`). No other assertion changes.

## Blocks

````
### parts/41-tools-a.js
<<<<<<< SEARCH
        + '<td><button class="quiet" onclick="openTool(\'slowest-calls\')">The calls</button></td></tr>';
=======
        + '<td><button class="quiet" onclick="openCallsFor(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">The calls</button></td></tr>';
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
function slowestApiFiltered(){
=======
function openCallsFor(api){
  W.apiMode = 'one';
  W.apiOne = api;
  W.slowPg = 0;
  W.slowShown = true;
  openTool('slowest-calls');
}
function slowestApiFiltered(){
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
  var min = Number(W.minCalls); if (!(min > 0)) min = 0;
=======
  var min = Number(W.minCalls); if (!(min >= 0)) min = 20;
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
  W.nearbyOpen = true;
  if (W.nearbyRow == null) W.nearbyRow = 0;
=======
  W.nearbyOpen = true;
  if (W.nearbyRow == null) { W.nearbyRow = 0; W.nearbyPicked = false; } else { W.nearbyPicked = true; }
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
    + dt('Buttons', 'Add its check on D1 (live).');
};
=======
    + dt('Buttons', 'Add its check on D1 (live).');
};

// FIX5: with no call chosen, the panel header shows the Around moment, not CALLS[W.nearbyRow].
(function(){
  if (typeof nearbyPanel !== 'function') return;
  var base = nearbyPanel;
  nearbyPanel = function(){
    var html = base.apply(null, arguments);
    if (W.nearbyPicked) return html;
    var around = String(W.nbAround == null ? '' : W.nbAround);
    var i = around.indexOf('T');
    var hhmm = i >= 0 ? around.slice(i + 1, i + 6) : around;
    var call = (typeof CALLS !== 'undefined' && CALLS) ? CALLS[W.nearbyRow] : null;
    if (call && String(call[1]).slice(0, 5) === hhmm) return html;
    return html.replace(/<h4>Nearby activity &middot; [^<]*<\/h4>/, '<h4>Nearby activity &middot; ' + hhmm + ' UTC</h4>');
  };
})();
>>>>>>> REPLACE

### ../packets/data/chk-s1A.js
<<<<<<< SEARCH
eq('sa call links', cnt(sa, '<button class="quiet" onclick="openTool(\'slowest-calls\')">The calls</button>'), 11);
=======
eq('sa call links', cnt(sa, '<button class="quiet" onclick="openCallsFor(\''), 11);
>>>>>>> REPLACE

### ../packets/data/chk-s1A.js
<<<<<<< SEARCH
eq('wire calls link', cnt(s41, "openTool(\\'slowest-calls\\')"), 1);
=======
eq('wire calls link', cnt(s41, "openCallsFor(\\'"), 1);
>>>>>>> REPLACE
````

## Check (you run it; the owner applies the blocks and runs the full check)

```
node --check parts/41-tools-a.js && node --check parts/42-tools-b.js && node ../packets/data/chk-fix5.js
```

`chk-fix5.js` loads the parts in a vm, renders the Slowest APIs table and the calls table, drives `openCallsFor`, the Minimum calls rule and the Nearby header, scans for ES5, runs `new Function` on both edited files and checks the wiring. It also checks that the two `chk-s1A` assertions were updated. After apply, `data/chk-b9.js`, `chk-s1A.js` and `chk-w1.js` must still print `PASS`.

## Output

The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
