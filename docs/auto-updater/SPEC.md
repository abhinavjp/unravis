# Auto-updater for Unravis and Blackbox: spec

Status: DRAFT for nachi's confirmation. No code written. 2026-11-20.
Rule: nothing below is built until nachi confirms (see "Open decisions", section 12).

## 1. Goal

Unravis and the Blackbox recorder can find a newer release, download it safely, install it, and roll back if it fails. Cases, saved connections and recordings are never lost.

Order of goals: **security first**, then footprint (CPU, memory, network ~ zero), then convenience.

## 2. What exists today (read from the repo)

| Piece | State |
|---|---|
| Release pipeline (`release.yml`) | Tag `vX.Y.Z[-rc.N]` builds a DRAFT release: setup exe, app zip, Blackbox zip, `SHA256SUMS.txt`. A person presses Publish. Never auto-publishes. |
| Signing | None yet (SignPath planned, A48). Releases are unsigned. |
| Update check | Manual button only (`internal/server/update_check.go`, decision A52: no background calls). Calls `releases/latest`. **Ignores pre-releases**, so an rc build is never offered a newer rc. Link only, no download. |
| Installer `unravis-setup.exe` | Already does upgrade in place: asks to close the app, stops services, replaces program files, keeps data and sealed secrets, refuses downgrade (`--allow-downgrade`), journal + rollback, keeps old files until the new version starts once. UI runs unelevated; a short-lived elevated worker does admin steps. |
| Blackbox pin (SEC-FIX-01) | The app embeds the SHA-256 of the exact Blackbox exe it ships with, and refuses any other. |
| BL-136 | Designed and built, **not run live**: app-driven Blackbox `update` op (one admin approval, hash check, `.old` rollback). |
| Keeper service | SYSTEM helper holding the sealing key. |

So most of the install, upgrade and rollback engine exists. The gap is: **finding, downloading and trusting** a release.

## 3. How others do it (from general knowledge; not live-fetched, re-check before build)

| Tool | Idea | Take |
|---|---|---|
| Squirrel.Windows | Per-user install, background check, swap folders, delta packages | Per-user swap is simple, but cannot touch services/admin. No built-in signature check beyond Authenticode. |
| Velopack (Squirrel successor) | Same, plus delta updates, update.exe, channels, rollback to last version | Good model for channels and "keep last good". Deltas not worth it for us. |
| WinSparkle / Sparkle | Feed (appcast) signed with **EdDSA (ed25519)**; public key inside the app | **Copy this.** Trust comes from a key in the app, not from the download host. |
| Chrome / Edge / Dropbox | Separate SYSTEM "elevation/update service" installs updates without UAC | Powerful, bigger attack surface. Later option only (section 8). |
| WinSW and service wrappers | Do **not** self-update. Update = stop service, replace exe, start | Service exe can't replace itself while running. Needs an outside actor. Our actor = installer / app op (BL-136). |
| go-selfupdate / minio selfupdate | Download, verify checksum, rename over the running exe | Fine for plain apps. Not for a service under Program Files. |
| TUF (The Update Framework) | Signed metadata with expiry, version, threshold keys, key rotation | Take the ideas: expiry, monotonic version, two keys. Skip the full framework. |
| winget / Chocolatey | Hash in a manifest, optional publisher signature | Hash from same host as file is weak. |

Key lesson: **a checksum next to the download proves nothing** if the attacker controls the release (stolen GitHub account, bad token). Trust must come from a key held outside GitHub.

## 4. Recommended approach (one line)

**Signed manifest + reuse `unravis-setup.exe`.** The app checks a small ed25519-signed manifest, downloads the installer, verifies it, and hands it to the existing upgrade flow. Blackbox updates ride along with Unravis (it is bundled). No new install engine.

## 5. Release side (CI + one manual step)

Add one more release asset, `update-manifest.json`, plus `update-manifest.json.sig` (ed25519, detached).

Manifest (signed):

```json
{
  "schema": 1,
  "product": "unravis",
  "version": "0.1.0-rc.48",
  "channel": "rc",
  "published": "2026-11-20T10:00:00Z",
  "expires": "2027-02-20T10:00:00Z",
  "seq": 48,
  "minUpdateFrom": "0.1.0-rc.30",
  "assets": {
    "setup":    { "name": "unravis-setup-v0.1.0-rc.48-windows-x64.exe", "sha256": "...", "size": 0 },
    "app":      { "name": "unravis-v...-windows-x64.zip", "sha256": "...", "size": 0 },
    "blackbox": { "name": "unravis-blackbox-v...-windows-x64.zip", "sha256": "...", "size": 0 }
  },
  "blackboxExeSha256": "..."
}
```

