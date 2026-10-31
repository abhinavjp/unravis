# Packet B3: styles for banners, plain-words line, term tooltips and fold, new file `parts/02-css-ev2.html`

Do exactly this. Do not read other files. Create ONE new file `buildout/wireframes/parts/02-css-ev2.html` that contains exactly one `<style>` ... `</style>` block (first characters `<style>`, last characters `</style>`), plain CSS, no `@import`, no hex colours (only `var(--token)`), no `.B` or `.C` rules, under 6000 characters. Available tokens: `--ok --warn --bad --good --accent --text --text-2 --line --surface --surface-2 --r`. Use compact one-line-per-rule style. Write every rule below.

1. `.ev-dep` : block, `margin:0`, `padding:10px 12px`, `border:1px solid var(--warn)`, `border-left-width:4px`, `border-radius:var(--r)`, `background:var(--surface-2)`, `display:grid`, `gap:6px`, `font-size:13px`.
2. `.ev-dep p` margin 0. `.ev-dep-title` colour `var(--text)`; `.ev-dep-why` and `.ev-dep-effect` colour `var(--text-2)`; `.ev-dep-note` colour `var(--text-2)`, `font-size:12px`.
3. `.ev-dep-fix` : `display:grid; gap:6px; justify-items:start`. `.ev-dep-fixlabel` : margin 0, `font-weight:600`, colour `var(--text)`.
4. `.ev-cmd` : margin 0, `max-width:100%`, `overflow:auto`, `padding:8px 10px`, `border:1px solid var(--line)`, `border-radius:var(--r)`, `background:var(--surface)`, `font:12px/1.5 ui-monospace,Consolas,monospace`, `white-space:pre`, colour `var(--text)`.
5. `.ev-copy` : `font:inherit; font-size:12px; padding:4px 10px; border:1px solid var(--accent); border-radius:var(--r); background:transparent; color:var(--accent); cursor:pointer`. `.ev-copy:hover` background `var(--surface)`. `.ev-copy:focus-visible` `outline:2px solid var(--accent); outline-offset:2px`.
6. `.ev-dep-pick` : `margin:0 0 8px; flex-wrap:wrap`. (it also has class `seg`; do not restyle `.seg`.)
7. `.ev-impact` : `margin:0; font-size:13px; color:var(--text-2)`; `.ev-impact b` colour `var(--text)`.
8. Tooltip: `.ev-tt` : `border-bottom:1px dotted var(--text-2); cursor:help; position:relative`. `.ev-tt[data-tip]:hover::after, .ev-tt[data-tip]:focus::after` : `content:attr(data-tip); position:absolute; left:0; bottom:calc(100% + 6px); z-index:20; width:max-content; max-width:min(280px,80vw); padding:6px 8px; border:1px solid var(--line); border-radius:var(--r); background:var(--surface); color:var(--text); font-size:12px; font-weight:400; line-height:1.4; white-space:normal; box-shadow:0 2px 8px rgba(0,0,0,.18)`. `.ev-tt:focus-visible` outline `2px solid var(--accent)`, `outline-offset:2px`.
9. `.ev-fold` : `border-top:1px dashed var(--line); padding-top:6px`. `.ev-fold > summary` : `cursor:pointer; color:var(--accent); font-size:13px`. `.ev-fold > summary:focus-visible` outline as above.
10. `.ev-qa` : `display:flex; flex-wrap:wrap; align-items:center; gap:8px; font-size:12px; color:var(--text-2)`. `.ev-qa code` : `font:12px ui-monospace,Consolas,monospace; color:var(--text)`.

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node ../packets/data/chk-b3.js
```

## Output
The full file in one code block headed `### parts/02-css-ev2.html`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
