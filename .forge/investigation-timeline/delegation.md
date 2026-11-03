# Delegation split (hub first), decided 2026-11-03

Owner ruling: Runner Hub (http://127.0.0.1:8080) is the FIRST preference. Sources: `~/.claude/runner-hub.md`, `model-allocation.md`, `hub-model-prompting.md`. Lanes probed OK 2026-11-03: droid-opus-5-5, droid-sonnet-5, droid-kimi-k3, droid-glm-5-3, droid-default, cursor-composer-2-5, cursor-default, agy-default, manus-default, kilo nemotron-3-ultra. Down/limited, never use: codex-default, cursor-opus/sonnet/gpt-5-6 lanes, opencode lanes.

The job API has no effort field. Effort is fixed per lane; scope limits go in the prompt (use the "Universal packet prompt template" and "Review prompt template" in hub-model-prompting.md, constraints at top and bottom). Reviews ask for ALL findings with severity labels; never "only high severity".

## Hub (default for every packet that passes the gate below)
| Packet kind | Lane | Timeout |
|---|---|---|
| `model: sonnet`, size M (50 packets), medium risk | droid-sonnet-5 | 1500 |
| `model: sonnet`, size S (22) | droid-sonnet-5 or cursor-composer-2-5 (parallel overflow) | 1200 |
| `model: haiku`, size S (7), mechanical | droid-glm-5-3 (set 2400, default effort is max) or cursor-composer-2-5 | 2400 / 1200 |
| Second implementer when first fails once | droid-kimi-k3 (explicit "edit only these files") | 1500 |
| Patch review, first pass | droid-kimi-k3 (different family from the implementer) | 1200 |
| Patch review, design/risk, second look | droid-opus-5-5 | 1800 |
| Free overflow, low stakes (docs-only, test fixtures) | kilo nemotron-3-ultra | 1500 |

Slots: 2 per lane, so run at most 2 jobs per lane at once; a wave of N packets spreads across droid-sonnet-5 + cursor-composer-2-5 + droid-kimi-k3 + droid-glm-5-3.

## Gate: stays local (not hub)
- The 20 `risk: high` packets, including Phase 07 WP-72c, 73a, 73c, 74b, 74j, 75b and all crypto/key/token/ACL/elevation/shortcut-write code: local Sonnet high, then an Opus security review. Optional extra hub review on droid-opus-5-5 after the local patch exists.
- Anything touching `sql/`, real DBs, secrets, deletions (uninstall), merges, commits.
- Browser/UI acceptance, security canaries, perf runs, clumsy-user pass, plan-§7 phase gates.
- Plan authoring, dependency graph, plan reviews of the security phase.

## Mechanics (what to do before the first job)
1. Packets chain across waves, so a hub bundle must contain earlier waves. Do not commit on the feature branch. Make a scratch branch `hub/p<NN>-w<N>` in a separate worktree with explicit-path commits of applied patches, `git bundle create` from that branch, upload, then `base` = that sha. Never bundle the 269-file dirty tree.
2. One job per packet, same-wave packets in parallel (check-plan reports 0 overlaps). `owner` = `unravis-<phase>`, `remote_id` = packet id.
3. Apply each returned patch in the scratch worktree: `git apply --check`, read it, run `go test ./...` / `npm test` locally (the hub may lack the toolchain), compare the write scope against the packet, then commit to the scratch branch.
4. Failures: one retry with a sharper prompt on the same lane, then droid-kimi-k3, then local Sonnet.
5. Hand patches onto the feature branch only at the phase gate, one local commit per phase by explicit paths. Stop before committing; never push.
6. Watch `get_usage` before each fan-out: Factory credits (droid) and Cursor monthly limits. If droid credits look short, shift S packets to cursor-composer-2-5 and nemotron.

## Open before the build starts
Plan sign-off (Phase 07 round-3 reviews pending) and the owner's go. Nothing is built or sent to the hub yet.
