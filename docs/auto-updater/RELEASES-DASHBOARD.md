# Releases dashboard

A small separate program, `unravis-releases`. It is not part of Unravis. It shows every release, read only.

Start: double-click `Start Releases.cmd` (rebuilds on each start, needs Go). It opens its own window (own taskbar entry, Unravis icon, no address bar or tabs). Closing the window ends the program.

The window is Microsoft Edge in app mode (the WebView2 engine Windows already has) with its own profile folder, so it shares nothing with your browser. If Edge is missing, it opens the default browser. Flags: `--open` (default browser), `--print-link` (print the link only, for servers and tests), `--data`, `--port`.

## What it shows

- **Channel strip** (rc and stable, top): the version the signed feed offers, when it was signed and when it expires, pause, rollout and withdrawn versions from the signed control file. These are checked with the same code and trusted keys as the app. A failed check is shown as a failed check.
- **Release list** (left): every release, newest first, one plain status word each (Live, verified / Verified / Published / Withdrawn / Verify failed / Stopped / Running). Up and Down (or j and k) move the selection, R reads again.
- **Detail** (right): the seven release steps (Preflight, Tag, Build, Sign, Publish, Feed, Verify) with times and the reason when one failed, plus facts and links to the release page and the CI run.
- **Run banner**: from the moment `Release.ps1` starts (waiting for master CI, before any version is chosen) until it ends, a banner says what it is doing: waiting for CI, tag, build, sign, publish, feed, verify. Before a version exists the list shows a "Next release" row. The step shows as running at once, not after the step before it is saved. If the process ends without a closing line the banner says it ended, never that it is running. The page re-reads the local files every 5 seconds while a release runs.

## Where the data comes from

| Source | Used for | Network |
|---|---|---|
| `%LOCALAPPDATA%\Unravis\release\state.json` and `audit.jsonl` (override: `--data` or `UNRAVIS_RELEASE_DATA`) | step status, verify result, withdraw, stop reason; `lock-info.json` only for when the run started and its process id (to see it is still alive), never its nonce or requester | none |
| GitHub releases list (public, no token) | version list, dates, links | at most one call per 5 minutes, ETag |
| Signed feed (rc and stable) | what is offered, control file | at most one read per 5 minutes |

On a PC that does not make releases there is no release log. Steps then show "unknown" except Publish and Feed, which are worked out from GitHub and the feed. The GitHub list is not signature checked; the feed cards are.

## Safety

- Read only: GET and HEAD only. Never writes in the release folder. Its only file is its own small update memory (`%LOCALAPPDATA%\\Unravis\\releases-dashboard\\state.json`, used by the same feed checks the app uses); delete it any time.
- No keys, no passphrase, no tokens. Machine, user and key names in the audit log are dropped, and file paths in reasons are replaced by `<path>`, before they reach the page.
- Loopback only, answers only to `127.0.0.1` or `localhost`, needs the one-time code (cookie, same site only), strict content security policy, page text set with `textContent` only.
- Footprint: one small process, no timers while the tab is hidden, 30 s local reads (5 s during a run).
- The audit chain is not re-verified here; use `Release.ps1 -CheckAudit`.

## Not done

- Live progress inside a CI run (build log lines). Step level only.
- Any action button (release, withdraw, pause). Release stays with the script.

## Auto-update (not built yet, BL-199)

Plan: ride the Unravis OTA instead of a second signing product. The releases app is packed into the Unravis setup payload (like the Blackbox helper), so it is replaced by the signed, hash-pinned setup with the same key checks, rollback and silent-update rules, and the Start Menu entry points at the installed copy. Cost: `build.ps1`, `cmd/unravis-setup` and the payload manifest change (sensitive paths, need review and one live rc run). A second signed product in the release manager is not planned: it would double the key and feed handling for a read-only tool.
