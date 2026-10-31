# Packet B9: plain-words line, term tooltips and query-ID helper in a new file `parts/12-evidence-h.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-h.js` defining exactly these three globals plus one local helper, nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Data globals that may or may not exist when called (always guard with `typeof X === 'object' && X`): `EVIMPACT` (card id -> sentence) and `EVHELP` (card id -> `{terms:[{t,d}, ...], ...}`).

Local helper: `function evEsc2(s)` returns `String(s == null ? '' : s)` with `&` `<` `>` `"` replaced by `&amp; &lt; &gt; &quot;` (replace `&` first; use `split/join`).

## 1. `evImpact(c)`
`c` is a card `{id}`. If `EVIMPACT` is an object and `EVIMPACT[c.id]` is a non-empty string, return `'<p class="ev-impact"><b>In plain words:</b> ' + evEsc2(that string) + '</p>'`, else `''`.

## 2. `evTip(c, text)`
Input `text` is PLAIN text (not HTML). Return HTML: `text` escaped with `evEsc2`, where, for each glossary term of this card, the FIRST whole-word occurrence is wrapped as `<span class="ev-tt" tabindex="0" data-tip="` + evEsc2(t + ': ' + d) + `">` + evEsc2(t) + `</span>`.
Steps:
1. `terms` = `EVHELP[c.id].terms` when `EVHELP` is an object, the entry is an object and `terms` is an array; otherwise `[]`. If `terms` is empty return `evEsc2(text)`.
2. Work on the ESCAPED text `s = evEsc2(text)`. Take the terms with non-empty string `t`, ordered by `t.length` descending (copy the array first; do not mutate it). Keep `marks = []`.
3. For each term, `et = evEsc2(t)`. Search `s` for `et` from position 0 with `indexOf`, advancing past rejected hits: a hit is accepted only when the character before it (if any) and the character after it (if any) are NOT letters, digits or `_` (test with `/[A-Za-z0-9_]/`), and the hit does not overlap the placeholder `\u0001<number>\u0001` already inserted. On acceptance replace that one occurrence in `s` with `'\u0001' + marks.length + '\u0001'`, push the span HTML for this term onto `marks`, and stop searching for this term (only the first occurrence is wrapped).
4. Finally replace each placeholder `'\u0001' + i + '\u0001'` with `marks[i]` and return the string.

## 3. `evQueryId(id)`
Returns `'<span class="ev-qa"><code>' + evEsc2(id) + '</code><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="' + evEsc2(id) + '">Copy query ID</button><span>Open the plan in SSMS or Azure Data Studio by this query ID (Query Store, Top Resource Consuming Queries). Unravis does not read plans.</span></span>'`.

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/12-evidence-h.js && node ../packets/data/chk-b9.js
```

## Output
The full file in one code block headed `### parts/12-evidence-h.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
