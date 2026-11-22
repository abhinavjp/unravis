# Groom 08: testing, CI and repo upkeep (BL-440 to BL-459, 18 used)

Status: groom draft, 2026-11-21. Stage 1 only (no spec, no plan, no build). Sources: research 09 (testing and CI) and 10 (maintainability and monorepo), `DECISIONS-draft.md` sections 10 and 11, `docs/BACKLOG.md` (Auto-updater section: BL-149 to BL-169, BL-194 to BL-197).

Rules used here:
- Owner rule (CLAUDE.md): done = works on the real data path. CI jobs use synthetic or recorded fixtures; they prove wiring only. Every item that touches data names a "live PC (nachi)" acceptance on real data, read-only.
- Tags per acceptance: **CI**, **live PC (nachi)**, **elevated/UAC**. GitHub Actions runners run as admin with UAC off, so UAC itself can never be tested in CI (report 09).
- Versions below are from the reports, dated Nov 2026. Re-check at spec stage.
- Report 09 and report 10 are partly inferred (`[INFERRED]`). Flagged where it matters.
- Repo layout (additive `desktop/src-tauri`, one Go module, PowerShell orchestration, Dependabot with no auto-merge) is a decision in report 10. It is not an item here except where a test needs it. Dependabot and AGENTS.md are folded into BL-440 and BL-441.

## Index

| ID | Title | Size | Phase |
|---|---|---|---|
| BL-440 | CI baseline audit, repo visibility and plan decision | S | P0 |
| BL-441 | Linux gates: lint, typecheck, race tests, known-failure baseline, Dependabot | M | P0 |
| BL-442 | Windows CI job skeleton with cache, Defender exclusions and build timing | M | P2 |
| BL-443 | Rust `tauri::test` harness and comctl32 manifest fix | M | P2 |
| BL-444 | Go Windows tests: named pipe, MCP subprocess, Session 0 pipe peer | M | P1 |
| BL-445 | Shared JSON Schema and golden fixtures (Go, Rust, Svelte) | M | P3 |
| BL-446 | Playwright Linux project with mocked Tauri IPC | M | P3 |
| BL-447 | Playwright WebView2 CDP smoke on Windows, de-elevated, with soak | L | P2 |
| BL-448 | Accessibility, visual baselines and shell focus checks | M | P3 |
| BL-449 | Security scan workflow (deps, secrets, licences) | S | P2 |
| BL-450 | Fuzz targets: PR corpus replay and nightly runs | M | P3 |
| BL-451 | Diff-coverage gate for Go, Rust and Vitest | M | P2 |
| BL-452 | `cargo-mutants --in-diff` on Rust IPC code | S | P3 |
| BL-453 | Installer CI: per-user (standard account) and per-machine silent installs | L | P4 |
| BL-454 | Services CI: Blackbox, BlackboxSql, Keeper, Session 0 pipe, PDH counters | M | P4 |
| BL-455 | OTA CI: N-1 to N, bad signature, 3-bad-start rollback on a localhost feed | L | P4 |
| BL-456 | Release evidence: SBOM, attestation, release-only ARM runner | S | P4 |
| BL-457 | Live-PC acceptance runner: one read-only script on nachi's PC for every gate | M | Parallel A (starts P0) |

---

## BL-440 CI baseline audit, repo visibility and plan decision
Outcome: we know what CI runs today, on which branch lineage, at what cost, and the owner has picked public or private and the GitHub plan.
- Scope (in): list jobs in `.github/workflows` on the feature branch and on `origin/master` (they differ: report 10 says `ci.yml`/`release.yml` on the feature branch, and `verify-release.yml`, `cmd/unravis-lite` only on master). Record runners, minutes, cache size (`gh cache list`), current required checks. Write a one-page result in `docs/desktop/`. Lay out the cost table for public vs private (report 09: $0 public; about $6.50 to $8.50 per month private, or $0 overage with weekly installer/fuzz). Record what is unavailable if private: CodeQL, Secret Protection, `actions/attest`, Rulesets on Free.
- Out of scope: changing visibility; adding jobs (later items); branch rules themselves (BL-197).
- Depends on: P0; BL-197 (same GitHub settings check).
- Risks / unknowns: report 09 risk 6 (public vs private) and risk 5 (10 GB cache churn, measure after 2 weeks). Report 10 risk: published tag/branch relationship and live feed state were not verified by the researcher. No build-time data exists for Tauri + 375k LOC Go + Svelte on a 2 vCPU Windows runner (report 09 risk 1).
- Acceptance:
  - CI: a script `gh run list` output saved shows last 20 runs per workflow with minutes. (CI)
  - live PC (nachi): owner confirms branch rules and plan in GitHub settings, and the decision is written down. (live PC (nachi))
  - CI: the doc states which later items are blocked by "private" (BL-449 CodeQL part, BL-456 attest). (CI)
- Size: S. Phase: P0.
- Needs owner action? Yes: public or private, GitHub plan (money, about $4/month for Pro if private), settings in the GitHub UI.

