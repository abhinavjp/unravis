# Unravis post-MVP roadmap, revision 8: adaptive, evidence-gated

- **Date:** 2026-10-24
- **Status:** **Proposal only. Revision 8 supersedes revision 7.** Nothing in this document is built, granted or scheduled. It is not permission to deploy, to grant access, or to execute any phase.
- **Supersedes:** revision 7 (2,885 lines), kept unchanged at `docs/superpowers/plans/archive/2026-10-24-unravis-post-mvp-roadmap-rev7.md`. Rev 7 stays a **design reference** for rule definitions, query templates and the evidence record (its §3.1, §3.3, §3.5 and §3A–§16 bodies). Where rev 7 and this document disagree, **this document wins**. §12 lists exactly what is overridden.
- **Inputs:** the independent review `unravis-roadmap-deep-review.md` (verdict CHANGE; blockers C1–C8; gates A–I; access verdicts; T0–T3), its filled decision sheet `unravis-roadmap-reviewed-plan.md`, and primary-source checks recorded in §9.
- **Scope of this revision:** correct dependencies, ordering, scope, permissions and acceptance criteria. No code, no permission change, no phase execution.

## How to read this document

1. §1 is the answer: what changed, what you must decide, what needs someone else's approval.
2. §2 separates what **exists today** from what is **proposed**. Anything not in §2.1 is proposed.
3. §3 and §4 are the audit trail: every review finding and every phase row, with accept / change / challenge and the reason.
4. §5–§8 are the plan itself: the T0–T3 sequence, the gates, the access matrix, the slices.
5. §9 records which technical claims were verified against vendor documentation, and which are still open.
6. §10–§12 are decisions, open items and what stays from rev 7.

**Conventions kept from rev 7.** Every threshold is a tunable default. Evidence labels are exactly one of **Symptom**, **Correlation**, **Strong correlation**, **Proven cause**. `[unverified]` marks a claim no one has confirmed against a primary source or the live system. "Dubai" is Asia/Dubai (UTC+4). Audit timestamps are UTC. Rule IDs (A, B, S, M, H, W, I, C, AI, HP) keep their rev 7 meaning unless §11 says otherwise.

---

## 1. Summary

### 1.1 What changed in one paragraph

Rev 7 was a fixed 14-phase build order with a theory engine, a node, 19 MCP tools and broad read grants. The review found it unsafe to build as sequenced: some of its core mechanics are not correct (`_id` pruning, "non-SQL CPU", the Mongo join, one `AccountId` contradiction), it asks for permissions the incident does not yet justify, and it builds before the cheapest evidence has been collected. Rev 8 replaces the order with **four tiers (T0–T3) that are entered only when the previous tier's evidence says so**, keeps phase IDs only so rev 7 text stays traceable, cuts two items outright (P2b as a standalone engine, N1 node), defers three (P10 Redis, P11 .NET, P12 log4net), and re-scopes the other thirteen into small slices with a stated question, a falsification test, an overhead budget and the privileged operations each needs.

### 1.2 Outcome

| Outcome | Count | Items |
|---|---:|---|
| Approve | 2 | G (instrumentation backlog principle only); standing evidence preservation |
| Change | 13 | P0, P1, P2, P3, P4, P5, P6, P7, P8, P9, P13, decision 14, decision 15 |
| Cut | 2 | P2b as a standalone engine/screen; N1 node |
| Defer | 3 | P10 Redis, P11 .NET runtime, P12 log4net |
| **New (your request, 2026-10-24)** | 1 | **P14** in-app guided tour and plain-language help for non-technical users (§8.14); plain-language copy is also a requirement on every slice card (§5.3) |

**The roadmap is no longer a sequence to approve.** It is a set of gated slices. Approving rev 8 means approving the **T0 no-code actions, the gates, and the slice-card process**. It does not approve any slice's build or any access grant (§8).

### 1.3 Where rev 8 differs from the reviewer (challenges kept)

The review is accepted on all eight blockers. These are the places rev 8 deliberately does **not** follow it literally:

| # | Reviewer position | Rev 8 position | Why |
|---|---|---|---|
| X1 | Cut the theory screen; move guards and a manual ledger into P1/P2 | **Cut the engine. Keep a thin analyst-set ledger with deterministic *guards* that can block "Supported" but never compute a verdict**, shown on one small static screen | The ledger is what turns manual notes into something the next incident reuses. Guards (coverage, independent episodes, source sanity, confounders) are the review's own §8 asks. The user's own feedback ("What is known and what is not" screen) is preserved in its simplest form. No permutations, no history, no LLM endpoints. |
| X2 | Offline `.xel`/`.blg` import | **Import CSV/JSON/text that Ops or the DBA produces with a documented command; do not write native `.xel` or `.blg` readers** | Microsoft's `.xel` reader is a .NET library and `.blg` needs `relog`. A pack of documented export commands is cheaper, auditable by the owner, and avoids reading binary formats in Go. A native reader is only reconsidered if exports prove impractical. |
| X3 | Replace or validate `_id` pruning | **Do not use `_id` bounds for authoritative results. Use them only as a calibrated narrowing hint, labelled, with an exact (indexed or off-peak) mode available**; recommend a DBA-owned `RequestInitiatedAt` index as a separate change request | The bound can be calibrated cheaply from an **indexed** sample of recent documents (the `_id` index is the one index that exists). That gives evidence, not a guarantee, so the output must say so. |
| X4 | Hangfire schema SELECT: reject as written | **Reject live; accept a DBA-run export of a fixed field list** (cron expression, queue, next-execution), no job arguments | Schedule evidence is needed (B3 recurrence). A fixed export gives it without schema access. |
| X5 | Elevated P0: reject | **Reject elevation and discovery for the first release. Hints-only is not code either until T0 produces the map.** The hints file is a small validator and a display of provenance. | Avoids building a parser for a map that may be one page of Ops notes. |
| X6 | 19 MCP tools unjustified | **Hold at the 10 existing audit tools. Add at most three read-only evidence tools, only after the evidence schema has been used by hand in two incidents.** | The MCP surface should follow the schema, not lead it. |

### 1.4 Decisions needed from you (§10 has the full register)

1. **Accept the reversals of previously accepted decisions** (2, 6, 7–10, 11, 14, 15). Each was accepted against rev 7's fixed order; the review gives reasons they no longer hold (§10.1).
2. **Confirm client authorization** for the T0 preservation and Ops-run counters. Preservation is an Ops action under client authority; this team cannot self-authorize it (§6.1).
3. **Decide whether the per-client access matrix (§7) is the approval vehicle**, so each source is approved separately with an expiry rather than as a bundle.
4. **Decide the data-handling position for external AI** (decision 14): none by default; per-client written approval before any case export reaches an external model (§7.4).

### 1.5 Approvals this document requests from others (none is granted by writing it)

Ops: T0 preservation and DCS. DBA: SQL exports, optional narrow grants, any index or Query Store change. Mongo owner: slow-op log copy, optional `read` scope review, optional index. Client security: any external-AI transfer. Azure owner: Log Analytics read or a KQL export. See §7 for the exact ask, the refusal fallback and the expiry for each.

---

## 2. Existing functionality versus proposed work

### 2.1 What exists today (the MVP, verified against the repository on 2026-10-24)

| Area | Fact | Where |
|---|---|---|
| Runtime | Go backend bound to `127.0.0.1`, Svelte 5 dashboard embedded; session token in memory; `--demo` and live modes | `README.md` |
| Source | MongoDB `ApiAuditLog` only. Server 7.0 uses the modern driver; 3.4 is handled by a legacy driver with record and byte limits | `README.md`, `internal/store/legacy_*` |
| Access posture | Read-only queries; documented advice is a `read`-only user on the audit database and collection; the tool never creates indexes | `README.md` |
| Time filter | Matches `RequestInitiatedAt` (or `CreatedOn` when it is absent). **No `_id` pruning.** Only the `_id` index exists on the sandbox, so each query scans | `internal/store/pipeline.go:99`, `requests.go:334` |
| Cost controls | 5-minute operation deadline; 4 operations overall, 2 per source; 100-row pages; 16 MiB aggregate cap; 10,000-row CSV cap. Modern path sets no explicit `maxTimeMS`; the legacy path derives one from the remaining deadline. **No `appName` is set on any client.** Server-selection timeout is the 5-minute operation deadline, not the driver default | `internal/store/mongo.go:24-29`, `legacy_client.go:341-361` |
| Views | Comparison of a period against a preceding reference; P50/P95 from log-bucket histograms; request volume by API; traffic-versus-latency scatter; slowest requests with stable cursors; nearby activity (≤ 24 h, ≤ 500 records); breakdown dimensions; CSV export of metadata only | `README.md` |
| Statistics | Low-n masking (n < 20); Mann-Whitney z in `internal/audit/significance.go`; Little's-law concurrency estimate | `internal/audit/*` |
| MCP | `--mcp` mode reuses the running dashboard's sources. **Ten tools:** `audit_get_guide`, `audit_list_sources`, `audit_compare`, `audit_get_groups`, `audit_get_timeline`, `audit_compare_timelines`, `audit_get_requests`, `audit_get_slowest`, `audit_get_breakdown`, `audit_get_activity`. No payloads, raw URLs, IPs or connection changes | `MCP.md` |
| Payloads | Read only when a single request is opened; capped at 256 KiB; CSV never includes payload bodies | `README.md` |
| Credentials | DPAPI-encrypted recent connections; passwords never returned to the browser | `README.md` |
| Incident analysis | **Manual, outside the product:** the `sql/` scripts and outputs, `sql/report.src.html` (built by `sql/build_report.js`). These are the reference for every proposed rule | `sql/` |

