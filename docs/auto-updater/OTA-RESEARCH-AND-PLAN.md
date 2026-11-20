# OTA updates for Unravis and Blackbox: research, gaps, go-live plan

Date: 2026-11-20. Status: RESEARCH + PLAN ONLY. No code, no tag, no release. Waiting for nachi to confirm.

Marks used everywhere:
- **[V]** verified from a page read today (source named).
- **[M]** my own knowledge, not re-checked today.
- **[R]** my reading of our repo docs (`docs/auto-updater/SPEC.md`, `KEYS.md`, `OFFLINE.md`). I did **not** re-read the Go code or `release.yml` in this pass.

---

## 1. ELI18 summary

**What an OTA update is.** The program asks a web page "is there a newer version?" every so often. If yes, it downloads it, checks it is really yours, swaps itself, and goes back to the old one if the new one breaks.

**The one idea the whole industry agrees on.** Never trust the download place. Trust a *key*. You sign a small file ("manifest": version, size, hash) with a private key you keep offline. The program carries the matching public key. A hacker who takes over GitHub still cannot fake your signature.
- Sparkle/WinSparkle, Elastic Agent, Chromium's updater all do this. [V] Elastic: agent checks a SHA hash plus a PGP `.asc` signature with a public key built in. [V] Chromium: installers carry a SHA-256 in the manifest, public key hard-coded in the updater. [M] Sparkle uses ed25519.
- Small Go desktop apps do the same in miniature: JSON manifest, SHA-256, minisign signature, user starts the installer. [V] (Oneleet write-up, 2024).

**The second idea: roll out slowly.** Big players do not push to everyone at once. Elastic spreads upgrades over a time window and retries with growing waits. [V] Chromium records a "cohort" per client and honours server "retry later" hints. [V] The point: if a build is bad, only a few machines get hurt, and you can stop.

**The third idea: a way back.** Elastic shows states "Upgrade monitoring / rolled back / failed" and keeps old data 7 days. [V] Chromium updaters "qualify" themselves and report outcomes so the server can "cease serving bad installers". [V]

**Servers with nobody logged in.** A Windows *service* (runs as SYSTEM) can update with no user. Chromium's updater uses a system service plus scheduled tasks for system installs. [V] A service cannot overwrite its own running exe, so a small helper does the swap. [M] That is what we built.

**Cost.** Everything above is free **except Windows code signing** (the "publisher name" Windows shows):
- Azure Artifact Signing: about USD 9.99/month; individuals only USA/Canada; organisations USA/Canada/EU/UK. [V] (Microsoft Learn, updated 2026-11-10)
- OV cert USD 150-300/year, EV USD 400+/year; EV no longer skips SmartScreen since 2024. [V]
- **SignPath Foundation: free for qualifying open-source.** [V] That is the only free path. Needs an OSI licence (we have Apache-2.0), manual approval per release, acceptance at their discretion. [R]
- Our own key signs the *update* (free, works today). Authenticode only affects the first manual install / SmartScreen. [M]

**Where we stand.** The design is already close to what the industry does, and in places stricter (signed version + size + hash, anti-rollback counter, expiry, per-channel keys, two keys). What is missing is mostly **proof it works for real**: no keys made, no signed release ever published, nothing run end to end. Plus a few missing safety nets (pause/stop a bad release, rollout percentage, backup feed host).

---

## 2. What we built vs the industry (side by side)

