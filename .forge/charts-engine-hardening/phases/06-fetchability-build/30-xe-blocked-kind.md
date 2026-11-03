# TSK-30 xe-blocked-kind

risk: medium — touches the importer next to the scrub; the scrub must stay exactly as it is.

## Outcome

The XE importer keeps `blocked_process_report` rows as kind `blocked-process`, **count only**: time, host and the event name, no report body, no duration. A file that carries the report XML column is still quarantined by the unchanged scrub, so the export must drop that column. This gives a blocked-process count; it does NOT make D1 testable (D1 needs the XML, an owner decision listed in `phase.md`).

## Write scope

- `internal/importer/sqlserver/xe.go`
- `internal/importer/sqlserver/xe_test.go`
- `internal/evidence/caseexport_enums.go` (line ~64 and ~270–272 only)

## Must not change

- `xeScrubFragments` and `unscrubbed()`: not one fragment added or removed ("report" and "xml" stay).
- Any `KindCall` code from TSK-29.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `xe.go` kinds const block: add `KindBlocked = "blocked-process"`.
2. `xeKinds`: add `"blocked_process_report": KindBlocked,`.
3. Event switch: add `case KindBlocked: e.Severity = "warning"` (no other attrs; `e.Attrs` keeps only `event`). Do NOT read any `duration` cell for this kind (its `duration` is the blocked time in µs and is not needed for a count).
4. `caseexport_enums.go` ~64: append `"blocked-process"` to the `sql-xe` set. ~270–272 `xeEvents`: append `"blocked_process_report"`.
5. `xe_test.go`: add `TestXEBlockedCountOnly`:
   - CSV `timestamp_utc,name,server_instance_name` with two `blocked_process_report` rows → two events, kind `blocked-process`, `Attrs` exactly `{"event": "blocked_process_report"}`, `DurationMs == nil`;
   - CSV that adds a `blocked_process_report_xml` (or `xml_report`) column → `QuarantineError` with `ReasonUnscrubbedColumn`.
6. gofmt the three files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/importer/sqlserver/... ./internal/evidence/...
```

PASS. `git diff` of `xe.go` shows no line inside `xeScrubFragments` changed.

## Depends on

29 (same files).