### 2.2 Facts about the incident environment that constrain the plan (from `sql/`, not the product)

| ID | Fact | Constraint it creates |
|---|---|---|
| E-mongo | Mongo 7.0.16, about 778,618 documents (probe), oldest 2025-10-31, newest 2026-10-23; **only `_id_` index**; instance also hosts four `*_Live` client databases | Every un-narrowed query scans; the instance is shared with live client data. Any extra load is not "sandbox only" |
| E-sql | SQL Server 2022 (16.0.1000.6), Query Store off on the sandbox, server clock UTC+1; SQL CPU read zero for 70 days in `system_health` | The zero is a measurement question before it is a finding (C2) |
| E-acct | The probe shows an `AccountId` field present but `null` in the last 20 documents; the schema doc does not list it, and says only that Hangfire URLs embed `AccountId` text | Tenant attribution cannot be assumed (C4, §6.3) |
| E-names | `sql/mongo_stalls.js` reads `PreActionFilterTotalMillseconds` and `PerActionFilterTimings`, both misspelled; the probe shows the real names `PreActionFilterTotalMilliseconds` and `PreActionFilterTimings` | Any earlier "pre-filter is unused/null" conclusion drawn from those scripts is unverified until rerun with the right names |
| E-env | The audit evidence comes from the sandbox audit database; patterns may differ in a client's live tenant | Findings are labelled with the source database; no cross-tenant inference |

### 2.3 What is proposed (everything else in this document)

Everything in §5–§8 and §11 is **proposed, unbuilt and unapproved**. The product today contains none of: `_id` margin logic, coverage probes, import commands, host hints, an evidence record, a ledger, case export, evidence MCP tools, any source other than Mongo, any node or agent.

---

## 3. Disposition of every review finding

Legend: **Accept**, **Change** (accepted with a different remedy), **Challenge** (partly or wholly not followed). "Where" points to the rev 8 section that implements the disposition.

### 3.1 Correctness blockers

| ID | Finding | Disposition | Rev 8 remedy | Where |
|---|---|---|---|---|
| C1 | `_id` pruning is not a completeness guarantee: ObjectId time is whole seconds, generated by the client, not monotonic across hosts, and the audit row is written in two stages | **Accept** | No `_id` bounds in authoritative output. Calibrate a margin from an *indexed* recent sample (distribution of `_id` time minus `RequestInitiatedAt`), use it only as a hint, print "bounded by margin M, calibrated on N rows; rows outside M would be missed". Offer exact mode (full `RequestInitiatedAt` match) with `maxTimeMS`. Recommend, as a separate DBA change, an index on `RequestInitiatedAt` | §8.2 (P1a), §6.4 |
| C2 | Flat-zero SQL CPU makes "non-SQL CPU" unsafe | **Accept** | B13 and S4 are relabelled **"overall host CPU; SQL attribution unknown"** until T0.3 validates the zero against PDH. W7 (named process) only from counter logs with per-process data | §6.2, §8.5 |
| C3 | Mongo M1 must join to the pre-action audit-create interval, and absence from the slow-op log is not exculpatory | **Accept** | M1 joins a slow `insert` to **`[RequestInitiatedAt, CreatedOn]`** of a B6 request (not the request end). "No slow-op entry" is "not observed", never "ruled out": the slow log only records operations above `slowms`, and client-side connection-pool or server-selection waits never reach the server log | §8.8 |
| C4 | `AccountId` contradiction between schema doc and probe | **Accept** | Treat `AccountId` as an **optional, unverified** field. P1a measures its non-null rate per kind and period; A2 runs only above a coverage threshold; otherwise tenant attribution reads "unavailable". T0.6 reads the writing code. The schema doc gets a correction proposal | §6.3, §8.2 |
| C5 | "Read-only" is not production-harmless (`ApplicationIntent=ReadOnly` is routing only, `NOLOCK` dirty reads, server-side timeouts and caps needed) | **Change** | Drop blanket `NOLOCK` and drop reliance on `ApplicationIntent`. Every query template carries: a row cap, a server-side timeout, a measured cost recorded in a lab or restored copy, an `appName`, per-source concurrency 1, and a kill switch. `LOCK_TIMEOUT` and `DEADLOCK_PRIORITY LOW` stay (cheap, correct). `NOLOCK` only where a template documents why a stale read is acceptable | §6.5 |
| C6 | Clock and join quality model; IIS `time-taken` includes network time | **Accept** | Every join declares a tolerance (source resolution + measured skew + method) and reports a match class (unique, ambiguous, none). IIS residual is "time outside the pipeline", never "queue"; unmatched IIS or audit rows are never "lost audits" without validation | §6.2 |
| C7 | The theory engine and its statistics are unsound (Holm family depends on the selected run set; permutations invalid under volleys; the 43 stalls are not independent) | **Accept for the engine; Challenge for the ledger (X1)** | No verdict engine, no Holm/BH, no permutations. Units of inference are **independent episodes**, not requests; windows are predeclared. A thin ledger with guards remains | §8.6, §10.2 |
| C8 | Export privacy: allowlist, minimization, preview and consent; external LLM is a data transfer | **Accept** | Field-level export allowlist (no payload, URL, query text, IP, raw user id by default); keyed pseudonyms with a per-case key that is never exported; thin-cell masking; preview and explicit confirm before any export is written; external AI only with per-client approval | §8.9, §7.4 |

### 3.2 Revision-8 instructions from the review (§8 of the review)

| Review instruction | Disposition | Where |
|---|---|---|
| Replace fixed releases with T0–T3; keep phase IDs for traceability | Accept | §5 |
| Split P1 into correctness/safety and optional 30–90-day analytics | Accept | §8.2 |
| Reword the CPU and scheduled-work causes as hypotheses | Accept | §6.2 (labels); rule rewording in §11 |
| Correct M1 | Accept | §8.8 |
| Delete P2b standalone and N1 | Accept (P2b keeps its ledger and guards, X1) | §4, §8.6 |
| Per-source ask / approve / refuse / offline fallback / expiry | Accept | §7 |
| Each slice states the question, falsification, overhead, privileged ops, retention, positive and negative demo | Accept | §5.3 (slice card) |
| Add a stop-building rule | Accept | §5.4 |

### 3.3 Access verdicts

Accepted as stated except where §7 notes a challenge (X4 Hangfire export; msdb as a DBA-owned view or export; `VIEW SERVER STATE` conditional). See the full matrix in §7.


---

## 4. Disposition of every phase and decision row

"Tier" is where the slice first becomes eligible (§5). Eligible is not approved: each slice needs its own slice card (§5.3) and the access it names (§7).

