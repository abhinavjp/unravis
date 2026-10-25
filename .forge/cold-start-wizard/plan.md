# Plan: Cold-start start screen and create wizard (detailed)

Work item: `cold-start-wizard`. Branch: `feat/cold-start-wizard` (master is protected).
Mode: detailed (decisive factors: several systems at once — discover, case store schema, HTTP API, Svelte UI; a schema migration of encrypted case data; security-sensitive probe endpoint).

## Source basis
- Goal: replace the current "Landing + Import side panel" cold start with a three-card start screen (use an existing case, enter an existing case, create a new one) and a large, beginner-friendly create wizard. One case holds everything, in the per-case encrypted SQLite. Sources are tagged Environment › Portal › API. **Everything the scan offers must be provably readable**: a finding is shown as ready only after Unravis has checked the importer can read it, and a finding that needs a connection must lead to a connection that can be reached.
- Spec: `docs/superpowers/plans/2026-10-25-cold-start-wizard-criteria.md` (v4, decisions Q1–Q14, section G "what the scan can and cannot know") and the signed-off wireframe `.dev/prototypes/coldstart.html` (layout B "Workspace"; wireframe is the visual contract, not code to copy).
- Code map: gathered 2026-10-25 from `internal/discover`, `internal/importer`, `internal/casestore`, `internal/server`, `web/src` (facts quoted in the tasks).
- No `context.md`/`decisions.md` exist for this work item; the criteria doc's "Decisions" blocks play that role. If the criteria doc and a task disagree, that is a blocker (shared contract, conflict rule).

## Scope
In: start screen; restore by 26-char code; create wizard (name, environments, sources, review); scan verification; folder-name suggestions; log4net locate; reachability probe; tags + environments in the case store; Mongo database classification; Hangfire/Redis/other exports through the existing file importers; migration of existing cases.
Out (later phases, recorded in the criteria doc Q6): Blackbox recorder, pack for other servers, live SQL/Redis reads (no drivers in `go.mod`), Hangfire-from-SQL, Linux, disk/network metrics, remote scanning of other computers (Q13), reading IIS site/app names (privacy ruling D3 stays).
Also in (user decision D-1, 2026-10-25: "protect with master key"): a **master key** — one passphrase the user chooses for this PC — protects the case keys (optional; without it the Windows-DPAPI sealing stays), so a case can be exported to one encrypted file and restored on another PC by typing the master key it was exported with (criteria items 2, 10, 13). Restore always creates a new case. Phase 03 builds this; the start screen (phase 04) and its case-file option depend on it.

## Requirements and invariants (canonical IDs)
| ID | Requirement | Satisfied by |
|---|---|---|
| R1 | Start screen always shows three cards: Use an existing case, Enter an existing case, Create a new one | TSK-13 |
| R2 | Enter a case by its 26-char code; invalid and unknown codes give one identical, non-leaking message | TSK-14 |
| R3 | Restore from an exported case file creates a new case (needs the master key the file was exported with, and a master key set and unlocked on this PC) | TSK-12, TSK-14 |
| R15 | A master key (passphrase) protects every case key; set once, asked once per launch; an existing DPAPI-sealed case keeps opening and can be re-wrapped; a wrong or forgotten key never reveals data and there is no recovery | TSK-09, TSK-10, TSK-11, TSK-16 |
| R16 | The case file is one encrypted file; it contains no plaintext data and no machine-bound secret | TSK-12 |
| R4 | Environments are free-text, optional, comma separated (no built-in Production/Staging) | TSK-17 |
| R5 | Every source carries Environment › Portal › API tags, stored in the case store; blank = "not named" | TSK-06, TSK-07, TSK-19 |
| R6 | Scan covers this PC only; a finding is "ready" only after a readability check passed | TSK-01 |
| R7 | Folder names are offered as suggestions (never auto-filled) | TSK-02, TSK-18 |
| R8 | log4net/NLog finding whose folder cannot be derived asks for it; once located it is re-checked and importable | TSK-03, TSK-18 |
| R9 | Another computer is reached by address (Mongo live test) or by files; SQL/Redis/IIS/app log/file connections work by files | TSK-18, TSK-19 |
| R10 | Each Mongo database is classified API audit logs / other data / not used | TSK-08, TSK-18 |
| R11 | A found service must lead to a reachable connection: the form is pre-filled from the scan and a reachability check says yes or why not | TSK-04, TSK-18 |
| R12 | Hangfire is imported from a CSV the user chooses; Quartz stays hidden | TSK-18 |
| R13 | Cases created before this change still open and read identically | TSK-06, TSK-20 |
| R14 | No credentials in case tags, logs, errors or URLs; every new route needs the session token; probes are rate limited | TSK-04, TSK-07, TSK-08, TSK-11, TSK-21 |

