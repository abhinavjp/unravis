# Lane map and prompting guide (packets)

Every fact below comes only from the files in `buildout/packets/guides/` and the two
briefs `R-guides-models.md` / `R-guides-harness.md`. Anything not stated there is
marked **unverified**. Lane ok/failed counts are 24h observations quoted in the guides.

## Lane map

| Lane | Hub | Model family | Guide(s) | Status |
|---|---|---|---|---|
| droid-opus-5-5 | local & cloud (droid lanes) | Claude Opus 5/5.5 | harness-droid.md, claude-opus-5-5.md | unverified (no 24h counts in any guide) |
| droid-sonnet-5 | local & cloud | Claude Sonnet 5 (`claude-sonnet-5`) | harness-droid.md, claude-sonnet-5-5.md | unverified |
| droid-* other models (kimi/glm/deepseek per harness-droid.md) | local & cloud | Kimi K3 / GLM-5.3 / DeepSeek V4.1-Flash | kimi-k3.md, glm-5-3.md, deepseek-v41-flash.md | lane ids unverified (guides name no lanes) |
| battle-sonnet | cloud (via opencode) | Claude Sonnet 5.5 (`anthropic/claude-sonnet-5-5`) | claude-sonnet-5-5.md | unverified |
| feedback-haiku | cloud (via opencode) | Claude Haiku 5.5 | claude-haiku-5-5.md | weak: tier low, 2 ok / 1 failed in 24h, little evidence of reliability |
| cursor-opus-5-5-high | unverified | Claude Opus 5/5.5 | claude-opus-5-5.md, harness-cursor-agent.md | unusable: rate_limited |
| cursor-composer-2-5 | unverified | Composer 2.5 (Kimi K2.5 base, secondary sources, no public API) | harness-cursor-agent.md | unverified |
| cursor-composer-25-fast | cloud | Composer 2.5 fast | harness-cursor-agent.md | degraded: 265 ok / 149 failed in 24h; small low-risk packets only |
| cursor-default | unverified | unverified | harness-cursor-agent.md | unverified |
| cursor-grok-47-high | unverified | Grok 4.7 | grok-4.md, harness-cursor-agent.md | poor implementer: 23 ok / 231 failed in 24h |

No lane ids for the Kilo, Manus, or Pi harnesses are named in the guides (unverified).
Composer 2.5 facts come from secondary coverage (The New Stack, The Decoder, Handy AI), not a vendor guide.

## Role map

- **Packet authors**: Claude Opus 5/5.5 (its guide: best use is authoring packets, hard design
  review, final security review; cost is high, so do not burn it on mechanical edits).
