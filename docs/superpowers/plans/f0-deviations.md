# F0 deviations from plan §1

Every signature in §1.2 to §1.5 is kept exactly as written. Nothing below changes or removes a plan signature. Items are additions, clarified semantics, or places where the plan was silent.

## Additions (new exported names)

- `importertest.Fixtures` has extra fields: `Formula []byte` (a fixture with cells starting `=`, `+`, `-`, `@`, tab and CR), `NoFreeText bool`, `Large []byte` and `Options`. See "Fix round" for what each does. `Run(t, imp, Fixtures{Good, Bad})` still compiles, but the formula check is no longer skipped when `Formula` is empty (see the fix round).
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
- **`Collect`:** validates each event with `Event.ValidateFor(opts.CaseID, Name(), AllowedAttrs())`, copies it, applies `Finish` (row limit, reject fraction) and returns no events on any error, which is the in-memory rollback. The `Report` is still returned. An error returned by `emit` is sticky: it fails the run even when `Parse` swallows it and returns nil, and `emit` keeps refusing afterwards.
- **textio line limit:** the limit counts the line without its terminator (LF or CRLF); exactly `maxLine` bytes is allowed. A breach returns `*LimitError{Limit:"line-bytes", At: <line number>}`. `CSVReader` may report a breach on the read after the last whole record, because `encoding/csv` reads ahead.
- **CSV:** `LazyQuotes` off. Quote errors, wrong field counts and too many fields become `RowError` values with fixed reasons, never the cell text. An empty file (or one with no header) is `ErrEmpty` itself, so `errors.Is(err, textio.ErrEmpty)` works. A duplicate header name that is in `RequireHeader` is a `QuarantineError` naming the column. A quoted multi-line record is bounded by `CSVOptions.MaxRecord` (see the fix round).
- **Event IDs:** `EventID` uses a per-import nonce, so the hash is not needed before parsing. `CasePrefixes` returns exactly the two prefixes in the plan.
- **casestore, `Import`:**
  - The store wraps `body` in a hashing, counting reader; the SHA-256 and byte count cover the whole file even when the importer stops reading early.
  - One import runs at a time per case (serialised).
  - The per-file cap is `Caps.MaxFileBytes` and `Caps.MaxFileRows` (rows counts both emitted events and `Report.Rows`).
  - The case cap is checked as the remaining room in the case. Every refusal leaves the case untouched.
  - A failed or capped import leaves no record and no events. A quarantine leaves a `QuarantineRecord` in a separate table (plan §1.5), never an import record: see the fix round. The returned `ImportResult.Record` carries Status `quarantined` and the Reason for quarantines (ID 0). For other failures its Status is empty.
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

- No importer implementations. The toy and the sixteen broken importers in `importertest` tests are test-only.
- `internal/evidence/types.go`, `record.go`, `build.go`, `internal/audit/patterns_*` and `web/` are untouched. `wording_test.go` reads `web/src/lib/copy/wordings.ts` read-only.

## Fix round (Opus review of F0)

Every plan signature is still as written. Exported additions are listed first, then behaviour changes that a parked importer or the CS store can notice, then each finding.

### Exported API changes

No exported name was removed or had its signature changed. Additions:

- `evidence.CheckEventID(id, caseID, importer) error` and `Event.ValidateFor(caseID, importer, allowedAttrs) error` (Validate plus Source equals the importer and the ID has the `ev:<case8>:<importer>:<16 hex>` shape for that case).
- `textio.CSVOptions.MaxRecord int` (bytes one logical record may span; 0 means 16 times `MaxLine`). A breach is `*LimitError{Limit:"record-bytes"}`.
- `casestore.ImportMeta.AllowedAttrs map[string]bool`, `casestore.QuarantineRecord`, and the `casestore.Quarantiner` interface (`Quarantines(ctx)`), implemented by the memory store's `Case`.
- `importertest.Fixtures.NoFreeText bool`, `Fixtures.Large []byte`.
- A new unexported field on `importer.Report` (an index for `Observe`). A `Report` built by hand or copied still works.

Stricter behaviour that existing code may trip over (the breaks to check when the six parked importers and the CS store are rebased):

