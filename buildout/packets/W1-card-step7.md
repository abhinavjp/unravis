# Packet W1: wire banners, impact line and collapsed watch body into `evidenceCard`, and effective verdicts into step 7

Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Output ONLY SEARCH/REPLACE blocks (format at the bottom) and one new file block. Define no new globals. Each SEARCH below is copied byte-for-byte from the current file and occurs EXACTLY ONCE (verified with `grep -c -F`).

Globals that already exist (call only inside functions, always guarded as shown): `evDepEffVerdict(c)` (returns `'none'` when a hard dependency of the card is switched off, else `c.verdict`), `evDepBlocked(c)` (true when a hard dependency of the card is switched off), `evDepBanner(c)` (HTML string, `''` when nothing to show), `evImpact(c)` (HTML string, `''` when none), `evDepToggles(group)` (HTML string).

## Edits to `parts/12-evidence-d.js`

1. SEARCH (one line):
`  var v = (c.verdict === 'ok' || c.verdict === 'watch' || c.verdict === 'bad' || c.verdict === 'none') ? c.verdict : 'none';`
REPLACE with these 3 lines:
```
  var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;
  var v = (ev0 === 'ok' || ev0 === 'watch' || ev0 === 'bad' || ev0 === 'none') ? ev0 : 'none';
  var blocked = (typeof evDepBlocked === 'function') ? evDepBlocked(c) : false;
```

2. SEARCH (one line):
`  return '<section class="ev ' + v + '" aria-label="' + x(c.label) + ': ' + chip + '">' +`
REPLACE with these lines (the new variables first, then the original line unchanged as the last line):
```
  var extra = ((typeof evDepBanner === 'function') ? evDepBanner(c) : '') +
    ((typeof evImpact === 'function' && (!blocked || c.verdict === 'none')) ? evImpact(c) : '');
  var body = '<div class="ev-body">' + evChart(c) + evTable(c) + '</div>';
  if (blocked) {
    body = '<p class="ev-blocked">No chart or table: the data this card needs was not collected.</p>';
  } else if (v === 'watch') {
    body = '<details class="ev-fold"><summary>Show the chart and table</summary>' + body + '</details>';
  }
  return '<section class="ev ' + v + '" aria-label="' + x(c.label) + ': ' + chip + '">' +
```

3. SEARCH (one line):
`    '<div class="ev-body">' + evChart(c) + evTable(c) + '</div>' +`
REPLACE with this one line:
`    extra + body +`

## New file `parts/02-css-card.html`
Exactly (then a newline): `<style>.ev-blocked{margin:0;padding:8px 10px;border:1px dashed var(--line);border-radius:var(--r);color:var(--text-2);font-size:13px}.ev .ev-dep{margin:8px 0}.ev .ev-impact{margin:8px 0}</style>`

## Edits to `parts/33-step7-evidence.js`

4. SEARCH (one line): `    var card2 = cards[j];`
REPLACE with these 2 lines:
```
    var card2 = cards[j];
    var ver2 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(card2) : card2.verdict;
```

5. SEARCH `    if (card2.verdict === 'bad') {` REPLACE `    if (ver2 === 'bad') {`
6. SEARCH `    } else if (card2.verdict === 'watch') {` REPLACE `    } else if (ver2 === 'watch') {`
7. SEARCH `    } else if (card2.verdict === 'none') {` REPLACE `    } else if (ver2 === 'none') {`
8. SEARCH `    } else if (card2.verdict === 'ok') {` REPLACE `    } else if (ver2 === 'ok') {`

9. SEARCH (two lines, needed because `switcher +` alone also matches the `switcher += '<button...'` line earlier in the function):
```
    switcher +
    evidenceLegend() +
```
REPLACE with these 3 lines:
```
    switcher +
    ((typeof evDepToggles === 'function') ? evDepToggles('sql') : '') +
    evidenceLegend() +
```

## Check (you run it; the owner applies the blocks and runs the full check)
From `buildout/wireframes`, after applying the blocks, each of these must print `1`:
```
grep -c -F "  var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;" parts/12-evidence-d.js
grep -c -F "  var extra = ((typeof evDepBanner === 'function') ? evDepBanner(c) : '') +" parts/12-evidence-d.js
grep -c -F "    extra + body +" parts/12-evidence-d.js
grep -c -F "    var ver2 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(card2) : card2.verdict;" parts/33-step7-evidence.js
grep -c -F "    if (ver2 === 'bad') {" parts/33-step7-evidence.js
grep -c -F "    } else if (ver2 === 'watch') {" parts/33-step7-evidence.js
grep -c -F "    } else if (ver2 === 'none') {" parts/33-step7-evidence.js
grep -c -F "    } else if (ver2 === 'ok') {" parts/33-step7-evidence.js
grep -c -F "    ((typeof evDepToggles === 'function') ? evDepToggles('sql') : '') +" parts/33-step7-evidence.js
node --check parts/12-evidence-d.js && node --check parts/33-step7-evidence.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). One block per numbered edit, in the order above. Multi-line REPLACE text is written as plain lines inside the block (not fenced).

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <what you ran>`, `Concerns: none` (or one line).
