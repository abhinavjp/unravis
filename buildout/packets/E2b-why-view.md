# Packet E2b: `whyView`, `ruledOutDrawer` in a new file `parts/13-why-b.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/13-why-b.js` containing exactly two global functions and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: return strings, no `document`/`window`. These globals exist elsewhere and you only CALL them (never define them): `evChip(v)` returns a word, `evChart(c)` and `evTable(c)` return HTML strings.

A card `c` is `{id,label,verdict,rule,chart,series,table,why,sampled}`; `c.why` is `{summary:string, steps:[string], dataNote:string}`; `verdict` is `'ok'|'watch'|'bad'|'none'`.

Local helper in each function: `x(s)` = `String(s == null ? '' : s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`.

## 1. `whyView(c)`
1. If `c` is falsy, return `''`.
2. `v` = `c.verdict` when it is one of `ok`, `watch`, `bad`, `none`, otherwise `'none'`. `w` = `c.why` or `{}` when missing.
3. Return the concatenation of, in this order:
   - `<div class="why-view">`
   - `<button class="btn" type="button" onclick="evBack()">&larr; Back to the evidence</button>`
   - `<h3>` + x(c.label) + ` <span class="ev-chip ` + v + `">` + evChip(v) + `</span></h3>`
   - `<p class="ev-rule">` + x(c.rule) + `</p>`
   - `evChart(c)` then `evTable(c)`
   - `<p>` + x(w.summary) + `</p>`
   - `<ol class="why-steps">` + one `<li>` + x(step) + `</li>` per entry of `w.steps` (only when it is an array, otherwise nothing) + `</ol>`
   - when `w.dataNote` is non-empty: `<p class="note">` + x(w.dataNote) + `</p>`, otherwise nothing
   - `</div>`

## 2. `ruledOutDrawer(cards)`
1. If `cards` is not an array, return `''`.
2. `list` = the entries that are truthy and have `verdict === 'ok'`, in order. If `list` is empty, return `''`.
3. Return `<details class="ruled-out"><summary>Ruled out (` + list.length + `)</summary><ul>` + one `<li>` per card: x(label) + ` &mdash; ` + x(rule) + ` ` + `<button class="ev-why" type="button" onclick="evWhy('` + id + `')">Why this colour?</button></li>` + `</ul></details>`. Here `id` = `String(c.id == null ? '' : c.id)` with every character outside `A-Za-z0-9_-` removed.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/13-why-b.js && node -e "const vm=require('vm'),fs=require('fs');const c={evChip:v=>'CHIP'+v,evChart:()=>'<CH>',evTable:()=>'<TB>'};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/13-why-b.js','utf8')+';this.wv=whyView;this.ro=ruledOutDrawer;',c);const h=c.wv({id:'a',label:'L<',verdict:'watch',rule:'R',why:{summary:'S&',steps:['one','two<'],dataNote:'N'}});const e=c.wv({id:'a',label:'L',verdict:'ok',rule:'R'});const d=c.ro([null,{id:'a-1',label:'A',verdict:'ok',rule:'RA'},{id:'b',label:'B',verdict:'bad',rule:'RB'}]);const ok=h.indexOf('<div class=\"why-view\"><button class=\"btn\" type=\"button\" onclick=\"evBack()\">')===0&&/L&lt; <span class=\"ev-chip watch\">CHIPwatch/.test(h)&&h.indexOf('<CH><TB>')>0&&(h.match(/<li>/g)||[]).length===2&&/two&lt;/.test(h)&&/class=\"note\">N</.test(h)&&e.indexOf('class=\"note\"')<0&&e.indexOf('<ol class=\"why-steps\"></ol>')>0&&c.wv(null)===''&&d.indexOf('Ruled out (1)')>0&&d.indexOf(\"evWhy('a-1')\")>0&&d.indexOf('RB')<0&&c.ro([])===''&&c.ro(undefined)==='';console.log(ok?'PASS':'FAIL '+h)"
```

## Output
The full file in one code block headed `### parts/13-why-b.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