Invariants (hold in every phase):
- I1 The scan never sends file paths to the browser beyond what it already sends, never reads file contents beyond a bounded head sample, and never opens network connections (ruling D8). The new probe is a separate, user-triggered route.
- I2 Imports still go through `postDiscoverImports` / `postImport`; no second ingest path.
- I3 Existing tests stay green: Go `go test ./...`, web `npm test` (1005 passing at start), `npm run check`.
- I4 UI copy stays beginner-clear with the ELI10 layer; the existing design decisions (Terminal theme with IBM Plex, filters "Group rail", drill-down "Navigator") are not touched.
- I5 The master key and anything derived from it are never written to disk in plain form, never logged, never returned by any route, never put in a URL, and zeroed after use. The only place the passphrase travels is the body of the set-up and unlock requests.

## Phases and dependency graph
```
P01 trust-the-scan ──┐
P02 case-model ──────┼─> P04 start-screen ──> P05 create-wizard ──> P06 harden-and-ship
P03 master-key ──────┘
```
- P01, P02 and P03 touch different packages and may run in parallel; the one shared file is `internal/casestore/sqlite/` (P02 schema v4, P03 key file v2). Run TSK-06 before TSK-10, or serialise them.
- P04's TSK-13 needs TSK-05 (P01 client types), TSK-06 (P02 counts) and TSK-11 (lock status); P05 needs P01–P04 and is sequential inside.
- Executable frontier at start: TSK-01, TSK-02, TSK-04, TSK-06, TSK-09 (TSK-03 after TSK-01; TSK-05 after TSK-01..04; TSK-07/08 after TSK-06; TSK-10 after TSK-06 and TSK-09; TSK-11 after TSK-10; TSK-12 after TSK-10 and TSK-11, because its routes sit behind the lock gate). Task IDs are stable names, not execution order.
- Task index: P01 TSK-01..05 · P02 TSK-06..08 · P03 TSK-09..12 · P04 TSK-13..16 · P05 TSK-17..19 · P06 TSK-20..21. Task files are numbered `NN-slug.md` under each phase directory.

## Integration points
- P01 → P05: `Finding` gains `verified`, `suggest`, and a locate route; the wizard renders them (contract in TSK-01/02/03).
- P02 → P05: tags and `case_sources` API (contract in TSK-07/08); wizard writes them.
- P03 → P04: `GET /api/master-key/status` (`none | locked | unlocked`) and `POST /api/master-key/{setup,unlock,lock,rewrap}`; `POST /api/cases/export` and `POST /api/cases/restore` (contract in TSK-11/12). Case routes answer 423 `locked` while the key is locked.
- P04 → P05: start screen routes "Create a new one" into the wizard component.

## Change policy and commit policy
```yaml
commit_granularity: phase
history_style: separate
```
A task may change files only inside its Write scope. A needed change outside it is a deviation recorded in `progress.md`; a change to a signature another task consumes is a blocker. Never push. Every commit is preceded by a stop.

## Cross-phase proof
- A scanned IIS/log4net/Mongo-log finding goes ready → ticked in the wizard → case created → import job finishes → events visible in the analysis, with tags (end-to-end, TSK-21).
- An old case created on master opens on the new build (TSK-20).