| Practice | Industry | Us [R] | Verdict |
|---|---|---|---|
| Signature from off-host key | Sparkle, Elastic, Chromium [V] | ed25519 manifest, keys in exe | Match |
| Hash + size in signed manifest | Chromium, Oneleet [V] | yes, + exact name + version | Match / stricter |
| Anti-rollback / replay | TUF idea [V from spec research] | `seq`, `expires`, no downgrade | Match |
| Key rotation | Elastic notes embedded key can go stale and break upgrades [V] | 2 keys per channel, rotation doc | Match |
| No "skip verify" switch | Elastic has `--skip-verify` ("not recommended") [V] | none, by design | Better |
| Channels | common [M] | rc / stable, separate keys | Match |
| Check cadence | Elastic 30 min [V]; Chromium about 4.5-5 h [V] | 30 min rc, 6 h stable | OK |
| Retry/backoff | Elastic 1m..1h [V]; Chromium honours X-Retry-After, cap 24 h [V] | x2 up to 6 h | OK |
| Rollback | Elastic auto-monitor + rollback [V]; Chromium qualification [V] | marker + health + skip list; Blackbox helper restores `.old` | Match (unproven live) |
| Staged rollout | Elastic window; Chromium cohorts [V] | **none** | Gap |
| Stop / pause a bad release | Chromium server stops serving [V] | **none** (only publish a newer one) | Gap |
| Health reporting | Chromium telemetry [V] | none, by choice (no telemetry) | Deliberate; see G6 |
| Air-gapped | Elastic: own artifact host [V] | `--verify` folder built; share source not built | Partial |
| SYSTEM service update, no user | Chromium system service [V] | Blackbox self-update built | Built, unproven |
| Authenticode | standard [M] | none yet | Gap (cosmetic for in-app path) |

---

## 3. Gap list, ranked

Rank = how much it blocks "live soon" or hurts if wrong.

**G1. Nothing has run for real. (Blocker)** [R]
No signing keys, no signed release, no feed. `Sign-Release.ps1`, attestation check (BL-150), U3 to U8 are all unproven on a real signed feed. Fix: Section 4, phases 0-3.

**G2. No way to pause or withdraw a bad release. (High)** [R + V]
Our anti-rollback rule means you cannot publish "go back to the old one". Today the only fix is a newer version, which costs time while servers keep updating. Industry servers can stop serving. Fix (small): add to the signed feed a `blocked` list (versions to refuse) and a `pause` flag; client honours it. Needs a spec line and about one slice. Free.

**G3. No staged rollout. (High for Blackbox on many servers)** [R + V]
Everything updates at the same 30-minute tick. Fix: signed manifest gets `rolloutPercent` (and optional `rolloutStart`); client keeps a random local number (no machine id sent, keeps "no telemetry") and updates only if its number is under the percent. Rise 5% -> 25% -> 100% by re-signing the feed. Free. Pairs with G2.

**G4. Feed host is a single GitHub path, rate limit unverified. (Medium)** [V + R]
GitHub tightened limits for unauthenticated requests in May 2025 (their post gives no numbers) [V]. Many servers behind one office IP could hit a 429. Client already backs off [R]. Fix: also publish the same signed 1 KB feed on a second free host (GitHub Pages of the same public repo [M]) and let the client try it second. Safe because trust is the signature, not the host. Test the real limit in phase 2 (spec says "U2 live-checks this").

**G5. `expires` can freeze the fleet. (Medium)** [R]
Manifest expiry is about 3 months in the spec example. If no release is signed for 3 months, clients treat the feed as expired and stop seeing updates, with no warning. Also a server with a wrong clock fails closed. Fix: re-sign the feed on a calendar (a monthly "refresh" with same version, higher `seq`), or lengthen expiry for stable. Check the code before choosing. TUF calls this the freeze attack. [V from spec research]

**G6. No signal back that an update worked. (Medium, by choice)** [V + R]
Chromium reports outcomes so the server can stop a bad build [V]. We send nothing (good for privacy and footprint). Free substitute: Blackbox and Unravis write a local result you can read (`/status` already carries update state [R]); for a fleet, Unravis shows each Blackbox's update state. Not built in Unravis display [R]. Keep "no telemetry". Decision for nachi in Section 6.

**G7. Blackbox unattended edge cases. (Medium)** [R]
Built: self-update, helper, rollback, `.old`. Missing: folder/share source (air-gapped servers), maintenance window, Unravis display of status. Also: the "update window" restarts a recording for a few seconds. Fine, but must be proven on a real service while **no user is logged in** (phase 3).