1. `Event.Validate` now requires `Line >= 1` and a `TZRule` equal to one of the four `clock.Rule*` values.
2. `importer.Collect` and the memory store reject an event whose ID is not `EventID`-shaped for the import's case and importer, and one whose `Source` is not the importer name. Importers must build IDs with `evidence.EventID(opts.CaseID, Name(), opts.ImportNonce, line, ordinal)`. The store checks against `meta.Importer`, or the event's own `Source` when that is empty.
3. A refused `emit` fails the import (Collect and the store) even when `Parse` ignored the error and returned nil. The store contract now requires this of every store.
4. A quarantine no longer creates an import record: `Case.Imports()` does not list it, `Info().Imports` and `Bytes` do not count it, the same bytes can be imported later, and `ImportResult.Record.ID` is 0. The CS store needs a quarantine table (plan §1.5) and `Quarantines`.
5. `clock.ParseLocal` returns `ErrBadTime` for a layout that has only a zone abbreviation (`MST`) when the parsed zone is not UTC or GMT; before it returned the wrong instant with no flag.
6. `join.Overlap` demotes to `JoinAmbiguous` a unique match whose only candidate is also claimed by another interval, so `CountUnique` no longer double-counts one event.
7. `casestore.ValidTitle` rejects control, format (bidi, zero-width) and line or paragraph separator characters.
8. `textio.NewCSV` returns `ErrEmpty` itself for an empty file or no header, and quarantines a file where a required column name appears twice.
9. `importertest.Run` is stricter (new subtests below). An importer must now: map hosts through `Options.Host` or `Options.Hosts` and report raw names with `Report.Observe`; return read errors and limit breaches from `Parse`; stop and return when `emit` fails; check its context itself; and either carry a free-text attribute (a whole cell of `Good`) or set `NoFreeText`.

### High

