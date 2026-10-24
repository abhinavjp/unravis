# Local storage decision

Status: proposed. Scope: where Unravis keeps data on disk, now and once evidence import lands (roadmap §6.7, §8.6, §8.9).

## Recommendation

| Tier | Data | Choice |
|---|---|---|
| (a) small state | saved connections, hypothesis ledger, preferences | **Keep DPAPI-sealed JSON files with atomic replace. No database.** Extend the existing `connections.dat` pattern to the ledger (plain today) and to preferences. |
| (b) imported evidence | IIS/HTTP.sys, mongod.log, SQL ERRORLOG/waits, perf counters, Hangfire, App Insights; millions of events per case | **One SQLite file per case through `github.com/ncruces/go-sqlite3` (MIT, cgo-free) with its `adiantum` encrypting VFS.** Each case gets a random 256-bit key, wrapped with DPAPI (current user) and stored in the case folder. Erase works by destroying the key. |

Fallback for (b): `modernc.org/sqlite` (BSD-3, cgo-free, no encryption) with AES-GCM sealing at the application level on the message/payload columns. It is only needed if the ncruces VFS proves unstable on Windows in our tests (see Risks).

Do not add a database for (a). Do not add a second engine for (b).

## Why

- **Needs of (b).** Time-range scans and interval overlap joins against audit rows are B-tree index plus SQL work. SQLite handles both in a few lines. Every alternative here means hand-building the index, the join and compaction ourselves, or it leaks plaintext keys.
- **Light.** ncruces needs no cgo, no gcc and no DLL, and it keeps `CGO_ENABLED=0` in `build.ps1`. The exe grows by about 11.5 MB in a default build (about 6.6 MB stripped). The current exe is about 20 MB.
- **Secure enough, stated honestly.** Adiantum encrypts the main DB, its journal/WAL and temp files, so the whole case file is ciphertext at rest. Its limits are written down under "What is and is not protected".
- **Proven core.** SQLite's own format, crash safety, `quick_check` and `secure_delete` carry over unchanged. ncruces tracks upstream SQLite closely, and the release in hand bundles a current 3.53.x.
- **Low memory.** With the index built first, the encrypted engine peaked at the lowest working set of any engine tested (table below).
- **Tier (a) is already solved.** `internal/history` already does DPAPI + temp + fsync + `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)` under a named mutex. Tens of records do not justify an engine. The only gap is that `ledger.json` is plaintext.

## Benchmark

Method, run in the scratchpad (not the repo) on Windows/amd64, Go toolchain from `.dev/toolchains`, `CGO_ENABLED=0`:

- **Data.** 2,000,000 synthetic events over 6 sources and a 7-day span. Columns: `ts`, `te` (ms), source, status, host id, kind, and a message of about 40–80 bytes. 2% of events last up to 60 s; the rest up to 2 s.
- **Load.** Batches of 10,000 rows per transaction.
- **Range test.** A 10-minute window mid-span; mean of 50 queries, median of 5 rounds.
- **Join test.** 500 audit intervals of 1–60 s, counting overlaps (`e.ts BETWEEN a.s - maxDur AND a.e AND e.te >= a.s`); median of 5 runs.
- **Memory.** Peak process memory via `K32GetProcessMemoryInfo`.
- **SQLite settings.** WAL, `synchronous=NORMAL`, 16 MB page cache, one connection.

All engines returned identical answers, which cross-checks every implementation.

