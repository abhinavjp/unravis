# Spec 08: testing, CI and repo upkeep (BL-440 to BL-457)

Status: spec, 2026-11-21. Stage 2 of 4. Inputs: groom 08, `DECISIONS-draft.md` (sections 9 to 11), groom and spec files 01 to 07 (conflict and question sections), research 09 and 10, and the code under `/home/claude/unravis` as checked today.

Rules from CLAUDE.md apply: done means it works on the real data path. CI proves wiring on synthetic or recorded data. Every item that touches data keeps a "live PC (nachi)" acceptance, read-only.

## 0. Where the code differs from the reports (checked today)

| Report or groom says | The repo says | Effect |
|---|---|---|
| CI has no Linux job and no lint. | Right. `ci.yml` has two Windows jobs on `windows-2022` (`build`, `powershell-syntax`). `build` runs `go vet`, `go test -p 2 -timeout 25m ./...`, web check, web test, web build, gofmt on changed files, screenshots. | BL-441 adds a Linux job. The Windows `go test` stays and must be fully green there. |
| Signed updater, Lite, `verify-release.yml` exist only on `origin/master` (report 10). | All of them are in this checkout: `internal/update`, `cmd/unravis-lite`, `verify-release.yml`, `mirror-feed.yml`, `tools/release/*`, `trusted_keys.txt` with 2 rc and 2 stable keys. | Groom conflict 13 is closed. BL-440 still records the lineage with a script. |
| "19 known Windows-only `internal/server` failures on Linux". | Measured today on Linux, Go 1.27.3: 9 failing tests in `internal/server` (`TestBlackboxInstall*`, `TestBlackboxRevealKeyIsRateLimited`, `TestBlackboxThisPCActions`, `TestBlackboxPackDownloadNameIsQuotedSafely`, `TestBlackboxRemovedFromThisPCWhileACaseReadsIt`, `TestScn1L8RevealKeyKeeperAnswers`, `TestScn1L8StoppedBlackboxIsPartlyReady`). The number 19 is not reproducible. | The allowlist is generated, not typed (D8). Specs 01, 04, 05 that quote "19" must say "the file `docs/desktop/baseline-go-tests.txt`". |
| Vitest, `npm run test:unit`. | `web/package.json` has `test` = `node --test tests/*.test.cjs` (247 files) and `check` = `svelte-check`. No Vitest. | No Vitest is added (D12). |
| Playwright UI suite in `web-and-e2e-linux`. | The Playwright suite is `tools/e2e` (63 files in `specs/`), runs only by hand against `unravis-demo.exe` through `support/launcher.ts` (`--demo --no-browser --port 0`, `UNRAVIS_EXE`, a `D:\` default path). `tools/screens` is a second Playwright project, run in `ci.yml` on Windows. | BL-446 makes `tools/e2e` run in CI for the first time, on Linux. `cmd/audit-explorer` builds on Linux today (3.9 s). |
| `setup` flags `--silent --scope=user|machine --install-dir`. | Real flags: `--silent --config setup.json`, `--uninstall`, `--repair`, `--upgrade`, `--allow-downgrade`, `--expect-sha256`, `--expect-version`, `--close-app`, `--recover`, `--no-restart`, `--no-browser`, `--verify <folder>`, `--version`. Scope and install dir are keys in `setup.json`. | BL-453 uses `setup.json`; it adds no new flags. |
| Service `UnravisKeyKeeper` / pipe `UnravisKeeper`. | `keeper.ServiceName = "UnravisKeeper"`; `blackbox.ServiceName = "UnravisBlackbox"`; `blackbox.SQLServiceName = "UnravisBlackboxSql"` with account `NT SERVICE\UnravisBlackboxSql`; `setup.KeeperAccount = NT SERVICE\UnravisKeeper`. | Use the constants, never string literals, in new tests. |
| `go-winio` for pipe tests. | `go-winio` is not in `go.mod`. `internal/keeper/pipe_windows.go` has its own overlapped listener on `x/sys/windows`. Specs 01 (go-winio) and 02 (`internal/pipesec`, own) differ. | Tests here call whatever `pipesec`/`pipesrv` expose; the helper below does not import go-winio. |
| Packages `internal/win`, `internal/mcp`, `internal/keeper`. | `internal/win` does not exist. Real: `internal/keeper`, `internal/instance`, `internal/mcp`, `cmd/audit-explorer` (`stdio_windows_test.go`), new `internal/pipesec`, `internal/pipesrv`, `internal/locator` (specs 01, 02). | BL-444 paths below are real. |
| `--check-update` needs to be exposed. | Exists (`cmd/audit-explorer/checkupdate.go`, tests `TestCheckUpdateFlagStandsAlone`). `--test-crash-on-start` does not exist (spec 06 BL-406 adds it). | OTA CI waits for 06 for the crash flag. |
| `release.yml`/`Invoke-Smoke.ps1` have no installer test. | `release.yml` has a `smoke` job; `tools/release/Invoke-Smoke.ps1` does silent install, service check, MCP/HTTP check, uninstall. `Invoke-VerifyRelease.ps1` tests a published tag. | BL-453/455 extend these scripts (spec 06 owns their content). Area 08 adds only plumbing (D10). |
| Repo public or private unknown. | `update.DefaultFeedBase` is an unauthenticated `github.com/abhinavjp/unravis/releases/download/update-feed` URL. That works for end users only if the repo or the mirror is public. | BL-440 reads `gh repo view --json visibility` and decides (D1). |

## 1. Purpose and boundaries

Owns:
- All GitHub Actions layout for the desktop work: workflow files, job names, the shared Windows setup action, runner choice, caches, path filters, required-check names.
- The gates: lint, race tests, Go failure baseline, diff coverage, mutation on Rust IPC, fuzz, accessibility and visual checks.
- Test harnesses that other areas reuse: standard-user runner helper, test pipe names, test feed, WebView2 CDP fixture, Rust `tauri::test` harness, golden fixtures and the `contracts/` folder.
- Release-time scan wiring for test-only code (the script is area 02's).
- `docs/desktop/ci-baseline.md`, `docs/adr/` skeleton, Dependabot.
- The live-PC runner `tools/dev/Test-LivePc.ps1`.

Does not own:
- Scanner policy and `supply-chain.yml` (area 02, BL-335). Area 08 tests that workflow (BL-449).
- SBOM and reproducible build (area 02 BL-336, BL-195). Attestation step stays in `release.yml` as today.
- Budget thresholds and size gate (area 07 BL-416; area 06 `Check-Size.ps1`). Area 08 only runs them.
- The code under test: pipe server (01, 02), MCP (03), sandbox (04, own workflow `code-sandbox.yml`), CLI (05, job `cli-windows`), installer and OTA content (06).
- Branch protection settings (BL-197). Area 08 lists the required check names (section 4).
- Signing keys. No production key or passphrase is ever used in CI.

## 2. Resolved decisions

| # | Question or conflict | Decision | Reason |
|---|---|---|---|
| D1 | Public or private repo (Q1, conflict 12). | Unknown from code. BL-440 script prints `visibility`. Default plan: PRIVATE-safe: no required job depends on CodeQL, Rulesets, or new attestation. When the script says PUBLIC, the owner-free switch is repo variable `REPO_PUBLIC=true`, set by `Get-CiBaseline.ps1 -SetVariable`; jobs that use public-only features read it (`if: vars.REPO_PUBLIC == 'true'`). No money decision is asked; if private, minutes are paid from the free quota and the weekly schedule keeps it low (D9). | Spec 02 (2.10) also assumes private. Nothing waits. |
| D2 | Which runners (Q2). | `ci.yml` stays `windows-2022` (screenshot reference renderer, comment at top of file). New desktop jobs use `windows-2025` in `desktop-windows.yml`. The Go jobs of areas 01 and 05 (`desktop-sidecar`, `cli-windows`) stay in `ci.yml` and get a matrix `os: [windows-2022, windows-2025]` for the pipe and CLI tests only. `release.yml` smoke keeps 2022 and 2025 (spec 06). Spec 01 4.9 ("moves it") is answered here: `desktop-shell` leaves `ci.yml` and becomes job `shell` in `desktop-windows.yml`. | Keeps the screenshot baseline stable; covers both Server images; one new file for new work. |
| D3 | CDP smoke or `@wdio/tauri-service` first (Q3). | CDP via `chromium.connectOverCDP` first. Flake rule 2% over 20 runs is adopted as the rule. Switch rule written in `docs/desktop/e2e-switch-rule.md`: 2 failed soaks in a row, or attach hang more than once in 20, means switch. Decided by spike BL-447 (soak result). Default until then: CDP. | Report 09 pick, medium confidence. The 2% is a gate, the soak measures it. |
| D4 | De-elevation for CDP (BL-447). | Runner token is admin with UAC off, so there is no unelevated twin and `gsudo -d` cannot work (inferred: UAC off means no split token). Default: run only the shell as a local standard user through `tools/ci/Invoke-AsStandardUser.ps1` (scheduled task, `RunLevel Limited`). Playwright itself runs as the runner user and attaches to `127.0.0.1:<port>`. Decided by spike BL-447: if WebView2 will not start in that task (no interactive desktop), fall back to `@wdio/tauri-service` embedded (D3). | Same helper as installer tests; no extra tool. |
| D5 | Coverage number and scope (Q4, Q5). | 80% of changed executable lines, per language, not combined. Floor for a language with fewer than 10 changed lines: pass. Go: merge the Linux and Windows profiles (`tools/ci/Merge-GoCover.ps1`, per line max count) so Windows-only files count. Rust: `cargo-llvm-cov` on the `windows-2025` job. Web: `node --test --experimental-test-coverage` on `web/src/lib/**/*.ts` only; `.svelte` files are excluded (no unit harness for them). Exclusions in `tools/ci/cover-exclude.txt`. | One number, per-language so a strong language cannot hide a weak one. |
| D6 | Contract source of truth (conflict 7, Q9). | JSON Schema plus golden JSON, in `contracts/`. No OpenAPI generation now. The OpenAPI line in `DECISIONS-draft.md` section 11 and `contracts/http/v1/openapi.yaml` is NOT built. A schema file is written by hand next to a captured golden; Go DTO drift is caught by the golden test. If drift hits twice, open an ADR to adopt OpenAPI. Decided by evidence after P3. | One schema source. Handlers stay Go; the Go side already has DTOs. `oasdiff` and generators add tools for no gate we lack. |
| D7 | Rust crate layout for fuzz and mutation (conflict 8). | Cargo workspace `desktop/Cargo.toml` with two members: `desktop/src-tauri` (shell) and `desktop/ipc-core` (pure Rust, no `tauri`, no Windows API, `#![forbid(unsafe_code)]`). `Cargo.lock` lives in `desktop/`. A second crate now exists, so DECISIONS "one package until a second crate exists" holds. `ipc-core` holds: `frame` (HTTP/1.1 response parse for the pipe relay with size caps), `locator` (parse of the locator JSON), `policy` (pure decision tables: elevation decision, command allow check by name). `src-tauri` calls it. | Tauri does not build on Linux without GTK libs and spec 01 D20 makes Linux shell builds a non-goal. Fuzz, mutation and fast tests need a crate that builds on `ubuntu-latest`. Cost: spec 01 modules `locator.rs`, `elevation.rs`, `pipe_client.rs` become thin callers. |
| D8 | Linux baseline of `internal/server` failures. | File `docs/desktop/baseline-go-tests.txt`, one line `pkg.TestName`, sorted. Today: 9 entries (section 0). Created by BL-441 with `tools/ci/Compare-GoFailures.ps1 -Write` (if BL-300 already wrote one in the same format, BL-441 only checks it). CI fails on any failure not in the file. An entry that now passes prints a notice and the file is trimmed by hand (no failure). Applies to all packages, not only `internal/server`. | The count 19 is wrong; a list is exact. |
| D9 | Schedule for heavy jobs (Q11). | Fuzz: nightly, Linux. Installer, services, OTA: run on PR only when paths change (`cmd/unravis-setup/**`, `internal/setup/**`, `internal/update/**`, `internal/keeper/**`, `internal/blackbox/**`, `cmd/unravis-keeper/**`, `cmd/unravis-blackbox/**`, `desktop/**`, `build.ps1`, `tools/release/**`, `tools/ci/**`, `.github/workflows/desktop-*.yml`) plus weekly Monday 03:00 UTC and `workflow_dispatch`. Rust shell build and smoke: every PR touching `desktop/**`, `web/**`, Go, workflows. | Windows minutes cost double; path filters cost nothing. |
| D10 | Installer, services, OTA scripts: reuse (conflict 4). | One set of scripts: `tools/release/Invoke-Smoke.ps1` and `Invoke-VerifyRelease.ps1` (spec 06 edits them). Area 08 adds: `tools/ci/New-TestUser.ps1`, `tools/ci/Invoke-AsStandardUser.ps1`, `tools/ci/testfeed/main.go` (localhost feed), workflow jobs that call them. Area 08 forks nothing. | Avoids two smoke scripts. |
| D11 | One test-build switch and one release scan (Q10). | Go build tag `e2e`, Rust Cargo feature `e2e`. Nothing else. Spec 02 `citest` becomes `e2e` (`pipesec/testallow_e2e.go`). Spec 04 `codedev` stays for the sandbox dev build (own workflow) but is also scanned. `tools/security/scan-release.ps1` (area 02) is the only scanner; spec 06 `Assert-NoTestFlags.ps1` becomes a call into it, no second script. The forbidden list is one file `tools/security/forbidden-strings.txt`; each area appends its strings. Area 08 appends the ones in section 4. The scan runs in `desktop-windows.yml` job `release-scan` on a `-Release` build of every PR touching Go or `desktop/`. | One tag, one list, one script. |
| D12 | Unit test runner for web. | Keep `node --test` (247 files). No Vitest. DECISIONS "Vitest" is read as "the web unit tests". | Adding a runner for no gain. |
| D13 | OTA test keys (BL-455). | Tag `e2e` file `internal/update/testkeys_e2e.go`: reads env `UNRAVIS_TEST_TRUSTED_KEY` (64 hex, adds one trusted key tagged `rc`) and `UNRAVIS_TEST_FEED_BASE` (must parse as `http://127.0.0.1:<port>`; anything else is ignored). Non-`e2e` build: `testkeys_other.go` empty. Production `trusted_keys.txt` is untouched. The name `ci_test_keys` is not used. | One tag (D11). Loopback-only so a test build cannot be pointed at a real feed. |
| D14 | `windows-11-arm` (Q12). | Dropped from this area. DECISIONS says Windows x64; no ARM build exists. BL-456 loses its ARM part. Re-open only with an owner go. | No product to test. |
| D15 | Server 2016 and Windows 10 (Q13). | Hosted runners cannot do it. Those acceptances are labelled "not verified" and belong to BL-397. BL-457 does not test them. It prints "OS build X, class Y" so a run on such a PC is recorded when the owner has one. | No box assumed (spec 02 2.18). |
| D16 | govulncheck version and Go 1.27.4 (Q14). | Take the newest `govulncheck` at build time, never below v1.10.0 (report 10, 2026-11-17). Go: `go.mod` says 1.27.3. First PR of BL-441 bumps `go` to 1.27.4 if `go.dev/dl` lists it that day; the PR runs the full Windows and Linux gates. | Security patch first, then gates. |
| D17 | AGENTS.md (Q15). | No `AGENTS.md`. `CLAUDE.md` stays the single agent file. Add `docs/adr/README.md` and `docs/adr/0000-template.md` (fields: status, context, decision, verify command). `CLAUDE.md` gets the one line to `https://v2.tauri.app/llms.txt` when `desktop/` lands (spec 01 BL-306 does it). | Two files drift. |
| D18 | Live case and scrub rule (Q8). | `Test-LivePc.ps1 -Case <name>` or env `UNRAVIS_LIVE_CASE`; with neither, it lists cases read-only and uses the only one, else stops with the list. Case names never go to the evidence file: only a case id hash (first 8 hex of SHA-256). Golden recorder scrub: every string value that is not in the schema's `enum` is replaced by `value-<n>` in order of first appearance; numbers, booleans, nulls stay; timestamps move to a fixed base but keep deltas. Output is reviewed by a diff against the schema before commit. Owner does one read of the first fixture set. | Fixtures keep shape, never identifiers. |
| D19 | Who tests Windows Server 2016 pipe ACLs (Q13 spill). | Nobody on CI. See D15. | Same. |
| D20 | Windows-only code in the Linux coverage view (Q5). | Not merged in one run: two jobs make two profiles, `Merge-GoCover.ps1` merges, gate reads the merged file in a final job `cover-gate` that downloads both artifacts. | Done in D5. |
| D21 | Loopback vs pipe in e2e (conflict 11). | The fixture has two modes, chosen by the shell build. P2 (loopback, spec 01 D17): shell loads `http://127.0.0.1:<port>/launch?c=...`; the page URL is read from CDP `Page.getNavigationHistory`; no token handling in the test. P3 (bundled UI, pipe relay): the page is `http://tauri.localhost/`; the test never sees a token. Specs use relative URLs (`page.goto('/')`) so both pass. Env `UNRAVIS_E2E_MODE=loopback|bundled` picks only the readiness check. | Both reports' assumption fails after P2; relative navigation survives both. |
| D22 | Test arguments to the sidecar. | `e2e` feature of the shell reads env `UNRAVIS_E2E_SIDECAR_ARGS` (for example `--demo`) and appends it to the spawn line. Not read in release builds. Spec 01 D2 `UNRAVIS_SIDECAR` (path override) stays, same feature. | The fixture needs demo data in the sidecar without a new flag. |
| D23 | Branch lineage (conflict 13). | Closed: all workflows are in this checkout. BL-440 records `git log --oneline origin/master..HEAD -- .github` so a diverged branch is visible. | Cheap guard. |

## 3. Design

### 3.1 Workflow files and jobs

```
.github/
  dependabot.yml
  actions/win-setup/action.yml        composite: go, node, rust-cache, tools (runs AFTER checkout)
  workflows/
    ci.yml                 (existing, windows-2022)  + job linux-gates, job rust-linux
    desktop-windows.yml    (new, windows-2025)       jobs: shell, release-scan, e2e-smoke, installer, services, cover-gate
    desktop-ota.yml        (new, windows-2025)       job: ota   (path-filtered, weekly, dispatch)
    nightly.yml            (new, ubuntu-latest)      job: fuzz
    supply-chain.yml       (area 02)
    code-sandbox.yml       (area 04)
    release.yml, verify-release.yml, mirror-feed.yml  (existing; areas 06, 07 edit)
```

Common rules (apply to every new job, already the style of `ci.yml`): `permissions: contents: read`; `concurrency` group `<workflow>-<ref>` with `cancel-in-progress` on PRs; every third-party action pinned to a full SHA with a tag comment; `timeout-minutes` set. `.github/actions/win-setup` is a local action, so it needs a checkout first. The Defender exclusion step is therefore an inline first step before checkout:

```powershell
Add-MpPreference -ExclusionPath $env:GITHUB_WORKSPACE, "$env:USERPROFILE\.cargo", "$env:USERPROFILE\.rustup", "$env:LOCALAPPDATA\go-build", "$env:LOCALAPPDATA\npm-cache"
```

(If `Add-MpPreference` is blocked on a runner image, the step prints a warning and continues; timing then shows it.)

Path filters: every Windows job lists `paths:` that include `web/**`, `**/*.go`, `go.mod`, `go.sum`, `desktop/**`, `contracts/**`, `build.ps1`, `.github/**`, `tools/ci/**`. A test (`tools/ci/Test-PathFilters.ps1`, run in `linux-gates`) fails if a directory that the build reads (list in the script: `web`, `cmd`, `internal`, `desktop`, `contracts`, `sql`, `assets.go`) is missing from the filter of `shell`.

### 3.2 Job: `linux-gates` (ci.yml, ubuntu-latest, BL-441)

Steps: checkout (fetch-depth 0), setup-go (`go-version-file: go.mod`), setup-node 22 with npm cache, `go vet ./...`, golangci-lint (pinned action SHA; binary version pinned in the step, minimum 2.5), `go test -race -count=1 -timeout 25m -json ./... > go-test.json`, `pwsh tools/ci/Compare-GoFailures.ps1 -Json go-test.json -Baseline docs/desktop/baseline-go-tests.txt`, `npm ci && npm run check && npm test && npm run build` in `web/`, `pwsh tools/ci/Test-PathFilters.ps1`, `node tools/security/check-action-pins.mjs` (area 02, runs if present). `go test -race` needs cgo on the runner (gcc is present on ubuntu-latest). Product builds keep `CGO_ENABLED=0`; the race step sets `CGO_ENABLED=1` for that step only.

`.golangci.yml`:

```yaml
version: "2"
run: { timeout: 10m }
linters:
  default: none
  enable: [errcheck, govet, staticcheck, gosec, rowserrcheck, bodyclose, nilerr]
issues:
  # Only code a PR adds or changes is judged. Old code is a separate pass.
  new-from-merge-base: origin/master
```

On `push` to `master` the step passes `--new-from-rev=${{ github.event.before }}` instead. `gosec` is allowed to be suppressed per line with `//nolint:gosec // <reason>` only; a bare `//nolint` fails (`nolintlint` enabled with `require-explanation: true, require-specific: true`).

`tools/ci/Compare-GoFailures.ps1` (pwsh, runs on Linux and Windows):
- Params: `-Json <file>`, `-Baseline <file>`, `-Write` (write the baseline from the json), `-Out <summary.md>`.
- Reads `go test -json` lines; a failure is `Action=fail` with `Test` set (or a package-level `fail` with no test, named `pkg.<package>` and always treated as new). A build failure of a package is a new failure.
- Exit 0 when new failures is empty. Exit 1 prints `NEW FAILURE <pkg.Test>` per line. Stale baseline lines print `::notice::`.
- Writes the table to `$GITHUB_STEP_SUMMARY`.

### 3.3 Job: `rust-linux` (ci.yml, ubuntu-latest, BL-452, BL-450 corpus)

Runs only when `desktop/ipc-core/**` or `contracts/**` changed. Steps: `cargo test -p ipc-core --locked`, `cargo clippy -p ipc-core -- -D warnings`, `cargo llvm-cov -p ipc-core --lcov --output-path rust-linux.lcov` (artifact), `git diff origin/${BASE}...HEAD > pr.diff`, `cargo mutants -p ipc-core --in-diff pr.diff --no-shuffle` (exit 0 when diff has no Rust change; survivors exit non-zero).

### 3.4 Job: `shell` (desktop-windows.yml, windows-2025, BL-442, 443)

1. Defender exclusions (inline), checkout, `win-setup` (go from `go.mod`, node 22, Rust toolchain from `desktop/rust-toolchain.toml`, `Swatinem/rust-cache` with `workspaces: desktop`).
2. `npm ci && npm run build` in `web/`, then `./build.ps1 -SkipWeb -SkipTests -Version ci-<sha7>` (the same call `ci.yml` uses). The sidecar is whatever `build.ps1` makes (`dist\unravis.exe`); the job does not rename it (spec 01 D2 launches `unravis.exe` next to the shell, not a Tauri `externalBin`, so the report's `binaries\unravis-x86_64-pc-windows-msvc.exe` is not used).
3. `cargo build --locked --manifest-path desktop/Cargo.toml -p unravis-desktop` (name from spec 01 BL-306; Windows-only crate), `cargo clippy --locked -- -D warnings`, `cargo test --locked`, `cargo llvm-cov --lcov` (artifact `rust-win.lcov`).
4. `go test -count=1 -coverprofile=go-win.cover -p 2 ./...` (this duplicates `ci.yml build`; it is run here only for the coverage profile and only when the path filter hits; `ci.yml build` stays the correctness gate).
5. Timing: step `tools/ci/Write-BuildTiming.ps1` writes cold or warm (read from `steps.rust-cache.outputs.cache-hit`) minutes per phase into the job summary and into `build-timing.json` (artifact, 90 days).
6. Space case: matrix `path: [normal, with space]`; the `with space` leg checks out to `C:\work dir\unravis` (`actions/checkout` `path:`), and runs the same build.
7. Upload `dist/` file name list `files.txt` (names and sizes only) for the live check.

Windows Rust tests hit `STATUS_ENTRYPOINT_NOT_FOUND` only if a dependency imports `TaskDialogIndirect` without a Common Controls v6 manifest (tauri#13419). The shell has zero plugins (DECISIONS), so it may never trigger. Guard anyway (BL-443): `desktop/src-tauri/build.rs` embeds `desktop/src-tauri/test.manifest` for test binaries through `tauri_build::WindowsAttributes::app_manifest` only in `cfg(test)` style: `CARGO_CFG_TEST` is not set in build scripts, so use feature `testmanifest` enabled by the CI command `cargo test --features testmanifest`. Control test: throw-away branch step `tools/ci/Test-ManifestGuard.ps1` adds a dev-dependency that imports `TaskDialogIndirect` (`native-dialog` crate or a one-line `windows` call in a test) and checks that the test exe loads. This runs on `workflow_dispatch input guard=true` only, not every PR.

### 3.5 Job: `e2e-smoke` (desktop-windows.yml, BL-447)

Build with `--features e2e --config tauri.e2e.conf.json`. `tauri.e2e.conf.json` merges one extra capability file `capabilities/e2e.json` that lists the test commands. The `e2e` feature adds Rust commands (all `#[cfg(feature = "e2e")]`): `e2e_tray_hide`, `e2e_tray_show`, `e2e_quit`, `e2e_invoke_unlisted` (calls a command that is not in the manifest to prove denial; spec 02 line 240 asks for this hook), and reads `UNRAVIS_E2E_SIDECAR_ARGS`, `UNRAVIS_E2E_KEEP_WEBVIEW2_ENV` (spec 01 BL-311). Native tray menus and dialogs are not driven; only these hooks.

Fixture `tools/e2e/support/desktop-launcher.ts` (sibling of `launcher.ts`), project config `tools/e2e/playwright.desktop.config.ts` (`testDir: ./specs-desktop`, `workers: 1`, `retries: 0`, same `use` as `playwright.config.ts`):

```
per test run:
  root   = mkdtemp(<runner temp>/unravis-e2e-<pid>-<rand>)
  env    = { LOCALAPPDATA: root\local, APPDATA: root\roam, UNRAVIS_RUN_DIR: root\run,
             UNRAVIS_E2E_SIDECAR_ARGS: "--demo", UNRAVIS_E2E_KEEP_WEBVIEW2_ENV: "1",
             WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS: "--remote-debugging-port=<port> --remote-allow-origins=http://127.0.0.1:<port>",
             WEBVIEW2_USER_DATA_FOLDER: root\wv2 }
  port   = free port found by binding 127.0.0.1:0 and closing, per worker
  start  = Invoke-AsStandardUser.ps1 -Exe <unravis-desktop.exe> -Env <json> -Detach   (D4)
  ready  = poll http://127.0.0.1:<port>/json/version every 250 ms, max 30 s
  attach = chromium.connectOverCDP(`http://127.0.0.1:${port}`, { noDefaults: true })   // wait for window.__TAURI_INTERNALS__
  stop   = e2e_quit invoke; wait 5 s; taskkill /T /F /PID <pid>; remove root
```

Setting `LOCALAPPDATA` and `APPDATA` per run isolates data, webview2 profile and locator from the runner and from other tests, and from nachi's real data.

Smoke specs (10): `start`, `auth-bootstrap`, `case-open`, `one-chart`, `tray-hide-show`, `shutdown-clean`, `unlisted-command-denied`, `csp-violation-free-home`, `external-nav-blocked`, `second-launch-focuses-first`. Reused browser specs run unchanged through relative URLs (D21).

Soak: workflow `desktop-windows.yml` input `soak=20` repeats the 10 specs 20 times in one job and writes `soak.json` (pass count per spec). Flake = failed runs / 200. Gate: at most 2% (4 of 200). A failed soak writes the decision text into `docs/desktop/e2e-switch-rule.md` (by hand from the artifact).

Release check (in `release-scan`, section 4): the release exe has no `remote-debugging` string and no `e2e_` command names; `cargo tree -e features -p unravis-desktop` for the release build does not list `e2e`.

### 3.6 Job: `installer` and `services` (desktop-windows.yml, BL-453, 454)

Shared helpers (area 08):
- `tools/ci/New-TestUser.ps1 -Name <n>`: creates local user `ci-<random6>` in group `Users` with a 24-char random password generated in the script, printed through `::add-mask::` and returned as a `SecureString` object (never written to disk or env of the step).
- `tools/ci/Invoke-AsStandardUser.ps1 -User <n> -Password <SecureString> -Exe <path> [-Args] [-Env <json>] [-Detach] [-TimeoutSec 600]`: `Register-ScheduledTask` with principal `-LogonType Password -RunLevel Limited`, runs `cmd /c "<exe> <args> > <log> 2>&1"` in a folder where the user has Modify, `Start-ScheduledTask`, polls `Get-ScheduledTaskInfo`, returns `{ExitCode, LogPath, Pid}`, unregisters the task. Does not use `runas /trustlevel` (stays High) or `psexec -l` (Low).
- `tools/ci/pipeprobe/main.go` (Go, test tool, `GOOS=windows`): `pipeprobe <pipe path>` tries `CreateFile` on the pipe and prints `OK` or the Win32 error; exit 0 only on `ERROR_ACCESS_DENIED` when run with `-expect denied`.

`installer` job (needs `shell` artifacts): per-user leg, per-machine leg, upgrade leg, recover leg, as `Invoke-Smoke.ps1 -Mode UserSetup|MachineSetup|Upgrade|Recover` (spec 06 adds the desktop file to its checks). `setup.json` for each leg is generated by `tools/ci/New-SetupConfig.ps1 -Scope user|machine -InstallDir <dir>` using the real key names read from `internal/setup/options.go`. Checks: files in `%LOCALAPPDATA%\Unravis` of the test user, shortcuts, the Add/Remove entry under `HKCU` and nothing new under `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall` for the per-user leg (registry snapshot before and after), `--recover` after a `Stop-Process` at the `place` step (spec 06 `UNRAVIS_SETUP_PAUSE_AT=place`, tag `e2e`), `--uninstall` keeping data.

`services` job: after the per-machine install, `sc.exe query`, `sc.exe qc` (account check: `UnravisBlackbox` LocalSystem, `UnravisBlackboxSql` `NT SERVICE\UnravisBlackboxSql` with `sc.exe qsidtype` unrestricted, `UnravisKeeper` `NT SERVICE\UnravisKeeper`), start, 35 s wait, parse the recording (`blackbox` format files in the data dir; reader is `internal/blackbox`, called by a Go test helper `tools/ci/bbcheck/main.go -min-ticks 2`), working set of the service process read by `Get-Process` (limit from `docs/desktop/budgets.md` if area 07 sets one; else record only, no fail), stop, delete, and `Session 0` check for the Keeper pipe: `tools/ci/pipeprobe -session` calls `GetNamedPipeServerProcessId` and `ProcessIdToSessionId` and prints `0`. Blackbox working set: no number exists in DECISIONS; the report's 50 MB is only logged. A fail threshold is set after the first 10 runs as median x 1.5 (decided by measurement; default: no fail).

Gap kept honest: UAC and per-machine from a standard user are not testable in CI (UAC off). Those two acceptances stay "elevated/UAC" on a real PC.

### 3.7 Job: `ota` (desktop-ota.yml, BL-455)

Sequence, one job, script `tools/release/Invoke-Smoke.ps1 -Mode Ota` (spec 06) with these parts from area 08:
1. `go run ./tools/ci/testfeed -gen-key key.json` makes an ed25519 pair (using `internal/update` signing helpers, same as `tools/release/sigtool`; the private key stays in the runner temp folder and is deleted in an `always()` step).
2. Build N-1 as the previous GitHub release asset (download with `gh release download <prev tag>`; no build) and N from the PR with `-tags e2e` (build script switch `-E2E`, spec 06 BL-406 adds `--test-crash-on-start`).
3. `go run ./tools/ci/testfeed -serve 127.0.0.1:0 -dir feed -key key.json` serves `/update-manifest.json`, `.sig`, and the setup file; prints its port. Modes: `-mode good`, `-mode badsig` (flip one byte of `.sig`), `-mode expired`, `-mode replay` (old sequence). Feed file names follow `update.DefaultFeedBase` layout (read from `internal/update/fetch.go`, not retyped).
4. Install N-1 under the standard user with env `UNRAVIS_TEST_TRUSTED_KEY` and `UNRAVIS_TEST_FEED_BASE` (D13; N-1 is the previous release, so it has no `e2e` code). Problem: N-1 cannot read test env. Resolution: step 4 installs N-1 built from the same commit's parent with `-tags e2e` (CI builds it: `git worktree add ../prev HEAD~0 <prev tag>` is not possible for the old code lacking `testkeys_e2e.go`). So the default test is: previous release (real, production keys) is used only for the "N-1 to N shape" file layout check; the update itself is tested with N-1' = N code at version `0.0.0-ci.1` and N = same code at `0.0.0-ci.2`, both `-tags e2e`. Real N-1 to N with production keys is tested by `verify-release.yml` after a real release (spec 06). Decided: this split is final; it is the honest limit of unreleased builds.
5. Tampered feed (`badsig`, `expired`, `replay`): `unravis.exe --check-update` prints refusal and exit code from `checkupdate.go`; no download file appears in `%LOCALAPPDATA%\Unravis\updates`.
6. Good feed: `--check-update`, then the silent apply as in `docs/auto-updater/SILENT-UPDATE.md` (`unravis-setup --silent --upgrade --close-app --expect-sha256 --expect-version`); assert version, assert the demo case store opens (`GET /api/cases` through the locator).
7. Rollback: install with `--test-crash-on-start` build as N; start it 3 times; assert `unravis.exe --version` is N-1' and the old setup in `.previous` ran; service restart (`sc.exe query` RUNNING) for Blackbox/Keeper.
8. A production-feed guard: run N-1' built with `-tags e2e` but with `UNRAVIS_TEST_FEED_BASE=https://example.com`; assert the env is ignored and `--check-update` talks to the default base (checked by an `HTTPS_PROXY=http://127.0.0.1:9` that makes any outbound call fail with a log line naming the host).

Passphrase: never in CI. The test private key is ephemeral.

### 3.8 Job: `fuzz` (nightly.yml, ubuntu-latest, BL-450)

Go native fuzz, targets (files `*_fuzz_test.go`):
- `internal/update`: `FuzzParseManifest` (`ParseManifest`), `FuzzVerify` (`Verify` with random manifest and sig bytes, fixed `Trust`).
- `internal/mcp`: `FuzzBridgeServe` (random bytes into `Bridge.Serve` with a fake backend; must not panic, must not exceed the 1 MiB caps).
- `internal/locator` (spec 01): `FuzzLocatorParse`.
- `internal/pipesrv` (spec 01): `FuzzHTTPOverPipeRequestLine`, only if the package exposes a parser; else skipped.
Seeds are committed under `testdata/fuzz/<Target>/`. Every PR replay is automatic: `go test` runs seeds as unit tests. Nightly: for each target `go test -run '^$' -fuzz '^<Target>$' -fuzztime 2m ./<pkg>`; a found crasher is uploaded as an artifact and the nightly run fails; a human commits the file to `testdata/fuzz/` to make it permanent.
Rust: `cargo +nightly fuzz run frame_parse -- -max_total_time=120` and `locator_parse` in `desktop/ipc-core/fuzz/` (Linux only, because ASan does not link on MSVC). Needs D7.

### 3.9 Contracts and goldens (BL-445)

```
contracts/
  README.md                    scrub rule (D18), how to regenerate
  desktop/v1/
    envelope.schema.json       {v,id,cmd,ok,err,data} IPC envelope (shape owned by spec 01 BL-314; area 08 stores it)
    handshake.schema.json      locator and /api/desktop/state
    info.schema.json           GET /api/info
    version.schema.json        GET /api/version
    error.schema.json          error body incl. 401, 423 locked, 422 validation, request id
  fixtures/
    info.ok.json, version.ok.json, error.401.json, error.423.json, error.422.json,
    optional-omitted.json, explicit-null.json, empty-204.json, bigint-as-string.json
```

Go: package `internal/contracts` with only `_test.go` files (not linked into any binary). Library `github.com/santhosh-tekuri/jsonschema/v6` (test dependency in `go.mod`; it is pure Go; `govulncheck` and SBOM see it). Tests: `TestGoldensValidateAgainstSchemas`, `TestHandlerResponsesMatchGoldens` (starts `server` in demo mode with `httptest`, requests the same routes, validates), `TestRenamedFieldFailsGolden` (mutates a copy in memory, expects failure). Rust: `desktop/ipc-core/tests/contracts.rs` uses `jsonschema` crate and `serde` structs; `contracts_roundtrip_goldens`, `renamed_field_fails`. Web: `web/tests/contracts.test.cjs` loads the goldens and checks the TS adapter in `web/src/lib/host.ts` (spec 01 BL-314) types by a small `node --test` shape check (keys present).

Recorder: `tools/ci/Record-Golden.ps1 -Base <url or pipe locator> -Out contracts/fixtures` (PS 5.1 compatible; read-only GETs only: `/api/info`, `/api/version`, one bounded read named in a list inside the script). Applies the scrub rule, writes files, and runs the validation. It never prints or stores the session token (`applog.RegisterSecret` style: reads token from the locator, sends it, forgets it). Regeneration check: `Record-Golden.ps1 -Check` against the demo server must give no diff (CI), so goldens are reproducible.

Large integers (ids over 2^53) are decimal strings in the schema (`"type":"string","pattern":"^-?[0-9]+$"`).

### 3.10 Playwright on Linux (BL-446) and a11y/visual (BL-448)

Job `e2e-linux` (ci.yml, ubuntu-latest, runs when `web/**`, `tools/e2e/**`, Go changes): build `go build -o unravis-demo ./cmd/audit-explorer`, `npm ci` in `tools/e2e` and `web`, `npx playwright install --with-deps chromium`, `UNRAVIS_EXE=$PWD/unravis-demo npx playwright test --grep-invert @windows`. `launcher.ts` change: `DEFAULT_EXE` no longer has a `D:\` default; it reads `UNRAVIS_EXE` only and throws a clear message; it picks `unravis-demo.exe` suffix by platform (no change to its spawn args). Specs that need Windows features (Blackbox install, tray, DPAPI) get the title tag `@windows`. The first run lists failures; each failing spec is tagged `@windows` only with a one-line reason in `tools/e2e/LINUX-SKIPS.md` (a test `Test-LinuxSkips.ps1` checks every tag has a line). Unmarked failing specs block.

Project `chromium-mocked` (`tools/e2e/playwright.mocked.config.ts`, `testDir: ./specs-mocked`): `support/tauri-mock.ts` calls `page.addInitScript` to install `mockIPC(cb, { shouldMockEvents: true })` and `mockWindows('main')` from `@tauri-apps/api/mocks` (version taken from `web/package.json` once spec 01 adds it), waits for `window.__TAURI_INTERNALS__`. Because `page.route()` does not see `invoke()`, the mock `cb` maps command names to handlers that forward to the Go demo server over HTTP (`support/ipc-forward.ts`), so the mocked project uses real handler output. Specs: `host-adapter-flows`, `invoke-error-shows-error-state` (cb throws; UI shows its error state), `event-channel-progress`.

BL-448: `@axe-core/playwright` in `tools/e2e/specs-a11y/axe.spec.ts` over the screens listed in `web/src/App.svelte` (list read at test time; fails if a screen is missing from the test table), rule: zero `serious` and `critical`. Visual: `toHaveScreenshot` only in the Linux job inside the pinned Playwright container image (`mcr.microsoft.com/playwright:v<same as package>-jammy`), `animations: 'disabled'`, ECharts test mode `animation:false` via `?e2e=1`-free approach: the demo server's `UNRAVIS_DEMO_NOW` plus a Playwright init script that sets `window.__UNRAVIS_TEST__ = {noAnimation:true}`; the chart code reads it (small change in `web/src/lib/` chart option builder, file name from the build pass). Charts are asserted by `page.evaluate` on series and axes, screenshots are a second layer. Windows shell shots: 3 only (window frame, tray reopen, error page) with `maxDiffPixelRatio: 0.01`, `deviceScaleFactor: 1`, baseline made on `windows-2025` and stored in `tools/e2e/baselines-win/`. Baseline refresh: workflow input `update_baselines=true`, same pattern as the existing `regenerate_baselines` in `ci.yml`; baselines never update on PR runs. Focus smoke (Alt+Tab, tauri#15624): `specs-desktop/focus.spec.ts` uses `SendInput` through a tiny helper `tools/ci/sendkeys/main.go` and asserts `GetForegroundWindow` belongs to the shell pid; this needs an interactive desktop, so under D4 it may be impossible; if the task has no desktop it is marked `not verified in CI` and runs in BL-457 on nachi's PC. Pin Tauri at or above 2.12.7 (spec 01 D14).

### 3.11 Diff coverage (BL-451)

`tools/ci/Test-DiffCover.ps1 -Base <ref> -Go <profiles...> -Rust <lcov...> -Web <lcov...> -Threshold 80 -Exclude tools/ci/cover-exclude.txt`:
1. `git diff -U0 --diff-filter=ACMR <merge-base> HEAD` gives changed line ranges per file (NUL-separated names, same care as the gofmt step in `ci.yml`).
2. Skips files by extension (`.md`, `.json`, `.yml`, `.ps1`, `.svelte`, `*_test.go`, `*.test.cjs`, `*.d.ts`), by `cover-exclude.txt` (generated code, `contracts/**`, `*_other.go` stubs listed per file), and by build tag: a file with `//go:build windows` counts only against the Windows profile.
3. For each language, executable changed lines = lines present in the profile (Go blocks, lcov `DA:`); lines not in the profile (comments, declarations) do not count. Covered % = hit / executable. Fewer than 10 executable changed lines in a language passes.
4. Prints a table per file (changed, covered, uncovered line numbers) to the summary; exit 1 under threshold.
Same script runs locally: `pwsh tools/ci/Test-DiffCover.ps1 -Base origin/master -Go go.cover` gives the same result on the same diff (acceptance). Merge: `tools/ci/Merge-GoCover.ps1 a.cover b.cover > merged.cover` (same `mode`, per block max count). Job `cover-gate` (desktop-windows.yml) needs `linux-gates` artifact `go-linux.cover` (from `-coverprofile` in the race step; add `-coverpkg=./...` is NOT used, per-package only) and `shell` artifact `go-win.cover`. Because `ci.yml` and `desktop-windows.yml` are two workflows, `cover-gate` instead lives in `ci.yml` and the Windows profile is produced by the existing `build` job: change its `go test` line to add `-coverprofile=go-win.cover` and upload it; `shell` then does NOT rerun Go tests (step 4 in 3.4 is removed). Final: `ci.yml` job `cover-gate` needs `build` and `linux-gates`, and downloads `rust-win.lcov` from `desktop-windows.yml` only when that workflow ran for the same SHA (uses `gh run download` with the run found by `head_sha`; if not found, Rust is skipped with a notice and the Rust gate runs inside `shell` itself on `rust-win.lcov` alone). Simpler default chosen: Rust gate runs inside `shell`; Go gate in `ci.yml cover-gate`; web gate in `linux-gates`.

### 3.12 Security workflow tests (BL-449)

Area 02 owns `supply-chain.yml` (jobs `cargo-deny`, `govulncheck`, `osv`, `secrets`, `pins`, `npm-policy`). Area 08 adds:
- `testdata/supply-chain/` fixtures: `bad-advisory/` (tiny Cargo project pinning a crate version with a published RustSec advisory; the exact crate is chosen at build from the then-current advisory DB and written into the README of the folder), `bad-licence/` (crate with a licence outside the allowlist), `expired-waiver/osv-scanner.toml` (`EffectiveUntil` in the past), `fake-secret.txt` (an obviously fake token in a pattern gitleaks detects).
- `tools/ci/Test-SupplyChainGates.ps1`: runs each fixture against the real policy files and expects non-zero; prints PASS or FAIL per case. Called by `supply-chain.yml` on `workflow_dispatch` input `selftest=true` and weekly.
- `tools/dev/Install-GitHooks.ps1` (PS 5.1 compatible): writes `.git/hooks/pre-commit` that runs `gitleaks protect --staged --redact`; stops with a message if `gitleaks` is not on `PATH` (suggests `winget install gitleaks.gitleaks`). `testdata/supply-chain/` is allowed in `.gitleaks.toml` by path so the fixture can be committed.
- trufflehog is `--only-verified`, so it never flags the fake. The gate that proves "a leaked secret fails" is gitleaks on the fixture; the trufflehog job is proven by running it on a repo path with no secret and by reading its exit code mapping in the job.

### 3.13 Release evidence (BL-456)

SBOM: area 02 BL-336. Attestation: existing `actions/attest-build-provenance` in `release.yml` (spec 02 line 35) and `Sign-Release.ps1` verification (BL-150 U1b). ARM: dropped (D14). Area 08 adds `tools/ci/Test-ReleaseEvidence.ps1 -Tag <tag>` (pwsh): checks the draft or published release has an SBOM asset, runs `gh attestation verify <file> --repo <owner/repo> --format json` for each binary, and prints the JSON field names it found (`sourceRepositoryDigest`, `sourceRepositoryRef` are expected per BL-150 U1b; the script fails with the real field list if they differ, which is how the unproven names get proven). Attestation of private repos needs Enterprise Cloud: when `vars.REPO_PUBLIC != 'true'` and the first call returns the plan error, the script records the reason and exits 0 with a notice, never silently.

### 3.14 Live-PC runner (BL-457)

`tools/dev/Test-LivePc.ps1` (must parse in Windows PowerShell 5.1: CI already parses `tools/dev` in 5.1; no `?:`, no `&&`, no `-AsHashtable`). Params: `-Case`, `-Install <dir>` (default: read from the Add/Remove entry), `-Checks <list>` (default all that exist), `-DryRun`, `-Evidence <dir>`, `-AllowElevated`.
Checks (name, item): `Build-Names` (442, compares `files.txt` from a CI artifact path given by `-CiFiles` with `dist\`), `Go-Baseline` (441, runs `go test -json ./...` when `go` exists, else skipped, uses `Compare-GoFailures.ps1` and expects zero failures on Windows), `Mcp-Ten-Tools` (444, runs `unravis.exe --mcp` against the real running dashboard; calls the 10 tool names read from `internal/mcp` list through `tools/desktop/mcp-probe` (spec 03); checks size caps and redaction), `Contract-Real` (445, `Record-Golden.ps1 -Validate` only, writes nothing to the repo), `E2E-Real-Server` (446, `UNRAVIS_EXE` real exe with no `--demo`, specs tagged `@live` only: read-only), `Desktop-Smoke` (447, attaches to the installed desktop through the same `desktop-launcher.ts` with `-Installed`, needs an `e2e` build, so it skips with "needs e2e build" on a release install; screenshots go to `-Evidence`), `Data-Readable` (453, 455: opens the case read-only before and after an upgrade, hashes case files before and after), `Focus` (448).
Safety: never writes to the data folder, never deletes, never prints a token (`Authorization`/`X-Audit-Token` values and the locator are not logged); refuses to run when elevated unless `-AllowElevated`; case folder hash taken by `Get-FileHash` on a copy-free read-only open; two runs must give identical hashes (`-Twice` runs it twice and compares). Evidence file `evidence-<yyyymmdd-hhmm>.json` has: versions (`unravis.exe --version`), OS build, CPU name, RAM, run timings, pass/fail per check, case id hash. No case names, no paths under the user profile except the install dir.
CI dry run: `-DryRun` swaps each check's inputs for fakes in `tools/dev/livepc-fakes/` and runs in a job in `desktop-windows.yml` (`livepc-dryrun`, PS 5.1 and pwsh 7 both).

## 4. Interfaces other areas rely on (exact names)

Build switches: Go tag `e2e`; Rust feature `e2e`; Cargo feature `testmanifest` (tests only); `build.ps1 -E2E` (spec 06 adds the switch; area 08 calls it).

Env (all read only in `e2e` builds): `UNRAVIS_E2E_SIDECAR_ARGS`, `UNRAVIS_E2E_KEEP_WEBVIEW2_ENV`, `UNRAVIS_RUN_DIR`, `UNRAVIS_SIDECAR`, `UNRAVIS_TEST_PIPE_IMAGES`, `UNRAVIS_TEST_TRUSTED_KEY`, `UNRAVIS_TEST_FEED_BASE`, `UNRAVIS_SETUP_PAUSE_AT`. Forbidden-string additions by area 08 in `tools/security/forbidden-strings.txt`: `UNRAVIS_E2E_`, `UNRAVIS_TEST_TRUSTED_KEY`, `UNRAVIS_TEST_FEED_BASE`, `test-crash-on-start`, `UNRAVIS_SETUP_PAUSE_AT`, `remote-debugging-port`, `e2e_tray_hide`, `e2e_quit`, `e2e_invoke_unlisted`, `pipesec.TestAllow`, `selftest-seal`, `UNRAVIS_TEST_PIPE_IMAGES`.

Test pipe names: `\\.\pipe\unravis-test-<pid>-<rand8hex>`, made by Go helper `internal/pipesec/pipesectest.Name(t testing.TB) string` (area 02 adds the package; area 08 specifies the format). Tests must not use production names.

Files and scripts: `docs/desktop/baseline-go-tests.txt`; `tools/ci/Compare-GoFailures.ps1`; `tools/ci/Test-DiffCover.ps1`; `tools/ci/Merge-GoCover.ps1`; `tools/ci/cover-exclude.txt`; `tools/ci/New-TestUser.ps1`; `tools/ci/Invoke-AsStandardUser.ps1`; `tools/ci/testfeed/`; `tools/ci/pipeprobe/`; `tools/ci/Record-Golden.ps1`; `.github/actions/win-setup`; `contracts/desktop/v1/*.schema.json`; `contracts/fixtures/*.json`; `tools/e2e/support/desktop-launcher.ts`; `tools/dev/Test-LivePc.ps1`.

Workflow and job names (area 06/07/01/05 reference these; the required checks for BL-197 are the bold ones): `ci / build`, `ci / powershell-syntax`, `ci / linux-gates`, `ci / e2e-linux`, `ci / cover-gate`, `ci / desktop-sidecar` (area 01), `ci / cli-windows` (area 05), `ci / rust-linux`, `desktop-windows / shell`, `desktop-windows / release-scan`, `desktop-windows / e2e-smoke`, `desktop-windows / installer`, `desktop-windows / services`, `supply-chain / (area 02 job names)`. Required for merge from the day they exist: `build`, `powershell-syntax`, `linux-gates`, `cover-gate`, `shell`, `release-scan`, `supply-chain`. Not required (path-filtered, slow, or measuring): `e2e-smoke` until its soak passes, `installer`, `services`, `ota`, `fuzz`. Path-filtered jobs that do not run must not block: use the `needs` plus an always-run `gate` job `ci / all-green` that treats "skipped" as pass, and make only `all-green` required, so a filter cannot deadlock a PR.

Rules the other areas must follow so these gates work:
1. Windows-only Go code goes in `*_windows.go` or `//go:build windows`; its tests in `*_windows_test.go`. A Windows-only test in an untagged file adds a Linux failure and fails `linux-gates`.
2. Every new wire shape gets a schema and a golden in `contracts/` in the same PR.
3. Playwright specs that need Windows carry `@windows` in the title; specs that are safe on a real PC carry `@live`.
4. New pipe tests use `pipesectest.Name`; new tests that spawn the shell or the sidecar use `LOCALAPPDATA`/`APPDATA` overrides, never the real profile.
5. `internal/**` Go fuzz targets go in `<name>_fuzz_test.go`.

## 5. Build notes per item

Order inside the area: BL-440 and BL-441 and BL-457 (skeleton) start together (P0) -> BL-444 (P1) -> BL-442 -> BL-443 -> BL-451 -> BL-449 -> BL-447 (P2) -> BL-445 -> BL-446 -> BL-448 -> BL-450 -> BL-452 (P3) -> BL-453 -> BL-454 -> BL-455 -> BL-456 (P4). BL-457 grows with each item above.

### BL-440 CI baseline audit, visibility (P0, S)
- Add `tools/ci/Get-CiBaseline.ps1` (pwsh; uses `gh`): for each workflow `gh run list --workflow <f> --limit 20 --json databaseId,conclusion,createdAt,updatedAt,headSha,event`, billable minutes from `gh api repos/{o}/{r}/actions/runs/<id>/timing`, `gh cache list --json sizeInBytes`, `gh repo view --json visibility,defaultBranchRef`, `gh api repos/{o}/{r}/branches/master/protection` (403 or 404 is recorded as "not readable", not a failure), `git log --oneline origin/master..HEAD -- .github`. Writes `docs/desktop/ci-baseline.md` with a cost table (public: 0; private: minutes x 2 for Windows over the free quota) and the list of items blocked when private (CodeQL, Secret Protection, Rulesets on Free, new attestation). `-SetVariable` runs `gh variable set REPO_PUBLIC`.
- Tests: `tools/ci/Test-CiBaseline.ps1` with a fake `gh` on PATH (fixtures in `testdata/ci-baseline/`); runs in `powershell-syntax`.
- Acceptance (run `pwsh tools/ci/Get-CiBaseline.ps1 -Out docs/desktop/ci-baseline.md`): file has last 20 runs per workflow with minutes (CI/live); visibility line; "blocked when private" list. Live PC (nachi): `gh auth status` and the same command; owner glances at branch rule lines (BL-197). If `gh` has no token the script says so and the item stays open.

### BL-441 Linux gates (P0, M)
- Add `.golangci.yml`, `.github/dependabot.yml` (ecosystems: `gomod` `/`, `npm` `/web`, `/tools/e2e`, `/tools/screens`, `github-actions` `/`; `cargo` `/desktop` is added by BL-442; weekly, one grouped PR per ecosystem, `open-pull-requests-limit: 5`, no auto-merge, no `automerge` label, commit prefix `deps:`), `ci.yml` job `linux-gates`, `tools/ci/Compare-GoFailures.ps1`, `tools/ci/Test-PathFilters.ps1`, `docs/desktop/baseline-go-tests.txt`, `docs/adr/README.md`, `docs/adr/0000-template.md`.
- Add `-coverprofile=go-linux.cover` to the race step; upload artifact.
- Pester-free tests in pwsh: `tools/ci/Test-Compare.ps1` (fixtures `testdata/ci/go-test-ok.json`, `-new-failure.json`, `-build-fail.json`) run in `linux-gates`.
- First golangci-lint run on 375k lines: judged only on new code (`new-from-merge-base`), so no mass fix.
- Acceptance: (a) throw-away PR adds `os.Remove("x")` with unchecked error in `internal/server`: job `linux-gates` fails with errcheck, revert passes (CI). (b) throw-away PR adds a Linux-failing test: job fails with `NEW FAILURE`, the 9 listed ones do not (CI). (c) Dependabot opens grouped PRs, none merges itself (CI). (d) Live PC (nachi): `go test -json ./... > t.json; pwsh tools/ci/Compare-GoFailures.ps1 -Json t.json -Baseline <empty file>` on Windows: zero failures (live PC).

### BL-442 Windows job skeleton (P2, M)
- Add `.github/workflows/desktop-windows.yml` (job `shell`, 3.4), `.github/actions/win-setup/action.yml`, `tools/ci/Write-BuildTiming.ps1`, `desktop/rust-toolchain.toml` (spec 01 BL-320 pins; area 08 reads it), add `cargo` to `dependabot.yml`. Remove `desktop-shell` from `ci.yml` if area 01 added it (D2).
- Acceptance: green from empty cache then warm; summary shows both times (CI); `with space` leg builds (CI); a PR touching only `web/src/**` triggers `shell` (CI); live PC (nachi): `./build.ps1 -SkipTests` then `Get-ChildItem dist | Select Name` matches `files.txt` of the CI run (live PC; checked by `Test-LivePc.ps1 -Checks Build-Names`).
- Open: cold build time unknown. If cold is over 20 min, split artifacts across two jobs (Rust and Go) first; `sccache` is not tried (MSVC PDB friction) unless warm time is still over 12 min. Decided by measurement.

### BL-443 Rust test harness (P2, M)
- Add `desktop/src-tauri/src/testing/mod.rs` (`#[cfg(test)]`): `fn app() -> tauri::App<tauri::test::MockRuntime>` that builds with `tauri::test::mock_builder()`, the same `invoke_handler` list as `main`, the real `capabilities/*.json` through `tauri::test::mock_context(noop_assets())`. Commands and setup helpers are `fn x<R: tauri::Runtime>(app: &AppHandle<R>)`. `Cargo.toml` `[dev-dependencies] tauri = { features = ["test"] }`; feature `testmanifest` + `test.manifest` (3.4).
- Tests (names): `command_args_serde_roundtrip`, `state_is_injected`, `unlisted_command_is_denied`, `listed_command_allowed`, `capability_file_matches_appmanifest` (reads `build.rs` command list and `capabilities/default.json`; fails when they differ), `no_plugin_in_cargo_lock` (greps `Cargo.lock` for `tauri-plugin-`; spec 02 has the script; this is the unit twin).
- Acceptance: `cargo test --locked` green on `windows-2025` (CI); denial test (CI); guard job `Test-ManifestGuard.ps1` shows binary loads with a plugin-style import (CI, manual dispatch).

### BL-444 Go Windows tests (P1, M)
- Add `ci.yml` matrix on `desktop-sidecar` (spec 01) and the tests: `cmd/audit-explorer/mcp_blackbox_windows_test.go`: `TestMcpTenToolsFrameOverStdio` (start `--demo --no-browser --port 0` under temp `APPDATA`, then `--mcp`; tool names must equal `internal/mcp/cases_absent_test.go`'s list), `TestMcpRejectsOversizeInput` (1 MiB plus 1), `TestMcpOutputIsBounded`, `TestMcpRedactsIdentifiersByDefault`, `TestMcpAllowIdentifiersFlag`. Pipe tests live with their owners (`internal/pipesec`, spec 02; `internal/pipesrv`, spec 01); area 08 adds `internal/pipesec/pipesectest` (`Name`, `StartServer`, `DialAsOtherUser`) and `TestOtherUserDenied` (skips unless env `UNRAVIS_CI_OTHER_USER` is set; the CI step sets it after `New-TestUser.ps1` and runs `tools/ci/pipeprobe` as that user). Do not use `CloseWrite` for half-close (go-winio #257): tests send length-framed requests and read to the frame end.
- Parallel test runs: `go test -p 4 -count=2` leg to show unique names do not collide.
- Acceptance: `go test -count=1 ./internal/pipesec/... ./internal/pipesrv/... ./internal/locator/... ./internal/mcp/... ./internal/keeper/... ./internal/instance/... ./cmd/audit-explorer/...` green on 2022 and 2025 (CI); other user denied (CI); live PC (nachi): `unravis.exe --mcp` with the real running dashboard and `Test-LivePc.ps1 -Checks Mcp-Ten-Tools` prints 10 PASS with bounded redacted output (live PC).

### BL-445 Contracts and goldens (P3, M)
- Files: 3.9. Add `github.com/santhosh-tekuri/jsonschema/v6` to `go.mod` (test only; `go mod tidy` step in CI already checks). Rust crate `jsonschema` as dev-dependency of `ipc-core`.
- Acceptance: rename one field in a handler struct, `go test ./internal/contracts/...` fails; same rename in a Rust struct fails `cargo test -p ipc-core` (CI); `Record-Golden.ps1 -Check` on demo gives no diff (CI); live PC (nachi): `Record-Golden.ps1 -Base <real locator> -Validate` passes schemas, nothing written, and the committed fixtures contain no string from a list the owner reads once (live PC).

### BL-446 Playwright Linux and mocked IPC (P3, M)
- Files: 3.10. Change `tools/e2e/support/launcher.ts` (platform suffix, no `D:\` default), `tools/e2e/package.json` scripts `test:linux`, `test:mocked`, new `playwright.mocked.config.ts`, `support/tauri-mock.ts`, `support/ipc-forward.ts`, `LINUX-SKIPS.md`, `Test-LinuxSkips.ps1`.
- Acceptance: `UNRAVIS_EXE=... npx playwright test --grep-invert @windows` green (CI); `npx playwright test -c playwright.mocked.config.ts` green incl. `invoke-error-shows-error-state` (CI); live PC (nachi): `Test-LivePc.ps1 -Checks E2E-Real-Server` runs `@live` specs against the real exe and a real case with no write (live PC).

### BL-447 CDP smoke (P2, L)
- Files: 3.5; `docs/desktop/e2e-switch-rule.md`. Needs shell items BL-306, BL-308, BL-311 from spec 01 and the `e2e` feature (spec 01 adds feature flag plumbing in BL-306; area 08 adds the commands).
- Acceptance: 10 specs green (CI); soak `soak=20` shows at most 4 failed spec runs of 200, else the rule file gets the switch decision (CI); `release-scan` clean (CI); live PC (nachi): installed desktop, `Test-LivePc.ps1 -Checks Desktop-Smoke` with an `e2e` build in a scratch folder (not the installed release), screenshots in the evidence folder (live PC).

### BL-448 Accessibility, visual, focus (P3, M)
- Files: 3.10. Acceptance: zero serious or critical axe findings on all screens, 5 reruns give identical screenshot results (CI); focus spec passes or is marked `not verified in CI` with D4 reason (CI/live); live PC (nachi): NVDA or Narrator manual pass recorded in `docs/desktop/a11y-manual.md` (live PC; stays manual).

### BL-449 Security workflow tests (P2, S)
- Files: 3.12. Acceptance: `Test-SupplyChainGates.ps1` prints PASS for bad advisory, bad licence, expired waiver, fake secret (CI); `Install-GitHooks.ps1` then `git commit` of a staged fake secret is refused on nachi's PC (live PC).

### BL-450 Fuzz (P3, M)
- Files: 3.8. Acceptance: nightly runs and uploads a crasher for a seeded bug (a test-only `//go:build fuzzseed` function that panics on one input; CI); the committed crasher file makes the normal `go test` fail on the buggy build and pass on the real one (CI); live PC (nachi): `go test -fuzz FuzzParseManifest -fuzztime 10m ./internal/update` seeded with the real feed's `update-manifest.json` (read-only download of the public file) and a real MCP session log (copy) with no crash (live PC).

### BL-451 Diff coverage (P2, M)
- Files: 3.11, `tools/ci/Test-DiffCover.ps1`, `Test-DiffCoverSelf.ps1` (fixtures: a git repo built in a temp folder).
- Acceptance: a PR adding an uncovered 10-line function fails, with a test it passes; docs-only PR passes; local and CI results equal for the same diff (CI).

### BL-452 cargo-mutants (P3, S)
- Files: 3.3. Scope `-p ipc-core` only; Windows-only shell code is not mutated.
- Acceptance: PR that changes `policy::command_allowed` to always true with no failing test fails with a surviving mutant (CI); a PR with no Rust diff passes in under 1 minute (CI).

### BL-453 Installer CI (P4, L; split A per-user, B per-machine and upgrade and recover)
- Files: 3.6. Depends on spec 06 BL-404, BL-405 for the desktop file. Real `setup.json` keys read from `internal/setup/options.go` by `New-SetupConfig.ps1`.
- Acceptance: A: per-user under the standard user, files and keys in the user profile, nothing new under `HKLM` (CI); B: per-machine, upgrade in place, `--recover` after a kill (CI); elevated/UAC: standard user starts setup on nachi's PC or Windows Sandbox and approves UAC (elevated/UAC; owner clicks); live PC (nachi): upgrade over the real install keeps the real case readable, `Test-LivePc.ps1 -Checks Data-Readable` hash equal before and after (live PC).

### BL-454 Services CI (P4, M)
- Files: 3.6, `tools/ci/bbcheck/`. Names from constants.
- Acceptance: install, start, query, stop, delete of 3 services and Session 0 pipe check prints 0 (CI); 35 s recording has at least 2 valid ticks (CI); elevated/UAC on nachi's PC through real setup (elevated/UAC); live PC (nachi): real recording opens read-only in the app (live PC).

### BL-455 OTA CI (P4, L; split A good and bad signature, B rollback)
- Files: 3.7, `internal/update/testkeys_e2e.go`, `testkeys_other.go`, `tools/ci/testfeed/`, tests `TestTestKeysIgnoredWithoutE2E`, `TestTestFeedBaseOnlyLoopback`, `TestTestKeyIsAddedNotReplaced`.
- Acceptance: five-step sequence green (CI); release scan finds none of the forbidden strings (CI); elevated/UAC: scratch PC, forced bad update, 3 failed starts, real rollback (elevated/UAC); live PC (nachi): after rollback the real case opens read-only (live PC). This item never replaces BL-150 U5's real-PC proof.

### BL-456 Release evidence (P4, S)
- Files: 3.13. Acceptance: `Test-ReleaseEvidence.ps1 -Tag <rc>` finds SBOM and verifies attestation, or records the private-repo reason (CI); on nachi's PC the same command on a real rc prints the field names and passes (live PC). ARM part dropped (D14).

### BL-457 Live-PC runner (Parallel A, starts P0, M)
- Files: 3.14, `tools/dev/livepc-fakes/`. Starts with checks `Go-Baseline` and `Build-Names`; each later item adds its check in its own PR (the item's acceptance includes "check added").
- Acceptance: nachi runs `pwsh tools/dev/Test-LivePc.ps1` (or `powershell` 5.1) and sees a table with a green row per built check (live PC); `-Twice` gives identical case hashes (live PC); `-DryRun` passes in CI under both shells (CI).

## 6. Security and footprint checks

- No production signing key or passphrase in any workflow. Test key is generated per run, deleted in an `always()` step, never uploaded as an artifact.
- Test-only code never ships: tag/feature `e2e` only; release scan (D11) runs on a `-Release` build in `release-scan` for every PR that touches Go or `desktop/`; checks strings in `forbidden-strings.txt` in `unravis.exe`, `unravis-desktop.exe`, `unravis-setup.exe`, `unravis-keeper.exe`, `unravis-blackbox.exe`; `cargo tree -e features` has no `e2e`. A control case: an `-E2E` build must FAIL the scan (the scan proves it can fail).
- `UNRAVIS_TEST_FEED_BASE` is accepted only for `http://127.0.0.1:<port>`; a unit test proves a hostname and `https` are ignored.
- Standard-user password: random per run, masked, never in `env`, files or artifacts. The test user and tasks are removed in `always()`.
- WebView2 remote debugging only through the kept env in an `e2e` build, on `127.0.0.1`, per-run port, per-run profile folder. Production strips `WEBVIEW2_*` (spec 01 BL-311) and the scan checks the string is not in the release exe.
- Fixtures: scrub rule D18; `gitleaks` runs on `contracts/fixtures/`; the owner reads the first set once.
- Every new action is pinned to a full SHA; `tools/security/check-action-pins.mjs` (area 02) fails otherwise. Dependabot updates SHAs with the tag in a comment.
- Cache: `rust-cache` and npm cache are keyed on lockfiles; no cache of `target/` from fork PRs (default behavior); total cache watched by `Get-CiBaseline.ps1` (10 GB cap; measure after 2 weeks, report 09 risk 5).
- Footprint (installer size, RAM, CPU, network silence) is area 07's gate; area 08 runs `Check-Size.ps1` and the network soak where area 07 and 06 wire them, and does not set numbers. Cost note: `desktop-windows.yml` is path-filtered (D9); fuzz is Linux only.
- Live runner: read-only, no secret in logs, refuses elevation.

## 7. Open risks left (each with a mitigation that needs no owner)

| Risk | Mitigation |
|---|---|
| Cold Windows build is slow (no data). | `Write-BuildTiming.ps1` records it from the first run; split Rust and Go jobs if over 20 min; path filters keep it off unrelated PRs. |
| WebView2 will not start in a standard-user scheduled task (no interactive desktop). | D4 fallback to `@wdio/tauri-service` embedded, written as a switch rule; Rust unit tests and the mocked Linux project still gate. |
| CDP attach hangs (playwright#42730). | Poll `/json/version` first, 30 s hard timeout, `taskkill /T /F`, no retries; flake measured by soak (D3). |
| `tauri::test` is unstable API. | Harness is one module (`testing/mod.rs`); Tauri version pinned; Dependabot PR for Tauri runs the suite before merge. |
| comctl32 crash may not occur with zero plugins. | Guard test is a manual-dispatch control only; the manifest stays behind feature `testmanifest` and costs nothing in release. |
| Linux Playwright reuse rate unknown (some of 63 specs need Windows). | `@windows` tag with a one-line reason each; unexplained failures block, so skips cannot grow silently. |
| Race run is slower and may find real races in old code. | New failures not in the baseline fail; real races found are fixed or added to the baseline with a backlog line in `docs/BACKLOG.md`. |
| golangci-lint noise on old code. | `new-from-merge-base` only; the old-code pass is a separate backlog item, not built here. |
| Merged Go coverage hides a Windows file with no Windows test. | `Test-DiffCover.ps1` counts a `windows`-tagged file only against the Windows profile; a file in no profile prints `NOT IN ANY PROFILE` and fails. |
| The `ipc-core` split changes spec 01 module layout. | The split is only for three pure modules; spec 01 modules become thin callers; the interface is `ipc_core::frame::parse_response`, `ipc_core::locator::parse`, `ipc_core::policy::*` (section 4 of spec 01 must list these names when it is next edited). |
| Real N-1 to N with production keys cannot run on an unreleased build. | Split in 3.7 step 4: same-code `-tags e2e` versions in CI; real pair by `verify-release.yml` after publish; real PC rollback stays an owner acceptance. |
| Hosted runners cannot represent Win 10, Server 2016, 2019. | Labelled "not verified" (D15); `Test-LivePc.ps1` records the OS class when run on such a PC. |
| `gh attestation verify` field names unproven. | `Test-ReleaseEvidence.ps1` fails with the real field list on the first rc; fix the names then. |
| Private repo: `gh api .../protection` unreadable; attestation unavailable. | Script records "not readable" and the private-plan consequence; jobs read `vars.REPO_PUBLIC`; none of the required checks depends on it. |
| The Blackbox working-set limit has no source number. | Logged for 10 runs, threshold set to median x 1.5, no fail before that. |
| Baseline file drifts as Windows-only tests move. | Stale lines print a notice every run; `Compare-GoFailures.ps1 -Write` regenerates; a PR that touches the baseline must say why in its description. |
