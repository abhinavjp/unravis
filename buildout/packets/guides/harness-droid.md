# Guide: Factory droid CLI (harness; lanes droid-*, local and cloud)

Fetched 2026-10-29 (vendor primary sources; docs moved from docs.factory.ai to docs.factory.com via 308 redirect):
- https://docs.factory.com/cli/droid-exec/overview.md
- https://docs.factory.com/droid-cli/cli-reference.md
- Search result list also showed https://docs.factory.ai/guides/building/droid-exec-tutorial (not fetched).
No dedicated prompt-writing section exists in the droid docs; model-specific guidance comes from the model vendor guides (Claude, Kimi, GLM, DeepSeek, Grok files in this folder).

## What the harness does
- `droid exec` is a one-shot non-interactive run. Default (no flag) is read-only: it can inspect files and run read-only git, but cannot edit. `--auto low` allows file edits; `--auto medium` adds local installs/builds/tests/commit (no push, no sudo); `--auto high` adds remote writes. `--skip-permissions-unsafe` only for disposable environments and cannot combine with `--auto`.
- `-r/--reasoning-effort` (levels depend on the model; not applicable to custom models), `--use-spec` (plan first in spec mode), `--spec-model`, `--spec-reasoning-effort`, `-f <file>` (prompt from file), `-o text|json|stream-json`, `-m <model>` (custom models via `custom:` prefix), `--cwd <abs path>`, `-s <session>` to continue, `--fork`.
- Exit code 0 means the run finished, not that the task succeeded. An above-level action stops the run, prints to stderr and exits non-zero, leaving earlier edits in the tree.

## How to prompt this lane for small blind-implementable work packets

- If the hub runs droid without `--auto`, it is read-only: a packet asking for file edits will "succeed" with no edits. For packets that RETURN SEARCH/REPLACE blocks as text (our contract) read-only is fine and safer; say explicitly "Do not write files; return the blocks in your reply." Confirm the lane's autonomy with the hub notes before relying on either.
- Droid is an agent: it will read the repo with tools. For blind packets, put exact file paths, exact SEARCH strings and exact checks in the prompt so it needs no exploration; tell it which files it may read and "do not explore beyond them".
- Use the model's own guidance for style: Claude lanes want tagged, literal, scoped prompts (see claude-*.md); kimi/glm/deepseek lanes want a plain numbered spec and a pinned output format.
- Spec mode (`--use-spec`) adds a planning phase and a second model call; skip it for packets that are already fully specified, use it only when the packet leaves design choices open.
- Reasoning effort: set it per lane (higher for review/authoring, medium for mechanical edits). Anthropic models respect low effort literally (may under-think); GLM and Kimi always think.
- Verify with deterministic tools (tests, linter) rather than trusting the agent's report; the docs themselves recommend this and committing in your own script, not via `--auto high`.
- Put the whole prompt in one file (`-f`) to avoid shell-quoting damage to exact strings.

Known failure modes
- Silent no-op in read-only mode; unknown tool names fail before start; ask-level permission rules cannot be answered in one-shot runs; Alpine images unsupported.
- Output cap: the final reply is the model's output, so the ~16K token limit of the underlying model applies; keep packet scope small.
