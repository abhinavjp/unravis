# Factory worker brief (Unravis, 23 Nov 2026)

You are one stage of an automated backlog factory. Work alone in your own git worktree. Do not ask the owner anything; decide small choices yourself and write them in your handoff.

## Setup
- Repo: /home/claude/unravis (origin abhinavjp/unravis). Make your own worktree from the latest develop:
  `cd /home/claude/unravis && git fetch origin develop && git worktree add /home/claude/wt/<name> -b claude/<name> origin/develop`
- Node 22.23.5 and Go 1.27.4 are installed. Web: `cd web && npm ci` once.
- Read `CLAUDE.md` first. Key rules: real data path, never demo-only; PR targets `develop`; do NOT commit generated web files (`web/dist`, `cmd/unravis-setup/ui`, `cmd/unravis-lite/ui`); no test may assert new UI text against the embedded copy (read `web/src`).
- Do NOT touch Blackbox SQL sampler files (`internal/bbsql`, `internal/blackbox/sqllog*`, other threads own them).

## Every PR needs
1. Tests that fail without the change and pass with it.
2. Go tests: about 382 Go tests fail on Linux for Windows-only reasons. Compare with `docs/desktop/baseline-go-tests.txt`, never with zero. Run only the packages you touch.
3. UI change: run `cd tools/uiaudit && node run-audit.mjs` (or the screens you touch), keep 0 new errors and warnings, remove any `tools/uiaudit/baseline.json` entry you fixed. Desktop layout first.
4. Security and perf check of your own diff (inputs, paths, secrets, unbounded loops, allocation).
5. Backlog: edit `docs/BACKLOG.md` with ONE small change at the end (add a "Built <date> (PR #N), not ticked" note on your item, or add new items with the next free BL number). Rebase on develop right before you push, because other workers edit the same file. Tick an item only if it is proved on the real data path; items that need Windows or the owner's PC stay unticked.
6. Commit message ends with:
   `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`
   `Claude-Session: https://claude.ai/code/session_01N1ipCkPytHVnTk4vnQYFYe`
7. Push `claude/<name>`, open the PR with the GitHub MCP tools, base `develop`. PR body starts with exactly these two lines, then your text, then the footer:
   `<!-- claude-projects-attribution: {"github_login":"shahnachiketdipak-coder"} -->`
   `_Requested by **nachi** · [project thread](https://claude.ai/code/project/chan_0154i2a1DFEjyop1DCimMhQM?thread=cmsg_0154i2a1DFEjyop1DCimMhQM6yUhWRu94yuQMCGaJbD6Ww)_`
   footer: `🤖 Generated with [Claude Code](https://claude.com/claude-code)` and `https://claude.ai/code/session_01N1ipCkPytHVnTk4vnQYFYe`.
   Then add `shahnachiketdipak-coder` as assignee and request their review (separate calls, best effort).
8. Watch your PR's CI (`fast-build`); if red, read the logs and fix. Do not skip or disable tests. Do not close and reopen the PR.

## Handoff
When done, write `/mnt/project-files/factory/handoffs/<name>.md` (md, short): what was built, files, PR number and CI state, what is verified (Linux/demo) and what is NOT (Windows/live), decisions you made, leftovers. Final chat answer: 5 lines max, same facts.
If your item turns out blocked (needs Windows, UAC, a secret, a depended-on item not built), stop, write the handoff saying why, and report. Do not fake it.
