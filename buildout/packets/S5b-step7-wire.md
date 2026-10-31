# Packet S5b: wire Step 7 to the evidence block (three SEARCH/REPLACE edits)

Do exactly this. Do not explore the repo, do not read other files. Output exactly the SEARCH/REPLACE blocks below, copied character for character (each SEARCH must match the file exactly once; keep the indentation shown). `step7Evidence()` is defined elsewhere (another packet); do not define it.

### parts/20-shell.js
<<<<<<< SEARCH
  backNote:false
};
=======
  backNote:false,
  why:null, evSet:'incident'
};
>>>>>>> REPLACE

### parts/31-steps-5-9.js
<<<<<<< SEARCH
  return statePrefix(7, st)
  + nutshell('Lock waits on the Orders tables rose at 09:14, the same minute the slow end moved. Blocking chains lasted up to 9 s.')
  + '<p class="lead" style="color:var(--text)">Checks on SQL Server: blocking, waits and capacity.</p>'
  + findingsList(['Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.', 'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.', 'No deadlocks were recorded in the window.', 'CPU on the SQL host is not in this data: it needs a Blackbox.'])
  + suspectsHere(['D1'])
=======
  if (W.why) return statePrefix(7, st) + step7Evidence();
  var inc = (W.evSet || 'incident') === 'incident';
  return statePrefix(7, st)
  + (inc ? nutshell('Lock waits on the Orders tables rose at 09:14, the same minute the slow end moved. Blocking chains lasted up to 9 s.') : '')
  + '<p class="lead" style="color:var(--text)">Checks on SQL Server: blocking, waits and capacity.</p>'
  + (inc ? findingsList(['Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.', 'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.', 'No deadlocks were recorded in the window.', 'CPU on the SQL host is not in this data: it needs a Blackbox.']) : '')
  + step7Evidence()
  + suspectsHere(['D1'])
>>>>>>> REPLACE

### parts/31-steps-5-9.js
<<<<<<< SEARCH
    + dt('Chart', 'None in the repo UI. (The SQL blocking and waits tool shows the same findings.)')
=======
    + dt('Chart', 'Evidence cards (new in this wireframe): each card has a chart, a small table, a colour and a Why this colour? view. Green means looked normal, amber worth a look, red likely cause, gray not collected. Green cards are kept in the Ruled out drawer. The pretend-case buttons are a wireframe aid, not a product control.')
>>>>>>> REPLACE

## Check (run from `buildout/wireframes` after the edits; it must print `PASS`)
```
node --check parts/20-shell.js && node --check parts/31-steps-5-9.js && node -e "const fs=require('fs');const a=fs.readFileSync('parts/20-shell.js','utf8'),b=fs.readFileSync('parts/31-steps-5-9.js','utf8');console.log(/why:null, evSet:'incident'/.test(a)&&/if \(W\.why\) return statePrefix\(7, st\) \+ step7Evidence\(\);/.test(b)&&/Evidence cards \(new in this wireframe\)/.test(b)&&b.split('step7Evidence()').length===3?'PASS':'FAIL')"
```

## Output
Reply with the three blocks above unchanged (so the reply can be applied by script), then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
