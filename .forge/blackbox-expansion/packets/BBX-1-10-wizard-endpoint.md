# BBX-1-10 Wizard targets endpoint (detected pools + services)
Backlog: BL-117 | Group: 1 | Model: performance | Reason: new HTTP endpoint on the app server; read-only but security-relevant (endpoint review).
Depends on: BBX-1-03 | Blocks: BBX-1-12

## Goal
Add a read-only app-server endpoint that lists the running IIS app pools and Windows services on THIS PC, so the install wizard can offer detected targets instead of asking the owner to type names. Names and running state only — no command lines, no paths, no credentials.

## Context
- File `internal/server/blackbox.go` (route table `init()` lines 140-162; this-pc handlers around 149-155; auth via the app's `X-Audit-Token`). Test `internal/server/blackbox_test.go`.
- Detection already exists in the repo: `internal/discover` (IIS pools from applicationHost.config — `detect_iis.go:263`, `internal/configparse/apphost.go:30-66`; services — `winenv_windows.go:398` `EnumServicesStatusEx`). READ these files and reuse their existing entry points; do not write a new enumerator.
- This is the APP server (cmd/audit-explorer), not the Blackbox service's own status server. It runs as the logged-in user; some detection may need elevation. Return what is readable; mark the rest "not detected" — never fail the whole endpoint, never trigger a UAC prompt from a GET.
- Rules that apply: **no text** beyond pool/service names (no command lines, binary paths, env); **owner-selected targets only** (this endpoint only OFFERS names; selection happens in the wizard/config); read-only GET.

## Do
1. Add route `GET /api/blackbox/this-pc/targets` in `blackbox.go` init(), guarded like the other this-pc routes (same auth).
2. Handler `bbTargets`:
   - Use the existing discover/configparse entry points to list app pool names (from applicationHost.config) and service names (running win32 services). Read the discover package to find the exact functions; reuse them.
   - Response JSON: `{"appPools":[{"name":"...","running":true}],"services":[{"name":"...","running":true}],"detected":true|false}`. `detected=false` when detection could not run (e.g. no IIS config readable) — still return empty lists, HTTP 200.
   - Sort names; deduplicate; cap each list at 256 entries. Names only — strip any path/command-line fields the discover structs may carry.
   - On Windows-only detection failure, return empty lists + detected=false (do not 500). Provide a non-Windows path that returns detected=false (the app server builds cross-platform).
3. Factor the detection into a small function `detectTargets() (pools, services []TargetName, detected bool)` so it is testable with a fake; the handler just serializes.

## Do not
- Do not return command lines, binary paths, environment variables, or account names.
- Do not trigger elevation/UAC from this GET.
- Do not write or change any config here (selection is a separate install/apply call).
- Do not expose this on the Blackbox service's status server (server.go) — app server only.
- Do not scan the network or other machines (this PC only).

## Tests to write first
- `TestBBTargetsShape` — fake detectTargets returns pools+services → JSON has name+running only, sorted, no extra fields.
- `TestBBTargetsNotDetected` — detectTargets returns detected=false → HTTP 200, empty lists, detected=false.
- `TestBBTargetsCapsAndDedup` — >256 and duplicates → capped, deduped.
- `TestBBTargetsAuth` — missing/invalid token → 401 (match existing this-pc auth test pattern).
- `TestBBTargetsNoText` — assert serialized JSON contains no "path"/"command"/"account" keys.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/server/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/server/...)
- [ ] Footprint: detection runs on demand (per GET), not in a loop; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (undetected → detected=false, empty lists)
- [ ] No text captured beyond names (TestBBTargetsNoText)
- [ ] Endpoint is GET, auth-guarded, this-PC only, no elevation (tests + code review)
- [ ] Existing blackbox server tests still pass

## Live-check needed?
YES — owner confirms on a real server with IIS: the endpoint lists the actual running app pools (e.g. the payroll API pools) and services. Fake tests verify shape/auth only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-10.md`.