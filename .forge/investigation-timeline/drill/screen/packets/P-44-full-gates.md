# P-44 Run every gate and report the numbers

model: haiku, effort: low. Reason: fixed commands, fixed order, report only.
risk: low. Review batch: RB-7. Wave: W7. Requirement: R-18.

HARD LIMITS (read first): you run commands and report. You edit NO file. You do not fix a failure. You do not commit, stash, reset or push. Work from `D:\Projects\api-audit-explorer`.

## Outcome
A report with the pass / fail counts of every gate on the finished tree.

## Write scope
- `.forge/investigation-timeline/drill/screen/reviews/gates.md` (new; the report)

## Must not change
- Every other file.

## Changes
Run each command, in this order, from Git Bash. Copy the LAST 15 lines of each output into `gates.md` under a heading with the command.
1. `cd /d/Projects/api-audit-explorer && export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./...`
2. `go vet ./...`
3. `go test ./internal/audit/ ./internal/store/ ./internal/casestore/ ./internal/dbhealth/ ./cmd/... -count=1`
4. `go test ./internal/server/ -count=1` (takes about 2.5 minutes; use a 10 minute timeout)
5. `cd web && npx svelte-check --threshold error`
6. `cd web && npm test`
7. `cd web && npm run build`
8. `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03,SCALL-01,SCALL-02,SCALL-03,SCALL-04,SCALL-05,SCALL-06`
At the top of `gates.md` write one table: `gate | result (PASS/FAIL) | numbers`. For gate 6 write the pass count; the baseline before this plan was 2779 pass, the new count must be higher and `fail 0`.

## Proof
- [ ] `gates.md` has 8 rows.
- [ ] Every row is PASS, or the failing test names are listed word for word.
- [ ] `git status --short` lists only `gates.md` as new from this packet.

## Depends on
P-39, P-42 (all build packets done or reported skipped).

HARD LIMITS (again): no edits, no fixes, no commit. A failure is a result to report, not a task.