**G8. Rollback hand-over needs a click. (Low-Medium)** [R]
Unravis app rollback opens the old setup and needs an Install click (and UAC for machine installs). Correct for a desktop app. Blackbox rollback is automatic. No change needed; just test.

**G9. No Authenticode. (Low for in-app updates, Medium for first install)** [V + M]
In-app updates are verified by our key, so SmartScreen is not involved. A first manual download of the setup from GitHub will show "unknown publisher". Fix: apply to SignPath Foundation now, in parallel, no cost. If refused, live without it and document the SmartScreen click. Azure Artifact Signing is not free, and nachi's country may not be eligible (not confirmed; ask). [V]

**G10. Single signing person. (Low)** [R]
Key lives with nachi only. That is the design (offline, 2 USB, backup key). Risk is loss, not theft. Mitigation exists (backup `-2` keys). Keep the passphrase written down in a safe place nachi trusts.

---

## 4. Plan to go live (free, shortest safe path)

Each phase ends with a live check. I do not start phase 1+ until nachi says go.

### Phase 0: make the keys (nachi, about 20 min, no admin needed)
1. On your own PC, a clean checkout of a commit you reviewed.
2. Run `pwsh ./tools/release/Make-Keys.ps1 -OutDir D:\secret -AddToTrusted` (`KEYS.md`). It asks 8 passphrases (4 keys x 2).
3. Use 4 different passphrases. Write them somewhere that is not the USB.
4. Copy `rc-1`, `stable-1` to 2 USB sticks. `rc-2`, `stable-2` to a third stick kept elsewhere.
5. `git diff`, then commit only `internal/update/trusted_keys.txt`. **Never** commit `*.key.json`. Never print a passphrase or key.
6. Tell me "keys done". (If you want, I review the diff of that one file before you push.)

### Phase 1: first signed release candidate (you press keys, I prepare)
1. I confirm `release.yml` still produces the manifest + attestation (read the workflow; run on a throwaway branch only if you allow).
2. You approve tagging the next rc (I do not tag without your word).
3. CI builds the DRAFT release. You run `Sign-Release.ps1` step 1 (passphrase prompt). It verifies attestation (BL-150) and asks "yes".
4. You press Publish. You run step 2 (updates the feed).
5. Live check: download feed with a plain browser, run `unravis-setup.exe --verify` on the folder. Expect `OK`.

### Phase 2: Unravis updates itself (nachi's PC)
1. Install rc.N from the signed release. Cut rc.N+1 the same way.
2. App shows the update bar; click; setup runs; cases and saved connections must survive.
3. Force one failure (bad build or killed start) to see rollback and the skip list.
4. Watch the real feed host for a 429 (answers G4).

### Phase 3: Blackbox on a service, nobody logged in (admin prompt once)
1. Install Blackbox as a service on an elevated host (this is BL-141: one UAC approval, **by you**).
2. Publish another rc; log out of the machine; wait one interval.
3. Expect: service updated, status shows new version, recording gap of seconds only.
4. Force a failure: expect `.old` restored and version on skip list.
5. Repeat with `managed-by-unravis` marker present (Blackbox next to Unravis): the service must NOT self-update.

### Phase 4: close the safety gaps (build, free, before stable)
Order: G2 pause/blocked list, then G3 rollout percent, then G5 expiry refresh, then G4 Pages mirror, then G7 share source + Unravis status display. Each follows the factory way (spec -> build -> review -> verify). Needs your go because it changes the signed format (seq/schema).

### Phase 5: stable
After the sandbox period (till Mon 23 Nov per tracker), sign `stable-1`, publish `stable.json`. Official release/tag only after you say so.

### In parallel, no cost
Apply to SignPath Foundation (needs your account details; I will not submit anything for you).

### Steps only nachi can do
- Phase 0: key creation, passphrases, USB backups.
- Every release: press Publish; type the passphrase for signing; type "yes".
- Phase 3: approve the Windows admin (UAC) prompt on the host.
- SignPath application (identity/consent).
- Any tag or official release.

---

## 5. What I am unsure about

