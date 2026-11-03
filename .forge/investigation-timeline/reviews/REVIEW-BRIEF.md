# Reviewer brief (plan review, read-only)

You review a build plan. You do NOT edit any plan file, source file, or run builds. Write your findings to the file named in your assignment and return a report of at most 20 lines. Use absolute paths; do not `cd`. Never read `sql/`; never print secrets.

Repo `D:\Projects\api-audit-explorer`. Plan root `D:\Projects\api-audit-explorer\.forge\investigation-timeline\` (plan.md, AUTHORING-BRIEF.md, check-plan.ps1, phases/NN-slug/phase.md and WP-*.md). Spec (466 KB; NEVER read whole, Reads max 120 lines; Grep -n first): `D:\Projects\api-audit-explorer\.dev\spec-src\2026-10-30-tracing-paper-impl-spec.md`. Owner decisions already approved (R6-02 aligned UTC axis, R6-03 409) are not findings.

Goal of the plan: a workhorse (cheap) model must be able to implement each packet blindly, without exploration, and packets are small, with a correct dependency graph and safe parallel execution.

Standard criteria (flag only real problems, rank by severity BLOCKER / MAJOR / MINOR):
1. Implementability: every step names file, symbol, exact signature, exact values; no vague steps ("handle edge cases"); tests named with assertions; Proof commands runnable and correct (right package paths, `-run` names that match tests defined in the packet).
2. Truth against the code: SPOT-CHECK with Grep/Read that referenced files, symbols, signatures, line anchors exist in the repo today (or are created by an earlier packet). Report every mismatch with file:line evidence.
3. Dependency graph: each packet's `Depends on` covers every symbol/file it consumes that another packet creates; no missing edge, no invented edge; plan.md §3 table equals packet files (check-plan.ps1 already checks equality and wave order, so do not re-check those).
4. Write scope: sufficient for the Changes (every file a step edits is listed); no packet edits files outside scope; Must-not-change protects the real hazards (plan.md §2 INV-1..8).
5. Size: S <= ~60 lines one file, M <= 3 files ~200 lines; flag packets that are clearly bigger.
6. Spec/requirements coverage and security/privacy (no raw cookies, IPs only as designed, no query strings), testability, no hard timing assertions.
7. Token economy: no packet tells the implementer to read the whole spec or large files.

Finding format (in your file and report): `[SEVERITY] <packet or plan section> — <problem, with evidence> — <exact fix>`. If nothing is wrong in an area, say "none". Do not pad. Do not re-raise issues listed as "accepted" in plan.md §10 or phase.md escalation notes unless they are unsafe.
