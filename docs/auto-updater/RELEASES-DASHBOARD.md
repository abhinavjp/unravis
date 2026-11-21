# Releases dashboard

A small separate program, `unravis-releases`. It is not part of Unravis. It shows every release, read only.

Start: double-click `Start Releases.cmd` (rebuilds on each start, needs Go). It opens its own window (own taskbar entry, Unravis icon, no address bar or tabs). Closing the window ends the program.

The window is Microsoft Edge in app mode (the WebView2 engine Windows already has) with its own profile folder, so it shares nothing with your browser. If Edge is missing, it opens the default browser. Flags: `--open` (default browser), `--print-link` (print the link only, for servers and tests), `--data`, `--port`.

## What it shows

- **Channel strip** (rc and stable, top): the version the signed feed offers, when it was signed and when it expires, pause, rollout and withdrawn versions from the signed control file. These are checked with the same code and trusted keys as the app. A failed check is shown as a failed check.
- **Release list** (left): every release, newest first, one plain status word each (Live, verified / Verified / Published / Withdrawn / Verify failed / Stopped / Running). Up and Down (or j and k) move the selection, R reads again.
- **Detail** (right): the seven release steps (Preflight, Tag, Build, Sign, Publish, Feed, Verify) with times and the reason when one failed, plus facts and links to the release page and the CI run.
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

## Auto-update (BL-199)

The releases app rides the Unravis OTA instead of being a second signed product. `build.ps1` builds `dist\unravis-releases.exe` and puts it in the setup payload (an optional companion, like the Blackbox helper): the same signed manifest, SHA-256 pin, key checks, silent-update rules and rollback cover it. Setup places it beside `unravis.exe` and adds a Start-menu entry "Unravis Releases" (with its own icon). Uninstall removes both. A setup program built without it installs exactly as before.

- It updates when Unravis updates, so the release manager needs no change and there is no second feed.
- If the releases window is open during an update, the old file is moved aside and the new one placed; the open window keeps running the old version until it is closed and opened again.
- Installed copies: setup writes the link in the profile through the same window-owned step as the Unravis link, and only when the file is really there.

Not verified: needs `go test ./internal/setup ./cmd/unravis-setup` on Windows (these tests are Windows-only; on Linux they fail the same way with or without this change), then one real rc release and an update on a PC that has the app installed.
