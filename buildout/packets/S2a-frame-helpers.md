# Packet S2a: Step 0 helpers in a new file `parts/30-frame-helpers.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/30-frame-helpers.js`. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). It defines only the globals listed here (plus local helper vars). Globals that exist elsewhere and you only USE: `W` (state; `W.frame` has string fields `incidentStart incidentEnd referenceStart referenceEnd` in the form `YYYY-MM-DDTHH:mm` meaning UTC, and `releases` = array of `{at,label}`), `render()`, `esc(s)`. Never read `W` at load time, only inside functions.

Constants: `var FP_MON = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];`

## 1. Time helpers
- `fpMs(s)`: `Date.parse(String(s || '') + 'Z')` (NaN when empty or invalid).
- `fpIso(ms)`: `new Date(ms).toISOString().slice(0, 16)`.
- `fpPad(n)`: two-digit zero pad string.
- `fpWhen(ms)`: UTC day number (no pad), a space, month name from FP_MON, a space, `HH:MM`, then ` UTC`. Example `26 Oct 09:20 UTC`.
- `fpDate(ms)`: UTC day number, space, month name, space, 4-digit year. Example `1 Nov 2026`.
- `fpDur(ms)` (long form): `m = Math.round(ms / 60000)`. If `m >= 1440` and `m % 1440 === 0` -> `N day` or `N days` (N = m/1440; singular only when N is 1). Else if `m >= 60` and `m % 60 === 0` -> `N hour`/`N hours`. Else if `m < 60` -> `N minute`/`N minutes`. Else `H h M min` (H = floor(m/60), M = m % 60). Examples: 259200000 ms -> `3 days`; 300000 -> `5 minutes`; 5400000 -> `1 h 30 min`.
- `fpShort(ms)` (short form): same split, but `Nd`, `Nh`, `Nm`, or `Hh Mm` (e.g. `10d`, `1d`, `45m`, `1h 30m`).
- `fpKey(kind)`: `kind + '|' + W.frame.incidentStart + '|' + W.frame.incidentEnd + '|' + W.frame.referenceStart + '|' + W.frame.referenceEnd`.

## 2. Banner (spec: overlap > touching > ratio; non-blocking)
- `frameBannerKind()`: `a = fpMs(incidentStart), b = fpMs(incidentEnd), c = fpMs(referenceStart), d = fpMs(referenceEnd)`. If any is NaN or `b <= a` or `d <= c` return `''`. Else: if `c < b && a < d` return `'overlap'`; else if `d === a` return `'touching'`; else if `(d - c) > 2 * (b - a)` or `(b - a) > 2 * (d - c)` return `'ratio'`; else `''`.
- `frameKeepTouching()`: `W.frameKeep = fpKey('touching');`
- `frameBanner()`: `kind = frameBannerKind()`; if `''` return `''`. If `typeof W.frameKeep !== 'string'` set `W.frameKeep = fpKey('touching')`. If `W.frameKeep === fpKey(kind)` return `''`. Else return `'<div class="notice" role="status"><p>' + TEXT + '</p><div class="row"><button class="btn sm" onclick="frameFix(\'' + FIX + '\')">' + LABEL + '</button><button class="btn sm" onclick="frameFix(\'keep\')">Keep anyway</button></div></div>'` where:
  - overlap: FIX `move`, LABEL `Move reference before the incident`, TEXT `The reference window overlaps the incident window by ` + fpDur(min(b,d) - max(a,c)) + `. Comparing against a period that includes the problem is not a fair baseline.`
  - touching: FIX `gap`, LABEL `Add a 5-minute gap`, TEXT `The reference window ends the instant the incident starts, with no gap between them. Calls right at the boundary may fall in either window.`
  - ratio: FIX `match`, LABEL `Match the incident window length`. Let `li = b - a`, `lr = d - c`. If `lr > li`: ratio `lr / li`, TEXT `The reference window is ` + R + `&times; the length of the incident window (` + fpShort(lr) + ` vs ` + fpShort(li) + `). Rates are compared per minute, but very different lengths make rare events harder to compare fairly.` Else ratio `li / lr`, TEXT `The incident window is ` + R + `&times; the length of the reference window (` + fpShort(li) + ` vs ` + fpShort(lr) + `). Rates are compared per minute, but very different lengths make rare events harder to compare fairly.` R = the ratio as an integer string when it is a whole number, else `toFixed(1)`.
