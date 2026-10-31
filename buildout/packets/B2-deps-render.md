# Packet B2: banner renderer and pretend toggles in a new file `parts/12-evidence-g.js`

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create ONE new file `buildout/wireframes/parts/12-evidence-g.js` defining exactly these globals, nothing else. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Globals that exist elsewhere and you only USE: `W` (state object), `render()`, `EVDEP_DEFS` (object key -> `{group,soft,title,why,effect,fix[],note}`), `EVDEP_MAP` (card id -> array of dep keys). A card is `{id, verdict}` where verdict is `'ok'|'watch'|'bad'|'none'`.

Local helper inside the file: `function evEsc(s)` returns the string with `&` `<` `>` `"` replaced by `&amp; &lt; &gt; &quot;` (use `split/join`).

## Functions
1. `evDepsOff()`: if `W.deps` is not an object set `W.deps = {}`; return `W.deps`. (`W.deps[key] === true` means the pretend source is missing.)
2. `evDepFor(c)`: returns `null` or `{key, def, forced, soft}`. `keys = EVDEP_MAP[c.id] || []`. In this order: (a) the first key `k` in `keys` with `evDepsOff()[k] === true` and `EVDEP_DEFS[k].soft === false` -> `{key:k, def:EVDEP_DEFS[k], forced:false, soft:false}`; (b) else if `c.verdict === 'none'` and some key in `keys` has `soft === false`, the first such key -> `{..., forced:true, soft:false}`; (c) else the first key in `keys` that is off and `soft === true` -> `{..., forced:false, soft:true}`; else `null`.
3. `evDepBlocked(c)`: `true` only when branch (a) above applies (a hard dependency pretended missing), else `false`.
4. `evDepEffVerdict(c)`: `'none'` if `evDepBlocked(c)` else `c.verdict`.
5. `evDepBanner(c)`: `d = evDepFor(c)`; if `null` return `''`. Else return this HTML (all dynamic text through `evEsc`):
   `<div class="ev-dep" role="note"><p class="ev-dep-title"><b>Could not check:</b> ` + title + `</p><p class="ev-dep-why">` + why + `</p><p class="ev-dep-effect">` + effect + `</p>` + (fix block only if `def.fix.length > 0`: `<div class="ev-dep-fix"><p class="ev-dep-fixlabel">To turn it on, an administrator can run:</p><pre class="ev-cmd">` + lines joined with `\n` (escaped) + `</pre><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="` + lines joined with `\n` (escaped) + `">Copy command</button></div>`) + `<p class="ev-dep-note">` + note + `</p></div>`.
6. `evDepToggle(key)`: `o = evDepsOff(); if (o[key]) { delete o[key]; } else { o[key] = true; } render();`
7. `evDepToggles(group)`: returns `<div class="seg ev-dep-pick" role="group" aria-label="Pretend a source is missing">` + for each key in `EVDEP_DEFS` (in order) whose `group` equals the argument: `<button type="button" class="btn" aria-pressed="` + (off ? 'true' : 'false') + `" onclick="evDepToggle('` + key + `')">` + (`Pretend off: ` + title) + `</button>` + `</div>`.
8. `evCopy(btn)`: `t = btn.getAttribute('data-copy') || ''`. In a try/catch: if `navigator.clipboard && navigator.clipboard.writeText` call it, else create a `textarea` via `document.createElement`, set `.value = t`, append to `document.body`, `.select()`, `document.execCommand('copy')`, remove it. Before copying, save `var orig = btn.textContent;`. Then set `btn.textContent = 'Copied'` and `setTimeout(function(){ btn.textContent = orig; }, 1500)`. Never throw.

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/12-evidence-g.js && node ../packets/data/chk-b2.js
```

## Output
The full file in one code block headed `### parts/12-evidence-g.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
