# TSK-02: Offer folder names as suggestions
risk: low — pure function, no I/O.

## Outcome
Each finding carries optional `suggest` hints derived from its folder names (a path ending `...\clienta-prod\portal\api-orders\logs` yields portal "portal" and api "api-orders"). Suggestions only: never applied by the server, never filled in silently (R7).

## Write scope
- `internal/discover/suggest.go` (new), `internal/discover/suggest_test.go` (new)
- `internal/discover/discover.go` (add `Suggest *Suggestion` json `suggest,omitempty` to `Finding`; set in `RunWith` from paths)

## Must not change
- `Finding.paths` and what is sent to the client: only derived words are sent, never a full path (I1). Drive letters, user-profile names and machine names must never appear in a suggestion.

## Changes
1. `type Suggestion struct { Portal, API string }` (words only).
2. `suggestNames(paths []string) *Suggestion`: take the nearest meaningful folder segments above the log folder, drop noise segments (`logs`, `log`, `bin`, `app_data`, `inetpub`, `wwwroot`, `program files`, `users`, `windows`, `temp`, drive letters), cap each word at 40 chars, strip anything outside `[A-Za-z0-9 ._-]`; nil when nothing meaningful remains.

## Proof
- Table test, 8 paths (IIS inetpub, log4net under an app folder, user-profile path, UNC path, deep path, only noise, very long name, unicode) → expected suggestions; a path containing `C:\Users\Name\` never yields `Name`.
- `go test ./internal/discover/...` green.

## Depends on
none
