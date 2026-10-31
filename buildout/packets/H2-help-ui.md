# Packet H2: `evHelpLook`, `evHelpMore` in a new file `parts/12-evidence-e.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-e.js` containing exactly two global functions and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Pure: return strings, no `document`/`window`. The global object `EVHELP` may or may not exist; read it only as `(typeof EVHELP === 'object' && EVHELP)`; never define it.

A card `c` has a string `id`. `EVHELP[c.id]` is `{look,what,normal,issue,check,chart,terms}` where the first six are strings and `terms` is an array of `{t,d}`.

Local helper in each function: `x(s)` = `String(s == null ? '' : s)` with `&` -> `&amp;`, `<` -> `&lt;`, `>` -> `&gt;`, `"` -> `&quot;`. Local helper `get(c)` = the entry `EVHELP[c.id]` when `c` is truthy, `c.id != null`, EVHELP exists and the entry is an object; otherwise `null`.

## 1. `evHelpLook(c)`
`h = get(c)`. If `h` is null or `h.look` is not a non-empty string return `''`. Otherwise return `<p class="ev-look"><b>What to look for:</b> ` + x(h.look) + `</p>`.

## 2. `evHelpMore(c, open)`
`h = get(c)`; if null return `''`. Build `<details class="ev-help"` + (open === true ? ` open` : ``) + `><summary>Explain it simply</summary><dl>`. Then, in this order, for each pair `[title, key]` of `['What is this?','what']`, `['What normal looks like','normal']`, `['How to tell it is a real problem','issue']`, `['Check it yourself','check']`, `['How to read the chart','chart']`: when `h[key]` is a non-empty string append `<dt>` + title + `</dt><dd>` + x(h[key]) + `</dd>`. Then, when `h.terms` is an array with at least one entry that is an object having a non-empty `t`: append `<dt>Words used</dt><dd><ul class="ev-terms">` + for each such entry `<li><b>` + x(t) + `</b>: ` + x(d) + `</li>` + `</ul></dd>`. Finish with `</dl></details>`. If no `<dt>` was appended at all return `''`.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-e.js && node -e "const vm=require('vm'),fs=require('fs');const c={EVHELP:{a:{look:'L<',what:'W&',normal:'N',issue:'I',check:'C',chart:'H',terms:[{t:'T1',d:'D1'},null,{t:'',d:'x'}]},b:{look:'',what:'',terms:[]}}};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-e.js','utf8')+';this.l=evHelpLook;this.m=evHelpMore;this.k=Object.keys(this).join();',c);const a=c.m({id:'a'},false),o=c.m({id:'a'},true);const ok=c.l({id:'a'})==='<p class=\"ev-look\"><b>What to look for:</b> L&lt;</p>'&&c.l({id:'b'})===''&&c.l({id:'zz'})===''&&c.l(null)===''&&c.m({id:'b'},true)===''&&c.m(undefined)===''&&a.indexOf('<details class=\"ev-help\"><summary>Explain it simply</summary><dl><dt>What is this?</dt><dd>W&amp;</dd>')===0&&o.indexOf('<details class=\"ev-help\" open>')===0&&(a.match(/<dt>/g)||[]).length===6&&a.indexOf('<li><b>T1</b>: D1</li>')>0&&(a.match(/<li>/g)||[]).length===1&&/<\/ul><\/dd><\/dl><\/details>$/.test(a);console.log(ok?'PASS':'FAIL '+a)"
```

## Output
The full file in one code block headed `### parts/12-evidence-e.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