## Final acceptance
0. With a master key set, a fresh launch asks for it once; a wrong key opens nothing; an exported case file restores on a second data folder (simulating another PC, with its own master key) when given the export key, as a new case, and does not restore with a wrong key.
1. Start screen shows three cards; each works.
2. A new case can be created through the wizard against a real scan of this PC, with environments typed freely, sources tagged, and data imported.
3. No finding is shown as ready that its importer cannot read (proved by tests with an unreadable and a mislabelled file).
4. A log4net finding with an underivable folder can be located and imported.
5. A Mongo connection can be tested and its databases classified; classification persists with the case.
6. Old cases open. All suites and build green; clumsy-user review done with no open high finding.

## Conditional controls
- Rollback/compat: TSK-06 migration is additive (new nullable columns/tables only; schema version 3 → 4); reverting the binary leaves v3-readable data. Proof in TSK-06.
- Rollback/compat for the master key: key file v2 is additive; a case still sealed by DPAPI keeps opening; re-wrap is per case and atomic (write new `case.key`, verify it opens, then replace); a failed re-wrap leaves the old key file intact. `case.db` is never rewritten.
- Security: TSK-04 (probe), TSK-09/10/11 (key derivation, wrapping, lock gate) and TSK-21 (review). Key derivation uses Argon2id from `golang.org/x/crypto` (already an indirect dependency; promoting it to direct is a `go.mod` change to record). Unlock attempts are rate limited and delayed after failures. Reachability (TSK-04) only dials a host:port the user typed or the scan's local host, 3 s timeout, no response body returned, rate limited like `/api/connections/test`.
- Data-loss warning (must be shown in plain words at set-up): a forgotten master key cannot be recovered; cases protected by it and case files made with it stay unreadable. Set-up requires typing the key twice and ticking that acknowledgement.
- Manual/live proof: TSK-21 browser pass against `http://127.0.0.1:18770/` (demo) and `18771` (live), per `.dev/browser-pass.md`. No SQL Server, Redis or IIS server exists on this machine; their live probes are `UNMEASURED` and must be reported as such, never as PASS.
- Idempotency: locate/verify routes are repeatable and side-effect free on the case store.

## Decisions and open points
- D-1 (decided by the user 2026-10-25): protect with a master key. Rulings that follow from it, each reversible before TSK-09 starts: (a) one master key per PC, not per case; (b) it is a passphrase of at least 12 characters, stretched with Argon2id into a 32-byte wrapping key, which seals `case.key` and `manifest.dat` through the existing `sealedfile.Protector` seam; (c) unlock lasts for the running session only, nothing remembers the key on disk (no "remember me" in this release); (d) no recovery key; (e) setting the master key is optional: with none set, today's DPAPI behaviour is unchanged; (f) the case file is `case.db` plus the case keys and manifest in one container; its header is encrypted with a key derived from the exporting master key and a fresh per-file salt (each PC's own `masterkey.dat` salt differs, so the file cannot rely on it); restore asks for that key, then re-wraps under the destination PC's master key and creates a new case id.
- D-2 (ruled): wireframe `Several, I will say per item` environment option is kept; per-item env field then applies.
- D-3 (ruled): SQL Server and Redis "connections" in phase 1 mean their log files on this PC or exported files from another PC, plus a reachability check; no live reads (no drivers).

## Phase index
| Phase | Risk | Outcome |
|---|---|---|
| 01-trust-the-scan | medium | scan findings are verified, suggest names, locatable; probe route |
| 02-case-model | high | tags, environments and classified sources live in the encrypted case store |
| 03-master-key | high | master key protects case keys; case file export and restore |
| 04-start-screen | medium | three-card start screen, set-up/unlock screen, restore by code or case file |
| 05-create-wizard | medium | the visual wizard end to end |
| 06-harden-and-ship | medium | regression, migration, security, clumsy-user review, rebuild and restart |
