# Round 1 review: phase 01-iis-privacy-export and phase 02-tooltip-dictionary

Scope: phase.md plus 15 packets (WP-01a, 01b, 02, 03, 04, 05, 06a, 06b, 07a, 07b, 45, 59, 65, 65a, 65b) of phase 01, and phase.md plus WP-08, WP-09 of phase 02. Anchors were checked against the Go and web code. Criteria 1, 2, 3, 4 and 6 were applied strictly.

## BLOCKER

[BLOCKER] WP-06a — `TestCaseExportConfirmAfterChangeIs409` cannot pass in its wave. The Import revision bump arrives in WP-65b (wave 3), and WP-06a depends only on WP-65a. `newIntEnv` uses the real sqlite store, so after an import the revision is still 0 and the confirm returns 200, not 409. — Drop that test from WP-06a (WP-65's `TestCaseConfirmAfterImportBeforeStartIs409` already covers it), or add WP-65b to WP-06a's Depends on and move the packet to wave 4 or later.

[BLOCKER] WP-07b step 4 — `scrubOffStatement` contains "because scrubbing was turned off". `verifyStatements` requires `claimFree(s)`, which calls `LintCausal`, and `causalRe` (internal/evidence/copylint.go:18) bans "because". Every scrub-off export therefore fails `verifyCase`, so `TestBuildCaseScrubOff` cannot pass. — Reword to "since scrubbing was turned off" (the file already uses "since") or drop the reason clause; re-run `LintCausal` over the final text in the test.

## MAJOR

[MAJOR] phase 01 phase.md — the Outcome table, edge diagram and frontier text contradict plan.md §3 and the packet headers. WP-04 is shown as wave 2 with dependency 45; the packet and §3 say wave 4 with deps 45, 02, 03. WP-05 is shown as wave 4 with dep 03; the packet says wave 5 with deps 03, 04. The line "equal plan.md §3 except WP-65b" is false, "15 files" should be 16, and the WP-02/WP-04 same-wave overlap note is wrong (they are no longer in one wave). A blind implementer who reads phase.md first will start WP-04 early. — Regenerate the table, diagram, frontier and notes of phase.md from plan.md §3.

[MAJOR] WP-65b / WP-01b escalation is only partly resolved. The edge WP-01b -> WP-65b is now present (Depends on: WP-65a, WP-01b). The "allowlist and key each raises by 1" wording is not resolved: `TestRevisionCookieKeysBump` still says "call the WP-01 setter for the allowlist and for the HMAC key; each raises the revision by exactly 1", but WP-01b has one setter, `SetCookieAllowlist`, which writes both meta rows (`cookie.allowlist`, `cookie.hmac_key_v1`) in one transaction. One call is one bump, not two. The write scope "the file(s) that WP-01 added" and the step 5 search leave the implementer to explore. The bump rule for a clear call or a no-op call (same list again) is unspecified, and it must agree with the phase.md clumsy-review rule that a no-op write does not bump. — In WP-65b: name `internal/casestore/sqlite/cookiepolicy.go` and `SetCookieAllowlist` in Write scope and step 5; rewrite the test as "one `SetCookieAllowlist` call with a changed list raises the revision by exactly 1; a second identical call leaves it unchanged (or also +1, state which)". Make WP-01b state the same rule so the two packets agree.

[MAJOR] WP-09 test "tooltip text is escaped" — the `loadTypeScript` helper in the web tests has no module cache. The test's `require` of the dictionary yields a different instance from the one `charts.ts` loaded, so mutating `DICTIONARY['latency.p95'].plain` in the test cannot reach the formatter and the assertion fails. — Copy the helper with a `Map` cache keyed by resolved path so `charts.ts` and the test share one dictionary instance, or have the test load `charts.ts` through a custom require that returns the test's own dictionary object.

[MAJOR, size] WP-02 — 5 files, 12 steps, 8 tests and 4 new helpers (`validIPv4or6`, `safeReferer`, `splitCookiePairs`, `HashCookie`): well over M. — Split into WP-02a (`common.go` helpers and their unit tests) and WP-02b (`w3c.go` wiring, fixture edit, `exportDropped`, edits to existing tests, integration tests), with WP-02b depending on WP-02a, and update plan.md §3.

## MINOR

[MINOR] WP-06b step 4 — reads `src/components/CaseTools.svelte`; the real path is `src/lib/components/CaseTools.svelte` (Write scope has it right). The test would throw ENOENT. — Correct the path in step 4.

[MINOR] WP-06a — `caseRevisionOf` should unwrap with `innerCase(c)` before the optional-interface assertion. `*sharedCase` embeds `Case` and does not promote `Revision`, so a handle wrapped by `openCase` would silently report 0. The export path uses `openCaseForWrite` (raw case), so it is safe today only. — Call `innerCase(c)` first and add a one-line comment.

[MINOR] WP-02 — adding `c-ip`, `cs(referer)` and `cs(cookie)` to `keep` makes `declare` quarantine a `#Fields` line that lists any of them twice (duplicate kept column), which imported before. — Document it in Must-not-change as an accepted change, or skip duplicates for these three columns only; add one test either way.

[MINOR] WP-07b — the referer rule keeps up to 512 bytes, but `verifyEvents` rejects attr values over `MaxEventAttrValue` (256), so a 257 to 512 byte referer passes the builder and fails the verifier. — Cap at 256 bytes. Also: 4 files in Write scope is over M; consider moving the `case_views.go` wiring into WP-06a or a separate packet.

[MINOR] WP-65a note 4 — says WP-65b "edits `case.go` in wave 2"; WP-65b is now wave 3. — Change "wave 2" to "wave 3" (or drop the wave number).

[MINOR] WP-08 — the module exports `deck`, not `terms`; write `const { deck } = loadTypeScript(...)` and iterate `deck.terms`.

[MINOR] WP-03 has 6 files (mostly one-line edits) and WP-45 has a large test file for size M; acceptable, but watch for overrun.

## Areas with no findings

- WP-01a, WP-01b: file, symbol and line anchors true; test counts (6 and 7) match.
- Injection site `importFileWith` (cases.go:1614), `toEventDTO`, `caseEvents` and the `.Attrs` readers: none.
- WP-03, WP-04 (anchors, wildcard `cookie:*` permission, `exportDropped` drift test), WP-05, WP-59 (querystore.go anchors and constants), WP-65, WP-65a (test counts 7 and 6): none.
- Privacy coverage (no raw cookies, IP only as designed, no query strings, referer cleaned), spec TR-22 coverage: none.
- Phase 02: anchors (charts.ts lines 10, 38, 120; CallTrace lines 10, 121), dependency graph, INV-6 (no chunk cycle) and INV-7 (CallTrace gets explain labels only): none. The 23 dictionary entries pass `lintTerms` and `lintScreenStrings` and `JARGON` (checked with an inline run).
- Hard timing assertions: none found.
- Token economy (criterion 7): none; no packet tells the implementer to read the whole spec.