| Item | Review verdict | Rev 8 disposition | Tier | What changed from rev 7 | Challenge to the reviewer |
|---|---|---|---|---|---|
| **P0** Host auto-detect + hints | Change | **Hints-only.** A validated hints file plus a provenance display. No elevation, no config scans, no discovery, no agent fingerprinting. Host topology and secrets are confidential. Full discovery is re-proposed only after demand and a security review | T1 (after T0.5 produces the map) | Rev 7 §3A P0 (discovery, profile drift, elevated scan) cut | X5: not even the hints validator is built until T0.5 shows the map is more than one page |
| **P1** Audit foundations | Change | **Split.** **P1a** correctness and safety (always first code). **P1b** optional 30–90-day analytics, off by default and gated | T1 (P1a); T2+ (P1b) | `_id` pruning demoted to a calibrated hint; `AccountId` optional; `appName`, `maxTimeMS`, row caps added to every query | X3: keep `_id` as a labelled hint with exact mode, plus a DBA-owned index request |
| **P2** Incident patterns | Change | **P2-lite.** B1, B2, B6, B12 plus step/duration views, provenance and the ledger. Host-CPU recurrence (B13) only on trusted counters. No exhaustive 90-day scan | T1 | 24 rules reduced to four to start; B13/S4 relabelled; low-n/censored/incomplete rows explained | none |
| **P2b** Theories screen | **Cut** | **Cut as an engine and standalone screen.** Keeps: the analyst-set ledger, deterministic guards, V6 source-sanity, low-n/missing-data checks, simple redacted export | T1 (ledger, guards) | 19-theory verdict engine, V1–V9, bundle, permutation tests, history, LLM proposal endpoints removed | **X1**: a one-screen ledger with guards stays, because it is the carrier for what the next incident reuses and for the user's own "known and not known" request |
| **P3** SQL Server | Change | **Offline import first** of Ops/DBA exports (CSV/JSON/text with a documented command). No live connector. CPU labelled "overall; SQL attribution unknown" until T0.3 | T1 (import), T3 (live) | Live connector, CPU split and S14 deferred; S14 needs its own approval | **X2**: no native `.xel`/`.blg` readers; exports only |
| **P4** Mongo native | Change | **Persisted `mongod.log` import early** (the ~28 s audit-create signature). M1 corrected. FTDC and `clusterMonitor` probes deferred | T2 | Rev 7 M1 joined to request end; now joins to the audit-create interval. Version- and timestamp-format notes added (§9) | none |
| **P5** Hangfire + SQL Agent | Change | **Ops/DBA schedule export** early; live connector delayed. Fixed field list only, never job arguments | T2 | Live `HangFire` schema SELECT removed | **X4**: reviewer said "selected fields/views"; rev 8 pins the list, and says views are acceptable only if the DBA owns them |
| **P6** Windows host | Change | **Ops-created per-process DCS and native-log preservation at T0, before code.** Then a thin offline importer. JEA stays a separately approved option, never a prerequisite | T0 (collection), T1 (import) | Moved from a late phase to day zero; JEA decoupled | none |
| **P7** IIS + HTTP.sys | Change | **Small offline W3C/HTTPERR + WAS importer** as evidence directs. Residual labelled "time outside the pipeline", never "queue"; unmatched rows never "lost audits" | T2 | Residual definitions and match classes added; `#Fields` header always parsed; WAS gaps called out (§9) | none |
| **P8** Correlation timeline | Change | **Thin timeline when two reliable sources exist.** No composite rules, no confidence promotion, no cause statements | T2 | 9 composite rules (V/C) dropped from first release | none |
| **P9** App Insights | Change | **Verify per-client existence, sampling, retention and permissions now (T0.4). Prefer an owner-produced KQL export** over a connector. A missing sampled trace is never counter-evidence | T0 (verify), T2 (import) | Connector deferred; `sum(itemCount)` rule added | none |
| **P10** Redis | Defer | **Deferred.** Reopen only after APM, errors or counters implicate Redis. `CLIENT SETNAME` is not in a read-only allowlist | none | Removed from the plan | Fact: the verifier confirmed SETNAME is `@slow @connection`, so an ACL restricted to `+@read` rejects it; "read-only in effect" is not "read-only by ACL" |
| **P11** .NET runtime | Defer | **Deferred.** The DCS already covers .NET Framework/ASP.NET counters. Confirm the actual runtime per host first | none | Removed | none |
| **P12** log4net | Defer | **Deferred.** Preserve existing service logs at T0.1 if present; no parser until a real format and a root-cause question exist | none | Removed | none |
| **P13** Evidence API + MCP | Change | **Small stable evidence schema, provenance and a redacted offline case export** early. Hold at the existing 10 tools; add at most 3 read-only evidence tools after two hand-run incidents | T1 (schema, export), T3 (tools) | 19 tools reduced to 10 + at most 3 | X6 |
| **P14** Guided tour + plain-language help | **New (user request)** | **Interactive first-run tour, "what does this mean?" hints on every technical word, a glossary, and a guided practice case on demo data, all written for non-technical readers.** Every slice must ship its own plain-language copy to count as done | T1 (tour of what exists), then grows with each slice | Did not exist in rev 7 (only the `audit_get_guide` MCP text and README) | n/a: the reviewer did not cover it |
| **N1** Node | **Cut** | **Removed.** No on-server service, no mTLS listener. A new security and business case only if unattended native DCS demonstrably cannot diagnose repeated incidents | none | Hub/node design (rev 7 §3.9) is non-normative reference only | none |
| **G** Instrumentation backlog | Approve (principle) | **Approved as a backlog principle only.** Not authority to enable Query Store, add correlation IDs, change logging, modify app code or create XE sessions. Each is its own change request (owner, cost test, rollback) | n/a | Wording narrowed | none |
| **Decision 14** External LLM | Change | **No built-in LLM; none by default.** External AI reads approved, minimized evidence only with per-client written data-handling approval; no writable theory/narrative submission endpoints | T3 | Endpoints removed | none |
| **Decision 15** Statistics | Change | **Descriptive counts, independent episodes, predeclared windows, coverage checks.** No Holm family over user-selected theories, no fixed 9,999 permutations, no auto "supported" verdicts without calibration on realistic fixtures | T1 | §10.2 | none |
| **Standing action** Evidence preservation | Approve | **Initiate immediately under existing client authorization; Ops-owned.** Copy available 1 Sep–24 Oct now; arrange retention or collection for 25–31 Oct | T0.1 | New T0 item | Authorization is the client's, not ours: §6.1 |

---

## 5. Adaptive sequence

### 5.1 The tiers

A tier is entered only when the exit test of the previous tier says it is worth doing. Phase IDs are kept only so rev 7 text stays traceable.

| Tier | Contains | Code? | Enter when |
|---|---|---|---|
| **T0** | Preserve, collect, verify, read | **None** | Now |
| **T1** | P1a safety slice; P0 hints validator; P2-lite with the ledger and guards (B1/B2/B6/B12); offline import for P6/P3-lite; minimal P13 schema and redacted export; **P14 guided tour and plain-language help (covering what exists, extended by every later slice)** | Yes, small | T0 exit test passes |
| **T2** | Evidence-directed: P9 KQL import; P5 schedule export import; P7 W3C/HTTPERR/WAS import; P4 `mongod.log` import; thin P8 timeline | Yes, only the slices the evidence names | T1 produced at least one incident case where an unanswered question names that source |
| **T3** | Still-unresolved only: a narrow live connector; one gated instrumentation change; up to 3 evidence MCP tools; external-AI export | Yes, tailored | T2 left a specific question that offline evidence cannot answer, with the stop-building rule not yet triggered |

### 5.2 T0, no code (all Ops/DBA/owner actions; none executed or granted by this document)

| ID | Action | Owner | Why now | Exit evidence |
|---|---|---|---|---|
| T0.1 | **Preserve native logs 1 Sep–24 Oct** (SQL ERRORLOG set, `system_health` `.xel`, Windows System/Application/WAS/TaskScheduler exports, IIS W3C + HTTPERR, `mongod.log` set, existing service logs, existing monitor exports). Record SHA-256, source host, copy time, timezone mapping. Arrange 25–31 Oct retention | Ops, under client authority | Retention is short and not time-based: ERRORLOG keeps about six prior logs and a restart cycles them; `system_health` is 4 × 5 MB on a default install; HTTPERR rolls at 1 MB; TaskScheduler/Operational is disabled by default (a gap, not evidence of no tasks); Mongo rotates only on command or signal | Manifest with hashes; list of what was **not** available |
| T0.2 | **5-day per-process counter capture (DCS)** on confirmed host(s): `Process`/`Process V2` working set, % processor, handles; `Processor`; `Memory`; `Web Service`; `ASP.NET`; SQL counters; **sample interval 5–15 s, size cap, retention and rollback stated in the change note** | Ops | W7 and every "which process" claim needs per-process data; SQL CPU is flat zero today | Counter log files plus the change record |
| T0.3 | **Validate the SQL CPU zero** against PDH: read `\Process(sqlservr)\% Processor Time` and `\SQLServer:SQL Statistics\*` on the same interval as the `system_health` `scheduler_monitor` samples. Divide per-process CPU by logical core count | DBA + Ops | C2. Until this passes, "non-SQL CPU" is not a permitted phrase | One page: zero confirmed / zero is a measurement artefact / unknown |
| T0.4 | **Find existing monitoring and App Insights per client** (resource, sampling, retention per table, who can query) | Ops + client | P9 may already hold the missing evidence; missing sampled traces prove nothing | Table per client: exists/not, sampling %, retention days, query route (export or reader role) |
| T0.5 | **Host map (one page):** which host runs which role, runtime (.NET Framework vs .NET), log paths, timezone, clock sync source | Ops | P0 hints and every import depend on it | The page; confidential |
| T0.6 | **Code reads (read-only):** the audit writer (which fields are set at which stage, `AccountId`, `CreatedOn` set by whom), the Mongo driver configuration (pool size, `serverSelectionTimeout`, `connectTimeout`, `waitQueueTimeout`), any retry wrapper around the audit insert | This team, from source | C4, C3, and the cause of the ~28 s audit-create signature may be client-side | Notes with file:line citations |
| T0.7 | **Rerun `sql/mongo_stalls.js` with the correct field names** (`PreActionFilterTotalMilliseconds`, `PreActionFilterTimings`) against a read-only copy or off-peak | This team + Mongo owner | E-names: earlier "null pre-filter" conclusions are unverified | Corrected output |
| T0.8 | **Read the Query Store state** with `sys.database_query_store_options` on the sandbox (read-only, DBA runs it). Do not assume: it is on by default only for new databases from 2022, not for upgraded or attached ones | DBA | Gate for any instrumentation request | Actual state and size |
| T0.9 | **Stop/go review** against §5.4 | You | Prevents building into the void | A written go / no-go for T1 |

**T0 exit test.** T1 begins only if all are true: T0.1 manifest exists; T0.3 is resolved one way or the other; T0.5 exists; T0.6 answered C4 and C3. If T0.1 cannot be done (no client authority), T1 may still start for P1a only, but §5.4 rule 1 applies.

### 5.3 Slice card (every slice must have one before it is built)

A slice is a unit that can ship alone. Copy this block per slice; an empty field blocks the slice.

