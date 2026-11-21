# Release coordination and speed

Spec and operating rules for the release manager ([RELEASE-MANAGER.md](RELEASE-MANAGER.md)) so that many threads and sessions can ask for a release without stepping on each other, and so a release is fast. Written 21 Nov 2026. Threats: [THREAT-MODEL.md](THREAT-MODEL.md) rows 10, 13, 14.

## What went wrong before

Measured on the rc.55 run (audit log, 21 Nov 2026, UTC):

| Stage | Time | Share |
|---|---|---|
| Preflight | 47 s | 4% |
| Tag | 4 s | |
| Wait (release.yml: build, attest, smoke, draft) | 715 s | 60% |
| Sign | 69 s | 6% |
| Publish + Feed | 28 s | 2% |
| Verify (verify-release.yml) | 330 s | 28% |
| **Total** | **1193 s (19 min 53 s)** | |

Inside release.yml (11 min 48 s): web tests 96 s, Go tests 334 s, type check, vet and the script self-test 34 s, Setup Go 39 s, build 91 s, smoke 25 s, draft 25 s. Everything in the "tests" group had already run in `ci.yml` on the same commit, which Preflight requires to be green.

Collisions found or possible:

- The old lock was an OS file lock on one PC: a second run was told "another release is running", not who or which step. It said nothing to sessions that do not run on that PC.
- The run used the shared working folder (`D:\Projects\api-audit-explorer`). On 21 Nov that folder was on another branch with uncommitted changes; the rc.55 run had to be started from a separate worktree by hand.
- `ci.yml` cancels an older master run when a newer commit lands (`cancel-in-progress`). A merge during a release's CI wait could cancel the very run the release was waiting for.
- Two sessions asking for "the next rc" at the same time both worked out the same version.

## Design

### 1. One lock for everyone: the `release-lock` branch

A branch `release-lock` on GitHub holds one file, `lock.json` (no secrets, and no machine or user name: the repository may be read by more people than the owner; `host` is a hash). It is written only by `Release.ps1`, with compare-and-swap (the contents API refuses a write whose file sha is not the one that was read; creating the branch fails if it exists). Two claims at once: one wins, the other is told it lost.

```json
{ "v": 1, "state": "held", "id": "<run id>", "host": "<12 hex, opaque id of the PC>", "pid": 1234,
  "requester": "thread-OTA", "started": "...", "heartbeat": "...", "channel": "rc", "version": "0.1.0-rc.56",
  "stage": "Wait", "commit": "<pinned sha>", "holdMerges": false, "lease_minutes": 180,
  "last": { "version": "0.1.0-rc.55", "outcome": "done", "at": "...", "requester": "..." } }
```

`state` is `held` while a run is going and `free` after (the branch stays, so the last result can be read).

- **Where to read it.** Any session: `GET repos/abhinavjp/unravis/contents/lock.json?ref=release-lock` (GitHub MCP `get_file_contents`, path `lock.json`, ref `refs/heads/release-lock`; a 404 means free). On the PC: `Release.ps1 -Status` (`-Json` for machines): the lock, `state.json`, and the last audit entries. This is the one place to look.
- **Busy answer.** A second run prints `BUSY: a release is running: <version> (<channel>), step '<stage>', started <time> by <requester> on <machine>, last sign of life N min ago. Hold merges to master: <bool>.` and exits with code 3. `-WaitMinutes N` queues behind it instead (checks every 10 to 60 s). There is no queue file: whoever claims first after a release ends goes next. Pass `-Requester <thread name>` (or set `UNRAVIS_RELEASE_REQUESTER`) so the message names you.
- **Crash safety.** Two layers: the OS lock on the PC (`release.lock`, dropped by the OS when the process dies) and the record's heartbeat on GitHub. If the OS lock is free, the record is `held` from this PC (same `host`) and its `pid` is no longer a live process that started before the record, that run crashed: the new run takes the record over (audit event `lock-acquired` with `stale`), and the normal resume (state.json) continues that release. A `held` record from another machine is stale only after its lease (180 min) with no heartbeat. The heartbeat is written at every stage change.
- **Lost lock.** Every stage first proves the record still carries this run's id. If it does not, the run stops ("lock lost").
- **Same version twice.** The version is worked out only after the lock is held, from tags fetched under the lock, and the tag push is atomic on GitHub (a tag that exists is never moved). The record names the version, so everyone sees it.
- **Nothing new.** If the newest tag of the channel already holds the commit to release and that release finished, `Release.ps1` says so and exits 0 (a second thread asking for an rc right after the first got what it wanted). If that release was withdrawn it refuses (same files would be built again). `-Again` overrides.

