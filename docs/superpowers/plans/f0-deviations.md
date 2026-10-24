# F0 deviations from plan §1

Every signature in §1.2 to §1.5 is kept exactly as written. Nothing below changes or removes a plan signature. Items are additions, clarified semantics, or places where the plan was silent.

## Additions (new exported names)

- `importertest.Fixtures` has a third field, `Formula []byte`: a fixture with cells starting `=`, `+`, `-`, `@`, tab and CR, used by the "kept verbatim" check. `Run(t, imp, Fixtures{Good, Bad})` still compiles; the formula check is skipped when `Formula` is empty.
- `importertest.Sentinel` (`"SENTINEL-7f3a"`) and `importertest.FuzzParse(f, imp, fx)`.
- `importer`: `Prepare`, `Finish`, `NewLimits`, `Limits.WithDefaults/CheckRows/CheckRejects`, `Report.Rule/Reject/Cover/Gap/Observe`, `Registry.Names`, `AllReasons`, `Reason.Text/Valid`, constants `DefaultMax*`, `HeadSize`, `MaxReportErrors`, `MaxObservedHosts`, `TieWindow`. `Reason` also has `ReasonRejectFraction` (listed in the plan's enum, no constant name given).
- `evidence`: `Event.Clone`, `ErrEventInvalid`, `MaxEventAttrs` and related bounds.
- `clock`: `BiasLocation(minutes)` for the header-bias rule, `ErrBadTime`.
- `textio`: `ErrEmpty`, `DefaultMaxLine`, `Encoding` constants.
- `casestore`: `NewMemoryWith(caps, now)`, `Caps` and `DefaultCaps`, `ParseID`, `NewID`, `ValidTitle`, `MaxTitleBytes`, `Retention`, `MaxEventQuery`, `Status*` constants, `DuplicateError`, and the extra errors `ErrBadID`, `ErrBadTitle`, `ErrHandleClosed`, `ErrDuplicateEvent`.
- `casestore/storetest`: the contract test (`storetest.Run(t, factory)`). The CS card must run the same contract against SQLite. This is a new package under the F0-owned `internal/casestore/` tree.

## Clarified semantics

- **Detect ties:** `Registry.Detect` returns `tied` non-nil only on a tie within 10 points and lists every tied name including the best, sorted. `best` is nil when nothing scores above zero.
- **`Limits` zero values:** zero `MaxLineBytes`, `MaxFields` and `MaxRejectFraction` mean the defaults; for no tolerance of rejects use a tiny positive fraction. `MaxRows <= 0` or `MaxBytes <= 0` means no limit (the importer's `DefaultLimits` should always set both).
- **Archive refusal:** `Sniff` reports `QuarantineError{ReasonArchive}` whose text says to extract first. Nothing is decompressed.
- **`Collect`:** validates each event against `AllowedAttrs`, copies it, applies `Finish` (row limit, reject fraction) and returns no events on any error, which is the in-memory rollback. The `Report` is still returned.
- **textio line limit:** the limit counts the line without its terminator (LF or CRLF); exactly `maxLine` bytes is allowed. A breach returns `*LimitError{Limit:"line-bytes", At: <line number>}`. `CSVReader` may report a breach on the read after the last whole record, because `encoding/csv` reads ahead.
- **CSV:** `LazyQuotes` off. Quote errors, wrong field counts and too many fields become `RowError` values with fixed reasons, never the cell text. An empty file is `ErrEmpty`.
- **Event IDs:** `EventID` uses a per-import nonce, so the hash is not needed before parsing. `CasePrefixes` returns exactly the two prefixes in the plan.
- **casestore, `Import`:**
  - The store wraps `body` in a hashing, counting reader; the SHA-256 and byte count cover the whole file even when the importer stops reading early.
  - One import runs at a time per case (serialised).
  - The per-file cap is `Caps.MaxFileBytes` and `Caps.MaxFileRows` (rows counts both emitted events and `Report.Rows`).
  - The case cap is checked as the remaining room in the case. Every refusal leaves the case untouched.
  - A failed, quarantined or capped import leaves no record and no events. The returned `ImportResult.Record` carries Status `quarantined` and the Reason for quarantines. For other failures its Status is empty.
  - A duplicate returns `*DuplicateError` (wraps `ErrDuplicate`), with `Record` set to the existing import and Status `duplicate`.
  - Duplicate evidence IDs, within one file or against the case, give `ErrDuplicateEvent`.
- **casestore, other methods:**
  - `Events` orders by Start then EvidenceID. The window is inclusive, a Limit of 0 or over 50 000 clamps to 50 000, and results are copies.
  - `Pseudonymize` returns `kind-` plus 8 hex characters of HMAC-SHA256 under a per-case key; an empty value maps to "".
  - `CloseCase` keeps the first close time. `Sweep` erases cases closed strictly more than 30 days before `now`.
  - `Erase` is idempotent. Open handles then see `ErrNotFound`. A closed handle returns `ErrHandleClosed`. A cancelled context is refused before any work.
  - Case IDs failing `^[a-z2-7]{26}$` give `ErrBadID` before any lookup.
- **Case store does not call the ledger.** `ledger.StripEvidence(CasePrefixes(id), ...)` belongs to the SQLite store and the service (CS and INT cards); the memory fake only erases its own data.

## Not done, by design

- No importer implementations. The toy and the seven broken importers in `importertest` tests are test-only.
- `internal/evidence/types.go`, `record.go`, `build.go`, `internal/audit/patterns_*` and `web/` are untouched. `wording_test.go` reads `web/src/lib/copy/wordings.ts` read-only.
