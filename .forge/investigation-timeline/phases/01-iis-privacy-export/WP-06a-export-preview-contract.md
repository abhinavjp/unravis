# WP-06a Case export: scrub setting and stateless previewId
risk: high    size: M    wave: 2    model: sonnet

## Outcome
The case export preview and confirm accept `scrubSensitive` (default ON when absent), carry a stateless advisory `previewId` derived from the case revision, and answer 409 when that id no longer matches (TR-21). Preview stores nothing; confirm recomputes the id and builds the files. The scrub setting only enters the id and the response in this packet; WP-07b makes it change the file.

## Write scope
- `internal/server/export.go`
- `internal/server/case_views.go`
- `internal/server/case_export_preview_test.go` (new)
- `internal/server/int_test.go` (edit the two case-export tests named in step 8)

## Must not change
- The findings export (no `caseId`): random id, `exportStore`, 15-minute TTL, 404 `exportGoneMessage`, `exportPreviewCapEvictsOldest` and the other `TestExport*` tests in `export_test.go` stay green and unedited.
- `evidence.BuildCase`, `evidence.CaseInput` and everything in `internal/evidence` (WP-07b owns those).
- INV-3: nothing new is logged or returned about IPs, cookies or query text. The 409 message is fixed text and never echoes input.
- `h.gates.acquire` plus `openCaseForWrite` around the build (erase safety), and `h.exports().dropCase` on erase (harmless, keep).