## BL-441 Linux gates: lint, typecheck, race tests, known-failure baseline, Dependabot
Outcome: every PR runs `golangci-lint` v2, `go test -race`, `svelte-check`, `tsc --noEmit`, Vitest and the existing build on `ubuntu-latest`, with no new failures.
- Scope (in): `.golangci.yml` with `errcheck`, `govet`, `staticcheck`, `gosec`, `rowserrcheck`, `bodyclose`, `nilerr` (report 09 names golangci-lint v2.15.0). `go test -race ./...` with the existing rule that `internal/server` has 19 known Windows-only failures on Linux (DECISIONS section 10): store that list as an explicit allowlist file so a 20th failure fails CI. `npm run check`, `npm run test:unit`, `npm run build` (main, setup, Lite). `dependabot.yml` for gomod, npm, github-actions (Cargo added when `desktop/` lands), weekly grouped, no auto-merge (report 10). Pin third-party actions by SHA, `permissions: contents: read`, `concurrency` cancel on PRs.
- Out of scope: Windows jobs (BL-442); coverage gates (BL-451); security scanners (BL-449); an `AGENTS.md` rewrite (report 10 suggests a short one; a small doc change, decide at spec).
- Depends on: P0; BL-440.
- Risks / unknowns: first `golangci-lint` run on 375k LOC will be noisy (report 09 gives no count). Needs a baseline file or `new-from-rev` so only new code fails. `gosec` false positives unknown. Report 09 cites `ubuntu-slim` 15-min timeout (only for light jobs).
- Acceptance:
  - CI: a PR that adds a deliberate `errcheck` violation fails; reverting it passes. (CI)
  - CI: a PR that makes a 20th `internal/server` test fail on Linux fails the job; the 19 known ones do not. (CI)
  - CI: Dependabot opens a first grouped PR; none auto-merges. (CI)
  - live PC (nachi): `go test ./...` result on nachi's Windows PC is compared with the Linux allowlist (the 19 should pass there). (live PC (nachi))
- Size: M. Phase: P0.
- Needs owner action? No.

