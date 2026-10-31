# Packet T0: automatic layout and chart check for the wireframe

Goal: create ONE new file, `check-guided.js`, in the folder `wireframes/` (next to `build-guided.js`). It is the pass/fail check used after every later wireframe packet. Do not edit any existing file.
Depends on: nothing.

## 1. Context (read-only facts; do not explore)
- `node build-guided.js` (run from `wireframes/`) builds `guided-analysis-layout.html` from `parts/*`. The page is one HTML file, opened with `file://`.
- Query parameters: `?variant=A|B|C` (layout), `?step=0..13` (opens that step), `?mode=guided|manual`, `?tool=<tool id>` (manual tool), `T` key toggles the theme. Default is variant A, guided, step 0.
- Layout facts: pane content lives in `.pane`; button strips are `<div class="row">` containing `<button>` elements; the step rail is `<nav class="rail">` with `<button class="row">` entries. Charts are inline `<svg>` elements; their lines are `<path>` elements with class names containing `line`. Page tab titles and step headings are not asserted.
- Playwright is installed in the repo at `tools/screens/node_modules` (from `buildout/wireframes` that is `../../tools/screens/node_modules/playwright`). Use `const { chromium } = require(require("path").join(__dirname, "../../tools/screens/node_modules/playwright"));` and launch headless Chromium. The check must work when run from `buildout/wireframes` as `node check-guided.js`.
- You may not be able to run Playwright in your sandbox. Write the file carefully; then run `node --check check-guided.js` (syntax only) and report whether it ran end-to-end.

## 2. Behaviour
Command line: `node check-guided.js [--slice 1|2|3|4]` (default 1). Steps: run `node build-guided.js` (stop with FAIL if it exits non-zero), then open the built file at each size and theme, run the assertions, print results.

- Viewports: `1440x900`, `1280x800`, `1024x768`. Themes: light and dark (use `page.emulateMedia({colorScheme})`, and also force with `localStorage` is NOT needed).
- Variants: `A` only for slice 1; `A`, `B`, `C` for slices 2 to 4.
- For every viewport x theme x variant, visit steps 0..13 (`?step=N`), and in manual mode visit the home and every tool the page lists (read tool ids from `window.TOOLS`, each item has `id`).
- Collect `pageerror` and `console` messages of type `error` for the whole run.

Assertions (all slices unless noted):
1. No `pageerror`, no console `error`.
2. Squashed-button check: every `button` inside a `.pane .row` element has a bounding box width of at least 60 px (if the button text is longer than 6 characters), and its text does not wrap into more than 2 lines (`height <= 2.6 * font size * line-height` is too fussy: use `height <= 64`).
3. Chart-draw check: for every `svg` inside `.pane` that contains at least one `path` whose class contains `line`, the number of such paths is at most 4 and at least 1, and every one has a bounding box width greater than 100 px. (Catches the bug where every minute becomes its own one-point path.)
4. No horizontal page scroll: `document.documentElement.scrollWidth <= window.innerWidth + 1`.
5. Slice 2 and up: assertions for Slice 2 to 4 are added later; for now the flag only controls the variants list above.

Output: one line per failure `FAIL <viewport> <theme> <variant> <step|tool> <assertion number>: <short detail>`, then a last line `RESULT PASS (<n> pages checked)` or `RESULT FAIL (<m> failures)`. Exit code 0 on pass, 1 on fail. Print nothing else. Cap at 40 failure lines (then `... and K more`).

## 3. Do not
Install anything, edit another file, add dependencies, take screenshots, or write files other than `check-guided.js`.

## 4. Checks
1. `node --check check-guided.js` prints nothing (valid syntax).
2. If you can run it: report the last output line.

## 5. Output
Return the full contents of `check-guided.js` in one code block, then a 3-line status: `Status: PASS|FAIL`, `Checks: <outputs>`, `Concerns: <none or one line>`.
