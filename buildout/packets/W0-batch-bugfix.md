# Packet W0 (batch of two): fix squashed buttons (RC1) and blank charts (RC2)

Goal: stop `.A .row` leaking onto pane button strips; make the reference and incident lines draw.
Target files: `buildout/wireframes/parts/01-css.html`, `buildout/wireframes/parts/10-data.js`. Nothing else.
Depends on: nothing. Revision 2: SEARCH/REPLACE output, verbatim anchors, exact check commands. Source parts are CRLF: apply with `node apply-packet.js <reply.md>` (matches either line ending).

## 1. Context (read-only facts; do not explore)
- Rail buttons are `<button class="row">` inside `<nav class="rail">` (parts/20-shell.js:109, 116). Pane button strips are also `class="row"` (generic rule parts/01-css.html:53, `display:flex`). The `.A .row` rule below turns every pane strip into a 22 px first column, which squashes "Add a change", "Change the window", "Add a source", "+ Add item", "Download report".
- `SERIES` interleaves reference and incident points, so `minuteLine` (do not edit it) starts a new run on every point (106 one-point paths today). The page draws one timeline: reference minutes 08:00-08:59 on the left, incident minutes 09:00-09:59 on the right (caption "26 Oct 08:00 to 26 Oct 09:59 UTC"), so x by array index is correct once the array is ordered reference first. Verified: `minuteLine` uses `x(i)` over `pts.length` and breaks runs on window change or `gap`.

## 2. Edits (exact string match; every SEARCH occurs exactly once in its file)

### parts/01-css.html
<<<<<<< SEARCH
.A .row { display:grid; grid-template-columns:22px 1fr auto; gap:8px; align-items:center; width:100%; text-align:left; padding:7px 8px; background:transparent; border:1px solid var(--line); border-radius:var(--r); }
.A .row:hover { border-color:var(--text-2); background:var(--surface-2); }
.A .row.cur { border-color:var(--accent); box-shadow:inset 2px 0 0 var(--accent); font-weight:600; }
.A .row .n { color:var(--text-2); font-variant-numeric:tabular-nums; }
=======
.A .rail .row { display:grid; grid-template-columns:22px 1fr auto; gap:8px; align-items:center; width:100%; text-align:left; padding:7px 8px; background:transparent; border:1px solid var(--line); border-radius:var(--r); }
.A .rail .row:hover { border-color:var(--text-2); background:var(--surface-2); }
.A .rail .row.cur { border-color:var(--accent); box-shadow:inset 2px 0 0 var(--accent); font-weight:600; }
.A .rail .row .n { color:var(--text-2); font-variant-numeric:tabular-nums; }
>>>>>>> REPLACE

### parts/10-data.js (edit 1 of 2)
<<<<<<< SEARCH
  var out = [];
  for (var m = 0; m < 60; m++) {
=======
  var out = [], inc = [];
  for (var m = 0; m < 60; m++) {
>>>>>>> REPLACE

### parts/10-data.js (edit 2 of 2)
<<<<<<< SEARCH
    out.push({ at:hm(9), win:'incident', p95:Math.round(incP), inFlight:Math.round(inf * 10) / 10, gap:gap });
  }
  return out;
=======
    inc.push({ at:hm(9), win:'incident', p95:Math.round(incP), inFlight:Math.round(inf * 10) / 10, gap:gap });
  }
  return out.concat(inc);
>>>>>>> REPLACE

(Reference points still go into `out` in the loop; incident points go into `inc`; the result is 60 reference points then 60 incident points. No other line changes.)

## 3. Do not touch
`minuteLine` and every other function; any CSS rule other than lines 101-104 of 01-css.html; `build-guided.js`; `.B` and `.C` rules (a later review checks them).

## 4. Checks (copy-paste, run from `buildout/wireframes`)
1. `node build-guided.js` exits 0.
2. `node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/10-data.js','utf8')+';this.S=SERIES;this.m=minuteLine;',c);const ok=c.S.length===120&&c.S.slice(0,60).every(p=>p.win==='reference')&&c.S.slice(60).every(p=>p.win==='incident');const n=(c.m('p95','P95','ms').match(/<path/g)||[]).length;console.log(ok&&n===3?'PASS':'FAIL len='+c.S.length+' paths='+n)"` prints `PASS` (before the fix it printed 120 points and 106 paths).
3. `node -e "const t=require('fs').readFileSync('parts/01-css.html','utf8');const bad=t.split('\n').filter(l=>/^\.A \.row/.test(l));console.log(bad.length===0?'PASS':'FAIL '+bad.length)"` prints `PASS`.

## 5. Output
Return only the SEARCH/REPLACE blocks you applied and a 3-line status: `Status: PASS|FAIL`, `Checks: <the three outputs>`, `Concerns: <none or one line>`.
