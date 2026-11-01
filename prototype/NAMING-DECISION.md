# Feature name: Investigation Timeline

**Decision:** the screen formerly called "Tracing" (nickname "tracing paper") is now the **Investigation Timeline**. In tight spaces (buttons) it is just "timeline".
**Status:** chosen by the author from the research below; the owner has not yet formally confirmed it. Reversible: the rename is limited to user-visible text.

## Why not "Tracing"
In IT operations "tracing" means distributed tracing (OpenTelemetry, W3C Trace Context): following one request through services. This screen does something different (several independent sources on one time axis), so the old name sets the wrong expectation. [Research S25, S26]

## Why Investigation Timeline
- Describes the job (investigate) and the form (time axis) in words a beginner knows. [Research D, E]
- Closest industry precedent: Grafana's RCA Workbench documents an "investigation timeline" that correlates metrics, logs and traces. [S2, S3]
- "Timeline" is the established word across Elastic, Splunk, Dynatrace and Loggly for chronological investigation views. [S4, S5, S8, S22]

## Rules for using it
1. Page and block title: **Investigation Timeline**. Compact labels and buttons: **timeline** (for example "Show timeline", "Refresh timeline", "Timeline details").
2. Case-home button: **Open the Investigation Timeline**.
3. Never say "tracing" in user-visible text. The specs, the route (`#/tracing/...`), function names and file names keep the old name until the real build.
4. Avoid "Incident Timeline": Datadog and PagerDuty use it for the log of responder actions, statuses and notifications. [S1, S10]
5. IIS's own "Failed Request Tracing" and the existing app's "Call trace" keep their names; they are other features.

## Caveats from the research (be honest in reviews)
- Collision risk is **Medium**: Grafana already uses "investigation timeline" in documentation, though its visible label is "Timeline view". [S3]
- No published study compares beginner comprehension of Tracing / Timeline / Correlation for this kind of screen; the ratings are opinion supported by UX guidance and observed usage. [S31–S33]
- Sources S1 (Datadog), S9 (Sentry) and S34 (Evidence Timeline) could not be opened by the research agent.
- This is not trademark, domain or legal clearance.

## Fallback
"Timeline" alone (shortest, most familiar, less specific). Runner-up descriptive name: "Unified Timeline" (rejected as primary because Verkada already uses the exact name, in physical-security software). Rejected: Tracing, Incident Timeline, Evidence Timeline.

## Related term: Investigation window
The period you inspect is now the **investigation window** (it was "Problem period (incident window)"). It matches the feature name; the helper text says "When was the problem happening?" so it is not read as "how long I have been investigating". The comparison stays **comparison period** ("Compare it with"). The spec term "incident window" remains in the specs and in the Run analysis screen, which is out of scope for now.

## Where it was applied
Screen title, case-home block and button, buttons (Show / Refresh timeline, Back to the timeline, Compare in the Investigation Timeline), "Timeline details", prepared-situation groups, notes in the prototype panel, `TRACING-SCREEN-EXPLAINER.md`, `FLOW-COVERAGE.md`, browser checks.
