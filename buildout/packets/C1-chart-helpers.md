# Packet C1: two chart helpers in a new part `11-charts-a.js`

Goal: create `buildout/wireframes/parts/11-charts-a.js` with two global chart helper functions that later evidence packets call. Output the whole new file.
Depends on: nothing (W0 is already merged). Must not edit any existing file: `build-guided.js` already concatenates `parts/*` in filename order, so `11-` lands between `10-data.js` and `20-shell.js`.

## 1. Context (facts; do not explore beyond `parts/10-data.js` lines 185 to 215)
- `minuteLine(field,label,unit)` in `parts/10-data.js` is the style reference: ES5 only (`var`, `function`, no arrows, no template literals, no `let`/`const`), builds an SVG string, `viewBox` 640 x 140 (`W`, `H`, `PAD`=4), `preserveAspectRatio="none"`, an `<line class="axis">` baseline, `<path class="line normal|incident">` for series, wrapped in `<figure class="ml"><figcaption>label <span class="range">caption</span></figcaption><svg role="img" aria-label="label">...</svg><p class="legend">...</p></figure>`. CSS for `.line.normal`, `.line.incident`, `.axis`, `.key.normal`, `.key.incident` already exists; reuse those classes, add no CSS.
- Pure functions only: no DOM access at load time or in the functions; return strings. Escape text you put in markup with a local `esc(s)` helper (`&`, `<`, `>`, `"`).

## 2. Functions (exact signatures)
1. `tlChart(series, markers)`: a timeline line chart.
   - `series`: array of `{name, win, pts}` where `win` is `'normal'` or `'incident'` and `pts` is an array of `{t, v}` (`t` is a minute index 0 to 119 on one 120-minute axis, `v` a number or `null` for no data).
   - `markers`: optional array of `{t, label}` drawn as a vertical dashed `<line class="marker">` with a small `<text class="mk-label">` at the top.
   - Scale y to the highest `v` over all series (minimum 1). A `null` v breaks the line into runs (as `minuteLine` does). Every series becomes one `<path class="line normal|incident">` per run. If a series has fewer than 2 numeric points, draw no path for it.
   - Returns a `<figure class="ml">` like `minuteLine`; the figcaption is just `series[0].name` when one series, else the distinct names joined with ` and `. The legend paragraph lists `Normal` and `Incident` keys only for windows present.
2. `stackedArea(series, pct)`: a stacked area chart.
   - `series`: array of `{name, cls, vals}` where `cls` is a short class suffix (`a`, `c`, `p`, `o`) and `vals` is an array of numbers (same length for all, at least 2).
   - `pct`: when true, normalise every column to 100 %; otherwise stack raw values and scale to the highest column total.
   - One `<path class="area s-<cls>">` per series (closed polygon from the lower edge to the upper edge). Draw a baseline `<line class="axis">`. Returns `<figure class="ml">` with figcaption `Stacked <names joined by ", ">` and a legend paragraph of `<span class="key s-<cls>"></span> name` items.
   - Edge cases: all-zero columns draw nothing for that column (no NaN in any attribute); fewer than 2 columns returns `<p class="note">Not enough data to draw this chart.</p>`.
3. Do not define any other global. Keep helper vars inside the function bodies, not at top level, except the two function names.

## 3. Checks (run from `buildout/wireframes`)
1. `node build-guided.js` exits 0 and `node check-guided.js --slice 1` ends with `RESULT PASS`.
2. `node --check parts/11-charts-a.js` prints nothing.
3. This prints `PASS`: `node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/11-charts-a.js','utf8')+';this.t=tlChart;this.s=stackedArea;',c);const a=[];for(let i=0;i<60;i++)a.push({t:i,v:100+i});for(let i=60;i<120;i++)a.push({t:i,v:i===90?null:300});const h=c.t([{name:'P95',win:'normal',pts:a.slice(0,60)},{name:'P95',win:'incident',pts:a.slice(60)}],[{t:74,label:'Release'}]);const n=(h.match(/<path/g)||[]).length;const s=c.s([{name:'Audit',cls:'a',vals:[1,2,0]},{name:'Action',cls:'c',vals:[3,3,0]}],true);const bad=/NaN|undefined/.test(h+s);const e=c.s([{name:'x',cls:'a',vals:[1]}],false);console.log(n===3&&!bad&&/<path[^>]*class=.area s-a./.test(s)&&/Not enough data/.test(e)&&/class=.marker./.test(h)?'PASS':'FAIL n='+n+' bad='+bad)"`

## 4. Output
Return the full contents of `parts/11-charts-a.js` in one code block headed `### parts/11-charts-a.js`, then 3 status lines: `Status: PASS|FAIL`, `Checks: <outputs>`, `Concerns: <none or one line>`. Keep it under 150 lines. Write LF line endings; the applier is not used for a new file.
