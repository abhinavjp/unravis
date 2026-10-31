# Packet W2-1: CSS for the chart helpers (SEARCH/REPLACE into `parts/01-css.html`)

Goal: add one CSS block so the output of `tlChart`, `stackedArea`, `heatmap` and `swimlane` renders with colour. Edit only `buildout/wireframes/parts/01-css.html`. Add rules after the block that ends the `/* evidence cards (W2-0) */` section; do not change any existing rule.
Depends on: W2-0, C1, C2, C3 (all applied).

## 1. Context (facts)
- Use only these existing variables: `--ok --warn --bad --accent --text-2 --line --surface --surface-2 --r`. No new colours, no `.B`/`.C` variant rules.
- Classes emitted and still without a rule: `path.area` plus `s-a`, `s-c`, `s-p`, `s-o` (stacked area bands), `line.marker`, `text.mk-label`, `text.axis-label`, `rect.cell` (+ `l0`..`l4`; heatmap sets its fill inline, so only add `stroke:none`, `rx` is already an attribute), `rect.ev`, `circle.ev`, `rect.ev.in-brush`, `rect.brush`.
- Existing: `.axis` (stroke/axis text), `.line` (+ `.normal`, `.incident` in the minute chart), `figure.ml svg`. Check them with grep before writing; reuse, do not redefine.
- Stack band colours: `s-a` accent, `s-c` warn, `s-p` bad, `s-o` text-2; each `fill:` via `color-mix(in srgb, var(--X) 55%, var(--surface))`, `stroke:var(--X)`, `stroke-width:1`.
- Legend swatches: `.key.s-a/.s-c/.s-p/.s-o` use the same band colours as background (look at how `.key.normal` is styled and follow it).
- `.marker`: dashed stroke `var(--bad)`, `stroke-width:1`. `.mk-label`, `.axis-label`: `fill:var(--text-2); font-size:10px`.
- `.ev`: default `fill:var(--text-2)`; `.ev.in-brush` gets `stroke:var(--accent); stroke-width:1.5`; dimmed events outside the brush are not needed.
- `.brush`: no extra rule needed (inline style), skip it.

## 2. Checks (run from `buildout/wireframes`)
1. `node build-guided.js` exits 0 and `node check-guided.js --slice 1` ends with `RESULT PASS`.
2. `grep -o -- '--[a-z0-9-]*' ` over the new block lists only the variables above.
3. No selector in the new block contains `.B` or `.C`.

## 3. Output
SEARCH/REPLACE block(s) for `### buildout/wireframes/parts/01-css.html`, then 3 status lines: `Status`, `Checks`, `Concerns`. Under 40 added lines.
