# Packet F2: fix the unit logic in `evChartCaption` (SEARCH/REPLACE on `parts/12-evidence-f.js`)

Do exactly this. Do not explore the repo, do not read other files except `buildout/wireframes/parts/12-evidence-f.js`. Output SEARCH/REPLACE blocks that rewrite steps 3 to 6 of the function. Keep ES5, keep `x`, keep the first sentences unchanged. First read the file, then output ONE block whose SEARCH is the whole text from the line `  var unit = '';` down to and including the line that appends the `The bottom is 0 and the top is about` sentence (copy it from the file exactly), and whose REPLACE is the new logic below. Heading: `### parts/12-evidence-f.js`.

## New logic (replaces the old unit and max-sentence code)
1. `unitOf(name)`: local function. `s = String(name == null ? '' : name)`. If `/\(([^()]*)\)\s*$/` matches and group 1 matches `/^[A-Za-z%\/ ]{1,10}$/` return that group trimmed. Else if `/\s%$/` matches `s` return `'%'`. Else return `''`.
2. `unit` = `''`, `same` = true. Loop over `series` (skip falsy entries): `u = unitOf(entry.name)`; on the first entry set `unit = u`; on later entries when `u !== unit` set `same = false`. When no entry exists, `same` = false.
3. `max` computation stays exactly as before.
4. Final sentence: when `max > 0` and `same` and `unit` is not `''`, append the old sentence `'The bottom is 0 and the top is about ' + (Math.round(max * 10) / 10) + ' ' + unit + '.'`. When `max > 0` and not (`same` and `unit` not empty), append `'The bottom is 0 and the heights are relative; read the exact numbers in the table.'`. When `max` is 0 or less append nothing.

The text `t` already ends with a space before this sentence is appended (as before). Do not change anything else in the file.

## Check (run from `buildout/wireframes` after the edit is applied; it must print `PASS`)
```
node --check parts/12-evidence-f.js && node -e "const vm=require('vm'),fs=require('fs');const c={evAreaSeries:s=>[{name:'a',cls:'a',vals:[1,5,2]},{name:'b',cls:'c',vals:[1,1,9]}]};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-f.js','utf8')+';this.f=evChartCaption;',c);const L=c.f({chart:'line',series:[{name:'p95 (ms)',pts:[{t:0,v:40},null,{t:5,v:191.26},{t:6,v:'x'}]}]});const S=c.f({chart:'stackedArea',series:[{name:'q (ms)',pts:[]}]});const M=c.f({chart:'line',series:[{name:'A (GB)',pts:[{t:0,v:5}]},{name:'B (ms)',pts:[{t:0,v:9}]}]});const U=c.f({chart:'line',series:[{name:'CPU used (user+sys)',pts:[{t:0,v:90}]}]});const P=c.f({chart:'line',series:[{name:'Log used %',pts:[{t:0,v:80}]}]});const B=c.f({chart:'bars',series:[{name:'n',pts:[{t:1,v:0}]}]});const ok=L.indexOf('The bottom is 0 and the top is about 191.3 ms.</p>')>0&&/top edge is the total\. The bottom is 0 and the top is about 10 ms\.<\/p>$/.test(S)&&/heights are relative; read the exact numbers in the table\.<\/p>$/.test(M)&&/heights are relative/.test(U)&&U.indexOf('user+sys')<0&&P.indexOf('top is about 80 %.')>0&&B.indexOf('highlighted bars')>0&&B.indexOf('top is about')<0&&B.indexOf('relative')<0&&c.f({chart:'none'})===''&&!/NaN|undefined/.test(L+S+M+U+P+B);console.log(ok?'PASS':'FAIL '+L+S+M+U+P+B)"
```

## Output
The SEARCH/REPLACE block, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