## BL-442 Windows CI job skeleton with cache, Defender exclusions and build timing
Outcome: one `windows-2025` job builds the Go sidecar, the web bundle and the Tauri shell, with warm-cache timing recorded.
- Scope (in): job `windows-pr-gate` per report 09: `Add-MpPreference -ExclusionPath` for workspace, `.cargo`, `go-build`, npm cache BEFORE `actions/checkout`; `setup-go`, `setup-node` (v7 per report, check), `Swatinem/rust-cache@v2` (v2.9.3); build Go sidecar to `src-tauri/binaries/unravis-x86_64-pc-windows-msvc.exe`; `npm ci && npm run build`; `cargo build`. Record cold and warm minutes in the job summary. Trigger only when `desktop/**`, Go, `web/**` or workflow paths change (report 10: prove every cross-language input triggers the job before narrowing path filters).
- Out of scope: tests inside the job (BL-443, BL-444, BL-447); signing; installer.
- Depends on: P2; BL-306 (shell skeleton), BL-317 (sidecar naming), BL-320 (pinned Rust and Tauri).
- Risks / unknowns: no build-time data (report 09 risk 1). If cold build is over 20 min, split artifacts or test `sccache` with R2 (report says skip sccache first because of MSVC PDB friction). `windows-2025` has only `C:\`. Report 10 asks for one clean build from a path containing spaces: add that case.
- Acceptance:
  - CI: green build from a clean cache, then a second run reuses cache; both times recorded in the summary. (CI)
  - CI: a clone path with a space builds. (CI)
  - CI: touching only `web/` still triggers the job. (CI)
  - live PC (nachi): `build.ps1` on nachi's PC produces the same set of file names as the CI job. (live PC (nachi))
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-443 Rust `tauri::test` harness and comctl32 manifest fix
Outcome: Rust commands can be unit-tested on Windows CI with `MockRuntime`, and `cargo test` runs without `STATUS_ENTRYPOINT_NOT_FOUND`.
- Scope (in): make all testable commands and setup helpers generic over `<R: tauri::Runtime>`; one internal harness module wrapping `tauri::test` (marked unstable); enable `features = ["test"]`; embed a Common-Controls v6 manifest in test binaries or delay-load `comctl32` (report 09; tauri#13419); first tests: command serde, `State<T>`, capability ACL denied for non-granted commands (ties to `AppManifest::commands`).
- Out of scope: WebView2 tests (BL-447); the commands' feature logic.
- Depends on: P2; BL-306, BL-321 (zero plugins, command gating), BL-442.
- Risks / unknowns: report 09 risk 3: whether Unravis's exact plugin set triggers the comctl32 crash is unverified. With zero plugins at first (DECISIONS), it may not trigger. `tauri::test` is unstable API. The report's fix is `[INFERRED]`.
- Acceptance:
  - CI: `cargo test` passes on `windows-2025`. (CI)
  - CI: test proves a command not listed in the capability file is rejected. (CI)
  - CI: if a plugin that imports `TaskDialogIndirect` is added in a throw-away branch, the test binary still loads (guards the manifest fix). (CI)
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-444 Go Windows tests: named pipe, MCP subprocess, Session 0 pipe peer
Outcome: Windows-only Go behaviour (pipe security, MCP framing, peer checks) is tested on every PR on a real Windows runner.
- Scope (in): job step `go test -count=1 ./internal/win/... ./internal/mcp/... ./internal/keeper/...` (paths from report 09 are a sketch; real package names to be confirmed). Ephemeral per-test pipe names `\\.\pipe\UnravisKeeper-test-<pid>-<rand>` via `go-winio`; do not rely on `CloseWrite()` (go-winio #257). Black-box `os/exec` test of `unravis.exe --mcp`: JSON-RPC framing for all 10 read-only tools, rejection of over 1 MiB input or output, default identifier redaction vs `--mcp-allow-identifiers`. Pipe tests for the new `--pipe-server`: SDDL current user only, remote clients rejected, first-instance flag, peer PID check.
- Out of scope: real service tests (BL-454); the pipe server code (BL-301, BL-330); MCP SDK migration (BL-342).
- Depends on: P1; BL-301, BL-330, BL-340.
- Risks / unknowns: go-winio version in report 09 is `v0.6.x` from 2024 (`[OLD]`). Pipe ACL tests need a second Windows user to prove "other user denied"; in CI a created local user can do it (see BL-453). Names of the 10 tool and flags come from the repo, not the report.
- Acceptance:
  - CI: all of the above pass on `windows-2025`; parallel test runs do not collide on pipe names. (CI)
  - CI: a second local user is denied on the pipe; remote client flag is set. (CI)
  - live PC (nachi): `unravis.exe --mcp` run against nachi's real dashboard and a real read-only investigation returns bounded, redacted results for the 10 tools. (live PC (nachi))
- Size: M. Phase: P1.
- Needs owner action? No.

## BL-445 Shared JSON Schema and golden fixtures (Go, Rust, Svelte)
Outcome: one schema plus golden JSON per wire shape; Go, Rust and the UI types all test against the same files, so field drift fails CI.
- Scope (in): `contracts/` per report 10 (`contracts/desktop/v1/` IPC envelope and handshake, `contracts/fixtures/`). Go validates with `santhosh-tekuri/jsonschema/v6` (pure Go, `CGO_ENABLED=0`); Rust uses the `jsonschema` crate and deserialises the same golden files into `serde` structs. Golden files are CAPTURED from real handler responses on the first slice (`/api/info`, `/api/version`, one bounded read), including omitted field, explicit null, `204`, auth error, locked `423`, validation error, request id (report 10 pilot step 1). Large integers as decimal strings.
- Out of scope: OpenAPI generation and `oasdiff` (report 10 pilot; belongs to the shell/API owner, see conflicts); MCP schemas beyond a pointer.
- Depends on: P3; BL-314 (Rust relay, first slice).
- Risks / unknowns: report 09 chooses JSON Schema + goldens, report 10 chooses OpenAPI for HTTP. They overlap (see conflicts). Schema checks do not catch redaction, authorization, locking or timeout changes (report 10) so those need adapter tests (BL-444, BL-354).
- Acceptance:
  - CI: Go test fails when a response field is renamed; Rust test fails on the same change. (CI)
  - CI: golden files regenerate from a recorded real response with no diff. (CI)
  - live PC (nachi): the recorder is run once against nachi's real dashboard (read-only) and the captured real response validates against the schema; no secrets or real identifiers are committed (scrubbed first). (live PC (nachi))
- Size: M. Phase: P3.
- Needs owner action? Deleting or committing real data: no; scrub rule only. Owner reviews that nothing sensitive lands in `contracts/fixtures/`.

## BL-446 Playwright Linux project with mocked Tauri IPC
Outcome: the full existing Playwright UI suite runs on `ubuntu-latest` in two modes: browser against the Go test server, and a new `chromium-mocked` project that fakes Tauri IPC.
- Scope (in): `@tauri-apps/api/mocks` `mockIPC(cb, { shouldMockEvents: true })` and `mockWindows('main')` injected with `page.addInitScript`. An auth fixture that hides the launch-link vs Tauri-handoff difference. Wait for `window.__TAURI_INTERNALS__` on startup (tauri#12348). Note: `page.route()` intercepts HTTP to Go, not `invoke()`, so IPC needs its own mocks (report 09). Wire into `web-and-e2e-linux`.
- Out of scope: real WebView2 (BL-447); visual baselines (BL-448).
- Depends on: P3; BL-441, BL-323 (CSP-compatible UI), host adapter in `web/src/lib/`.
- Risks / unknowns: the mock hides real IPC bugs, so it cannot replace BL-447. Existing suite's exact reuse rate (report says 100% on Linux, about 95% on WebView2) is `[INFERRED]`.
- Acceptance:
  - CI: existing suite passes unchanged in browser mode; `chromium-mocked` passes the desktop-adapter flows. (CI)
  - CI: a test where `invoke` is mocked to fail shows the UI error state. (CI)
  - live PC (nachi): the same suite run (`PLAYWRIGHT` against a locally run real Go server) on a real investigation, read-only. (live PC (nachi))
- Size: M. Phase: P3.
- Needs owner action? No.

## BL-447 Playwright WebView2 CDP smoke on Windows, de-elevated, with soak
Outcome: 8 to 12 critical flows run against the real Tauri exe and WebView2 on `windows-2025` on every PR; flake rate is measured.
- Scope (in): fixture per report 09 (spawn exe with unique `WEBVIEW2_USER_DATA_FOLDER` and per-worker port, poll `/json/version`, `chromium.connectOverCDP` with `noDefaults: true`, `taskkill /T /F` on teardown). Run de-elevated (`gsudo -d` or a standard local user) because WebView2 150+ ignores the remote-debugging flag when elevated (WebView2Feedback #5645). Flag only in a test build/profile; add a release-binary check that the flag and any test-only feature are absent (ties to BL-325 which strips `WEBVIEW2_*` env). Flows: start, auth bootstrap, case open, one chart, tray hide/show, shutdown. Replace hardcoded `127.0.0.1` navigation with relative navigation (`tauri.localhost`).
- Out of scope: native dialogs and tray menus (CDP cannot drive them; test the Rust handlers in BL-443 or use a `#[cfg(feature = "e2e")]` trigger); visual baselines; fallback `@wdio/tauri-service` build (write down the switch rule only).
- Depends on: P2; BL-442, BL-446 (shared specs), BL-306, BL-308, BL-311.
- Risks / unknowns: report 09 risk 2: attach hangs (`playwright#42730`), flaky on WebView2 150+. Rule from report: 20x soak of the 10-test suite; switch to `@wdio/tauri-service` embedded if flake is over 2%. DECISIONS says desktop mode has no loopback port, so the fixture's "wait for token handoff" path depends on BL-332. Report 09 says `gsudo` use is in CI only.
- Acceptance:
  - CI: 10-test smoke passes; 20 consecutive runs show flake at or below 2%; if above, the item records the switch decision. (CI)
  - CI: release build contains no `--remote-debugging-port` support and no e2e feature (string and feature check). (CI)
  - live PC (nachi): the same smoke run against the installed desktop on nachi's PC, opening a real case read-only; also screenshots of the real window attached. (live PC (nachi))
