# BBX-6-03 Owner-started allocation sampling mode — BLOCKED
Backlog: BL-126 | Group: 6 | Model: performance | Reason: EventPipe GCAllocationTick + cost; cannot validate without a real target and overhead measurement.
Depends on: BBX-3-04 (EventPipe proof) | Blocks: none
Status: **BLOCKED — do not dispatch.** Depends on the BL-110 EventPipe live proof (BBX-3-04) AND a measured overhead figure (handoff §5; prioritised "Not yet confirmed").

## Blocked reason
BL-126 logs `GCAllocationTick` type names with `GCHeapStats` for one selected target, owner-started and time-boxed. It needs the EventPipe pipe-access proof (BBX-3-04) and a measured in-target cost before it can be built safely. Allocation type names are text-like but explicitly allowed by this item (they show churn, not retained data). Until EventPipe is proven and costed, this stays blocked.

## Live proof a human must run first
1. Complete BBX-3-04's proof (SYSTEM can open the diagnostic pipe; overhead measured).
2. Measure the specific cost of `GCAllocationTick` + `GCHeapStats` on a real target for a few minutes; confirm it stays inside BL-122 and does not pause the target.
3. Record the figures and the owner's go for this mode.

## Goal (once unblocked)
An owner-started, time-boxed mode for ONE selected target that logs allocation type names (GCAllocationTick) with GCHeapStats, shows its cost before it starts, is off by default, and never takes a dump.

## Context (for the eventual implementer)
- Reuse the EventPipe client from BBX-3-04. A new owner action (CLI/UI) starts the mode for a fixed duration; a cost estimate is shown first. Output: type names + allocation counts/sizes (churn), time-boxed, into a separate file. No dumps.
- Rules: **owner-started, off by default, time-boxed**; **shows cost before start**; **never pauses/dumps**; **one selected target**; **footprint budget**; type names allowed for this item only.

## Do (once unblocked)
1. A time-boxed EventPipe session requesting GCAllocationTick + GCHeapStats for one target; auto-stop at the deadline.
2. A cost preview shown to the owner before start; explicit owner confirmation.
3. Tests with a fake pipe: time-box auto-stop, off-by-default, single target, no dump.

## Do not
- Do not dispatch before BBX-3-04 proof + cost measurement + owner go.
- Do not enable by default; do not run without a time box; do not dump or pause.
- Do not capture retained heap content (allocation churn only).

## Verification checklist (once unblocked)
- [ ] go build/vet/test pass; both build-tag variants compile
- [ ] Footprint: time-boxed; cost shown; budget gate passes
- [ ] Off by default; owner-started; single target; no dump
- [ ] Live proof + cost recorded; owner go recorded

## Live-check needed?
YES — gated on the EventPipe proof and a measured cost. State plainly in the PR that BL-126 is NOT built until then.

## Report back
When unblocked: files changed, checklist, recorded cost figures, owner go. Full report to `.forge/blackbox-expansion/reports/BBX-6-03.md`.