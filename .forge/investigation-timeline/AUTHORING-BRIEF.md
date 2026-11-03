# Authoring brief (shared by all packet-authoring agents)

You write plan files only. You do NOT implement, edit source, run builds, or commit. Repo root: `D:\Projects\api-audit-explorer` (use absolute paths; do not `cd`).

## Read first (in this order, nothing else unless a step below says so)
1. `D:\Projects\api-audit-explorer\.forge\investigation-timeline\plan.md` (§2 invariants INV-1..8, §3 dependency table: your packets' `Depends on` and wave must equal it exactly, §6 packet rules).
2. One example phase and one example packet for format: `D:\Projects\api-audit-explorer\.forge\charts-engine-hardening\` (Glob `phases/*/phase.md` and one task file; copy the structure and tone, not the content).
3. Your spec slices (see your assignment). Spec file: `D:\Projects\api-audit-explorer\.dev\spec-src\2026-10-30-tracing-paper-impl-spec.md` (466 KB). NEVER read it whole; read in chunks of at most 120 lines (larger Reads fail). Use Grep with `-n` for a WP id (for example `WP-17`) to find every mention, §9.2 rows are at lines 298-367, requirements §8 at 248-286, Appendix P at 4183+, cross-cutting definitions at 4206-4264.
4. Never read `sql/` and never print secrets.

## What to write
For each assigned phase: `D:\Projects\api-audit-explorer\.forge\investigation-timeline\phases\NN-slug\phase.md` and one file per packet `WP-XX-slug.md` in the same folder.

`phase.md`: Goal (2-3 lines), `risk:`, packet list with wave and dependency ids (from plan.md §3), phase-level proof commands, phase-specific additions to plan.md §7 (what to look at in the browser vs the wireframe for this phase, security canaries, perf bounds), and the commit file list (explicit paths).

Packet file (exact headings, this order, nothing else at `##` level):
```
# WP-XX <title>
risk: low|medium|high    size: S|M    wave: N    model: haiku|sonnet
## Outcome
## Write scope
## Must not change
## Changes
## Proof
## Depends on
```
- Outcome: 1-3 sentences, observable result, cites TR ids.
- Write scope: exact repo-relative file paths (new files marked `(new)`). An implementer may touch only these.
- Must not change: files/symbols/behaviours at risk (plan.md invariants that apply), 2-6 bullets.
- Changes: numbered steps. Each step names the file, the function/type/component with its exact signature, constants with exact values, and the test to add with its name and assertions in words. NO pasted implementation code (signatures and one-line type declarations are fine). The implementer will not open the spec, so copy every needed value, key name, error string, status code and field name from the spec into the packet. A cheap model must be able to implement it without exploration.
- Proof: runnable commands with the expected outcome, for example `go test ./internal/blackbox -run TestW3CStoresClientIP -count=1` -> `ok`. Every test named in Changes appears here. UI packets also name the Vitest/Playwright test. Never assert `<15 ms`/`<100 ms`.
- Depends on: ids exactly as in plan.md §3, or `none`.

## Verify against the code (mandatory)
The spec was written against an older tree. Before writing a packet, open the real files it names (Grep for symbols, Read only the relevant lines) and confirm: path exists, symbol and signature exist, line anchors. Write the verified facts into the packet. Resolve every "(unverified)" note in the spec by opening the file. If the spec and the code disagree, follow the code, state the discrepancy in one line under Changes as `Note:`; if the disagreement changes a public contract or schema, put it in your final report as an ESCALATION and still write the packet on the safest reading. If a symbol the spec expects does not exist and cannot be created within the packet's scope, say so as an ESCALATION.

## Sizing
S = one file plus its test, at most ~60 changed lines. M = at most 3 files plus tests, at most ~200 changed lines. If a spec packet is larger, split it into WP-XXa/WP-XXb files and tell me in your report (I will update plan.md); do not change any other packet's id. Keep files concise: a packet file should normally be 40-120 lines.

## Model label
`haiku` only when S-size and fully mechanical (rename, constant, copy text); otherwise `sonnet`.

## Final report (max 25 lines)
List: files written; any packet split; ESCALATIONS (contract/schema/route changes, missing symbols, spec-vs-code conflicts); packets whose Write scope overlaps another packet in the same wave (file names). No prose recap of the packets.
