# Releasing: the commands

Needs PowerShell 7.2+, git, go, `gh` (logged in), and your signing key files (see [KEYS.md](KEYS.md)).

## One command (release manager)

```powershell
pwsh ./tools/release/Release.ps1 -Channel rc -Yes
```

Nothing to type. It works out the next version, picks the key from `D:\secret`, takes that key's passphrase from Proton Pass (table below), checks that the passphrase opens the key before anything is tagged, waits for CI, signs, publishes, moves the feed, reads the feed back, verifies on a clean runner, and withdraws the version by itself if that verify fails. If it stops, it says exactly why; run the same command again and it continues where it stopped. Full spec: [RELEASE-MANAGER.md](RELEASE-MANAGER.md). Threats: [THREAT-MODEL.md](THREAT-MODEL.md).

- Stable: `-Channel stable` (version = the X.Y.Z of the newest rc), or `-Version 0.1.0`.
- Checks only, changes nothing: `-CheckOnly` (old name `-DryRun`).
- Audit log check: `-CheckAudit` (log in `%LOCALAPPDATA%\Unravis\release\audit.jsonl`).
- Keys elsewhere (USB stick): `-KeyDir E:\keys`. One key: `-KeyFile`. Other Proton Pass entry: `-PassphraseFromProtonPass`.
- First release on a channel: add `-FirstRelease`.
- Without `-Yes` it asks you to type: the tag, `yes` (sign), `publish`, `feed`, and `reviewed` / `keys ok` when those changed.

The old form still works: `Release.ps1 -Version 0.1.0-rc.52 -KeyFile D:\secret\rc-1.key.json -PassphraseFromProtonPass '...' -Yes`.

## Read the passphrase from Proton Pass (optional)

Add `-PassphraseFromProtonPass 'pass://Vault/Item/password'`.

- Needs the Proton Pass CLI (`pass-cli`), logged in. It is on paid Proton Pass plans only.
- Read only at the signing step. Held in memory. Never written to disk, an environment variable, a command line, a log or CI.
- If `pass-cli` is missing, logged out, locked, slow (30 s), or returns anything but one clean line, it says so and you type the passphrase instead. It never signs with a guess.
- The typed confirmations stay.
- Check the reference first (prints only yes/no and the length):

```powershell
pwsh ./tools/release/Test-ProtonPass.ps1 -Reference 'pass://Vault/Item/password'
```

- Install and login: https://protonpass.github.io/pass-cli/ (`pass-cli login`, check with `pass-cli info`).
- Not verified on a real account yet: the exact output of `pass-cli item view <pass:// URI>` (the check above tells you).

### The references for the "Unravis Passphrases" item (vault "Work")

| Key | Reference |
|---|---|
| rc-1 | `pass://Work/Unravis Passphrases/rc.1 passphrase` |
| rc-2 | `pass://Work/Unravis Passphrases/rc.2 passphrase` |
| stable-1 | `pass://Work/Unravis Passphrases/stable-1 passphrase` |
| stable-2 | `pass://Work/Unravis Passphrases/stable-2 passphrase` |

Put the whole reference in quotes (it has spaces). Use the reference that matches `-KeyFile` (rc-1 key with the rc.1 field). A wrong one just fails to sign; nothing is uploaded.

```powershell
pwsh ./tools/release/Release.ps1 -Version 0.1.0-rc.52 -KeyFile D:\secret\rc-1.key.json -PassphraseFromProtonPass 'pass://Work/Unravis Passphrases/rc.1 passphrase' -Yes
```
(Add `-FirstRelease` only for the first release of a channel.)

## Steps, if you want them one at a time

`-From Preflight|Tag|Wait|Sign|Publish|Feed|Verify` resumes at a stage.

## Hands-off signing (-Yes) and what still protects the key

With `-Yes` no script in `tools/release` waits for typed input (owner's standing approval, 21 Nov 2026). Fixed rules replace the typed words (code in `tools/release/ReleaseLib.ps1`, `Invoke-SigningGates`; Sign-Release, Set-Control and Refresh-Feed all use it):

- **Always:** the commit being signed must be on GitHub's `master`, every first-parent commit since the release on the feed must be a merged pull request, and `sigtool` may depend only on the Go standard library and `internal/update`. The signing tool is built in a throw-away worktree.
- **Signing code** (all of `tools/release`, `internal/update`) changed: its diff SHA-256 goes to the audit log.
- **Trusted keys** changed (or a channel's first release): every key ADDED must have its private key file (`*.key.json`, same role and public key) in the key folder and open with its own Proton Pass entry. A key nobody here holds stops the run and is named. Removed keys only narrow trust and are logged.
- **Passphrase**: only from Proton Pass. If pass-cli fails, the run stops with the reason; it never falls back to a hidden prompt.

Without `-Yes` the old typed prompts (`reviewed`, `keys ok`, `yes`) still apply.
