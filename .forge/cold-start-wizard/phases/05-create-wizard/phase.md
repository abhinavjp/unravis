# Phase 05: create-wizard
risk: medium — reasons: largest UI surface; composes every earlier contract; no new backend beyond phases 1–3.

## Outcome
"Create a new one" opens a large, uncongested wizard (layout B "Workspace" from `.dev/prototypes/coldstart.html`): name the case and optionally name environments (free text), choose what to connect from a gallery that mixes verified scan findings and manual connections, tag each source with environment › portal › API (suggestions offered, nothing auto-filled), review, create, import. Another computer is reached by address (Mongo, plus a reachability check for SQL/Redis) or by files.

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-17 wizard shell, name and environments | medium | TSK-07, TSK-15 |
| TSK-18 sources gallery and scan integration | high | TSK-05, TSK-08, TSK-17 |
| TSK-19 review, create and import | medium | TSK-07, TSK-18 |

## Edges
Strictly sequential in practice: TSK-18 mounts inside TSK-17's shell, TSK-19 reads TSK-18's selection model. Shared files must be serialised; do not run these in parallel.

## Review focus
Free-text environments with commas/duplicates/blank; ticking an unverified finding (must be impossible); a half-created case when the user abandons; the wizard when the scan returns nothing; Mongo database classification that is skipped.

## Phase proof
`cd web; npm test; npm run check; npm run build` green; browser pane walkthrough of the full flow against demo mode (18770) and a live scan (18771).
Commit: one commit for the phase, after a stop. Never push.