## Changes
1. `export.go` constants: `exportProjectionVersion = "p1"`; `exportChangedMessage = "the preview changed; run the preview again"`; `exportPreviewDomain = "unravis-preview-v1"`.
2. `export.go` types: add `ScrubSensitive *bool` with tag `json:"scrubSensitive,omitempty"` to `exportPreviewRequest` and to `exportConfirmRequest`; add `CaseID string` with tag `json:"caseId,omitempty"` to `exportConfirmRequest`. To `exportPreviewResponse` add `ScrubSensitive *bool` (`json:"scrubSensitive,omitempty"`) and `SummarizesBytes *bool` (`json:"summarizesBytes,omitempty"`); both are set (to the resolved value and to false) only by the case preview, so the findings preview JSON is unchanged.
3. `export.go`: `func casePreviewID(caseID string, revision int64, scrub bool) string` returns the lowercase hex SHA-256 of these parts joined by a single `0x00` byte: `exportPreviewDomain`, `caseID`, the revision as base-10 text, `"true"` or `"false"`, `exportProjectionVersion`. `func resolveScrub(p *bool) bool` returns `p == nil || *p`.
4. `case_views.go`: `func caseRevisionOf(ctx context.Context, c casestore.Case) (int64, error)` first unwraps with `c = innerCase(c)` (defined in `internal/server/case_lifecycle.go:160`; `*sharedCase` embeds `Case` and does not promote `Revision`, so without the unwrap a wrapped handle would report 0; add a one-line comment saying so), then asserts it implements `interface{ Revision(context.Context) (int64, error) }` (the sqlite case does, WP-65a) and returns it; a case without the method (the memory store used in tests) returns 0, nil.
5. `case_views.go`: move the build out of `caseExportPreview` into `func (h *handler) buildCaseExport(w http.ResponseWriter, r *http.Request, c casestore.Case) (caseExportResult, bool)` where `caseExportResult` holds `export evidence.Export`, `canonical []byte`, `csv []byte`. It does exactly what the preview does today (read `c.Imports`, `c.Events` with `casestore.MaxEventQuery`, fresh key zeroed after `BuildCase`, `canonicalExport`, `evidence.EventsCSV`) and writes the same errors itself (`writeStoreError` for reads, `writeExportBuildError` for builds), returning false after writing.
6. `case_views.go`: `caseExportPreview(w, r, raw string, scrub *bool)` (the caller `exportPreview` in `export.go` passes `body.ScrubSensitive`): acquire the gate and `openCaseForWrite` as now; read the revision; call `buildCaseExport` (the preview still returns the whole `Export` for display); do NOT call `h.exports().put`; answer `exportPreviewResponse` with `PreviewID: casePreviewID(string(id), rev, scrub)`, `Export`, `ScrubSensitive` and `SummarizesBytes=false`.
7. `case_views.go`: `func (h *handler) caseExportConfirm(w, r, body exportConfirmRequest)`: validate `body.CaseID` with `caseIDOf` (bad id -> the same 404 as the preview); acquire the gate; `openCaseForWrite`; read the revision; if `body.PreviewID != casePreviewID(id, rev, resolveScrub(body.ScrubSensitive))` (including an empty id) answer `writeError(w, http.StatusConflict, exportChangedMessage)` and stop; else `buildCaseExport` and write the files. Extract the shared tail of `exportConfirm` into `h.writeConfirmed(w, r, canonical []byte, sha string, csv []byte)` (filename stamp `unravis-evidence-<UTC 20060102-150405>.json`, optional `unravis-events-<stamp>.csv`, `export_finished` event) and call it from both paths; the case path computes `sha` over `canonical`. In `exportConfirm`, when `body.CaseID != ""` call `caseExportConfirm` before the `take` lookup. A confirm with no `caseId` is the findings path, unchanged.
8. Note: the spec calls the preview "stateless" with no 404; the code today stores a one-shot. A case preview confirmed without `caseId` now meets the findings store, misses, and answers 404 `exportGoneMessage`; WP-06b makes the web client send `caseId`. Update the two existing tests in `int_test.go`: `TestIntegrationImportTimelineExportErase` (confirm bodies add `"caseId":<id>`; the second confirm of the same unchanged case is now 200; confirm after erase stays 404) and `TestCaseExportClumsyUse` (the `confirm` closure takes the case id and sends it; repeat confirm of an unchanged case is 200; the empty/`x`/5000-byte ids with a real `caseId` are 409; confirm after erase is 404). `scn1_review2_test.go` uses the findings path and needs no edit.
9. New tests in `case_export_preview_test.go` (use `newIntEnv(t, intRepo())`, `e.newCase`, `e.commit(id, "iis-httperr", "web-a", intHTTPERR)`):
   - `TestCasePreviewIDDeterministicAndSensitive`: same inputs give the same 64-hex id; changing the case id, the revision (by 1) or the scrub flag changes it.
   - `TestCaseExportScrubDefaultsOn`: preview with no field answers `scrubSensitive` true and `summarizesBytes` false; `{"scrubSensitive":false}` answers false; a findings preview body has neither key.
   - `TestCaseExportPreviewIsStateless`: two previews of an unchanged case return the same `previewId`; confirm with that id and `caseId` returns 200 twice (replay).
   - `TestCaseExportConfirmWrongOrMissingIDIs409`: empty `previewId`, a random 64-hex id, and a valid id sent with the other scrub value each give 409.
   - `TestCaseExportConfirmDoesNotPromiseIdenticalBytes`: two confirms of the same preview both return 200 and each `sha256` equals the SHA-256 of its own `content` (do not compare the two to each other).
   - Note: no test here asserts a 409 after an import. The revision bump at the import site arrives with WP-65b, so in this packet the real sqlite revision is still 0 after an import; the stale-after-import 409 is proved by WP-65 (`TestCaseConfirmAfterImportBeforeStartIs409`). The 409 tests above rely only on a wrong, empty or other-scrub id.

## Proof
- `go test ./internal/server/ -run 'TestCasePreviewID|TestCaseExport' -count=1` -> `ok` (new tests plus `TestCaseExportRefusals` and `TestCaseExportClumsyUse`).
- `go test ./internal/server/ -run 'TestIntegrationImportTimelineExportErase|TestExport|TestScn1Review2' -count=1` -> `ok`.
- `go test ./internal/server/... ./internal/evidence/... -count=1` -> `ok`.
- `go vet ./internal/server/` -> no output.

## Depends on
WP-65a
