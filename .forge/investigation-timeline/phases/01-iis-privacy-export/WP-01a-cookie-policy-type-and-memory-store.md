# WP-01a Cookie policy type and in-memory store
risk: medium    size: M    wave: 1    model: sonnet

## Outcome
`importer.Options` carries an optional `CookiePolicy` (allowlisted cookie names plus a 32-byte HMAC key), and `casestore` has an optional `CookiePolicyStore` interface with a name cleaner and an in-memory implementation that creates the key once from the crypto RNG (TR-19, TR-20). The sqlite implementation and the server wiring are WP-01b; the parser use is WP-02a/WP-02b.

## Write scope
- `internal/importer/importer.go` (edit: add one type and one field)
- `internal/casestore/cookiepolicy.go` (new)
- `internal/casestore/memory.go` (edit: two fields on the per-case data struct next to `retries []RetryItem`, line ~72)
- `internal/casestore/cookiepolicy_test.go` (new)

## Must not change
- The `casestore.Case` and `casestore.Store` interfaces in `internal/casestore/store.go`: no method is added to them (the server uses a type assertion, WP-01b).
- Existing `importer.Options` fields and their order; no importer reads the new field in this packet.
- The key never leaves the store: it is not in `CaseInfo`, any manifest, any log line or any error text (INV-3, no secret in files).
- INV-4: tests use in-memory stores only; never create `.unravis.db`.

## Changes
1. `internal/importer/importer.go`: add `type CookiePolicy struct { Allowed []string; HMACKey []byte }` with a doc comment: `Allowed` holds lower-cased cookie names whose values may be stored as digests; `HMACKey` is exactly 32 bytes; the zero value stores nothing. Add the field `CookiePolicy *CookiePolicy` as the last field of `Options` (after `Supplied`). Nothing else in the file changes.
2. `internal/casestore/cookiepolicy.go` (package `casestore`), constants: `MaxCookieNames = 4`, `MaxCookieNameLen = 48`, `CookieKeyBytes = 32`. Reason for 4: a W3C row already has 10 static attrs plus `client-ip` and `referer`, and `evidence.MaxEventAttrs` is 16, so 4 cookie attrs is the most that always fit. Variable `ErrBadCookieName = errors.New("casestore: cookie names may use a-z, 0-9, '_', '.', '-' and be 1-48 characters")`.
3. Same file: `func CleanCookieNames(names []string) ([]string, error)`. For each name: `strings.TrimSpace`, `strings.ToLower`, skip empty strings, skip duplicates (first wins). Every kept name must match `^[a-z0-9_.-]{1,48}$` (check by loop, no regexp needed), else return `nil, ErrBadCookieName`. If more than `MaxCookieNames` names remain return `nil, ErrCapExceeded`. Return the names sorted ascending; an empty or nil input returns an empty non-nil slice and nil error.
4. Same file: `type CookiePolicyStore interface { CookiePolicy(ctx context.Context) (importer.CookiePolicy, error); SetCookieAllowlist(ctx context.Context, names []string) ([]string, error) }` with doc comments. `CookiePolicy` returns the zero `importer.CookiePolicy` (nil `Allowed`, nil key) when the allowlist is empty or was never set; it never creates or writes anything. `SetCookieAllowlist` cleans with `CleanCookieNames`, replaces the stored list, returns the cleaned list, and creates the 32-byte key from `crypto/rand` the first time a non-empty list is stored (an empty list clears the allowlist but keeps any existing key so re-enabling gives the same digests). It refuses a closed case with `ErrCaseClosed`.
5. Same file: `func NewCookieKey() ([]byte, error)` returns 32 bytes from `crypto/rand.Read`; a short read is an error. Export it so WP-01b reuses it.
6. Same file: memory implementation on `*memCase`: `CookiePolicy` uses `c.check(ctx)` then `c.d.mu.RLock()`; copies `Allowed` and `HMACKey` into new slices so callers cannot mutate store state. `SetCookieAllowlist` calls `CleanCookieNames` first, then `c.tagWrite(ctx)` (the same gate `SetLedgerBlob` uses at `memory.go:717`), then `c.d.mu.Lock()`; refuse `c.d.erased` with `ErrNotFound` and `c.d.info.Closed != nil` with `ErrCaseClosed`; store the list; create the key when needed.
7. `internal/casestore/memory.go`: add to the per-case data struct (the one holding `retries`) `cookieAllow []string` and `cookieKey []byte`. No other edit.
8. Tests in `internal/casestore/cookiepolicy_test.go`, package `casestore_test` (like `memory_test.go`). Helper `openCookieCase(t) casestore.Case`: `st := casestore.NewMemory()`, `info, _ := st.Create(ctx, "t")`, `c, _ := st.Open(ctx, info.ID)`, then assert `c.(casestore.CookiePolicyStore)`; close the case with `t.Cleanup`. For the closed-case test call `st.CloseCase(ctx, info.ID)` (store method, `store.go:351`) after the first open handle is created:
   - `TestCleanCookieNames`: `[" SID ", "sid", "Theme"]` gives `["sid","theme"]`; `nil` gives an empty non-nil slice; `["a b"]`, `["x;y"]`, `["é"]`, `[""+strings.Repeat("a",49)]` each give `ErrBadCookieName`; five distinct valid names give `ErrCapExceeded`; four names are accepted; a 48-character name is accepted.
   - `TestCookiePolicyZeroWhenUnset`: a new case returns the zero policy and a nil error; two reads still leave no key (`CookiePolicy` after an empty `SetCookieAllowlist(nil)` still has a nil key).
   - `TestSetCookieAllowlistCreatesKeyOnce`: first non-empty set returns the list and `CookiePolicy` shows `len(HMACKey) == 32` and the sorted names; a second set with other names keeps the identical key bytes; clearing with `nil` gives an empty `Allowed` and a nil key in the returned policy, and setting again restores the same 32 bytes as before.
   - `TestCookiePolicyReturnsCopies`: mutate the returned `Allowed[0]` and `HMACKey[0]`, read again, the values are unchanged.
   - `TestSetCookieAllowlistRefusals`: closed case returns `ErrCaseClosed` and leaves the list unchanged; a bad name returns `ErrBadCookieName` and leaves the list unchanged; a cancelled context returns the context error.
   - `TestNewCookieKey`: two calls return 32 bytes each and differ.

## Proof
- `go test ./internal/casestore/ -run "CookieNames|CookiePolicy|NewCookieKey|SetCookieAllowlist" -count=1` -> `ok`, six tests run.
- `go build ./...` -> no output.
- `go vet ./internal/casestore/ ./internal/importer/` -> no output.
- `grep -n CookiePolicy internal/importer/importer.go` -> shows the struct and the `Options` field.
- `go test ./internal/casestore/... ./internal/importer/... -count=1` -> `ok` (existing tests unchanged).

## Depends on
none