| Engine | Load | Index build | On disk | 10-min range | 500-interval join | quick_check | Peak working set / private |
|---|---|---|---|---|---|---|---|
| modernc (plain) | 2.8 s | 0.9 s | 142 MiB | 1.9 ms | 62 ms | 0.4 s | 170 / 212 MiB |
| ncruces (plain) | 2.8 s | 1.0 s | 142 MiB | 1.2 ms | 58 ms | 0.4 s | 95 / 131 MiB |
| **ncruces + adiantum** | **3.6 s** | **1.3 s** | **142 MiB** | **1.2 ms** | **70 ms** | **0.6 s** | **96 / 132 MiB** |
| ncruces + xts | 4.9 s | 2.3 s | 142 MiB | 1.2 ms | 160 ms | 1.5 s | 96 / 132 MiB |
| bbolt, values AES-GCM sealed | 3.4 s | n/a | 512 MiB | 0.3 ms | 22 ms | 0.3 s | 303 (mmap) / 91 MiB |
| append-only segments, AES-GCM blocks + in-memory index | 0.2 s | n/a | 73 MiB | 1.4 ms | 160 ms | 0.1 s | 263 / 302 MiB |
| modernc, index created first | 4.9 s | 0.2 s | 145 MiB | 1.9 ms | 66 ms | n/a | 49 / 89 MiB |
| **ncruces + adiantum, index first** | 6.6 s | 0.3 s | 145 MiB | 1.2 ms | 73 ms | n/a | **35 / 71 MiB** |

Exe size delta over a minimal Go program (default build / `-s -w`):

| Engine | Default build | Stripped (`-s -w`) |
|---|---|---|
| bbolt | +1.0 MB | +0.6 MB |
| modernc | +7.0 MB | +4.5 MB |
| ncruces | +11.3 MB | +6.4 MB |
| ncruces + adiantum | +11.5 MB | +6.6 MB |

**Reading the numbers:**

- Encryption with adiantum costs about +30% load time and +20% join time over plain SQLite. Range scans show no measurable difference.
- At 2M events, loading takes a few seconds, which is acceptable for an import step.
- Building the index after a bulk load is fastest. Building it first uses the least memory.
- **Default: create the index after the bulk insert**, inside the import transaction boundary.

## Rejected options

