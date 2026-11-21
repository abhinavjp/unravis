# Releasing: the commands

Needs PowerShell 7.2+, git, go, `gh` (logged in), and your signing key files (see [KEYS.md](KEYS.md)).

## One command

```powershell
pwsh ./tools/release/Release.ps1 -Version 0.1.0-rc.52 -KeyFile D:\secret\rc-1.key.json
```

First release on a channel (nothing trusts the keys yet): add `-FirstRelease`.
Dry run (checks only, changes nothing): add `-DryRun`.

By default it stops to ask you to type: the tag, `yes` (sign), `publish`, `feed`.

**Hands-off:** add `-Yes` (rc or stable). It skips those four typed confirmations. All checks still run and any failure still stops it (CI green, build attestation, manifest and feed checks, signature, no downgrade, verify run). Two gates in Sign-Release.ps1 still ask, on purpose: signing code changed since the last release, and trusted keys changed.

With `-Yes` and `-PassphraseFromProtonPass` there is nothing to type (if pass-cli is logged out it falls back to asking for the passphrase).

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

With `-Yes` the script asks nobody anything (owner's standing approval, 21 Nov 2026). Two things replace the typed prompts, both automatic:

- **Signing code:** the diff of `tools/release/sigtool` and `internal/update` since the release now on the feed is no longer typed over. Instead the commit being signed must already be part of GitHub's `master` (`gh api compare` says behind or identical), which only takes changes through a pull request with the required CI check. A tampered signing tool that never went through master stops the run. The SHA-256 of that diff is appended to `%LOCALAPPDATA%\Unravis\release\signing-code-approvals.log` for later audit.
- **Trusted keys:** if `trusted_keys.txt` changed since the last signed release, or this is the channel's first signed release, `-Yes` refuses and says to run `Sign-Release.ps1` by hand. It never prompts.

Without `-Yes` the old typed prompts (`reviewed`, `keys ok`) still apply.
