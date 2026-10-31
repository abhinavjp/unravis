# Packet C3: swim-lane chart helper in a new part `11-charts-c.js`

Goal: create `buildout/wireframes/parts/11-charts-c.js` with one global helper `swimlane(lanes, window, brush)` for the Nearby activity view (API, SQL, Mongo, Host lanes with a time brush). Output the whole new file.
Depends on: nothing. Must not edit any existing file: `build-guided.js` concatenates `parts/*` in filename order, so `11-charts-c.js` lands after `11-charts-b.js` and before `20-shell.js`.

## 1. Context (facts; do not explore beyond `parts/10-data.js` lines 185 to 215)
- Style reference is `minuteLine` in `parts/10-data.js`: ES5 only (`var`, `function`; no arrows, template literals, `let`, `const`), pure function returning an SVG string inside `<figure class="ml"><figcaption>...</figcaption><svg role="img" aria-label="..." viewBox preserveAspectRatio="none">...</svg><p class="legend">...</p></figure>`. Existing classes: `.axis`, `.line`, `.key`, `.note`, `.marker`, `.mk-label`. Escape text with a local `esc(s)` helper (`&`, `<`, `>`, `"`). Define no new colour values; use `class` names plus inline `style="fill:var(--accent)"` or `var(--bad)` / `var(--warn)` / `var(--text-2)` only.
- Time axis: minute 0 to 119 (the 60 reference minutes then the 60 incident minutes), same as `minuteLine`.

## 2. Function (exact signature)
`swimlane(lanes, window, brush)`:
- `lanes`: array of `{name, events:[{t, dur, label, sev}]}` where `t` is the start minute, `dur` is the length in minutes (0 means a point event), `label` text, `sev` is `'ok'`, `'warn'`, `'bad'` or omitted (omitted uses `var(--text-2)`).
- `window`: `{from, to}` minute range to show (default 0 to 119 when missing). Events outside the range are not drawn.
- `brush`: optional `{from, to}` minute range drawn as a translucent band across all lanes (`<rect class="brush" ...>`, inline `fill:var(--accent)`, `fill-opacity:.15`). Events whose span overlaps the brush get `class="ev in-brush"`, others `class="ev"`.
- Layout: viewBox width 640, lane height 28, gutter 80 on the left for lane names (`<text class="axis-label">`), a top axis with a tick every 15 minutes (labels `+0`, `+15`... relative to `window.from`); one `<line class="axis">` under each lane. Events with `dur > 0` are `<rect rx="2">` at least 3 units wide; point events are `<circle r="4">`. Each event has a `<title>` child with `name + ": " + label + " (minute " + t + (dur ? " for " + dur + " min" : "") + ")"`.
- A legend paragraph with three items `ok`, `warn`, `bad` and a text line `Shaded band is the selected time slice` when a brush is given.
- Returns `<p class="note">No activity to show.</p>` when `lanes` is empty or has no events in range.
- No NaN or undefined in any attribute (guard division by zero when `window.to <= window.from`: treat as 0 to 119).
- Define no other global. Keep helper vars inside the function body.

## 3. Checks (run from `buildout/wireframes`)
1. `node build-guided.js` exits 0 and `node check-guided.js --slice 1` ends with `RESULT PASS`.
2. `node --check parts/11-charts-c.js` prints nothing.
3. This prints `PASS`: `node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/11-charts-c.js','utf8')+';this.s=swimlane;',c);const L=[{name:'API',events:[{t:70,dur:10,label:'slow calls',sev:'bad'},{t:10,dur:0,label:'x'}]},{name:'SQL',events:[{t:72,dur:5,label:'blocking',sev:'warn'}]},{name:'Host',events:[]}];const h=c.s(L,{from:60,to:119},{from:70,to:80});const e=c.s([],null,null);const w=c.s(L,{from:5,to:5},null);const rects=(h.match(/<rect class=.ev/g)||[]).length;console.log(rects===2&&/in-brush/.test(h)&&/class=.brush./.test(h)&&!/NaN|undefined/.test(h+w)&&/No activity/.test(e)?'PASS':'FAIL r='+rects)"`

## 4. Output
Return the full contents of `parts/11-charts-c.js` in one code block headed `### parts/11-charts-c.js`, then 3 status lines: `Status: PASS|FAIL`, `Checks: <outputs>`, `Concerns: <none or one line>`. Under 130 lines. LF line endings.
