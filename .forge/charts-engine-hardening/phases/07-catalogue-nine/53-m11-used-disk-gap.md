# TSK-53 m11-used-disk-gap

risk: medium — widens an importer allowlist and the case-export policy; a loose policy would let free text out of a case file.

## Outcome

The profiler's `usedDisk` flag is kept: the `mongo-profile` importer stores it as the attr `usedDisk` (`"true"`/`"false"`), the case-export policy allows it as a boolean enum, and the live Mongo lane's profiler read projects it. Nothing reads it yet (TSK-54).

## Write scope

- `internal/importer/mongoexports/profile.go`
- `internal/importer/mongoexports/profile_test.go` (append; or the test file that already covers `NewProfile` — grep `NewProfile(` in `*_test.go`)
- `internal/evidence/caseexport.go` (the `mongo-profile` policy line only, ~213)
- `internal/dbhealth/lane_mongo.go` (`profileFind` projection only, ~102)

## Must not change

- Any other attr, policy or importer; `caseexport_enums.go` (TSK-55 owns it in this phase).
- `evalLaneProfiler`, `checkProfiler` and the `steps` list (TSK-54).
- `internal/dbhealth/mongo.go` (~383, the older diagnostics read).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `profile.go` `profileAttrs` (~30–31): append `"usedDisk"`.
2. `opFields` (~76–84): add `usedDisk string`. (The struct is shared with the currentOp parser; an empty field changes nothing there.)
3. Key switch (~100–135): add
   ```go
   case "usedDisk":
       switch strings.TrimSpace(string(raw)) {
       case "true":
           f.usedDisk = "true"
       case "false":
           f.usedDisk = "false"
       default:
           f.bad = true
       }
   ```
   Check first whether the package has a bool parser (grep `func .*bool` in `internal/importer/mongoexports/*.go`); if it has one, use it. Add the `strings` import only if missing.
4. Attrs build (~158–173): after the `queryHash` block add `if f.usedDisk != "" { attrs["usedDisk"] = f.usedDisk }`.
5. `internal/evidence/caseexport.go` ~213: the `mongo-profile` entry becomes
   ```go
   "mongo-profile": withPolicy(mongoOpPolicy, map[string]attrPolicy{"responseLength": polNum, "usedDisk": polEnum(boolValues)}),
   ```
   Read `boolValues` (`caseexport_enums.go` ~523) to confirm it holds `"true"` and `"false"`; if its spelling differs, store that spelling in step 3 instead.
6. `lane_mongo.go` `profileFind` (~102): projection `{"op":1,"ns":1,"millis":1,"planSummary":1,"ts":1}` → `{"op":1,"ns":1,"millis":1,"planSummary":1,"ts":1,"usedDisk":1}`. Then grep `lane_mongo_test.go` and `mongo_test.go` for the old projection string; if a test pins it, update that test's literal only.
7. Tests, appended to the profile importer test file:
   - `TestProfileUsedDisk`: a line with `"usedDisk":true` → event attr `usedDisk == "true"`; `"usedDisk":false` → `"false"`; no key → attr absent; `"usedDisk":"yes"` → the row is rejected (`ReasonBadNumber`), same as a bad count.
   - In `internal/evidence`, find the test that checks every importer attr is covered by the export policy (grep `profileAttrs` or `withPolicy` in `*_test.go`); if one exists it must pass unchanged. If none exists, add `TestCaseExportAllowsUsedDisk` in a new `internal/evidence/caseexport_useddisk_test.go` that exports a `mongo-profile` event with `usedDisk: "true"` through the same entry point the existing caseexport tests use and checks the attr survives, and `usedDisk: "maybe"` is refused or dropped (whichever the policy does for other enums; read one enum test first).
8. gofmt every touched Go file.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/importer/mongoexports/... ./internal/evidence/... ./internal/dbhealth/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS.

## Depends on

Phase 06 complete (ordering only; no file overlap with it).
