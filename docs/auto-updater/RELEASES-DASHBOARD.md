# Releases dashboard

A small separate program, `unravis-releases`. It is not part of Unravis. It shows every release, read only.

Start: double-click `Start Releases.cmd` (builds on first run, needs Go), or `go run ./cmd/unravis-releases --open`.
It prints a link with a one-time code. Open it in a browser on this PC.

## What it shows

- **Feed cards** (rc and stable): the version the signed feed offers, when it was signed and when it expires, pause, rollout and withdrawn versions from the signed control file. These are checked with the same code and trusted keys as the app. A failed check is shown as a failed check.
- **Releases table**: every release on GitHub, newest first, with channel, on feed, verified / verify failed / withdrawn, and the seven release steps (Preflight, Tag, Build, Sign, Publish, Feed, Verify). A failed step shows the reason on hover. The "run" link opens the CI run.
- **Run banner**: while `Release.ps1` is running, the step it is on. The page then re-reads the local files every 5 seconds.

## Where the data comes from

| Source | Used for | Network |
|---|---|---|
| `%LOCALAPPDATA%\Unravis\release\state.json` and `audit.jsonl` (override: `--data` or `UNRAVIS_RELEASE_DATA`) | step status, verify result, withdraw, stop reason | none |
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
