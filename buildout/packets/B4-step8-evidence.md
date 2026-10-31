# Packet B4: Mongo evidence view in a new file `parts/34-step8-evidence.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/34-step8-evidence.js` with one global variable `EV_SETS8` and one global function `step8Evidence()`, nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). Do NOT define `evSetPick` (it exists elsewhere; you only reference it inside an onclick string). These globals exist elsewhere and you only USE them: object `W` (you read `W.why` and `W.evSet`), `evFind(id)` (card or `null`), `evidenceLegend()`, `evidenceCard(card)`, `whyView(card)`, `ruledOutDrawer(cardsArray)`, `evDepToggles(group)` (all return HTML strings), `evDepEffVerdict(card)` (returns `'ok'|'watch'|'bad'|'none'`).

## 1. `var EV_SETS8`
Exactly this (keep order, ids, labels verbatim):
```
var EV_SETS8 = {
  incident: { label: 'Real incident', ids: ['MG-BAD-SLOWOPS', 'MG-BAD-TICKETS', 'MG-BAD-REPLAG', 'MG-WATCH-CACHE', 'MG-NONE-COLLSCAN'] },
  quiet: { label: 'Quiet window', ids: ['MG-OK-SLOWOPS', 'MG-OK-TICKETS', 'MG-OK-REPLAG', 'MG-NONE-COLLSCAN', 'MG-NONE-OPLOG'] },
  watch: { label: 'Pressure building', ids: ['MG-WATCH-CACHE', 'MG-WATCH-CONNS', 'MG-WATCH-GLOCK', 'MG-OK-SLOWOPS', 'MG-OK-REPLAG', 'MG-NONE-INDEX'] }
};
```

## 2. `step8Evidence()`
1. If `W.why` is truthy: `card = evFind(W.why)`. If `card` is truthy return `whyView(card)`. Otherwise set `W.why = null` and continue.
2. `key` = `W.evSet` when `EV_SETS8[W.evSet]` exists, otherwise `'incident'`. `set = EV_SETS8[key]`.
3. `cards` = `set.ids` mapped through `evFind`, keeping only truthy results, in order.
4. Classify each card by `evDepEffVerdict(card)` (NOT `card.verdict`): `shown` = cards whose effective verdict is not `'ok'`, ordered all `bad`, then all `watch`, then all `none` (original order inside each group). `okCards` = cards whose effective verdict is `'ok'`, in order.
5. `switcher` = `<div class="seg" role="group" aria-label="Pretend case">` + for each key of `EV_SETS8` in order (`for (var k in EV_SETS8)`): `<button type="button" class="btn" aria-pressed="` + (k === key ? 'true' : 'false') + `" onclick="evSetPick('` + k + `')">` + label + `</button>`, then `</div>`.
6. Return the concatenation, in this order:
   - `<p class="note">Wireframe only: pick a pretend case, or pretend a source is missing, to see how the cards change.</p>`
   - `switcher`
   - `evDepToggles('mongo')`
   - `evidenceLegend()`
   - `<div style="display:grid;gap:12px">` + `evidenceCard(c)` for each of `shown` + `</div>`
   - `ruledOutDrawer(okCards)`

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/34-step8-evidence.js && node ../packets/data/chk-stepev.js parts/34-step8-evidence.js step8Evidence EV_SETS8 mongo
```

## Output
The full file in one code block headed `### parts/34-step8-evidence.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
