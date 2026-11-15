# Slow call full-page screen: decisions (owner, 2026-11-13)

Input for `/skill-engineer:forge-plan detailed`. Wireframe: `prototype/slow-call-popup-WIREFRAME-v3.html`. Backlog items: BL-005, BL-006 to BL-019 in `docs/BACKLOG.md`.

## Settled
- Surface: full-page screen under the nav bar, not a pop-up (BL-019). Owner said "go ahead" after wireframe v3; layout not named, so W1 Workbench (the recommended one) is the assumption. Say so in the plan; switching layout later must stay cheap.
- Lane visibility rule: API, web server (IIS) and database lanes always fully visible together with no inner scroll at 1440x800 and 1100x760; timeline pinned while the page scrolls; window control then timeline first, call facts below.
- Overview strip (minimap): keep, redesigned for beginners (research file `drill/research/minimap-overview-research.md`).
- Nearby calls and Nearby activity: owner has not answered. Assume separate, build so merging them later is cheap.
- Scope: use only what the app already reads. No new collector or agent (BL-100 to BL-103 stay deferred).
- Finish on live data: every packet has a real-data path, not demo-only (project `CLAUDE.md`). The field-name probe (`GET /api/sources/{id}/fields`) is owed for BL-006; plan it as a packet that finds the names through the app's own read-only code first.
- Beginner copy for every message and tooltip; update `web/tests/drill.copy.test.cjs` counts.
- Local install only (rc.35). No tag, release, push or merge.

## Owner process rules for this plan
- Packets: extremely small, very detailed, each with a verification checklist the implementing subagent must tick before reporting done.
- Make every packet that can be made now. Dependency graph showing what runs in parallel.
- Implementation models: Haiku or Sonnet, chosen per packet by complexity, impact and risk; the packet records the choice.
- Review: batch-wise (one review per batch, findings returned to the individual implementers), not per task. Reviewer Sonnet, Opus or Fable chosen by impact, severity, risk and effort.
- Planning and orchestration by a Fable-level model. Follow each model's current prompt guidance (context engineering); hub notes in `~/.claude/hub-model-prompting.md`, allocation in `~/.claude/model-allocation.md`.
- Cap review loops at 5 rounds; watch weekly usage.
