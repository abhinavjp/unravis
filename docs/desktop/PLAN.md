# Unravis Desktop - plan (stage 3)

Factory order: research -> groom (`groom/`) -> spec (`spec/`) -> **plan (this file)** -> build -> review -> verify. 140 items, BL-300 to BL-459. Decisions: `DECISIONS-draft.md` (still valid except where this file overrides it).

## 1. Cross-area contract (overrides conflicting text in the specs)

Eight specs were written in parallel and disagreed in places. These rulings are final; build workers follow this file over a spec.

| Topic | Ruling |
|---|---|
| Pipe library | `internal/pipesec` (spec 02) is the only pipe listener and client code. Spec 01's `pipesrv` and `internal/locator` call it. No `go-winio` dependency. |
| Pipe names, DACL | `\\.\pipe\unravis.<role>.<32hex>`. Roles: `ui` (shell only), `ag` (CLI and MCP). DACL is spec 02's logon-SID form (stricter than 01's user-SID form). Client checks: logon SID, session, image path inside install dir (allow `<install>\bin\unravis.exe` too). |
| Locator | `%LOCALAPPDATA%\Unravis\run\backend.json`, no token field, written only by `internal/locator`. Browser-mode `session.json` unchanged. Desktop-started Go runs `--pipe-only`. |
| `ag` pipe routes | Two prefixes: `/mcp/v1/*` (spec 03) and `/cli/v1/*` (spec 05, `internal/clisrv`). Browser-mode owner also opens `ag` so CLI and MCP get grants there. |
| Executables | `unravis.exe` (GUI subsystem, owner, sidecar, `--mcp`, legacy flags) stays. New: `unravis-desktop.exe` (shell), `bin\unravis.exe` (thin console CLI, only `bin` on PATH), `sandbox\unravis-sandbox.exe`. `unravis-core.exe` only if spike BL-305 says so. |
| Exit codes | CLI: 0 to 7 (spec 05); no owner = 3. Spec 01's exit 10 for CLI is dropped. `--mcp` with no owner still answers `tools/list`. Shell: 77 admin, 78 WebView2 debug flags. |
| Data folders | No data move. BL-316 only adds `run`, `webview2`, `crashes` under `%LOCALAPPDATA%\Unravis`. MCP data in `%LOCALAPPDATA%\Unravis\mcp`; setup and uninstall leave it alone. |
| Signing | BL-312 means BL-400. Provider SignPath Foundation (owner choice A48), SSL.com fallback. rc stays unsigned until approval; stable fails unless signed. |
| Test switch | One Go build tag and Rust feature: `e2e`. All areas append to `tools/security/forbidden-strings.txt`; release scan fails if any appears. |
| CI | `ci.yml` stays on `windows-2022`. New desktop jobs in `desktop-windows.yml` on `windows-2025`. One always-run `ci / all-green` job is the required check. Code sandbox gets `code-sandbox.yml`. |
| Rust layout | `desktop/` is a Cargo workspace: `src-tauri` plus pure crate `desktop/ipc-core` (parsers, policy tables; testable on Linux). Lock at `desktop/Cargo.lock`. |
| Contracts | JSON Schema plus golden files in `contracts/`. No OpenAPI generation (supersedes DECISIONS section 11). |
| Go test baseline | Today 9 failing `internal/server` tests on Linux (not 19). BL-441 writes `docs/desktop/baseline-go-tests.txt`; cite the file. |
| Bulk chart data | Pipe through the Rust relay. Loopback only if BL-313 benchmark fails the p95 <= 100 ms for 1 MB rule, and then as a recorded exception. |
| WebView2 config | One file `lockdown.rs` with `security()` (02) and `network()` (07). `build_main_window` is the only window constructor. |
| Window idle | BL-308 simple hide/destroy first; BL-427 replaces the timer with `idle.rs`. Idle clock is `Updater.InUse()` plus `internal/power`. |
| Update hand-off | Real setup flags only (`--upgrade --silent --close-app --expect-sha256 --expect-version`, `--allow-downgrade`). Shell exits on `--quit`, then setup sends `instance.Quit`. No new manifest asset key. |

## 2. Build order

Rule: browser mode keeps working in every PR. Each PR is small, has tests, gets a fresh adversarial reviewer, then merges. Items marked **live** need nachi's PC and stay unticked until run there.

**Wave 0 (foundations, parallel, no desktop code yet)**
- BL-440 CI audit, BL-441 Linux gates + Go failure baseline, BL-300 / BL-420 baseline harness (live), BL-380 CLI baseline, BL-340 MCP baseline (live), BL-335 supply-chain gates, BL-421 Go build flags.
- Spikes (independent, results feed later items): BL-305 exe split, BL-341 MCP Go SDK, BL-360/361 sandbox runtime, BL-381 Cobra, BL-324 Trusted Types, BL-327 ACG, BL-411 WiX.

**Wave 1 (P1 Go attachable)**
BL-330 `pipesec` -> BL-301 pipe server -> BL-302 locator + attach/start -> BL-303 shutdown RPC -> BL-304 admin gate -> BL-318 CLI/MCP attach via locator. Windows CI jobs `desktop-sidecar` and `pipesectest`.

