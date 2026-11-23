# Wire contracts (BL-445)

One JSON Schema and one golden answer per wire shape. Go, Rust and (later) the UI types are tested against the same files, so a renamed, dropped or extra field fails CI.

```
contracts/v1/schema/    JSON Schema (draft 2020-12), additionalProperties false
contracts/v1/fixtures/  golden bodies, captured from the real Go handlers
```

First slice: `GET /api/info`, `GET /api/version`, and the error body (401 no token, 400 validation, 423 locked). The error schema is the union of the failure shapes the handlers write (master-key `state`, throttle `retryAfterSeconds`, source-add `code`, `stage`, `collections`); only three of them have a golden so far.

## Who checks what
- Go: `internal/server/contracts_test.go` calls the real handlers, checks the live answer and the golden against the schema, and compares live with golden. `TestWireContractsCatchDrift` proves the schema refuses a renamed, extra, wrongly typed or dropped field.
- Rust: `desktop/ipc-core/tests/contracts.rs` reads the same golden files into strict serde structs (`deny_unknown_fields`). Runs in the desktop jobs (promotion only, not in the fast gate) and by hand with `cargo test -p ipc-core`, so a regenerated golden is checked on the Rust side at promotion. It does not use a JSON Schema crate: the strict structs catch the same drift without a new dependency in the shipped tree.
- UI types (Svelte): not wired yet. The web tests are `node --test`; a test that parses the same files is the next slice.

## Change a contract on purpose
1. Change the Go handler and the schema.
2. `UPDATE_CONTRACTS=1 go test ./internal/server -run TestWireContracts` rewrites the goldens.
3. Review the diff of `contracts/v1/fixtures/`, then update the Rust structs if the shape changed.

## Rules
- Fixtures hold the demo dataset and fixed test values only. No real host names, paths, tokens or case data. Review every fixture diff for this.
- Large integers go as decimal strings in new shapes.
- Not covered by a schema: redaction, authorisation, locking order, timeouts. Those need adapter tests, not shape checks.

## Not done yet
- More shapes (a bounded read, `204`, request id, the pipe `hello`, `captureCases: true`, goldens for the 429 and source-add errors).
- A change under `contracts/` runs `internal/server` only; when the Svelte test exists, `fast-scope.mjs` must also set web for it.
- Recording a real response from a live PC, scrubbed (BL-445 live-PC step, owner reviews the scrub).
- OpenAPI and `oasdiff` (out of scope of BL-445).
