# Worker setup: blackbox-wizard-popup

Approach: **D, hybrid** (owner choice 2026-11-16). Planning and independent review stay on native subagents.
Probed 2026-11-16 via `GET /api/lanes` (token never printed).

## Hubs
| Hub | State | Use |
|---|---|---|
| https://hub.54-37-6-173.sslip.io (owner's 1st) | 5 gumloop lanes only; all probes failed (`rate_limited`), 0 ok / 156 failed in 24 h | **not usable now**; re-probe before each batch |
| https://hub.brainpayroll.ae (owner's 2nd) | 87 lanes; the Nov 4 `droid-az-*` lanes are gone | **used** |

Never submit: `battle-sonnet`, `master-opus`, `feedback-haiku`. Never use personal lanes (`*-jayeshk*`, `claude-<name>-*`).

## Remote lanes used (1 slot each, so at most 3 hub jobs at once)
| Lane | Model, harness | 24 h ok / failed | Role |
|---|---|---|---|
| droid-kimi-k3-1-pro | Kimi K3.1 Pro via droid (BYOK, effort fixed at model default `max`) | 17 / 1 | implementation (Go, larger text) |
| droid-glm-5-3 | GLM-5.3 via droid (effort `max`, slow, no disable) | 13 / 2 | small and mechanical packets |
| droid-gpt-6-1-terra | GPT-6.1 Terra via droid | 13 / 2 | implementation of TSK-08; second-family patch reviews |

Prompting facts reused from `~/.claude/hub-model-prompting.md` (verified 2026-11-03/04): hard limits at top and bottom (K3 acts eagerly); exact file lists; timeout 2400+ for K3, 1800+ for GLM; patch-only; slim bundle (413 otherwise); droid exit 0 means ran, not succeeded. **Not verified:** any primary prompting guide for K3.1 Pro or GPT-6.1 Terra (Sol notes applied by inference); the hub has no effort field.

Hub limits that shape the plan: the hub returns a patch only. It cannot run the app or the browser, and its Node and Go tooling is unverified. Every patch is applied locally, and tests (`go test`, vitest, `npm run build`) are run locally by the orchestrator.

## Packet to worker
| Packet | Worker | Model, effort | Why |
|---|---|---|---|
| TSK-02 catalogue + copy | hub droid-kimi-k3-1-pro | max (lane default) | long exact text, must match `groups_types.go` |
| TSK-03 folder/keep rules | hub droid-glm-5-3 | max (lane default) | small pure functions + test |
| TSK-04 drives route (Go) | hub droid-kimi-k3-1-pro (queued after TSK-02) | max | Windows API read, Go test |
| TSK-05 drives client | hub droid-glm-5-3 | max | small |
| TSK-06 dialog shell | native subagent, sonnet | high | focus, Esc, a11y, strong |
| TSK-07 progress rail | hub droid-glm-5-3 | max | small component |
| TSK-08 shared controls | hub droid-gpt-6-1-terra | lane default | three small components |
| TSK-12 extract target picker | native subagent, sonnet | high | must keep Apply working |
| TSK-01 wizard state | native subagent, sonnet | high | wire contract |
| TSK-09, 10, 11, 14 steps | hub (kimi / glm / terra, by free slot) | lane default | step components from spec |
| TSK-13, 15, 16 steps | native subagent, sonnet | medium | watch, folder, review: more logic |
| TSK-17 place wizard | native subagent, sonnet | high | integration |
| TSK-18 mount, delete old | native subagent, sonnet | medium | |
| TSK-19 dist rebuild | orchestrator (local build) | n/a | needs toolchain |
| TSK-20 reviews | **merge-sentinel** review subagent (opus, high), plus hub droid-gpt-6-1-terra patch review | | independent, different family |
| TSK-21 clumsy-user pass | native subagent, sonnet, medium, browser | | needs the running app, never the hub |
| TSK-22 owner live run | owner | | live data |

Escalation: rounds 1-3 same worker; round 4-5 one tier up; cap 5 rounds; one fixer pass per round.
Branch: `feat/blackbox-wizard-popup`, created from HEAD (carries the unpushed installer fix 702e6c2f). No commits and no push without the owner's word.
