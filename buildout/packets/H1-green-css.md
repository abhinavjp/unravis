# Packet H1: a real green for "looked normal", and styles for the new help text

Do exactly this. Do not explore the repo, do not read other files. Output exactly the SEARCH/REPLACE blocks below, copied character for character (each SEARCH must match the file exactly once). Headings are paths relative to `buildout/wireframes`.

### parts/00-head.html
<<<<<<< SEARCH
--bad:#ff7b72; --ok:#3fd6b0;
=======
--bad:#ff7b72; --ok:#3fd6b0; --good:#4cc760;
>>>>>>> REPLACE

### parts/00-head.html
<<<<<<< SEARCH
--bad:#b02a3d; --ok:#0b6b57;
=======
--bad:#b02a3d; --ok:#0b6b57; --good:#1e7a34;
>>>>>>> REPLACE

### parts/01-css.html
<<<<<<< SEARCH
.ev.ok { border-left-color:var(--ok); }
=======
.ev.ok { border-left-color:var(--good); }
>>>>>>> REPLACE

### parts/01-css.html
<<<<<<< SEARCH
.ev-chip.ok { color:var(--ok); border-color:var(--ok); background:color-mix(in srgb,var(--ok) 12%,var(--surface)); }
=======
.ev-chip.ok { color:var(--good); border-color:var(--good); background:color-mix(in srgb,var(--good) 12%,var(--surface)); }
>>>>>>> REPLACE

### parts/01-css.html
<<<<<<< SEARCH
.ev-legend i.ok { background:var(--ok); }
=======
.ev-legend i.ok { background:var(--good); }
>>>>>>> REPLACE

### parts/01-css.html
<<<<<<< SEARCH
.ev-why:focus-visible, .ruled-out > summary:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }
=======
.ev-why:focus-visible, .ruled-out > summary:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }
.ev-look { margin:0; font-size:13px; } .ev-look b { color:var(--text); }
.ev-cap { margin:0; font-size:12px; color:var(--text-2); }
.ev-help { border-top:1px dashed var(--line); padding-top:6px; } .ev-help > summary { cursor:pointer; color:var(--accent); font-size:13px; }
.ev-help > summary:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }
.ev-help dl { margin:6px 0 0; display:grid; gap:6px; font-size:13px; } .ev-help dt { font-weight:600; } .ev-help dd { margin:0; color:var(--text-2); }
.ev-terms { margin:0; padding-left:18px; display:grid; gap:2px; } .ev-terms b { color:var(--text); }
.ruled-out ul { margin:8px 0 0; padding:0; list-style:none; display:grid; gap:10px; }
.ruled-out li { display:grid; gap:3px; justify-items:start; font-size:13px; padding-left:8px; border-left:3px solid var(--good); }
>>>>>>> REPLACE

## Check (run from `buildout/wireframes` after the edits; it must print `PASS`)
```
node -e "const fs=require('fs');const a=fs.readFileSync('parts/00-head.html','utf8'),b=fs.readFileSync('parts/01-css.html','utf8');const ok=/--good:#4cc760/.test(a)&&/--good:#1e7a34/.test(a)&&/\.ev\.ok \{ border-left-color:var\(--good\)/.test(b)&&/\.ev-chip\.ok \{ color:var\(--good\)/.test(b)&&/\.ev-legend i\.ok \{ background:var\(--good\)/.test(b)&&/\.ev-help dl/.test(b)&&/\.ruled-out li/.test(b);console.log(ok?'PASS':'FAIL')"
```

## Output
Reply with the blocks above unchanged (so the reply can be applied by script), then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
