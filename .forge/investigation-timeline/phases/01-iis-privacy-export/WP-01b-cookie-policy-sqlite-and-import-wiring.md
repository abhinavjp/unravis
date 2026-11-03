# WP-01b Cookie policy in the case file and on every import
risk: high    size: M    wave: 2    model: sonnet

## Outcome
The per-case cookie allowlist (`meta` key `cookie.allowlist`) and the one 32-byte HMAC key (`meta` key `cookie.hmac_key_v1`) persist in the sqlite case file, and every import (preview, commit, async, discover) hands the policy to the importer as `Options.CookiePolicy`. The key never appears in a manifest, log, error or export (TR-19, TR-20).

## Write scope
- `internal/casestore/sqlite/cookiepolicy.go` (new)
- `internal/casestore/sqlite/cookiepolicy_test.go` (new)
- `internal/server/cases.go` (edit `importFileWith` only, at the `opts := importer.Options{...}` literal, line ~1614)
- `internal/server/cookiepolicy_import_test.go` (new)

## Must not change
- `schemaVersion` stays 4 and `sqliteSchema` is untouched: both keys use the existing `meta(k TEXT PRIMARY KEY, v TEXT NOT NULL)` table.
- `casestore.ImportMeta`, the manifest and `CaseInfo` carry no cookie field (the key must not be in a manifest).
- A preview import writes nothing: reading the policy never creates a key.
- `bumpCaseRevision` is not called here. WP-65b adds that one line inside `SetCookieAllowlist`, in the `changed` branch of step 3 (see Note 2).
- Existing sqlite and server tests stay green; no route is added or changed.
- `internal/casestore/sqlite/helpers_test.go` defines `newTestStore`, `mustCreate`, `mustOpen`, `tMeta` (line 68) and is read-only here: use the helpers, do not edit the file. If a helper is missing, define it in `cookiepolicy_test.go`.
- `internal/server/cases.go` is also edited by WP-07a (`toEventDTO`, `caseEvents`, a later wave); this packet touches `importFileWith` only.

