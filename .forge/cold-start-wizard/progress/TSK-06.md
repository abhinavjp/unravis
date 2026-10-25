# TSK-06 report: schema v4 and Tagger

Status: done. `go test ./internal/casestore/...` green (casestore, sqlite); `go build ./...` and `go vet ./internal/casestore/...` clean. Nothing committed.

## Files changed
- `internal/casestore/store.go`: `CaseInfo.Envs/Sources`, `Tagger`, `SourceTag`, bounds, `ErrBadTag`, `CleanEnvs`, `CleanSource`.
- `internal/casestore/memory.go`: Tagger on the in-memory case; `Info()` counts; erase clears tags.
- `internal/casestore/sqlite/schema.go`: `schemaVersion = 4`, `tagsSQL` (appended to `schemaSQL`, so `createDB` needed no change), `refusesSchema`.
- `internal/casestore/sqlite/discover.go`: `migrate` also runs `tagsSQL` and writes 4 in the same transaction; always ends with `loadTagCounts`.
- `internal/casestore/sqlite/tags.go` (new): Tagger on the SQLite case.
- `internal/casestore/storetest/tags.go` (new) + one `t.Run` line in `storetest/contract.go`.
- `internal/casestore/sqlite/tags_test.go` (new).

## API for TSK-07/08 (package `casestore`)
```go
const (
    MaxEnvNameChars = 60; MaxTagTextChars = 80; MaxEnvs = 32; MaxSources = 200
    ClassAudit = "audit"; ClassOther = "other"; ClassUnused = "unused"
)
var ErrBadTag error // bad name/field/classification; never echoes input. Over-cap uses existing ErrCapExceeded; closed case uses ErrCaseClosed.

type SourceTag struct {
    ID             int64     // assigned by AddSource; ignored on input
    ImportID       int64     // 0 = none (stored NULL); not checked against Imports
    Kind           string    // required, <= 80 chars
    Label          string    // <= 80
    Env            string    // <= 60, free text
    Portal         string    // <= 80
    API            string    // <= 80
    Classification string    // "", "audit", "other", "unused"
    Created        time.Time // assigned by AddSource (UTC, ms); ignored on input
}

type Tagger interface {
    SetEnvs(ctx context.Context, names []string) error
    Envs(ctx context.Context) ([]string, error)
    AddSource(ctx context.Context, s SourceTag) (SourceTag, error)
    Sources(ctx context.Context) ([]SourceTag, error)
    RemoveSource(ctx context.Context, id int64) error
}
// Obtain it by assertion: g, ok := c.(casestore.Tagger). Both stores implement it.

func CleanEnvs(names []string) ([]string, error)
func CleanSource(s SourceTag) (SourceTag, error)

type CaseInfo struct { ...existing...; Envs int; Sources int }
```
Semantics: `SetEnvs` replaces the whole list (nil clears), trims, drops blanks, collapses case-insensitive duplicates to the first spelling, keeps order; the 32 cap counts after dedupe. `Sources` oldest first (by id). `RemoveSource` of a missing id returns nil. Writes on a closed case return `ErrCaseClosed`; reads still work; closed handle `ErrHandleClosed`; erased case `ErrNotFound`. Text rejects control chars, bidi/format chars (Cf), line/paragraph separators and invalid UTF-8; limits count characters (runes), not bytes. A source `Env` need not match a `SetEnvs` name.

`Info()` carries `Envs`/`Sources` from `Case.Info()` only. `Store.List` still reads the manifest alone, so its `CaseInfo` has `Envs == 0 && Sources == 0`. TSK-13 must not expect counts from List; it needs an opened case, or a separate task must extend the manifest.

## Tests
- Contract `tags-and-environments` (storetest/tags.go), run for memory and sqlite: empty start, env order/trim/blank/dup collapse/replace/clear/copy-on-read, env and source bounds (length in characters, control, newline, bidi, bad UTF-8, classification, negative import), no input echo in errors, caps 32/200 (cap frees after remove), add/list/remove, id ordering, round trip, isolation between cases, Info counts, cancelled context, second handle, closed handle, closed case (writes refused, reads fine), erased case.
- sqlite `TestVersionThreeCaseMigratesToFourOnce`: v3 case (made by dropping the two tables and pinning version 3) opens as 4; every other schema object byte-identical; imports, events, Info unchanged; reopen is a no-op and keeps tags.
- sqlite `TestOlderCasesMigrateStraightToFour` (v1 and v2 gain all tables).
- sqlite `TestSchemaFourCaseIsRefusedByAVersionThreeBuild`: `refusesSchema(4,3)` true; real Open path returns `ErrNewerSchema` for a case one version ahead.
- sqlite `TestTagsAreNotWrittenInPlain`: tag text absent from every file of the case folder, readable through the store.
- Existing tests unchanged and green (including the v1/v2 migration tests).

## Deviations
1. `internal/casestore/sqlite/case.go` is outside the packet's Write scope but had to change, minimally: two `atomic.Int64` count fields in `caseState`, two fields in `Info()`, and `verify` calls `refusesSchema(v, schemaVersion)` instead of `v > schemaVersion` (same behaviour, makes the rollback guard testable).
2. `casestore.Tagger` is a separate interface asserted off `Case` (like `Quarantiner`/`Discoverer`), so `Case` is unchanged.
3. Counts for `Info()` are cached in `caseState` and loaded at open (end of `migrate`), refreshed after each tag write; cost is one extra small `COUNT` query at open.
4. Tag writes take the folder lock and re-read the manifest (as `Import` does) so a `CloseCase` from another process is honoured.

## Not done / notes
- `-race` unavailable; concurrent tag writes rely on the folder lock and an atomic insert-with-count statement for the 200 cap.
- Manifest is not extended, so tags are not mirrored there and `List` has no counts.
