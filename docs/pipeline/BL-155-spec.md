# BL-155 spec: durable withdrawals, re-check before apply, stable cadence, live harness

Spec for slices S2, S1, S6 and the S0/S3/S4 live harness of `BL-155-groom.md`. Written 2026-11-21 against master `452f3b9` (= rc.55 `45682f8` + 3 unrelated commits). Every code claim below was checked at the file:line given.

Owner decision S6 (6 h stable default in 0.1.0): **yes**, by nachi's standing rule.

## 0. Groom claims, checked

| Groom claim | Check | Result |
|---|---|---|
| Expired control is deleted, no hold on network path | `internal/update/fetch.go:438-446` (`if !ctlLive(cur, now) { delete(st.Control, want); if required {...}; return false, "" }`) | true |
| Equal control seq accepted without byte compare | `fetch.go:433` `fresh.Seq >= floor`; manifest compares `SeenHash` at `fetch.go:364` | true |
| `fetchControl` returns nil on any problem (404, unreachable, bad sig, expired) | `fetch.go:396-410`; `VerifyControl` refuses expired files via `Validate` at `control.go:68-70` | true |
| Control is fetched only when the offered release is an upgrade | `fetch.go:333-339` | true |
| `Install()` only re-hashes the file | `internal/server/updater_install.go:179-220` (`u.dl.Recheck` at :202, launch at :211) | true |
| `autoStep` calls `Install()`, un-bumps tries only for 4 errors | `internal/server/updater_auto.go:205-214` | true |
| An unreachable check keeps `u.avail` | `internal/server/updater.go:268-273` (only Available/Latest/Dev touch `avail`) | true |
| Blackbox goes Check, Stage, marker, Apply with no re-check | `internal/blackbox/selfupdate.go:298-339` | true |
| Backup answers are capped to held | `fetch.go:259-270` | true |
| Default interval 30 min, app and Blackbox | `internal/server/updater.go:22`, `internal/blackbox/selfupdate.go:40`, used at `selfupdate.go:139` and `:413` | true; also `internal/server/update_check.go:109` and `updater.go:75,101,114` |
| Live feed today | fetched read-only 2026-11-21: `rc.json` 200 (rc.55, seq 3101, expires 2027-05-20), `rc-control.json` 404 on both hosts, `stable.json` 404 | true |

