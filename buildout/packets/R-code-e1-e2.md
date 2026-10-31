# Review packet R-code-e1-e2: break the evidence card and Why view code

You are an independent reviewer. Do not edit files. Read only: `buildout/wireframes/parts/12-evidence-a.js`, `12-evidence-b.js`, `12-evidence-c.js`, `12-evidence-d.js`, `13-why-a.js`, `13-why-b.js`, and `buildout/wireframes/parts/01-css.html` (only the lines from `/* evidence cards (W2-0) */` to the end of that block). Requirements are the file comments plus this list.

## Check each item and answer with evidence (file:line, or the node output)
1. ES5 only (no `=>`, backticks, `let`, `const`, spread, `Array.from`), pure (no `document`, `window` use), and no globals other than: `evAreaSeries`, `evBars`, `evChip`, `evChart`, `evTable`, `evidenceLegend`, `evidenceCard`, `evWhy`, `evBack`, `evFind`, `whyView`, `ruledOutDrawer`. Load each file with `vm` and list `Object.keys(context)`.
2. Escaping: every card field that reaches HTML (`label`, `rule`, table cells, `why.summary`, `why.steps`, `why.dataNote`, series `name`) is escaped for `&`, `<`, `>` and `"`. The `id` used inside `onclick="evWhy('...')"` cannot break out of the attribute.
3. Try to break each function with: `undefined`, `null`, `{}`, a card with `series` that is not an array, `pts` containing `null` or strings, a negative `v`, `t` outside 0 to 119, a table whose rows are not arrays, a `why` with `steps` that is not an array, a label containing `"><img src=x onerror=alert(1)>`. Report any exception or any output containing `NaN`, `undefined`, `[object` or an unescaped `<img`.
4. `evAreaSeries`: same-name series merge, columns are the sorted union of `t`, missing values carry the earlier value, class order a, c, p, o.
5. `evBars`: one bar per point, no `NaN` for all-zero data, and a single point works.
6. CSS: list every class emitted by these files (for example `ev-head`, `ev-title`, `ev-chip`, `ev-rule`, `ev-body`, `ev-foot`, `ev-why`, `ev-sampled`, `ev-legend`, `why-view`, `why-steps`, `ruled-out`, `seg`, `btn`, `wf`, `note`, `legend`, `key`, `axis`, `ml`) that has no rule in `parts/01-css.html`, `parts/10-data.js` or `parts/20-shell.js` (a class that only needs the browser default is fine; say which).
7. Keyboard and screen reader: the card is a `section` with an `aria-label`; the Why control is a real `button`; the drawer is a native `details`. Say if anything needs a role or label that is missing.

## Output
A table `| # | pass/fail | evidence |` for items 1 to 5 and 7, a bullet list for item 6, then `Verdict: ACCEPT | FIX` and the list of fixes (file, line, one-line change). Be terse; the reply is capped near 16,000 output tokens.
