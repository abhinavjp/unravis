# How to review the Unravis post-MVP roadmap (rev 9)

- **Date:** 2026-10-24
- **Reviews:** `docs/superpowers/plans/2026-10-24-unravis-post-mvp-roadmap.md` (**revision 9**, about 770 lines). It replaces rev 7 (about 2,900 lines) and rev 8; both are historical and not in this pack.
- **Goal:** accept or challenge the gates, the access matrix and the first slices, in **three short sessions**, without reading all of it.
- **Who:** you (owner), plus optionally a DBA, an ops lead, a Mongo owner and client security. Each reads only their part.
- **Sharing tip:** share the roadmap file only. Do not forward the `sql/` folder: it contains a credentials file.
- **Status:** planning only. Nothing in the roadmap is built, and writing it grants no access and approves no build.

---

## 1. What rev 9 is

Rev 8 turned a fixed 14-phase build into **gated slices in four tiers (T0–T3)**: collect evidence first (T0, no code), build small offline importers next (T1), and add sources only when an unanswered incident question names them (T2, T3). Rev 9 applies a second independent review of rev 8. Seven blockers were accepted and fixed:

1. SQL timestamps are per field (XE `timestamp_utc` is UTC; ring-buffer XML, `GETDATE()` and ERRORLOG are server-local).
2. `system_health` records only locks over 30 s and latches over 15 s, so a ~28 s stall is invisible. Absence reads "not observed".
3. The existing MCP tools can return a user breakdown and opt-in IPs, and MCP output goes to the AI client's model provider.
4. Broad Mongo scans do not run live by default; "complete" is defined; budgets fail closed.
5. One Mongo-log rule (P4) with a source-quality record and an "ambiguous" class.
6. No causal wording in plain-language copy (P14's example claimed a cause).
7. "Proven cause" is removed from T1.

The change log is at the top of the roadmap; §3.4 lists every review item with accept, change or challenge.

---

## 2. Reading order (stop when you have enough)

| Step | Section(s) | What you get | Time |
|---|---|---|---|
| 1 | Rev 9 change log (top), §1 Summary | What changed and what is asked of you | 10 min |
| 2 | §2.1 What exists today | The built MVP, so you can tell built from proposed | 5 min |
| 3 | §3.4 and §1.3 | Where rev 9 follows or challenges the reviewers (X1–X9) | 10 min |
| 4 | §5 Adaptive sequence (tiers, T0.1–T0.12, slice card, stop-building rule) | The process you are approving | 15 min |
| 5 | §7 Per-source access matrix, §7.4 External AI | Each ask, its refusal fallback, expiry | 15 min |
| 6 | §6 Corrected shared models (clocks, `_id`, query safety, wordings, import contract) | The rules every slice must follow | 20 min |
| 7 | §8.1–§8.3 P0, P1, P2-lite; §8.6 ledger; §8.14 P14 | The first slices in depth | 15 min each |
| 8 | §10.3 Remaining decisions R1–R16 | The decision register | 5 min |
| 9 | §9 Verification | What was checked against vendor docs, and what is still open | 10 min |
| — | Skip unless needed: §8.4–§8.5, §8.7–§8.13 (T1/T2 sources), §11, §12 | Reference | — |

Steps 1–5 (about an hour) are enough to decide the process.

---

## 3. Per-slice review checklist

Apply to each slice you are asked to approve. Short notes are enough.

1. **Question.** Which unanswered incident question does it serve? If none, the no-question rule (§5.4) says do not build it. (Safety, preservation and usability slices are exempt but state their purpose.)
2. **Preconditions.** Are the T0 items it lists done? Are they the *right* ones, not all of T0?
3. **Falsification test.** Can the slice's test fail? Does absence of data read "not observed", with the source's limits, rather than "did not happen"?
4. **Wording.** Does every output use the §6.6 wordings, with no causal claim on a Symptom or Correlation label?
5. **Access and data.** Is every permission acceptable to the owner, with a fallback when refused? Does any data reach an external model?
6. **Production safety.** Is any query bounded by a scan class, `docsExamined` budget, time window, row cap, timeout and kill switch (§6.5)?
7. **Over-engineering.** What is the simplest version that still answers the question?
8. **Unverified claims.** Which items in §9.3 does it rest on? Can one be checked cheaply first?

---

## 4. Decisions to fill in

Mark one box per row. "Change" means accept with the change written in Notes.

| Item | What you decide | Accept | Change | Cut | Defer | Notes |
|---|---|---|---|---|---|---|
| R1 | Re-confirm decisions 2, 6, 7–10, 11, 14, 15 as reversed, void or changed (§10.1) | | | | | |
| R2 | Authorization to preserve logs and run the 5-day counter capture (T0.1, T0.2): Ops under client authority | | | | | |
| R3 | The per-source access matrix (§7) is the approval vehicle, source by source with expiry | | | | | |
| R8 | Plain-language content review by a non-engineer; five test users (P14 "done") | | | | | |
| R9 | Independent-episode gap default (10 minutes) and `AccountId` coverage threshold (80 %) | | | | | |
| R10 | MCP stays at the 10 existing tools (at most 3 more, later) | | | | | |
| R11 | T0.10 audit of the 10 MCP tools' outputs and the AI client's provider path, with the client's written field approval | | | | | |
| R12 | DBA dry run of every SQL export command and scrub list (T0.11) | | | | | |
| R13 | Mongo custom role limited to the audit collection, tested (T0.12), or a refusal | | | | | |
| R14 | Cost-budget acceptance: per-slice `docsExamined`, time and concurrency budgets; a restored copy to measure on (§6.5) | | | | | |
| R15 | Proof standard: "Proven cause" stays out of T1; what approval would be needed if ever introduced (§8.6) | | | | | |
| R16 | Import, security and retention contract (§6.7): default retention (30 days after case closure) and per-importer limits | | | | | |
| R4–R7 | Optional or later: Query Store change, JEA, external-AI data handling, `RequestInitiatedAt` index | | | | | |

---

## 5. Items that need client facts or code reads (not decisions)

| Item | Needed for | Owner (suggested) |
|---|---|---|
| Host map (T0.5): which host runs which role; runtime; timezone; clock source | P0, every import | Ops |
| Code reads (T0.6): audit writer fields (`AccountId`, `CreatedOn`), Mongo driver timeouts and pool, any retry wrapper | P1a | This team |
| Is the SQL CPU counter really zero? (T0.3) | Any "SQL" CPU wording | DBA + Ops |
| Existing monitoring and App Insights per client (T0.4) | P9 | Ops + client |
| Query Store state (T0.8) | Any instrumentation request | DBA |
| `system_health` live event definition (read at T0.11) | Wording of absence | DBA |
| Which fields each of the 10 MCP tools returns on live data (T0.10) | Any MCP use on live data | This team |

---

## 6. Riskiest assumptions, ranked by consequence

| # | Assumption | Consequence if wrong | Cheapest check |
|---|---|---|---|
| 1 | The sandbox evidence reflects live clients | The framing may not hold for clients | Repeat the P1a census on one client copy, read-only |
| 2 | Ops can preserve logs for the full window (T0.1) and the client authorizes it | Retrospective evidence is lost; only P1a and P14a can proceed | Ask Ops now; retention is short |
| 3 | `system_health` thresholds and ring-buffer limits are as documented (prose only so far) | Wording of "not observed" is too strong or too weak | Read the live event definition at T0.11 |
| 4 | The SQL CPU zero is an artefact, not a true zero | "Non-SQL CPU" stays an unusable phrase | T0.3 PDH comparison |
| 5 | A single-collection Mongo role can be created | A1 falls back to an owner export | T0.12 on the sandbox |
| 6 | The AI client's provider path is acceptable to the client | MCP on live data is not allowed | T0.10 |
| 7 | Narrow indexed Mongo queries over 24 h are cheap enough live | Even the P1a census needs a restored copy | Measure on a restored copy (R14) |
| 8 | Hints alone suffice for non-engineers (P14a) | P14b (tour) is then justified | One real incident plus R8 test users |

---

## 7. Challenge questions

1. **Is rev 9 still too big?** Which slice would you cut first if only P1a and P14a could be built?
2. **Is T0 enough on its own?** Would the T0 evidence (counters, logs, code reads) answer the incident without any build?
3. **Is the access matrix workable?** What is the real approval lead time per source, and who can refuse?
4. **Is AI/MCP use acceptable at all** once T0.10 shows which fields leave the client?
5. **Does it duplicate the client's third-party SQL monitor?** Which questions does only Unravis answer?
6. **Will people misread the labels?** Does the screen make Symptom, Correlation and Strong correlation hard to confuse with proof?
7. **Is the stop-building rule (§5.4) strong enough** to prevent building sources nobody needs?
8. **Where do you disagree with the challenges X1–X9?** Name the ones you would reverse.

Write a one-line answer for each, or mark it "agree, change the plan".

---

## 8. Review roles (who reads what)

| Role | Read | Decide or answer |
|---|---|---|
| **You (owner)** | Change log, §1, §3.4, §5, §10.3, §8.1–§8.3, §8.14 | R1, R3, R8–R10, R14–R16 |
| **DBA** | §6.2, §6.6, §7.1 (SQL rows), §8.5, T0.3, T0.8, T0.11 | R12; scrub list; SQL CPU zero; Query Store state |
| **Ops lead** | §6.1, §7.1 (host rows), T0.1, T0.2, T0.5, §8.4 | R2; retention; counter capture; host map |
| **Mongo owner** | §6.4, §6.5, §7.1 (Mongo rows), T0.12, §8.2 | R13, R14; the restored copy; optional index |
| **Client security** | §6.7, §7.3, §7.4, T0.10 | R6, R11, R16; AI data handling |
| **Developer** | §6, §8.2, §8.3, §8.6, §8.14 | Is each slice buildable as written? What is missing from its test? |

---

## 9. Three review sessions (30–45 minutes each)

### Session 1: Process and shape (you alone, about 45 minutes)

- **Read:** steps 1–4 of §2.
- **Do:** answer challenge questions 1, 2, 7 and 8. Mark what you would cut.
- **Exit:** you accept or change the tiers, the slice card and the stop-building rule; you know which slices you will not fund.

### Session 2: Access and data (owner with DBA, Ops, Mongo owner and client security as available)

- **Read beforehand:** each person reads their row in §8.
- **Do:** walk the access matrix (§7). Settle R2, R11, R12, R13, R14, R16. Go through challenge questions 3–5.
- **Exit:** every ask is accepted, refused (with the fallback noted) or pending with an owner and a date; the §5 facts have owners.

### Session 3: First slices (you, optionally a developer)

- **Read:** §8.1–§8.3, §8.6, §8.14 and §6.6.
- **Do:** run the §3 checklist on P1a, P0, P2-lite and P14a. Decide R8–R10 and R15.
- **Exit:** each of those slices is Accept or Change; every test is checkable; the top assumptions in §6 each have a check scheduled.

After session 3, ask for **revision 10** with your changes. It should touch only what you changed.

---

## 10. From approved slices to implementation plans

1. **One slice at a time**, each with its own **slice card** (§5.3): question, preconditions, falsification test, overhead budget, privileged operations, rule definitions restated in full, plain-language copy.
2. Start with **P1a** (audit safety and coverage) and **P14a** (plain-language hints). Neither needs production grants beyond what R13 settles, and P14a needs nothing external.
3. For each approved slice, write an implementation plan with the `superpowers:writing-plans` skill, using the roadmap section and its card as the spec.
4. Turn each slice's falsification and acceptance tests into tests written test-first. Real extracts are never used as fixtures.
5. Plan at most **two slices ahead**. After the first real incident, review the slice list against what it actually needed (§5.4).
6. Re-open this review if any of the top three assumptions in §6 fails.
