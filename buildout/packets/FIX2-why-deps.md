# Packet FIX2: Why view honours a pretended-off hard dependency; pretend-off buttons use short names

Goal: fix two QA defects in the evidence area. Edited files only: `parts/13-why-b.js` and `parts/12-evidence-g.js`. Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). The one new global is `EVDEP_NAMES`. Read the two files first.

## Defect 1: the "Why this colour?" view ignores a pretended-off hard dependency

`parts/13-why-b.js`, function `whyView(c)`. Today it reads raw `c.verdict` and always calls `evChart(c)`, so a card whose hard dependency is pretended off (`evDepBlocked(c)`) still draws its chart in the Why view, while the card list (`parts/12-evidence-d.js`, `evidenceCard`) shows a grey "Not collected" card. Compute the chip verdict from `evDepEffVerdict(c)` (guarded, falling back to `c.verdict`) and, when `evDepBlocked(c)` is true (guarded), do not call `evChart(c)`; show instead the blocked sentence below, copied byte-for-byte from `evidenceCard`.

### parts/13-why-b.js (edit 1 of 2)
<<<<<<< SEARCH
  var v = (c.verdict === 'ok' || c.verdict === 'watch' || c.verdict === 'bad' || c.verdict === 'none') ? c.verdict : 'none';
=======
  var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;
  var v = (ev0 === 'ok' || ev0 === 'watch' || ev0 === 'bad' || ev0 === 'none') ? ev0 : 'none';
>>>>>>> REPLACE

### parts/13-why-b.js (edit 2 of 2)
<<<<<<< SEARCH
  out += evChart(c);
=======
  if (typeof evDepBlocked === 'function' && evDepBlocked(c)) {
    out += '<p class="ev-blocked">No chart or table: the data this card needs was not collected.</p>';
  } else {
    out += evChart(c);
  }
>>>>>>> REPLACE

Leave every other line of `whyView` unchanged (the `evTable(c)`, `evHelpLook(c)` and `evHelpMore(c, true)` calls stay as they are).

## Defect 2: pretend-off buttons use the long state sentence as the label

`parts/12-evidence-g.js`, function `evDepToggles(group)`. The buttons are labelled with `EVDEP_DEFS[k].title` (the long sentence, e.g. "Pretend off: Query Store is off for this database"). Add a short name per banner id in a new object `EVDEP_NAMES`, declared in the same file. Every id in `EVDEP_DEFS` (`parts/14-deps-data.js`: `querystore`, `qswaits`, `systemhealth`, `viewstate`, `profiler`, `monitorrole`, `oplogread`, `blackbox`, `perfaccess`) must have a name; fall back to the id. Label the button `Pretend off: <name>` when not pressed and `Stop pretending: <name>` when pressed (`aria-pressed="true"`). Keep `aria-pressed` and the `evDepToggle(...)` handler unchanged.

### parts/12-evidence-g.js (edit 1 of 2)
<<<<<<< SEARCH
// Evidence banners: pretend a dependency source is missing (packet B2).
// Globals used from elsewhere: W (state), render(), EVDEP_DEFS, EVDEP_MAP.

function evEsc(s) {
=======
// Evidence banners: pretend a dependency source is missing (packet B2).
// Globals used from elsewhere: W (state), render(), EVDEP_DEFS, EVDEP_MAP.

var EVDEP_NAMES = {
  querystore: 'Query Store',
  qswaits: 'Query Store wait stats',
  systemhealth: 'system_health capture',
  viewstate: 'VIEW SERVER STATE',
  profiler: 'Profiler trace',
  monitorrole: 'Monitor role',
  oplogread: 'oplog read',
  blackbox: 'Blackbox',
  perfaccess: 'performance counter access'
};

function evEsc(s) {
>>>>>>> REPLACE

### parts/12-evidence-g.js (edit 2 of 2)
<<<<<<< SEARCH
    h += '<button type="button" class="btn" aria-pressed="' + (o[k] ? 'true' : 'false')
      + '" onclick="evDepToggle(\'' + k + '\')">Pretend off: ' + EVDEP_DEFS[k].title + '</button>';
=======
    h += '<button type="button" class="btn" aria-pressed="' + (o[k] ? 'true' : 'false')
      + '" onclick="evDepToggle(\'' + k + '\')">' + (o[k] ? 'Stop pretending: ' : 'Pretend off: ') + (EVDEP_NAMES[k] || k) + '</button>';
>>>>>>> REPLACE

## Do not touch
Anything else in either file. Do not change `evEsc`, `evDepsOff`, `evDepFor`, `evDepBlocked`, `evDepEffVerdict`, `evDepBanner`, `evDepToggle`, `evCopy`, `evidenceCard`, `EVDEP_DEFS`, or `EVDEP_MAP`.

## Check
From `buildout/wireframes` run `node ../packets/data/chk-fix2.js`. It must print `PASS`. It loads the evidence parts in a vm, asserts the blocked-card Why view has no chart markup and shows the blocked sentence, asserts the button labels in both states, asserts every `EVDEP_DEFS` id has a name, runs the ES5 scan, and parses both edited files with `new Function(src)`.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
