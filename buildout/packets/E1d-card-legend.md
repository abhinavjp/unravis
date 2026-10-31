# Packet E1d: `evidenceLegend`, `evidenceCard` in a new file `parts/12-evidence-d.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-d.js` containing exactly two global functions and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: return strings, no `document`/`window`. These globals exist elsewhere and you only CALL them (never define them): `evChip(v)` returns a word, `evChart(c)` and `evTable(c)` return HTML strings.

A card `c` looks like `{id,label,verdict,rule,chart,series,table,why,sampled}`; `verdict` is `'ok'|'watch'|'bad'|'none'`; `sampled` is a boolean.

Local helper in each function that needs it: `x(s)` = `String(s == null ? '' : s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`.

## 1. `evidenceLegend()`
Returns exactly this string (no arguments):
`<p class="ev-legend"><span><i class="ok"></i>Looked normal</span><span><i class="watch"></i>Worth a look</span><span><i class="bad"></i>Likely cause</span><span><i class="none"></i>Not collected</span></p>`

## 2. `evidenceCard(c)`
1. If `c` is falsy, return `''`.
2. `v` = `c.verdict` when it is one of `ok`, `watch`, `bad`, `none`; otherwise `'none'`. `chip` = `evChip(v)`. `id` = `String(c.id == null ? '' : c.id)` with every character outside `A-Za-z0-9_-` removed.
3. Return the concatenation of, in this order:
   - `<section class="ev ` + v + `" aria-label="` + x(c.label) + `: ` + chip + `">`
   - `<div class="ev-head"><span class="ev-title">` + x(c.label) + `</span><span class="ev-chip ` + v + `">` + chip + `</span></div>`
   - `<p class="ev-rule">` + x(c.rule) + `</p>`
   - `<div class="ev-body">` + `evChart(c)` + `evTable(c)` + `</div>`
   - `<div class="ev-foot"><button class="ev-why" type="button" onclick="evWhy('` + id + `')">Why this colour?</button>` + (when `c.sampled` is true: `<span class="ev-sampled">Sampled data: short spikes can be missed.</span>`, otherwise nothing) + `</div>`
   - `</section>`

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-d.js && node -e "const vm=require('vm'),fs=require('fs');const c={evChip:v=>'CHIP'+v,evChart:()=>'<CH>',evTable:()=>'<TB>'};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-d.js','utf8')+';this.lg=evidenceLegend;this.cd=evidenceCard;',c);const h=c.cd({id:'a-1\\'x',label:'L<b>',verdict:'bad',rule:'R',sampled:true});const n=c.cd({id:'z',label:'Q',verdict:'weird',rule:'R'});const ok=c.lg().indexOf('Looked normal')>0&&/Not collected<\\/span><\\/p>$/.test(c.lg())&&h.indexOf('<section class=\"ev bad\" aria-label=\"L&lt;b&gt;: CHIPbad\">')===0&&h.indexOf(\"evWhy('a-1x')\")>0&&h.indexOf('ev-sampled')>0&&h.indexOf('<CH><TB>')>0&&h.slice(-10)==='</section>'&&n.indexOf('ev none')>0&&n.indexOf('ev-sampled')<0&&c.cd(null)==='';console.log(ok?'PASS':'FAIL '+h)"
```

## Output
The full file in one code block headed `### parts/12-evidence-d.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
