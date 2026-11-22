# Test cycle before the first promotion (develop to master), 2026-11-22

Subject: `develop` at `9b99d3a` (18 pull requests since master, see PR 141). One full cycle, no extra runs.

| Check | Where | Result |
|---|---|---|
| Full CI (`ci.yml`): linux-gates, web, go-tests 1/3 to 3/3, go-race, package, powershell-syntax, desktop-sidecar (windows-2022, windows-2025), build | push to `develop`, run 45015101573 | all green |
| UI audit, ubuntu-24.04 and windows-2022 | `develop` at `1000b3e` (run 45014892571) | green |
| UI audit again, after the svelte/vite bump (#139) | promotion PR 141, `9b99d3a` | see PR 141 |
| Full `build` plus Supply chain and desktop-windows | promotion PR 141 | see PR 141 |

Regression and e2e are the go-tests shards, the race job, the package job and the desktop-sidecar jobs inside `ci.yml`; there is no separate e2e workflow.

## What broke, and what was done

- **Dependabot #111 (npm-web)**: typescript 7 does not install next to svelte-check 4.7 (peer `typescript ^5 || ^6`), so `npm ci` fails. Fix: #139 took svelte, svelte-check and vite; typescript majors are ignored in `dependabot.yml`.
- **Dependabot #112 (gomod)**: go-sqlite3 0.37.0 makes `TestBucketCountsReadOneSnapshot` fail. This is a real behaviour change, not a flake. Fix: #139 took the other Go bumps and keeps go-sqlite3 at 0.35.6; go-sqlite3 is ignored in `dependabot.yml` until someone understands the snapshot change. **Needs its own investigation.**
- **`Promote-Develop.ps1` could not run**: a function named `Gh` made `& gh` call itself (PowerShell command lookup is case-insensitive), "call depth overflow". Renamed to `Invoke-Gh`. Found only by running the script, which had not been run live before.
- Stuck concurrency groups: cancelled runs stayed "in_progress" and blocked the next run of the same group. `gh api -X POST repos/<r>/actions/runs/<id>/force-cancel` clears them.

## Not covered

- #131 (owner fixing) is not in this promotion.
- The race job's timing and budget tests stay in the baseline.
- The Go-change and web-change fast-gate seconds are still estimates; PR 139 is the first real web+Go case (go 3m12s, web 1m51s, lint 2m08s, gate 4 s: much slower than the estimate because the runners were busy with the full CI queue at that time).
