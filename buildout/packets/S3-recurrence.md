# Packet S3: recurrence selector, summary, calendar heatmap and overlay in a new file `parts/30-recurrence.js`

Do exactly this. Do not explore the repo. Create the new files below and make the 2 edits. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Globals that exist elsewhere and you only USE (always inside functions, never at load time): `W`, `render()`, `fpMs(s)` (UTC ms from `YYYY-MM-DDTHH:mm`, NaN if bad), `fpDate(ms)` (`1 Nov 2026`), `fpPad(n)` (two digits), `heatmap(cells, cols, rows)` (returns `<figure class="ml">…</figure>` with an `<svg>` whose cells are `<rect class="cell lN" …>` in row-major order: index = row * cols.length + col; `cells` = array of `{r, c, v, tip}`; `v` a number or null).
New globals (only these): `recurInit`, `recurSet`, `recurMode`, `recurOcc`, `recurSummary`, `recurHeat`, `recurOverlay`, `frameRecurrence`, `RECUR_WD`.

## State (lazy, never at load)
`recurInit()`: if `W.recur` is not an object set `W.recur = {mode:'one', st:'09:00', en:'10:00', n:6, wd:4}`; return `W.recur`. `mode` is one of `one`, `days`, `weeks`, `month`; `wd` is a UTC weekday number 0 (Sunday) to 6. `var RECUR_WD = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'];`
`recurSet(key, val)`: `r = recurInit()`; if `key === 'n'` then `r.n = parseInt(val, 10)` (NaN becomes 1), else if `key === 'wd'` then `r.wd = parseInt(val, 10)` (NaN becomes 4) else `r[key] = String(val)`; then `render();`.
`recurMode(m)`: `recurInit().mode = m; render();`.
Limits per mode (clamp `n` when used, in `recurOcc`, do not mutate): `days` 1 to 30, `weeks` 1 to 26, `month` 1 to 24. Unit word: `day`/`days`, `week`/`weeks`, `month`/`months` (singular when n is 1).

## `recurOcc()`
Returns `{ok:false, msg}` or `{ok:true, list:[ms,...], n, unit, mode, st, en}` (`mode`, `st`, `en` copied from the state). Parse `r.st` and `r.en` as `HH:MM` (regex `^([01]\d|2[0-3]):[0-5]\d$`); if either is invalid return `{ok:false, msg:'Enter the start and end as HH:MM.'}`; if end is not later than start (compare as minutes) return `{ok:false, msg:'The end time must be after the start time.'}`. `a = fpMs(W.frame.incidentStart)`; if NaN return `{ok:false, msg:'Set the incident window first.'}`. `day0 = Math.floor(a / 86400000) * 86400000` (UTC midnight of the incident day). `list` holds UTC midnights of each occurrence day, most recent first:
- `days`: `day0 - k * 86400000` for k = 1..n.
- `weeks`: first = the latest UTC day before `day0` whose weekday is `r.wd` (step back day by day from `day0 - 86400000`), then `first - (k - 1) * 7 * 86400000` for k = 1..n.
- `month`: for k = 1..n, take the month that is k months before the incident's month (use `Date.UTC(y, m - k, 1)` to normalise) and the LAST Thursday (weekday 4) of that month: last = `Date.UTC(y, m + 1, 0)` day-of-month `dl` and weekday `wl`; day = `dl - ((wl - 4 + 7) % 7)`; midnight = `Date.UTC(y, m, day)`.
Return `n` as the clamped count.

## `recurSummary(o)` (`o` from `recurOcc`, ok)
Exact text `'<p class="note" role="status">Comparing ' + st + '&ndash;' + en + ' UTC on ' + DESC + ', for ' + n + ' ' + unit + ' (' + fpDate(oldest) + ' to ' + fpDate(newest) + ').</p>'` where `oldest`/`newest` are the earliest/latest in `list`, and DESC is: `days` -> `each of the previous days`; `weeks` -> `the ` + weekday name lowercase... precisely `'the same weekday (' + RECUR_WD[wd] + ') on previous weeks'`; `month` -> `the last Thursday of the month`.

## `recurHeat(o)`
Rows = `['Mon','Tue','Wed','Thu','Fri','Sat','Sun']`, cols = `['00','01',…,'23']`. Weekday of a row r (0 = Mon) is `(r + 1) % 7` in UTC numbering. History value for row r, hour c (deterministic): `v = 80 + (c >= 8 && c <= 18 ? 120 : 0) + r * 6 + ((r * 31 + c * 17) % 40)`; if `(r === 3 && c === 9)` add 260; cells with `r >= 5 && (c < 6)` get `v: null` (no data). Build `cells` for all 168 buckets with `tip: 'p95 latency, ms'`. Call `heatmap(cells, cols, rows)`; then mark the matching hours: `sh = parseInt(st.slice(0,2),10)`, `eh = ` hour of `en` (if `en` minutes > 0 the end hour is included, else excluded: `lastH = en minutes === 0 ? eh - 1 : eh`). Matching rows: `days` -> all 7 rows; `weeks` and `month` -> only the row whose weekday equals `wd` (`month` uses weekday 4). For every matching `(r, c)` with `sh <= c <= lastH`, replace the `(r * 24 + c + 1)`-th occurrence (1-based) of the text `<rect class="cell ` in the heatmap string with `<rect class="cell hit' + (o.mode === 'month' ? ' dash' : '') + ' ` — i.e. the rect then reads `class="cell hit lN"` or `class="cell hit dash lN"`. Implement by `parts = html.split('<rect class="cell ')` and re-joining with the replacement for the chosen indices (part index i is the i-th rect). After the heatmap append, in this order: `'<p class="note">Darker amber = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.</p>'` and `'<p class="note">' + (mode === 'month' ? 'Dashed border: the hours the rule matches, on only one such weekday a month (the last).' : 'Outlined cells: the hours the rule matches.') + '</p>'`.

