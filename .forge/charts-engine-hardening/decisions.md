# Decisions: charts-engine-hardening

Answered by the owner on 2026-11-02 via the Decision Desk.

| # | Decision | Answer | vs. plan |
|---|---|---|---|
| 1 | Lane clock margin | 5000 ms (`LaneClockToleranceMs`) | as planned |
| 2 | Onset persistence share | 0.5 (`OriginPersistShare`) | as planned |
| 3 | Lane with no data | Counts as unread; blocks "earliest" | as planned |
| 4 | Spread-check skip | Mean events per minute, skip below 1 | as planned |
| 5 | Zero-event incident window (wireframe :1204) | Grey it out | as planned |
| 6 | Catalogue items S8–S12, D16, D17, M11, M12 | **Add all nine now** | CHANGES PLAN: plan assumed drop; needs new packets (about 12) |
| 7 | Audit coverage policy (B8) | Defer | as planned |
| 8 | Fetchability build order | **Add as Phase 6 of this plan** | CHANGES PLAN: plan assumed a later plan; needs a new phase |

## Plan extended

The plan was extended on 2026-11-02 with Phase 06 fetchability-build (TSK-29..42, 14 packets, decision 8: SQL data → HTTPERR → Blackbox sampler) and Phase 07 catalogue-nine (TSK-43..60, 18 packets, decision 6; more than the ~12 estimated, because the data plumbing, the orange cap, the grey words and the golden regen are separate packets). Graph: P02 → P06 → P07; P05 stays the checkpoint for P01–P04. Owner questions raised by the extension are listed under "Out of scope / needs owner decision" in each phase's `phase.md`.
