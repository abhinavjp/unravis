# Code-mode settings: prototype notes (v2)

Prototype: `web/prototype/code-mode-settings.html`. One standalone page, example data only, not wired to the app. The real settings UI waits for nachi's approval.

## Decision (read this first)

**A guided popup, seven steps, one question per screen, with a left rail that shows where you are.** Not an inline page.

Why a popup stepper beat the inline settings page:

- Code mode lets an assistant run scripts on audit data. A consequential, rarely changed setting should slow the reader down once, not sit among everyday toggles where one click flips it.
- "No time limit" needs a typed confirmation and a warning that is read, not skimmed. A step can hold that; a row of switches cannot.
- Nothing is saved until the review step. Back, Next and "Change" on every review line make it safe to look around.
- The popup can be opened from the header badge (see below), so the same guided flow serves "turn it on" and "turn it off in a hurry".
- Cost: more clicks for someone who only wants to change the command-line minutes. Mitigated: from the Result step "Change settings" returns to the review, and the badge opens straight to Result, where "Switch to a 15 minute window" and "Close the command line now" are one click.

## Order of steps and why

1. **What it is.** Plain words first: what code mode does and does not do, so the next steps make sense.
2. **Switch on.** The master choice. Choosing off skips steps 3 to 5 (nothing to configure) and goes to Review.
3. **Assistants.** Who may run scripts. Chosen before the safety choices because it is the widest consequence.
4. **Safety choices.** Keep a copy of every result, weaker safe runner. Defaults are the careful ones. Real IDs stay masked and are read-only here (set at start-up).
5. **Command line.** Shut, timed window (1 to 60 minutes), or no time limit. Last because it is the riskiest option and the reader now has the full picture.
6. **Review and confirm.** Every choice on one screen with Change links. Danger choices repeat their warning. Nothing has changed until the button here.
7. **Result.** What is now true, safe-runner status, the command-line state, and how to turn it off again.

## Danger path: no time limit

- Third option on step 5, styled as danger (red, warning icon, not just colour).
- Warning in plain words: any program running as your Windows user that can reach the command-line connection can run scripts on your audit data for as long as the window is open; Unravis cannot tell it from you typing at the keyboard.
- To continue the reader types `no time limit`. Next stays disabled until it matches.
- Review repeats the warning. Result shows a red "Danger" panel with "Switch to a 15 minute window" and "Close the command line now".
- Not remembered: when Unravis restarts the command line is shut again, so the reader must confirm it again after a restart.

## The persistent badge

- In the app header: hidden when code mode is off; "Code mode on · command line shut"; "Code mode on · mm:ss left" (amber in the last minute); "Code mode on · no time limit" (danger style, warning icon); "Code mode off · settings file damaged".
- Phone: short labels (On, mm:ss, No limit, Off). The badge has an accessible name with the full state, and a 36 px minimum height on phones.
- Click opens the popup on the Result step.

### How it will be wired

- Reads `GET /api/code/settings`: `enabled`, `cliOpenUntil` (RFC 3339), `damaged`, plus a new `cliUnlimited` boolean.
- Re-read on window focus, every 30 s, and after every PUT.
- The countdown runs locally from `cliOpenUntil`, corrected by the offset between the server clock and the browser clock; it is never trusted to be the enforcement, the gate is.

## Backend change this needs (when built)

- New PUT field `cliUnlimited` (bool), mutually exclusive with `cliMinutes`.
- `codemode.CLIGate` gains an unlimited state: in memory only, closed on app start and when code mode goes off, so it must be re-confirmed after every restart.
- Default stays off. This changes the security model of the CLI run path: it needs a fresh Opus security review before it ships.
- Audit: opening and closing the window, and the unlimited state, are recorded.

## What is real and what is example

- Real: the field names, the 0 to 60 range, the error words, the gate behaviour for the timed window (merged, PR #309).
- Example: every state in the prototype bar (Fresh install, On command line shut, Window open, Last 8 seconds, No time limit, Damaged file, Simulate app restart, Toggle safe runner) and the data shown.
- Not built: `cliUnlimited`, the badge, the popup in the real app.
