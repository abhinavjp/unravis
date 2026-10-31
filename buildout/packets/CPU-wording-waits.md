# Packet CPU: CPU-card wording, Copy query ID, per-query wait column, Query Store banner

Do exactly this. Do not explore the repo. Every SEARCH below is copied byte-for-byte from the current files and occurs exactly once; verify each with `grep -c -F` before you finish (all must print `1`). Apply in this order: the new file, then the five edits. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). New globals in `parts/12-evidence-i.js`: `evQueryWaitsOn`, `evQueryTable` (only these). The card fixture adds no global. Code runs in one concatenated script; parts load in sorted filename order (`12-evidence-c.js` before `12-evidence-h.js` before `12-evidence-i.js`), and `evTable` only calls `evQueryTable` at render time, guarded with `typeof`.

The CPU card is the SQL fixture with id `sql-cpu-high` (its id contains `cpu`). Inside the card, `evTable` renders the card's `table` (the top waits) first, then appends `evQueryTable(c)` (the top queries).

## Contract (exact)

- `evQueryWaitsOn(c)` -> boolean. `true` only when all hold: `typeof EVDEP_MAP === 'object' && EVDEP_MAP`; `typeof EVDEP_DEFS === 'object' && EVDEP_DEFS && EVDEP_DEFS.qswaits`; `EVDEP_MAP[c.id]` lists `'qswaits'`; and `evDepsOff()` (when it is a function) does not have `qswaits === true`. Otherwise `false`.
- `evQueryTable(c)` -> HTML string. Returns `''` when `!c` or `c.queries` is not an array. Otherwise returns `<table class="wf"><caption>Top queries in the incident window</caption>` with header `Query ID`, `Calls`, `Avg ms`, `p95 ms`, plus a fifth `Wait type` header only when `evQueryWaitsOn(c)` is true. Each row's first cell is `evQueryId(q.id)` (Copy button plus text); other cells are `evEsc2(q.calls)`, `evEsc2(q.avg)`, `evEsc2(q.p95)`, and, only when the wait column is on, `evEsc2(q.wait)`. When `evQueryWaitsOn(c)` is false the `Wait type` column is omitted and the exact note `<p class="note">Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled.</p>` is appended.
- `evQueryId(id)` (existing, edited): the Copy button label stays `Copy query ID`; the trailing sentence becomes exactly `Open the plan with showplan in SSMS or Azure Data Studio using this query ID.` No plan XML is shown anywhere.
- `evTable(c)` (existing, edited): (a) `!c` returns `''`; (b) when `typeof evDepBlocked === 'function' && evDepBlocked(c)` and `typeof evDepBanner === 'function'`, it returns `evDepBanner(c)` (Query Store off shows the banner only, with no table); (c) otherwise unchanged, and after the existing `</tbody></table>` it appends `evQueryTable(c)` when that function exists.
- Card `sql-cpu-high`: `label` and `why.summary` both contain the exact wording `CPU is high; here are the top waits and queries`; `verdict` is `bad`; `queries` has 4 rows.
- `EVDEP_MAP['sql-cpu-high'] = ['querystore', 'qswaits']`.
- `EV_SETS.incident` (in `parts/33-step7-evidence.js`) lists `sql-cpu-high` first so the card renders in Step 7.

### parts/12-evidence-i.js
```js
// Query table for evidence cards that carry a per-query list (packet CPU).
// Globals used from elsewhere: evEsc2 and evQueryId (parts/12-evidence-h.js),
// EVDEP_MAP and EVDEP_DEFS (parts/14-deps-data.js), evDepsOff (parts/12-evidence-g.js).

function evQueryWaitsOn(c) {
  if (typeof EVDEP_MAP !== 'object' || !EVDEP_MAP || typeof EVDEP_DEFS !== 'object' || !EVDEP_DEFS || !EVDEP_DEFS.qswaits) { return false; }
  var keys = EVDEP_MAP[c.id] || [];
  var has = false;
  var i;
  for (i = 0; i < keys.length; i++) {
    if (keys[i] === 'qswaits') { has = true; }
  }
  if (!has) { return false; }
  var off = (typeof evDepsOff === 'function') ? evDepsOff() : null;
  return !(off && off.qswaits === true);
}

function evQueryTable(c) {
  if (!c || Object.prototype.toString.call(c.queries) !== '[object Array]') { return ''; }
  var waitOn = evQueryWaitsOn(c);
  var h = '<table class="wf"><caption>Top queries in the incident window</caption><thead><tr><th>Query ID</th><th>Calls</th><th>Avg ms</th><th>p95 ms</th>';
  if (waitOn) { h += '<th>Wait type</th>'; }
  h += '</tr></thead><tbody>';
  var i;
  for (i = 0; i < c.queries.length; i++) {
    var q = c.queries[i];
    h += '<tr><td>' + evQueryId(q.id) + '</td><td>' + evEsc2(q.calls) + '</td><td>' + evEsc2(q.avg) + '</td><td>' + evEsc2(q.p95) + '</td>';
    if (waitOn) { h += '<td>' + evEsc2(q.wait) + '</td>'; }
    h += '</tr>';
  }
  h += '</tbody></table>';
  if (!waitOn) {
    h += '<p class="note">Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled.</p>';
  }
  return h;
}
```

