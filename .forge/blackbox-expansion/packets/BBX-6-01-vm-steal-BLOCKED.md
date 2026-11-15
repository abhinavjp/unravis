# BBX-6-01 Hyper-V / VMware CPU steal + memory balloon — BLOCKED
Backlog: BL-113 | Group: 6 | Model: performance | Reason: VM-only counters with unverified paths; cannot validate without a real VM.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none
Status: **BLOCKED — do not dispatch.** Needs a live `typeperf -q` on a real VM first (handoff §5; prioritised "Not yet confirmed").

## Blocked reason
BL-113 is "Could, VMs only". The Hyper-V/VMware counter object names and paths are unverified, and the counters exist only on specific hypervisors/guest configurations. Building blind risks a group that never reads anything. Confirm the paths on a real VM first.

## Live proof a human must run first (on a real Hyper-V and/or VMware VM)
1. `typeperf -q | findstr /i "Hyper-V VMWare"` — record the exact object names (e.g. `\Hyper-V Hypervisor Virtual Processor(*)\% Guest Run Time`, steal-related counters; VMware tools perf counters).
2. Confirm a steal/balloon counter exists and is readable by SYSTEM; sample it twice and record a value.
3. Record which hypervisor and guest OS the figures came from.

## Goal (once unblocked)
Sample CPU steal and memory ballooning every 15 s on VMs, machine-wide, into `VMGroup{StealPct, BalloonBytes}` (groups_types.go). Off/omitted when the counters do not exist (bare metal).

## Context (for the eventual implementer)
- Group pattern as BBX-2-01 (init RegisterGroup("vm", true, ...); optional). PDH source + engine; missing counters → nil group (never 0). Paths come from the live proof above.
- Rules: **machine-wide**; **missing never 0** (bare metal → group omitted); **two baselines before a rate**; **no text**; **light**.

## Do (once unblocked)
1. Encode the verified paths; factory builds PDH source + engine; Collect maps to VMGroup.
2. If the counters are absent (PdhAddEnglishCounter fails) → group skipped (nil), not an error.
3. Tests with a fake source: mapping, missing→nil, rate gating.

## Do not
- Do not dispatch before the live proof is recorded.
- Do not emit 0 on bare metal (omit the group).
- Do not hardcode unverified paths as if confirmed.

## Verification checklist (once unblocked)
- [ ] go build/vet/test pass; both build-tag variants compile
- [ ] Footprint: reused handle; no goroutine/ticker; budget gate passes
- [ ] Missing never 0; no text; rate gating
- [ ] Live proof recorded (exact paths + a real value)

## Live-check needed?
YES — the whole packet is gated on the live VM proof. State plainly in the PR that BL-113 is NOT built until then.

## Report back
When unblocked: files changed, checklist, the recorded paths/figures. Full report to `.forge/blackbox-expansion/reports/BBX-6-01.md`.