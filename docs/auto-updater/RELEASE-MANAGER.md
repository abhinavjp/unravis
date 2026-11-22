# Release manager

One command makes a whole release with nobody typing anything. It checks everything first, works out keys and versions by itself, says exactly what is wrong when something is, and cleans up after a failure. Threats it answers: [THREAT-MODEL.md](THREAT-MODEL.md).

## The command

```powershell
pwsh ./tools/release/Release.ps1 -Channel rc -Yes
```

That is all. It picks:

| What | How |
|---|---|
| Version | Next one after the newest tag: `0.1.0-rc.54` → `0.1.0-rc.55`. Stable: the `X.Y.Z` of the newest rc. `-Version` overrides; it must be newer than every tag. |
| Key | From `-KeyDir` (default `D:\secret`): the first `<channel>-N.key.json` whose public key is trusted by both the commit being released and the release now on the feed. `-KeyFile` overrides. |
| Passphrase | The Proton Pass entry that belongs to that key (table in [RELEASING.md](RELEASING.md)). `-PassphraseFromProtonPass` overrides. |
| Where to start | If the last run stopped part way (and was not withdrawn), it continues that version from the stage after the last one that finished, with the same key it checked. A tag is only made right after a preflight in the same run. `-From` overrides. |

Many sessions can ask at once: one release at a time (lock on GitHub that says who and which step), the commit is pinned and built from a private worktree, and a second request for the same commit is a no-op. Details, the busy answer and the rules for other sessions: [RELEASE-COORDINATION.md](RELEASE-COORDINATION.md). `-Status` shows where everything is. `-WaitMinutes N` queues behind a running release. `-LastGreen` releases the newest green master commit without waiting for CI on the head. `-Requester` names you in the lock.

Other switches: `-CheckOnly` (all checks, including the passphrase opening the key, then stop; changes nothing), `-CheckAudit` (check the audit log chain and stop), `-SkipVerify`, `-FirstRelease`. The old form `-Version X -KeyFile K -PassphraseFromProtonPass R -Yes` still works.

## Stages

0. **Bootstrap** (before the numbered stages): claims the lock (PC and GitHub), picks the commit (master head when its CI is green, waits for CI, or `-LastGreen`), makes a private worktree of it and runs the stages from there.
1. **Preflight** (nothing is changed)
   - tools: git, gh (signed in), go, pass-cli
   - the worktree is clean, at the pinned commit, and that commit is still part of `origin/master`
   - CI (`ci.yml`) on this commit: green (the bootstrap already waited for it; a check-only run waits here, up to 45 min), stops if it failed
   - every first-parent commit since the release on the feed is a merged PR (no direct pushes). With the release train ([../ci/RELEASE-TRAIN.md](../ci/RELEASE-TRAIN.md)) master's commit is the promotion PR's merge commit; the release manager then also checks its develop side: every develop commit in it must be a PR merged into develop, or the one "Merge master into promote/..." commit, which must redo cleanly with git's own merge (same tree) and whose master parent is already on master. The one generated commit of a promotion ("Rebuild web files for promotion ...", changing only `web/dist`, `cmd/unravis-setup/ui` and `cmd/unravis-lite/ui`, see `docs/pipeline/dist-on-promotion.md`) is accepted as well, case-sensitively and only at the tip end of the promote branch (above every develop pull request); the same subject touching any other file, or buried under a develop commit, is not. A direct push to develop stops the run. The sensitive-paths report names the develop PRs, not only the promotion. A promotion is a PR into master whose head is `develop` or `promote/...`; it must be merged as a merge commit (what `Promote-Develop.ps1` does): a squashed or rebased promotion stops the run, since its develop commits can no longer be checked. Also refused: a promote branch updated with "rebase" or with conflicts fixed in GitHub's web editor (fix conflicts on develop by PR instead). Other PRs into master are checked as before, whatever merge style. Master must stay the repository's default branch: GitHub only maps a commit to its merged PR on the default branch.
   - version valid and newer than every tag; tag and release not used yet
   - feed: readable, its release tag is in this clone, it expires in more than 30 days (warns otherwise)
   - keys: key folder found; every key file readable and matching its `.pub`; the chosen key is for this channel and trusted by HEAD and by the feed's release; added keys in `trusted_keys.txt` since the feed's release all have a key file in the folder
   - passphrase: read from Proton Pass and **checked to open the chosen key** (`sigtool keycheck`), so a wrong entry is found before anything is tagged
   - signing code: if `tools/release/sigtool` or `internal/update` changed since the feed's release, `sigtool` may depend only on the standard library, `internal/update` and `golang.org/x/sys/windows` (pinned in go.mod)
   - PowerShell transcription or module logging turned on by policy: stop (they could log the passphrase)
   - `Test-Release.ps1` passes