### parts/14-scenarios.js
<<<<<<< SEARCH
   "sampled": false
  }
 ],
 "mongo": [
=======
   "sampled": false
  },
  {
   "id": "sql-cpu-high",
   "label": "CPU is high; here are the top waits and queries",
   "verdict": "bad",
   "rule": "Red: CPU is high in the incident window and one bounded wait type holds most of the wait delta while the top query holds the extra time.",
   "chart": "bars",
   "series": [
    {
     "name": "Top wait share (%)",
     "win": "normal",
     "pts": [ { "t": 0, "v": 14 }, { "t": 30, "v": 15 }, { "t": 60, "v": 44 }, { "t": 90, "v": 51 }, { "t": 110, "v": 48 } ]
    },
    {
     "name": "Top wait share (%)",
     "win": "incident",
     "pts": [ { "t": 60, "v": 44 }, { "t": 90, "v": 51 }, { "t": 110, "v": 48 } ]
    }
   ],
   "table": {
    "cols": [ "Wait type", "Incident share", "Per-task delta" ],
    "rows": [
     [ "PAGEIOLATCH_SH", "44%", "+124 ms" ],
     [ "WRITELOG", "31%", "+104 ms" ],
     [ "SLEEP_TASK", "16%", "-3 ms" ]
    ]
   },
   "queries": [
    { "id": "1204", "calls": "1,204", "avg": "142", "p95": "190", "wait": "PAGEIOLATCH_SH" },
    { "id": "3318", "calls": "3,318", "avg": "31", "p95": "58", "wait": "SLEEP_TASK" },
    { "id": "2907", "calls": "2,907", "avg": "12", "p95": "26", "wait": "LCK_M_S" },
    { "id": "1205", "calls": "1,204", "avg": "9", "p95": "21", "wait": "WRITELOG" }
   ],
   "why": {
    "summary": "CPU is high; here are the top waits and queries. PAGEIOLATCH_SH holds 44% of the wait delta and Orders.Create insert is the top query.",
    "steps": [
     "Delta dm_os_wait_stats between the reference and incident windows.",
     "Rank the queries by total elapsed time in the incident window.",
     "CPU is high and one bounded wait type holds most of the delta, so the row is red."
    ],
    "dataNote": "Wait stats are sampled; per-query wait types need SQL Server 2017+ with Query Store wait stats."
   },
   "sampled": true
  }
 ],
 "mongo": [
>>>>>>> REPLACE

### parts/12-evidence-c.js
<<<<<<< SEARCH
  if (!c || !c.table || !c.table.cols || !c.table.cols.length || !c.table.rows || !c.table.rows.length) return '';
=======
  if (!c) return '';
  if (typeof evDepBlocked === 'function' && evDepBlocked(c) && typeof evDepBanner === 'function') { return evDepBanner(c); }
  if (!c.table || !c.table.cols || !c.table.cols.length || !c.table.rows || !c.table.rows.length) return '';
>>>>>>> REPLACE

### parts/12-evidence-c.js
<<<<<<< SEARCH
  html += '</tbody></table>';
  return html;
}
=======
  html += '</tbody></table>';
  if (typeof evQueryTable === 'function') { html += evQueryTable(c); }
  return html;
}
>>>>>>> REPLACE

### parts/12-evidence-h.js
<<<<<<< SEARCH
  return '<span class="ev-qa"><code>' + evEsc2(id) + '</code><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="' + evEsc2(id) + '">Copy query ID</button><span>Open the plan in SSMS or Azure Data Studio by this query ID (Query Store, Top Resource Consuming Queries). Unravis does not read plans.</span></span>';
=======
  return '<span class="ev-qa"><code>' + evEsc2(id) + '</code><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="' + evEsc2(id) + '">Copy query ID</button><span>Open the plan with showplan in SSMS or Azure Data Studio using this query ID.</span></span>';
>>>>>>> REPLACE

### parts/14-deps-data.js
<<<<<<< SEARCH
  'sql-tempdb-contention': ['viewstate'], 'sql-plan-regression': ['querystore'], 'sql-deadlocks': ['systemhealth'],
=======
  'sql-tempdb-contention': ['viewstate'], 'sql-plan-regression': ['querystore'], 'sql-deadlocks': ['systemhealth'],
  'sql-cpu-high': ['querystore', 'qswaits'],
>>>>>>> REPLACE

### parts/33-step7-evidence.js
<<<<<<< SEARCH
  incident: { label: 'Real incident', ids: ['sql-waits-bottleneck', 'sql-blocking-chain', 'sql-tempdb-contention', 'sql-lock-waits-rising', 'sql-waits-benign', 'sql-log-growth', 'sql-plan-regression'] },
=======
  incident: { label: 'Real incident', ids: ['sql-cpu-high', 'sql-waits-bottleneck', 'sql-blocking-chain', 'sql-tempdb-contention', 'sql-lock-waits-rising', 'sql-waits-benign', 'sql-log-growth', 'sql-plan-regression'] },
>>>>>>> REPLACE

## Check (you run this; the owner applies the blocks and runs the full check)
```
node --check parts/12-evidence-i.js
grep -c -F "'sql-cpu-high': ['querystore', 'qswaits']" parts/14-deps-data.js
grep -c -F "'sql-cpu-high'" parts/33-step7-evidence.js
grep -c -F "evQueryTable(c)" parts/12-evidence-c.js
```
Expected: no syntax error, then `1` three times.

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits in file order.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
