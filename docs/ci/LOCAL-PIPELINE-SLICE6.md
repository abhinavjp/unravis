# Local CI pipeline, slice 6: many PCs, claims and the Machines tab (BL-585)

Branch `claude/local-ci-fleet`, stacked on slice 5 (`claude/local-ci-ui`, base commit 682e22e8). Spec: `docs/ci/LOCAL-PIPELINE.md`, the "many PCs" part. CI only: the release code paths are untouched. Slice 5 follow-ups are included (an unknown `project=` is now a plain error; `serve` saves the comparison on a timer).

## What it does

- **Claim.** Before running a request (a push or a pull request, one claim per commit and workflow), `serve` creates the git ref `refs/ci-claim/<sha>-<workflow>` on the host. Creating a ref that exists fails, so exactly one PC wins. The release is a delete of the ref when the run is cancelled, fails to start, is superseded, or the PC stops. The core is generic: `internal/localci/claim.go` is the interface (`ClaimHost`), `host_gh_claims.go` is the GitHub implementation (`gh api`), `claim_mem.go` is the fake the tests use. No project names live in `internal/localci` (`purity_test.go` covers tests too); the live test takes the repo from `LOCALCI_LIVE_REPO`.
- **Lease.** The ref points at a commit on the empty tree whose message is a small JSON record: agent id, state, started time and a heartbeat. The holder refreshes it about every 60 s. A record whose heartbeat is older than `--claim-ttl` (default 5 minutes, minimum 2 minutes) can be taken over by another PC.
- **Takeover, and why a plain ref update is not used.** GitHub does not give a compare-and-swap on custom refs: a PATCH with `force=false` does not check the old value in the `refs/ci-*` namespace (found by testing against the real repo). Only create-if-absent is atomic. So a takeover creates the next generation of the name: generation 1 is `<sha>-<workflow>`, generation n is the same name plus `+n`. The highest generation is the one that counts. The holder re-reads the list at every refresh and stops (releases and does not publish a result) if it is no longer the top generation. The taker deletes the generations it replaced. Race: two PCs may both see a stale lease and both try to create generation 2; one create fails, so one wins. If a holder was only slow (not dead) it finds out at its next refresh. The worst case is a duplicate run of the same commit, never a lost run, because a request is never marked done by a PC that is not the top generation.
- **Clocks.** Age is the HTTP `Date` header of the host minus the commit date the host stamped on the record, so a PC with a wrong clock cannot make a lease look fresh or stale.
- **Agent list.** Each PC publishes `refs/ci-agent/<opaque id>` about every 60 s (the id is `m-` plus 8 hex digits, kept in `<state>/fleet/id`). The record holds the id, labels (`--label`), state, current run and heartbeat. Names given with `--name`, host names and paths are never written, because the repository is public (a test checks this). Every PC reads the others through the same host and writes `<state>/fleet/agents.json`. Only people with write access to the repository can create refs, so a fork PR never creates a claim. Remote records are untrusted: they are sanitised, capped and shown as text only.
- **Offline, asleep, draining.** A draining or paused PC (the release pause and keep-awake logic of slice 3) releases its claims. A graceful exit writes a `stopped` agent record. A killed PC writes nothing; its claim expires after the TTL and its agent record is dropped by the next garbage collection (7 days, or 24 hours when it said it stopped).
- **Done claims stay as tombstones (deviation).** A claim that finished is kept (state `done`, with the conclusion) for about 7 days instead of being deleted, so a PC that was deferred does not run the same commit again later. The loser logs "skipped, already ran on PC m-xxxx: failure". Cancel, error, superseded, stop and lost still delete the ref. `unravis-ci claims --clean` removes finished claims older than a week.
- **Fail closed.** The token needs Contents: write. `serve` probes this at start and exits with a plain message when the host refuses (`--no-claims` turns the whole feature off for a single-PC setup). Transient errors only log and the request is deferred and retried.
- **Cost.** About 4 API calls per minute for every claim a PC holds, plus one publish and one sync per minute. One `serve` per state folder. A push and a pull request on the same commit and workflow share one claim.
- **Commands.** `unravis-ci claims [--repo owner/name] [--clean]` lists the PCs and the claims (read-only unless `--clean`). `serve` has `--claim-ttl`, `--no-claims`, `--label` and `--compare-every` (default 10 minutes, `0` is off; it runs `compare --save` so the Overview headline stays fresh).
- **Machines tab.** `internal/releasedash/machines.go` serves `GET /api/machines`, reading only the synced `agents.json` (GET only, loopback only, same cookie and CSP as before). The tab is shown only when more than one PC has been seen or the project record sets `machines`; it stays visible while it is the current tab. Columns: PC, labels, state (Running, Idle, Quiet = no news for more than 3 minutes, Stopped), what it is running now, last heard. This PC is marked "This PC". Other PCs show the opaque id only. A short line says what the tab is and that it can lag by about a minute. Below 720 px the table becomes cards.
- **Slice 5 follow-ups.** An unknown `project=` in the address now shows a plain error and the list of projects instead of silently using the first. `serve` runs `compare --save` on the timer above.

