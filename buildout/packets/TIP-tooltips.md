# Packet TIP: jargon tooltips in the card help text (`parts/12-evidence-e.js`) and tooltip CSS (`parts/02-css-tip.html`)

Do exactly this. Do not explore the repo, do not read other files, do not improve anything. Create the 1 new file and make the 2 edits below. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). New globals: none. No new state.

## Context
- `parts/12-evidence-e.js` defines `evHelpLook(c)` and `evHelpMore(c)`. `evHelpLook(c)` renders `EVHELP[c.id].look` inside `<p class="ev-look">`. `evHelpMore(c)` renders `EVHELP[c.id].what` / `normal` / `issue` / `check` / `chart` as `<dt>`/`<dd>` pairs (guarded by `hadDt`) plus a "Words used" glossary. Both currently escape each plain help string with their local `x()`.
- `parts/12-evidence-h.js` defines `evTip(c, text)` (already present; do not change it). It takes a card `c` and PLAIN text and returns safe HTML: it escapes `& < > "` and wraps the FIRST whole-word occurrence of each glossary term of that card (`EVHELP[c.id].terms`) as `<span class="ev-tt" tabindex="0" data-tip="TERM: MEANING.">TERM</span>`. When the card has no terms (or no `EVHELP` entry) it returns the escaped text unchanged, so it is a safe drop-in for `x()`.
- Task: pass the card's plain help strings through `evTip(c, ...)` in place of `x(...)`, so jargon terms in the help get a hover/focus tooltip. Keep the "Words used" glossary emitted with `x(...)` (its short definitions are already plain), so definitions are not tooltipped. Keep the local `x()` helpers and the `hadDt` logic exactly as they are; only the two strings below change.

## New file: `parts/02-css-tip.html`
`grep -F '.ev-tip' parts/02-css-*.html` finds no rule, so add the tooltip CSS here. `evTip` (and therefore every tooltip in the help text) emits the class `ev-tt`; this file styles it. Exact contents, followed by a newline:

### parts/02-css-tip.html
```html
<style>
.ev-tt{border-bottom:1px dotted var(--text-2);cursor:help;position:relative}
.ev-tt[data-tip]:hover::after,.ev-tt[data-tip]:focus::after{content:attr(data-tip);position:absolute;left:0;bottom:calc(100% + 6px);z-index:20;width:max-content;max-width:min(280px,80vw);padding:6px 8px;border:1px solid var(--line);border-radius:var(--r);background:var(--surface);color:var(--text);font-size:12px;font-weight:400;line-height:1.4;white-space:normal;box-shadow:0 2px 8px rgba(0,0,0,.18)}
.ev-tt:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
</style>
```

## Edit 1: `parts/12-evidence-e.js`, in `evHelpLook`

### parts/12-evidence-e.js
<<<<<<< SEARCH
  return '<p class="ev-look"><b>What to look for:</b> ' + x(h.look) + '</p>';
=======
  return '<p class="ev-look"><b>What to look for:</b> ' + evTip(c, h.look) + '</p>';
>>>>>>> REPLACE

## Edit 2: `parts/12-evidence-e.js`, in `evHelpMore`

### parts/12-evidence-e.js
<<<<<<< SEARCH
      out += '<dt>' + title + '</dt><dd>' + x(h[key]) + '</dd>';
=======
      out += '<dt>' + title + '</dt><dd>' + evTip(c, h[key]) + '</dd>';
>>>>>>> REPLACE

## Check (the owner applies the blocks and runs this; from `buildout/wireframes`)
```
node --check parts/12-evidence-e.js && node ../packets/data/chk-tip.js
```

## Output
The applied blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
