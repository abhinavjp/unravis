# Unravis build-out: goal and evidence

Date: 2026-10-27. Owner: Abhinav. Repo: https://github.com/abhinavjp/unravis (branch master).

## Goal

Take Unravis from today's working tool to Release 1:

- a case lifecycle that is safe to use for real incidents (Reopen, Archive, Verify, Free, Restore, optional per-case key, Erase),
- a guided investigation (Steps 0 to 10) that says what it knows, what it does not, and why,
- SQL, Mongo and Blackbox sources that feed that investigation,
- the Blackbox recorder that keeps 15-second samples on the servers,
- a one-file installer, a GitHub Releases pipeline, and docs with screenshots that match the shipped screens.

Unravis only reads. Anything that changes a server is shown as a script the owner runs.

## Where we are today (evidence from the repo)

| Area | What exists now | Source |
|---|---|---|
| Build | Go 1.27.3, CGO off, pure-Go SQLite (ncruces, adiantum VFS). `build.ps1` runs `npm ci`, `npm run check`, `npm run build`, `go test ./...`, `go vet ./...`, then builds `dist\unravis.exe` and a compatibility copy `dist\api-audit-explorer.exe` (remove after 2026-12-31). | `go.mod`, `build.ps1` |
| go.mod hygiene | `github.com/microsoft/go-mssqldb` is marked `// indirect` but `internal/dbhealth` imports it directly. Needs `go mod tidy`. | `go.mod` |
| Web UI | Svelte 5, Vite 8, echarts 6, TypeScript 5.9. Embedded with `go:embed` from `web/dist`. About 72 node tests (`node --test tests/*.test.cjs`). No Playwright. | `web/package.json` |
| Brand tokens | All tokens and the IBM Plex `@font-face` blocks sit at the top of `web/src/app.css`. A few colour fallbacks live outside it: `web/src/lib/charts.ts` lines 23 to 25, `CaseHome.svelte` lines 257 to 259, `HealthPanel.svelte` lines 264 to 271. `Inspector.svelte` line 50 uses `#000` as a mask, which is fine. | `web/src/` |
| Start-up | Flags `--demo`, `--port` (0 = any free port), `--no-browser`, `--mcp`, `--mcp-allow-identifiers`. No `--version`. Loopback only. Opens the browser with `rundll32 url.dll,FileProtocolHandler`. With `--no-browser` it logs a one-time launch link (`/launch?c=<code>`). | `cmd/audit-explorer/main.go` |
| Demo mode | Uses `audit.NewDemoWithPatterns(time.Now())`, so the data moves with the clock. Demo writes nothing to disk and the case routes answer 503. Screens of cases therefore cannot be captured in demo today. | `main.go` lines 150 to 165 |
| Data folders | Cases under `%LOCALAPPDATA%\APIAuditExplorer\cases`. Session file, history and saved connections under the per-user config folder. `AUDIT_CONFIG_DIR` moves all of them (cases go under it too). | `internal/casestore/sqlite/sqlite.go`, `cmd/audit-explorer/cases.go`, `internal/mcp/sessionfile.go`, `internal/sqlconn` |
| Secrets | Saved SQL connections sealed with user-scope DPAPI in `sqlconnections.dat` (max 20). Sealed passwords are never returned to the UI. | `internal/sqlconn` |
| Case lifecycle | Create, import, Close (one-way today), Erase, Export, Restore (always a new case). Locked key answers 423; untrusted cases answer 409. | `internal/server`, `internal/casestore/sqlite/case.go` |
| Sampler | An in-app sampler exists (`/api/cases/{id}/sampler`). The Blackbox can reuse `internal/sampler`. | `internal/sampler` |
| CI | None. There is no `.github` folder. | repo root |

## What the owner has settled (full list in decisions.md)

- Case lifecycle spec v7 (`buildout/specs/2026-10-26-case-lifecycle-spec.md`) and its decisions log.
- Investigation model and walkthrough (`buildout/research/investigation-walkthrough.md`).
- Workflow wireframe, 139 screens (`buildout/wireframes/workflow-walkthrough.html`), and the case-lifecycle wireframe.
- Brand and tokens (`buildout/brand/unravis-brand.md`).
- Wireframe rulings of 27 October: "In a nutshell" label, outlined buttons, optional SQL probe, N17 dropped.

## New requirements in this plan

1. A one-file installer with preflight, upgrade, repair, uninstall, silent mode, logging and rollback.
2. A GitHub Releases pipeline on a Windows runner.
3. An installer UI in the Unravis brand, sharing tokens with the app.
4. Docs and screenshots in every phase, captured reproducibly.
5. A Phase 1 that can start tomorrow, with later phases ordered by real dependencies.

## Gaps found while planning

- The spec puts the Blackbox out of scope; the owner's later decisions put it in Release 1. This plan follows the owner.
- The wireframe shows 8-character case codes (for example `k7m2qx4a`); the spec uses a 26-character id plus an 8-character short code. Recorded as an open question.
- Running Unravis as a Windows service clashes with user-scope DPAPI, the per-user session file and opening the browser. Recorded as an open question; the plan defaults to a per-user app.
- Demo mode has no cases, so most new screens cannot be captured until Phase 1 adds demo cases for capture.
- On Windows Server, a modern browser is not always installed. The installer preflight must check.

## Files in this folder

- `context.md`: this file.
- `decisions.md`: settled decisions, then planner proposals (marked as such).
- `plan.md`: Release 1 scope, phase list, parallel lanes, the phase packets, open questions, risks.
- `installer-and-releases.md`: installer technology choice, UI wireframes, signing, release pipeline, upgrade check, smoke tests.
