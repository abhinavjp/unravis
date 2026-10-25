# TSK-21 Security review: cold-start routes

Read-only review of branch feat/cold-start-wizard (9bd5dcd, d83454d, 3b5b12e) by a Read/Grep/Glob-only subagent. The agent could not write this file or run git or the app, so the controller saved its report here. Nothing was executed against a real instance.

Scope: locate, probe/reachable, environments, sources, mongo-sources, master-key/{status,setup,unlock,lock,rewrap,reset-damaged}, cases/{id}/export, cases/restore. Every finding was traced in code.

Severity counts: Critical 0, High 0, Medium 0, Low 6, Info 7.

## Low

### L1. Each passphrase check has its own throttle (about 3x guesses)
- Where: internal/server/case_file.go:248-291 (export, own `exportTh`, verifies with `masterkey.Unlock(h.mk.dir, copy)`), case_file.go:387-405 (restore, own `restoreTh`, a different target), internal/server/masterkey.go:634-640 (unlock, `unlockTh`).
- Scenario: a process holding the session token guesses the master passphrase through unlock and export side by side. Each throttle counts separately, so the rate doubles or triples. Export works even when already unlocked. Argon2id (64 MiB, t=3) still caps the rate, so the gain is linear, not a bypass.
- Fix: send every master-passphrase check through the single `unlockTh` under `s.op`, or have export compare against the open session's verifier.

### L2. masterkey.dat size-checked by path, then read by path without a limit
- Where: internal/masterkey/masterkey.go:154-183 (`readDoc`: Lstat + size check, then `os.ReadFile`).
- Scenario: a same-user process swaps the file for a link to a huge file between the two calls. Local DoS only.
- Fix: open once refusing reparse points, fstat the handle, read through `io.LimitReader(f, fileMax+1)`.

### L3. reset-damaged checks one file, then renames by path
- Where: internal/server/masterkey.go:812-848.
- Scenario: an outside process drops a good masterkey.dat between the damaged check and the rename. The good file is renamed to `.damaged-<ts>` (not deleted) and the app shows "no master key". Denial and confusion, not data loss.
- Fix: re-Inspect right before the rename; tell the user where the renamed file is.

### L4. A header-less DPAPI case.key is still accepted after a master key exists
- Where: internal/casestore/keyfile.go:121-141, internal/casestore/sqlite/sqlite.go:628-666.
- Scenario: a same-user process plants a complete DPAPI case folder. It opens without the passphrase and lists beside real cases. It cannot downgrade an existing master-key case. Integrity and provenance only.
- Fix: keep a case index sealed under the master key, or label DPAPI cases "not protected by master key" in the UI and block new DPAPI cases once all are re-wrapped.

### L5. Probe blocklist misses NAT64/6to4-embedded addresses and Azure WireServer
- Where: internal/server/probe.go:158-173 (`probeBlockedAddr`).
- Missing: 64:ff9b::/96, 64:ff9b:1::/48, 2002::/16 (embedded IPv4 not rechecked), 168.63.129.16/32. Impact small: TCP connect only, no bytes, fixed reason, token required, 20/min.
- Fix: extract the embedded IPv4 and re-check it; add 168.63.129.16. Write down that RFC 1918 and loopback targets are allowed on purpose.

### L6. locate leaves its worker running after the 10 s timeout, and reasons reveal whether a path exists
- Where: internal/server/discover_locate.go:429-444.
- Scenario: slow paths (dead network drive) strand workers, at most about 30 a minute (rate limit). Reasons are a closed set but show exists / directory / reparse point, an existence oracle limited to what the user's own account sees.
- Fix: semaphore on concurrent locate workers (429 while full), pass a context into the walker. Note the oracle in the threat model.

## Info
- I1. Throttles are in memory, so a restart resets them. Needs same-user control already.
- I2. In export/restore, `wait()` runs before the busy-flag CAS, so two in-flight requests can each get one extra guess. One Argon2 run at a time still holds.
- I3. `checkCasePath` accepts SUBST drive letters; a SUBST pointing into the data root passes the lowercase-prefix `insideRoot` check. The user must create the SUBST.
- I4. Small TOCTOU between RejectReparse and the O_EXCL create in WriteCaseFile. O_EXCL never overwrites. Accepted.
- I5. Secret copies in Go memory (io.ReadAll growth, NFC copy, hex-string case keys) cannot all be zeroed. Nothing is written, logged or returned, so the I5 rule holds.
- I6. A crafted case file whose passphrase the attacker knows reaches SQLite on restore. Confined by SQLITE_DBCONFIG_DEFENSIVE, trusted_schema=OFF, quick_check, the case_id check and the migration path. Keep SQLite current.
- I7. During restore the new case id exists between Create and the end of the rebind. Random id, folder lock held, erased on any failure. Accepted.

## Checked and sound
- Auth: every non-public /api route needs X-Audit-Token (constant-time compare). Host must be loopback or localhost; Origin must equal Host (defeats DNS rebinding). Security headers on every response.
- 423 lockedGate covers every /api/cases* route while locked or damaged, applied after auth.
- Passphrases travel only in POST JSON bodies, as []byte, wiped after use. Bodies capped (1 MiB general, 16 KiB case-file routes), DisallowUnknownFields set.
- I5 invariant holds: keys never logged, returned, put in URLs or written in plain form. masterkey.dat holds only salt, params and a sealed verifier. Keyring.Lock zeroes the key.
- Error text is fixed; no paths, resolver text or passphrase hints are returned.
- Argon2id bounds validated before allocation (t 1-10, m 16-256 MiB, p 1-16, salt 8-64). Passphrases over 1024 bytes count as wrong without running Argon2.
- AES-256-GCM, fresh 12-byte nonce each seal, distinct AAD labels, so case.key and manifest.dat cannot be swapped and the case-file KDF params are bound into the header tag.
- Case-file parsing checks magic, version, KDF id, params, salt, header length, exact file size, DB at most 8 GiB, SHA-256 before any case is created. No compression, so no decompression bomb.
- Restore rebind runs in one transaction (BEGIN IMMEDIATE, case_id check, evidence_id rewrite, COMMIT), writes the new id to key, manifest and folder name, and erases on any failure.
- Key files: 4 KiB cap, v and p must agree, mixed kinds refused, Rewrap DPAPI to master only with read-back check and rollback.
- Export: O_EXCL placeholder, temp file, atomic replace; never overwrites; refuses targets inside the data root; full path validation (no \\?\, UNC, ADS, "..", 8.3 aliases, reserved names, reparse points).
- Probe: only dials addresses it checked (no second lookup), TCP connect only, sends no bytes, closed reason set, 20/min. Mongo sources accept only database names from a recent test (30 min TTL), refuse credential-like labels, never echo passwords.
- Case ids are 128-bit random and checked against `^[a-z2-7]{26}$` before use.
