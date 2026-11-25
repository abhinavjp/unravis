# Local CI pipeline, slice 5: Overview and Runs tabs in the Releases app (BL-584)

Branch `claude/local-ci-ui`, stacked on slice 4 (`claude/local-ci-status`). Spec: `docs/ci/LOCAL-PIPELINE.md` section 5a, layout A, plus `docs/ci/research-ci-ui.md`. CI only: the release code paths are untouched except that they now sit under the Releases tab and show commit chips.

## What it does

- Read-only JSON routes (GET and HEAD only, loopback only, the existing strict CSP and no-store, same one-time-code cookie): `/api/projects`, `/api/overview`, `/api/runs` (filters `project`, `status`, `branch`, `pr`, `sha`, `limit`, `cursor`), `/api/run` (jobs, steps, timings), `/api/run/log` (per-step log, `tail` and `offset`). Every string goes through `redact.go`. Run ids are checked before any path is built (`../` and separators are refused with 400).
- `internal/localci/read.go` is the one reader (the CLI uses the same functions): ledger, queue listing without creating folders, saved compare report, agent heartbeats. The core stays project-name-free (`purity_test.go`).
- Per-project record (`internal/releasedash/project.go`, flag `--projects <file.json>`, default built in `cmd/unravis-releases/projects.go`): name, repo slug, CI store folder, release step labels, branch names, `releases` on or off. The Releases screens read their channel and step names from it; the default equals the old hard-coded values, so nothing changes there. Flag `--ci-store` overrides the store of the default project.
- UI (`ui/ci.js`, `ui/ci.css`): project switcher (hidden with one project), tabs Overview, Runs, Releases. Overview: latest run per branch and pull request, running and waiting, agents and queue (stale heartbeat warning), the comparison headline (streak toward 20, disagreements, with a one-line explanation of what "checked on this PC" means) and a link to Runs. Runs: filterable list with Show more, run page with job and step timings, log viewer with search, copy, jump to end and live tail while the run is Running. Status words: Passed, Failed, Running, Waiting, Stopped, Not recorded here. Commit chips link a run to the release that contains its commit, and a release back to its runs.
- Hash routes: `#/overview`, `#/runs[?status&branch&pr&sha&project]`, `#/runs/<id>`, `#/releases[/<version>]`.

Not built: `/api/run/tests` and `/api/run/changes`. The store keeps no per-test or per-file-change data, so there is nothing to serve (the brief said to leave them out in that case).

## Live versus seeded

Live (the real store `D:\Projects\ci-live-store-4` and the real release data in `%LOCALAPPDATA%\Unravis\release`): Overview, Runs, a real run page with real step logs, the real agent heartbeat, the comparison headline after a real `unravis-ci compare --save` (0 of 20, 30 GitHub-only), and the Releases tab listing the 16 real releases with unchanged steps. No console errors, no horizontal overflow at 390 px, no contrast failures in light or dark.

Seeded, NOT live (a generated store, mixed with the real release data, at `D:\Projects\ui-audit\seed`): Running and Waiting states, the stale-agent warning, live tail of an appended log, a 100,000-line log (first lines in about 150 ms, 1,501 lines in the DOM, paging controls), 69 runs with paging and every filter, release chips both ways (joined on the real commit a27432a of rc.61), the four-project switcher and its phone layout, a project with `releases:false` (Releases tab hidden), empty store, missing folder, missing run, traversal id, server-lost.

## UI audit

Screenshots, desktop 1440 and phone 390, light and dark: `D:\Projects\ui-audit\shots\` (`v4-*` and `v5-*` live store; `v2s-*`, `v3-*` and `v5-*` seeded; key ones: `v4-desktop-overview.png`, `v4-phone-overview.png`, `v4-desktop-run-open.png`, `v4-desktop-releases.png`, `v2s-desktop-run-with-release-chip.png`, `v2s-desktop-longlog.png`, `v2s-desktop-running-run.png`, `v3-desktop-server-lost.png`, `v5-phone-light-*`).

Fixed once:
- Refresh button stretched across the whole filter row: now sits in its own grid cell.
- Filter placeholder text too faint: uses the muted text colour at full opacity.
- Phone: the project switcher was clipped: now wraps to its own full-width line.
- Durations such as "1 min 35 s" broke across two lines: non-breaking spaces.
- A lost connection showed "Failed to fetch": now says "Could not reach the Releases program. It may have been closed. Start it again, then press Refresh."
- Comparison card with no local results read as an error: now says none has been checked on this PC yet and how to start the agent (`unravis-ci serve`).

Left:
- An unknown `project=` in the hash silently shows the first project (harmless, but not announced).
- The 404 and 400 console lines for a missing or malformed run id are the browser logging the expected response.
- The "Search this log" input is narrow on a phone (existing viewer CSS, shared with the Releases logs).
- Log search covers the loaded window only (the viewer pages very long logs).

## Deviations and open questions

- BL-584 in the backlog is the slice 4 leftovers (`fallback`, `--post-required`); this slice's UI items are recorded under it and ticked separately. `fallback` and `--post-required` stay unticked.
- `unravis-ci compare --save` is what feeds the Overview headline; until someone runs it the card says so. Should `serve` save it on a timer? Needs an owner go (agent change).
- Should the Machines tab and `/api/run/tests` wait for a collector that records them?

## Checks

gofmt (LF files), go vet, `go test ./internal/localci/ ./internal/releasedash/ ./cmd/unravis-releases/ ./cmd/unravis-ci/`, golangci-lint on windows and linux (0 issues), `node --test tests/releasesCiUi.test.cjs tests/ui-sweep-f4.test.cjs` (45 pass).
