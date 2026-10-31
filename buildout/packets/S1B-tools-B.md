# Packet S1B: wireframe-only fixes for calls-in-flight, nearby-activity and changes in `parts/42-tools-b.js`

Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Globals that exist elsewhere and you only USE (always inside functions, never at load time, guarded by `typeof`): `W`, `render()`, `nearView()` (swim-lane chart with axis and brush, defined in `parts/10-data3-near.js`). New global (only this one): `TBWindowMs`.

## Background (from `buildout/specs/2026-10-29-manual-tools-audit.md`, Group B rows)
- `calls-in-flight`: missing "pool limit context when no limit entered".
- `nearby-activity`: missing "swim-lane timeline with axis and brush"; its list is thin without a chart. The repo already has that exact chart as `nearView()` (used by Step 4); this wires it into the tool too.
- `changes`: missing "long-window behaviour" (its optional-release-vs-not-ticked distinction is already present in the file; nothing to do there).
- `sql-waits` is left exactly where it is and unedited (needs new collectors, out of scope for this packet).
Tools are not moved between files.

## New file `parts/10-data5-toolsB.js`
One function, one global: `TBWindowMs(s)`. Match `s` against `/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/`; if it does not match, return `NaN`; otherwise return `Date.UTC(` year, month minus 1, day, hour, minute `)` taken from the five captured groups (each passed through `Number(...)`).

## Edits to `parts/42-tools-b.js` (verify each SEARCH occurs exactly once with `grep -c -F`)
1. In `TOOL_BODIES['calls-in-flight']`, replace the fixed pool-limit sentence with one that reads the actual `W.frame.poolLimit` and falls back to a no-limit sentence when it is empty or blank.
2. In `TOOL_BODIES['nearby-activity']`, append the existing swim-lane chart from `nearView()` after the existing panel, guarded by `typeof`.
3. In `TOOL_BODIES['changes']`, after the existing "Only changes you listed..." note, append a note for windows longer than a day, guarded by `typeof TBWindowMs`.

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/10-data5-toolsB.js && node --check parts/42-tools-b.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/42-tools-b.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits, in file order.

### parts/10-data5-toolsB.js
```
// ---------- toolsB: shared helper for the calls-in-flight, nearby-activity and changes fixes (S1B) ----------
function TBWindowMs(s) {
  var m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(String(s));
  if (!m) return NaN;
  return Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
}
```

### parts/42-tools-b.js
<<<<<<< SEARCH
    + '<p class="note">Calls in flight peaked at 34 of the 100 entered as the pool limit. The pool was not filled.</p>'
=======
    + (W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class="note">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>' : '<p class="note">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>')
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
  return nearbyPanel();
=======
  return nearbyPanel() + (typeof nearView === 'function' ? nearView() : '');
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
    + '<p class="note">Only changes you listed are compared. Unlisted changes are invisible to this step.</p>';
=======
    + '<p class="note">Only changes you listed are compared. Unlisted changes are invisible to this step.</p>'
    + (typeof TBWindowMs === 'function' && (TBWindowMs(W.frame.incidentEnd) - TBWindowMs(W.frame.incidentStart)) > 86400000 ? '<p class="note">This window spans more than a day. A change from early in a long window is easy to miss if it is not already in the list above.</p>' : '');
>>>>>>> REPLACE

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
