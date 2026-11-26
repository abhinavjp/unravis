# Homestretch round 4: build plan

Approved by the owner on 2026-11-26 (prototype `homestretch-redesign-prototype.html`, design notes in `ROUND4-DESIGN.md`).
Rule for every packet: real data path first; anything simulated in the prototype is built for real or stays labelled. One pull request per packet, into `develop`. Each runs `tools/ci/prepush.sh` before the push. UI follows the strict page policy (no inline script or style) and the Impeccable and slop checks.

## Packets and order

| Id | Packet | Model | Depends on |
|---|---|---|---|
| P1 | Queue controls in the engine: move to top, pause and resume claiming (the `PAUSE` file as a command), `--json` for `status`, `runs`, `queue`, exit codes 4 and 5 reserved | Sonnet | none |
| P2 | Machine policy: per project Any / Only these / Any except these, stored beside the queue; the agent claim honours it; why-waiting computed once in Go; drain and resume; CLI `policy`, `machines allow\|deny\|resume` | Opus | P1 |
| P3 | Holds and activity log: hold store, `hold add\|lift\|list\|check` (exit 5), `Promote-Develop.ps1` refuses while a hold is active, append-only activity log with actor `ui`, `cli`, `claude` | Opus | P1 |
| P4 | Read API: `/api/queue`, `/api/policy`, `/api/holds`, `/api/activity` from the real state, no writes | Sonnet | P2, P3 |
| P5 | Write channel: one action registry, `POST /api/action/<id>` with a per-launch token, loopback only, same checks as the CLI, activity entry on every action | Opus | P4 |
| P6 | UI: Queue, Holds and Activity tabs, Machines policy drawer, Overview lead line with hold awareness, prototype components ported to the real strict-CSP UI | Sonnet | P5 |
| P7 | Unified `homestretch` binary (alias `hs`), engine commands under `homestretch ci`, table test that fails when an action has no command | Sonnet | P5 |
| P8 | Rename: shims for `unravis-releases` and `unravis-ci`, `Release.ps1` and launcher updated | Sonnet | P7, last |

Later (not part of round 4, listed so nothing is lost): the generic multi-strategy promote flow (rounds 2 and 3), project onboarding (parked by the owner), GitLab provider.

## Safety notes

- The page and the CLI share one code path per action; the UI never shells out with user text.
- Write endpoints: loopback only, `Origin` and `Host` checked, token header required, JSON body only, size limit.
- Policy edits that would strand queued runs are refused unless the caller confirms (`--force` / confirm flag), and both outcomes are logged.
- A hold blocks Promote only. Release cutting and CI runs are not held in this round.
- Secrets are never read, stored or shown.
