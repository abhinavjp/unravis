# Packet S8 (id s8): Step 5 gets Nearby activity and suspect cards, in `parts/10-data4-suspects.js` + an edit to `parts/31-steps-5-9.js`

Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Globals that exist elsewhere and you only USE (always inside functions, never at load time): `go(n)` (defined in `parts/20-shell.js`), `nearView()` (defined in `parts/10-data3-near.js`, guarded by `typeof`).

New globals (only these): `SUSPECTS5`, `RULED_OUT5`, `suspectCards`, `ruledOutDrawer5`. These are renamed from the names `SUSPECTS` / `ruledOutDrawer` you might expect, because both already exist with a different shape and are load-bearing elsewhere: `SUSPECTS` is defined in `parts/10-data.js` and used by `suspectsHere`/`addCheck` in `parts/31-steps-5-9.js` itself (the very file you are editing) and by the Step 10 ranking in `parts/32-steps-10-13.js` (`s.colour`, `s.id`); `ruledOutDrawer(cards)` is defined in `parts/13-why-b.js` and used by the Step 7/8/9 evidence cards in `parts/33-step7-evidence.js`, `parts/34-step8-evidence.js`, `parts/35-step9-evidence.js`. Reusing those exact names would silently break all of that. Do not touch those other files.

## New file `parts/10-data4-suspects.js`
Contains the data and both new render functions, byte-for-byte as below.

### parts/10-data4-suspects.js
```js
// Step 5 suspect cards: deterministic fixture rows for "who might be responsible"
// and the suspects the data has already set aside. Named with a "5" suffix because
// a differently-shaped SUSPECTS array (and a differently-shaped ruledOutDrawer
// function) already exist elsewhere in the app; see the brief header.
var SUSPECTS5 = [
  { name:'Blocking on the Orders tables in SQL Server',
    why:'Lock waits rose at 09:14, the same minute the slow end moved.',
    confidence:'High',
    evidence:[
      'Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.',
      'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.',
      'The slowest calls of Orders.Create (up to 4,812 ms) overlap the blocking chains.'
    ] },
  { name:'Release 2026.10.26.2 of Orders.Api',
    why:'Deployed 9 minutes before the onset, the closest listed change.',
    confidence:'Medium',
    evidence:[
      'Change listed for 09:05; the onset is 09:14, 9 minutes later.',
      'No change after 09:05 is listed.'
    ] },
  { name:'Queueing before the app (outside time)',
    why:'Outside time grew sharply and holds most of the extra time, but where the wait happens is not in the data.',
    confidence:'Medium',
    evidence:[
      'Outside time (O) rose from 150 ms to 1,584 ms per call.',
      'Outside time holds 85% of the extra time.'
    ] },
  { name:'Index maintenance on the Orders table',
    why:'Finished well before the onset, so the timing fits less well than the release.',
    confidence:'Low',
    evidence:[
      'Index maintenance finished at 08:40, 34 minutes before the onset.',
      'No lock or error event was recorded between 08:40 and 09:14.'
    ] }
];
var RULED_OUT5 = [
  { name:'Higher load: more calls arriving',
    reason:'Calls arriving per second were about the same (10.2 against 10.4); the system filled because calls took longer, not because more arrived.' },
  { name:'Clock difference between sources',
    reason:'7,204 calls had both a start and an end; the clocks agree within 2 seconds, so a clock gap cannot explain the slow end.' },
  { name:'Larger payloads on Orders.Create',
    reason:'The median request size stayed at 2.1 KB through the window, so a size increase cannot explain the extra 1,680 ms per call.' }
];
function suspectCards(){
  return '<div class="suspects5"><h4 style="font-size:14px">Who might be responsible</h4><div class="scards">'
    + SUSPECTS5.map(function(s){
      return '<article class="scard conf-' + s.confidence.toLowerCase() + '"><h5>' + s.name + '</h5>'
        + '<p class="note">' + s.why + '</p>'
        + '<p><strong>Confidence:</strong> ' + s.confidence + '</p>'
        + '<ul><li>' + s.evidence.join('</li><li>') + '</li></ul>'
        + '<p><a href="#"' + (typeof go === 'function' ? ' onclick="go(7);return false;"' : '') + '>Open the evidence</a></p></article>';
    }).join('')
    + '</div></div>';
}
function ruledOutDrawer5(){
  return '<details class="ev-fold"><summary>Ruled out</summary><ul>'
    + RULED_OUT5.map(function(r){ return '<li><strong>' + r.name + ':</strong> ' + r.reason + '</li>'; }).join('')
    + '</ul></details>';
}
```

## New file `parts/02-css-suspects.html`
Exactly this line, then a newline. Uses only the tokens `--line`, `--r`, `--surface`, `--bad`, `--warn`, `--text-2`.

### parts/02-css-suspects.html
```html
<style>.scards{display:flex;flex-wrap:wrap;gap:8px;margin-top:8px}.scard{border:1px solid var(--line);border-radius:var(--r);background:var(--surface);padding:8px;flex:1 1 220px}.scard.conf-high{border-color:var(--bad)}.scard.conf-medium{border-color:var(--warn)}.scard.conf-low{border-color:var(--text-2)}.scard h5{margin:0 0 4px;font-size:14px}</style>
```

## Edit to `parts/31-steps-5-9.js`
Verify the SEARCH occurs exactly once: `grep -c -F "  + body + suspectsHere(['C1']);" parts/31-steps-5-9.js` must print `1`.

### parts/31-steps-5-9.js
<<<<<<< SEARCH
  + body + suspectsHere(['C1']);
=======
  + body + suspectsHere(['C1'])
  + (typeof nearView === 'function' ? nearView() : '')
  + suspectCards() + ruledOutDrawer5();
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/10-data4-suspects.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/31-steps-5-9.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
