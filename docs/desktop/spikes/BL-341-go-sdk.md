# BL-341 Spike: official Go MCP SDK parity, both wire lifecycles

Date 2026-11-23. Branch `claude/mcp-sdk`. Spec: `docs/desktop/spec/03-mcp.md` (BL-341, D19). Next item: BL-342 (second pull request, on top of this one).

## Verdict: GO, on the official SDK, with the bounds kept in front of it

- SDK: `github.com/modelcontextprotocol/go-sdk` **v1.8.0** (newest release; `v1.9.0-pre.1` exists and is not used). It serves protocol 2026-07-28 (per-request lifecycle, SEP-2575) and falls back to the `initialize` handshake for 2024-11-05 to 2025-11-25, on stdio, with no HTTP code needed.
- It cannot be used bare. Out of the box it misses several bounds of today's hand-rolled loop (list below). All of them are held by a thin loop in front of the SDK (`sdk_proto_test.go` here, `sdkserver.go` in BL-342), and each has a test. The spec allows exactly this: "or the failing bound can be enforced in a wrapper before the SDK".
- Footprint is inside the limit: **+1.6 to +2.4 MiB** against the limit of +4 MiB (table below).
- Fallback if a later SDK bump breaks a bound: keep the hand-rolled loop (it is the reference in the tests of BL-342, `legacy_serve_test.go`), or mark3labs v1.6.0 per D19. mark3labs was **not** tested: D19 asks for it only if the SDK fails, and it does not.
- Today's shipped stdio server has **no 2026-07-28 lifecycle at all**: a client that skips `initialize` gets `-32000 Initialize the MCP session first` (test: `TestSDKBothLifecycles/hand-rolled_loop_has_no_stateless_lifecycle`, and the same on the real binary, below). So BL-342 is also what makes Claude Code on the new revision work.

## What was run

All in `internal/mcp`, run with `go test ./internal/mcp -race`. A "host" is a test client writing JSON-RPC lines and reading lines, once per loop, same script each time (`wire` in `sdk_lifecycle_test.go`).