- **H1 CSV record size.** `CSVOptions.MaxRecord`. The reader samples the pending bytes after every `Read`, so a stray quote that opens a multi-line field fails with `record-bytes` after `MaxRecord` bytes instead of growing to `MaxBytes`. Test: `TestCSVRecordLimit`.
- **H2 ignored emit error.** `Collect` and the memory store keep the first emit error and fail the run with it; emit keeps refusing afterwards. `storetest` has `ignored-emit-error` (run swallows the refusal; the import must fail, with no events or record) and `importertest` has `emit-errors-surface` (Parse called directly with an emit that fails on call 1, 2 and 3 must return that error and make no further emit call). Broken importer: `ignores-emit-error`.
- **H3 vacuous limit-bytes, read errors turned into EOF.** `limit-bytes` also runs on a file over twice `HeadSize` (`Fixtures.Large`, or Good's header plus its rows repeated), with `MaxBytes` between the head and the end, so the breach happens inside `Parse`, which must return the `LimitError` itself. New `read-errors-surface`: a reader that fails at 20 bytes, `HeadSize+100`, `HeadSize+101` and half way, with the error on its own call and together with the last bytes; the import must fail with `errors.Is` on that error and return no events. Broken importer: `swallows-read-errors`. `limitReader` also gives up with `io.ErrNoProgress` after 100 empty reads (L3).
- **H4 ID shape, Line, TZRule.** `Event.Validate` and `ValidateFor` as above; `Collect`, the memory store and `FuzzParse` use `ValidateFor`. New `event-ids` subtest: shape via `CheckEventID`, `Line >= 1`, `TZRule` set, `Source == Name()`, `Report.Emitted == len(events)`, and IDs that change with `ImportNonce` and with `CaseID`. Broken importers: `bad-event-id`, `line-zero`, `no-tz-rule`.

### Medium

- **M1 raw host names.** The toy importer now maps the host through `Options.Host` / `Options.Hosts`, calls `Report.Observe`, and quarantines an unknown host. New `hosts-are-labels`: `Event.Host` must be empty, `Options.Host.Label` or a label from `Options.Hosts` for an observed name; and with a mapping that knows no host, no raw observed name (3 characters or more) may appear in any error, gap, rule, row error or event. Broken importer: `raw-host`. `Fixtures.Options` should carry a `Hosts` mapping for the names in `Good`.
- **M2 formula cells.** `formula-cells-verbatim` cannot be skipped. With `Fixtures.Formula`, each of `=`, `+`, `-`, `@`, tab and CR must start some attribute value byte for byte (no trimming) and none may be `'`-escaped. Unless `Fixtures.NoFreeText` is set, the harness also takes up to 40 attribute values of `Good` that appear as a whole cell of its bytes (bounded by a separator, quote, line end or file edge, so `2001` is not found inside a timestamp), letter-containing values first, inserts each trigger in front of the cell and parses again. For each trigger the subtest fails only if some parse succeeded, no row was rejected in any of them, and no attribute arrived as trigger+value: a refused file, a rejected row, or a tab or CR read as a separator is a legitimate answer to a changed cell, but silently dropping or trimming the character is not. If `Good` has no such cell the subtest fails unless `NoFreeText` is set. `NoFreeText` (strictly typed attributes: numeric codes, fixed words, derived labels) skips the generated cells entirely; a non-nil `Formula` fixture is still checked. Broken importers: `escapes-formula-cells`, `trims-formula-cells`. Tests: `TestNumericAttrsWithNoFreeText`, `TestRejectingTriggersDoesNotFalseFail`.
- **M3 ParseLocal.** See behaviour change 5. `clock.offsetInLayout` replaces `zoneInLayout`; tests added.
- **M4 concurrency contract.** `storetest`: `same-bytes-race` (fresh `Open` per goroutine, goroutines importing the same bytes: exactly one ok, the rest `ErrDuplicate`), `erase-during-import` (Erase while an import is blocked in `run`: the import fails with `ErrNotFound` and leaves nothing), `reads-during-import` (Events, List and Imports on a second handle mid-import show nothing from the pending import), and `imports-serialised` now opens a handle per goroutine. The review's "run under -race in CI" is not done here: this toolchain has CGO off, so there is no race detector. The CS card should run the contract with `-race` where it can.
- **M5 quarantine.** Following plan §1.5: a separate quarantine table. `QuarantineRecord`, `Quarantiner`, and the contract's `quarantine` test assert: no import record, no events, bytes not counted against the case, the same bytes importable afterwards, the record holds reason, file name, SHA-256, column and row count only, and Erase removes it.
- **M6 join.** See behaviour change 6. Test: `TestOverlapOneEventTwoIntervals`.

### Low

- **L1** `ErrEmpty` returned (behaviour change 8; test `TestErrEmptyReturned`). **L2** duplicate required header quarantined (`TestCSVDuplicateRequiredHeader`). **L3** `io.ErrNoProgress` after 100 empty reads (`TestLimitReaderNoProgress`). **L5** `Report.Observe` uses a map index. **L7** `ValidTitle`. **L10** the store checks `ImportMeta.AllowedAttrs` and the event rules; contract `event-provenance`.
- **L4** added the missing Windows zone IDs except Mid-Atlantic Standard Time, with a table test pinning NZ and about ten common IDs.
- **L8** `context-cancel-in-parse`: `Parse` called directly with an emit that ignores the context, on endless good rows and endless bad rows; it must return within 1 s of cancellation. The review asks for a stream where every row is rejected; a generic harness cannot make one, so it repeats the Bad fixture. Broken importer: `ctx-only-via-emit` (an importer that never looks at its context and relies on `Collect`'s emit to fail).
- **L9** all four: `no-network` (failing `http.DefaultTransport`, `DefaultClient.Transport` and `net.DefaultResolver` dial; must see zero calls; broken importer `calls-network`), `line-endings-identical` (LF and CRLF copies, attribute values compared after normalising CRLF), `Emitted` against events received (in `event-ids`), and `concurrent-parse` (eight goroutines, same result as one). The last runs without the race detector (CGO off), so it catches shared-state corruption and map panics, not data races.
- `TestHarnessCatchesBrokenImporters` now also names the subtest that must fail for each of the sixteen broken importers (`wantFail`), so a fault cannot be "caught" by an unrelated early failure.

### FuzzParse false positives (importer authors P4, P6, P9)

- `Report.ObservedHosts` holds raw host names by contract, so the sentinel check (`leaks`, used by `Run`'s sentinel subtest and by `FuzzParse`) no longer looks at it. The report is printed field by field, without `ObservedHosts` or its unexported index. The rule that labels, not raw names, reach events stays in `hosts-are-labels`.
- New `importertest.Fixtures.KeepsCellValues bool` (additive; `FuzzParse(f, imp, fixtures)` calls compile unchanged). An importer that keeps whole cell values in event attributes by design (counter paths, `LogName`, `ProviderName`, `AppPool`, a free-text `name`) sets `KeepsCellValues: true` in its fixtures. `FuzzParse` then skips the sentinel check on event attribute keys and values only; it still fails on the sentinel in an error chain, `KnownGaps`, `FieldRules`, report text, or any event field other than `Attrs` (host label, kind, unit, ID). The default is false: the strict check. It is separate from `NoFreeText` (which says no attribute holds file text) and does not change `Run`'s sentinel subtest, whose Bad fixture must put the sentinel in a cell that is not kept.
- Test: `FuzzToyKeepsCells` (seed corpus, run by `go test`) has the sentinel in a kept note cell and in an observed raw host cell; it passes with `KeepsCellValues` and fails without it.

### Declined

- **L6** (registry ignores a duplicate name): turning it into a panic or an error changes `Register` for the six parked importers; left for the coordinator.
- **L11** (`wording.num` against JS `String(n)`): needs the TS side to agree on exponent thresholds and NaN handling; outside the F0 files' remit. Inputs are finite, bounded numbers in practice.
- **Mid-Atlantic Standard Time** is not mapped: it has no clean IANA equivalent.
