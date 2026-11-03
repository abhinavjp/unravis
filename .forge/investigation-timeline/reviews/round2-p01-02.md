# Round 2 review: phases 01-iis-privacy-export and 02-tooltip-dictionary

Scope: phase.md and WP-01a, 01b, 02a, 02b, 03, 04, 05, 06a, 06b, 07a, 07b, 65, 65a, 65b (phase 01); phase.md, WP-08, WP-09 (phase 02). Read-only; no plan or source file was edited.

Counts: BLOCKER 0, MAJOR 1, MINOR 6.

Round-1 findings still open: none.

## Round-1 verification

All round-1 findings (2 BLOCKER, 3 MAJOR, the size MAJOR, 7 MINOR) were checked against the packet files and are fixed. The key evidence is in the packets read this round:

- Plan §3 table and phase.md now agree (16 packets in phase 01, edges at phase.md:38-51, writer table at phase.md:57-74); WP-02 is split into WP-02a and WP-02b and the old packet is gone.
- WP-04 (`WP-04-httperr-client-ip.md`): `Depends on` is `WP-45, WP-02b, WP-03` (line 38) and wave is 5 (line 2); the `exportDropped` edit for `iis-httperr` is in scope (lines 11, 23); the drift test reason is stated; `TestHTTPERRGood` edit is spelled out (line 24); every test named in Proof (line 33) is defined in Changes (lines 26-30).
- WP-05 (`WP-05-iis-privacy-tests.md`): `Depends on` is `WP-03, WP-04` (line 35), wave 6; replacement tests for `TestW3CDropsSensitiveColumns` are defined (lines 18-22) and every name in Proof (line 28) is defined in Changes; `TestW3CTimeIsEndGap` is untouched and checked by the diff grep (line 32).
- WP-08 (`WP-08-tooltip-dictionary.md`): 23 entries listed (lines 19-41); `explain` uses `Object.hasOwn` and never throws (line 17); the five tests in Proof (line 51) match the five in Changes (lines 44-48); no imports (line 48).
- WP-09 (`WP-09-tooltip-wiring.md`): depends on WP-08 (line 37); module cache for the shared dictionary instance is specified (line 26); five tests in Proof (line 31) match the five in Changes (lines 24-28); `escapeHTML` is applied (line 16).
- Copy lint: dictionary text avoids the causal words banned by `internal/evidence/copylint.go` and the web lint.
- Case revision bracket (WP-65, 65a, 65b) and export policy changes (WP-07a, 07b) agree with the real symbols found: `bumpCaseRevision`, `Revision()`, `exportPolicy`, `exportDropped`, `verifyStatements`, `claimFree`, `scrubText`, `residualRisk`.

## Findings

[MAJOR] WP-07b — `TestBuildCaseScrubOffReducesBadShapes` uses a 257-byte referer, which cannot reach the scrub-off export path through `BuildCase`. `exportEvents` (`internal/evidence/caseexport.go:590`) calls `ev.Validate(own)` before applying the export policy, and `Event.Validate` (`internal/evidence/event.go:140`) rejects any attribute value longer than `MaxEventAttrValue` (256) with an invalid-event error. The case build would fail instead of reducing or dropping the value, so the named test cannot pass as written (WP-07b line 31). — Fix: either build the over-length case at helper level (call `sensitivePolicyFor` / `exportAttr` directly with a 257-byte value and assert it is dropped), or keep the `BuildCase` test to shape-only bad values within 256 bytes (non-http scheme, userinfo, spaces, IPv6 without brackets). State in the packet that the length rule is covered by the helper test, not by `BuildCase`.

[MINOR] WP-07b — `caseHeadlineScrubOff` text is described, not given verbatim, so the lint expectation in the Proof cannot be checked from the packet. The statement must be `claimFree`, satisfy `scrubText(s) == s` and contain no `residualRisk` hit (`caseexport_verify.go:56`, `mask.go:419`, `mask.go:473`). — Fix: put the exact sentence in Changes and add it to the test that runs `verifyStatements` on a scrub-off case.

[MINOR] WP-07b — `resolveScrub(scrub)` is used in two steps with no stated type for `scrub` (pointer to bool, bool, or request field) and no stated default when the field is absent. The TR-21 rule is "ON by default", so the nil/absent case needs one explicit line. — Fix: add "absent or null means ON" next to the first use and a test row for it.

[MINOR] WP-01b / WP-07a and WP-45 / WP-02b — packets that write the same file are ordered by wave number only, with no dependency edge. phase.md:76 says the graph "puts every pair of writers of one file in a fixed order"; that is true by wave, not by edge, so a wave reshuffle would silently remove the ordering. — Fix: either add the two edges, or reword phase.md:76 to "by wave number" and state that `check-plan.ps1` enforces wave order.

[MINOR] WP-07b — HTTPERR `client-ip` is omitted from the scrub-off export, while the scrub-off statement wording speaks of "client addresses kept for IIS" without naming the HTTPERR source. A reader of the exported case can be told addresses are present when HTTPERR events have none. — Fix: name both IIS sources in the statement, or state that `iis-httperr` client addresses stay in `exportDropped` and the wording says "web server log (W3C)".

[MINOR] WP-65 — the `export_finished` assertion is vague ("the record is written"). The test should name the fields that must be present and the ones that must not (no previewId contents, no scrub-off payload). — Fix: list the exact audit fields in Changes and assert them in the named test.

[MINOR] WP-01b / phase.md — WP-01b mentions the `tMeta` fixture but does not list the file that defines it in Write scope, and phase.md does not show the `65a -> 65` edge that WP-65's `Depends on` lists. `check-plan.ps1` compares plan.md §3 only, so this gap would not be caught. — Fix: add the edge to phase.md (edges list at lines 38-51) and name the defining file in WP-01b Write scope or Must not change.

## Areas with no findings

- Phase 02 (phase.md, WP-08, WP-09): none beyond the verified fixes.
- WP-01a, WP-02a, WP-02b, WP-03, WP-06a, WP-06b, WP-65a, WP-65b: no new defects found; test names in Proof are defined in Changes, paths and symbols spot-checked against the repo.
- Owner decisions R6-02 and R6-03: not treated as findings.
