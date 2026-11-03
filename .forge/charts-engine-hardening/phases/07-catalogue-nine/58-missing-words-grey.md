# TSK-58 missing-words-grey

risk: medium — the grey reason is what the user reads when data is absent; a wrong word sends them after the wrong file.

## Outcome

The three new missing sources read in plain words, and a test proves each of the nine new suspects is Grey ("Cannot test yet") when its data is absent:

| Missing | Words |
|---|---|
| `httperr` | no HTTPERR log imported |
| `substatus` | the IIS rows carry no sub-status |
| `payload` | no PayloadRecordTime in the audit |

## Write scope

- `internal/investigate/evaluate.go` (`missingWord` only)
- `internal/investigate/sus_grey_test.go` (new)

## Must not change

- The four existing `missingWord` cases and the default; `colour` (TSK-57 is done).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `missingWord` (~277–289): add before the closing `}` of the `switch`
   ```go
   case "httperr":
       return "no HTTPERR log imported"
   case "substatus":
       return "the IIS rows carry no sub-status"
   case "payload":
       return "no PayloadRecordTime in the audit"
   ```
2. `sus_grey_test.go` (`package investigate`):
   - `TestNineGreyWithoutData`: start from `fullInput()` (`readiness_test.go`); set every `Request.P` to nil, `HTTPErrPresent = false`, `HTTPErr = nil`, `IISPresent = false`, `IIS = nil`, `Lanes = nil`. `r := Analyze(in)`. For each of `S8 S9 S10 S11 S12 D16 D17 M11 M12`: `wantSuspect(t, r, id, ResCannotTest, Grey)`. Then check the why text: S8 contains "no PayloadRecordTime in the audit"; S9 contains "no IIS data"; S10, S11 contain "no HTTPERR log imported"; D16, D17, M11, M12 contain "It needs:".
     S12 with no IIS and no HTTPERR: its why contains "no IIS data" or "no audit data" (it carries the `o_large` source); assert `ResCannotTest`/Grey only, and log the why.
   - `TestS9GreyWithoutSubStatus`: `fullInput()` with `IISPresent = true`, some IIS rows with empty `SubStatus`, `IISSubStatus = false` → S9 `ResCannotTest`, Grey, why contains "the IIS rows carry no sub-status".
   - `TestNineNeverGreenWithoutData`: in the first input, none of the nine is `Green` or `ResContra`.
   If `fullInput()` has a different name or shape, read `readiness_test.go` and use its closest full-input builder; do not change that file.
3. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestNineGreyWithoutData|TestS9GreyWithoutSubStatus|TestNineNeverGreenWithoutData|TestMissingDataNeverContradicts"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
.dev\toolchains\go\bin\go.exe build ./...
```

PASS. If a suspect is not Grey, record the id, result and why in `progress.md` and stop (BLOCKED); the fix belongs to that suspect's packet.

## Depends on

57 (same file, in order), and 45–56 (all signals and lanes in place).
