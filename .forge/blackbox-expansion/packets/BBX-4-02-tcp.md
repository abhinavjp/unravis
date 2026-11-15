# BBX-4-02 TCP retransmit/reset/outbound queue + state counts
Backlog: BL-112 | Group: 4 | Model: performance | Reason: PDH rates + GetTcpTable2 state counting with a strict no-address rule.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none

## Goal
Every 15 s sample TCP retransmit and reset rates, outbound queue length, and TCP state counts — counting states only, never storing remote/local addresses.

## Context
- New file `internal/blackbox/tcp.go` (no tag for the PDH part) + `internal/blackbox/tcptable_windows.go` / `tcptable_other.go` for GetTcpTable2 + test `internal/blackbox/tcp_test.go` (fakes). Group pattern as BBX-2-01 (init RegisterGroup("tcp", true, ...); optional).
- Researched PDH paths (UNVERIFIED): `\TCPv4\Segments Retransmitted/sec`, `\TCPv4\Connections Reset`, `\TCPv6\Segments Retransmitted/sec`, `\TCPv6\Connections Reset`; outbound queue from `\Network Interface(*)\Output Queue Length` (sum over up interfaces). State counts: iphlpapi `GetTcpTable2` (lazy DLL) — count rows by `dwState` (MIB_TCP_STATE numeric), discard all address fields immediately.
- `TCPGroup{RetransmitsPerSec, ResetsPerSec, OutboundQueue, States []TCPStateCount}`; TCPStateCount{State uint8, Count uint32} (groups_types.go).
- Rules: **no remote addresses** (count states only); **machine-wide**; **two baselines before a rate**; **missing never 0**; **no text**.

## Do
1. PDH part: engine specs RetransmitsPerSec/ResetsPerSec = KindPDHRate (sum v4+v6), OutboundQueue = KindGauge (sum of Output Queue Length).
2. `tcpTableCounts() ([]TCPStateCount, error)` (windows): GetTcpTable2, iterate rows, increment a count per dwState, NEVER copy addresses into any struct. other twin returns error. Inject as a func for tests.
3. `Collect`: engine.Process for rates + tcpTableCounts for states → *TCPGroup. If both fail → nil. A failed state query leaves States nil (rates may still be present).
4. Cap state rows scanned (e.g. 65536) for footprint.

## Do not
- Do NOT store IP addresses, ports, or PIDs from the TCP table — counts by state only.
- Do not emit 0 for a missing counter; no text; no goroutine/ticker.
- Do not cap rates; two baselines before a rate.

## Tests to write first (fakes)
- `TestTCPStateCountsNoAddresses` — fake table rows with addresses → only State+Count survive (assert no address field exists on TCPStateCount).
- `TestTCPRatesGated` — retransmit rate absent until two baselines.
- `TestTCPMissingOmitted` — invalid counter → nil field; state query error → States nil but rates kept.
- `TestTCPSumV4V6` — v4+v6 retransmits summed.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: capped table scan; reused PDH handle; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestTCPMissingOmitted)
- [ ] No text/addresses captured (TestTCPStateCountsNoAddresses + code review)
- [ ] Two baselines before a rate
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q TCPv4` / `TCPv6` and `netstat -an` state distribution on a real server; confirm counter names and that state counts match netstat totals. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-4-02.md`.