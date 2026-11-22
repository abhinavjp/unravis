# BL-155 groom: OTA gaps before stable (G2, G3, G4, G5, G7)

Groomed 2026-11-21 against master `452f3b9`. Stable is planned Mon 23 Nov 2026.
Inputs: `/mnt/project-files/research/outputs/backlog/BL-155-ota-gaps-before-stable.md` and its prompt, `docs/auto-updater/{CONTROL,REFRESH,MIRROR,SILENT-UPDATE,THREAT-MODEL}.md`, `internal/update/*.go`, `internal/server/updater*.go`, `internal/blackbox/selfupdate.go`, the live feed (fetched read-only today).

## 1. Verdict

Most of BL-155 is already built. What is missing is mainly live proof. G2 (pause and withdraw), G3 (rollout percent), G4 (raw-GitHub backup), G5 (`Refresh-Feed.ps1`) and the G7 folder source are all on master. None of them has been proven on the live feed: **no control file is published today** (`rc-control.json` returns 404), and no refresh has been run. The G7 display (showing each Blackbox's update state in Unravis) is in open PR #71. Do not build it again.

Build two small client fixes, and land them in 0.1.0 if they can merge safely by Sunday:
- **S1:** re-check the control file just before apply.
- **S2:** keep withdrawals after the control file expires, and refuse a control file that reuses a seq with different bytes.

Why these two matter now: stable 0.1.0 has no OTA users on day one, because rc builds stay on rc and there is no `stable.json` yet. So the first stable OTA hop is 0.1.0 to 0.1.1, and that hop runs whatever updater logic ships inside 0.1.0. The client code in 0.1.0 is what counts. Feed-side work can wait.

Run the live drills (S0, S3) before or right after stable.

Leave until after stable:
- G4 second company (Cloudflare Pages).
- An expiry alarm workflow.
- The 6-hour stable cadence (D9).

Drop:
- The research's `policy-v2` / `timestamp-v2` / key-roster redesign.
- The HMAC rollout bucket.
- Fleet heartbeat files. These need a new writer in the services, so they need the owner's go.

## 2. What the research found that matters

- [SOURCE] rc.55 already has signed pause, block, expiry and per-version rollout, a persisted local draw, offline refresh, the raw-GitHub backup and Blackbox folder mode. Checked: master equals rc.55 (`45682f8`) plus 3 unrelated commits.
- [SOURCE] A remembered hold lasts only until the control file expires (max 90 days). On the network path, no live control means the upgrade becomes eligible again, so a withdrawal is not durable. Checked: `applyControl` in `internal/update/fetch.go:438-446` deletes the control and returns no hold unless `required` (folder mode).
- [SOURCE] A control file with an equal seq is accepted without comparing bytes. The manifest path does compare. Checked: `fresh.Seq >= floor` in `fetch.go:433`, while the manifest uses `SeenHash` at `fetch.go:364`.
- [SOURCE] App install uses eligibility from the last check. The Blackbox checks before staging and applies straight after, with no re-check. Checked:
  - `Install()` in `internal/server/updater_install.go:179-220` only re-hashes the file.
  - `autoStep` in `updater_auto.go:209` calls `Install()`.
  - `CheckOnce` in `internal/blackbox/selfupdate.go:298-339` goes Check, then Stage, then Apply.
  - An unreachable check keeps `u.avail` (`updater.go:268-273`), so a pause published while the PC cannot reach the feed is missed until the manifest expires.
- [SOURCE] Raw-GitHub backup answers are capped to "held" and never start an install. Checked: `fetch.go:262-268`.
- [INFERRED] Keep REST out of the polling path. Already true: the code only uses release-download URLs, and `update_check.go` has no `api.github.com`. The research's note that the "local update check uses REST" (its source 80) describes an older checkout, so the research is wrong here.
- [INFERRED] Keep the integer percent and the local draw for the pilot. Skip the HMAC bucket. With a fleet of a few machines, percentages mean little anyway.
- [INFERRED] Prove the offline refresh before relying on it, and add expiry monitoring that does not depend on the publisher. Today only release preflight warns, at 30 days (THREAT-MODEL row 7).
- [INFERRED] Cloudflare Pages is the free second company for metadata. It needs a new allowlist entry baked into the client.
- [INFERRED] Keep fleet status read-only, from existing folder or share sources, and never let it carry update authority.

## 3. Current state in code

**Control file (G2/G3)**
- `internal/update/control.go`: schema, `Validate`, strict `ParseControl`, `VerifyControl` (key role equals channel), `Hold` (pause, then blocked, then rollout), `newDraw`.
- `internal/update/fetch.go`: `fetchControl` (only when the offered version is an upgrade) and `applyControl`. The control seq floor is never lowered.
- `State` has `Control`, `ControlSeq`, `Draw` and `DrawFor`.
- Missing:
  - durable blocked list
  - control hash for the equal seq
  - re-check before apply

**Refresh (G5)**
- `tools/release/Refresh-Feed.ps1` and `sigtool refresh` exist.
- Live rc feed: seq 3101, `published` 2026-11-21, `expires` 2027-05-20. Nothing expires before May 2027.
- No expiry alarm outside the release preflight.

**Backup feed (G4)**
- `DefaultMirrorBases` points at raw.githubusercontent `feed-mirror`. `allowURL` pins the exact path.
- The `mirror-feed` workflow ran successfully on 2026-11-21 21:43Z. Today `rc.json` and `rc.json.sig` on the backup are byte-identical to the primary (sha256 `939e7a…`, `bc9fe8…`).
- No client read from the backup has been observed.
- No second company yet.

**G7**
- `ReadUpdateSource` and `Fetcher.Folder` exist. Folder mode requires a live control (`required=true`).
- The Blackbox reports its update state at `/status` (`internal/blackbox/server.go:65,392`, `SelfUpdater.Status`).
- Unravis reads that state only in open PR #71: `internal/server/blackbox.go`, `BlackboxPanel.svelte`, plus 1 line in `selfupdate.go`.

**Cadence**
- `updateDefaultMinutes = 30` (`internal/server/updater.go:22`) and `DefaultUpdateMinutes = 30` (`internal/blackbox/selfupdate.go:40`).
- SPEC D9 says 6 hours for stable. Not built.

**Live feed**
- `update-feed` holds only `rc.json` and `.sig`.
- No `stable.json` and no control files.

## 4. Scope (slices)

| ID | Goal | Files | Size | Before stable? |
|---|---|---|---|---|
| S0 | Live control drill, no code: publish an empty `rc-control.json`, then a short rc `-Pause`, prove a client holds, then lift. After the first stable release, publish an empty `stable-control.json` too. | `tools/release/Set-Control.ps1` (run only). A throwaway Go test harness kept outside the repo or under the scratchpad. | S | Yes. This is the only proof G2 has. |
| S1 | Re-check before apply. App: `Install()` runs a fresh `fetcher.Check` and launches only if the result is `Available` with `sameSetup` as the downloaded manifest. Held, Latest, Unreachable or Rejected means no launch, and the state goes to idle or held with the plain reason. Blackbox: after `Stage` and before the pending marker, run `u.Check` again and require `Available` with the same version and the same Blackbox asset. | `internal/server/updater_install.go`, `internal/server/updater_auto.go` (try counter: a refused re-check is not a try), `internal/blackbox/selfupdate.go`, tests. Docs: `SILENT-UPDATE.md` (new failure row), `CONTROL.md`. | S/M (~150 to 250 lines with tests) | Should. It ships in the 0.1.0 client. |
| S2 | Control hardening. (a) Store the hash of the accepted control per channel, and refuse an equal seq with different bytes (as the manifest does). (b) Keep `Blocked` from the newest accepted control after it expires. Pause and rollout still expire. A newer control replaces the list, which is how a block is lifted. | `internal/update/fetch.go` (`State`, `seenMem`, `applyControl`), `internal/update/control.go`, `control_test.go`, `fetch_test.go`, `CONTROL.md` ("Limits" and "what it can do"). | S (~120 to 200 lines) | Should. It ships in the 0.1.0 client. |
| S3 | Live refresh drill: run `Refresh-Feed.ps1 -Channel rc`, check that the new pair has seq 3102, the same version, hashes and commit, a new expiry, and that a client accepts it. Then tick G5. | none (run only). The harness from S0. | S | Nice before stable. It is safe because nothing new is installed. |
| S4 | Live backup read: harness with the primary forced unreachable reads the backup and returns held. | none (harness only) | S | Nice. |
| S5 | Expiry alarm: a daily CI workflow reads `rc.json`, `stable.json` and the control files from both hosts, verifies them with `go run` against the checkout's keys, and fails the run at less than 30 days to expiry or on any mismatch between hosts. A failed run emails the owner. | `.github/workflows/feed-watch.yml`, a small `tools/release/feedwatch` Go cmd (or a `sigtool` subcommand) | S/M | After stable. Expiry is May 2027. |
| S6 | Stable cadence D9: default check interval of 6 hours for stable builds, 30 minutes for rc, in the app and the Blackbox. | `internal/server/updater.go`, `internal/blackbox/selfupdate.go`, tests | S | Owner call. If wanted for 0.1.0 it must land before stable, because the default is baked into the client. |
| S7 | G4 second company: Cloudflare Pages direct-upload mirror of the feed files, plus an exact host and path allowlist entry in `allowURL`, plus a mirror publish step. | `internal/update/fetch.go`, `.github/workflows/mirror-feed.yml`, `MIRROR.md` | M | After stable. Needs an owner Cloudflare account. |

## 5. Risks and unknowns

- **Security (S1):** a check while the PC is offline must fail closed for auto-apply. If "Apply now" also needs a reachable feed, a PC on a cut-off network cannot apply a file it already downloaded. The offline import path (`OFFLINE.md`) stays the route there. SPEC needs to decide: recommend fail closed for both, and say so in the bar.
- **Security (S2):** a durable block that never expires could only be lifted by a newer signed control. That is intended. Cap at the existing 32 entries. Old rc builds (before control support) ignore all of this; that is the owner's rc machines only.
- **Rollback:** S1 and S2 only add refusals. The worst case is "update waits", never a bad install. A bug that refuses everything would freeze OTA for 0.1.0 machines with no OTA fix. That is why S1 and S2 need a passing live harness before merge (section 7).
- **Timing:** two days to stable, and both client fixes touch the OTA-owned files. If they cannot be reviewed by Sun 22 Nov, ship 0.1.0 without them. The current 0.1.0 logic still honours pause, block and rollout while a control file is live. Land them in 0.1.1, and keep the control file live, re-published within 90 days, until then.
- **Footprint:** S1 adds one small manifest fetch plus one control fetch per apply (about 2 KB). That is negligible.
- **Fleet:** S6 cuts stable polling 12-fold. With raw-GitHub plus 30 minutes, GitHub rate limits are not a real risk at today's fleet size.
- **Windows-only:** the silent apply, UAC and service apply paths are covered by Windows CI only. The re-check logic itself runs the same on any OS.
- **Unknown:** whether any PC other than the owner's runs an rc build older than the control support. No telemetry exists, by design.
- **Data loss:** none. The state file grows by one hash and one list per channel, well under the 64 KiB cap.

## 6. Acceptance per slice

**S0**
- `rc-control.json` and `.sig` are on `update-feed` and the backup, and both verify with the checkout's rc keys.
- A Fetcher with `Current=0.1.0-rc.54` against the live feed returns `Held` with "paused" while the pause is live, and `Available` after the lift.
- The owner's PC Help > Updates shows the held line, if run on an older rc.

**S1**
- With a downloaded, ready file and a control that turns to pause or block between check and apply, `Install()` does not call the launcher. The status shows the hold reason. `auto-tries.json` is not bumped.
- Unreachable at re-check: no launch for auto. The next tick retries.
- Blackbox: a pause seen at re-check means no pending marker, no `StartVerified`, and state "held".
- Normal path is unchanged: Available at re-check means launch, as before.

**S2**
- An equal-seq control with different bytes is refused, and the old one stays.
- After the last control with `blocked:[X]` expires and no new file can be read, X is still held on the network path.
- A newer control without X lifts it.
- Pause and rollout still end at expiry.
- An existing `state.json` from rc.55 loads without error.

**S3**
- Live `rc.json` seq is higher than 3101, with the same version, assets and commit, and a later `expires`.
- The harness Fetcher (`Current=rc.54`) returns `Available` for rc.55.
- An rc.55 client returns `Latest` with no rejection.

**S4**
- With the primary host refused by the harness `HTTP` transport, the result is `Latest` and `Held` with the "main update address could not be reached" detail.

**S5**
- The workflow fails on a feed copy that expires within 30 days, and on a byte mismatch between hosts.
- It passes on today's feed.

**S6**
- A stable build's default interval is 6 hours, an rc build's is 30 minutes.
- A saved user interval still wins.

**S7**
- The client reads a Pages copy only after primary and raw-GitHub are unreachable.
- Its answers are capped to held.
- No other host on `pages.dev` is allowed.

## 7. LIVE-PATH CHECK per slice

**S0**
- (a) Linux: the worker fetches the live files and runs a harness test using `update.TrustedKeys()` and the real `DefaultFeedBase`, after the owner publishes. Real feed, real keys, nothing fake.
- (b) Owner's PC: `Set-Control.ps1` needs `D:\secret` keys and Proton Pass. Optional: the hold shown in the UI on a PC with an older rc.
- (c) Nothing impossible.

**S1**
- (a) Linux: unit tests, plus the same harness wired to a real `Updater` with a stub launcher against the live feed during the S0 pause window. That proves the real control is read at re-check.
- (b) Owner's PC: one real silent apply rc.N to rc.N+1 with a pause published between download and idle apply, proving no setup starts. One Blackbox service on a server, or the owner's PC, doing the same.
- (c) We cannot prove behaviour on third-party PCs. There is no telemetry, by design.

**S2**
- (a) Linux: unit tests plus a harness with a real expired-control state file (copy the owner's `state.json` shape) against the live feed.
- (b) Owner's PC: load the existing rc.55 `state.json` after upgrade, and check that nothing is set aside as corrupt.
- (c) Waiting a real 90-day expiry is impractical. The tests use `Now`.

**S3**
- (a) Linux: verify the new pair against the keys and the harness accept.
- (b) The refresh run itself needs the owner's PC.
- (c) none.

**S4**
- (a) Fully on Linux against the real backup host.
- (b) Optional: an office network that blocks github.com.
- (c) none.

**S5**
- (a) Linux: run the cmd locally against the live feed, then the workflow on GitHub.
- (b) none.
- (c) none.

**S6**
- (a) Unit tests.
- (b) Install stable 0.1.0 and read the interval in Help > Updates.
- (c) none.

**S7**
- (a) Linux: the harness against a real Pages URL once it exists.
- (b) A Cloudflare account and its token: owner.
- (c) none.

## 8. Needs a new collector or agent?

- S0 to S7: no. S5 is a CI check, not a collector.
- Fleet heartbeat files (research "Fleet status display" items 1 to 4: every service writes `status.json` to a share) would add a new writer in the Blackbox services. Under the CLAUDE.md owner rule ("anything that needs a new collector or agent" is deferred), and the BACKLOG rule "**Deferred** means the owner said 'not now' ... Do not build a deferred item without the owner's go", this needs the owner's go. Not proposed here.
- PR #71 reads the existing `/status` and adds no writer.

## 9. Dependencies and file-collision warnings

- **S1** touches `internal/server/updater_install.go`, `updater_auto.go` and `internal/blackbox/selfupdate.go`. All are owned by the OTA thread. PR #71 also edits `selfupdate.go` (+1 line), so rebase after #71.
- **S2** and **S7** touch `internal/update/fetch.go` (OTA thread). PR #72 adds `ReadState` at the end of `fetch.go` and reads `State`. Adding fields is compatible, but merge #72 first or coordinate. #72's dashboard may want to show the new durable-block field.
- **S6** touches `internal/server/updater.go` and `selfupdate.go` (OTA thread, #71).
- PR #70 (BL-183) is closed and touched only `internal/setup`, so there is no conflict.
- **S0, S3, S4** are run-only on the live feed. Coordinate with the OTA thread and the release manager. A pause on rc blocks any rc release verify running at the same time (`Release.ps1` withdraw logic, THREAT-MODEL row 8). Do not run S0 during a release.
- `docs/BACKLOG.md` is edited by #70, #71 and #72. The ticking slice should rebase.

## 10. Recommended order and handoff to SPEC

1. **S0** (owner runs `Set-Control`, worker proves with the harness). Today or Sun.
2. **S2**, then **S1**: hand both to SPEC now as one spec with two PRs. Gate: merged and live-harness green by Sun 22 Nov evening, or they move to 0.1.1.
3. **S6**: ask the owner the D9 question now (6 h stable default in 0.1.0: yes or no).
4. **S3** and **S4**: any time. Safe. Tick G5 and G4 (GitHub-side) in BACKLOG when green.
5. After stable: S5, then S7 only if a site shows GitHub blocked.

SPEC next: S2 + S1 ("pre-apply re-check and durable withdrawals"), with the S0 harness as its live test.

**Drop, with reasons**
- `policy-v2` / `timestamp-v2` / key-epoch roster: a big format change. The current offline control file covers a solo-owner fleet. Revisit only if a 90-day offline re-sign becomes a burden.
- HMAC per-install bucket and basis points: the persisted draw already gives stable answers without tracking. Percent granularity does not matter at this fleet size.
- R2, B2, Netlify, jsDelivr payload mirrors: no requirement for installs during a GitHub outage.
- Fleet heartbeat files: they need a new writer in the services, so they are deferred until the owner says go.
- Code signing: not part of BL-155. The research ranks SignPath first, then OSSign, then Certum €49. Track it under its own item, and do not tie stable to it.