### 2. Master frozen: pin, hold, private worktree

- **Pin.** The bootstrap picks the commit once: the master head when its CI is green; if the head's CI is still running it waits (up to `-CiWaitMinutes`, default 45; every 5 to 30 s, backing off); if the head's CI failed it stops; with `-LastGreen` it takes the newest of the last 20 master commits with a green CI run, at once. From then on everything uses that commit, never "current master". A merge after the pin changes nothing for this release; it goes into the next one.
- **Hold merges.** `holdMerges` is `true` from the claim until the tag is pushed. Anyone about to merge to master (PR sweep, any thread) must read the lock first and wait while `state` is `held` and `holdMerges` is `true`. Reason: CI on master cancels the older run when a newer commit lands, so a merge during the CI wait can cancel the run the release waits for. This is advisory (nothing but `Release.ps1` is blocked by it); the pin and `-LastGreen` are what keep a release correct when someone merges anyway. `-CheckOnly` does not hold merges.
- **Orphans.** The run (the re-run from the worktree) holds its own `run.lock`, and only starts if the bootstrap's one-off secret (env var, matched against `lock-info.json`) is right: the lock id on GitHub is public and proves nothing. A killed bootstrap can leave its run going; a new bootstrap sees `run.lock` held and waits, and never removes a worktree that is in use. A run that finds its lock lost stops without touching `state.json`.
- **Old releases.** A bootstrap only re-runs a `Release.ps1` that carries `$ReleaseProtocol = 2`. A release tagged before this change must be finished with the old command from a checkout of its commit.
- **Printed record text.** `requester`, `stage`, `version` from the public record are cut to printable characters and a short length before they are shown.
- **Private worktree.** The bootstrap makes `%LOCALAPPDATA%\Unravis\release\work\r<time>` with `git worktree add --detach <pinned sha>` and runs `Release.ps1` again from there. So the scripts that see the passphrase are those of the pinned commit, and the shared working folder (other branch, dirty tree, half-pulled) cannot break or change a release. The worktree is removed at the end; stale ones are removed at the next start (safe: the lock is held).
- **Idempotent resume.** The resume rule (`Get-ResumePlan`) is one function used by both the bootstrap and the run. If the tag of the unfinished release is already on GitHub, the pin is that tag's commit, whatever master has become, but only if this PC's audit log says it tagged exactly that commit for that version (a tag pushed by someone else is never run: its scripts would see the passphrase).
- **Every pin is checked.** The commit must be part of `origin/master`, must contain the newest release tag (so `-LastGreen` cannot go back in history), and the bootstrap's own `Release.ps1`, `ReleaseLib.ps1` and `ProtonPass.ps1` must be byte-identical to the pinned commit's (run from an up-to-date clean checkout of master, or the run refuses). Git hooks are switched off for the run (`core.hooksPath` points at an empty folder), so a hook in the shared clone cannot run when the worktree is made.

### 3. Speed

