# Release pipeline threat model

Scope: everything between a commit on `master` and an installed copy of Unravis or Blackbox taking an update. Written 21 Nov 2026 for the release manager (`tools/release/Release.ps1`, spec in [RELEASE-MANAGER.md](RELEASE-MANAGER.md)).

## What we protect

| Asset | Why it matters |
|---|---|
| Private signing keys (`rc-1`, `rc-2`, `stable-1`, `stable-2` `.key.json` in `D:\secret`) | Whoever holds a key and its passphrase can push any code to every installed copy on that channel. |
| Key passphrases (Proton Pass, vault "Work") | Turn a stolen key file into a working key. |
| `internal/update/trusted_keys.txt` | Decides which keys future builds trust. Adding a key is as good as stealing one. |
| Signing code (`tools/release/sigtool`, `internal/update`) | Runs on the owner's PC with the passphrase in memory. |
| Build (`.github/workflows/release.yml`, actions, Go toolchain) | Produces the files the owner signs. |
| Feed (`update-feed` release: `rc.json`, `stable.json`, `.sig`, control files) | Tells every copy what to install. |
| Audit trail | Lets us find out later what was signed, when, with which key, and from which code. |

## Who might attack

- **Automated agents with merge rights.** Many Claude sessions build and merge PRs under the owner's standing approval. A prompt injection (from a PR comment, a web page, a file) or a plain mistake can land bad code on `master`. This is the most likely source of harm.
- **Someone with a stolen GitHub token** (the owner's `gh` login, or an app token). Can edit releases, upload files, start workflows, maybe push.
- **Malware on the owner's PC** (DESKTOP-QQDVSD1).
- **A compromised third-party action or toolchain** used by CI.
- **Network attacker** between a copy of Unravis and GitHub.

## Threats, ranked

Rank = likelihood x impact today, after the controls already built. 1 is the worst.

| # | Threat | Likelihood | Impact | Controls in place | Controls the release manager adds | Left over |
|---|---|---|---|---|---|---|
| 1 | **Malicious or broken PR merged to master** (build, signing code, updater, workflows). No human reads every diff. | High | Critical | CI must pass; fresh-context review per PR; signing tool built from the tag in a clean worktree. | Under `-Yes`: every commit that touches a sensitive path since the last signed release must belong to a merged PR (named in the report and audit log). `sigtool` may only depend on the standard library and `internal/update`. The final report lists every sensitive-path PR in the release so the owner sees it. Preflight also checks that every first-parent commit since the release on the feed is the merge of a PR, so a direct push with a stolen token stops the run (the commit must be the PR's own merge commit; checked live on master since rc.53). A token that can push to master can still fake this with a PR of its own: branch rules (BL-197) are the real stop. | **High.** The pipeline signs what master holds. Real protection here is review quality and staged rollout. See "What replaces human review". |
| 2 | **Trusted-key list changed** to add an attacker's key. | Medium (rides on #1) | Critical | Old flow: typed `keys ok`. PR #54: `-Yes` refuses any change. | Automatic decision: an added key is accepted only if its private key file (`*.key.json`, with matching role and public key) is in the owner's key folder. Removed keys are allowed and logged. A key that is in no owner file stops the run and is named. | Low. An attacker would need write access to `D:\secret`, which is #4. |
| 3 | **Stolen GitHub token swaps release files** (draft assets, feed files, control file). | Medium | High | Build attestation checked before signing (`gh attestation verify`, workflow + tag + commit). Files re-hashed against the manifest. Clients verify Ed25519 signatures with built-in keys, check seq and refuse downgrades. | Feed read back and verified after upload. Audit log records manifest hash, so a later swap is visible. | Low for code execution (token cannot sign). Medium for denial of service: a token can delete releases or the feed. Clients keep running the version they have. |
| 4 | **Malware on the owner's PC** reads the key file and the passphrase (an unlocked `pass-cli` session, a keylogger, memory). | Low | Critical | Keys encrypted (PBKDF2 600k + AES-GCM). Passphrase never on disk, argv, env or logs. | Preflight refuses if PowerShell transcription or module logging is turned on by policy (they could record the passphrase pipe). Key folder can be a removable drive (`-KeyDir`). | **Medium.** A fully compromised PC can sign anything. Key rotation (`-2` keys) and the control file pause are the recovery path. Moving the key folder to a USB stick that is plugged in only for releases lowers exposure (supported by `-KeyDir`). |
| 5 | **Tampered signing tool on the PC** (edited working folder, swapped `sigtool.exe`). | Low | Critical | Tool built fresh from the tag's commit in a throw-away worktree; tag must equal GitHub's. | Release refuses if the tag is not on GitHub's master. Dependency allowlist for `sigtool`. | Low. A changed Go toolchain on the PC is #4. |
| 6 | **CI compromised** (third-party action, runner) builds bad files that carry a valid attestation. | Low | Critical | Actions pinned by full SHA. CI holds no signing key. Smoke test on two runners. | None new. | Medium. Fix is a local reproducible rebuild and hash compare (backlog BL-195). |
| 7 | **Rollback or freeze**: attacker replays an old signed manifest, or the feed expires. | Low | Medium | `seq` must rise; clients refuse a lower version; manifests expire; `Refresh-Feed.ps1` re-signs before expiry; control file expires. | Preflight warns when the feed expires within 30 days. | Low. |
| 8 | **Bad release reaches the fleet** (honest bug). | Medium | High | Verify run on a clean machine; control file can pause or withdraw. | If the verify run fails after the feed moved, the release manager withdraws the version itself (signed control file `-Block`) and says so. | Low. |
| 9 | **Passphrase leaks** through a prompt, a log, argv, env, a crash dump, an error message. | Low | High | Passphrase only from Proton Pass into a SecureString, then to `sigtool` on stdin. Env var cleared. pass-cli errors dropped. | Under `-Yes` the scripts never fall back to `Read-Host` for the passphrase (that stalled a window on 21 Nov); they stop with the cause. Audit log has no secrets by construction (fixed field list). | Low. PowerShell holds a plain string for the length of one `sigtool` call. |
| 10 | **Half-finished release** (crash, closed window, network drop). | Medium | Low | Stages are resumable with `-From`. | Lock file stops two runs at once. State file says the last stage that finished, so the next run continues by itself. Failure text names the exact next command. | Low. |
| 11 | **Audit log edited** to hide a signing. | Low | Medium | None before. | Hash-chained JSON lines: each line carries the SHA-256 of the line before. `-CheckAudit` reports the first broken line. | Medium: whoever owns the PC can rewrite the whole chain. Copies of the hashes live in the GitHub release (signed manifest hash is public). |
| 12 | **Agent runs the release with wrong arguments** (wrong key, wrong channel, version skip). | Medium | Low | Key role checked by `sigtool sign`. | Version is worked out from tags if not given; a given version must be the next one. Key and Proton Pass entry chosen by the script from the channel and checked against the trusted lists before tagging. | Low. |

## What replaces human review

The owner chose "human near the loop, not in it". Nobody types `reviewed` any more. What stands in its place:

1. **Mechanical gates** (all automatic, all fail closed): code on GitHub master, every sensitive commit in a merged PR, CI green, build attested, files hashed, key list explained by the owner's own key files, signing tool dependencies limited.
2. **Independent review per PR**: a fresh, no-context reviewer for each PR. This is a process rule, not something the script can prove.
3. **Visibility**: the release report and audit log name every PR that touched sensitive paths. The owner can read them after the fact.
4. **Blast-radius limits**: rc channel first, then stable; control file pause and withdraw; staged rollout percent.

We considered a signed allowlist of reviewed commit ranges. It adds no protection here: the same automation that would run the release would also sign the allowlist with the same Proton Pass access. It only helps if a person signs it with a separate key, which is the human step the owner removed.

## Why scripts, not a GUI app

A separate release app was considered. It would be one more program that touches the passphrase and the key, one more thing to build, sign and keep trusted, and it would still have to call `gh`, `git`, `go` and `sigtool`. It removes no threat in the table above. The gain from a GUI is comfort, and the owner does not run releases by hand. So: one PowerShell entry point, hardened, with a machine-readable state file that a Claude session (or a future small status page) can read.

## Open items

- BL-195: reproducible local rebuild and hash compare against CI output (threat 6).
- BL-196: keep the key folder on a removable drive, plugged in only for releases (threat 4). Supported by `-KeyDir`; owner's choice.
- BL-197: GitHub branch rules that require a passing CI check before merge on `master` (cannot be read from this session; owner to confirm in repo settings).