| Option | Why not |
|---|---|
| **Encrypted JSON for evidence** | The whole document must be decrypted into memory for every query; there is no range index. Fine for (a), unusable for millions of rows. |
| **mattn/go-sqlite3** ([repo](https://github.com/mattn/go-sqlite3)) | Requires `CGO_ENABLED=1` and gcc on Windows. This box has neither, and the build is cgo-free by design. Its README documents no encryption. |
| **SQLCipher** | C library, so cgo. It would reintroduce a C toolchain and a native build per release. |
| **DuckDB** ([Go client notes](https://duckdb.org/docs/current/clients/go/troubleshoot)) | cgo plus MSYS2 gcc on Windows, with large static libs. Columnar analytics is overkill for indexed time-window lookups. |
| **bbolt** ([repo](https://github.com/etcd-io/bbolt)) | No encryption, and keys stay plaintext: our time-ordered keys would leak every event timestamp. The file grew to 3.6x SQLite's size and never shrinks, because freed pages are not returned to the OS. Large single transactions into new buckets are discouraged. Fast reads do not offset these problems. |
| **Pebble** ([repo](https://github.com/cockroachdb/pebble)) | It is scoped to CockroachDB's needs. Encryption at rest is a CockroachDB enterprise feature, not part of Pebble ([docs](https://www.cockroachlabs.com/docs/v24.1/security-reference/encryption)). It is an LSM with compaction tuning, a heavyweight for one user. |
| **BadgerDB v4** ([repo](https://github.com/hypermodeinc/badger)) | Encryption is AES in a counter-like XOR mode with CRC32 checks only and no MAC ([key registry](https://raw.githubusercontent.com/dgraph-io/badger/main/key_registry.go), [docs](https://docs.dgraph.io/v24.1/admin/enterprise-features/encryption-at-rest)). Value-log GC must be driven by the caller. Stewardship has changed hands repeatedly. |
| **Pogreb** ([repo](https://github.com/akrylysov/pogreb)) | Hash index: its README says range scans are impossible. |
| **BuntDB** ([repo](https://github.com/tidwall/buntdb)) | The dataset lives in RAM with an append-only log. Millions of events per case would sit in memory. No encryption. |
| **Append-only segments + in-memory index** (built and benchmarked) | Smallest file and fastest load, with real authenticated encryption (AES-GCM per block). But we would own the index format, crash recovery, compaction, integrity scans and every query shape. The overlap join was 2.3x slower than encrypted SQLite and used 2–4x the memory. Keep it as the documented escape hatch if SQLite ever becomes a problem, not as the starting point. |
| **ncruces + xts VFS** ([README](https://github.com/ncruces/go-sqlite3/blob/main/vfs/xts/README.md)) | Viable: AES-XTS uses FIPS-approved primitives. It was about 2x slower than adiantum on load, join and integrity checks. Choose it instead only if a customer requires FIPS-approved primitives. Switching is a one-line VFS change plus a re-import. |
| **modernc.org/sqlite as primary** ([pkg](https://pkg.go.dev/modernc.org/sqlite)) | Solid and smaller (+7 MB), but it has no encrypting VFS. Encrypting at application level leaves timestamps, index pages and the schema in clear, and it needed more memory in our run. Kept as the fallback. |

## Encryption and key management

### Tier (a): small state

- One file per concern under `%APPDATA%\APIAuditExplorer\`:
  - `connections.dat`: unchanged.
  - `ledger.dat`: replaces `ledger.json`.
  - `prefs.dat`: new.
- Each file is the whole JSON document sealed with `CryptProtectData`:
  - Flag `CRYPTPROTECT_UI_FORBIDDEN`, current-user scope.
  - Never `CRYPTPROTECT_LOCAL_MACHINE`, which lets any local user decrypt.
  - Optional per-file entropy constant, a domain separator such as `"unravis/ledger/v1"`, so one blob cannot be swapped for another.
- DPAPI binds to the user's logon credentials and the machine, and adds a MAC. Tampering or a foreign blob makes decrypt fail, which gives integrity for free ([CryptProtectData](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata)).
- Reuse the `history.Protector` interface. The ledger store takes a Protector just as `history.Recent` does.
- Do not add a separate AES key layer for (a): DPAPI already provides encryption + MAC, and the files are capped small.

### Tier (b): per-case evidence

```
%APPDATA%\APIAuditExplorer\cases\<case-id>\        user-only ACL (roadmap §6.7)
  case.key        DPAPI blob: { version, 32-byte DB key, 32-byte pseudonym key (§8.9) }
  case.db         SQLite via adiantum VFS (journal/WAL alongside, also encrypted)
  manifest.dat    DPAPI-sealed JSON: importer, source SHA-256, row counts, byte counts, schema version
  quarantine\     refused/oversize inputs awaiting user action (§6.7)
```

- **Key generation.** 32 bytes from `crypto/rand` at case creation.
  - Sealed with DPAPI and written atomically, the same way as `connections.dat`.
  - The plaintext key lives only in process memory: zero the buffer after open, accepting that Go may keep copies.
- **Key delivery.** Open with `file:case.db?vfs=adiantum`, then issue `PRAGMA hexkey='…'` as the first statement on the connection, using the driver's connection hook (`driver.Open(dsn, fn)`).
  - Never put the key in the DSN, because DSNs appear in errors and logs.
  - Use `hexkey`, not `textkey`. The key is already random, so the Argon2id stretch is wasted.
- **Hardening applied on every connection:**
  - `SQLITE_DBCONFIG_DEFENSIVE` on.
  - `trusted_schema=OFF`.
  - No FTS or other extensions registered.
  - `secure_delete=ON`.
  - `foreign_keys=ON`.
  - `cell_size_check=ON`.
  - One connection per case DB (`SetMaxOpenConns(1)`).
  - `journal_mode=WAL`, `synchronous=FULL` during import commits, `NORMAL` while reading.
  - Optional page checksums via `conn.EnableChecksums("main")`; enable from day one, because it changes the reserved bytes and needs a VACUUM to retrofit.
  - These follow SQLite's CVE guidance: risk comes from untrusted SQL or untrusted DB files; we run neither, and defensive mode covers the rest ([sqlite.org/cves](https://www.sqlite.org/cves.html)).
- **Pseudonym key (§8.9).** Kept in the same `case.key` blob, so one deletion destroys both the DB key and the pseudonym key.

### What is and is not protected

| Protected | Not protected |
|---|---|
| All case DB pages, journal/WAL and temp files: ciphertext at rest, and unreadable without the user's DPAPI context ([adiantum README](https://github.com/ncruces/go-sqlite3/blob/main/vfs/adiantum/README.md)). | **Tamper detection on the DB.** Adiantum and XTS are length-preserving: an attacker with write access can corrupt pages undetectably, and the result decrypts to garbage rather than failing a MAC. Mitigations: page checksums, `quick_check` at open, and the manifest. Our threat model is a lost or stolen disk, backups or another local user, not an active attacker as the same user. |
| Small-state files: confidentiality plus integrity (DPAPI MAC). | **Change patterns.** Encryption is deterministic per page, so two snapshots of the DB reveal which 4 KiB blocks changed. Acceptable: imports are write-once. |
| Keys at rest: DPAPI-wrapped and bound to the user and machine. | **Same-user malware.** Anything running as the user can call DPAPI or read process memory. This is outside any local tool's reach. |
| | **Metadata.** File sizes, file times and the case folder name are visible. Case IDs must be random, never derived from a client name or host. |
| | **Super-journal files** (multi-DB transactions) are not encrypted by the VFS. Never ATTACH a second DB inside a transaction. |

### Integrity and atomicity

- **Imports.** One import file maps to one SQLite transaction. A file that fails a limit is refused whole (§6.7), so no partial imports occur.
- **Idempotency.** The manifest records the source SHA-256 before commit, and a re-import of the same hash is a no-op.
- **On open.** Run `PRAGMA quick_check` (O(N); [pragma docs](https://www.sqlite.org/pragma.html)) and check manifest row counts against `COUNT(*)` per importer. Offer a full `integrity_check` from the case's maintenance menu. A failure marks the case "damaged, re-import required", and its evidence is never silently used.
- **Tier (a).** Writes stay whole-file temp + fsync + `MoveFileEx(WRITE_THROUGH)`. The ledger switches from `os.Rename` to the same Windows replace helper that history uses.

### Crash safety

- SQLite in WAL mode with `synchronous=FULL` on import commit survives power loss: the transaction either committed or never existed.
- A crash mid-import leaves no rows, because the transaction rolled back. The manifest entry is written in the same transaction (a `manifest` table) and mirrored to `manifest.dat` after commit.
- Tier (a) uses the atomic rename. The old file remains valid until the replace completes.
- **Windows WAL bug.** A WAL corruption bug triggered by several pooled writers on Windows ([issue #404](https://github.com/ncruces/go-sqlite3/issues/404)) was fixed upstream. A single-connection policy avoids that code path entirely.

### Erase and secure delete

- **Erase case**, in this order:
  1. close all connections
  2. overwrite `case.key` with random bytes, flush, and delete it (crypto-erase)
  3. delete `case.db`, `-wal`, `-shm`, `manifest.dat` and `quarantine\`
  4. strip ledger evidence refs to "case erased" (§8.6)
  5. delete exports of that case
- **Why crypto-erase.** On SSDs, overwriting a file does not reliably reach the flash cells: wear levelling and over-provisioning keep stale copies. Destroying the key is the approach NIST SP 800-88 Rev. 2 recognises ([NIST](https://csrc.nist.gov/pubs/sp/800/88/r2/final)). The DB pages left in flash are ciphertext under a key that no longer exists anywhere.
- **What the UI says, and only this:** "Case data deleted and its encryption key destroyed. Remaining fragments on disk are unreadable." Never "securely wiped".
- **Caveats to document:**
  - DPAPI blobs captured in a backup or volume shadow copy before erase remain decryptable by that user. This is why backups are excluded (below).
  - A Windows system restore point could hold an old `case.key`.
- **Row-level deletes inside a live case** (retention trimming) rely on `secure_delete=ON`, which zeroes freed content, followed by `VACUUM`. The WAL may briefly hold old pages until the next checkpoint. Run `PRAGMA wal_checkpoint(TRUNCATE)` after a delete. The [pragma docs](https://www.sqlite.org/pragma.html) give no guarantee for journal/WAL remnants, and the remnants are encrypted anyway.

### Backup

- **No automatic backup.** Case data is re-derivable from the original source files, and the user keeps those.
- **Export.** "Export case" writes a portable archive encrypted with a user-chosen passphrase (Argon2id + AES-GCM), because DPAPI blobs do not move between machines or users. This is an optional later feature.
- **Tier (a) export.** Connections export without secrets, matching current behaviour. The ledger exports as reviewed text through the existing ledger export path.
- **Cloud sync.** `%APPDATA%\Roaming` can sync with roaming profiles. DPAPI handles roaming keys, but case data should live under `%LOCALAPPDATA%` so multi-GB case files never roam. Small-state files stay in Roaming, which is the current location.

## Migration from today's files

- **`connections.dat`.** No change.
- **`ledger.json` (plaintext) to `ledger.dat` (DPAPI):**
  1. On first start of the new version, if `ledger.json` exists and `ledger.dat` does not, load `ledger.json` and validate it with the existing identifier guards.
  2. Seal and write `ledger.dat` atomically, then read it back and compare.
  3. If they match, overwrite `ledger.json` with zeros, fsync and delete it. The same SSD caveat applies, but the ledger by contract holds no row data or identifiers.
  4. If validation or the read-back fails, leave `ledger.json` untouched, log a contents-free message, and keep running from the plaintext file until the next start.
  5. The migration is guarded by the existing named mutex, so two instances cannot both migrate.
  6. `cmd/audit-explorer/main.go` changes its ledger path to `ledger.dat` and passes the DPAPI protector.
- **Preferences.** New `prefs.dat`, absent means defaults. No migration needed.
- **Evidence.** There is no existing evidence store, so it starts on the new design. Add a `schema_version` row in each case DB with forward-only migrations, run inside a transaction on open. A case from a newer version is refused read-only with a clear message.

## Size caps

| Item | Cap | On breach |
|---|---|---|
| `connections.dat` | 20 records, 1 MiB (existing) | refuse write |
| `ledger.dat` | 200 hypotheses, 8 MiB (existing) | refuse write |
| `prefs.dat` | 64 KiB | refuse write |
| Single import file | per-importer limit (§6.7); start at 2 GiB source / 10 M rows | refuse whole file, move to quarantine |
| Case DB | 4 GiB, configurable; checked before each import from the source size × measured expansion ratio, and after each import | refuse import |
| All cases together | 20 GiB, plus a free-disk floor of 2 GiB | refuse import, offer to erase closed cases |
| Retention | closed cases auto-erased after 30 days (§6.7) | crypto-erase as above |

Use `PRAGMA max_page_count` as a hard stop inside SQLite in addition to the pre-check. A breach rolls back the import transaction.

## Test plan

- **Unit, with a fake Protector** (exists for history):
  - Ledger seal/unseal round trip.
  - Tampered blob is rejected.
  - Wrong entropy is rejected.
  - Migration: plain to sealed, plaintext removed; failed read-back keeps plaintext; a second start is a no-op.
- **Case store:**
  - **Create, import, query:** a range query and an overlap join return the same counts as a brute-force in-memory scan over generated events, with edge cases at interval boundaries and events longer than `maxDur`.
  - **Wrong key:** returns a clear error and leaves no plaintext temp files.
  - **No key in strings:** grep for the hex key in DSNs, error strings and logs; it must be absent.
  - **Crash:** kill the process mid-import (a test helper subprocess), then reopen. `quick_check` must pass and rows must be zero or complete.
  - **Corruption:** flip a byte in `case.db`. Open must report "damaged", via a checksum or `quick_check` failure.
  - **Caps:** an over-cap file is refused whole and nothing is committed; `max_page_count` triggers a rollback.
  - **Erase:** afterwards `case.key` is absent and the DB files are gone. The ledger shows "case erased". A copy of `case.db` taken before erase cannot be opened without the key.
  - **Plaintext absence:** search the raw `case.db`, `-wal` and temp dir for a known message marker; it must be absent.
  - **Concurrency:** two app instances must not corrupt a case, through the named mutex per case plus SQLite locking.
- **Windows CI:** run the case-store suite on Windows (WAL plus `MapViewOfFile` path), including a soak loop of 1,000 open/import/close cycles.
- **Benchmark guard:** keep the 2M-event benchmark as a `-tags bench` test, and fail if the join is over 500 ms or peak private memory is over 300 MiB.
- **Build guard:** CI asserts `CGO_ENABLED=0` builds and that the exe size stays under budget.

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| ncruces encrypting VFS is younger than SQLite itself; its README notes it is less battle-tested | medium | Single connection; Windows soak tests; pin the version; modernc + app-level GCM fallback behind the same `CaseStore` interface |
| No tamper MAC on DB pages | low (threat model) | Checksums, `quick_check`, manifest counts; document the limit |
| Library is effectively single-maintainer | medium | MIT licence, vendorable; SQLite format is standard, so a case DB can be decrypted and dumped to plain SQLite for any other engine |
| Exe grows about 11.5 MB (about +55%) | certain | Acceptable for a single exe; optionally strip with `-s -w` (cuts 40%) |
| DPAPI key lost (profile reset, machine change) makes cases unreadable | low | Cases are re-importable from source; state this in the UI; optional passphrase export later |
| Wasm-translated SQLite is CPU-slower than native | low | Measured: well inside interactive budgets at 2M rows |
| Upstream SQLite CVE | low | Defensive mode, no untrusted SQL or DB files, no extensions; bump ncruces on SQLite security releases |
| Shadow copies or restore points retain old key blobs | low | Documented in erase wording; case data under `%LOCALAPPDATA%` |

## Sources

- ncruces/go-sqlite3: https://github.com/ncruces/go-sqlite3 (releases: https://github.com/ncruces/go-sqlite3/releases)
- Adiantum VFS: https://github.com/ncruces/go-sqlite3/blob/main/vfs/adiantum/README.md
- XTS VFS: https://github.com/ncruces/go-sqlite3/blob/main/vfs/xts/README.md
- Windows WAL issue: https://github.com/ncruces/go-sqlite3/issues/404
- modernc.org/sqlite: https://pkg.go.dev/modernc.org/sqlite
- mattn/go-sqlite3: https://github.com/mattn/go-sqlite3
- bbolt: https://github.com/etcd-io/bbolt
- Pebble: https://github.com/cockroachdb/pebble and https://www.cockroachlabs.com/docs/v24.1/security-reference/encryption
- Badger: https://github.com/hypermodeinc/badger and https://docs.dgraph.io/v24.1/admin/enterprise-features/encryption-at-rest
- DuckDB Go: https://duckdb.org/docs/current/clients/go/troubleshoot
- Pogreb: https://github.com/akrylysov/pogreb
- BuntDB: https://github.com/tidwall/buntdb
- SQLite CVE policy: https://www.sqlite.org/cves.html
- SQLite pragmas (secure_delete, quick_check, cell_size_check, max_page_count): https://www.sqlite.org/pragma.html
- DPAPI: https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata
- NIST SP 800-88 Rev. 2 (media sanitization, crypto-erase): https://csrc.nist.gov/pubs/sp/800/88/r2/final
