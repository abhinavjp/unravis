# Packet H3: `evChartCaption` in a new file `parts/12-evidence-f.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-f.js` containing exactly one global function `evChartCaption(c)` and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: return a string, no `document`/`window`. The global function `evAreaSeries(series)` exists elsewhere (you only CALL it): it returns an array of `{name, cls, vals:[numbers]}` where every `vals` has the same length; the total at column k is the sum of `vals[k]` over the array.

A card `c` has `chart` (`'line'|'stackedArea'|'bars'|'none'`) and `series` (array of `{name, win, pts:[{t,v}]}`).

Local helper `x(s)` = `String(s == null ? '' : s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`.

## Steps
1. If `c` is falsy or `c.chart` is not `'line'`, `'stackedArea'` or `'bars'`, return `''`.
2. `series` = `c.series` when `Object.prototype.toString.call(c.series) === '[object Array]'`, otherwise `[]`.
3. `unit` = `''`; for each entry of `series` (skip falsy ones) test its `name` as a string against `/\(([^()]*)\)\s*$/`; at the first match with a non-empty group 1 set `unit` to that group and stop.
4. `max` = 0. For `'stackedArea'`: `cols = evAreaSeries(series)`; `n` = `cols[0].vals.length` when `cols` is a non-empty array whose first entry has an array `vals`, otherwise 0; for each k below `n` add up `cols[j].vals[k]` over all j (only numbers that are not NaN) and keep the largest total in `max`. For `'line'` and `'bars'`: for every series entry that has an array `pts`, for every point that is an object with a number `v` (not NaN), keep the largest `v` in `max`.
5. Text `t` = `'Left part: the reference hour (a normal period). Right part: the incident hour. '` then, for `'line'`: `'The dashed line marks when the incident starts. '`; for `'stackedArea'`: `'The bands are stacked, so the top edge is the total. '`; for `'bars'`: `'Each bar is one reading; the highlighted bars are from the incident hour. '`.
6. When `max > 0` append `'The bottom is 0 and the top is about ' + (Math.round(max * 10) / 10) + (unit ? ' ' + unit : '') + '.'`.
7. Return `<p class="ev-cap">` + x(t.replace(/\s+$/, '')) + `</p>`. (The unit is escaped by x as part of t.)

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-f.js && node -e "const vm=require('vm'),fs=require('fs');const c={evAreaSeries:s=>[{name:'a',cls:'a',vals:[1,5,2]},{name:'b',cls:'c',vals:[1,1,9]}]};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-f.js','utf8')+';this.f=evChartCaption;',c);const L=c.f({chart:'line',series:[{name:'p95 (ms)',pts:[{t:0,v:40},null,{t:5,v:191.26},{t:6,v:'x'}]}]});const S=c.f({chart:'stackedArea',series:[{name:'q<',pts:[]}]});const B=c.f({chart:'bars',series:[{name:'n',pts:[{t:1,v:0}]}]});const ok=L==='<p class=\"ev-cap\">Left part: the reference hour (a normal period). Right part: the incident hour. The dashed line marks when the incident starts. The bottom is 0 and the top is about 191.3 ms.</p>'&&/top edge is the total\. The bottom is 0 and the top is about 10\.<\/p>$/.test(S)&&B.indexOf('highlighted bars')>0&&B.indexOf('top is about')<0&&c.f({chart:'none'})===''&&c.f(null)===''&&c.f({chart:'line'}).indexOf('<p class')===0&&!/NaN|undefined/.test(L+S+B);console.log(ok?'PASS':'FAIL '+L+S+B)"
```

## Output
The full file in one code block headed `### parts/12-evidence-f.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
