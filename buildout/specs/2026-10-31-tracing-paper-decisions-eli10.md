# The 15 Rulings, Explained Simply

This is the plain-words version of the decisions made about the "Tracing Paper" tool. A short note on words used below: a **window** is a slice of time you are looking at; a **bucket** is a small sub-slice inside that window; a **rollup** is a saved summary of many events; an **event** is one logged action; and a **case** is one investigation file.

## 1. Bucket sizes must be predictable [TPR-001]

**What was wrong:** Bucket sizes were described as a range ("pick something between 300 and 900 seconds"), and a range is like telling someone "make the cake bake between 20 and 40 minutes" — you cannot tell what you will get. For a 24-hour window, a 300-second choice gave 288 buckets, over the 120-bucket limit.

**What I decided:** The size is now a fixed lookup based on window length, and the first bucket starts exactly at the window start. The count is the ceiling of span divided by width, capped at 120. (Exactly 24 h uses 900 s and gives 96 buckets; saved rollup summaries use UTC-aligned 60 s and 3600 s instead.)

**Why:** Anyone computing a chart must get the same answer as anyone else, every time.

**Cost if wrong:** Charts and tests disagree, and two people looking at the same window see different numbers.

## 2. Windows stop just before the end time [TPR-011]

**What was wrong:** An event at exactly the boundary time was counted in both the window ending there and the window starting there — the same event double-counted — like counting a person at the exact moment they cross a doorway on both sides of the door.

**What I decided:** All windows, buckets and coverage slices are half-open: an event at the exact start is included, and at the exact end it is excluded because it belongs to the next window.

**Why:** Each event should be counted exactly once, with no special de-duplication rule.

**Cost if wrong:** Totals are slightly too high at every boundary.

## 3. Some features were moved to Later [TPR-018]

**What was wrong:** The plan contradicted itself: the IIS estimate series split by Win32 error code and the route/status/tenant filter were listed as "Later," yet work packets put them in the first release. The per-call tray was described as unchanged in one place and changed in another.

**What I decided:** The Win32-error-code series and the route filter are Later, and the first-release request has no filter fields. The one allowed exception is `explain()` labels in One file; the per-call tray stays "Not wired." Tests tied to the Later work are retagged as Later.

**Why:** One release must have one honest scope, not two versions of it.

**Cost if wrong:** Work is either skipped that was meant to ship, or shipped that was meant to wait.

## 4. Pointers now point at files that exist [TPR-019]

**What was wrong:** The plan told the agent to read detail files that do not exist in the repo, like sending someone to a library shelf that was never built; and one packet was marked "ready to build" while its table was already written.

**What I decided:** Pointers now target the tracked final-packets file (a copy already sits in `buildout/specs/`) or Appendix P, and the stale draft pointers are dropped. The dependency list names every first-release packet, and packets marked Later are excluded from the gate.

**Why:** An instruction that points at missing files cannot be followed or checked.

**Cost if wrong:** The build stalls or silently skips required work.

## 5. No renumbering of requirements [TPR-026]

**What was wrong:** The requirement id "TR-24" was used for two different things — the coverage table in one section and the rollup capability elsewhere — so any reference to it was ambiguous, like two streets sharing one house number.

**What I decided:** TR-24 is redefined as "coverage ledger and rollups (one capability)," so every existing use stays valid and nothing is renumbered.

**Why:** Fixing the definition is safer than renumbering and breaking every cross-reference.

**Cost if wrong:** Some requirement remains unclear and its test cannot be traced.

## 6. Rollups store whole numbers, not decimals [TPR-013 / TPR-003 / TPR-002]

**What was wrong:** The stored latency sum used decimal floating-point and the spec promised bit-exact results, which decimals cannot guarantee when the same numbers are combined in different orders. Percentiles were stored as single estimates that cannot be correctly merged across buckets — averaging percentiles is simply wrong.

**What I decided:** Store the sum as `sum_us INTEGER` (whole microseconds, with overflow checks); count, min and max stay exact. Replace stored p50/p95/p99 with a per-rollup log-histogram `bins` blob, so percentiles are computed by merging bins, never by averaging. Rollups are rebuilt transactionally for affected buckets at the end of each import and after a delete, and the key stays `(source, kind, bucket_ms, at_ms)`.

**Why:** Integers add exactly, and bins merge honestly, so overlapping or repeated imports never double-count.

**Cost if wrong:** Displayed totals or latency percentiles drift from the true numbers.

## 7. Every lane says how complete it is [TPR-004]

**What was wrong:** A lane that hit the 50,000-row cap or lost a page could still look like it held everything, like a photo claiming to be the whole view when its edges were cropped off.

**What I decided:** Each lane carries `scanned`, `truncated` and `complete`. A raw lane that hit the cap or lost a page is labelled "first N rows" and never claims exact totals; an audit lane aggregate is "partial" if any page failed.

**Why:** A user must be able to tell a complete answer from a cut-off one.

**Cost if wrong:** People make decisions on numbers that quietly miss data.

## 8. Failed pull attempts are logged separately [TPR-006 / TPR-007]

**What was wrong:** The spec wanted a failed pull to roll back completely and also to record the failure — but a record tied to the rolled-back import vanishes with it, so a failed pull could leave either no trace or a broken link.

**What I decided:** The import transaction (events plus coverage rows) still rolls back whole. A small separate `import_attempts` table (source, window, started_at, outcome, error text, pages_ok) is written outside that transaction; the coverage view shows "last attempt failed" from it, and an attempt row never counts as coverage. Coverage slices are half-open and keyed by source plus query fingerprint; an empty ledger returns `[]` with a state, never an "unknown case."

**Why:** The failure history must survive the rollback that erases the data.

**Cost if wrong:** Failures go unseen and windows get treated as fetched when they never were.

