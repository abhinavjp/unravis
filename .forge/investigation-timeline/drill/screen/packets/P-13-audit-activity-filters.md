# P-13 Activity input accepts "only this user" and "only this API"

model: sonnet, effort: medium. Reason: input validation that guards a database query.
risk: medium. Review batch: RB-3 (and security batch RB-6). Wave: W0. Requirement: R-11 (BL-007).

## Outcome
`audit.ActivityInput` carries an optional user id and an optional operation, validates both strictly, and the demo repository honours them.

## Write scope
- `internal/audit/activity.go`
- `internal/audit/activity_demo.go`
- `internal/audit/activity_test.go` (existing or new; add tests, keep old ones)

## Must not change
- Existing fields and JSON tags of `ActivityInput` (`sourceIds,start,end,cursor,limit`), the 24 h rule, `MaxPageSize`. Other files in `internal/audit`. No other package.

## Changes
1. In `ActivityInput` (`internal/audit/activity.go`, about line 11) add:
```go
	// UserID keeps only calls made by this user. Nil means everyone.
	UserID *int `json:"userId,omitempty"`
	// Operation keeps only calls to one API, written "Controller.Action". Empty means every API.
	Operation string `json:"operation,omitempty"`
```
2. In the function that validates `ActivityInput` (same file; find it by the 24 h check) add: `UserID` non-nil must be `>= 0`; `Operation` non-empty must match the same rule `audit.Query` applies to its `Operation` field. First find that rule (`grep -n "Operation" internal/audit/*.go`) and call the same helper. If `Query` has no validator for it, add an unexported `validOperation(s string) bool`: exactly one dot, each side 1..100 characters of `[A-Za-z0-9_]`. Return the package's usual validation error type with a short message that does not echo the value.
3. `activity_demo.go`: after the demo rows for the window are chosen and before paging, drop rows that do not match `UserID` (compare with the row's `UserID *int`, a nil row user never matches) and `Operation` (`Controller + "." + Action`, exact, case-sensitive).
4. Tests (table-driven): accepted: nil/empty, `userId 0`, `userId 7`, `Orders.Get`. Refused: `userId -1`, `Orders`, `Orders.Get.More`, `Orders.$where`, `{"$ne":1}.x`, a 300-character name, a name with a space. Demo: filter by user returns only that user's rows; filter by operation returns only that operation; both together.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/audit/` → no output.
- [ ] `go test ./internal/audit/ -run 'Activity' -count=1` → `ok`.
- [ ] `go test ./internal/audit/ -count=1` → `ok`.
- [ ] The refusal message for a hostile operation does not contain the hostile text (asserted in a test).
- [ ] Demo path verified by test. Live path is P-17 and P-18.

## Depends on
Nothing. Only Go writer in `internal/audit` during W0.
