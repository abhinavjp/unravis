# TSK-02 Catalogue and wizard copy
Risk: medium (wrong or invented figures break trust). Worker size: medium. Reason: careful reading of Go types plus plain-language writing.

## Outcome
`web/src/lib/blackboxCatalogue.ts` and `web/src/lib/copy/blackboxWizardCopy.ts` follow contract C-2. Each figure has Details (what, why, what to look for, what is stored). All copy passes copy lint.

## Write scope
- `web/src/lib/blackboxCatalogue.ts` (new)
- `web/src/lib/copy/blackboxWizardCopy.ts` (new)
- `web/tests/blackbox.wizard.copy.test.cjs` (new)

## Must not change
`web/src/lib/copy/blackboxCopy.ts` (BB_TEXT, BB_REFUSAL), any Go file.

## Changes
- Groups from `internal/blackbox/groups_types.go` and `sqlhost.go` only. Each group says if it is always on, tied to one of the 3 record booleans, or "automatic when present" (IIS, SQL host, ASP.NET, CLR and the like). Machine counters are always on.
- The 13 SQL host counters match `SQLHostInstance` fields one for one.
- No VM group (BL-113). No deeper checks as items; one read-only line only (B-02).
- Name hint follows B-06 (no spaces). "Watch closely" uses the electricity meter analogy (S-09).
- Folder notices: C:, unlisted letter, USB, network/UNC (B-04 copy).
- Lead the wireframe's wording where it is correct; fix it where decisions.md says the code wins.

## Proof
`cd web && node --test tests/blackbox.wizard.copy.test.cjs && npm test`. Tests: every entry passes `lintScreenStrings`; 13 SQL figures; no figure id repeats; no group named VM; every figure has all four Details parts.

## Depends on
None.
