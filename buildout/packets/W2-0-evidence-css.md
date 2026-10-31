# Packet W2-0: shared CSS for evidence cards, the colour legend and chips

Goal: add the CSS that later evidence-card packets (E1, E2, S5) will use. CSS only; no JS. Layout variant A is frozen: add no `.B` or `.C` rules.
Target file (only): `buildout/wireframes/parts/01-css.html` (source files are CRLF; you emit blocks, the applier handles line endings: `node apply-packet.js <reply.md>`).
Depends on: nothing.

## 1. Context (facts; do not explore)
- Colour tokens already exist as CSS variables in this file: `--ok`, `--warn`, `--bad`, `--accent`, `--text`, `--text-2`, `--line`, `--surface`, `--surface-2`, `--r`. Use ONLY these; define no new colours. `color-mix(in srgb, var(--x) N%, var(--surface))` is already used for tinted backgrounds (see `.problems`).
- Gray means "not collected yet": use `--text-2` for its text and border and `--surface-2` for its background.
- Existing classes to reuse and not to redefine: `.nutshell`, `.picks`, `.row`, `.btn`, `.btn.sm`, `.note`, `.wf`, `figure.ml`, `.legend` (a chart caption paragraph; do NOT reuse that name), `.key`.
- Where to insert: immediately after the last rule of the `.nearby` group (line starting `.nearby {`), as one new block.

## 2. Add these rules (new class names only; names are fixed because later packets use them)
- `.ev` card: border 1px `--line`, border-left 4px (colour set by the verdict class), radius `--r`, background `--surface`, padding 12px 14px, `display:grid; gap:8px; max-width:900px`.
- Verdict classes on `.ev`: `.ev.ok` left border `--ok`; `.ev.watch` `--warn`; `.ev.bad` `--bad`; `.ev.none` `--text-2` with background `--surface-2` and `.ev.none .ev-body` at `opacity:.8`.
- `.ev-head`: flex row, wrap, `gap:8px`, `align-items:center`; `.ev-title` weight 600; `.ev-chip`: inline-block, padding 1px 8px, radius 999px, font-size 12px, weight 600, border 1px solid; colour per verdict (`.ev-chip.ok/.watch/.bad/.none`, text and border from the same token, background `color-mix(... 12%, var(--surface))`).
- `.ev-rule`: `color:var(--text-2); font-size:13px; margin:0`. `.ev-body`: `display:grid; gap:8px`. `.ev-foot`: flex row with `justify-content:space-between; align-items:center; gap:8px`. `.ev-why`: a link-styled button (no border, background none, color `--accent`, underline on hover, cursor pointer, padding 0). `.ev-sampled`: `font-size:12px; color:var(--text-2)`.
- `.ev-legend`: flex row, wrap, `gap:12px`, font-size 13px; `.ev-legend i`: 10px square, radius 2px, inline-block, margin-right 6px, `vertical-align:baseline`; `.ev-legend i.ok/.watch/.bad/.none` backgrounds `--ok/--warn/--bad/--text-2`.
- `.why-view`: `display:grid; gap:12px; max-width:900px`; `.why-view h3` margin 0; `.why-steps` ordered list, padding-left 20px, `gap:4px`.
- `.ruled-out`: a `details` element style: border 1px dashed `--line`, radius `--r`, padding 8px 12px; `.ruled-out > summary` cursor pointer, color `--text-2`.
- Focus: `.ev-why:focus-visible, .ruled-out > summary:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }`.
- Small screens: `@media (max-width:700px){ .ev { padding:10px; } .ev-foot { flex-direction:column; align-items:flex-start; } }`.
Keep the file's style: one rule per line where it does now, no comments except one line `/* evidence cards (W2-0) */` above the block.

## 3. Do not touch
Any existing rule, any other file, `.B`/`.C` rules.

## 4. Checks (run from `buildout/wireframes` after applying)
1. `node build-guided.js` exits 0.
2. `node -e "const t=require('fs').readFileSync('guided-analysis-layout.html','utf8');const need=['.ev {','.ev.ok','.ev.watch','.ev.bad','.ev.none','.ev-chip','.ev-legend','.why-view','.ruled-out','.ev-why:focus-visible'];const miss=need.filter(s=>!t.includes(s));console.log(miss.length?'FAIL '+miss.join(','):'PASS')"` prints `PASS`.
3. `node check-guided.js --slice 1` ends with `RESULT PASS`.

## 5. Output
Return only the SEARCH/REPLACE block(s) in this format, then 3 status lines (`Status: PASS|FAIL`, `Checks: <outputs>`, `Concerns: <none or one line>`). The SEARCH text must be copied from the file as it is now and occur exactly once.
```
### parts/01-css.html
<<<<<<< SEARCH
old text
=======
new text
>>>>>>> REPLACE
```