- **GitHub release-download rate limit:** no number found; the May 2025 changelog gives none. [V] Needs a live test.
- **Chromium wake cadence:** the two Chromium docs disagree (5 h vs 4.5 h). Not important for us.
- **Chromium rollout %:** the pages I read describe cohorts and retry delays but no rollout-percentage mechanism. [V] So "rollout percent" is my design, borrowed from the idea, not a copy of Chromium.
- **Elastic automatic rollback:** the pages show the states but not the mechanism. [V]
- **Not checked this pass:** Velopack/Squirrel/Sparkle (covered in SPEC section 16 earlier), GitHub Pages limits [M], the actual code in `internal/update` and `release.yml` [R only], whether nachi's country qualifies for Azure signing.
- I used web-fetch helpers for four source reads; I quoted only what they marked as verbatim.

## 6. Decisions I need from nachi

1. **Go on Phase 0-3 now (prove it works), then Phase 4?** Recommended: yes.
2. **Pause + rollout % (G2, G3):** build before stable? Recommended: yes, before stable, not before the first rc.
3. **Keep "no telemetry" (G6)?** Recommended: yes; use status screens.
4. **Apply to SignPath now?** Recommended: yes, parallel, no cost.
5. What country/entity do you want shown as publisher? Affects Azure eligibility.

## Sources