## Changes
1. `internal/casestore/sqlite/cookiepolicy.go`, package `sqlite`: constants `cookieAllowKey = "cookie.allowlist"` and `cookieKeyKey = "cookie.hmac_key_v1"`. Compile-time check `var _ casestore.CookiePolicyStore = (*Case)(nil)`.
2. `func (c *Case) CookiePolicy(ctx context.Context) (importer.CookiePolicy, error)`: `ctx.Err()`, then `db, err := c.handle()` (a closed or erased case gives the usual error), read both keys with `SELECT v FROM meta WHERE k=?` (`sql.ErrNoRows` means unset). Parse `cookie.allowlist` as a JSON array of strings, run it through `casestore.CleanCookieNames` (a stored list that no longer cleans is an error wrapping the sqlite package's `ErrInvalid`, not a panic). Decode `cookie.hmac_key_v1` as hex; it must decode to exactly `casestore.CookieKeyBytes` bytes, else an error wrapping the sqlite package's `ErrInvalid` whose text never contains the value. Return the zero policy when the allowlist is missing or empty. Never write.
3. `func (c *Case) SetCookieAllowlist(ctx context.Context, names []string) ([]string, error)`: clean first with `casestore.CleanCookieNames` (return its error before touching the database); then `db, release, err := c.tagGate(ctx)` (it refuses a Closed case with `ErrCaseClosed`) and `defer release()`; open one transaction with `db.BeginTx(ctx, nil)`. In that transaction: read the current `cookie.allowlist` value and whether a `cookie.hmac_key_v1` row exists; encode the cleaned list as the JSON array `want` (an empty list is `[]`; a missing row counts as `[]`). Declare `changed := want != current || (len(names) > 0 && !keyExists)`. Only when `changed`: upsert `cookie.allowlist` with `want` (`INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`) and, if the list is non-empty and no key row exists, create the key with `casestore.NewCookieKey()` and insert its 64-character lowercase hex (never replace an existing key). Put the comment `// WP-65b: bumpCaseRevision(ctx, tx) here, only when changed` as the last line of that branch. A call that changes nothing (same list again, key already present, or clearing a list that is already empty) writes nothing and so will not bump. Commit. Map database errors with `c.mapDBErr`. Return the cleaned list. One call to this function is therefore at most one revision bump (both meta rows are written in the same transaction); the rule is stated identically in WP-65b.
4. `internal/server/cases.go`, in `importFileWith` immediately after the `opts := importer.Options{...}` statement: `if cp, ok := innerCase(c).(casestore.CookiePolicyStore); ok { pol, perr := cp.CookiePolicy(ctx); if perr != nil { writeCaseFailure(w, http.StatusInternalServerError, "import-failed", "The import could not start."); return }; if len(pol.Allowed) > 0 && len(pol.HMACKey) == casestore.CookieKeyBytes { opts.CookiePolicy = &pol } }`. Use exactly the failure code and text already used for the `randomNonce` error just above. Do not add anything to `meta := casestore.ImportMeta{...}`. This is the single injection site: the async import and discover paths already go through `importFileWith`.
5. Note 1: the spec lists `internal/casestore/sqlite/case.go`; a new file keeps the diff apart from WP-65b, which edits `case.go`. The spec's "inject on each import" is the server edit in step 4.
6. Note 2 (contract): the allowlist has no HTTP route or UI in this plan; it can be set only through `SetCookieAllowlist` (tests, later work). WP-65b step 5 and its test `TestRevisionCookieKeysBump` use this setter, `SetCookieAllowlist` in `internal/casestore/sqlite/cookiepolicy.go`: it writes both keys in one transaction, so one changing call is exactly one revision bump and a repeat call with the same list is no bump.
7. Tests in `internal/casestore/sqlite/cookiepolicy_test.go` (helpers `newTestStore`, `mustCreate`, `mustOpen`, `tMeta` from `helpers_test.go`):
   - `TestCookiePolicyRoundTrip`: set `["Theme","sid"]`; returns `["sid","theme"]`; `CookiePolicy` gives the same sorted names and a 32-byte key; the `meta` rows hold a JSON array and 64 hex characters.
   - `TestCookiePolicySurvivesReopen`: close and reopen the case with `s.Open`; same names and identical key bytes.
   - `TestCookieKeyCreatedOnceAndKept`: second set with other names keeps the key; clearing with `nil` stores `[]`, `CookiePolicy` is the zero policy, the key row still exists; setting again restores the identical key; calling `SetCookieAllowlist` a second time with the same list leaves both `meta` rows unchanged (the no-op rule WP-65b relies on).
   - `TestCookiePolicyUnsetWritesNothing`: read the policy of a new case; `SELECT COUNT(*) FROM meta WHERE k LIKE 'cookie.%'` is 0.
   - `TestCookiePolicyRefusals`: closed case -> `ErrCaseClosed`; bad name -> `ErrBadCookieName`; five names -> `ErrCapExceeded`; each leaves the stored rows unchanged.
   - `TestCookiePolicyCorruptRows`: write `cookie.hmac_key_v1` = `zz` and, separately, a 10-byte hex key directly in `meta`; `CookiePolicy` returns an error wrapping the sqlite package's `ErrInvalid` and the error text does not contain the stored value.
   - `TestCookieKeyNotInManifest`: with a key set, read the case's manifest/info (`c.Info()` and the manifest file the sqlite package syncs, see `syncManifest`) and assert the 64-character key hex appears in none of them.
8. Tests in `internal/server/cookiepolicy_import_test.go` (helpers `newCaseEnv(t, envConfig{importers: ...})`, `e.newCase(title)`, `e.upload(commitPath(id), ackFields, &upload{...})`, `toyCSV`, `toyRow`; read `cases_test.go` for the exact usage). Define a probe importer in the test file that wraps `toyImporter`, records the `importer.Options.CookiePolicy` it receives on each `Parse`, and otherwise behaves like the toy:
   - `TestImportGetsNoPolicyByDefault`: commit import on a case with no allowlist; the probe saw `CookiePolicy == nil`.
   - `TestImportGetsPolicyWhenAllowlistSet`: open the case with `e.store.Open`, call `SetCookieAllowlist(["sid"])` through the `CookiePolicyStore` assertion (the memory store from WP-01a is enough), import; the probe saw `Allowed == ["sid"]` and a 32-byte key.
   - `TestPreviewImportGetsPolicyButWritesNoKey`: no allowlist, preview upload (`?preview=1` as in the existing preview tests); the probe saw nil, and `CookiePolicy` afterwards is still the zero policy.
   - `TestImportResponseNeverContainsKey`: with an allowlist set, the response body of the commit import and of `GET` case info contains neither the key as hex nor as base64.

## Proof
- `go test ./internal/casestore/sqlite/ -run "CookiePolicy|CookieKey" -count=1` -> `ok`, seven tests run.
- `go test ./internal/server/ -run "ImportGetsNoPolicyByDefault|ImportGetsPolicyWhenAllowlistSet|PreviewImportGetsPolicyButWritesNoKey|ImportResponseNeverContainsKey" -count=1` -> `ok`.
- `go vet ./internal/casestore/... ./internal/server/` -> no output.
- `go test ./internal/casestore/... ./internal/server/... -count=1` -> `ok`.
- `grep -rn "cookie.hmac_key_v1" internal/ --include=*.go` -> hits only in `sqlite/cookiepolicy.go` and test files.

## Depends on
WP-01a
