# Packet F1: null guards in `heatmap` and `swimlane`

Do exactly this. Do not explore the repo, do not read other files. Output exactly the four SEARCH/REPLACE blocks below, copied character for character (SEARCH text must match the file exactly once; keep the indentation shown). Then run the check.

### buildout/wireframes/parts/11-charts-b.js
<<<<<<< SEARCH
  var i, max = 0;
  for (i = 0; i < cells.length; i++) {
    var cv = cells[i].v;
=======
  cells = cells || [];
  var i, max = 0;
  for (i = 0; i < cells.length; i++) {
    var cv = cells[i] ? cells[i].v : null;
>>>>>>> REPLACE

### buildout/wireframes/parts/11-charts-b.js
<<<<<<< SEARCH
    if (typeof cc.r === 'number' && typeof cc.c === 'number') map[cc.r + ':' + cc.c] = cc;
=======
    if (cc && typeof cc.r === 'number' && typeof cc.c === 'number') map[cc.r + ':' + cc.c] = cc;
>>>>>>> REPLACE

### buildout/wireframes/parts/11-charts-c.js
<<<<<<< SEARCH
    var lane = list[i];
    var evs = lane.events || [];
=======
    var lane = list[i] || {};
    var evs = lane.events || [];
>>>>>>> REPLACE

### buildout/wireframes/parts/11-charts-c.js
<<<<<<< SEARCH
      var e = evs[j];
      var t = e.t, dur = e.dur || 0, end = t + dur;
=======
      var e = evs[j];
      if (!e || typeof e.t !== 'number') continue;
      var t = e.t, dur = e.dur || 0, end = t + dur;
>>>>>>> REPLACE

## Check (run from `buildout/wireframes` after the edits; it must print `PASS`)
```
node --check parts/11-charts-b.js && node --check parts/11-charts-c.js && node -e "const vm=require('vm'),fs=require('fs');const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/11-charts-b.js','utf8')+fs.readFileSync('parts/11-charts-c.js','utf8')+';this.h=heatmap;this.s=swimlane;',c);const a=c.h([null,{r:0,c:0,v:5}],['a'],['r']);const b=c.h(undefined,['a'],['r']);const d=c.s([null,{name:'N',events:[null,{label:'x'},{t:5,dur:0,label:'ok',sev:'ok'}]}],{from:0,to:119});console.log(/<rect/.test(a)&&/<rect/.test(b)&&/<circle/.test(d)&&!/NaN|undefined/.test(a+b+d)?'PASS':'FAIL')"
```

## Output
Reply with the four blocks above unchanged (so the reply can be applied by script), then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