- `frameFix(kind)`: read the four times as above (`a b c d`), `li = b - a`, `lr = d - c`, `f = W.frame`. `keep`: `W.frameKeep = fpKey(frameBannerKind())`. `move`: `e = a - 300000; f.referenceEnd = fpIso(e); f.referenceStart = fpIso(e - lr);`. `gap`: `f.referenceStart = fpIso(c - 300000); f.referenceEnd = fpIso(d - 300000);`. `match`: `f.referenceStart = fpIso(d - li);`. Then always `render();`. Do nothing (but still render) when any of a b c d is NaN.

## 3. Release lines
- `frameReleaseHint()`: if `W.frame.releases.length === 0` return `'<p class="note">If you know of a deploy, flag flip or config change near the start of the slowdown, add it here. Unravis does not assume one happened.</p>'`, else `''`.
- `frameReleaseLine(r)`: `t = fpMs(r.at)`, `a = fpMs(W.frame.incidentStart)`, `b = fpMs(W.frame.incidentEnd)`. If not `(t >= a && t <= b)` return `''`. Else return `'<p class="note" role="status">This change is inside the incident window (' + fpWhen(t) + '). It may be the cause rather than just a marker &mdash; Step 5 will line it up against the onset.</p>'`.

## 4. Estimate line
`frameEstimateLine()`: `a b c d` as above; if any NaN or `b <= a` or `d <= c` return `''`. `n = (typeof W.srcCount === 'number' && W.srcCount > 0) ? W.srcCount : 2`. `mins = ((b - a) + (d - c)) / 60000`. `rows = Math.round(mins * 20 * n)`. Rows text: if `rows < 1000` the plain number; else if `rows < 1000000`: `p = Math.pow(10, String(rows).length - 3); r = Math.round(rows / p) * p;` formatted with thousands commas (`String(r).replace(/\B(?=(\d{3})+(?!\d))/g, ',')`); else `(Math.floor(rows / 100000) / 10).toFixed(1) + 'M'`. Time text: `rows < 10000` -> `3 s`; `rows < 200000` -> `20 s`; `rows < 1000000` -> `1 min`; else `4 min`. `long = (b - a) > 86400000 || (d - c) > 86400000`. Return `'<p class="note" role="status">This reads &asymp; ' + ROWS + ' calls from ' + n + ' source' + (n === 1 ? '' : 's') + ' and takes about ' + TIME + '.' + (long ? ' Shown as hourly averages &mdash; see below.' : '') + '</p>'`.

## 5. Daylight-saving notice
- `fpNthSunday(y, m, n)`: `first = new Date(Date.UTC(y, m, 1)).getUTCDay(); return Date.UTC(y, m, 1 + ((7 - first) % 7) + (n - 1) * 7);`
- `fpLastSunday(y, m)`: `last = new Date(Date.UTC(y, m + 1, 0)); return Date.UTC(y, m, last.getUTCDate() - last.getUTCDay());`
- `frameDst()`: gather valid starts and ends of both windows (`fpMs`; skip NaN). If none return `''`. `lo` = smallest start, `hi` = largest end. For each year from `new Date(lo).getUTCFullYear()` to `new Date(hi).getUTCFullYear()` collect these four UTC-midnight dates: US start `fpNthSunday(y, 2, 2)`, US end `fpNthSunday(y, 10, 1)`, EU start `fpLastSunday(y, 2)`, EU end `fpLastSunday(y, 9)`. Sort ascending. The first date `d` with `d < hi && d + 86400000 > lo` triggers: return `'<p class="note" role="status">This window crosses a daylight-saving change on ' + fpDate(d) + '. All times above stay in UTC; the same UTC time may map to a different local clock time before and after this date.</p>'`. Otherwise `''`.

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/30-frame-helpers.js && node ../packets/data/chk-s2a.js
```

## Output
The full file in one code block headed `### parts/30-frame-helpers.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
