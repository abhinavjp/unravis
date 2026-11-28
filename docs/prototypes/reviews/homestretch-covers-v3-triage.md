# Triage: homestretch-covers-v3, round 1

| # | Sev | Decision | Note |
|---|---|---|---|
| 1 | blocker | Fixed | Waiting rows always show a reason and age. Ready + free slot + waiting shows "starts within a minute"; over 30 min adds "longer than usual: is the queue paused?" with a link to the Queue controls. |
| 2 | major | Rejected | Header counters and Queue badge are real-app behaviour, unchanged by this feature. |
| 3 | major | Fixed | A machine problem (busy, low disk, offline) now shows as a one-line strip under the Overview headline, plus in the Queue card. |
| 4 | major | Fixed | "See the log" button sits beside the failed advice; "merge again" becomes "push a fix". |
| 5 | major | Fixed (part) | Offline state: running rows say the machine stopped reporting. Cross-state time differences are separate what-if scenarios of the state switcher; rejected. |
| 6 | major | Fixed (part) | "?" help on "local" and "slot" on first screen. Fast gate / Full CI are real workflow names; rejected. |
| 7 | major | Rejected | Inert buttons are labelled "Prototype only"; the real controls exist in the real app. |
| 8 | minor | Rejected | Real run page layout, unchanged. |
| 9 | minor | Rejected | Real Overview columns, unchanged. |
| 10 | minor | Rejected | v2 tooltip copy approved in v2 round 2. |
| 11 | minor | Fixed | Waiting rows show age ("waiting 2 h 25 min"). |
| 12 | minor | Fixed | Five identical sentences replaced by short words under one column heading. |
| 13 | minor | Rejected | Real Runs filter counts. |
| 14 | minor | Rejected | Stubs are by design. |
| 15 | minor | Fixed | Overview Queue card cut to three lines. |
| 16 | minor | Fixed | Machines healthy state shows "Disk: ok" (derived from state ready). |
| 17 | minor | Rejected | Real responsive CSS. |
| 18 | minor | Rejected | Real Releases wording. |
| 19 | minor | Fixed | Bottom padding so the dashed controls never cover content. |
