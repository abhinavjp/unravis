# BL-371 The command-line window

Owner ruling 2026-11-25: code mode stays OFF by default, and `unravis code run` may run scripts only while the person in the app has said "allow the command line for N minutes". Turning code mode on is not enough.

## Why a window, not a setting

Any program on the PC that can reach the owner pipe can send `code.run`. The pipe proves "a local program", not "this is the person's own `unravis`". A switch in `code-mode.json` would be a file any program can write. So the permission is a short window, held in the app's memory:

- never written to `code-mode.json` (a file edit cannot open it; an app start finds it shut);
- ends by itself (1 to 60 minutes; a longer wish is refused with a plain message, 0 closes it);
- opened only by `PUT /api/code/settings {"cliMinutes": N}`, which needs the session token (the app's page holds it; see "What it does not stop");
- closed when code mode is switched off.

## How it holds

`codemode.CLIGate` (`internal/codemode/cligate.go`) is the broker's `Config.Authorize` hook, so it runs on every path to `Broker.Run`: after the on/off check, before any identifier, case or record work. A refused run records nothing and starts no worker. It answers `cli_closed` (HTTP 403, CLI exit PERMISSION_DENIED) with plain words telling the person where to allow it.

It holds only the surface `"cli"`. The command-line entrance hard-codes that surface (a pipe client cannot choose another); on the HTTP route a User-Agent starting `unravis-cli/` is read as `"cli"`. The app's page (`ui`) and the MCP entrance have their own checks and are untouched.

A plain pipe client (the program alone, no window opened) is refused: `cmd/audit-explorer/codemode_cli_test.go`, `internal/codeapi/cligate_test.go`.

## What it does not stop (Opus review 2026-11-25)

The session token is also in `session.json`, which any program running as the same user can read (the MCP bridge does). Such a program can call `PUT /api/code/settings` itself, or post `/api/code/run` with a User-Agent that is not `unravis-cli/`, which the route labels `ui`. The window therefore stops the **pipe entrance** (`unravis code run`, and any pipe client), which is the owner's ruling; it is not a defence against a same-user program that holds the token. That program could already run scripts before this change once code mode was on, so the window does not widen the hole. A proof that belongs only to the app (for example one tied to the shell's ui-pipe hello) is a later hardening, not part of this item.

`TestTheAppsOwnPageIsNotHeldByTheWindow` pins the `ui` surface as untouched; it is not a claim that a stranger cannot use it.

## Accepted limit

A run started inside the window is not cancelled when the window ends or is closed. Runs are capped at 280 s, so the longest overrun is one run. Closing the window stops every run that starts after it.

## Still owed

The window is Linux-tested. No script runs yet on a real worker on Windows (BL-366/367 stay deferred), so BL-371 is not ticked. The settings screen for it is a prototype first (no real UI until approved).
