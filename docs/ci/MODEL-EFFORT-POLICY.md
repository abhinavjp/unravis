# Unravis factory: model and effort policy (25 Nov 2026)

Owner rule (nachi): Opus only when risk is really high (security, crypto, credentials, data loss, release/setup that deletes things, high performance impact). Normal builds and reviews use Sonnet. Cheap work uses Haiku-class. Never pick a model blindly.
IDs: `claude-sonnet-5-5` (default), `claude-opus-5-5` (risk), `claude-haiku-5-5` (cheap). Fable 5.2 (`claude-fable-5-2`) is not used by the factory unless the owner asks.

## Why this split (from the docs, checked 25 Nov 2026)
- Price per MTok in/out: Haiku 5.5 $0.10/$0.50 (prompts over 100K: $0.50/$2.50), Sonnet 5.5 $2/$10, Opus 5.5 $4/$20, Fable 5.2 $8/$40. Opus 5.5 is 2x Sonnet per token, not more. Haiku is 20x cheaper than Sonnet.
- Thinking is billed as output tokens and cannot be turned off on Opus/Sonnet/Haiku 5.5. Effort is the cost lever, and Anthropic says it is often a better lever than switching model.
- Claude Code docs: Sonnet handles most coding; reserve Opus for complex or multi-step reasoning; Haiku for simple subagents. Claude Code reports Opus 5.5 at `medium` matches or beats Opus 5 at `high` on coding evals.
- Fable is never an account default and may bill to usage credits; `-p` runs bill without asking. Do not use it in the factory.
- Rate limits are per model class (Opus 5.5 and Sonnet 5.5 have separate limits), so splitting work across models also spreads load. A monthly spend cap returns `enforced_spend_limit_reached` with no retry-after: agents must stop, not retry.

## Table: task -> model, effort, escalate, drop
Effort: set it explicitly (docs disagree on the Sonnet 5.5 default; see Unverified).
| Task | Model | Effort | Escalate when | Drop when |
|---|---|---|---|---|
| Groom / spec / plan | Sonnet 5.5 | high | spec touches security, crypto, credentials, deletion, perf-critical path: Opus, high | item is a pure doc/text change: medium |
| Build / implement | Sonnet 5.5 | medium; high for multi-file or unfamiliar code | diff touches the risky list (below): Opus builds or Opus reviews it. Two failed fix rounds on the same bug: Opus, high | one-file, well-specified change: medium |
| Test writing | Sonnet 5.5 | medium | tests guard a security rule or data-loss path: high, and mutant check | table-driven or golden updates: Haiku 5.5, low |
| Normal PR review | Sonnet 5.5 | high | any MUST on a risky area, or reviewer unsure: second review by Opus | docs-only or backlog-only PR: Haiku 5.5, medium |
| Security / crypto / credentials / data-loss review | Opus 5.5 | high; xhigh only for long multi-file attacker analysis | a MUST survives round 2: fresh Opus, xhigh, new reviewer | never drop below Opus for this class |
| Performance-critical review (hot loop, unbounded alloc, collector cost on customer PC) | Opus 5.5 | high | measured regression or unbounded growth found: Opus, xhigh | change is cold path: Sonnet, high |
| UI audit / UX test with screenshots | Sonnet 5.5 | medium; high when judging layout against spec | auth/consent/destructive-action screens: Opus read of the flow | re-running a known audit and diffing the baseline: Haiku 5.5, low |
| Research (web, docs, repo) | Sonnet 5.5 | medium | conclusion drives a security or cost decision: high, cite sources | simple lookup of one fact: Haiku 5.5, low |
| Docs | Sonnet 5.5 | low-medium | docs state a security guarantee: Opus reads that paragraph | typos, link fixes: Haiku 5.5, low |
| Backlog bookkeeping, PR bodies, handoff notes | Haiku 5.5 | low | needs a judgment call (priority, tick on real data): Sonnet, medium | - |
| CI fix | Sonnet 5.5 | medium | red is a race, flaky, or in a security test: high; never disable the test | lint/format/rebase conflict: Haiku 5.5, low |
| Release, setup, uninstaller, updater (anything that deletes or replaces files) | Opus 5.5 | high | any path/delete logic in diff: Opus review required before merge | - |

## Risky list (Opus review required before merge)
Auth, tokens, grants, keys, encryption, hashing, scrubbing/privacy of data sent to models, audit store integrity, SQL/path/URL input handling, file opens by a privileged service, anything that deletes or overwrites user data, installer/updater/release scripts, collectors that run on the owner's PC every minute, loops or caches with no bound. When unsure, treat as risky.