## `recurOverlay(o)`
Shows at most the 8 most recent occurrences (`list.slice(0, 8)`). Window length in minutes `L` = end minutes minus start minutes. Each occurrence k (0 = most recent) draws a polyline of 13 points (x from 0 to 600, evenly spaced, `y = 110 - ((k * 37 + i * 17) % 23) * 3 - (k === 0 ? 14 : 0)` for i = 0..12) inside `<svg role="img" aria-label="Overlay of ' + shown + ' occurrences, aligned to the window start" viewBox="0 0 640 140" class="recur-ov">`; the most recent line has `class="recur-line now"`, others `class="recur-line"`. Add axis text: `<text class="axis-label" x="0" y="136">0</text>` and `<text class="axis-label" x="600" y="136" text-anchor="end">' + L + ' min</text>`. After the svg: `<p class="note">One line per occurrence, aligned so each starts at 0. The darkest line is the most recent.</p>`; if `list.length > 8` add `'<p class="note">Showing the most recent 8 of ' + list.length + ' occurrences; all ' + list.length + ' appear in the heatmap.</p>'`. Then `<details class="ev-fold"><summary>Occurrence dates</summary><ul>` + one `<li>` per occurrence (all of them, `fpDate`) + `</ul></details>`.

## `frameRecurrence()`
Returns HTML: `<fieldset class="wf recur"><legend>Compare with a repeating pattern (optional)</legend>` then a radio group `<div role="radiogroup" aria-label="Repeat pattern">` with 4 labels, each `<label class="wf"><input type="radio" name="recur-mode" value="M"` + (checked when current) + ` onchange="recurMode('M')"> TEXT</label>` for M/TEXT: `one`/`One window`, `days`/`Same hours on the previous days`, `weeks`/`Same weekday on previous weeks`, `month`/`Last Thursday of every month`. If mode is `one` return that fieldset closed (`</fieldset>`) with a `<p class="note">The reference window above is used as it is.</p>` after the radios. Otherwise add inputs in a `<div class="pair">`: `<label class="wf">Start (UTC)<input class="wf" type="time" value="ST" onchange="recurSet('st', this.value)"></label>`, same for `en` ("End (UTC)"), for `weeks` a `<label class="wf">Weekday<select class="wf" onchange="recurSet('wd', this.value)">` with 7 options (value = number, text = name, `selected` on current), and `<label class="wf">How many ' + unitPlural + ' back<input class="wf" type="number" min="1" max="MAX" value="N" onchange="recurSet('n', this.value)"></label>`. Then, if `recurOcc()` is not ok: `<p class="note" role="alert">` + msg + `</p>`; else `recurSummary(o) + recurHeat(o) + recurOverlay(o)`. Close `</fieldset>`.

## New file `parts/02-css-recur.html`
`<style>` with exactly these rules, then `</style>` and a newline: `.recur .pair{margin-top:8px}` `rect.cell.hit{stroke:var(--text);stroke-width:2}` `rect.cell.hit.dash{stroke-dasharray:3 2}` `.recur-ov{width:100%;height:auto}` `.recur-line{fill:none;stroke:var(--text-2);stroke-width:1;opacity:.45}` `.recur-line.now{stroke:var(--accent);stroke-width:2;opacity:1}`.

## Edits to `parts/30-steps-0-4.js` (verify each SEARCH occurs once with `grep -c -F`)
1. SEARCH `  + frameBanner() + frameDst()` -> REPLACE `  + frameBanner() + frameDst()\n  + (typeof frameRecurrence === 'function' ? frameRecurrence() : '')` (two lines).
2. SEARCH `    + dt('Out of date', 'The form always edits the live windows; editing them is what marks steps 1 to 10 out of date.')` and REPLACE with that same line followed by a new line `    + dt('Repeat pattern', 'Optional. Compare against the same hours on earlier days, the same weekday on earlier weeks, or the last Thursday of each month. The heatmap shows which hours the rule matches; the overlay lines up each occurrence at 0. Times stay in UTC. This preview does not change the windows used to run the analysis.')`.

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/30-recurrence.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/30-steps-0-4.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