- Size: L. Phase: P2 (smoke), grows through P3.
- Needs owner action? No.

## BL-448 Accessibility, visual baselines and shell focus checks
Outcome: DOM accessibility, chart visuals and real-shell focus behaviour are checked, each on the platform where it is stable.
- Scope (in): `@axe-core/playwright` 4.14 on the Linux project and on the WebView2 smoke. Visual baselines (`toHaveScreenshot`) on Linux in the pinned Playwright Docker image (webp, per report); only 2 to 3 shell-chrome shots on Windows with `maxDiffPixelRatio: 0.01` and `deviceScaleFactor: 1`. ECharts 6 determinism: `animation: false` in test mode, `page.clock.setFixedTime`, wait for `finished`, assert series/axes with `page.evaluate`. Keyboard-focus smoke on Windows (Alt+Tab returns focus; tauri#15624, fixed in Tauri 2.12.0). Pin Tauri at or above 2.12.7.
- Out of scope: Narrator/NVDA manual test (axe cannot see UIA; stays manual, noted); P5 accessibility evidence write-up.
- Depends on: P3; BL-446, BL-447, BL-320.
- Risks / unknowns: sub-pixel diffs between Windows and Linux (report 09). Baseline refresh policy is open. UIA gaps are not covered by axe.
- Acceptance:
  - CI: zero serious/critical axe violations on the listed screens; baselines stable over 5 reruns. (CI)
  - CI: Windows focus smoke passes. (CI)
  - live PC (nachi): manual NVDA or Narrator pass on the real shell (open case, tab order, tray reopen). (live PC (nachi))
- Size: M. Phase: P3 (a11y evidence feeds P5).
- Needs owner action? No.

## BL-449 Security scan workflow (deps, secrets, licences)
Outcome: one fast workflow blocks PRs on known-vulnerable or unlicensed dependencies and on leaked secrets.
- Scope (in): `cargo-deny` (advisories, SPDX licence allowlist, bans, sources) via `EmbarkStudios/cargo-deny-action@v2`; `govulncheck` plus `osv-scanner` (`--call-analysis=go`, expiring `EffectiveUntil` waivers in `osv-scanner.toml`); `trufflehog --only-verified`; `gitleaks` as local pre-commit; npm `ignore-scripts` check. Pin by SHA (the report's sketch uses `trufflehog@main`: do not).
- Out of scope: CodeQL (public repo only, see BL-440); SBOM (BL-456); the larger supply-chain gate set owned by BL-335 (see conflicts).
- Depends on: P2 (Cargo lock exists); BL-440; overlaps BL-335.
- Risks / unknowns: version disagreement: report 09 lists `govulncheck` v1.1.4 (Jan 2025, `[OLD]`), report 10 lists v1.10.0 (2026-11-17). Use the newer; confirm at spec. DECISIONS says no Trivy action (CVE-2026-33634). Go 1.27.4 security patch is named in report 10 as the first maintenance item. Stdlib DB lag is why both `govulncheck` and `osv-scanner` run.
- Acceptance:
  - CI: a PR adding a crate with a known advisory fails; with a disallowed licence fails; with a fake secret pattern fails (verified detector uses a test token on a throw-away branch). (CI)
  - CI: waiver with a past `EffectiveUntil` fails. (CI)
  - live PC (nachi): `gitleaks` pre-commit hook installed and blocks a staged fake secret on nachi's PC. (live PC (nachi))
- Size: S. Phase: P2.
- Needs owner action? Installing the local hook on nachi's PC only. If public: enabling GitHub secret scanning in settings.

## BL-450 Fuzz targets: PR corpus replay and nightly runs
Outcome: parsers that take untrusted input are fuzzed nightly; any crasher found becomes a permanent PR test.
- Scope (in): Go native fuzz targets for the MCP JSON-RPC request, the update manifest parser, SQLite decoders (report 09 names `FuzzMCPRequest`, `FuzzUpdateManifest`; real names TBD). Committed `testdata/fuzz/` corpus replays on every `go test ./...`. Nightly 2 min per target on `ubuntu-latest`. `cargo-fuzz` (0.13.2) for a platform-agnostic Rust `ipc_frame` crate, Linux only (ASan link errors on MSVC).
- Out of scope: Windows fuzzing; code-mode escape tests (BL-375); MCP injection suite (BL-354).
- Depends on: P3; BL-441; a split-out Rust IPC crate (BL-314). Rust target waits for that crate.
- Risks / unknowns: DECISIONS says one Cargo package until a second crate exists; a fuzzable `ipc_frame` crate may force a second crate, a conflict with report 10 (see conflicts). `cargo +nightly` needed.
- Acceptance:
  - CI: nightly workflow runs; a seeded known-bad input found by fuzzing is saved and then replayed by PR CI. (CI)
  - CI: corpus replay runs in the normal PR job. (CI)
  - live PC (nachi): a fuzz target run for 10 min on nachi's PC against a real update manifest and a real MCP session log (read-only). (live PC (nachi))
- Size: M. Phase: P3.
- Needs owner action? No.

## BL-451 Diff-coverage gate for Go, Rust and Vitest
Outcome: changed lines in a PR must be covered at 75 to 85% (final number at spec stage); old code is not blocked.
- Scope (in): `scripts/ci-diff-cover.sh` (report 09 sketch; write in PowerShell or bash per repo convention, report 10 says PowerShell orchestration). Inputs: `go test -coverprofile`, `cargo-llvm-cov` 0.9.1, Vitest coverage. Compare against `git diff origin/<base>`. Report in the job summary. Scope the gate to paths with logic; exclude generated files (contracts, OpenAPI output).
- Out of scope: repo-wide coverage threshold (report rejects it); mutation tests (BL-452).
- Depends on: P2; BL-441, BL-442.
- Risks / unknowns: tool choice and exact threshold are the researcher's pick. Windows-only Go code is not covered on Linux, so the gate needs to merge Linux and Windows profiles or accept a gap. Assertion-free tests can game it, hence BL-452.
- Acceptance:
  - CI: a PR adding an uncovered function with 10 changed lines fails; adding a test passes. (CI)
  - CI: a docs-only PR passes without tests. (CI)
  - CI: the script gives the same result locally and in CI on the same diff. (CI)
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-452 `cargo-mutants --in-diff` on Rust IPC code
Outcome: on PRs that touch the Rust IPC, auth or sandbox code, surviving mutants fail the check.
- Scope (in): `cargo-mutants` 27.1.0, `--in-diff` against the PR diff, `--no-shuffle`, on `ubuntu-latest`; exits 0 when no Rust changed. Scope limited to the IPC/command-gating modules.
- Out of scope: Go mutation testing in CI (report says no: `go-mutesting` abandoned, `gremlins` broken on Go 1.25+, `ooze` slow). Optional local `ooze` run on the ed25519 verifier, sealing and MCP redactor: noted only, not built.
- Depends on: P3; BL-314, BL-321, BL-451.
- Risks / unknowns: IPC code must compile on Linux (Windows-only code cannot be mutated there). Run time unknown. DECISIONS says Rust target is under 1,500 LOC, so cost should be small.
- Acceptance:
  - CI: a PR that weakens a capability check without a failing test is caught by a surviving mutant. (CI)
  - CI: a PR with no Rust changes is a no-op pass. (CI)
- Size: S. Phase: P3.
- Needs owner action? No.

## BL-453 Installer CI: per-user (standard account) and per-machine silent installs
Outcome: `unravis-setup.exe` is installed silently on a clean runner both per-user (under a real standard user) and per-machine, and the result is checked.
- Scope (in): script `scripts/ci-verify-installer.ps1`. Create a local user in group `Users`, run setup via a Scheduled Task with `RunLevel = LeastPrivilege` (report 09 pattern), check `%LOCALAPPDATA%`, `HKCU` Run key, shortcuts, files, upgrade-in-place, `--recover` after a killed install, and uninstall. Per-machine as admin. Do not use `runas /trustlevel` (stays High) or `psexec -l` (Low). Non-interactive flags (`--silent --scope=user|machine --install-dir`) may need to be added to setup: the report calls them `[INFERRED]`, so first check what setup exposes today.
- Out of scope: UAC prompt path; desktop-specific installer steps (BL-404, BL-405) and release verification (BL-408); MSI (BL-412, BL-413).
- Depends on: P4; BL-404, BL-405, BL-442. Reuses BL-153 (`verify-release.yml`) lineage.
- Risks / unknowns: report 09 says UAC cannot be exercised in CI (UAC off), so per-machine from a standard user is untested there. CI scheduled-task user needs a password in the workflow (test-only, local throw-away account; do not reuse the example value). Hosted runners cannot represent Windows 10, Server 2016 or 2019 (report 09 risk 4); `windows-2022` and `windows-2025` only.
- Acceptance:
  - CI: fresh per-user install under the standard user succeeds, files and keys land in the user's own profile and nothing under `HKLM`. (CI)
  - CI: per-machine install, upgrade-in-place and `--recover` pass; the desktop exe and the headless parts are both present. (CI)
  - elevated/UAC: on nachi's PC (or Windows Sandbox) a standard user starts setup, sees the UAC prompt, and the machine-scope step completes after approval. (elevated/UAC)
  - live PC (nachi): upgrade over nachi's real current install keeps real case data readable (read-only open before and after). (live PC (nachi))
- Size: L (split into per-user and per-machine PRs at spec). Phase: P4.
- Needs owner action? Yes for the UAC check (owner clicks UAC prompt). Real-data check must not delete user data.

## BL-454 Services CI: Blackbox, BlackboxSql, Keeper, Session 0 pipe, PDH counters
Outcome: the three services install, start, run under the right identity and stop cleanly on a Windows runner; the Keeper pipe is proven to be served from Session 0.
- Scope (in): `sc.exe create/start/query/stop/delete` for `UnravisBlackbox` (LocalSystem) and `UnravisBlackboxSql` (`NT SERVICE\UnravisBlackboxSql` with `sc.exe sidtype ... unrestricted`); Keeper service pipe test where the client verifies `GetNamedPipeServerProcessId` and `GetNamedPipeServerSessionId == 0`; run Blackbox 35 s (2+ ticks of 15 s) and assert valid JSON lines, working set under 50 MB, clean stop.
- Out of scope: sandbox exe (BL-368); service changes in installer (BL-404); fleet or multi-machine.
- Depends on: P4; BL-453 (installer lays services down), BL-334, BL-407.
- Risks / unknowns: report 09 names the service `UnravisKeyKeeper` in one place and the pipe `UnravisKeeper` elsewhere: confirm real names from the repo. The `UnravisBlackboxSql` service exists in the report; check it is in the current code. The 50 MB working-set number is `[INFERRED]` and DECISIONS budgets say tray-idle 35 MB for the shell; no Blackbox budget is given there. Azure PDH availability is documented for VMs, hosted runner counters need a real try. Server 2016 cannot be tested on hosted runners (report 09 risk 4).
- Acceptance:
  - CI: install, start, query, stop and delete of both Blackbox services and the Keeper; the Session 0 check returns 0. (CI)
  - CI: after 35 s the recording has at least 2 valid ticks. (CI)
  - elevated/UAC: on nachi's PC, install the real services through the real setup and see them running (admin prompt). (elevated/UAC)
  - live PC (nachi): the real Blackbox recording opens read-only in the app. (live PC (nachi))
- Size: M. Phase: P4.
- Needs owner action? Yes for the elevated step on a real PC. A Server 2016 VM check is a separate owner task (BL-397).

## BL-455 OTA CI: N-1 to N, bad signature, 3-bad-start rollback on a localhost feed
Outcome: the real updater code is exercised end to end in CI against a localhost feed signed with a throw-away key.
- Scope (in): ephemeral ed25519 keypair generated in CI, compiled in only under `-tags=ci_test_keys`; install N-1 (previous GitHub release asset); serve a tampered manifest on `http://127.0.0.1:<port>/feed.json` and assert rejection; serve a valid manifest and run `--check-update` and the idle apply; assert N is active and the SQLite case data opens; run N three times with `--test-crash-on-start` (test-tag only) and assert rollback to N-1 and service restart. Add to or share with `verify-release.yml`. Add a release-build check that `ci_test_keys` and `--test-crash-on-start` are absent.
- Out of scope: the updater itself (BL-149 to BL-169, built upstream); the desktop-aware hand-off (BL-406); the real-feed verify (BL-153, BL-408); Tauri's updater plugin (rejected in DECISIONS).
- Depends on: P4; BL-453, BL-406, BL-408; existing BL-150 U5 (rollback built, not verified on a real install).
- Risks / unknowns: report 09 notes this uses real `--check-update` and tags that may not exist yet (`--test-crash-on-start` is new; BL-152 built `--check-update`, not run on a real feed). BL-150 says the whole rollback path is NOT verified on a real install; CI success here does not replace the real scratch-PC rollback. Backlog says setup run can still need an Install click and UAC (report 10 item 4). The "silent" path is BL-168/BL-169, also unticked. Signing passphrase never goes in CI.
- Acceptance:
  - CI: the 5-step sequence above passes on `windows-2025`; a test-key build cannot validate the production feed. (CI)
  - CI: release artifact scan finds no test key or crash flag. (CI)
  - elevated/UAC: on a scratch PC, force a bad update, see 3 failed starts and the real rollback including the UAC step. (elevated/UAC)
  - live PC (nachi): after rollback, nachi's real case data opens read-only. (live PC (nachi))
- Size: L (split: bad-signature and upgrade first, rollback second). Phase: P4.
- Needs owner action? Yes: scratch PC rollback (UAC). No production keys or passphrase are used by this item.

## BL-456 Release evidence: SBOM, attestation, release-only ARM runner
Outcome: each tagged release carries a CycloneDX SBOM and, if the repo is public, a verifiable build attestation; ARM Windows is built on tags only.
- Scope (in): `anchore/syft` (v1.57.2) CycloneDX SBOM attached to the draft release; `actions/attest@v4` for binaries and SBOM if public (report: private needs Enterprise Cloud). `windows-11-arm` job on release tags only. Keep the offline ed25519 signing path as the trust root.
- Out of scope: Authenticode (BL-400 to BL-403); release manager changes (BL-417); reproducible-build compare (BL-195, BL-336).
- Depends on: P4; BL-440 (public or private), BL-336 (SBOM and reproducible builds, overlap), BL-417.
- Risks / unknowns: report 10 says check `gh attestation verify --format json` field names against a real rc (BL-150 U1b notes the same, unproven). Immutable releases vs the mutable signed `update-feed` is a flagged compatibility question (report 10). Whether Unravis ships ARM at all is not decided in DECISIONS (it says Windows x64).
- Acceptance:
  - CI: tag build attaches SBOM; `gh attestation verify` passes (public) or the step is skipped with a recorded reason (private). (CI)
  - CI: the ARM job runs only on tags. (CI)
  - live PC (nachi): `gh attestation verify` run on nachi's PC against a real rc file. (live PC (nachi))
- Size: S. Phase: P4.
- Needs owner action? Only the public/private decision (BL-440); no signing key use.

## BL-457 Live-PC acceptance runner: one read-only script on nachi's PC for every gate
Outcome: one script runs the "live PC (nachi)" acceptances listed above on a real install with a real investigation, read-only, and prints a pass/fail table.
- Scope (in): `tools/dev/Test-LivePc.ps1` (name TBD) with named checks that map to items here: build file names (BL-442), `go test ./...` baseline vs allowlist (BL-441), MCP 10 tools on the real dashboard (BL-444), schema validation of a real response (BL-445), UI suite against the real Go server (BL-446), desktop smoke on the real install (BL-447), data readable after upgrade/rollback (BL-453, BL-455). Evidence file saved (versions, hardware, timings) so P0/P5 budgets (DECISIONS section 9) have real numbers. Never writes, never deletes, never prints secrets; refuses to run elevated unless a check says so.
- Out of scope: any collector or agent (needs the owner's go per BACKLOG rule); Server 2016/10/11 matrix (BL-397, BL-408).
- Depends on: P0 (starts as soon as BL-300 baseline exists), and grows with each item above; BL-441, BL-446.
- Risks / unknowns: the "real investigation" case name is not in the reports (an existing live case is mentioned in BACKLOG: Brain Sandbox). The script must not leak real identifiers into logs that get committed. pwsh 7 vs Windows PowerShell 5.1 differences (BACKLOG notes an unverified pwsh 7 case).
- Acceptance:
  - live PC (nachi): script run on nachi's PC prints a green table for every check already built; failures show which item. (live PC (nachi))
  - live PC (nachi): the script run twice leaves case data byte-identical (hash compare). (live PC (nachi))
  - CI: a dry-run mode with fakes runs in Windows CI so the script itself is tested. (CI)
- Size: M. Phase: Parallel A (starts P0, extended each phase).
- Needs owner action? Owner runs it on nachi's PC. No secrets or UAC unless an elevated check is added.

---

## Conflicts with other areas
1. **BL-335 (02 security, supply-chain CI gates) vs BL-449 and BL-456.** Same scanners (`cargo-deny`, `govulncheck`, `osv-scanner`, `trufflehog`, SBOM). Pick one owner at spec; suggest BL-449 holds the workflow, BL-335 holds the policy (no Trivy, ignore-scripts, release-age cooldown).
2. **BL-336 (SBOM, reproducible builds) vs BL-456.** Overlap on SBOM. BL-195 (reproducible check) is outside both.
3. **BL-383 (05 CLI, Windows shell test harness) and BL-393 (parity CI) vs BL-442 and BL-444.** Both need a Windows runner job and shared setup. One Windows workflow file should host them.
4. **BL-408 and BL-153 (verify-release) vs BL-453, BL-454, BL-455.** `verify-release.yml` tests a published release; these items test unreleased builds with a test key. Same scripts should be reused, not forked.
5. **BL-416 (CI size and footprint gate) vs BL-442.** Footprint thresholds (DECISIONS section 9) are in BL-416; BL-442 only records time.
6. **BL-375 and BL-354 (hostile fixture suites) vs BL-450 (fuzz).** Different tests, same CI minutes; fuzz is nightly only.
7. **Contracts: report 09 (JSON Schema + goldens) vs report 10 (OpenAPI 3.1.2 for HTTP).** BL-445 covers goldens and IPC; OpenAPI generation belongs to a separate item in the shell/API area. DECISIONS section 11 lists both. Needs one spec decision so we do not have two schema sources.
8. **BL-306, BL-314 vs BL-450 and BL-443.** Fuzzable Rust `ipc_frame` crate and `<R: Runtime>` generics change the shell layout (report 10 wants one Cargo package until a second crate exists).
9. **BL-317 (sidecar naming) vs BL-442.** Report 09 sketch builds `unravis-x86_64-pc-windows-msvc.exe`, while DECISIONS section 2 leaves "same `unravis.exe` or split `unravis-core.exe`" open until the BL-305 spike.
10. **BL-325/BL-311 (strip `WEBVIEW2_*` env at startup) vs BL-447.** The test fixture must set `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS`; production strips it. Test profile and a release check are both needed.
11. **BL-332 (no loopback in desktop mode) vs report 09 fixtures.** Report 09 assumes the UI at `http://127.0.0.1` and a launch link in browser mode only; the Tauri mode token handoff is not described by either report.
12. **BL-197 (branch rules on `master`) vs BL-440.** Same GitHub setting; Rulesets need GitHub Pro if private.
13. **Branch lineage:** report 10 says signed updater, Lite and `verify-release.yml` exist only on `origin/master`, not on the feature branch where `ci.yml` was inspected. CI items must be built on the branch that has them.

## Questions for spec stage
1. Public or private repo? (decides CodeQL, attest, Rulesets, runner size, cost). Who tells us, and when?
2. Do we keep `windows-2025` only, or also `windows-2022`? Release workflow smoke uses 2022 and 2025 today (report 10); report 09 lists only 2025 for PRs.
3. CDP smoke or `@wdio/tauri-service` embedded as the first choice? Report 09 picks CDP at medium confidence. Is the 2% flake cut-off the owner's rule?
4. What is the coverage number: 75, 80 or 85% on changed lines? Per language or combined?
5. Can the Windows-only Go packages (about 19 known failures on Linux) be merged into one coverage view?
6. Does setup already have `--silent`, `--scope`, `--install-dir`, `--recover`, `--check-update`? Report 09 says to "expose" them. Which exist today and which are new?
7. Real names: service `UnravisKeyKeeper` vs pipe `UnravisKeeper`; does `UnravisBlackboxSql` exist in code? What is the Blackbox working-set budget (50 MB is only the report's guess)?
8. Real-data test case: which case does nachi want as the standard read-only live case, and how is it scrubbed so no identifiers are committed?
9. Where do golden fixtures live and who scrubs them (BL-445)? OpenAPI vs JSON Schema as the HTTP source of truth?
10. Test-only code (`ci_test_keys`, `--test-crash-on-start`, `e2e` feature, remote debugging flag): one shared "test build" profile and one release-time scan? Who owns the scan?
11. Nightly or weekly for fuzz and installer/OTA jobs? Cost: about $20.46 per month daily vs $15.00 weekly gross (report 09; free if public).
12. Is `windows-11-arm` in scope? DECISIONS says Windows x64 first; report 09 adds ARM on release tags only.
13. Server 2016 and Windows 10 checks cannot run on hosted runners. Does the owner keep a local VM, and is that checked by BL-457 or by BL-397?
14. `govulncheck` version (v1.1.4 vs v1.10.0) and when to take Go 1.27.4 (report 10 first maintenance item).
15. AGENTS.md: add a short one that imports into CLAUDE.md, or keep CLAUDE.md only? Report 10 recommends a small root file and ADRs under `docs/adr/` linked to a verification command.
