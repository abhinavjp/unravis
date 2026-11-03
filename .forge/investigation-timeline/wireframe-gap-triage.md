# Wireframe gap triage: tracing screen (phases 01-05)

Key: A = planned in a phase not yet built; B = out of scope or superseded; C = promised in 01-05, missing.
Sources read: plan.md, phases/04-tracing-ui/phase.md and packets, phases/03/05/06, prototype, built web files.

| Element | Class | Evidence |
|---|---|---|
| Range chips | B | No chip or preset in any packet (grep "chip" over phases/ finds none). The plan gives typed UTC fields: WP-22-window-picker.md:5, phase.md:6 ("window picker"). No TR in plan.md:23-48 asks for presets. |
| Sources chips | B | Superseded by the three row toggles (WP-10-trace-toolbar.md:28, TR-23 at plan.md:44). Which sources are read is server-side (WP-14, TR-03, plan.md:25). |
| "Trace again" | B | Same function under other names: "Start tracing" (WP-22:5, :33) and "Try again" (WP-16b-tracing-shell-entry.md:28). TRACE-01 and TRACE-12 pass. Only the label differs. |
| Comparison rows | B | phase.md:44 (accepted deviation), WP-17b-tracing-chart.md:22, phases/06-mvp1-gate/phase.md:28, WP-58-main-suite-gate.md:33. The reference window is sent (WP-16b:25) but not drawn. |
| 4xx / 5xx panels | B | phase.md:44: "only the 5xx share is drawn, as darker bars"; WP-17b:22, :39 (`bar-err`). Same lines in phase 06 and WP-58:33. |
| HTTP.sys panel | B, with a caveat | phase.md:44 and WP-17b:22 accept the missing panel. But plan.md:9 says "IIS lane (with HTTPERR markers)", WP-19-httperr-markers.md:5 puts the markers in the response, and WP-17b:19 and WP-17c:16 forbid drawing `lane.events`. No packet in 01-05 draws them, so the API carries data nobody renders. Owner should confirm "accepted". |
| Suspects, "What Unravis sees" | B | No TR for them in plan.md:23-48. plan.md:49 leaves TR-14, 17, 24-29, 33, 34 out, and plan.md:12 puts MVP-2/3 out of scope. Suspect cards stay in guided only (WP-60-guided-reuse-table.md:36). Hand-off to analysis is WP-61. |
| P95 right axis | C (minor, latent) | WP-23-canvas-toolbar.md:33 promises "right axis ticks `0` and that value". Built: only a `.latency-max` label (TracingChart.svelte:309, :434); the right axis column is empty (WP-17b:39 says so for 17b only). Not reachable today: the server sends no p50Ms/p95Ms (WP-23:22, "target until BM-04"; Bench is out of scope, plan.md:12). Missing the `0` tick and a real axis. |
| Per-lane bar colours (bars use `--accent`) | C (weak: the plan is silent) | Plan colours only the swatches: WP-10:28, WP-17b:38. WP-17b:39 gives bars no colour. But WP-17b:59 and phase.md:45 say to compare "lane colours" with the prototype, and the prototype fills bars with `var(--lane-<key>)` (prototype/tracing-paper-PROTOTYPE.html:1248, :1297). Built: `.bar { background: var(--accent) }` (TracingChart.svelte:413). Fix is one line per lane with an existing token. |

A items: none. No phase 05-07 packet adds any of these (phase 05 is hand-off and unlisting, phase 07 is background run, phase 06 only records differences).

## Lane colours versus the token rule
- No conflict in the wireframe. The prototype defines `--lane-audit/-sql/-iis` as CSS variables per theme (prototype:19, :23, :27) and uses them as `var(--lane-<key>)` (:1248, :1297). Hex appears only in the definitions.
- The app already has them, in `web/src/brand/tokens.css` (lines 15-17 dark, 66-68 light by media, 117-119 light forced). There is no `web/src/styles/tokens.css`; the real file is `web/src/brand/tokens.css`.
- The plan did say otherwise: WP-16b:24 defines the three variables as hex literals in a `<style>` on `.tracing-paper`, and WP-10:20 and :28 add hex fallbacks like `var(--lane-audit, #3fd6b0)`. Those lines break the no-hex rule. The build already departed from them: tokens.css holds the values, `TraceToolbar.svelte` and `TracingChart.svelte` use bare `var(--lane-*)`, and `tracingPaper.test.cjs:48-54` pins "defined once in tokens.css". The packet text is stale. WP-16b:24 and WP-10:20, :28 should be marked superseded.
- Plan guidance for new colour: use only existing variables, define no colour in a component (WP-17b:38, :43; WP-17c:40 "no new colour").
