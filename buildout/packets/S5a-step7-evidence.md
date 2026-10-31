# Packet S5a: `step7Evidence`, `evSetPick` in a new file `parts/33-step7-evidence.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/33-step7-evidence.js` with one global variable `EV_SETS` and two global functions `evSetPick(key)` and `step7Evidence()`, nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`, spread). These globals exist elsewhere and you only USE them (never define them): object `W` (state; you read `W.why` and `W.evSet`), function `render()`, `evFind(id)` (returns a card or `null`), `evidenceLegend()`, `evidenceCard(card)`, `whyView(card)`, `ruledOutDrawer(cardsArray)` (each returns an HTML string). A card has a string `verdict` of `'ok'|'watch'|'bad'|'none'`.

## 1. `var EV_SETS`
Exactly this object (keep key order, ids and labels verbatim):
```
var EV_SETS = {
  incident: { label: 'Real incident', ids: ['sql-waits-bottleneck', 'sql-blocking-chain', 'sql-tempdb-contention', 'sql-lock-waits-rising', 'sql-waits-benign', 'sql-log-growth', 'sql-plan-regression'] },
  quiet: { label: 'Quiet window', ids: ['sql-waits-benign', 'sql-topqueries-orders', 'sql-log-growth', 'sql-plan-regression', 'sql-deadlocks'] },
  watch: { label: 'Pressure building', ids: ['sql-lock-waits-rising', 'sql-memory-grants', 'sql-index-maintenance', 'sql-waits-benign', 'sql-topqueries-orders', 'sql-deadlocks'] }
};
```

## 2. `evSetPick(key)`
Sets `W.evSet = key; W.why = null;` then calls `render();`.

## 3. `step7Evidence()`
1. If `W.why` is truthy: `card = evFind(W.why)`. If `card` is truthy return `whyView(card)`. Otherwise set `W.why = null` and continue.
2. `key` = `W.evSet` when `EV_SETS[key]` exists, otherwise `'incident'`. `set = EV_SETS[key]`.
3. `cards` = `set.ids` mapped through `evFind`, keeping only truthy results, in order.
4. `shown` = the cards whose `verdict` is not `'ok'`, ordered: all `bad` first, then all `watch`, then all `none` (keep the original order inside each group). `okCards` = the cards whose `verdict` is `'ok'`, in order.
5. `switcher` = `<div class="seg" role="group" aria-label="Pretend case">` + for each key of `EV_SETS` in order (`for (var k in EV_SETS)`): `<button type="button" class="btn" aria-pressed="` + (k === key ? 'true' : 'false') + `" onclick="evSetPick('` + k + `')">` + label + `</button>` + `</div>`.
6. Return the concatenation of, in this order:
   - `<p class="note">Wireframe only: pick a pretend case to see how the colours change.</p>`
   - `switcher`
   - `evidenceLegend()`
   - `<div style="display:grid;gap:12px">` + `evidenceCard(c)` for each of `shown` joined with nothing + `</div>`
   - `ruledOutDrawer(okCards)`

## Check (run from `buildout/wireframes`; it must print `PASS`)
```
node --check parts/33-step7-evidence.js && node -e "const vm=require('vm'),fs=require('fs');let n=0;const S={a:{id:'sql-waits-bottleneck',verdict:'bad'},b:{id:'sql-lock-waits-rising',verdict:'watch'},c:{id:'sql-waits-benign',verdict:'ok'},d:{id:'sql-log-growth',verdict:'ok'},e:{id:'sql-plan-regression',verdict:'none'},f:{id:'sql-blocking-chain',verdict:'bad'}};const c={W:{why:null,evSet:'incident'},render:()=>{n++},evFind:id=>{for(const k in S)if(S[k].id===id)return S[k];return null},evidenceLegend:()=>'<LG>',evidenceCard:x=>'<C:'+x.id+'>',whyView:x=>'<WV:'+x.id+'>',ruledOutDrawer:a=>'<RO'+a.length+'>'};vm.createContext(c);vm.runInContext(fs.readFileSync('parts/33-step7-evidence.js','utf8')+';this.sp=evSetPick;this.s7=step7Evidence;this.SETS=EV_SETS;',c);const h=c.s7();const order=h.indexOf('<C:sql-waits-bottleneck>')<h.indexOf('<C:sql-blocking-chain>')&&h.indexOf('<C:sql-blocking-chain>')<h.indexOf('<C:sql-lock-waits-rising>')&&h.indexOf('<C:sql-lock-waits-rising>')<h.indexOf('<C:sql-plan-regression>');c.W.why='sql-log-growth';const w=c.s7();c.W.why='nope';const z=c.s7();c.sp('quiet');const ok=Object.keys(c.SETS).join()==='incident,quiet,watch'&&order&&h.indexOf('<LG>')>0&&h.indexOf('<RO2>')>0&&h.indexOf('aria-pressed=\"true\" onclick=\"evSetPick(\\'incident\\')\"')>0&&w==='<WV:sql-log-growth>'&&z.indexOf('<LG>')>=0&&c.W.evSet==='quiet'&&c.W.why===null&&n===1;console.log(ok?'PASS':'FAIL '+h)"
```

## Output
The full file in one code block headed `### parts/33-step7-evidence.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