## 9. Privacy rules, spelled out [TPR-005 / TPR-016 / TPR-017 / TPR-008 / TPR-009 / TPR-024]

**What was wrong:** Sensitive reads were controlled by request flags anyone could set; export preview and confirm used independent flags, so you could preview safely and confirm something else; and calling the cookie key a "salt" blurred a threat model.

**What I decided:** No role system — the case key is the authorization. Export confirm must carry the `previewId` from preview, bound to the case revision and the scrub flag; a mismatch or stale preview returns 409 and needs a fresh preview. The scrub pill says what is hidden in the export ("export hides IP, referer and cookie hash"), never that stored data is masked. Cookie hashing uses a per-case random 32-byte HMAC key (not a "salt"), stored only inside the encrypted case.db, domain-prefixed, described as pseudonymous not anonymous, first duplicate name wins, no rotation in the first release. The allowlist lives in case settings (proposed); until set, hashing is off and the UI says so. Canary IP/referer/cookie tests run for every export path.

**Why:** A flag is not a permission, and a preview must match what gets confirmed.

**Cost if wrong:** Sensitive identifiers leak, or a user exports more than they previewed.

## 10. Requests are strict and checked first [TPR-010]

**What was wrong:** Unknown fields were accepted silently, source ids were not checked for case membership, duplicates were unbounded, and size limits were unclear — so a bad or hostile request could open a live connection before anyone noticed.

**What I decided:** Unknown JSON fields return 400; source ids must belong to the case (else 404 or 400, as section 5.3 names it); ids are de-duplicated and capped at 50 (proposed); string and body sizes are capped; all validation happens before any live connection opens.

**Why:** Cheap checks must reject bad input before expensive reads start.

**Cost if wrong:** Wasted load, confusing errors, or reads against another case.

## 11. Windows-server timing stays a point until confirmed [TPR-012]

**What was wrong:** The spec drew an assumed request start from a timestamp whose meaning was never confirmed, and its time-taken value includes network time, so presenting it as "API processing time" would be false.

**What I decided:** WP-20 depends on WP-45 (VF-01). Until VF-01 confirms the semantics, a Windows-server event is drawn as a point at its timestamp with the observed time-taken shown separately; the UI never claims a verified request start and never calls time-taken processing time.

**Why:** A chart must not assert a fact nobody has verified.

**Cost if wrong:** Users infer wrong request durations and chase false problems.

## 12. Only one trace at a time [TPR-020]

**What was wrong:** Nothing limited repeated Start clicks, hanging sources or overlapping expensive reads, so twenty impatient clicks could launch twenty heavy queries.

**What I decided:** At most one in-flight trace per case; a new Start cancels the previous request through the request context; server timeout 60 s (proposed); the UI ignores stale responses; response size is capped; identical in-flight requests are not duplicated.

**Why:** Bounded concurrency protects the app and the data sources.

**Cost if wrong:** Leaked queries, slow responses, and a stale result overwriting a fresh one.

## 13. Both test suites gate the release [TPR-022]

**What was wrong:** Archiving 78 guided tests moved them out of routine checks while the guided feature stayed reachable, so regressions could slip through unseen.

**What I decided:** The archived guided suite runs in the pre-release gate and on a schedule (documented in WP-62 and the release checklist); WP-58 runs the main suite only, so the gate is "main + archive both green."

**Why:** Moving a test is not proof the feature still works.

**Cost if wrong:** Broken guided flows ship unnoticed.

## 14. Migrations, response shape, and the Start control [TPR-014 / TPR-021 / TPR-023]

**What was wrong:** Migration code used "IF NOT EXISTS," which checks nothing about table shape or version; the response example was missing fields scenarios already required; and the Start control's focus-return pointed at a control that did not exist.

**What I decided:** Migrations verify table shape and version inside one transaction; `PRAGMA foreign_keys=ON` runs per connection with `foreign_key_check` on open; a failed migration rolls back and a newer-schema file is refused. The section 5.1 JSON example gains per-lane `reason`, top-level `disabled` and the new completeness fields. The Start control lives where sections 1 and 6 already say, focus returns to the control that opened the trace, and the spec names that control once.

**Why:** A migration must verify, not assume; a response must render from itself; and focus must return to a real element.

**Cost if wrong:** Corrupt or half-migrated databases, broken UI, and keyboard users losing their place.

## 15. Two proposals rejected or deferred [TPR-005 / TPR-015 / TPR-025]

**What was wrong:** One proposal wanted a full roles and permissions system; another wanted per-event hydration storage and a database profiler in the first release, though neither was worked out.

**What I decided:** The roles/authorization system is not accepted. Hydration storage and the profiler stay in the later phase: add one sentence each — "(proposed) side table `event_hydration(event_id, field, value)`" and "profiler enabling is a separate explicit administrator action, never part of tracing, default off."

**Why:** This is a local single-user app, so a role system adds weight without benefit, and unfinished storage should not ship.

**Cost if wrong:** Either needless complexity now, or a deferred feature arrived without a retrieval path.

---

## Needs your OK

These rulings change an earlier owner choice or an earlier ruling, so they need your sign-off.

| # | Change | Ruling |
|---|---|---|
| 1 | Windows become half-open instead of inclusive of the end time | 2 (TPR-011) |
| 2 | IIS Win32-error-code series and route filter pushed to Later | 3 (TPR-018) |
| 3 | Failed-pull attempt log written outside the import transaction | 8 (TPR-006/007) |
| 4 | No role system; the case key is the only authorization | 9, 15 (TPR-005) |
| 5 | Latency sum stored as integer microseconds, not a decimal | 6 (TPR-013) |
| 6 | Export confirm bound to the preview it came from | 9 (TPR-017) |
