# Packet E1a: `evAreaSeries` in a new file `parts/12-evidence-a.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-a.js` containing ONE global function and nothing else. ES5 only: `var`, `function`, `for` loops. No arrow functions, no template literals, no `let`, `const`, spread, `Array.from`.

## Function: `evAreaSeries(series)`
Input: array of `{name:string, win:'normal'|'incident', pts:[{t:number, v:number}]}` (t is 0..119).
Output: array of `{name, cls, vals:[number,...]}`.

Steps, in this order:
1. If `series` is not an array, return `[]`.
2. `names` = the distinct `name` values in order of first appearance.
3. For each name, `pts` = all points of every series with that name, normal-window series first, then incident ones, in their given order. Skip a point if `t` or `v` is not a number.
4. `cols` = the distinct `t` values over all names, sorted ascending (numeric sort).
5. If `cols.length` is 0, return `[]`.
6. For each name and each `cols[k]`: `vals[k]` is the `v` of the point with that exact `t`; if there is none, use the `v` of the nearest earlier point (largest `t` below `cols[k]`); if there is no earlier point, use 0.
7. `cls` by the name's position among `names`: position 0 is `'a'`, 1 is `'c'`, 2 is `'p'`, 3 and above is `'o'`.
8. Return the array in the order of `names`.

Define no other global; keep helper variables inside the function.

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/12-evidence-a.js && node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/12-evidence-a.js','utf8')+';this.f=evAreaSeries;',c);const a=c.f([{name:'A',win:'normal',pts:[{t:0,v:1},{t:30,v:2}]},{name:'A',win:'incident',pts:[{t:60,v:3},{t:119,v:4}]},{name:'B',win:'normal',pts:[{t:0,v:5}]}]);const ok=a.length===2&&a[0].cls==='a'&&a[1].cls==='c'&&a[0].vals.join()==='1,2,3,4'&&a[1].vals.join()==='5,5,5,5'&&c.f(undefined).length===0&&c.f([]).length===0;console.log(ok?'PASS':'FAIL '+JSON.stringify(a))"
```

## Output
The full file in one code block headed `### parts/12-evidence-a.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