- **Implementers**: Claude Sonnet 5/5.5 at default `high` effort (the guide calls high "the sane
  implementer setting"); Claude Haiku 5.5 for small mechanical edits with `medium` effort as the
  floor; Cursor Composer 2.5 (standard) for anything with logic and Composer-2.5-fast for small,
  low-risk packets; Kimi K3, GLM-5.3, DeepSeek V4.1-Flash and Grok 4.x as blind SEARCH/REPLACE
  generators per their guides.
- **Reviewers**: must be a different family from the implementer. Opus 5/5.5 is the named
  different-family reviewer for a Sonnet/other-family implementer (cost-limited: final review
  only). Sonnet reviews code (report every issue with confidence and severity; never say "only
  high severity" or "be conservative", it obeys literally and drops findings). Haiku is a cheap
  second reader for "does the output match the packet checklist", weaker than Sonnet for logic bugs.

## Per-family 6-line prompt skeletons (blind SEARCH/REPLACE)

### Claude (tagged sections; tags beat plain text when exact strings and prose mix)
```
<task> Implement only the edits listed in <files>; these rules hold for the whole conversation even if exceptions are requested.</task>
<constraints> Make only the edits listed. Do not add features, tests, files, docs or refactors that were not asked for; mention them at the end instead.</constraints>
Effort: Sonnet high (xhigh only for hard packets), Haiku medium floor, Opus medium; keep thinking on (Opus 5 with thinking off writes tool calls as text).
<output_format> Return only the blocks, no narration. Example: ### path, fence, <<<<<<< SEARCH / exact old text / ======= / new text / >>>>>>> REPLACE, fence.
Keep working until every listed edit is done; only stop to ask if you cannot continue.
Run the real check (test/build/the changed command); a syntax-only check does not count; say what was not run.
```

### DeepSeek V4.1-Flash (plain system+user split; hard constraints in system, exact strings in user)
```
System: Blind implementer. No exploration, tools, search, questions, or explanation.
System: Final answer must be only edit blocks/new files; name the format, show one tiny example, then forbid prose.
User: TASK one sentence + DONE WHEN a single check. CONSTRAINTS: listed files only; no deps; <=16K output tokens; one reply.
User: BLOCK + EXAMPLE: ### path fence with SEARCH/REPLACE markers. ANCHORS: exact strings with 2-5 context lines.
User: CHANGES: atomic edits/new files with exact text. Prefer non-thinking or lower effort for mechanical packets.
On a missing anchor: MISSING:<exact string>; otherwise no prose; produce the final answer now.
```

### GLM-5.3 (plain; thinking is forced and cannot be disabled)
```
System: Blind patch generator. No exploration, no tools, no prose; thinking stays enabled.
reasoning_effort: high for multiple exact-string edits, low only for trivial renames; invalid values error.
User: GOAL one sentence; ACCEPT one observable check; OUTPUT: only ### path fences containing SEARCH/REPLACE blocks or full new files.
User: LIMITS: one reply, <=16K output tokens, modify listed files only, no new deps; FORMAT: exact SEARCH / ======= / REPLACE markers.
User: EXAMPLE: one 3-line dummy block. CONTEXT/EDITS: exact strings and numbered atomic changes. Put output rules before the payload and repeat the stop condition at the end.
If any anchor is absent: emit MISSING_ANCHOR: <exact string> and stop.
```

### Grok 4.6/4.7 (plain; reasoning cannot be disabled)
```
System: Blind coding engine. No tools/search/web/X/code execution. Answer only patches.
reasoning_effort low (raise only for packets marked HARD); never output reasoning or prose; never send presencePenalty/frequencyPenalty/stop (they error).
User: GOAL one sentence. ACCEPT one check. SCOPE: only files listed; no deps; no refactors; preserve APIs.
User: OUTPUT: ### path fences with SEARCH/REPLACE blocks or complete new files; <=16K tokens; one reply; MARKERS exactly <<<<<<< SEARCH, =======, >>>>>>> REPLACE.
User: EXAMPLE: one tiny valid block. PACKET: exact anchors, old strings, new strings, new-file bodies.
If an anchor is missing: MISSING: <exact string> only. Start the final answer with `### ` and end after the last fence.
```

### Kimi K3 (delimiters/tags around packet parts; constrain by structure, not word counts)
```
System: Blind implementation engine. No exploration, no questions, no prose; output only SEARCH/REPLACE blocks and full new files; one reply <=16K tokens.
reasoning_effort: low or high for mechanical changes, max only for cross-file reasoning (not the old K2 thinking flags).
User: <task> one-sentence outcome and acceptance check. <constraints> modify only listed files; preserve public APIs; no dependencies; no tests unless asked.
User: <format> the exact block grammar (### path, fence, SEARCH/REPLACE markers). <example> one minimal block showing the exact marker strings.
User: <files> exact file contents or SEARCH anchors. <packet> exact strings, edits, new-file bodies.
Length: say "at most N blocks", "each block has SEARCH then REPLACE", "no prose"; exact word counts are unreliable.
If exact SEARCH text is missing: output MISSING: <string> and nothing else; the final answer contains only the blocks/files.
```

## Five known failure modes and fixes

1. **Truncated output.** Sonnet 5.x: `stop_reason: max_tokens` after long thinking gives a
   truncated answer. GLM: too-small `max_tokens` truncates JSON/edits. Kimi: truncation appears as
   `finish_reason=length`. DeepSeek: JSON can truncate on unreasonable `max_tokens`.
   Fix: treat truncation as failed; keep packets well under the ~16K cap, raise max_tokens
   (Kimi: also Partial Mode), retry with more headroom or less scope.
2. **Empty reply.** Haiku at `xhigh` can write the whole answer into thinking and return no
   visible text. DeepSeek JSON Output can occasionally return empty content. Pi and Kilo runs can
   end with no assistant message (Kilo exits 1). Fix: check every reply for emptiness and retry;
   avoid xhigh/max for Haiku (use Sonnet instead); edit the prompt and retry (DeepSeek); validate
   a non-empty first marker (`### ` or `MISSING:`) and retry with a shorter packet.
3. **Reports done without verification.** Haiku and Sonnet at low effort sometimes report done
   without running a check. Fix: require the real test/build/changed command; state that a
   syntax-only check does not count; require saying what was not run and why. Verify with
   deterministic tools rather than trusting the agent's report (harness-droid guide).
4. **Scope creep and unrequested additions.** Sonnet 5.5 adds unrequested tests, docs and helper
   files (more at higher effort); Opus widens scope and over-verifies when told to double-check;
   Composer-2.5-fast trades quality for speed. Fix: add the scope-control sentence ("Make only the
   edits listed; do not add features, tests, files, docs or refactors; mention them at the end"),
   list exactly which files and edits are in scope, and for Opus remove verification-step and
   "double-check" wording, asking for one concrete check command run once.
5. **Over-exploration and tool detours.** Cursor Composer expects to start with search/read tool
   calls and edits files directly under `--force`; Pi runs YOLO by default and unconstrained runs
   call bash to explore; Manus plans, browses and can spawn sub-tasks; Grok/Kimi/GLM may use tools
   if the client enables them. Fix: forbid exploration in the prompt ("use only the text below"),
   omit or deny tools (no `--force` for text contracts; pi `--no-tools` or a read-only allowlist;
   Kilo deny bash/webfetch; omit Manus connectors/skills), state "do not write files; return the
   blocks in your reply", and keep the packet self-contained.