**Wave 2 (P2 shell preview)** needs wave 1.
BL-320 pin floor -> BL-306 skeleton + Job Object -> BL-321/322 plugins zero + conf + CSP -> BL-309 single instance -> BL-308 tray/destroy -> BL-311/328/329 lockdown -> BL-325/326 startup hardening -> BL-307 supervisor -> BL-310 autostart -> BL-317 bundle sidecar. In parallel: BL-323 UI CSP changes, BL-429 UI idle.

**Wave 3 (P3 bundled transport)**
BL-313/432 bulk benchmark (live) -> BL-314 case-home slice over relay -> BL-332 no loopback in desktop -> BL-430/431 M4 and binary series -> BL-423/424/425/426/427/428 footprint work -> BL-433/434 network silence (live) -> BL-315 crash capture -> BL-316 folders.

**Parallel A (MCP)** after BL-330/301: BL-341 -> 342 -> 343 -> 344/345 -> 346/347 -> 348/349/350 -> 351 -> 352/353 -> 354/355 (live, six hosts).
**Parallel B (CLI)** after BL-381/330: BL-382 -> 383 -> 384 -> 385 -> 386/387 -> 388/389 -> 390/391 -> 392/393 -> 394/395/397; BL-396 last.
**Parallel C (code mode)** after BL-360/361 and spec 03 `mcpsrv`: BL-362 -> 363 -> 364 -> 365 -> 366/367 -> 368 -> 369/370 -> 371/372 -> 373/374 -> 375.

**Wave 4 (P4 desktop RC)** distribution: BL-400 (owner, money/identity) -> 401/402/403 -> 404/405/406/407 -> 408 -> 416/417 -> 409/410 -> MSI/WinGet 412/413/414 -> 415 (fresh PC). Testing: BL-442 onward (Windows skeleton, shell smoke, installer, OTA CI), BL-455, BL-456, BL-457 live runner. Then BL-435/436/437.
**Wave 5 (P5)** default flip only with evidence from the live runner.

## 3. Owner actions (everything else is decided)

- BL-400 signing identity onboarding (SignPath application, identity proof, or a paid cert). Until then rc builds ship unsigned.
- UAC clicks on elevated installs (BL-141 type), signing passphrase via Proton Pass, live runs on the PC.
- Server 2016 / domain / Intune / proxy checks are written "not verified" until someone with such a box records a dated result.

## 4. Research gaps I will close myself (web research workers)

Tauri 2 cold start and RAM on Windows with this bundle; WebView2 binary IPC throughput; MCP 2026-07-28 host support; SignPath eligibility terms; wazero ACG behaviour. Outputs go to `research/outputs/desktop-tauri/gaps/` (git-excluded) and a short result is copied into the owning spec.

## 5. Review and verify

Every PR: a fresh reviewer with no context reads only the diff and the spec section (cap 2 fix rounds). Verify on the real path: Windows CI jobs for what CI can prove, nachi's PC for the rest, listed per item in groom acceptance. An item is ticked only after that. Never present CI-only evidence as live.

## 6. Gap research results (2026-11-21; full files in `research/outputs/desktop-tauri/gaps/`, git-excluded)

| Gap | Finding | Plan change |
|---|---|---|
| Tauri footprint on Windows | No published cold-start number for a bundle like ours. Blank WebView2 about 75-80 MB; small UI 135-150 MB. Hiding to tray keeps RAM; closing the window frees it. Server 2016/2019 WebView2 support is not stated by Microsoft (2022 is). | Keep "destroy window on idle" (BL-427). Budgets stay targets until P0/P2 measurements. Server 2016/2019 desktop shell is "not verified"; headless Blackbox and Lite cover servers. |
| Bulk IPC | Windows `ipc::Response`: 10 MB about 80-200 ms, 33 MB about 320 ms. Only measured fix is WebView2 SharedBuffer (33 MB in 20-24 ms). CSP must allow `ipc:` or it falls back to slow JSON. | BL-313 benchmark adds: Response, Channel (64 KB bursts), custom scheme, SharedBuffer. Charts already M4-downsampled (<= 4 x width points), so payloads are small; 1 MB rule stays. |
| MCP 2026-07-28 | Go SDK supports it from v1.7.0 (latest seen v1.8.0), falls back to `initialize`. Only Claude Code is seen on the new revision; Cursor, VS Code on 2025-11-25; others unknown (medium/low confidence). | BL-341 tests both lifecycles (already planned). Ship both. Windows hosts: document `cmd /c` workaround for path-with-spaces and shim issues. |
| Signing | Unravis is Apache-2.0 and the repo is public, so **SignPath Foundation is eligible** on license. Needs: all components OSI-licensed, no dual license, published signing policy, MFA, release approval. Fallback Certum OSS (EUR 49/yr). No option gives instant SmartScreen trust. | BL-400 owner action is only the SignPath application (free). Open check: confirm bundled third-party parts (WebView2 loader, vendored wasm) do not break the "all OSI" rule. |
| wazero + ACG + LPAC | wazero compiler flips memory to executable, so it conflicts with ACG; interpreter is ACG-safe but about 10x slower. LPAC needs explicit ACL grants (package SID and S-1-15-2-2) and a per-run overlapped named pipe, not Winsock. No data on Go runtime under ACG/LPAC. | BL-327 / BL-360 / BL-367 become Windows spikes on nachi's PC before locking: worker defaults to **interpreter + ACG** and falls back to compiler without ACG only if too slow. Spec 04 "inherited stdio pipes" is changed to a per-run named pipe if the spike shows handle inheritance fails under LPAC. |