Two extra findings the groom missed:
- `Set-Control.ps1 -KeepInForce` drops the withdrawn list of an **expired** control (`tools/release/Set-Control.ps1:122-128`, else branch "nothing to keep from it"). With S2, clients keep that list after expiry, so the next Set-Control run (including `Release.ps1`'s automatic withdraw, which uses `-KeepInForce`) would silently lift those blocks. S2 fixes the script too (section 2.6).
- `TestAvailableSurvivesAFailedCheck` (`internal/server/updater_install_test.go:227-247`) asserts that Install **launches** after a failed check. S1 (fail closed) reverses that assertion. This is an intended behaviour change, not a regression.
- The Downloader reuses a verified file already on disk (`internal/update/download.go:124-126`), so a hold that drops the download state does not cause a second download after the lift.

## 1. Conflicts with open PRs #71 and #72 (checked on GitHub)

- **#71** (G7 display): `internal/blackbox/selfupdate.go` one line, the `State` comment at :162. Adds `internal/server/blackbox.go` `bbUpdateStates` allowlist: `idle, managed, available, held, skipped, downloading, installing, failed, off`. Also `docs/BACKLOG.md` line 123.
  - Rule: S1 and S6 must **not add any new Blackbox state word**. A new word would be dropped by #71's allowlist and the recorder line would vanish in Unravis. S1 uses only `held` and `idle`.
  - No textual overlap: S1 edits `selfupdate.go:289-340`, S6 edits `:36-41`, `:122-142`, `:413`. #71 edits `:162`. Rebase after #71 merges; expect a clean rebase.
- **#72** (releases dashboard): appends `ReadState` at the end of `fetch.go` (after `:668`) and reads `State.Control[ch]` (live only) in `internal/releasedash/snapshot.go`.
  - Rule: S2 adds fields inside `State` and code inside `applyControl`/`loadState`/`fetchControl`/`seenMem`. Do **not** append anything at the end of `fetch.go`. Then the rebase is clean either way.
  - `ReadState` uses plain `json.Unmarshal`, so the new fields load with no change. The dashboard will not show kept blocks; a follow-up may show `KeptBlocked` (new BACKLOG line, not built here).
- **BACKLOG.md**: #70 (closed), #71, #72 edit line 123. These PRs do not touch BACKLOG.md. One last small commit adds the BL-155 lines after #71/#72 merge, and ticks only what the live drill proved.
- Merge order: #71 and #72 first if they are green; else these PRs go first and #71/#72 rebase (also clean).

## 2. S2: control hardening (PR A)

### 2.1 New state fields (`internal/update/fetch.go`, `State` at :46-58)

```go
// ControlHash is, per channel, the SHA-256 (hex) of the exact bytes of the control file accepted at ControlSeq.
// Kept after that file expires, like ControlSeq, so the same seq with other bytes is refused.
ControlHash map[string]string `json:"controlHash,omitempty"`
// KeptBlocked is, per channel, the withdrawn list of the newest accepted control file. It does not end when that file
// expires: only a newer accepted control file replaces it. Pause and rollout still end at expiry.
KeptBlocked map[string][]string `json:"keptBlocked,omitempty"`
```

`seenMem` (:113-121) gets `ctlHash map[string]string` and `kept map[string][]string`, created with the others at :422.
`loadState` (:572) copies `s.ControlHash, s.KeptBlocked` too. Size: 64 hex chars + at most 32 versions per channel, under 1.5 KB per channel; the 64 KiB cap (`maxStateSize`, :37) is safe.

### 2.2 Backward and downgrade compatibility

- rc.55 file read by new code: both new keys absent, so the maps are nil and `loadState` treats them as empty. No set-aside. Migration rules in 2.3 fill them on the next check.
- New file read by rc.55: rc.55 `loadState` (:566) uses non-strict `json.Unmarshal` into its own `State`; unknown keys are ignored. No set-aside. rc.55's next `saveState` drops the two keys. Effect after a downgrade then upgrade: the hash is learned again (2.3 rule c), and `KeptBlocked` comes back only from a live control. Acceptable: a rollback version is also in the skip list (`Tracker`).
- #72 `ReadState`: same non-strict decode, no change.
- Folder mode (`required=true`): unchanged; still holds with no live control.

### 2.3 New `applyControl` rules (`fetch.go:415-462`)

`fetchControl` (:396) returns `(*Control, string)`: the control and `hex(sha256(body))` of the exact verified bytes. `once1` (:335-339, :383) passes the hash on. `applyControl` gets a `freshHash string` parameter.

Let `floor = max(st.ControlSeq, mem.ctlSeq, cur.Seq)` as today, and `known = st.ControlHash[want]`, else `mem.ctlHash[want]`.

1. `fresh == nil`: nothing new (missing, 404, unreachable, bad signature, expired file, wrong channel).
2. `fresh.Seq < floor`: ignored (replay). Unchanged.
3. `fresh.Seq > floor`: accepted. `cur = fresh`, `floor = fresh.Seq`, `ControlHash = freshHash`.
4. `fresh.Seq == floor`:
   a. `known != ""` and `known == freshHash`: accepted (same file again). No change.
   b. `known != ""` and `known != freshHash`: **refused**. `cur` stays the remembered one. No message in the result (a control problem never blocks by itself; same as a bad file).
   c. `known == ""` (rc.55 state): if a remembered `cur` with that seq exists, compare `json.Marshal(cur)` with `json.Marshal(fresh)` (ParseControl already proves the bytes equal the canonical form by value, `control.go:116-129`). Equal: accept and record `freshHash`. Different: refuse as in b. No remembered `cur`: accept and record (first sight).
5. Write `st.ControlSeq`, `mem.ctlSeq`, `st.ControlHash`, `mem.ctlHash` (hash never dropped, like the seq).
6. If `cur` is live: `KeptBlocked[want] = copy(cur.Blocked)` (delete the key when empty), in state and memory. This also migrates an rc.55 state that holds a live control.
7. If `cur` is not live: delete `st.Control[want]` as today. Then:
   - `required` (folder): hold with the folder message, as today.
   - version in `KeptBlocked[want]` (state, else memory): hold, why = `"version X was withdrawn"`.
   - else no hold.
8. If `cur` is live: `cur.Hold(...)` as today (pause, blocked, rollout). Unchanged.

State machine per channel (control part):

```
NONE --accept(seq>floor)--> LIVE(seq,hash,kept=blocked)
LIVE --expiry--> EXPIRED(seq,hash,kept)        pause/rollout end; kept still holds those exact versions
LIVE|EXPIRED --accept(seq>floor)--> LIVE(new seq, new hash, kept=new blocked)   the only way a block is lifted
LIVE|EXPIRED --seq<floor or (seq==floor, other bytes)--> unchanged
```

### 2.4 How a block is lifted

Only by a newer signed control file (seq above the floor) whose `blocked` list leaves the version out: `Set-Control.ps1 -Channel rc -KeyFile ...` with no switches, or naming the blocks to keep. A newer release is never blocked by an old list (exact version match only, `control.go:196-200`), so a stale block never holds the next version.

### 2.5 Failure table, S2 (network path; "kept" = KeptBlocked)

| Case | Result |
|---|---|
| Control 404 / unreachable, remembered live | remembered holds (as today) |
| Control 404, remembered expired, version in kept | **held, "withdrawn"** (new) |
| Control 404, remembered expired, version not in kept | no hold |
| Control invalid (bad sig, strict parse, wrong role/channel, expired file) | same as 404 |
| Seq regression | ignored, remembered/kept apply |
| Equal seq, same bytes | accepted, no change |
| Equal seq, different bytes | **refused** (new), remembered/kept apply |
| Expired control, no blocked | no hold (pause and rollout end) |
| Expired control with blocked | blocked versions still held (new) |
| Pause lifted by newer control | no hold; kept replaced by its list |
| State file lost mid-run | memory keeps seq, hash, kept |
| Folder, no live control | held (as today) |

### 2.6 Set-Control.ps1 fix (same PR)

`tools/release/Set-Control.ps1:122-128`: with `-KeepInForce`, keep `$old.blocked` even when the file on the feed has expired (keep `pause` only when live). Print "The control file on the feed has expired; keeping its withdrawn versions: ...". The signature is still checked first (`controlsig`, :115). Add a case to `tools/release/Test-ReleaseManager.ps1` if it covers `-KeepInForce` (check first); else note it as owner-PC verified.

Doc: `docs/auto-updater/CONTROL.md` "How a machine uses it" step 4 and "Limits": withdrawn versions are kept after expiry until a newer control file; the same seq with other bytes is refused; to lift a block publish a newer file without it.

### 2.7 Tests, PR A (`internal/update`)

Helpers exist: `newFeed`, `fs.publish`, `fs.publishControl`, `goodControl`, `goodManifest`, `key`, `restart` (`fetch_test.go:29-67,463`, `control_test.go:14-24`).

| Test | Input | Expected |
|---|---|---|
| `TestControlEqualSeqDifferentBytesRefused` | ctl seq 100 no pause, Check; then other bytes seq 100 `Pause:true`; then reverse order in a fresh fetcher | first: Available both times, `ControlHash` unchanged; reverse: held both times |
| `TestControlEqualSeqSameBytesAccepted` | same file twice | Available, no state change besides normal saves |
| `TestControlEqualSeqRc55State` | write rc.55-shaped `state.json` (live `control` seq 100, `controlSeq` 100, no `controlHash`); serve value-equal file seq 100, then a value-different file seq 100 | first accepted and hash recorded; second refused |
| `TestControlBlockedSurvivesExpiry` | manifest rc.48 expires +300 d; ctl seq 100 `Blocked:[rc.48]` expires +10 d; Check; remove control files; `Now=+11 d`; Check; `restart()` and new Fetcher on same file; Check | held "withdrawn" all three times |
| `TestControlPauseAndRolloutStillExpire` | ctl pause expires +10 d; then ctl rollout rc.48 1% with draw forced ≥1; `Now` past expiry, files removed | Available after expiry in both |
| `TestControlNewerFileLiftsKeptBlock` | after the expiry above, ctl seq 200 `Blocked:[]`; then seq 300 `Blocked:[rc.47]` | Available; `KeptBlocked["rc"]` gone, then `[rc.47]`, rc.48 still Available |
| `TestControlKeptBlockOnlyExactVersion` | kept `[rc.48]` expired; feed offers rc.49 | Available |
| `TestControlKeptBlockSurvivesLostStateFile` | as Survives, then `os.Remove(StatePath)` before the last Check | held |
| `TestKeptBlockMigratesFromRc55LiveControl` | rc.55 state with live control `blocked:[rc.48]`, no `keptBlocked`, no control on feed; Check; `Now` past expiry; Check | held both; saved file has `keptBlocked` |
| `TestStateFileRc55LoadsAndNewLoadsInRc55Shape` | fixture JSON of rc.55 shape (`seenSeq, seenHash, control, drawFor, draw, controlSeq`); Check; then decode saved file into a local copy of the rc.55 struct with `json.Unmarshal` | no `.corrupt-*` file; decode ok; seq/hash kept |
| existing `TestFolderFeedIsVerifiedLikeTheInternetOne`, `TestControl*`, `TestMirror*` | unchanged | still pass |

## 3. S1: re-check right before apply (PR B)

### 3.1 App (`internal/server/updater_install.go`, `updater_auto.go`)

New errors (plain words, shown by `updateStep` as 409, `update_check.go:225-229`):
- `errRecheckHeld`: wraps the detail: "The publisher has put this update on hold: <detail>. Nothing was started."
- `errRecheckOffline`: "Could not reach the update feed to confirm this update is still safe. Nothing was started; it will try again."
- `errRecheckRefused`: "The update feed's answer did not pass its checks, so this update was not started."

`Install()` keeps its signature (`update_check.go:211` uses the method value). New order, after the busy flag is set (:196-199) and **before** `Recheck` (:202):

1. `ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)`; `res, ok := u.checkOnce(ctx)` (takes `checkMu`, updates `u.last`, `u.avail`, calls `noteCheck`; no lock is held by Install at this point, so no deadlock).
2. `!ok` (timeout): `errRecheckOffline`.
3. `res.Status == Available && res.Manifest != nil && sameSetup(res.Manifest, m)`: `m = res.Manifest`; continue to `Recheck` and launch as today.
4. `Available`, other setup (newer version): `noteCheck` already dropped the download; return `errNotReady`.
5. `Latest && Held` (pause, withdrawn, rollout, or backup-only answer): return `errRecheckHeld`. `checkOnce` set `avail=nil`, `noteCheck(nil)` cleared the install state. The verified file stays on disk and is reused after the lift.
6. `Latest`, not held (skipped version): `errNoUpdate`.
7. `Unreachable`: `errRecheckOffline`. `avail` and the ready download stay.
8. `Rejected`: `errRecheckRefused`. `avail` and the download stay until the manifest's own expiry.

Both the person's "Apply now" and the automatic apply fail closed. The offline route stays `OFFLINE.md` (import).

`autoStep` (:205-214): add the three new errors to the un-bump list (a refused re-check is not a try). New `autoState` fields `rcFails int`, `rcNext time.Time`:
- on `errRecheckOffline` or `errRecheckRefused`: `rcFails++`, `rcNext = now + dlBackoff(rcFails)` (5 min, 15, 60, then 6 h);
- at the `ready` branch (:163), before the idle test: if `now < rcNext`, return (no network call every minute);
- on launch or `errRecheckHeld`: reset both.

### 3.2 Blackbox (`internal/blackbox/selfupdate.go` `CheckOnce` :290-340)

After `Stage` succeeds (:320-325) and **before** writing `pending.json` (:326):

```
res2 := u.Check(ctx)
if ctx.Err() != nil { return res2 }
ok := res2.Status == Available && res2.Manifest != nil && res2.Manifest.Version == v &&
      res2.Manifest.Assets[AssetBlackbox] == res.Manifest.Assets[AssetBlackbox] &&
      strings.EqualFold(res2.Manifest.BlackboxExeSHA256, res.Manifest.BlackboxExeSHA256)
```

- `res2.Held`: `set("held", v, "A newer version is on hold: "+res2.Detail+".")`, return `res2`.
- other not ok: `set("idle", "", "Could not confirm that version "+v+" is still safe to install ("+resultSentence(res2)+"). It will try again.")`, return `res2` (its `Backoff` drives `Run`'s back-off, :381-386).
- ok: marker, `installing`, `Apply`, as today.

No new state words (see section 1). Staged files stay; `Stage` re-creates the stage folder next time and the zip is reused.

### 3.3 Failure table, S1

| At re-check | App auto | App "Apply now" | Blackbox |
|---|---|---|---|
| Available, same file | launch | launch | marker + apply |
| Offline / feed 5xx / 403 | no launch, not a try, back-off 5→15→60→360 min, stays ready | 409 offline text, stays ready | no marker, `idle` + "could not confirm", loop backs off |
| Control 404 or invalid | rules of 2.5 (remembered or kept), else launch | same | same |
| Paused / withdrawn / rollout-out | no launch, not a try, bar shows held line | 409 held text | no marker, `held` |
| Seq regression or equal-seq other bytes (control) | ignored; remembered/kept decide | same | same |
| Manifest rollback / bad sig (Rejected) | no launch, back-off, stays ready until manifest expiry | 409 refused text | `idle`, no marker |
| Newer version appeared | old download dropped, new download next tick | 409 not ready | `idle`; next loop stages the new one |
| Expired control, no kept block | launch | launch | apply |
| Expired control, version kept-blocked | held (needs PR A) | held | held |
| Pause lifted | next check Available, file reused (no second download), launch | launch | apply |
| Only backup host reachable | held ("main address could not be reached") | 409 held | `held` |

### 3.4 Tests, PR B

App (`internal/server`; helpers `newInstallRig`, `readyDownload`, `autoRig`, `fakeFeed`; add `rg.publishControl(c update.Control)` that signs into `/rc-control.json` and `.sig`):

| Test | Input | Expected |
|---|---|---|
| `TestInstallRechecksAndRefusesAPause` | `readyDownload`; publish ctl `Pause:true`; POST install | 409, `launch==0`, `Status().Held` contains "paused", `Install==nil` |
| `TestInstallRechecksAndRefusesAWithdrawal` | ctl `Blocked:[rc.48]` | 409, `launch==0`, held contains "withdrawn" |
| `TestInstallRefusedWhenFeedUnreachableAtRecheck` | `feed.code=503` after ready | 409 "could not reach", `launch==0`, status available + ready; then `feed.code=0`, install 200, `launch==1` |
| `TestInstallRefusedWhenRecheckRejected` | bad `/rc.json.sig` after ready, cache cleared by new body | 409, `launch==0`, still ready |
| `TestInstallRunsAFreshCheck` | count `feed.hits` around install | hits grew (manifest + control) and `launch==1` |
| `TestInstallAfterHoldLiftedReusesTheFile` | pause, install refused; ctl seq higher no pause; `CheckNow`; download | ready with no request for the setup path; install `launch==1` |
| `TestAutoStepRecheckHeldIsNotATry` | `autoRig`; pause; idle; `autoStep` | applied 0, `autoTries==0`, no `auto-tries.json` |
| `TestAutoStepRecheckOfflineBacksOff` | `autoRig`; 503; `autoStep` twice | one re-check only (hits), tries 0; set `rcNext` past, `autoStep` re-checks again |
| `TestAvailableSurvivesAFailedCheck` (changed) | as today | status stays available/ready; **install now 409 and `launch==0`** |
| existing `TestInstallFlowDownloadThenInstall`, `TestAutoStep*`, `TestDoubleInstallStartsOnce`, `TestInstallCanBePressedAgainAfterLaunched` | unchanged feed | still pass |

Blackbox (`internal/blackbox/selfupdate_test.go`; `suRig` gets `res2 *update.Result` returned from the second `Check`):

| Test | Second Check | Expected |
|---|---|---|
| `TestInstallModeStagesAppliesAndWritesTheMarker` (changed) | same Available | `checks==2`, applied, marker |
| `TestRecheckHeldAfterStageStartsNothing` | Latest, Held, "updates are paused" | stages 1, applies 0, no marker, state `held` |
| `TestRecheckUnreachableAfterStageStartsNothing` | Unreachable, Backoff | applies 0, no marker, state `idle`, result has "confirm", returned `Backoff==true` |
| `TestRecheckOtherReleaseStartsNothing` | Available 1.3.0; and 1.2.0 with other `BlackboxExeSHA256` | applies 0, no marker, `idle` |

Docs: `docs/auto-updater/SILENT-UPDATE.md` failure table gets "feed unreachable or holding at the moment of apply: nothing starts; retried later"; `CONTROL.md` step 5: the hold is checked again right before apply, in the app and the Blackbox.

## 4. S6: 6 h stable default (PR C)

- `internal/server/updater.go:21-26`: replace `updateDefaultMinutes` with `updateDefaultMinutesRC = 30`, `updateDefaultMinutesStable = 360`, `func defaultUpdateMinutes(channel string) int` (stable → 360, else 30).
- `NewUpdater` (:72-88): set `u.channel` first, then `settings.IntervalMinutes = defaultUpdateMinutes(u.channel)`. `loadSettings` (:101) and `cleanUpdaterSettings` (:112-120) take the default from the channel (make `cleanUpdaterSettings(s, def int)`). `update_check.go:109` (dev) uses the rc default.
- A saved, valid `intervalMinutes` in `settings.json` still wins. Note: `SetSettings` saves the whole struct, so anyone who once changed a setting keeps the interval shown then. Stable 0.1.0 starts with no settings file (rc stays rc), so it gets 360.
- `internal/blackbox/selfupdate.go`: add `DefaultStableUpdateMinutes = 360`; `func (c Config) UpdateIntervalFor(channel string) time.Duration` (configured value, else 360 for stable, else 30); `UpdateInterval()` stays and equals `UpdateIntervalFor(ChannelRC)` for its callers; `NewServiceUpdater` (:413) uses `cfg.UpdateIntervalFor(update.ChannelOf(v))`. Fix the comments at `updater.go:31` and `selfupdate.go:122`. 360 is within `MaxUpdateMinutes` (1440).
- UI: `web/src/lib/updateCheck.ts:92` already has "Every 6 hours" (360). No web change. `Glossary.svelte:55` `?? 30` is a pre-load fallback only.
- Back-off: `NextDelay` caps at `max(6 h, interval)` (`fetch.go:618-637`), so a 6 h interval never backs off past 6 h.
- Docs: `SPEC.md` D9 marked built; Blackbox config doc lines that say "every 30 minutes" (grep `docs/` for it).

Tests:

| Test | Input | Expected |
|---|---|---|
| `TestDefaultIntervalFollowsTheChannel` | `setVersion("0.1.0")`, `NewUpdater`; then `"0.1.0-rc.55"` | 360; 30 |
| `TestSavedIntervalWinsOverStableDefault` | `settings.json` `{"auto":true,"intervalMinutes":30,"applyAuto":true}`, version 0.1.0; and `intervalMinutes:0` | 30; 360 |
| `TestSetSettingsZeroUsesChannelDefault` | stable, `SetSettings{IntervalMinutes:0}` | 360 saved |
| `TestBlackboxUpdateIntervalForChannel` | `Config{}` stable/rc; `AutoUpdate{IntervalMinutes:60}` | 6 h / 30 min; 60 min both |
| existing `selfupdate_test.go:205,223` | unchanged | pass |

## 5. Live harness (S0, S3, S4; also S1 and S2 live)

Go tests, skipped unless `UNRAVIS_LIVE_FEED=1` (CI never sets it). Real `update.TrustedKeys()`, real `DefaultFeedBase` and `DefaultMirrorBases` (`Base` left empty), state in `t.TempDir()`. Never a real launcher, never a real service apply.

Files (each in the PR whose code it proves):
- `internal/update/live_feed_test.go` (PR A): `TestLiveFeed`. Env:
  - `UNRAVIS_LIVE_CURRENT` (default `0.1.0-rc.54`), `UNRAVIS_LIVE_WANT` (default `rc`)
  - `UNRAVIS_LIVE_EXPECT` = `available` | `held` | `latest`; `UNRAVIS_LIVE_DETAIL` = substring the detail must hold (e.g. `paused`)
  - `UNRAVIS_LIVE_PRIMARY=off`: `f.HTTP` with a transport that fails every request to `github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com` (proxy from env kept), so only the backup is read (S4)
  - `UNRAVIS_LIVE_NEED_CONTROL=1`: after the check, the saved state must have `controlSeq[want] > 0` and a `controlHash` (proves the live control was read and verified)
  - `UNRAVIS_LIVE_STATE=<path>`: copy that file (for example the owner's `state.json`) into the temp dir first, never write the original; assert no `.corrupt-*` file after
  - `UNRAVIS_LIVE_SCENARIO=kept-block`: write a state with an expired control `blocked:[<offered version>]`, `controlSeq` = `1<<40 - 1` (above any live seq, so the live file counts as a replay) and the matching `keptBlocked`; expect held "withdrawn" from the real feed
  - Logs status, version, manifest seq, detail, held, control seq.
- `internal/server/updater_live_test.go` (PR B): `TestLiveRecheckBeforeApply`. `setVersion(UNRAVIS_LIVE_CURRENT)`, `NewUpdater(tmp, TrustedKeys())`, `EnableInstall(tmp2, stub)` (stub counts calls, starts nothing), `CheckNow`, `StartDownload`, wait `ready` (real setup download, tens of MB). Then logs "READY: publish the pause now" and polls a second Fetcher every 30 s (max 30 min) until held. Then `Install()`: expect `errRecheckHeld`, stub calls 0. With `UNRAVIS_LIVE_EXPECT=available` it skips the wait and expects 1 stub call.
- `internal/blackbox/selfupdate_live_test.go` (PR B): `TestLiveBlackboxRecheck`. `NewServiceUpdater(Config{}, current, <tmp>/unravis-blackbox.exe, t.Logf)`, replace `Apply` with a stub, wrap `Stage` so that after the real stage it waits (same poll) for the pause. Expect state `held`, no `pending.json`, stub 0.

Run on Linux worker (proxy env is already set; if TLS fails, `SSL_CERT_FILE=/root/.ccr/ca-bundle.crt`). `L='UNRAVIS_LIVE_FEED=1'`, `-count=1 -v`.

Not during a release run (a pause on rc stops `Release.ps1` verify, THREAT-MODEL row 8). Tell the OTA thread and the release manager first.

1. Baseline: `$L UNRAVIS_LIVE_EXPECT=available go test ./internal/update -run TestLiveFeed` → Available rc.55.
2. S4 backup: `$L UNRAVIS_LIVE_PRIMARY=off UNRAVIS_LIVE_EXPECT=latest UNRAVIS_LIVE_DETAIL='main update address' ...` → Latest, Held, backup detail.
3. Owner (PC, `D:\secret`, Proton Pass): `./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json -Days 30` (empty control). Then run the `mirror-feed` workflow (or wait for it).
4. `$L UNRAVIS_LIVE_EXPECT=available UNRAVIS_LIVE_NEED_CONTROL=1 ...` → Available, control seq set. Repeat with `UNRAVIS_LIVE_PRIMARY=off` once the mirror has the file.
5. Start in background: `$L go test ./internal/server -run TestLiveRecheckBeforeApply -timeout 40m` and `$L go test ./internal/blackbox -run TestLiveBlackboxRecheck -timeout 40m`. Wait for both "READY".
6. Owner: `Set-Control.ps1 -Channel rc -KeyFile ... -Pause -Days 1 -Note 'BL-155 drill'`.
7. `$L UNRAVIS_LIVE_EXPECT=held UNRAVIS_LIVE_DETAIL=paused ...` → held. Step 5 tests pass (no launch, no marker).
8. S2 live: `$L UNRAVIS_LIVE_SCENARIO=kept-block UNRAVIS_LIVE_EXPECT=held UNRAVIS_LIVE_DETAIL=withdrawn ...`; and `UNRAVIS_LIVE_STATE=<copy of owner's state.json>` with `EXPECT=held` (paused) → no set-aside.
9. Owner lifts: `Set-Control.ps1 -Channel rc -KeyFile ...` (no switches). `$L UNRAVIS_LIVE_EXPECT=available ...` → Available; `UNRAVIS_LIVE_EXPECT=available go test ./internal/server -run TestLiveRecheckBeforeApply` → 1 stub launch.
10. S3 (any time after): owner runs `Refresh-Feed.ps1 -Channel rc`; `$L UNRAVIS_LIVE_EXPECT=available` (seq > 3101, same version) and `$L UNRAVIS_LIVE_CURRENT=0.1.0-rc.55 UNRAVIS_LIVE_EXPECT=latest` (no rejection).

Not provable live: equal seq with other bytes (needs the private key to sign twice; unit only); a real 90-day expiry (unit with `Now`; step 8 covers the shape).

## 6. Footprint

- S2: no extra request. State grows < 1.5 KB per channel.
- S1: one extra check per apply attempt: manifest GET (usually 304 with the cached ETag, then no `.sig` GET) + control `.json` and `.sig` GETs. About 3 requests, ~2 KB. Offline re-check backs off 5/15/60/360 min, so no per-minute calls. Blackbox: same 3 requests per apply.
- S6: stable machines drop from 48 to 4 checks a day (12-fold).
- Harness: manual only, never in CI.

## 7. Security

- **Mirror or MITM, downgrade**: no. Manifest seq floor and hash (`fetch.go:361-366`) and control seq floor are unchanged; backup answers never start an install (`fetch.go:262-268`). S1 re-check runs the same checks.
- **Mirror or MITM, freeze**: an attacker who can block the network can delay updates, as today (it can block checks). S1 does not add a new power; it removes one: before, a PC that could not reach the feed applied a release the owner had since paused. A backup-only answer is held, so cutting github.com stops applies until it is back (intended fail closed).
- **Mirror serving an old signed control**: refused by the seq floor. Serving the newest one only once and then hiding it: the remembered file (and now kept blocks) still hold.
- **Two signed files with the same seq** (two `Set-Control` runs in the same second; seq = Unix seconds, `sigtool/main.go:132`): before, a mirror could flip between them; now the first one seen wins on each PC.
- **Abusing the durable block to brick**: needs a channel key (no key, no accepted control). It holds only the listed exact versions, never a newer release. Worst case with a stolen rc key: block current versions and set seq to `maxControlSeq` (2^40), so the owner cannot publish a higher control. Recovery: ship a new release (not in the list, flows), and rotate the key (KEYS.md). This seq-ceiling risk already exists for pause replays today; not new.
- **Owner recovery from a wrong block**: only a newer signed control without the version (2.4). Set-Control fix (2.6) stops an accidental lift.
- **S1 bug that refuses everything**: freezes OTA on 0.1.0 machines with no OTA fix (they need a manual setup run). Mitigation: the stable gate (section 10).
- **Fail closed for "Apply now"**: a person on a cut-off network cannot apply a downloaded file. Route: OFFLINE.md import. Decided: fail closed for both.

## 8. PR split

| PR | Slice | Files | ~Lines | Can merge without |
|---|---|---|---|---|
| A | S2 + harness `TestLiveFeed` + Set-Control fix | `internal/update/fetch.go`, `control_test.go` (or new `control_durable_test.go`), `live_feed_test.go`, `tools/release/Set-Control.ps1`, `docs/auto-updater/CONTROL.md` | ~80 code, ~200 tests | B, C, #71, #72 (rebase on #72: no overlap if nothing is appended at the end of fetch.go) |
| B | S1 + live re-check tests | `internal/server/updater_install.go`, `updater_auto.go`, `updater_install_test.go`, `updater_auto_test.go`, `updater_live_test.go`, `internal/blackbox/selfupdate.go` (:290-340 only), `selfupdate_test.go`, `selfupdate_live_test.go`, `docs/auto-updater/SILENT-UPDATE.md`, `CONTROL.md` | ~90 code, ~250 tests | A (works on rc.55 control logic; the "kept-block" row needs A), C; rebase after #71 |
| C | S6 | `internal/server/updater.go`, `update_check.go`, `updater_test.go`, `internal/blackbox/selfupdate.go` (:36-41, :122-142, :413), `selfupdate_test.go`, `docs/auto-updater/SPEC.md` | ~30 code, ~60 tests | A, B; rebase after #71 |

Order: A, then B, then C. Each needs its own fresh reviewer. BACKLOG.md lines in one last small commit after the drill.

## 9. Windows-only parts

The new logic (re-check, control rules, intervals) is OS-neutral and is covered by `go test ./...` on `windows-2022` in `ci.yml:118` and on Linux. Windows-only paths these changes sit in front of, which need nachi's PC:
- silent setup start and `--expect-sha256` (`internal/update/launch_windows.go`), UAC test `canSilently`;
- Blackbox service apply via `StartVerified` + `update` command, rollback, `pending.json` reconcile;
- the real `state.json` under `%LOCALAPPDATA%` and `<blackbox>\updates\` loading after upgrade (no `.corrupt-*`).

Owner PC checks: (1) rc.55 → rc.56 (new code) OTA; state file not set aside. (2) rc.56 → rc.57 silent idle apply (re-check passes, setup runs). (3) Optional: pause between download and idle apply on rc.56 → no setup starts, Help > Updates shows the held line. (4) One Blackbox service on rc.56 doing the same (needs a PC without the managed marker, or `update-source.txt`).

## 10. Stable gate (0.1.0 with A, B, C)

Must be green before 0.1.0 is tagged:
1. A, B, C merged; CI green on `windows-2022` (full `go test ./...` and the e2e job).
2. Live harness steps 1-9 green on the real feed with merged master.
3. rc.56 released from that master; owner PC: rc.55 → rc.56 OTA works, `state.json` kept.
4. rc.57 released (refresh-only content is fine); owner PC: rc.56 → rc.57 **silent apply** works. This is the only live proof of the new apply path that 0.1.0 will use for 0.1.0 → 0.1.1.
5. Right after 0.1.0 is on the feed: owner publishes an empty `stable-control.json` (S0); then `$L UNRAVIS_LIVE_WANT=stable UNRAVIS_LIVE_CURRENT=0.0.1 UNRAVIS_LIVE_EXPECT=available UNRAVIS_LIVE_NEED_CONTROL=1 go test ./internal/update -run TestLiveFeed` (the harness sets `Build` from `ChannelOf(CURRENT)`, so this is a stable build reading stable keys). Not a pre-tag gate, but due the same day.

Fallback if 1-4 are not green by Sun 22 Nov evening:
- Ship 0.1.0 from master without B. Without A too, unless A's live step 8 passed (A only adds refusals and has no Windows path).
- C may ship alone if its unit tests pass (it only changes a default; low risk), but note: a 6 h default makes the missing S1 re-check matter more (staler holds). If B is not in, prefer leaving C out too.
- Keep a control file live on both channels and re-publish within 90 days (Set-Control with `-KeepInForce`) until 0.1.1 carries A and B.
- Say plainly in the release notes and BACKLOG what is and is not proven live.
