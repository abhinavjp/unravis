# TSK-31 xe-attention-kind

risk: low — one more allowlisted event, same shape as TSK-30.

## Outcome

The XE importer keeps `attention` events (client cancel or command timeout) as kind `attention`, count only. D16 (Phase 07) counts them.

## Write scope

- `internal/importer/sqlserver/xe.go`
- `internal/importer/sqlserver/xe_test.go`
- `internal/evidence/caseexport_enums.go` (line ~64 and ~270–272 only)

## Must not change

- `xeScrubFragments`, `unscrubbed()`, the `KindCall` and `KindBlocked` code.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `xe.go` kinds const block: add `KindAttention = "attention"`.
2. `xeKinds`: add `"attention": KindAttention,`.
3. Event switch: add `case KindAttention: e.Severity = "warning"`. No duration read.
4. `caseexport_enums.go` ~64: append `"attention"` to the `sql-xe` set; `xeEvents`: append `"attention"`.
5. `xe_test.go`: add `TestXEAttentionKind`: CSV `timestamp_utc,name,server_instance_name` with three `attention` rows → three events of kind `attention`, attrs `{"event":"attention"}`.
6. gofmt the three files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/importer/sqlserver/... ./internal/evidence/...
```

PASS.

## Depends on

30 (same files).
