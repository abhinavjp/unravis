# BBX-2-03 Disk latency/IOPS split + CPU queue/privileged/DPC/interrupt
Backlog: BL-108 | Group: 2 | Model: performance | Reason: per-disk wildcard instances + rate/latency unit conversion; machine-wide evidence.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none

## Goal
Every 15 s, sample disk read/write latency split, IOPS and split IO per physical disk, plus processor queue length and privileged/DPC/interrupt CPU time. Machine-wide.

## Context
- New file `internal/blackbox/diskcpu.go` (no tag) + test `internal/blackbox/diskcpu_test.go` (fakes). Same group pattern as BBX-2-01 (init RegisterGroup("diskCpu", false, ...); mandatory group).
- Researched paths (UNVERIFIED — live check): `\PhysicalDisk(*)\Avg. Disk sec/Read`, `\PhysicalDisk(*)\Avg. Disk sec/Write`, `\PhysicalDisk(*)\Disk Reads/sec`, `\PhysicalDisk(*)\Disk Writes/sec`, `\PhysicalDisk(*)\Split IO/Sec`; `\System\Processor Queue Length`; `\Processor(_Total)\% Privileged Time`, `\Processor(_Total)\% DPC Time`, `\Processor(_Total)\% Interrupt Time`.
- `DiskCPUGroup{Disks []DiskSplit, CPU *CPUSplit}`; `DiskSplit{Name, ReadMs, WriteMs, ReadsPerSec, WritesPerSec, SplitIOPerSec}`; `CPUSplit{QueueLength, PrivilegedPct, DPCPct, InterruptPct}` (groups_types.go).
- Rules: **machine-wide**; **two baselines before a rate**; **valid-status check**; **multi-core CPU not capped at 100**; **missing never 0**; latency counters are SECONDS → multiply by 1000 for the Ms fields.

## Do
1. Factory builds PDH source with the disk wildcard paths + the system/processor paths; engine specs: ReadMs/WriteMs = KindGauge then ×1000 in mapping (Avg. Disk sec/* are gauges in seconds); ReadsPerSec/WritesPerSec/SplitIOPerSec = KindPDHRate; QueueLength gauge; PrivilegedPct/DPCPct/InterruptPct = KindPDHRate (no 100 cap).
2. `Collect`: parse disk instance names from expanded paths (skip `_Total` for the per-disk list, or include it as Name "_Total" — choose per-disk only, exclude _Total). Build []DiskSplit. CPU fields → CPUSplit. Return *DiskCPUGroup; nil if nothing valid.
3. Re-expand disk wildcards only on ProcessSetChanged() (disks rarely change).

## Do not
- Do not cap CPU percentages at 100.
- Do not emit 0 for a missing counter; leave nil.
- Do not forget the seconds→ms conversion (×1000) for ReadMs/WriteMs.
- Do not re-expand every tick; no goroutine/ticker; no text.

## Tests to write first (fakes)
- `TestDiskCPULatencyMs` — fake Avg. Disk sec/Read = 0.002 → ReadMs == 2.0.
- `TestDiskCPUPerDisk` — two disk instances → two DiskSplit rows, _Total excluded.
- `TestDiskCPURateGated` — ReadsPerSec absent until two baselines.
- `TestDiskCPUMultiCoreNotCapped` — PrivilegedPct 120 stays 120.
- `TestDiskCPUMissingOmitted` — invalid counter → nil field.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; re-expand on change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestDiskCPUMissingOmitted)
- [ ] No text captured (disk names + numbers)
- [ ] Two baselines before a rate; multi-core not capped; seconds→ms correct (tests above)
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q PhysicalDisk` and `typeperf -q Processor` on a real server; compare ReadMs to perfmon "Avg. Disk sec/Read" ×1000. Confirm paths; fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-2-03.md`.