```
Slice:                 <ID and name>
Incident question:     the single question this slice helps answer, in the form
                       "Is <cause> the reason for <symptom> on <window>?"
Falsification:         the result that would make us drop or demote this cause
Evidence inputs:       sources, fields, window, granularity (UTC)
Overhead:              measured cost on the source (rows scanned, ms, bytes), run
                       on a lab or restored copy; per-source concurrency; kill switch
Privileged operations: exact commands/permissions, with the §7 row they come from
Retention:             what is stored, where, for how long, who can delete
Provenance:            how each output cell traces to source row/file + hash
Plain-language copy:   the slice's on-screen words, hint text and glossary entries,
                       written for a non-technical reader (§8.14 rules); reviewed
                       before the slice is called done
Positive demo:         a fixture where the slice must say "supported by evidence"
Negative demo:         a fixture where it must say "not supported / not observed"
Approvals needed:      Ops / DBA / Mongo owner / client security / Azure owner
Stop condition:        §5.4 rule that retires it
```

### 5.4 Stop-building rule

1. **No-preservation rule.** If native logs for the incident window cannot be preserved, **no slice that depends on them is built**; the slice moves to T3 as an instrumentation request.
2. **No-question rule.** A slice with no unanswered incident question in the ledger is not built.
3. **Three-incident rule.** After three closed incidents, any slice that was never used in a ledger entry is retired, not extended.
4. **Diminishing-returns rule.** If two consecutive slices add no new supported or rejected cause, stop and review the diagnosis method, not the tooling.
5. **Cost rule.** A slice whose measured overhead on a shared source exceeds its card budget is cut or moved to offline.
6. **Refusal rule.** A refused access request is final for the slice; use the offline fallback or retire the slice. Do not re-ask with a narrower wording the same quarter.

---

## 6. Corrected shared models

These replace rev 7 §3.1 (where marked), §3.3 and §3.4 for everything built from this revision.

### 6.1 Evidence preservation and authority

Preservation is an **Ops action under client authority**, not something this team initiates or self-authorizes. Rev 8 asks for it; it does not perform it. Copies are confidential (they hold host names, paths and possibly user identifiers), stored encrypted, with a manifest of SHA-256, source host, copy time, source timezone and the UTC mapping. Anything not preserved is listed as a **known gap** in the case record. A missing file is never evidence that nothing happened.

### 6.2 Clocks, joins and labels (C2, C6)

1. **Everything is UTC internally.** Every import records the source's timezone rule: SQL ERRORLOG and `system_health` use server local time (UTC+1 on the sandbox); IIS **W3C logs are always UTC** while IIS-format logs are local and NCSA is local with offset; `mongod.log` defaults to **local time with an offset** (`iso8601-local`), so the offset must be parsed, never assumed `Z`, and rotated files carry a UTC suffix; Windows events carry their own UTC value; App Insights is UTC; audit rows are UTC.
2. **Every join declares a tolerance:** `tolerance = source resolution + measured skew + method error`. ObjectId time is whole seconds; IIS `time-taken` resolution is milliseconds but its timestamp is the **request-start** (first byte), not the end; `system_health` samples are about 5 s apart.
3. **Every join reports a match class:** unique, ambiguous (more than one candidate) or none. Ambiguous matches are never counted as confirmed; they are listed.
4. **IIS `time-taken` includes delivery time.** It runs from HTTP.sys receiving the first byte to the last send completing, so for large or slow-client responses it includes network time. The residual "IIS minus audit `ApiTime`" is called **"time outside the pipeline"**. It is never "queue" without HTTPERR or counter confirmation.
5. **Unmatched rows** (IIS without audit, or the reverse) are never "lost audits" until the matching key and the tolerance are validated on a labelled sample.
6. **CPU labels.** Until T0.3 resolves it, B13 and S4 read **"overall host CPU; SQL attribution unknown"**. W7 (named process) needs per-process counters; absent that, it is disabled, not inferred.
7. **Absence rule.** "Not observed" and "ruled out" are different outputs. A source with a retention or sampling gap produces "not observed (gap)", never exculpation.

### 6.3 `AccountId` (C4)

`AccountId` is an **optional, unverified** field. The probe shows the field present but null in the last 20 rows; the schema doc does not list it. Rev 8 therefore: (a) measures its non-null rate per kind and period in P1a; (b) runs A2 (tenant attribution) only above a coverage threshold (default 80 % non-null in the window, tunable); (c) otherwise prints "tenant attribution unavailable (x % populated)"; (d) reads the writer at T0.6; (e) proposes a correction to `docs/api-audit-log-schema.md` once T0.6 answers. No other rule may depend on `AccountId`.

### 6.4 `_id` time bounds (C1)

1. ObjectId carries a **4-byte seconds timestamp**; the **driver generates it when `_id` is omitted** and, per MongoDB's own documentation, ordering across clients is **not guaranteed monotonic** because clients' clocks differ (verified, §9). So `_id` can shift a document outside a window derived from it, and the audit row is written in two stages, so the first write time is not the request time.
2. Therefore **`_id` bounds never produce authoritative output.** They produce a **narrowing hint**, labelled "bounded by margin M, calibrated on N indexed rows; rows outside M would be missed".
3. **Calibration** reads a bounded sample through the only index that exists (`_id`), e.g. the newest 10,000 documents, and measures `ObjectId time − RequestInitiatedAt`. The margin is the observed 99.9th percentile plus a safety pad; the sample size, dates and percentile are shown with the result.
4. **Exact mode** (full `RequestInitiatedAt` match, no `_id` bound) remains available with `maxTimeMS` and a row cap; it is the default for anything labelled complete.
5. **Recommended separate change request (DBA/Mongo owner):** a single-field index on `RequestInitiatedAt`. This tool never creates it.

### 6.5 Query safety template (C5)

Every query against a shared source (Mongo, SQL, Azure) carries all of: a **row cap**; a **server-side time limit** (`maxTimeMS` for Mongo, `SET LOCK_TIMEOUT` plus a command timeout for SQL, a request timeout for KQL); an **`appName`** that identifies the tool and version; **per-source concurrency of 1** unless the card states otherwise; a **measured cost** recorded on a lab or restored copy; a **kill switch** (config flag and a documented manual kill). SQL templates add `SET DEADLOCK_PRIORITY LOW`. Rules:

- **No blanket `NOLOCK`.** Microsoft documents that it can return rows twice or not at all and still takes schema-stability locks. A template may use it only where its card documents why a stale read is acceptable.
- **No reliance on `ApplicationIntent=ReadOnly`** as a safety control: it affects listener routing only. Read-only is enforced by the login's grants.
- **`explain('executionStats')` runs the query.** Never used as a cost probe on an un-limited scan; always paired with `maxTimeMS`.
- The tool never changes server state: no profiler level, no `slowms`, no index, no `sp_cycle_errorlog`, no Query Store change. (`setProfilingLevel` with `slowms` changes the threshold **instance-wide**, including the four `*_Live` client databases.)
- **Rev 7's MVP gaps are in scope for P1a:** set `appName`, add explicit `maxTimeMS` on the modern path, and reconsider the 5-minute server-selection timeout (the driver default is 30 s).


---

## 7. Per-source access matrix

**Nothing here is granted.** Each row is an *ask* to the named owner, with the condition for approval, the refusal fallback and an expiry. The default expiry for any standing grant is **30 days, renewable once per incident by the owner**; exports are one-off and need no expiry. A refusal is final for that slice (§5.4 rule 6).

### 7.1 Matrix