## Escalation rules
1. Author is Sonnet; if the diff is on the risky list, the author writes "SECURITY-SENSITIVE: Opus review wanted" in the handoff (already the practice: 11 of 15 handoffs in `factory/handoffs/` say so). The conductor then runs the Opus review. No merge before it.
2. A Sonnet review finds a MUST in a risky area: do not fix and merge on Sonnet alone. Fix, then fresh Opus re-review (high).
3. Loop: finding -> fix -> fresh reviewer of the same tier -> repeat until CLEAN. Each round uses a new reviewer with no earlier context. Round 3 on the same area: raise the reviewer to Opus even if it was Sonnet.
4. Two Sonnet rounds produce the same class of finding (e.g. repeated path bugs): the author model is the problem, switch the fix to Opus.
5. Low-confidence or "could not verify on Windows": record it, do not escalate model to guess. Escalate only to get a better analysis, not to replace a live test.
6. Raise effort before raising model for logic depth; raise model before raising to `max`. `max` only with an eval showing headroom; it can overthink. `xhigh` is for long-horizon work (docs say 30+ minutes).
7. Do not change effort mid-session on a cached conversation (it can invalidate the prompt cache); pick it at task start.

## Drop-down rules
- A task done twice with CLEAN results at a lower tier: next similar task starts at that lower tier.
- Mechanical work (rename, baseline refresh, rebase, ticking boxes, formatting) goes to Haiku 5.5 at low. Policy choice (not measured): do not use Haiku to review code, only docs-only or backlog-only PRs.
- Subagents default to `low` effort (docs); use Haiku for read-heavy exploration, Sonnet for anything that writes code.

## Evidence from this repo (calibration)
- Only one head-to-head exists: PR 238 (`reviews/pr238-sonnet-review-1.md` vs `pr238-opus-review-1.md`, same head 1e86df3). Sonnet found two real correctness bugs Opus did not list (HealthUnavailable lost with 2+ instances; 32-byte head misses a UTF-16 log cycle) plus the missing `FILE_SHARE_DELETE`. Opus found the security-class gaps Sonnet did not (Lstat/Open race and reparse points, UNC and `\\?\` paths, forged log events) plus the same `FILE_SHARE_DELETE`. Opus's later rounds ended CLEAN. Reading: Sonnet is good at logic bugs; Opus is better at attacker analysis. That matches the owner rule.
- Opus review of PR 228 (`jsonl-review-1.md`) found 1 High and 4 Medium in a plain CLI-streaming change, so Opus is not only for security, but that does not justify Opus for all CLI work.
- 172 review files exist; only 3 name their model. Most MUSTs cannot be traced to a model. Fix: every review file starts with `Reviewer model: <id>, effort: <level>`.

## Cost notes
- A default Sonnet review costs about half the per-token price of an Opus review for the same tokens. Opus may also spend fewer or more tokens; not measured here.
- Prompt-cache reads are about 5% of input price on Sonnet 5.5 and Opus 5.5 ($0.10 and $0.20 per MTok). Keep the brief, CLAUDE.md and system prompt identical across workers so they cache.
- Batch API gives 50% off but the factory is interactive; use it only for large offline sweeps.
- Fast mode costs 2x on Opus 5.5 ($8/$40); do not use it.
- Budget by tracking `/usage` or Console usage per model after one week; revise this table with real numbers.

## Unverified / gaps
- No Anthropic page I read gives a Sonnet-vs-Opus benchmark for code review or security review. The split rests on docs' general guidance plus the one PR 238 comparison (n=1, briefs differed).
- Default effort for Sonnet 5.5 is `high` on the models overview and choosing-a-model pages but `medium` on the Claude Code model-config page. Set effort explicitly.
- Opus 5.5 token use versus Sonnet 5.5 at the same effort: no published number found; not guessed.
- Whether Claude Code in this factory honors per-subagent `effort`/`model` depends on the version in use; I did not test it.
- Third-party model-routing blog posts appeared in search; I did not use them.

## Sources (fetched 25 Nov 2026)
- https://platform.claude.com/docs/en/about-claude/models/overview
- https://platform.claude.com/docs/en/about-claude/models/choosing-a-model
- https://platform.claude.com/docs/en/build-with-claude/effort
- https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking
- https://platform.claude.com/docs/en/about-claude/pricing
- https://platform.claude.com/docs/en/api/rate-limits
- https://code.claude.com/docs/en/model-config
- https://code.claude.com/docs/en/costs
- https://code.claude.com/docs/en/sub-agents
- Repo: /mnt/project-files/reviews/ (pr238-*, jsonl-review-1), /mnt/project-files/factory/handoffs/, /mnt/project-files/factory/BRIEF.md