| Change | Saves (rc.55 numbers) | Guard kept |
|---|---|---|
| `release.yml` skips web tests, type check, `go vet`, Go tests and the script self-test when the newest `ci.yml` run for the exact commit on master succeeded (same rule as `Release.ps1`); any other commit runs them all | about 7.5 min of 11.8 | the compile, packaging, attestation and smoke steps always run; nothing signed depends on the tests |
| `verify-release.yml` runs the recorder self-update test (B5) on its own runner in parallel with the rest; B5 polls every 5 s (was 20) | about 1 to 2 min | same checks, same pass rule (the run fails if either job fails) |
| Build attestations of the 5 files checked side by side (`ForEach-Object -Parallel`) | about 20 to 30 s | every file still checked on its own; any failure fails the lot |
| Merged-PR check of each commit since the feed's release runs 8 at a time | a few seconds to 20 s | same rule; no cache file (an editable cache would weaken the check) |
| Polling with backoff (5 s growing to 30 s) for CI and run discovery; `gh run watch` every 10 s | up to ~20 s per wait | none |
| CI wait happens before the claim is spent on a worktree, and `-LastGreen` skips it | the whole CI wait when master keeps moving | the pinned commit must have a green CI run |

Not changed: signing, the feed read-back, the withdraw-on-failed-verify, the clean-runner upgrade test, key and passphrase checks, the trusted-key and signing-code gates.

Expected rc: preflight about 45 s, Wait about 4.5 min, Sign about 45 s, Feed 25 s, Verify about 4 min: roughly 11 to 12 min, down from 19 min 53 s. The largest remaining items are CI itself (about 15 min on master, mostly Go tests and screenshots; not part of a release when CI is already green) and the recorder's own first update check (1 to 5 min, set by the recorder).

### 4. BL-194: which Go builds the signing tool

Preflight records two lines in the audit log (`preflight-ok`: `go_installed`, `go_used`). `go_installed` is `GOTOOLCHAIN=local go version`; `go_used` is the plain `go version` in the pinned worktree (what `go.mod` asks for). If they differ, Go fetched a toolchain and checked it against the Go checksum database (`GOTOOLCHAIN=auto`, set by `ReleaseLib.ps1`). On 21 Nov the PC had go1.27.4 and `go.mod` asks for 1.27.3, so a normal release does not switch. The fetch path itself was exercised with `GOTOOLCHAIN=go1.27.3` (see BACKLOG BL-194). The unmodified path (go.mod newer than the installed Go) is proven the first time that happens.

## Rules for other sessions

1. Before merging to `master`: read the lock. If `state` is `held` and `holdMerges` is `true`, wait and read again (every 1 to 2 minutes). A release holds merges from its claim until its tag, normally under 20 minutes, mostly CI wait.
2. To release: `pwsh ./tools/release/Release.ps1 -Channel rc -Yes -Requester <your thread name> [-WaitMinutes 30]`. Never pass `-Version` for a routine rc. Exit 0 with "Nothing new" is a success.
3. To look: `Release.ps1 -Status`. The Releases dashboard reads the same `state.json` and `audit.jsonl`.
4. Never write or delete the `release-lock` branch by hand. If a run is stuck, `-Status` says why; a crashed run is taken over by the next run on that PC by itself.

## Files and fields added

`audit.jsonl` (same hash chain): `lock-acquired` (id, requester, machine, stale, hold_merges), `pinned` (id, commit, why), `lock-released` (id, outcome); `preflight-ok` gains `go_installed`, `go_used`. `state.json` is unchanged. New on the PC: `lock-info.json` beside `release.lock` (who holds it, shown to a refused run), `work\` (the private worktree, removed at the end).

## Limits

- The lock is advisory for everything except `Release.ps1`. A token holder can write or delete the branch: that can stall releases or merges, never sign anything (signing decisions come from git, the feed and the checks, not from the lock).
- Merge holding depends on other sessions reading the lock. Without that, `-LastGreen` and the pin still give a correct release.
- Releases only run on the PC that holds the keys (`D:\secret`), so the cross-machine lease (180 min) exists for safety, not for normal use.
- The speed numbers are estimates from step timings until the next real run; the run's own timings replace them (see the rc.56 notes in BACKLOG).
