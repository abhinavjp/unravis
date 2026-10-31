# Packet E1c: `evChip`, `evChart`, `evTable` in a new file `parts/12-evidence-c.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-c.js` containing exactly three global functions and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: return strings, no `document`/`window`. These globals exist elsewhere and you only CALL them (never define them): `tlChart(series, markers)`, `stackedArea(seriesArray, pct)`, `evAreaSeries(series)`, `evBars(series)`; each returns an HTML string.

A card `c` looks like `{id,label,verdict,rule,chart,series,table,why,sampled}`; `verdict` is `'ok'|'watch'|'bad'|'none'`; `chart` is `'line'|'stackedArea'|'bars'|'none'`; `table` is `{cols:[string], rows:[[string]]}`.

## 1. `evChip(v)`
Returns the word for a verdict: `'ok'` -> `Normal`, `'watch'` -> `Worth a look`, `'bad'` -> `Likely cause`, anything else -> `Not collected`.

## 2. `evChart(c)`
1. If `c` is falsy, return `''`.
2. `c.chart === 'line'` -> return `tlChart(c.series, [{t:60, label:'Incident starts'}])`.
3. `c.chart === 'stackedArea'` -> return `stackedArea(evAreaSeries(c.series), false)`.
4. `c.chart === 'bars'` -> return `evBars(c.series)`.
5. Anything else -> return exactly `<p class="note">This was not collected for this case, so there is no chart.</p>`.

## 3. `evTable(c)`
1. Local helper `x(s)`: `String(s == null ? '' : s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`.
2. If `c` is falsy, or `c.table` is falsy, or `c.table.cols` is not a non-empty array, or `c.table.rows` is not a non-empty array, return `''`.
3. Return `<table class="wf"><thead><tr>` + one `<th>` + x(col) + `</th>` per col + `</tr></thead><tbody>` + one `<tr>` per row, for at most the first 5 rows, each with one `<td>` + x(cell) + `</td>` per cell (a row that is not an array gives an empty `<tr></tr>`) + `</tbody></table>`.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-c.js && node -e "const vm=require('vm'),fs=require('fs');const c={tlChart:()=>'TL',stackedArea:(a,p)=>'SA'+(p===false?'F':'T'),evAreaSeries:s=>s,evBars:()=>'BR'};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-c.js','utf8')+';this.chip=evChip;this.ch=evChart;this.tb=evTable;',c);const t=c.tb({table:{cols:['A<'],rows:[['1'],['2'],['3'],['4'],['5'],['6']]}});const ok=c.chip('ok')==='Normal'&&c.chip('watch')==='Worth a look'&&c.chip('bad')==='Likely cause'&&c.chip('x')==='Not collected'&&c.ch({chart:'line'})==='TL'&&c.ch({chart:'stackedArea'})==='SAF'&&c.ch({chart:'bars'})==='BR'&&/not collected for this case/.test(c.ch({chart:'none'}))&&c.ch(null)===''&&(t.match(/<tr>/g)||[]).length===6&&/A&lt;/.test(t)&&c.tb({})===''&&c.tb(null)==='';console.log(ok?'PASS':'FAIL')"
```

## Output
The full file in one code block headed `### parts/12-evidence-c.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