2. **Tag**: creates and pushes `vX`.
3. **Wait**: waits for `release.yml` (build, attestation, smoke test, draft).
4. **Sign**: `Sign-Release.ps1` with the chosen key; same gates as preflight, run again on the tag's clean worktree; attestation, hashes, sign, check against new and old keys, upload.
5. **Publish**: draft becomes public (refused without the signature).
6. **Feed**: moves the channel's feed to this version (Sign-Release checks the signature against the old and new keys first), then **reads the feed back** and checks it is byte for byte the signed pair of this release.
7. **Verify**: `verify-release.yml` on a clean runner, found by a one-off id in its run name. **If it fails, or cannot start or be found**, the version is withdrawn at once with a signed control file (`Set-Control.ps1 -Block <version>`, keeping any pause or blocks already set, signed with a key the previous release trusts) and the run stops with the reason. A withdrawn version is finished: the next run makes a newer one. Machines that have not installed it are not offered it.

## Key problems it finds by itself

Each one stops the run with one line naming the key and the cause:

- key folder missing (USB stick not plugged in)
- no key file for the channel
- key file damaged, not a key file, or does not match its `.pub`
- key is for the other channel
- key not in `trusted_keys.txt` of the commit (the build would refuse its own update)
- key not trusted by the release on the feed (installed copies would refuse it); the next key in the folder is tried first
- Proton Pass missing, logged out, locked, slow, entry missing, or not one line
- passphrase from Proton Pass does not open the key (wrong entry); the next usable key is tried first
- a key was added to `trusted_keys.txt` that has no key file in the folder (someone else's key)

## No prompts under -Yes

With `-Yes` no script in `tools/release` waits for typed input. Where a person used to type a word, a rule decides (see the threat model, "What replaces human review"). If the rule cannot decide, the run stops and says why. The passphrase never falls back to a hidden prompt under `-Yes`.

## Files it keeps (`%LOCALAPPDATA%\Unravis\release`)

| File | What |
|---|---|
| `audit.jsonl` | One JSON line per event: run start, preflight result, tag, build run, signing gates (diff hash, keys added or removed), signed manifest SHA-256 and key id, publish, feed, feed read-back, verify, withdraw, stop reason. Each line holds the SHA-256 of the line before (`prev`), so an edit breaks the chain; `-CheckAudit` finds it. No secrets: field names with pass, secret or token are refused. |
| `state.json` | Version, last stage that finished, running / stopped / done, reason. A Claude session reads this to report or continue. |
| `release.lock` | Held open while a run is going. A second run on the same PC stops at once. The OS lets go if the process dies. `lock-info.json` beside it says who holds it. |
| `work\` | The private worktree of the pinned commit the run executes from. Removed at the end. |
| GitHub branch `release-lock` | `lock.json`: who holds the release (or the last result), version, step, pinned commit, whether merges to master should wait. Readable by any session. See [RELEASE-COORDINATION.md](RELEASE-COORDINATION.md). |

## Report

At the end (and when it stops) it prints the version, key id, manifest SHA-256, verify run link, and the PRs in this release that touched sensitive paths (`tools/release`, `internal/update`, workflows, `go.mod`/`go.sum`, `installer`).

## Limits to know

- A withdraw is a control file that lives 90 days at most (the feed still points at the withdrawn version). Release a fixed version before then. A later `Set-Control.ps1` run replaces the whole control file, so name the withdrawn version again with `-Block` when you run it. `Refresh-Feed.ps1` refuses to re-sign a withdrawn version.
- If verify did not pass and the withdraw itself failed, state says `withdraw-pending`; the next run only retries the withdraw.
- Go is run with `GOFLAGS`, `GOWORK`, `GOENV` cleared or off and `GOTOOLCHAIN=auto` (Go's default), so settings outside git cannot change what the signing tool is built from. Every script builds it in a throw-away worktree of the commit being used.
- PowerShell 7 logging policies (both Windows PowerShell and PowerShellCore keys, and `powershell.config.json`) are checked before every passphrase read.

## Not done here (by design)

- No GUI app. See the threat model: it would add a program that touches the key and passphrase and remove no threat.
- No signing in CI. The key stays on the owner's PC.
- No change to the updater runtime or feed format (owned by the OTA work).
