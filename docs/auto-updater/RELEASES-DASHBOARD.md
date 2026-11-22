# Releases dashboard

A small separate program, `unravis-releases`. It is not part of Unravis. It shows every release, read only.

Start: double-click `Start Releases.cmd` (rebuilds on each start, needs Go). It opens its own window (own taskbar entry, Unravis icon, no address bar or tabs). Closing the window ends the program.

The window is Microsoft Edge in app mode (the WebView2 engine Windows already has) with its own profile folder, so it shares nothing with your browser. If Edge is missing, it opens the default browser. Flags: `--open` (default browser), `--print-link` (print the link only, for servers and tests), `--data`, `--port`.

## What it shows

- **Channel strip** (rc and stable, top): the version the signed feed offers, when it was signed and when it expires, pause, rollout and withdrawn versions from the signed control file. These are checked with the same code and trusted keys as the app. A failed check is shown as a failed check.
- **Release list** (left): every release, newest first, one plain status word each (Live, verified / Verified / Published / Withdrawn / Verify failed / Stopped / Running). Up and Down (or j and k) move the selection, R reads again.
- **Detail** (right), six tabs, TeamCity and Octopus style:
  - **Steps**: the seven steps (Preflight, Tag, Build, Sign, Publish, Feed, Verify) with state, start, end and duration. Click a step to open its log: live tail while it runs, search, Copy, Jump to end. A stopped release shows a red "Stopped at <step>" box with the reason and the lines before it, and the exact failing line is marked in the log.
  - **Tests**: passed, failed and skipped counts read from the CI jobs and the clean-machine verify run, grouped by test suite; failed tests with their output.
  - **CI checks**: every check run on the release commit (name, result, duration, link to GitHub).
  - **Files**: the assets on the GitHub release with the SHA-256 GitHub recorded, the signing key id and the hash of the signed feed file.
  - **Changes**: the pull requests and commits since the release before it in the same channel.
  - **All logs**: every saved log of the release (what the release script printed, CI run logs), searchable.
- **Run banner**: from the moment `Release.ps1` starts (waiting for master CI, before any version is chosen) until it ends, a banner says what it is doing: waiting for CI, tag, build, sign, publish, feed, verify. Before a version exists the list shows a "Next release" row. The step shows as running at once, not after the step before it is saved. If the process ends without a closing line the banner says it ended, never that it is running. The page re-reads the local files every 5 seconds while a release runs.

## Where the data comes from

| Source | Used for | Network |
|---|---|---|
| `%LOCALAPPDATA%\Unravis\release\state.json` and `audit.jsonl` (override: `--data` or `UNRAVIS_RELEASE_DATA`) | step status, verify result, withdraw, stop reason; `lock-info.json` only for when the run started and its process id (to see it is still alive), never its nonce or requester | none |
| `%LOCALAPPDATA%\Unravis\release\logs\run-<time>.log` | what `Release.ps1` printed (one line per row with its time, redacted when written); gives each step its log lines, start time and the "STOPPED at" line | none |
| `logs\ci-<tag>-<ci, release or verify>-<run id>.log.gz` | the GitHub Actions run logs the script saves after a run, so the Tests tab can read them later (redacted when written) | none |
| GitHub releases list (public, no token) | version list, dates, links, assets and their SHA-256 | at most one call per 5 minutes, ETag |
| GitHub check runs and compare (public, no token) | CI checks tab, Changes tab | on demand, kept 5 minutes for a release being made and a day for a finished one |
| Signed feed (rc and stable) | what is offered, control file | at most one read per 5 minutes |

On a PC that does not make releases there is no release log. Steps then show "unknown" except Publish and Feed, which are worked out from GitHub and the feed. The GitHub list is not signature checked; the feed cards are.

## Safety

- Read only: GET and HEAD only. Never writes in the release folder. Its only file is its own small update memory (`%LOCALAPPDATA%\\Unravis\\releases-dashboard\\state.json`, used by the same feed checks the app uses); delete it any time.
- No keys, no passphrase, no tokens. Machine, user and key names in the audit log are dropped, and file paths in reasons are replaced by `<path>`, before they reach the page. Logs are redacted twice, by `Protect-LogLine` in `ReleaseLib.ps1` when written and by `redact.go` when read (tokens, bearer headers, key blocks, long secrets, machine and user names). The log route only opens files named like the two patterns above, never a path.
- Loopback only, answers only to `127.0.0.1` or `localhost`, needs the one-time code (cookie, same site only), strict content security policy, page text set with `textContent` only.
- Footprint: one small process, no timers while the tab is hidden, 30 s local reads (5 s during a run).
- The audit chain is not re-verified here; use `Release.ps1 -CheckAudit`.

## Not done

- Live lines from inside a CI run while it is still building (the CI log is saved after the run ends). Step level only until then.
- Logs and test results exist only for releases made after the capture was added (the next rc). Older releases show step times from the audit log, the CI checks, files and changes, and say plainly that no logs were saved.
- Any action button (release, withdraw, pause). Release stays with the script.

## Auto-update (not built yet, BL-199)

Plan: ride the Unravis OTA instead of a second signing product. The releases app is packed into the Unravis setup payload (like the Blackbox helper), so it is replaced by the signed, hash-pinned setup with the same key checks, rollback and silent-update rules, and the Start Menu entry points at the installed copy. Cost: `build.ps1`, `cmd/unravis-setup` and the payload manifest change (sensitive paths, need review and one live rc run). A second signed product in the release manager is not planned: it would double the key and feed handling for a read-only tool.