## Live versus seeded

Live, on the real repository `abhinavjp/unravis` with the owner's `gh` login (no statuses were posted, `--no-status`; all test refs were deleted, `matching-refs/ci-` was empty at the end):
- Host create, a second create of the same name (refused), list with age, update, the `+2` generation names, delete, delete of a missing ref. Opt-in suite: `LOCALCI_LIVE_REPO=abhinavjp/unravis go test ./internal/localci/ -run Live` (it passed twice).
- A 6-goroutine race on the real host: exactly one winner.
- Stale-lease takeover at the Fleet level (TTL 30 s, because each real call takes seconds) and release on cancel.
- A real two-`serve` race on pull request 282: PC m-cc93ad02 won and ran fast-gate, PC m-2464d85b deferred three times and then logged "skipped, already ran on PC m-cc93ad02: failure". One claim ref existed, with the winner's id, state `done`, conclusion `failure`; the loser's store has no run. (The failure is step 6 of fast-gate, the known EBUSY on this Windows host, not the claim code.)
- A `serve` takeover of a planted dead lease (agent m-dead0001, planted with `gh api`) at 2 min 2 s with `--claim-ttl 2m`: it wrote the `+2` generation and ran. The planted lease is a simulation; the takeover is real.
- The Machines tab on that data: two PCs Idle, then one PC Running.

Seeded, NOT live:
- Quiet and Stopped states and the multi-state table, from `D:\Projects\ui-audit\seed\store-fleet`.
- The phone card layout on seeded data.
- Empty and unknown-project screens (real code path, but no real second PC).

Not verified live:
- A real second physical PC (both serves ran on one machine, with two state folders).
- A real draining or paused release against GitHub (fake tests cover it).
- A graceful stop writing `stopped` on the live repo (fake command test covers it; the live serves were killed).
- Fork PR behaviour (enforced by slices 2 and 3 and by write-only ref creation).

## UI audit

Screenshots in `D:\Projects\ui-audit\shots\`, 1440 px and 390 px, light and dark: `v6-seeded-*`, `v6-one-pc-*`, `v6-unknown-project-*`, `v6-live-two-pcs-*` (live), `v6-live-running-desktop-light.png` and `v6-live-running-phone-dark.png` (live). No console errors and no horizontal overflow in any of them.

Fixed once:
- Phone: the Machines cards were an ugly grid; each value is now a label and value pair (`.mv` wrapper).
- The tab stayed hidden when opened by address on a one-PC project; it now stays visible while it is current.

Left:
- With an unknown project, the project selector still shows the first project next to the error.
- The fixed footer covers the last card of the phone screenshot when the page is shorter than the screen (screenshot tool, not a layout bug in use).

## Deviations and open questions

- Done claims are kept for about 7 days (see above) rather than deleted on completion. Keep this, or delete on completion and accept a re-run on deferred PCs?
- `claims --clean` removes only finished claims older than a week. Should a killed PC's agent record be removed sooner than the 7-day or 24-hour garbage collection?
- The cross-PC view lags by up to about 60 s (publish and sync period), so a job started on another PC can take up to a minute to show.
- One unexplained live anomaly: at TTL 3 s, once, an `Acquire` right after the other PC's `Finish` returned Won instead of "already done". Not reproduced at TTL 30 s (many runs). Possible read-after-write delay on the ref listing; the duplicate-run worst case covers it.
- Should BL-585 be ticked only after a real second physical PC is seen on the Machines tab? Left unticked for the UI.
- `ui-sweep-f4.test.cjs` fails on this machine with a missing `typescript` module (no `node_modules`); it did not run before this slice either.

## Checks

gofmt (LF files), go vet, `go test ./internal/localci/ ./internal/releasedash/ ./cmd/unravis-releases/ ./cmd/unravis-ci/` (pass; `-count=20` of the double-claim, stale-takeover, release-on-cancel and two-agent race tests), golangci-lint on windows and linux (0 issues), `node --test tests/releasesCiUi.test.cjs` (16 pass).
