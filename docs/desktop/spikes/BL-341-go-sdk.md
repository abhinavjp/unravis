# BL-341 Spike: official Go MCP SDK parity, both wire lifecycles

Date 2026-11-23, revised the same day after review 1 of PR #247 (section "Review 1" below: several bounds this document first claimed did not hold, and were fixed in BL-342). Branch `claude/mcp-sdk`. Spec: `docs/desktop/spec/03-mcp.md` (BL-341, D19). Next item: BL-342 (second pull request, on top of this one).

## Verdict: GO, on the official SDK, with the bounds kept in front of it

Note on the groom item's "handwritten parser gone": the hand-rolled loop and its dispatch are gone, but `sdkline.go` is a new, small handwritten reader in front of the SDK (one strict parse per line, canonical message out). It is what makes the bounds hold, so it is security code and is covered by `TestSDKDecoderAgreesWithTheLoop` and `FuzzReadLine`.

- SDK: `github.com/modelcontextprotocol/go-sdk` **v1.8.0** (newest release; `v1.9.0-pre.1` exists and is not used). It serves protocol 2026-07-28 (per-request lifecycle, SEP-2575) and falls back to the `initialize` handshake for 2024-11-05 to 2025-11-25, on stdio, with no HTTP code needed.
- It cannot be used bare. Out of the box it misses several bounds of today's hand-rolled loop (list below). They are held by a thin loop in front of the SDK. The loop in this pull request (`serveSDK` in `sdk_proto_test.go`) is the spike's first version and is **not** the reference: review 1 found it checked one reading of a line and gave the SDK another (see "Review 1"). The reference is `sdkserver.go` and `sdkline.go` in BL-342 (PR #247), which sends the SDK only a message it built itself; the tests named below as "(#247)" are the ones that hold the bounds there. The spec allows exactly this: "or the failing bound can be enforced in a wrapper before the SDK".
- Footprint is inside the limit: **+1.64 MiB (Linux) and +1.69 MiB (Windows)** stripped, against the limit of +4 MiB (table below; measured on the review-3 head, `-trimpath -ldflags "-s -w"`, `origin/develop` `da2abe6c` as the base).
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
| Malformed input | `TestSDKMalformedArgs`: 28 cases (not JSON, truncated, NUL, `{}`, `[]`, batch, 5000-deep array at top level, wrong version, no method, fractional/null/object/bool id, unknown and unoffered methods, bad `tools/call` params, bad argument types, unknown tool). Both loops give the same answer class for every case and keep running after each. Lines that are valid JSON but not valid for the SDK (trailing space or tab, depth over 1000 inside `params`) were **not** in this table, and the prototype closed the session on them; `TestSDKValidJSONKeepsSession` (#247) covers them | pass for the cases listed; see Review 1 |
| Duplicate JSON keys | In this pull request only exact duplicates were tried, which both decoders resolve alike. The claim "no parser differential" was wrong for keys that differ in case (Review 1, M1). In #247 `TestSDKDuplicateJSONKeys` (17 lines, case variants included) and `TestSDKDecoderAgreesWithTheLoop` / `FuzzReadLine`: a line with a duplicate or case-variant key is refused with -32600, at any depth | fixed in #247 |
| Capabilities only tools | `TestSDKCapabilitiesOnlyTools`: `{"tools":{}}` exactly on `initialize` and `server/discover` (no logging, no `listChanged`, no resources or prompts); eleven other methods refused with -32601; no unsolicited message after the handshake | pass |
| Cache scope private | `TestSDKCacheScopePrivate`: `ttlMs: 0`, `cacheScope: "private"` on `server/discover` and `tools/list`; the word "public" never on the wire | pass |
| 4 in flight, no duplicate ids | `TestSDKInFlightLimit4`: fifth call -32000, repeated id -32600, refused calls never reach the dashboard, a cancel frees a slot. The prototype could be got around with a case-variant `METHOD` key (ten calls reached the dashboard), an `"ID"` key (four lines used up every slot) and integer ids above 2^53 (slots never freed); `TestSDKInFlightCannotBeBypassed` (#247) | fixed in #247 |
| Errors scrubbed | `TestSDKErrorsScrubbed`: a secret planted in the line, tool name, method, arguments, protocol version, `_meta` and cursor never comes back; dashboard-down text has no port or URL. Not true as first written: the test sent its lines before a handshake, where the SDK refuses them before reading `params`, so it never saw the SDK quote `params` back (Review 1, M3). #247 runs it after a handshake, with none, and stateless, and every error answer now carries a fixed text | fixed in #247 |
| EOF exits clean | `TestSDKEOFExitsClean`: idle, with a call running (dashboard request cancelled, loop stops in under 3 s), parent context cancelled, stdout closed | pass |
| Stdout only JSON-RPC | `TestSDKStdoutOnlyJSONRPC` (300 mutated inputs) and `FuzzServeSDK` (one JSON-RPC object per line, each under 1 MiB). As first written it did not check that the session survives or that the bounds hold, so none of the Review 1 inputs would have failed it. In #247 every input is followed by a ping that must be answered, at most four requests may be open on the data path, and the seeds include the review's probes. `FuzzReadLine` (#247) compares the loop's reading of a line with the SDK decoder's | fixed in #247 |
| Known differences pinned | `TestSDKKnownWireDifferences` | pass |

The existing suite (`FuzzBridgeServe` seeds, `secfix_test.go`, identifier filter and output audit tests, the `bridge_test.go` tests) passes unchanged in this pull request. Shipped code changes only by `go.mod` and one extracted constant (`serverInstructions` in `protocol.go`); the SDK loop in this pull request is test-only.

Mutation check: breaking each of the order fix, the cache options, the explicit capabilities, the in-flight cap, the method allowlist and the unknown-tool answer in the prototype makes the matching test fail. That says the tests detect those breaks; it did not say the bounds held against crafted input (they did not, Review 1). For #247 each Review 1 fix was removed again and its test fails (list in the pull request).

### Real binary (`audit-explorer --mcp`, Linux amd64, built from `origin/develop` and from BL-342)

| Binary | Handshake, ten tools | Stateless, ten tools | `--mcp-allow-identifiers` |
|---|---|---|---|
| today's (`origin/develop`) | pass | **fails**: `-32000 Initialize the MCP session first` | pass |
| BL-342 (SDK loop) | pass | pass | pass |

The test skips the stateless row for the old binary when `UNRAVIS_MCP_BINARY_NO_STATELESS` is set; that is how the first row was produced.

## Where the SDK needs the loop in front of it

Each was seen in a probe or test run against v1.8.0 directly, and is held by the loop (and tested):

1. **A bad line ends the session.** A non-JSON line makes the SDK's reader fail and the session close (seen); today it answers `-32700` and carries on. The SDK's read path hands decode errors up the same way for JSON that is not a valid request (batches, wrong `jsonrpc`, null or fractional ids; today `-32600`). The loop reads lines itself (1 MiB scanner, as today), parses each once with strict rules, answers bad ones, and sends the SDK a canonical message it re-encodes from the fields that passed, never the client's bytes.
2. **Frame cap.** By reading the SDK source: its `MaxLineLength` counts every byte the decoder reads since the last frame, read-ahead included, so a frame near the cap could fail on the next frame's bytes. The loop enforces the 1 MiB line cap on the line; the SDK's own cap is set to twice that and only backs it up.
3. **Duplicate request id in flight** is dropped silently by the SDK. The loop answers `-32600 Request id is already in flight`.
4. **No in-flight cap** in the SDK. The loop keeps 4 `tools/call` requests in flight and answers `-32000` for the fifth. A slot is freed before the answer is written (the hand-rolled loop freed it just after, so a host that sent its next call on receiving an answer could meet its own old slot).
5. **Extra keys in `tools/call` params** are ignored by the SDK; today they give `-32602`. The loop runs the same `strictArgs` check, and tool arguments still go through `strictArgs` in `Bridge.call` untouched.
6. **Unknown tool**: the SDK answers `-32602 unknown tool "<name>"`, echoing the name. Today it is a tool error with a fixed text. A receiving middleware keeps the fixed text.
7. **Methods outside the tools capability** are not all refused with the same answer (an empty `resources/list` result was seen), and error texts echo the method name. The loop passes only `initialize`, `ping`, `server/discover`, `tools/list`, `tools/call` and the two notifications; anything else gets `-32601 Method not found` (notifications are dropped).
8. **Catalog order.** The SDK lists tools sorted by name; the catalog has a fixed order of its own (`audit_get_activity`, `audit_get_guide`, `audit_list_sources`, `audit_compare`, ...). A receiving middleware re-sorts the `tools/list` result to the catalog order.
9. **Host closes the pipe with a call running.** `Run` returned while the dashboard request was still open (the test dashboard was still holding the connection when it was closed). The loop ties every call to its own context, cancels it on exit and waits for the calls, as the hand-rolled loop did. A `notifications/cancelled` still ends one call.
10. **No output cap.** The loop's writer replaces any message over 1 MiB with today's tool error, so no line over the cap leaves the process.
11. **Echo in errors.** The SDK quotes client input in its error texts and data: the unsupported version string, the method name, and, after a handshake, `params` and Go type names (`mcp.initializeParamsV2`). A length limit on the version string (the first version of this loop) did not cover it. In #247 every error answer from the SDK is rewritten to a fixed text per code; only the list of supported versions is kept for -32022 (SEP-2575), and `requested` is dropped.
12. **Defaults to switch off.** The SDK's default capabilities include `logging`; tools bring `listChanged`; cache scope defaults to `public`. The server sets `Capabilities{Tools: {}}` and a `SetCacheable` that writes `ttlMs 0`, `cacheScope private`.

Parser note (corrected): `encoding/json` matches keys without regard to case and keeps the last duplicate; the SDK's decoder is case-sensitive, stops at depth 1000 and rejects bytes after the message. The first version of the loop validated a line with `encoding/json` and forwarded the same bytes, so the two read different messages (Review 1, M1 and M2); the earlier text here, "no differential found", was wrong. The effect was not bounded by "read-only tools": the in-flight cap and the method allowlist protect the live audit store and the SDK surface. In #247 the line is parsed once, duplicate or case-variant keys are refused, nesting is limited to 256, and only a canonical message goes to the SDK. A reviewer on any SDK bump should re-run `TestSDKDecoderAgreesWithTheLoop` and `FuzzReadLine`, which compare the two readings.

## Review 1 (2026-11-23, on PR #247 head `befed800`): what did not hold, and where it is fixed

| Finding | Was | Fix in #247 | Test (#247) |
|---|---|---|---|
| M1 parser differential | `"METHOD":"ping"` beside `"method":"tools/call"`: ten calls past the cap, no duplicate-id check; `Method` reached `resources/list`; method name echoed; `PARAMS` shadowed the params checks | one strict parse, duplicate and case-variant keys refused at any depth, canonical re-encode sent to the SDK | `TestSDKDuplicateJSONKeys`, `TestSDKInFlightCannotBeBypassed`, `TestSDKForwardsOnlyTheCanonicalMessage`, `TestSDKDecoderAgreesWithTheLoop` |
| M2 valid JSON closed the session | trailing space or tab, `JSONRPC` key, depth over 1000 in `params` | canonical re-encode (the first two), nesting limit 256 answered -32600 | `TestSDKValidJSONKeepsSession` |
| M3 SDK errors echoed `params` | after a handshake, ordinary bad params came back in the error text | error answers rewritten to a fixed text per code in `frameWriter.Write`; `params` that is not an object answered -32602 | `TestSDKErrorsScrubbed` (after handshake, none, stateless) |
| S1 slot leaks | `"ID"` lines and integer ids above 2^53 (the SDK answers a rounded id) kept slots forever | ids limited to +-(2^53-1); `ID` read by the same strict parse as every key; every request id, of any method, is tracked, so a ping cannot free the slot of a running call with the same id | `TestRequestIDsUseDecodedIdentity`, `TestSDKInFlightCannotBeBypassed` |
| S2 `MCPGODEBUG` | a malformed value made the SDK panic in `init`, killing every binary at startup; valid values change error codes and annotation encoding | package `internal/mcp/envguard` clears the variable before the SDK reads it (init order is by import path; the test re-runs the test binary with bad values and fails if the order ever changes) | `TestEnvGuardClearsMCPGODEBUG` |
| S3 `govulncheck` | "run after merge" | spec gate: run before merge (see "Not done") | n/a |
| S4 fuzz | 0.5 exec/s as reported, no survival check | the execs counter of `go test -fuzz` under-reports (a worker reports its count only when it returns, so the counter sits still for long stretches; timed with a log line per input, `FuzzBridgeServe` and `FuzzServeSDK` ran about 1,000 inputs a second; inputs over 64 KiB are skipped, the large cases have unit tests); the targets now check survival and the in-flight bound | `FuzzServeSDK`, `FuzzBridgeServe`, `FuzzReadLine` |
| S5 spec drift | version "1.0.0"; `audit_compare` `idempotentHint` true | `serverInfo.version` is `internal/version.Version`; `audit_compare` is `idempotentHint: false` (it replaces the cached comparison); annotations are read from `tools()` | `TestSDKServerVersionAndHints`, `TestCatalogDigest` |
| S6 shared blind spot | parity tests read every line the way the checks did | differential test and fuzz target against the SDK decoder | `TestSDKDecoderAgreesWithTheLoop`, `FuzzReadLine` |

Found while fixing, not by the review: when the host wrote its requests and closed the pipe at once (a script, `echo ... | audit-explorer --mcp`), the first SDK loop ended the session before it had answered them, where the hand-rolled loop answered each one first. #247 now answers every request it has read (calls still running are abandoned, as before) and then ends: `TestSDKAnswersWhatWasReadBeforeEOF`. The fuzz harness and most tests keep the pipe open, which is why nothing had shown it.

## Review 3 (2026-11-23, on PR #247 head `bef0ae7d`): CLEAN, one fix

S1: a slot was freed under the id as the SDK wrote it back, and the SDK escapes U+2028 and U+2029 (3 bytes become 6), so an id of about 250 bytes could come back over the 256-byte limit and never free its slot (64 such pings locked the session). A slot is now freed by the canonical id with no length limit (`canonicalID`); the 256-byte limit applies to the id as the client sent it. `TestSDKSlotsFreedForIdsTheSDKReEncodes`. S3: see the fuzz row above. Size figures made consistent (section "Footprint"). The effective line cap is 1 MiB minus one byte (the scanner buffer holds the line and its newline), as in the old loop. `audit_list_sources` reads connection state live but queries no production data, which is why it carries `Cached, no query.`.

## Review 2 (2026-11-23, on PR #247 head `ad9cc654`): what changed

Review 2 found that the text above ("the SDK is sent only a message re-encoded from the fields that passed") was still not true for `params`: the envelope was rebuilt but the client's own `params` bytes went on, so two things were wrong.

| Finding | Was | Fix in #247 | Test |
|---|---|---|---|
| M1 a valid line under 1 MiB closed the session | the canonical message was built with `json.Marshal`, which escapes `<`, `>`, `&` to six bytes; about 350 KB of them made a message past the SDK's 2 MiB frame cap, the SDK's reader failed and the process exited | the message is built with `json.Compact` and a non-escaping string encoder; a message over 1 MiB is refused with -32600; the SDK's own cap stays at twice that, as a margin | `TestSDKValidJSONKeepsSession` (350 KB of `<`, 400 KB of `&`, 900 KB of `>` in a cursor, in tool arguments and in a method), size check in `checkDifferential` |
| M2 the checks and the SDK read different `name` / `arguments` | `strictArgs` reads keys in any case (and Unicode folds such as U+017F); the SDK does not. `ARGUMENTS`, `Arguments`, `argumentſ`, `Name`, `_META` passed the checks and the SDK saw no arguments (the tool ran with defaults) or no name | for `tools/call`, params are rebuilt from the fields the checks read, under their exact names (`name`, `arguments`, `_meta`). For other methods the loop reads nothing from params except "is an object", so there is no second reading to differ | `TestSDKToolParamsKeyCaseMatchesLegacy` (same answers as the hand-rolled loop; arguments are never dropped), `checkTypedReading` (the SDK's case-sensitive decoder, `segmentio/encoding/json` with `DontMatchCaseInsensitiveStructFields`, against `strictArgs` on the client's line) in `FuzzReadLine` and `TestSDKDecoderAgreesWithTheLoop` |
| S1 D24 prefixes | missing | every description starts `Live query.` (activity, compare, requests, slowest, breakdown) or `Cached, no query.` (guide, sources, groups, timeline, compare_timelines); catalog golden updated | `TestToolDescriptionsSayLiveOrCached`, `TestCatalogDigest` |
| S2 unused code linked in | `oauth2`, `x/time/rate`, `uritemplate`, `jsonschema-go`, and the SDK's `auth` and `oauthex` packages are linked into `audit-explorer` and `unravis` because `go-sdk/mcp` imports them unconditionally; they cannot be left out without forking the SDK | accepted, and counted: +1.64 MiB Linux and +1.69 MiB Windows stripped (table in "Footprint"), limit +4 MiB. They widen what `govulncheck` has to cover, so that gate matters more | `go list -deps ./cmd/audit-explorer` |
| S4 ids rewritten | a string id with invalid UTF-8 or a lone surrogate came back as U+FFFD, and two such ids collided | refused with -32600 | `TestSDKValidJSONKeepsSession`, differential seeds |
| nit | error code `0` | mapped to -32600 | `TestSDKKnownWireDifferences` |

Other places where `encoding/json` and the SDK decoder can disagree were checked by a typed differential (`checkTypedReading`) and by seeds in `FuzzReadLine`: duplicate keys (refused by `strictShape`), case and Unicode-fold variants of keys (refused when duplicated; read by the loop and rebuilt under exact names for `tools/call`), `\u` escapes in keys and strings, a BOM (not valid JSON: -32700), number forms (`1.0E+2`, `-0`, `0.1e-400`, ids only plain integers up to 2^53-1), invalid UTF-8 and lone surrogates (ids refused; elsewhere passed through unchanged), depth (limit 256), trailing and leading blanks, and size growth by escaping. `govulncheck` is still not run (refused in this session too): it is a hard gate before merge, and its result goes into this document.

Left as the spec describes and not built in BL-342: `upstream.go` and `legacy_transport.go` (the loop is still `Bridge.Serve`; the hand-rolled loop is kept only as the test reference `legacy_serve_test.go`), and a `runMCP` that builds `mcp.NewServer(upstream, opts)`. Listed here and in `docs/BACKLOG.md` so they are not read as done.

## Known differences (pinned by `TestSDKKnownWireDifferences`)

| Where | Hand-rolled loop | SDK loop | Why it is acceptable |
|---|---|---|---|
| Successful `tools/call` | `"isError": false` | `isError` left out | optional in the schema, default false |
| Stateless results | n/a | `"resultType": "complete"`, `_meta` carries `serverInfo` | required by 2026-07-28; absent after a handshake |
| `tools/list`, `server/discover` | no cache fields | `ttlMs: 0`, `cacheScope: "private"` | additive; this is the private zero-TTL metadata BL-342 asks for |
| Request before `initialize` | `-32000 Initialize the MCP session first` | `-32600 Invalid request` (the SDK's own code `0` is mapped; its text is replaced) | both are JSON-RPC errors for a host bug; message differs |
| Second `initialize` | `-32600 Already initialized` | `-32600 Invalid request` | same code, fixed text |
| `tools/list` with a cursor | cursor ignored | `-32602 invalid params` | the catalog has one page and no cursor is ever handed out |
| `ping` | answered | answered after a handshake, `-32601` on the stateless lifecycle | a stateless client has no session to ping; hosts do not ping a stdio server they just launched |
| Key order in JSON | sorted | struct order | not meaningful |
| A key given twice, or twice with different case (any depth) | last one wins | `-32600`, nothing forwarded | two readings of one line; refused (`TestSDKDuplicateJSONKeys`) |
| `JSONRPC` / `METHOD` / `ID` alone, other case | read like the lowercase key | same; the SDK is sent the canonical form | the old loop's behaviour is kept (`TestSDKValidJSONKeepsSession`) |
| Nesting over 256 levels | up to 10000 | `-32600` | the SDK stops at 1000 and closes the session; real requests are a few levels deep |
| Integer id beyond +-(2^53-1) | exact | `-32600` | the SDK carries ids as float64 and would answer another number |
| `params` that is not an object | ignored on `tools/list`, `ping` | `-32602 Invalid params` | fixed text instead of the SDK quoting it |
| A second request with the id of a running request, of any method | answered | `-32600 Request id is already in flight` | a ping answer must not free a call's slot |
| Error text | per case | fixed per code; the SDK's code `0` becomes `-32600` | no echo of client input |
| `serverInfo.version` | `1.0.0` | build version (`internal/version`, "dev" when unstamped) | spec 03-mcp |
| `audit_compare` `idempotentHint` | true | false | spec 03-mcp BL-342 |
| `-32022` data | n/a | `supported` only | the version asked for is not sent back |

## Footprint (Linux amd64 build in this sandbox; Windows build cross-compiled, not run)

| Build (`-trimpath -ldflags "-s -w"`, Go 1.27.4, amd64) | Today (`origin/develop` `da2abe6c`) | With SDK loop (#247) | Delta |
|---|---|---|---|
| Linux | 38,109,447 B | 39,829,767 B | +1,720,320 B (**+1.64 MiB**) |
| Windows (cross-built, not run) | 39,281,152 B | 41,048,576 B | +1,767,424 B (**+1.69 MiB**) |

These include the SDK packages that are linked but never used at run time (see Review 2, S2). The first measurement of this spike (not stripped, Linux: +2.43 MiB; stripped +1.64 / +1.67 MiB on the spike prototype) is superseded by this table.

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
- **`govulncheck`**: not run (running an extra fetched tool was refused here, and again for review 1). Spec says "go if govulncheck clean": this is an **open merge gate**, to be run **before** #247 merges (on nachi's PC or in CI): `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`. Nothing was seen in a dependency review of the six modules above, but that is not a scan.
- **Two real hosts, both lifecycles, real backend on a real PC** (BL-341 live acceptance, BL-342 real proof): not done, needs nachi's PC and host accounts. What is proved is the real dashboard handler over real HTTP and the real session proof, with the demo repository behind it, and the real `audit-explorer --mcp` process over real pipes.
- **Windows**: the code builds for `GOOS=windows`; the tests ran on Linux only. `TestMCPSmokeUnderMitigations` (spec, Windows, ACG policy) is not written.
- **mark3labs v1.6.0**: not tested (not needed, see verdict).

## How to re-run, and when to re-run

- `go test ./internal/mcp -race` (about 15 s). Real binary: `go build -o /tmp/ae ./cmd/audit-explorer && UNRAVIS_MCP_BINARY=/tmp/ae go test ./internal/mcp -run TestSDKStdioProcess -v`.
- Pin the SDK version exactly and re-run the whole package on every bump. The SDK changes defaults between releases (this one already treats `logging` and `public` cache scope as defaults), and 1.9.0 is in pre-release. A bump that moves a "Known differences" row fails `TestSDKKnownWireDifferences`; one that moves a default fails the capability or cache test.