- `channel` is `stable` or `rc`. `seq` only goes up. `expires` stops "freeze" replays of an old manifest.
- The manifest names assets by exact file name; the app builds the URL itself (`https://github.com/abhinavjp/unravis/releases/download/<tag>/<name>`). **No URL is ever taken from GitHub API JSON.**
- Signing key: ed25519. **Private key stays off CI** (open decision D2). Flow: CI builds the draft and uploads the unsigned manifest; nachi runs `tools/release/Sign-Release.ps1` on a trusted PC, which signs and uploads the `.sig`; nachi presses Publish. This fits the existing "person presses Publish" rule.
- Public keys (current + next) are compiled into `unravis.exe`, `unravis-setup.exe` and `unravis-blackbox.exe`. Two keys allow rotation without a stranded fleet.
- Later, when SignPath is approved: Authenticode on top. The app also checks the signer. This is a second lock, not a replacement (the ed25519 check also works offline and on air-gapped PCs).

## 6. Client side: check

- **Where:** `internal/update` (new), used by the existing server. Only the Unravis app checks. **The Blackbox service never touches the network** (footprint + air-gap).
- **Opt-in, default OFF** (open decision D1: A52 said manual only; this overturns it, so it needs nachi's yes). Three modes: Off (manual button only, as today), Notify (default when on), Notify + download.
- **Cadence:** one check at app start (after 60 s delay) then every 24 h plus random jitter up to 2 h, only while the app runs. No service, no scheduled task, no timers when off. Idle cost = one sleeping timer.
- **Request:** `GET /repos/abhinavjp/unravis/releases?per_page=5` with `If-None-Match` (ETag). A `304` costs almost nothing. Then fetch `update-manifest.json` and `.sig` only if a newer tag appears (~2 KB). Roughly 2 to 10 KB a day.
- **Channels:** `stable` (ignores pre-releases) and `rc` (includes them). Default = the channel of the running build (an rc build stays on rc). Change in Settings. The channel in the signed manifest must equal the one asked for.
- **Proxy:** use the Windows system proxy. Honour no-proxy. On any error: say "Could not check", retry next cycle, never loop. Back off x2 on repeated failure, cap 7 days.
- **No telemetry.** Request carries no user data; User-Agent is `Unravis/<version>` only. Version-only, no machine id.
- Dev builds (`version = dev`) never call out (as today).

## 7. Client side: verify, download, install (Unravis)

Verify order. Any failure = stop, delete the file, show a plain message, log it. **Never fall back to a weaker check.**

1. HTTPS only (TLS 1.2+, system trust). No HTTP, no redirect to another scheme. Redirects allowed only to `github.com` and `*.githubusercontent.com`, max 3.
2. Verify `update-manifest.json.sig` against the embedded public keys (ed25519).
3. Check manifest: `product`, `channel` matches, `expires` in the future, `seq` > last seen `seq` (stored), `version` > installed (semver; `rc.N` numeric) and the tag in the URL matches `version`. **No silent downgrade.** Equal or lower = ignored. `minUpdateFrom` honoured.
4. Download the setup exe: size capped to the manifest `size` (+0), written to a per-user folder with a locked ACL (not Temp), hashed while streaming; SHA-256 must equal the manifest.
5. Authenticode (once signing exists): signer must match the pinned publisher.
6. Show "Update to X ready. Needs administrator approval if Blackbox is installed. Close Unravis and update?" The user clicks. **Never installs without a click** (open decision D3 for a "install on next quit" option).
7. Launch `unravis-setup.exe --upgrade --expect-sha256 <hash> --expect-version <v>`. The installer's elevated worker **re-hashes its own image** before doing any admin step (closes the swap-after-verify gap). The installer re-verifies the embedded Blackbox pin as today.
8. The installer runs its normal upgrade: stop, replace program files, keep data, restart Unravis. A user-scope install without Blackbox needs no UAC.

Release-notes text from GitHub is shown as plain text only (escaped). It is never a link target or HTML.

## 8. Blackbox service

The Blackbox exe is inside the Unravis installer and pinned by hash. So it updates **with** Unravis. Two paths:

| Case | Path |
|---|---|
| Blackbox on this PC | Step 7's installer upgrades it. Or, if only the recorder is older than the bundled one, the app's BL-136 `update` op does it alone. One UAC prompt either way. |
| Blackbox on another server | Unravis shows "Recorder on SERVER is older (x vs y)". No remote push (remote code install is too risky). It offers the Blackbox zip + an exact command for the admin on that server: `unravis-blackbox.exe update --from <zip>` (verify the same signed manifest, offline). |

**Admin prompt handling**
- Windows needs a person for UAC. So: the app explains first ("Windows will ask for administrator approval to update the recorder service"), then shows one prompt. If declined or no admin rights: nothing changes, banner stays, message says what to do.
- Service is stopped only after the new file is staged and its hash is checked. Restart only if it was running. Wait for "running" + a status answer; else roll back.
- Never silent for the service in the first version. **Later option (D4):** the keeper service (already SYSTEM) verifies the signature itself and installs without UAC (the Chrome model). Faster, but a SYSTEM process that installs code is a bigger target. Not recommended for v1.

## 9. Rollback

- Before replacing: installer keeps the old program files in a backup folder (already in the plan) and writes a `pending` marker with old version, new version and time.
- After install the new app must start and report "ready" within 60 s (service: "running" + status answer). The app then clears the marker and deletes the backup after a grace period (next successful start).
- If the marker is still `pending` when the app starts next (crash loop, 2 failed starts) the installer's journal restores the backup, restarts the old version, and records "update X failed; staying on Y". That version is added to a local skip list so it is not offered again until a newer one appears.
- Data is never touched by rollback. Data schema: an update must not migrate cases in a way the old version can't read, or must take a copy first. (Check against case store before build.)
- Rollback is local only. A remote or manifest request can never lower the version.

## 10. Offline and air-gapped servers

- Updates off by default; nothing calls out unless turned on.
- **Import update** button (and `unravis-setup.exe --verify <folder>`): pick a folder with `unravis-setup-*.exe`, `update-manifest.json`, `.sig`. The same verify steps 2, 3, 4 run offline. Carry files on a USB stick after checking on a connected PC.
- Docs page: where to download, how to check with `Get-FileHash` and the in-app verify.
- Sealed corporate proxy that breaks TLS: check fails with "could not check". No option to skip verification. Ever.

## 11. Threats and answers

| Threat | Answer |
|---|---|
| Release tampered (stolen GitHub account/token) | Manifest signature from an off-GitHub key. Attacker can't forge. |
| MITM / bad Wi-Fi | HTTPS + signature + hash. |
| Downgrade to an old buggy/vulnerable build | Signed `version` + `seq` must exceed installed; installer also refuses. |
| Replay of an old valid manifest | `seq` and `expires`. |
| Fake "update" pointing to another host | URL built by app from the signed asset name; host allowlist. |
| Swap file between verify and run | Locked-ACL folder, hash re-checked in the elevated worker. |
| Huge download / disk fill | Size cap from signed manifest, free-space check. |
| Malicious release notes | Plain text only. |
| Signing key lost | Two embedded keys: current + next. Documented rotation. If both are lost: manual reinstall. |
| Signing key stolen | Offline key, optional passphrase/YubiKey. Revoke by shipping a build with a new key set (needs a manual install for PCs that never updated: state this honestly). |
| Update server (GitHub) down or rate-limited | Skip, retry later. Unauthenticated API: 60 requests/hour per IP, we use ~1 a day. |
| Update crashes the app/service | Section 9. |

Honest limits: a local admin can already do anything on the PC. Unsigned binaries still trigger SmartScreen on a manual first install; the in-app path avoids that because the app is already trusted.

## 12. Open decisions for nachi

| # | Decision | Recommendation |
|---|---|---|
| D1 | Overturn A52 (manual check only): allow an opt-in background check? | Yes, opt-in, default OFF, 24 h. |
| D2 | Where does the ed25519 private key live? (a) your PC / YubiKey, signed by hand before Publish; (b) CI secret (fully automatic, but a CI break = signing break) | (a). Matches "a person presses Publish". |
| D3 | Install timing: only on click, or also "on next quit"? | Click only for v1. |
| D4 | Silent Blackbox update via keeper service (no UAC)? | No for v1. One UAC prompt. Revisit later. |
| D5 | Default channel for rc builds on production-bound boxes: stay on rc, or move to stable once stable exists? | Stay on channel until the user changes it. |
| D6 | Remote Blackbox servers: manual zip + command (recommended), or Unravis pushes? | Manual. |
| D7 | Is the repo public? SignPath needs it. Does the update host have to stay GitHub? | Need your answer; spec works either way. |
| D8 | Ship before SignPath is approved? | Yes: ed25519 does the trust job; add Authenticode later. |

## 13. Build plan (after you confirm)

| Slice | Content | Live check on |
|---|---|---|
| U1 | `Sign-Release.ps1`, manifest generator in `release.yml`, public keys embedded, key-rotation doc | Real rc release signed + verified by a tiny CLI |
| U2 | `internal/update`: verify manifest, semver/seq/expiry, host allowlist, unit tests incl. bad sig, downgrade, replay, expired, wrong host, truncated | Real GitHub release (rc) |
| U3 | Check: ETag, cadence, jitter, channel, Settings toggle, proxy; extend the existing button to pre-releases | rc.N to rc.N+1 on your PC |
| U4 | Download + hand-off to `unravis-setup --upgrade --expect-sha256`; worker re-hash | Same, with cases + saved connections kept |
| U5 | Rollback marker + health check + skip list | Forced failure (bad build) on your PC |
| U6 | Blackbox: finish BL-136 live run; remote-server banner + `update --from zip` | Elevated host |
| U7 | Offline import + docs page | USB-stick run on a PC with no network |

Each slice follows the factory way: spec, build, review, verify, then reinstall on your PC. Verified-on-live vs tests-only is reported per slice (owner rule). Owed live items stay unticked in `BACKLOG.md`.

## 14. Footprint summary

- Off: zero. No timer, no network.
- On: one timer, ~2 to 10 KB a day, a few KB of memory for the verifier. Download only after a new release and only the one installer (size = manifest value).
- Blackbox: zero network, zero added CPU.
