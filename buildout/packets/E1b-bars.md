# Packet E1b: `evBars` in a new file `parts/12-evidence-b.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-b.js` containing ONE global function `evBars(series)` and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: returns a string, no `document`/`window`.

Input `series`: array of `{name, win:'normal'|'incident', pts:[{t, v}]}` (t is 0..119, v a number). Output: an HTML string.

Steps:
1. Local helper `esc(s)`: `String(s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`.
2. If `series` is not an array, or it holds no point with numeric `t` and `v`, return exactly `<p class="note">Not enough data to draw this chart.</p>`.
3. Collect the bars: one per point with numeric `t` and `v`, in series order, each remembering `name`, `win`, `t`, `v`.
4. `W=640`, `H=140`, `PAD=4`. `top` = the largest `v` among the bars, but at least 1. Bar slot width `sw = (W - 2*PAD) / bars.length`; bar width `bw = max(3, sw * 0.7)`. Bar `i`: `x = PAD + i*sw + (sw - bw)/2`; height `h = max(0, v) / top * (H - 2*PAD)`; `y = H - PAD - h`. Use `.toFixed(1)` for every number written into the markup.
5. Each bar is `<rect x=".." y=".." width=".." height=".." rx="2" style="fill:VAR"><title>NAME, minute T: V</title></rect>` where VAR is `var(--accent)` when `win` is `'incident'`, else `var(--text-2)`. NAME and the numbers go through `esc`.
6. Caption `cap`: the distinct names joined by `' and '` (via `esc`).
7. Return exactly: `<figure class="ml"><figcaption>` + cap + `</figcaption><svg viewBox="0 0 640 140" role="img" aria-label="` + cap + `" preserveAspectRatio="none"><line x1="4" y1="136" x2="636" y2="136" class="axis"/>` + all rects + `</svg><p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident</p></figure>`.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-b.js && node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-b.js','utf8')+';this.f=evBars;',c);const h=c.f([{name:'A<x>',win:'normal',pts:[{t:0,v:5},{t:30,v:7}]},{name:'A<x>',win:'incident',pts:[{t:60,v:20}]}]);const z=c.f([{name:'Z',win:'normal',pts:[{t:0,v:0}]}]);const n=(h.match(/<rect/g)||[]).length;console.log(n===3&&/A&lt;x&gt;/.test(h)&&!/NaN|undefined/.test(h+z)&&/Not enough data/.test(c.f(undefined))&&/Not enough data/.test(c.f([]))?'PASS':'FAIL n='+n)"
```

## Output
The full file in one code block headed `### parts/12-evidence-b.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
