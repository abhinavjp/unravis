# TSK-57 cap-orange

risk: high — edits the red/orange rule every run goes through; only the capped suspect may change.

## Outcome

A catalogue entry with `Cap: "orange"` (S9 only) is never coloured red. Where it would have been the lone red, it is orange with a plain reason. No other suspect's colour or reason changes; in particular the near-tier set is unchanged, so a capped suspect still counts as a tier that moved.

## Write scope

- `internal/investigate/evaluate.go` (`colour` only)
- `internal/investigate/evaluate_cap_test.go` (new)

## Must not change

- Every other case of the `colour` switch, their order and their text; `evaluateOne`; `missingWord` (TSK-58).
- `catalog.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `colour` (~296–372), just before the `for _, i := range comp {` loop that holds the colour `switch` (~340), add
   ```go
   caps := catalogIndex()
   ```
2. In that `switch`, insert a new case **directly above** the plain red case
   ```go
   case near(sp) && len(nearTiers) == 1:
       sp.Colour = Red
       sp.Why += " Its tier moved first."
   ```
   (find it by its text; Phase 02 may have moved line numbers). The new case:
   ```go
   case near(sp) && len(nearTiers) == 1 && caps[sp.ID].Cap == "orange":
       // Counted rows alone do not date a start, so this suspect is never called
       // the first (catalogue Cap).
       sp.Colour = Orange
       sp.Why += " Its tier moved first, but counted rows alone do not date a start, so it is not called the first."
   ```
   It sits after the unread-tier and load-retry cases, so those keep their own wording. Check the comment and the why text with the causal lint words (no "because", "due to", "caused" …).
3. `evaluate_cap_test.go` (`package investigate`): `TestColourCapOrange`. Build the suspects by hand and call `colour` directly (read how `colour`'s arguments are used: `Facts.Onset.Status = "found"` so ranking runs; the two suspects need `Result: ResConsistent`, a `Tier`, an `OnsetAt`):
   - one suspect `S9`, tier `o`, alone → `Orange`, why ends with "not called the first.";
   - one suspect `S1`, tier `o`, alone → `Red` (uncapped control);
   - `S9` and `S1` both tier `o`, same onset → `S1` `Red`, `S9` `Orange` (same tier, so one near tier);
   - `S9` tier `o` earlier and `L1` tier `load` 30 minutes later → `S9` `Orange`, `L1` `Orange` with " Its tier moved after an earlier one." (unchanged default case).
4. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestColourCapOrange|TestEvaluate|TestOrigin"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (`CatalogEntry.Cap`, S9 in the catalogue).
