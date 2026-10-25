# Phase 04: start-screen
risk: medium — reasons: replaces the first screen every user sees; touches app state routing; small data surface.

## Outcome
The first screen always shows three cards: use an existing case, enter an existing case by its 26-character code or by an exported case file, create a new one. "Use an existing case" opens a case from the list; "Enter" resolves a code or restores a case file as a new case; "Create" hands over to the wizard (built in phase 05; until then it opens the existing import panel so the app stays usable). When a master key is set and locked, the unlock screen comes first; the user can also set the key up from here (TSK-16).

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-13 three-card start screen | medium | TSK-05 (client types), TSK-06 (counts), TSK-11 (lock status) |
| TSK-14 enter by code or case file | medium | TSK-12, TSK-13 |
| TSK-15 app-state routing + copy | low | TSK-13 |
| TSK-16 master-key set-up, unlock, lock screens | medium | TSK-11, TSK-13 |

## Edges
TSK-14 and TSK-15 both edit app-state; run TSK-15 first or serialise (shared file `appState.svelte.ts`). TSK-13 owns `StartScreen.svelte`; TSK-14 and TSK-16 add to it, so serialise them after TSK-13. TSK-15 and TSK-16 both touch `AppHeader.svelte` (home action vs lock action): serialise.

## Review focus
The three cards are present in every state (no cases, one case, many cases, backend offline, master key locked); a bad code or a bad case file gives one identical message; a forgotten master key is explained before it can hurt; keyboard and screen-reader reachability; no regression in how a running analysis returns to the start.

## Phase proof
`cd web; npm test; npm run check; npm run build` green; browser pane pass of the start screen at desktop and mobile widths.
Commit: one commit for the phase, after a stop. Never push.