| # | Source / privilege | Ask (to whom) | Approve only if | Refuse if | Offline fallback | Expiry |
|---|---|---|---|---|---|---|
| A1 | **Mongo `read` on the audit database only** (the existing documented advice) | Mongo owner | A dedicated login, not shared with the `*_Live` databases; connection cap and `appName` agreed; the §6.5 template accepted | The login can read any `*_Live` database; or the owner cannot cap concurrency | Owner-run export of a fixed field list for a stated window (CSV, no payloads) | 30 days |
| A2 | **Mongo slow-op log copy** (`mongod.log` for the window) | Mongo owner / Ops | Owner copies files; hashes recorded; timestamps' offset recorded | Log contains data the owner will not release | Owner-supplied grep extract of `"c":"COMMAND"` lines with `durationMillis`/`workingMillis` over the window | One-off |
| A3 | **`clusterMonitor`, `getCmdLineOpts`, `$currentOp`, `getLog`, `system.profile` on all databases, FTDC** | Mongo owner | **Not default-approved.** Only for a named incident with a written reason that A1/A2 cannot answer; `clusterMonitor` includes `find` on **all** `system.profile` collections, which can expose query shapes and values across tenants; `getCmdLineOpts` output is not documented as redacting secrets | Any shared-instance tenant risk | A2 | Incident, max 7 days |
| A4 | **Mongo `setProfilingLevel`, `slowms`, index creation, `compact`, `killOp` (anything state-changing)** | n/a | **Never** by this tool. `slowms` is global to the instance | always | n/a | n/a |
| A5 | **Mongo index on `RequestInitiatedAt`** | Mongo owner (a change request, not an access grant) | Owner measures build cost and schedules it | Owner declines | `_id`-hint or exact mode with `maxTimeMS` (§6.4) | n/a |
| B1 | **SQL `VIEW SERVER STATE` / `VIEW SERVER PERFORMANCE STATE` (2022+)** | DBA | Conditional and broad: it exposes all instance DMVs; use only if B2 cannot answer | The login is shared | DBA-run export of named DMV queries | 30 days |
| B2 | **SQL `VIEW DATABASE PERFORMANCE STATE` (2022+) to read `.xel` via `sys.fn_xe_file_target_read_file`** | DBA | Narrower than B1 and database-scoped, so preferred for XE reads. On pre-2022 versions the permission is `VIEW SERVER STATE` (branch on version) | Pre-2022 and the owner will not grant `VIEW SERVER STATE` | DBA exports `.xel` events to CSV with a documented command; or hands over the files | 30 days |
| B3 | **msdb job/schedule data** (for P5) | DBA | DBA-owned **view or export** of fixed fields (job name, schedule, next run, last duration). **`SQLAgentReaderRole` is rejected as the vehicle**: it can create jobs and run jobs it owns, and inherits proxies, so it is not read-only | Role-based grant requested | DBA export of the fixed field list | One-off or 30 days (view) |
| B4 | **Hangfire schema SELECT** | DBA | **Rejected as written.** Recurring job fields live in Hash `recurring-job:{id}`, and the `Job` field plus `Job.Arguments` and `State.Data` hold serialized payload and exceptions, so a broad SELECT exposes arguments | always | DBA export of fixed fields only: id, Cron, Queue, NextExecution, LastExecution, TimeZoneId, **no `Job`, no arguments** | One-off |
| B5 | **Query Store enable/size/capture-mode change** | DBA (separate change request, owner G) | Sandbox only first, cost test, rollback stated; actual current options read at T0.8 | Production without a cost test | none; this is the instrumentation backlog | n/a |
| B6 | **Blanket `NOLOCK`** | n/a | **Rejected** (§6.5) | always | n/a | n/a |
| B7 | **`TrustServerCertificate=true`** | n/a | **Rejected as a production default.** Allowed only in a lab with a note; production uses a trusted chain | always | n/a | n/a |
| C1 | **Windows: Event Log Readers + Performance Monitor Users** (remote counters/events) | Ops | Conditional; scoped to named hosts; the account is not a local administrator | Account is local admin | Ops-run export: `wevtutil epl` to `.evtx`, `Get-WinEvent`→CSV; counters via `relog`→CSV | 30 days |
| C2 | **Ops-run 5-day DCS (T0.2)** | Ops | Interval, size cap, retention, rollback and owner stated in a change note | no change note | Ops runs a single 30-minute capture around the next known stall | One-off |
| C3 | **JEA endpoint** | Ops | **Separately approved option, explicit commands only**: each command named, `ValidateSet`/`ValidatePattern` **anchored** (`^…$`), no `Start-Process`, no `Invoke-Expression`, no provider (FileSystem/Registry) access, no custom function in full-language mode. Role merges are reviewed so an unconstrained grant cannot cancel a constraint | Free-form, wildcard or provider access | Ops-run exports (C1) | 30 days |
| C4 | **HTTPERR logs** | Ops | Readable only by Administrators/SYSTEM, so Ops copies them | n/a | Ops copy | One-off |
| D1 | **App Insights / Log Analytics** | Azure owner / client | Prefer an **owner-produced bounded KQL export**. If a live reader is required: `Log Analytics Data Reader` (scoped to the workspace or resource, table-level where available) or, in resource-context mode, Reader on the App Insights resource. Protected tables and data-actions-only mode return **empty results, not an error**, so an empty result is "possibly no access", never "no data" | `Log Analytics Reader` (`*/read`) or Contributor requested | Owner-run KQL with a fixed query, results as CSV/JSON; counts use `sum(itemCount)` | 30 days (reader) / one-off |
| E1 | **Redis** | n/a | **Deferred.** If reopened: SLOWLOG misses I/O and queueing; `CLIENT SETNAME` is `@slow @connection`, not `@read`, so it cannot be in a `+@read` allowlist and is excluded | always (for now) | n/a | n/a |
| E2 | **Elevated P0 discovery, N1 node, agent fingerprinting** | n/a | **Rejected for this roadmap** | always | hints file | n/a |

### 7.2 Why exports first

A fixed export is auditable by its owner before it leaves, needs no standing credential on a production system, and exercises the same evidence schema. The cost is latency and one-off friction, which is acceptable because the incidents under study are days, not minutes.

### 7.3 Credential handling

Passwords and tokens stay as in the MVP: DPAPI-encrypted, never returned to the browser. This revision adds nothing that stores a credential for a new source. A source reachable only through an export has no credential in the tool at all.

### 7.4 External AI and data handling (decision 14)

- No built-in LLM client; no outbound call to any model from the tool.
- The MCP surface stays read-only and returns no payloads, URLs, IPs or raw user IDs (as today).
- A case export may be given to an external model **only if the client has approved it in writing** and the §8.9 export passed preview. Absent that, exports stay local.
- Treat an export given to an external service as published: it may be cached or indexed even if later deleted.
- No endpoint accepts a model's proposed theories or narrative back into the ledger; the analyst types ledger entries.

---

## 8. Slices

Each subsection is a slice card summary (§5.3). **Existing** marks what the product already does; everything else is proposed.

### 8.1 P0: host hints only (T1, conditional on T0.5)

- **Question:** Which host, role, runtime and timezone does a given import belong to?
- **Falsification:** If T0.5 shows a one-page map with fewer than five hosts, skip the validator and keep the map as a documented file.
- **Build:** a small `hosts.json` with schema validation, shown with provenance ("from Ops note dated…") on every import. No discovery, no elevation, no config scan.
- **Overhead / privileged ops:** none (local file). **Retention:** the file is confidential; never exported without redaction.
- **Positive / negative demo:** an import with a matching host entry shows the host label; an import with an unknown host is refused with "unknown host".

### 8.2 P1: audit foundations, split

**P1a, correctness and safety (always first code, T1).**

- **Question:** Can we trust the numbers the existing views show, and can we query without hurting the shared instance?
- **Existing:** the views in §2.1. Not changed in meaning.
- **Build:**
  1. `appName` on every Mongo client; explicit `maxTimeMS` on the modern path; reconsider the 5-minute server-selection timeout; per-source concurrency stays 2 → proposal: 1 for shared sources.
  2. Coverage probe: per kind and day, document count and non-null rate for `RequestInitiatedAt`, `CreatedOn`, `ApiTime`, `AccountId` (§6.3), so every other number can be qualified.
  3. `_id` margin calibration and the hint/exact switch (§6.4), default exact.
  4. Result provenance: each response carries the source, window, query mode, sample size and margin.
- **Falsification:** If exact mode with `maxTimeMS` completes within the card budget on the sandbox, the hint is not needed and is not built.
- **Overhead:** measured on a restored copy before use on the sandbox; row cap and `maxTimeMS` on every query.
- **Privileged ops:** A1 only. **Retention:** none beyond the existing in-memory results.
- **Demo:** positive: a window containing a known late-written audit row appears in exact mode; negative: a hint-mode window that deliberately lies outside the margin prints the "rows outside M would be missed" warning.

**P1b, optional analytics (T2+, off by default).** 30–90-day trend scans behind an explicit "run a long scan" action showing the estimated rows scanned, requiring A5 (index) or off-peak confirmation, and the `maxTimeMS` cap. Not built until an incident question needs it.

### 8.3 P2-lite: incident patterns (T1)

- **Question:** Does the audit data show a recurring, volley, step or slow-write pattern in the incident window, and is the window itself covered?
- **Rules built first:** B1, B2, B6 (slow audit create), B12; step/duration views. B13/S4 relabelled per §6.2; B3 (recurrence against schedule) waits for the P5 export.
- **Output rule:** every finding prints its evidence label, low-n flag, censored/incomplete row counts and the coverage figure from P1a.
- **Falsification:** a pattern that does not recur across independent episodes (§8.6) is shown as "single episode", never "recurring".
- **Demo:** positive: a fixture with three independent stalls yields "recurring"; negative: one stall with 43 requests inside yields "single episode, n requests", not "43 stalls".

### 8.4 P6: Windows host evidence (T0 collection, T1 import)

- **Question:** What was each process doing on the host during the window?
- **Collect (T0.2/C1/C2):** Ops-created DCS plus native event-log exports.
- **Build:** an offline importer for CSV exports (`relog` → CSV for counters, `Get-WinEvent` → CSV for events) with `#Fields`/header validation, host/timezone lookup from hints, hash recording. **No native `.blg` or `.evtx` reader.**
- **Notes from verification (§9):** `Process % Processor Time` is per-core, so divide by the core count; legacy instance names (`svchost#N`) shift when processes exit, so prefer Process V2 with PID where available (availability on Server is undocumented, `[unverified]`); TaskScheduler/Operational is disabled by default; event-log sizes are read at runtime (`Get-WinEvent -ListLog`).
- **Falsification:** if no process other than SQL or the worker process rises above its baseline, "non-SQL process CPU" is not supported.
- **Privileged ops:** none for the tool (Ops exports); C1/C3 only if Ops chooses. **Retention:** the case store, confidential.

### 8.5 P3: SQL Server (T1 import; live connector T3)