- [Microsoft Learn: code signing options](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options) [V]
- [GitHub changelog: unauthenticated rate limits, 2025-05-08](https://github.blog/changelog/2025-05-08-updated-rate-limits-for-unauthenticated-requests/) [V, no numbers]
- [Chromium updater design doc](https://chromium.googlesource.com/chromium/src.git/+/HEAD/docs/updater/design_doc.md) and [functional spec](https://chromium.googlesource.com/chromium/src/+/main/docs/updater/functional_spec.md) [V, partly summarised by the fetcher]
- [Elastic: upgrade standalone agents](https://www.elastic.co/docs/reference/fleet/upgrade-standalone) and [Fleet upgrade](https://www.elastic.co/guide/en/fleet/master/upgrade-elastic-agent.html) [V]
- [Oneleet: custom Windows auto-updater for Go apps](https://www.oneleet.com/blog/crafting-a-custom-windows-auto-updater-for-go-powered-desktop-apps) [V, 2024]
- [nullprogram: code-signing, 2026-04-25](https://nullprogram.com/blog/2026/04/25/) [V, one author's view]
- Earlier sources already logged in `docs/auto-updater/SPEC.md` section 16 (Sparkle, CVE-2025-0509, TUF, SignPath terms, Velopack, Doyensec).

---

## 7. Addendum (nachi, 2026-11-20): automate the release and the dev install

Ask: no more pasted commands. One command to release. One command to install or update both programs on a dev PC, with a single admin prompt. Zero cost. Plan only; nothing built.

### 7.1 What already exists [R]
- `release.yml`: a pushed tag `vX.Y.Z[-rc.N]` builds, attests, runs the silent-install smoke test on two runner images, then makes a DRAFT release. Never publishes.
- `Sign-Release.ps1 -Tag ... -KeyFile ...` (step 1) and `-Feed` (step 2). Checks attestation, asks passphrase and "yes".
- `Test-Release.ps1`, `Invoke-Smoke.ps1`, `New-ReleaseNotes.ps1`, `Make-Keys.ps1`.
- Installer: UI unelevated, short-lived elevated worker for admin steps. Blackbox `update` op (BL-136) built, not run live.

So the work is mostly **gluing these into one command** and adding a **post-release check on a test target**.

### 7.2 One-command release: `tools/release/Release.ps1 -Version 0.1.0-rc.49`
Runs on nachi's PC (the key never leaves it). Stops at the first failure. Prints one line per stage.

| # | Stage | Who | Notes |
|---|---|---|---|
| 1 | Preflight: on `master`, clean tree, CI green on HEAD, version/seq moves forward, `Test-Release.ps1` passes, keys present in `trusted_keys.txt` | script | Uses `gh`. Refuses if anything is off. |
| 2 | Tag + push | script, after you type the version again | Running it with `-Version` is your approval for **this tag only**. No flag runs without the confirm. |
| 3 | Wait for CI: build, attest, smoke, DRAFT | script (`gh run watch`) | Existing workflow, unchanged. |
| 4 | Sign (existing `Sign-Release.ps1` step 1) | **you**: key passphrase, type "yes" | The only secret step. Never in CI. |
| 5 | Publish the draft | script, after you type "publish" | Single explicit confirm, shows version + notes. |
| 6 | Update the feed (`-Feed`) | script | Refuses a draft, checks it moves forward. |
| 7 | Verify (7.3) | script + CI | Failure = loud result and the pause/blocked list (gap G2) as the stop button. |

Options: `-DryRun` (stages 1 and the checks, no tag), `-Resume` (continue after a failure at the stage that failed), `-Channel` is implied by the tag.
Zero cost: all of it is `git`, `gh`, `pwsh` and GitHub Actions on a public repo [M: Actions are free for public repos].

### 7.3 Verify on a test target (free)
Two targets, both free:

1. **GitHub-hosted Windows runner** (workflow `verify-release.yml`, started by stage 7). It installs the PREVIOUS published version, starts Unravis and the Blackbox service, forces an update check against the real public feed, and asserts: Blackbox service now reports the new version and recording still works; Unravis downloaded, verified and (with the expect flags) upgraded; a bad-signature and a downgrade case are refused. A runner is admin and has no human logged in, so it is a fair stand-in for the "server, nobody logged in" case. It is not a real server: services, antivirus and policy differ. [M]
2. **Your own dev PC** via the install script below, as the real-machine check.

Needs a small build item I have **not verified exists**: a `check-now` trigger for both programs (a CLI flag or status op) so the test does not wait 30 minutes. Must stay local-only and read-only in effect (it only runs the normal check).

A Hyper-V VM on your PC is optional (free with Windows Pro [M]); not needed for go-live.

### 7.4 One-command dev install: `tools/dev/Install-Dev.ps1`
Replaces every hand-typed admin command (including the recorder update).

- Start it normally (not as admin). Modes: `-FromBuild` (build the repo now) or `-FromRelease <tag>` (download, run `--verify`).
- It does all unelevated work first: build or download, hash, verify.
- Then it asks for **one** UAC prompt and re-launches itself once, elevated, for a fixed list of steps only: stop the Blackbox service if running, run the verified setup with `--upgrade --expect-sha256 --expect-version`, update the Blackbox recorder (the BL-136 op), start it, wait for "running" plus a status answer, roll back on failure, then print a short result and write a log.
- Install and update both programs in the same run. The same script works for a first install.
- Elevated part runs only the verified file by hash (same rule as the app). No secrets printed; read key and sealed secrets are never touched.
- `-FromBuild` is for dev only: it installs a locally built pair that is consistent with the Blackbox pin. It never skips verification for a release download.

### 7.5 Build slices (order matters)

| Slice | Content | Needs |
|---|---|---|
| P1 | `Install-Dev.ps1` + log. Live-proves BL-136 / BL-141 on your PC | your 1 UAC |
| P2 | `check-now` hook in Unravis and Blackbox | none |
| P3 | `verify-release.yml` | P2 |
| P4 | `Release.ps1` | keys (Phase 0), P3 |
| P5 | docs: `RELEASING.md` (one page, the commands above) | none |

Recommended order: P1 first (helps today, no keys needed), then keys, then P2-P4 together with Phase 1 of the main plan, so the first signed rc already goes out through `Release.ps1`.

### 7.6 Still only nachi (cannot be automated, by design)
- Key passphrase and the "yes" at signing.
- The explicit confirms for tag and publish.
- One UAC prompt per dev install.
- Anything official (stable tag) stays a separate ask.

### 7.7 Unsure
- Whether the runner can be a faithful stand-in for a real server: partly. Real-server proof stays a manual check once (Phase 3).
- Whether `check-now` already exists: not checked.
- Hyper-V availability on your PC: not checked.
