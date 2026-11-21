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
| Where to start | If the last run of this version stopped part way, it continues from the stage after the last one that finished. `-From` overrides. |

Other switches: `-CheckOnly` (all checks, including the passphrase opening the key, then stop; changes nothing), `-CheckAudit` (check the audit log chain and stop), `-SkipVerify`, `-FirstRelease`. The old form `-Version X -KeyFile K -PassphraseFromProtonPass R -Yes` still works.

## Stages

1. **Preflight** (nothing is changed)
   - tools: git, gh (signed in), go, pass-cli
   - on `master`, clean tree, same commit as `origin/master`
   - CI (`ci.yml`) on this commit: **waits** while it is queued or running (up to 45 min), stops if it failed
   - every first-parent commit since the release on the feed is a merged PR (no direct pushes)
   - version valid and newer than every tag; tag and release not used yet
   - feed: readable, its release tag is in this clone, it expires in more than 30 days (warns otherwise)
   - keys: key folder found; every key file readable and matching its `.pub`; the chosen key is for this channel and trusted by HEAD and by the feed's release; added keys in `trusted_keys.txt` since the feed's release all have a key file in the folder
   - passphrase: read from Proton Pass and **checked to open the chosen key** (`sigtool keycheck`), so a wrong entry is found before anything is tagged
   - signing code: if `tools/release/sigtool` or `internal/update` changed since the feed's release, `sigtool` may depend only on the standard library and `internal/update`
   - PowerShell transcription or module logging turned on by policy: stop (they could log the passphrase)
   - `Test-Release.ps1` passes
2. **Tag**: creates and pushes `vX`.
3. **Wait**: waits for `release.yml` (build, attestation, smoke test, draft).
4. **Sign**: `Sign-Release.ps1` with the chosen key; same gates as preflight, run again on the tag's clean worktree; attestation, hashes, sign, check against new and old keys, upload.
5. **Publish**: draft becomes public (refused without the signature).
6. **Feed**: moves the channel's feed to this version, then **reads the feed back and verifies it** (signature against the old and new keys, version is this one).
7. **Verify**: `verify-release.yml` on a clean runner. **If it fails**, the version is withdrawn at once with a signed control file (`Set-Control.ps1 -Block <version>`, keeping any pause or blocks already set) and the run stops with the reason. Machines that have not installed it are not offered it.

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
| `release.lock` | Held open while a run is going. A second run on the same PC stops at once. The OS lets go if the process dies. |

## Report

At the end (and when it stops) it prints the version, key id, manifest SHA-256, verify run link, and the PRs in this release that touched sensitive paths (`tools/release`, `internal/update`, workflows, `go.mod`/`go.sum`, `installer`).

## Not done here (by design)

- No GUI app. See the threat model: it would add a program that touches the key and passphrase and remove no threat.
- No signing in CI. The key stays on the owner's PC.
- No change to the updater runtime or feed format (owned by the OTA work).
