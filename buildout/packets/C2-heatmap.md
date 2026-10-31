# Packet C2: heatmap chart helper in a new part `11-charts-b.js`

Goal: create `buildout/wireframes/parts/11-charts-b.js` with one global helper `heatmap(cells, cols, rows)` used by the host-resource heatmap and the recurrence day-by-hour calendar. Output the whole new file.
Depends on: nothing. Must not edit any existing file: `build-guided.js` concatenates `parts/*` in filename order, so `11-charts-b.js` lands after `11-charts-a.js` and before `20-shell.js`.

## 1. Context (facts; do not explore beyond `parts/10-data.js` lines 185 to 215)
- Style reference is `minuteLine` in `parts/10-data.js`: ES5 only (`var`, `function`; no arrows, template literals, `let` or `const`), returns an SVG string inside `<figure class="ml"><figcaption>label</figcaption><svg role="img" aria-label="..." viewBox preserveAspectRatio="none">...</svg><p class="legend">...</p></figure>`. Existing CSS classes `.axis`, `.key`, `.note`, `figure.ml` exist. Pure function, returns a string, no DOM access.
- The new CSS for cell colours does not exist yet. Use the existing colour variables through inline `style="fill:var(--ok)"` etc. only on these classes of cell: pick the level by value: `l0` (no data, `var(--surface-2)`), `l1` low, `l2`, `l3`, `l4` high (use `color-mix(in srgb, var(--bad) 20%, var(--surface))`, 40, 65, 90 percent as inline `fill` style on each `<rect>`). Add the class name (`cell l1` ...) too, so a later CSS packet can restyle. Define no new colour values.
- Escape text you put in markup with a local `esc(s)` helper (`&`, `<`, `>`, `"`).

## 2. Function (exact signature)
`heatmap(cells, cols, rows)`:
- `cols`: array of column labels (strings, at most 24), `rows`: array of row labels (strings, at most 31).
- `cells`: array of `{r, c, v, tip}` where `r` is the row index, `c` the column index, `v` a number or `null` (no data), `tip` optional text. Missing cells count as no data.
- Scale: the highest `v` over all cells is level 4; levels split the range 0..max into four equal bands (value 0 is `l1`; `null` is `l0`). If every `v` is null or 0, all cells are `l0` or `l1` with no NaN anywhere.
- Each cell is `<rect class="cell lN" x y width height rx="2" style="fill:...">` with a child `<title>` containing `rows[r] + ", " + cols[c] + ": " + (v==null ? "no data" : v) + (tip ? " (" + esc(tip) + ")" : "")`.
- viewBox width 640; label gutters: row labels in `<text class="axis-label">` at the left (width 56), column labels at the top (height 18; show every nth label so at most 12 show); height grows with the rows (cell height 16, minimum 80).
- Legend paragraph: five items `<span class="key"></span>` pairs: `No data`, `Low`, `...`, `High` (text only; no new classes).
- Returns `<p class="note">Not enough data to draw this chart.</p>` when `cols` or `rows` is empty.
- Define no other global. Keep helper vars inside the function body.

## 3. Checks (run from `buildout/wireframes`)
1. `node build-guided.js` exits 0 and `node check-guided.js --slice 1` ends with `RESULT PASS`.
2. `node --check parts/11-charts-b.js` prints nothing.
3. This prints `PASS`: `node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/11-charts-b.js','utf8')+';this.h=heatmap;',c);const cells=[];for(let r=0;r<7;r++)for(let k=0;k<24;k++)cells.push({r:r,c:k,v:k===3?null:r*k});const h=c.h(cells,Array.from({length:24},(_,i)=>String(i)),['Mon','Tue','Wed','Thu','Fri','Sat','Sun']);const e=c.h([],[],[]);const z=c.h([{r:0,c:0,v:0}],['a'],['b']);const n=(h.match(/<rect/g)||[]).length;console.log(n===168&&!/NaN|undefined/.test(h+z)&&/Not enough data/.test(e)&&/no data/.test(h)?'PASS':'FAIL n='+n)"`

## 4. Output
Return the full contents of `parts/11-charts-b.js` in one code block headed `### parts/11-charts-b.js`, then 3 status lines: `Status: PASS|FAIL`, `Checks: <outputs>`, `Concerns: <none or one line>`. Under 120 lines. LF line endings.