| Spec check (BL-341) | Test | Result |
|---|---|---|
| Both lifecycles | `TestSDKBothLifecycles`: handshake (`initialize`, `notifications/initialized`) and stateless (`server/discover`, `tools/list`, `tools/call`, each with the `_meta` triple, no `initialize`); unsupported version gives -32022; missing capabilities gives -32602; version negotiation equals the hand-rolled loop for six versions | pass |
| Catalog names, order, schemas, descriptions, annotations equal the current ones, both lifecycles | `TestSDKCatalogParity` (tool by tool, `reflect.DeepEqual` against `tools()`, also against the hand-rolled loop's wire), `TestCatalogOrder`, `TestCatalogDigest` (sha256 golden `testdata/catalog.sha256`, `-update` to change) | pass |
| Ten tools on the real data path | `TestServeParity`: every tool once, through the real dashboard HTTP handler (`server.New`, real session proof, real audit engine on the demo repository), hand-rolled loop vs SDK handshake vs SDK stateless; answers equal byte for byte after masking the result handle and the query timing | pass |
| Real process, real pipes | `TestSDKStdioProcess`: the loop as a separate process, both lifecycles, ten tools, clean exit on EOF. With `UNRAVIS_MCP_BINARY=<built audit-explorer>` it runs `audit-explorer --mcp` itself | pass (see "Real binary") |
| `--mcp-allow-identifiers` | `TestSDKIdentifierSwitchStillDecides` (in process), `TestSDKStdioProcessIdentifierSwitch` (the flag on the real binary) | pass |
| Oversize frame (> 1 MiB) | `TestSDKOversizeFrameRejected`: same error text as today, no echo, no hang; a frame just under the cap, and one at the cap followed by another in the same write, are answered | pass |
| Oversize answer | `TestSDKOversizeAnswerReplaced`: same tool error text as today | pass |
| Cancel during a blocked call | `TestSDKCancelDuringBlockedHandler`: same answer text, dashboard request abandoned, session still usable; handshake and stateless | pass |
| Malformed input | `TestSDKMalformedArgs`: 27 cases (not JSON, truncated, NUL, `{}`, `[]`, batch, 5000-deep array, wrong version, no method, fractional/null/object/bool id, unknown and unoffered methods, bad `tools/call` params, bad argument types, unknown tool). Both loops give the same answer class for every case and keep running after each | pass |
| Duplicate JSON keys | `TestSDKDuplicateJSONKeys`: last key wins in both loops, and the SDK dispatches the line the checks looked at (no parser differential in the cases tried) | pass |
| Capabilities only tools | `TestSDKCapabilitiesOnlyTools`: `{"tools":{}}` exactly on `initialize` and `server/discover` (no logging, no `listChanged`, no resources or prompts); eleven other methods refused with -32601; no unsolicited message after the handshake | pass |
| Cache scope private | `TestSDKCacheScopePrivate`: `ttlMs: 0`, `cacheScope: "private"` on `server/discover` and `tools/list`; the word "public" never on the wire | pass |
| 4 in flight, no duplicate ids | `TestSDKInFlightLimit4`: fifth call -32000, repeated id -32600, refused calls never reach the dashboard, a cancel frees a slot | pass |
| Errors scrubbed | `TestSDKErrorsScrubbed`: a secret planted in the line, tool name, method, arguments, protocol version, `_meta` and cursor never comes back; dashboard-down text has no port or URL | pass |
| EOF exits clean | `TestSDKEOFExitsClean`: idle, with a call running (dashboard request cancelled, loop stops in under 3 s), parent context cancelled, stdout closed | pass |
| Stdout only JSON-RPC | `TestSDKStdoutOnlyJSONRPC` (300 mutated inputs) and `FuzzServeSDK` (same invariant as `FuzzBridgeServe`: one JSON-RPC object per line, each under 1 MiB). `go test -fuzz FuzzServeSDK -fuzztime 60s` ran clean | pass |
| Known differences pinned | `TestSDKKnownWireDifferences` | pass |

The existing suite (`FuzzBridgeServe` seeds, `secfix_test.go`, identifier filter and output audit tests, the `bridge_test.go` tests) passes unchanged in this pull request, because nothing shipped changes in it.

Mutation check: breaking each of the order fix, the cache options, the explicit capabilities, the in-flight cap, the method allowlist and the unknown-tool answer in the prototype makes the matching test fail.

### Real binary (`audit-explorer --mcp`, Linux amd64, built from `origin/develop` and from BL-342)

| Binary | Handshake, ten tools | Stateless, ten tools | `--mcp-allow-identifiers` |
|---|---|---|---|
| today's (`origin/develop`) | pass | **fails**: `-32000 Initialize the MCP session first` | pass |
| BL-342 (SDK loop) | pass | pass | pass |

The test skips the stateless row for the old binary when `UNRAVIS_MCP_BINARY_NO_STATELESS` is set; that is how the first row was produced.

## Where the SDK needs the loop in front of it

Each was seen in a probe or test run against v1.8.0 directly, and is held by the loop (and tested):

1. **A bad line ends the session.** A non-JSON line makes the SDK's reader fail and the session close; today it answers `-32700` and carries on. Same for batches, wrong `jsonrpc`, null or fractional ids (today `-32600`). The loop reads lines itself (1 MiB scanner, as today), checks them with the same rules, answers bad ones, and passes only good lines on.
2. **Frame cap.** The SDK's `MaxLineLength` counts bytes the decoder reads ahead, not the line. The loop enforces the 1 MiB line cap on the line; the SDK's own cap is set to twice that and only backs it up.
3. **Duplicate request id in flight** is dropped silently by the SDK. The loop answers `-32600 Request id is already in flight`.
4. **No in-flight cap** in the SDK. The loop keeps 4 `tools/call` requests in flight and answers `-32000` for the fifth. A slot is freed before the answer is written (the hand-rolled loop freed it just after, so a host that sent its next call on receiving an answer could meet its own old slot).
5. **Extra keys in `tools/call` params** are ignored by the SDK; today they give `-32602`. The loop runs the same `strictArgs` check, and tool arguments still go through `strictArgs` in `Bridge.call` untouched.
6. **Unknown tool**: the SDK answers `-32602 unknown tool "<name>"`, echoing the name. Today it is a tool error with a fixed text. A receiving middleware keeps the fixed text.
7. **Methods outside the tools capability** are not all refused with the same answer (an empty `resources/list` result was seen), and error texts echo the method name. The loop passes only `initialize`, `ping`, `server/discover`, `tools/list`, `tools/call` and the two notifications; anything else gets `-32601 Method not found` (notifications are dropped).
8. **Catalog order.** The SDK lists tools sorted by name; the catalog has a fixed order of its own (`audit_get_activity`, `audit_get_guide`, `audit_list_sources`, `audit_compare`, ...). A receiving middleware re-sorts the `tools/list` result to the catalog order.
9. **Host closes the pipe with a call running.** `Run` returned while the dashboard request was still open (the dashboard saw an active connection for minutes). The loop ties every call to its own context, cancels it on exit and waits for the calls, as the hand-rolled loop did. A `notifications/cancelled` still ends one call.
10. **No output cap.** The loop's writer replaces any message over 1 MiB with today's tool error, so no line over the cap leaves the process.
11. **Version echo.** For an unsupported 2026-07-28 version the SDK puts the client's string back in the error data. The loop refuses a version string longer than 32 bytes first; a date-sized string can still come back to the client that sent it.
12. **Defaults to switch off.** The SDK's default capabilities include `logging`; tools bring `listChanged`; cache scope defaults to `public`. The server sets `Capabilities{Tools: {}}` and a `SetCacheable` that writes `ttlMs 0`, `cacheScope private`.

Parser note: the loop validates a line with `encoding/json`, then hands the same bytes to the SDK, which decodes with its own JSON package. For duplicate keys both take the last one (tested). A BL-342 reviewer should keep this in mind: any future difference between the two decoders is a place where the loop could check one reading and the SDK act on another. The effect is bounded, because the tools are read-only, the allowlist of dashboard routes is in `Bridge.request`, and arguments are re-checked by `strictArgs` after the SDK.

## Known differences (pinned by `TestSDKKnownWireDifferences`)

| Where | Hand-rolled loop | SDK loop | Why it is acceptable |
|---|---|---|---|
| Successful `tools/call` | `"isError": false` | `isError` left out | optional in the schema, default false |
| Stateless results | n/a | `"resultType": "complete"`, `_meta` carries `serverInfo` | required by 2026-07-28; absent after a handshake |
| `tools/list`, `server/discover` | no cache fields | `ttlMs: 0`, `cacheScope: "private"` | additive; this is the private zero-TTL metadata BL-342 asks for |
| Request before `initialize` | `-32000 Initialize the MCP session first` | error with code `0`, "invalid during session initialization" | both are JSON-RPC errors for a host bug; message differs |
| Second `initialize` | `-32600 Already initialized` | error with code `0` | same |
| `tools/list` with a cursor | cursor ignored | `-32602 invalid params` | the catalog has one page and no cursor is ever handed out |
| `ping` | answered | answered after handshake, `-32601` on the stateless lifecycle | `ping` is gone from 2026-07-28 |
| Key order in JSON | sorted | struct order | not meaningful |

## Footprint (Linux amd64 build in this sandbox; Windows build cross-compiled, not run)

| Build | Today (`origin/develop`) | With SDK loop | Delta |
|---|---|---|---|
| `go build ./cmd/audit-explorer` (Linux, not stripped) | 54,945,579 B | 57,488,506 B | +2,542,927 B (+2.43 MiB) |
| same, `-ldflags "-s -w"` | 38,060,192 B | 39,776,416 B | +1,716,224 B (+1.64 MiB) |
| `GOOS=windows`, `-ldflags "-s -w"` | 39,363,584 B | 41,116,160 B | +1,752,576 B (+1.67 MiB) |

Start time and memory, `audit-explorer --mcp`, time from process launch to the first `initialize` reply, 100 runs each after a warm-up, in this shared sandbox VM (noisy; medians and p95 swing by tens of milliseconds between identical runs, so only the fastest runs and memory say anything):

| Binary | fastest run | median | peak RSS median |
|---|---|---|---|
| today, not stripped | 16.7 ms | 49.1 ms | 26.5 MiB |
| SDK loop, not stripped | 20.6 ms | 38.4 ms | 29.7 MiB |
| today, stripped | 15.7 ms | 39.8 ms | 24.6 MiB |
| SDK loop, stripped | 18.9 ms | 52.6 ms | 28.9 MiB |

Read it as: about 3 to 4 ms more at the fastest, about 3 to 4 MiB more resident, medians not separable from noise. To be re-measured on nachi's PC (Windows), which is where it matters.

Dependencies added to `go.mod`: `github.com/modelcontextprotocol/go-sdk` v1.8.0 (direct), and indirect `github.com/google/jsonschema-go` v0.4.3, `github.com/segmentio/encoding` v0.5.4, `github.com/segmentio/asm` v1.1.3, `github.com/yosida95/uritemplate/v3` v3.0.2, `golang.org/x/oauth2` v0.35.0, `golang.org/x/time` v0.15.0 (all six are linked into the binary; `golang-jwt` and `pkg/browser` are in `go.sum` but not linked). The SDK's licence is Apache-2.0 for new work and MIT for older contributions; `NOTICE` already covers Apache-2.0 and MIT third-party code. The SDK needs Go 1.25; the repo is on 1.27.4.

## Not done, and why

- **Official conformance harness** (`@modelcontextprotocol/conformance`, npm 0.1.16 / 0.2.0-alpha.12): not run. Installing it was refused by this session's permission layer, and its server scenarios drive an HTTP URL, not a stdio process. The wire fixtures in the tests above (both lifecycles, written against the 2025-11-25 and SEP-2575 texts) stand in for it. The SDK's own `conformance/baseline.yml` lists only OAuth client scenarios as unsupported. Run the harness against a Streamable HTTP wrapper only if BL-355 wants it.
- **`govulncheck`**: not run (running an extra fetched tool was refused here). Spec says "go if govulncheck clean": this is an open condition. Run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` on `develop` after merge, or in CI; nothing was seen in a dependency review of the six modules above, but that is not a scan.
- **Two real hosts, both lifecycles, real backend on a real PC** (BL-341 live acceptance, BL-342 real proof): not done, needs nachi's PC and host accounts. What is proved is the real dashboard handler over real HTTP and the real session proof, with the demo repository behind it, and the real `audit-explorer --mcp` process over real pipes.
- **Windows**: the code builds for `GOOS=windows`; the tests ran on Linux only. `TestMCPSmokeUnderMitigations` (spec, Windows, ACG policy) is not written.
- **mark3labs v1.6.0**: not tested (not needed, see verdict).

## How to re-run, and when to re-run

- `go test ./internal/mcp -race` (about 15 s). Real binary: `go build -o /tmp/ae ./cmd/audit-explorer && UNRAVIS_MCP_BINARY=/tmp/ae go test ./internal/mcp -run TestSDKStdioProcess -v`.
- Pin the SDK version exactly and re-run the whole package on every bump. The SDK changes defaults between releases (this one already treats `logging` and `public` cache scope as defaults), and 1.9.0 is in pre-release. A bump that moves a "Known differences" row fails `TestSDKKnownWireDifferences`; one that moves a default fails the capability or cache test.
