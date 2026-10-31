# Packet E2a: `evWhy`, `evBack`, `evFind` in a new file `parts/13-why-a.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/13-why-a.js` containing exactly three global functions and nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). These globals exist elsewhere and you only USE them (never define them): the object `W` (app state), the function `render()`, and the object `SCEN` of the form `{sql:[card,...], mongo:[card,...], host:[card,...]}` where each card has a string `id`.

## 1. `evWhy(id)`
Sets `W.why = id;` then calls `render();`.

## 2. `evBack()`
Sets `W.why = null;` then calls `render();`.

## 3. `evFind(id)`
1. If `typeof SCEN === 'undefined'` or `SCEN` is falsy, return `null`.
2. For each key in this exact order: `'sql'`, `'mongo'`, `'host'`: let `list = SCEN[key]`; if it is an array, loop it in order and return the first card where `card && card.id === id`.
3. Return `null`.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/13-why-a.js && node -e "const vm=require('vm'),fs=require('fs');let n=0;const c={W:{why:null},render:()=>{n++},SCEN:{sql:[null,{id:'a',k:1}],mongo:[{id:'b'}],host:[{id:'a',k:2}]}};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/13-why-a.js','utf8')+';this.w=evWhy;this.b=evBack;this.f=evFind;',c);c.w('q');const s1=c.W.why==='q'&&n===1;c.b();const ok=s1&&c.W.why===null&&n===2&&c.f('a').k===1&&c.f('b').id==='b'&&c.f('zz')===null;vm.runInContext('SCEN=undefined',c);console.log(ok&&c.f('a')===null?'PASS':'FAIL')"
```

## Output
The full file in one code block headed `### parts/13-why-a.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