- **Question:** Was SQL Server blocked, starved or idle during the window?
- **Import (T1):** a DBA-run export of: `system_health` events (`xml_deadlock_report`, `wait_info`, `scheduler_monitor_system_health_ring_buffer_recorded`), ERRORLOG for the window, and PDH counters from T0.3. **The tool never computes "SQL CPU" until T0.3 resolves the zero.**
- **Label rules:** CPU is "overall host CPU; SQL attribution unknown" until validated. `blocked_process_report` is **not** in `system_health`; it needs the `blocked process threshold` setting plus a custom session, so its absence proves nothing.
- **`dm_exec_query_stats`:** if ever read, note it is updated at query completion, rows vanish with plans, `last_execution_time` is server-local, and running queries need `dm_exec_requests`.
- **S14** (live DMV probes) is a separate approval and stays out of T1.
- **Falsification:** a `wait_info` event over 30 s overlapping a B6 audit stall (join class unique) supports "SQL waited"; none, with coverage confirmed, supports "SQL was not waiting".
- **Privileged ops:** B2 (or the export). **Retention:** case store.

### 8.6 Ledger and guards (the P2b residue, T1)

- **Question:** What do we believe, what supports it, what contradicts it, and what is not yet known?
- **What it is:** an analyst-edited list of **hypotheses**, each with a state: *open*, *supported by correlation*, *supported by strong correlation*, *contradicted*, *not testable with available evidence*. The state is set by the analyst. **The product never sets "supported" and never computes a verdict.**
- **Guards (deterministic, can only block):** a hypothesis cannot be marked "strong correlation" if: the evidence window has a coverage gap; fewer than 3 **independent episodes** (a *volley* is one episode; episodes are separated by a predeclared quiet gap, default 10 minutes); the join class is ambiguous for more than 10 % of rows; the source's timezone mapping is unrecorded; or a known confounder (same-minute scheduled job from P5, recycle event from P7) overlaps.
- **Statistics (decision 15):** counts, rates and episode counts only; windows are predeclared on the card, not chosen after seeing data. No Holm/BH, no permutations, no automatic p-values over user-selected theory sets. The existing Mann-Whitney z in the MVP stays as is, for the existing comparison view.
- **UI:** one small screen (the user's earlier "known versus not known" request). It lists hypotheses with state, evidence links (by provenance ID) and guard messages. Nothing else.
- **Falsification:** if after three incidents no ledger entry was reused, retire it (§5.4 rule 3).
- **Demo:** positive: a hypothesis with three independent episodes and a unique join can be marked strong correlation; negative: the same hypothesis with the 43 requests of one volley is blocked with "1 independent episode".

### 8.7 P7: IIS and HTTP.sys (T2)

- **Question:** Did the web tier wait, recycle or drop requests that the audit log does not show?
- **Import:** offline W3C (always parse the `#Fields` header: field sets differ by host and Windows update), HTTPERR (Administrators-only; Ops copies), and WAS events.
- **Rules:** residual "time outside the pipeline" (§6.2); WAS recycle events 5074 (time limit), 5075 (request limit), 5076 (schedule), 5077 (virtual memory), 5079 (admin request), 5080 (config change), 5117 (private bytes). **5186 is idle shutdown, not a recycle.** `5074` text says "processing time limit" but it is the periodic restart, not CPU.
- **Gap rule:** before IIS 10, `logEventOnRecycle` defaults to Time, Memory and PrivateMemory only, so a missing 5079/5080/5075 event proves nothing.
- **Falsification:** no recycle event and `logEventOnRecycle` covering the reason → "no recycle observed"; otherwise "not observed (logging gap)".
- **Privileged ops:** none for the tool; Ops copy. **Retention:** case store.

### 8.8 P4: Mongo native, `mongod.log` (T2)

- **Question:** Did the audit store itself stall during the ~28 s audit-create signature?
- **Import:** a copy of the log file(s) (A2). JSON format since 4.4; parse the offset on every timestamp; rotated files carry a UTC suffix.
- **Corrected M1 (replaces rev 7 M1):** a slow `insert` or `update` on the audit collection is joined to the **pre-action interval of a B6 request: `[RequestInitiatedAt, CreatedOn]`**, not to the request end (C3). The tolerance includes the one-second `_id` resolution (§6.2).
- **What each outcome means:**
  - Slow-op line found inside the interval → **correlation** that the server was slow for that write.
  - No line → **"not observed"**, never "ruled out". The threshold compares **total duration through 7.0**; **from 8.0** it compares `workingMillis`, which excludes lock and flow-control waits, so on 8.0+ a lock or flow-control stall can stay below the threshold while `durationMillis` is large. Client-side waits (pool wait, server selection, connect) never reach the server log at all.
  - The sandbox is 7.0.16 (total duration is logged), but a client's cluster may differ: read the server version first.
- **The ~28 s value:** the driver default `serverSelectionTimeout` is 30 s and the C# `ConnectTimeout` is 30 s, so 28 s is **not a clean match** for either; treat "client-side wait" as one open hypothesis alongside "server-side stall", to be decided by T0.6 (which timeouts the application actually sets).
- **Deferred:** FTDC (compressed, not human-readable, no tool named in the docs, contains hostnames and startup options, default `DirectorySizeMB` is 500 on current servers) and A3 probes.
- **Falsification:** a B6 request whose `[RequestInitiatedAt, CreatedOn]` contains no slow-op line, with log coverage confirmed and version known, lowers "server-side stall" (it does not eliminate "client-side wait").
- **Privileged ops:** A2 only. **Retention:** case store.

### 8.9 P13: evidence schema and redacted export (T1)

- **Question:** Can any finding be reproduced and shared without leaking more than intended?
- **Existing:** CSV export of metadata only; MCP 10 tools; payloads never in CSV or MCP.
- **Build:** (1) a versioned **evidence record**: `evidence_id`, `source`, `window_utc`, `join_class`, `tolerance`, `provenance` (file/query + hash), `label` (see Conventions, top of this document), `known_gaps[]`; (2) a **case export**.
- **Export rules (C8):**
  - Field-level **allowlist**; the default excludes payloads, raw URLs, query text, IPs and raw user IDs.
  - **Keyed pseudonyms** for user/client/host identifiers, with a **per-case key never written to the export**.
  - **Thin-cell masking** (n < 20, as in the MVP) applied to every count in the export.
  - **Preview then explicit confirm**: the export is shown in full before a file is written.
  - Manifest with SHA-256, tool version and a statement of what was masked.
- **MCP:** unchanged (10 tools). At most 3 read-only evidence tools at T3, only after two incidents have used the schema by hand.
- **Falsification:** a re-identification test on a fixture with small cells and a known user must fail to recover the user from the export.
- **Privileged ops:** none. **Retention:** the case store, confidential; deleted with the case.

### 8.10 P5: Hangfire and SQL Agent schedule (T2)

- **Question:** Does the stall recur on a scheduled-work boundary?
- **Import:** a DBA export of fixed fields (B3/B4 rows in §7).
- **Rules:** B3 (recurrence on cron boundaries) now uses declared tolerance and the schedule's timezone (`TimeZoneId`); a job whose run time is unknown is "unknown", not "not scheduled".
- **Rewording:** "scheduled work causes the stall" is a **hypothesis**; the only product statement is "stall start falls within ±tolerance of a scheduled boundary in k of n independent episodes".
- **Falsification:** episodes that do not align to any boundary, with the export covering the window, weaken the hypothesis.
- **Privileged ops:** export. **Retention:** case store (no job arguments ever).

### 8.11 P9: App Insights (T0 verify, T2 import)

- **Question:** Does request-scoped dependency evidence show where a slow request waited?
- **Import:** an owner-produced KQL export for the incident window (D1). Counts use `sum(itemCount)`; per-table retention is read, not assumed; adaptive sampling is on by default in ASP.NET and ASP.NET Core, so a **missing record is never counter-evidence**.
- **Attribution rule:** a dependency's duration is "attributed duration", not "underlying cause".
- **Falsification:** a sampled-in slow request whose dependency durations sum to far less than its total shows the wait is outside instrumented dependencies.
- **Privileged ops:** D1. **Retention:** case store.

### 8.12 P8: thin timeline (T2)

- **Question:** In what order did things happen across sources?
- **Build:** a single clock-aligned list of evidence records (one row per event, source and join class shown). **No composite rules, no confidence promotion, no cause statements.** Gated on at least two sources with a validated timezone mapping.
- **Falsification:** if the timeline of a known incident is not reproducible by an analyst by hand from the same files, the join logic is wrong and is fixed before more is built.
- **Demo:** negative: two sources with an ambiguous join show both candidates, not one.

### 8.13 Deferred and cut (not in this roadmap)

P10 Redis, P11 .NET runtime, P12 log4net (deferred, reopen conditions in §4); P2b engine and N1 node (cut). The instrumentation backlog (G) is a list of change requests, each with its own owner, cost test and rollback; none is authorized by this document.


### 8.14 P14: guided tour and plain-language help (new, T1, grows with every slice)

Added at your request on 2026-10-24: the app must teach non-technical people how to read it, in words they understand. Nothing here exists today (the MVP has a README and the `audit_get_guide` MCP text, both written for engineers).

- **Question this slice answers:** Can someone with no database or server background open the app, understand what each number means, and say what the evidence does and does not show?
- **Falsification:** Test with **five non-engineers** (support, account or business staff) on the demo data. After the tour each answers five fixed questions (for example "Is this slow because of the database?", "What does 'not observed' mean?"). If fewer than four of five get four of five right, the words are rewritten and retested; the tour is not called done.
- **Parts (all built into the existing Svelte UI, local only):**
  1. **First-run tour.** A short, skippable, replayable walk-through (about 8 steps) that highlights real screen areas: what the comparison shows, what P50/P95 mean, how to open a slow request, what "evidence label" means. Replay from a visible "Help" button at any time.
  2. **"What does this mean?" hints.** Every technical word on screen (P95, coverage, episode, correlation, join, "not observed", "time outside the pipeline", "low sample") has a small hint with a one-sentence plain definition and a "learn more" link into the glossary. Hints are keyboard- and screen-reader-reachable.
  3. **Glossary.** One page, alphabetical, each term defined in two sentences plus a "what it does **not** mean" line (for example: "Correlation: two things happened together. It does not prove one caused the other.").
  4. **Guided practice case.** In `--demo` mode a scripted incident walks the reader through reading a result, checking coverage, opening the ledger, and seeing a guard block a claim ("only one episode, so this is not yet a pattern"). It uses synthetic data only; the tour never touches a live source.
  5. **Plain results.** Each finding shows a one-line summary in everyday words first ("The database was waiting for about 28 seconds before saving these requests"), with the technical detail one click deeper (progressive disclosure, as in the earlier UI direction).
- **Writing rules (apply to every slice's copy):**
  - Aim for an **8th-grade reading level**; short sentences; active voice.
  - **No jargon without a hint** the first time it appears on a screen; prefer the everyday word ("waiting" over "blocked", "saved" over "persisted").
  - **Say what it does not mean** wherever a reader could over-read (correlation, "not observed", sampling).
  - **Never imply certainty the evidence label does not support** (the four labels in the Conventions paragraph at the top are the only strength words).
  - Numbers always carry their unit and window ("about 28 seconds, on 24 Oct, 14:45–14:50 UTC").
  - No blame language about people or systems; describe what the data shows.
- **One copy deck.** All on-screen words, hints, glossary entries and tour steps live in a single reviewable file (not scattered in components), so a non-engineer can read and edit them. `audit_get_guide` (MCP) reads the same entries so the app and the AI client never disagree.
- **Done criteria for any slice:** its new words are in the copy deck; its terms are in the glossary; its tour step or hint exists; a copy lint passes (reading-level check, undefined-term check against the glossary).
- **Overhead / privileged operations:** none (local UI). **Retention:** only a local "tour seen" flag in the browser (per-viewer convenience; the app works without it). The tour never stores or shows real data.
- **Accessibility:** tour is operable by keyboard, announces steps to screen readers, respects reduced-motion, and never traps focus.
- **Positive demo:** a first-time user finishes the tour and correctly explains a "not observed" result. **Negative demo:** a hypothesis blocked by a guard shows the plain-language reason, not an error code.
- **Approvals needed:** none (no access, no data). Content review by a non-engineer is the gate.

---

## 9. Verification of technical assumptions

All fetched 2026-10-24 by two independent agents from vendor documentation. **"Partly"** means the page supports part of the claim; **"inference"** means no page states it. Where a verdict changes the plan, the last column says how.

### 9.1 MongoDB

| # | Claim | Verdict | Source | Effect on this plan |
|---|---|---|---|---|
| M-1 | ObjectId time is 4-byte seconds; driver generates `_id`; not monotonic across clients | **Confirmed** | mongodb.com/docs/manual/reference/bson-types/ | C1: `_id` is a hint only (§6.4) |
| M-2 | Slow-op threshold basis | **Version-dependent**: through 7.0 total duration; from 8.0 `workingMillis` (excludes lock and flow-control waits); ticket-queue exclusion implied, not stated | release-notes 8.0; configuration-options; log-messages | M1 reads both fields; version read first; "not observed" never exculpates (§8.8) |
| M-3 | `serverSelectionTimeoutMS` default 30 s; `connectTimeoutMS` 10 s (driver-specific; C# 30 s); client waits never reach `mongod.log` | **Confirmed** (default); the log claim is an **inference** | connection-string-options | The ~28 s signature is not a clean match; T0.6 reads the application's real timeouts (§8.8). The MVP's 5-minute selection timeout is revisited (§6.5) |
| M-4 | Profiler: level/filter database-scoped; `slowms`/`sampleRate` global; `system.profile` capped (1 MB default); can degrade performance | **Confirmed** | manage-the-database-profiler; db.setProfilingLevel | Tool never changes profiling (A4) |
| M-5 | `read`, `clusterMonitor` privileges; `$currentOp` needs `inprog` | **Confirmed**; `clusterMonitor` can `find` on all `system.profile`; `getCmdLineOpts` output not documented as redacted | built-in-roles; currentOp; privilege-actions | A3 not default-approved |
| M-6 | FTDC defaults | **Partly / version-dependent**: `DirectorySizeMB` 500 on current servers (was 250 before 8.3 for non-sharded); files not human-readable; no reader named; contain hostnames and startup options | parameters; full-time-diagnostic-data-capture | FTDC deferred |
| M-7 | Log rotation and format | **Confirmed**: rotate only on `logRotate`/SIGUSR1 (Unix only); JSON since 4.4; default timestamp is **local time with offset** | rotate-log-files; configuration-options | §6.2: parse the offset; never assume UTC |
| M-8 | Scans and `maxTimeMS` | **Confirmed**: without indexes each query scans; `maxTimeMS` kills at interrupt points; `explain('executionStats')` **runs** the query | indexes; explain-results; cursor.maxTimeMS | §6.5: never an unlimited cost probe |
| M-9 | Redis SLOWLOG scope; `CLIENT SETNAME` ACL | **Confirmed**: SLOWLOG excludes I/O and queueing; SETNAME is `@slow @connection`, not `@read` | redis.io commands | Redis deferred; SETNAME out of allowlist |
| M-10 | Hangfire storage layout | **Confirmed from source, not narrative docs**: recurring jobs in Hash `recurring-job:{id}` (Cron, Job, LastExecution, NextExecution, Queue, TimeZoneId, Error…); payload in `Job`/`Job.Arguments`/`State.Data`; the monitoring API does not list recurring jobs | Hangfire GitHub source; api.hangfire.io | B4: fixed-field export, no payload fields |

### 9.2 SQL Server, Windows, IIS, Azure

| # | Claim | Verdict | Source | Effect on this plan |
|---|---|---|---|---|
| S-1 | Permission to read `.xel` | **Confirmed, version-dependent**: `VIEW SERVER STATE` through 2019; `VIEW SERVER PERFORMANCE STATE` or **`VIEW DATABASE PERFORMANCE STATE`** from 2022 | sys.fn_xe_file_target_read_file | B2 branches on version and prefers the narrower grant |
| S-2 | `dm_exec_query_stats` | **Confirmed**: updated on completion; rows leave with plans; `last_execution_time` is local | sys-dm-exec-query-stats | §8.5 notes |
| S-3 | `NOLOCK`; `ApplicationIntent` | **Confirmed**: dirty/duplicate/missing rows; intent is routing only | table hints; AG listener docs | B6 reject; read-only via grants (§6.5) |
| S-4 | Query Store default | **Partly, version-dependent**: off by default through 2019; **on for new databases from 2022**; upgraded or attached databases not covered; the docs disagree on capture-mode default | query-store docs; alter-database options | T0.8 reads actual options; decision 2 reversed (§10) |
| S-5 | `system_health` retention; `blocked_process_report` | **Partly**: 4 × 5 MB by default (larger on Standard/Enterprise); no retention in days; blocked-process report is not in it | use-the-system-health-session | T0.1 urgency; §8.5 |
| S-6 | `logman`, per-core CPU, Process V2 | **Confirmed (mostly)**: Process V2 documented for Windows 11+; server availability not documented | logman-create-counter; handling-duplicate-instance-names | `[unverified]` on Server; divide CPU by cores (§8.4) |
| S-7 | Task Scheduler log default | **Confirmed by local test only** (no Learn page): Operational log disabled, 10 MB | `wevtutil gl` on Windows 11 26200 | Read real settings at runtime; empty ≠ nothing ran |
| S-8 | IIS `time-taken`; log time zones; HTTPERR | **Confirmed**: includes delivery to last send; W3C is UTC; IIS-format local; NCSA local+offset; HTTPERR 1 MB roll, admin-only | time-taken-field-http-log; logfile docs | §6.2 residual wording; C4 |
| S-9 | WAS recycle event IDs | **Confirmed + caveat**: 5074 time, 5075 request limit, 5076 schedule, 5077 virtual memory, 5079 admin, 5080 config, 5117 private bytes; 5186 is idle shutdown; before IIS 10 default logging is partial | WAS event docs; recycling settings | §8.7 gap rule |
| S-10 | Azure roles | **Partly**: `Log Analytics Data Reader` exists; protected tables and data-actions-only mode return empty, not 403 | azure-monitor logs manage-access | D1: empty = "possibly no access" |
| S-11 | App Insights sampling and retention | **Confirmed with nuance**: use `sum(itemCount)`; adaptive sampling on by default; per-table retention; the sampling page covers the classic SDK (archived) | sampling-classic-api; data-retention-configure | §8.11 |
| S-12 | JEA constraints | **Confirmed**: `ValidatePattern` is unanchored; merged roles can cancel constraints; providers grant broad access | JEA role-capabilities | C3: anchored patterns, reviewed merges |
| S-13 | SQL Agent roles | **Confirmed**: `SQLAgentReaderRole` can create jobs and run owned jobs; inherits proxies | sql-server-agent-fixed-database-roles | B3: not read-only; DBA view/export |
| S-14 | ERRORLOG retention | **Confirmed**: "typically" previous six; restart cycles; never call `sp_cycle_errorlog` (sysadmin) | viewing-the-sql-server-error-log; sp_cycle_errorlog | T0.1 urgency |

### 9.3 Not verified (kept open)

- Whether **`Process V2`** is available on the Windows Server versions in use `[unverified]`.
- Whether **ticket-queue time** is excluded from `workingMillis` (implied, not stated).
- The application's actual **driver version and timeouts** (T0.6).
- Whether a client's cluster is **7.0 or 8.0+** (read `buildInfo` through A1 or ask the owner).
- That IIS `time-taken` for **small** responses excludes network time: documented behaviour covers responses over 2 KB only.
- Everything about **host topology**, **client authorization** and **per-client App Insights**: facts to collect, not claims to verify.

---

## 10. Decisions

### 10.1 Decisions 1–15 revised

"Reversed" means the rev 7 answer no longer holds and needs your re-confirmation. Rev 7 acceptance was against a fixed order; the review gives the reasons below.

| # | Rev 7 resolution | Rev 8 status | New position and reason |
|---|---|---|---|
| 1 | Ops confirms a host map per client; P0 fills it | **Kept, narrowed** | Ops writes the one-page map (T0.5); hints validator only. Auto-detect removed (see 11) |
| 2 | Query Store **On**, specific settings, DBA runs | **Reversed** | A separate DBA change request with a cost test (B5); read the real state first (T0.8). Docs show the default is on only for new 2022 databases |
| 3 | 5-day DCS, Unravis generates, Ops creates | **Kept, moved to T0** | Add interval, size cap, retention and rollback in the change note; Ops-run (C2) |
| 4 | Read-only console/export for the third-party SQL monitor | **Kept, export first** | T0.4 identifies it; exports before any console access |
| 5 | S14 sampler 5 s cadence, ≤ 30 min, ≤ 5/day | **Deferred** | S14 is a separate approval (T3). The cadence has no data behind it |
| 6 | WinRM + JEA endpoint, native RPC, then offline | **Reversed** | Offline Ops exports first; JEA is an optional, separately approved extra (C3), not the primary channel |
| 7 | On-server node, hybrid, gate N | **Void (cut)** | N1 removed |
| 8 | Node transport spool-only first | **Void** | N1 removed |
| 9 | Node identity, 14-day expiry | **Void** | N1 removed |
| 10 | Node signing and updates | **Void** | N1 removed |
| 11 | `unravis detect` run by Ops, elevated or hints | **Reversed** | Hints only; no elevated run, no discovery in the first release (X5) |
| 12 | Record run-as account names; redact SQL-auth IDs and secrets | **Kept, tightened** | Hints files hold no identities or secrets at all; run-as names recorded only if an owner supplies them |
| 13 | Host profiles and hints are client-confidential, DPAPI-stored | **Kept** | The hints file and host map stay confidential and leave the client boundary only pseudonymised |
| 14 | LLM external via MCP, no built-in client; may propose theories | **Changed** | No built-in LLM; no proposal endpoints; external AI only with per-client approval (§7.4) |
| 15 | Holm α = 0.05, BH, 9,999 permutations | **Changed** | §8.6: descriptive statistics, independent episodes, predeclared windows; no verdict engine |

**Also changed from the older resolved table:** old 2 (DBA grant list in rev 7 §1A.E) is **replaced by §7**; old 3 (App Insights live for ~50 %) → export first; old 5 (90 days, daily rollups only) → P1b, gated; old 6 (extend the audit MCP and add 4 generic tools) → hold at 10, add at most 3 later; **kept**: old 4 (preservation as a standing action, window now 1 Sep–24 Oct plus future retention), old 8 (no real extracts as fixtures).

### 10.2 Statistics policy (decision 15 detail)

1. The **unit of inference is an independent episode**, not a request. A volley is one episode; episodes are separated by a predeclared quiet gap (default 10 minutes; tunable per case, declared before looking).
2. Analysis **windows are predeclared** on the slice card or ledger entry, not chosen after seeing the data.
3. Output is **counts, rates and episode counts with coverage**, plus the existing low-n masking (n < 20).
4. **No family-wise correction over user-selected theories**: the family would depend on which theories the user chose to run, so it is not well defined (C7).
5. **No permutation test** without a calibration run on realistic fixtures that shows its false-positive rate under volley-shaped data; and then only as a labelled aid, never as a verdict.
6. The existing Mann-Whitney z in the comparison view is unchanged; it is not extended to theory verdicts.

### 10.3 Remaining decisions and approvals (summary of §1.4 and §12 items)

| # | Needs | From | Blocks |
|---|---|---|---|
| R1 | Re-confirm decisions **2, 6, 7–10, 11, 14, 15** as reversed/void/changed (§10.1) | You | Final acceptance of rev 8 |
| R2 | Authorization to preserve logs and run the DCS (T0.1, T0.2) | Client / Ops | T0 and T1 for P6/P3 |
| R3 | Approve the **per-source access matrix** (§7) as the approval vehicle | You, then each owner | Any access |
| R4 | Query Store change, if ever wanted | DBA (change request) | Instrumentation backlog |
| R5 | JEA, if Ops wants it | Ops | Optional only |
| R6 | External-AI data handling per client | Client security | T3 export |
| R7 | `RequestInitiatedAt` index | Mongo owner | Exact-mode speed (optional) |
| R8 | Plain-language **content review** by a non-engineer; pick five test users | You | P14 "done" |
| R9 | Confirm the **independent-episode gap default** (10 minutes) and the **`AccountId` coverage threshold** (80 %) | You | §8.6, §6.3 |
| R10 | Confirm **MCP stays at 10 tools** (+ at most 3 later) | You | P13 T3 |

**Unresolved assumptions** (also in §9.3): the application's real driver timeouts; the Mongo version at each client; Process V2 on servers; whether T0.1 files exist for the full window; whether `AccountId` is populated by the writer.

---

## 11. Retained rule IDs and rewording

Rule IDs keep their rev 7 meaning for traceability; their **definitions and thresholds live in the rev 7 archive** and are used only when a slice names them. Rewording and status:

| Family | Rev 8 status |
|---|---|
| A0–A5 (audit coverage) | A0, A1 in P1a; A2 only with `AccountId` coverage (§6.3); A3–A5 on demand |
| B1, B2, B6, B12 | **First build (P2-lite)** |
| B13 | Relabelled "overall host CPU; SQL attribution unknown" until T0.3 |
| B3 | Needs the P5 export; wording per §8.10 |
| other B (B4, B5, B7–B11) | Not built until a question names them |
| S1–S14 | Offline only (§8.5); S4 relabelled; **S14 separate approval, T3** |
| M1 | **Corrected (§8.8)**; M2–M9 deferred except what `mongod.log` shows |
| H1–H5 | Fixed-field export only (§8.10) |
| W1–W9 | Offline imports (§8.4); W7 only with per-process counters |
| I1–I6 | Offline import (§8.7); residual wording per §6.2 |
| C1–C10 | **Not built.** Only the thin timeline (§8.12) |
| AI1–AI5 | Export first (§8.11); missing sample ≠ counter-evidence |
| HP1–HP6 | Hints validation only |
| V1–V9, TH1–TH19 | **Not built** (theory IDs, not rules); V6 source-sanity survives as a guard |

---

## 12. Rev 7 errata and what stays

### 12.1 What rev 8 overrides in rev 7

Section numbers on the left of each arrow are **rev 7** sections; on the right, rev 8.

1. §0, §2, §25 (fixed phase order and release table) → §5 here.
2. §1 and §1A (access list, grants) → §7 here.
3. §3.1 (`_id` pruning), §3.3 (clocks), §3.4 (safety), §3.5 (hurting-ops register: **kept as a reference**, but every item needs a §6.5 template) → §6 here.
4. §3.9 (hub/node), N1 and everything about a node, mTLS, spool, signing → **cut**.
5. §3.10 (LLM) and decision 14 → §7.4.
6. §3A **P0** discovery body → §8.1; **P1** `_id` pruning → §8.2; **P2b** (all of §5A, V1–V9, TH1–TH19, bundle, verdict engine, theories screen) → §8.6; P10, P11, P12 → deferred.
7. §24 decisions → §10.
8. Anything in rev 7 that reads "19 tools", "R0…R13", "build order", "node", "Holm", "permutations", "theories screen", "elevated" is **not current**.

### 12.2 What stays from rev 7

Evidence labels; rule IDs and their definitions as a reference catalogue; the query templates (re-measured before use); the `sql/` analysis scripts and the E-fact table; the demo-data approach (no real extracts as fixtures); low-n masking; the progressive-disclosure UI direction; the principle that the tool is read-only and never changes server state.

### 12.3 Traceability

Rev 7 phase → rev 8 section: P0→§8.1; P1→§8.2; P2→§8.3; P2b→§8.6; P3→§8.5; P4→§8.8; P5→§8.10; P6→§8.4; P7→§8.7; P8→§8.12; P9→§8.11; P10/P11/P12→§8.13; P13→§8.9; N1→§4 (cut); G→§4; new P14→§8.14.

