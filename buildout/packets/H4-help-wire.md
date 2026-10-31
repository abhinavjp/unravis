# Packet H4: wire the help text, chart caption and a coloured Ruled out drawer (SEARCH/REPLACE)

Do exactly this. Do not explore the repo, do not read other files. Output exactly the SEARCH/REPLACE blocks below, copied character for character (each SEARCH must match the file exactly once; keep the indentation shown). `evHelpLook`, `evHelpMore`, `evChartCaption` are defined elsewhere (other packets); do not define them. Headings are paths relative to `buildout/wireframes`.

### parts/12-evidence-c.js
<<<<<<< SEARCH
  if (c.chart === 'line') return tlChart(c.series, [{t:60, label:'Incident starts'}]);
  if (c.chart === 'stackedArea') return stackedArea(evAreaSeries(c.series), false);
  if (c.chart === 'bars') return evBars(c.series);
=======
  if (c.chart === 'line') return tlChart(c.series, [{t:60, label:'Incident starts'}]) + evChartCaption(c);
  if (c.chart === 'stackedArea') return stackedArea(evAreaSeries(c.series), false) + evChartCaption(c);
  if (c.chart === 'bars') return evBars(c.series) + evChartCaption(c);
>>>>>>> REPLACE

### parts/12-evidence-d.js
<<<<<<< SEARCH
    '<p class="ev-rule">' + x(c.rule) + '</p>' +
    '<div class="ev-body">' + evChart(c) + evTable(c) + '</div>' +
=======
    '<p class="ev-rule">' + x(c.rule) + '</p>' +
    evHelpLook(c) +
    '<div class="ev-body">' + evChart(c) + evTable(c) + '</div>' +
    evHelpMore(c, false) +
>>>>>>> REPLACE

### parts/13-why-b.js
<<<<<<< SEARCH
  out += '<p class="ev-rule">' + x(c.rule) + '</p>';
  out += evChart(c);
  out += evTable(c);
=======
  out += '<p class="ev-rule">' + x(c.rule) + '</p>';
  out += evHelpLook(c);
  out += evChart(c);
  out += evTable(c);
  out += evHelpMore(c, true);
>>>>>>> REPLACE

### parts/13-why-b.js
<<<<<<< SEARCH
    out += '<li>' + x(card.label) + ' &mdash; ' + x(card.rule) + ' <button class="ev-why" type="button" onclick="evWhy(\'' + id + '\')">Why this colour?</button></li>';
=======
    out += '<li><span class="ev-chip ok">' + evChip('ok') + '</span><strong>' + x(card.label) + '</strong><span class="ev-rule">' + x(card.rule) + '</span>' + evHelpLook(card) + '<button class="ev-why" type="button" onclick="evWhy(\'' + id + '\')">Why this colour?</button></li>';
>>>>>>> REPLACE

## Check (run from `buildout/wireframes` after the edits; it must print `PASS`)
```
node --check parts/12-evidence-c.js && node --check parts/12-evidence-d.js && node --check parts/13-why-b.js && node -e "const vm=require('vm'),fs=require('fs');const c={tlChart:()=>'<T>',stackedArea:()=>'<S>',evAreaSeries:s=>s,evBars:()=>'<B>',evHelpLook:()=>'<LK>',evHelpMore:(k,o)=>'<HM'+o+'>',evChartCaption:()=>'<CAP>'};vm.createContext(c);vm.runInContext(['12-evidence-c.js','12-evidence-d.js','13-why-b.js'].map(f=>fs.readFileSync('parts/'+f,'utf8')).join('\n')+';this.ec=evidenceCard;this.wv=whyView;this.ro=ruledOutDrawer;',c);const card={id:'a',label:'L',verdict:'bad',rule:'R',chart:'line',series:[],why:{summary:'S',steps:['1']}};const e=c.ec(card),w=c.wv(card),d=c.ro([{id:'q',label:'Q',verdict:'ok',rule:'RQ'}]);const ok=e.indexOf('R</p><LK><div class=\"ev-body\"><T><CAP>')>0&&e.indexOf('</div><HMfalse><div class=\"ev-foot\">')>0&&w.indexOf('R</p><LK><T><CAP>')>0&&w.indexOf('<HMtrue>')>0&&d.indexOf('<li><span class=\"ev-chip ok\">Normal</span><strong>Q</strong><span class=\"ev-rule\">RQ</span><LK><button')>0;console.log(ok?'PASS':'FAIL '+e+w+d)"
```

## Output
Reply with the blocks above unchanged (so the reply can be applied by script), then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
