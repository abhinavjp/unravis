# Phase 01: iis-privacy-export

risk: high — privacy boundary (client IP, referer and cookie digests enter the case file), per-case secret key in `meta`, export scrub setting and the export confirm contract.

## Goal

IIS imports keep the client IP, a clean referer and keyed cookie digests (only for per-case allowlisted names), while server IPs, query strings, user agents and raw cookie values stay dropped (TR-19, TR-20, TR-30). Every outward event view and every export goes through one attribute projection with scrub ON by default, and the export confirm reads one snapshot or answers 409 (TR-15, TR-21). The unverified IIS and Query Store assumptions are recorded line by line (VF-01a to h).

## Outcome

Packets (16 files; WP-01 is split into WP-01a/WP-01b and WP-02 into WP-02a/WP-02b). Order and dependencies live only in plan.md §3 and in each packet header and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

| Packet | Title |
|---|---|
| WP-01a | Cookie policy type and in-memory store |
| WP-01b | Cookie policy in the case file and on every import |
| WP-02a | IIS privacy helpers: IP check, referer cleaning, cookie split and digest |
| WP-02b | W3C stores client IP, clean referer and cookie digests (wiring) |
| WP-03 | Event validation accepts dynamic cookie attrs |
| WP-04 | HTTPERR stores the client IP when the file has one |
| WP-05 | IIS privacy tests match the new rule |
| WP-06a | Case export: scrub setting and stateless previewId |
| WP-06b | Web client sends the case id on export confirm |
| WP-07a | One projection for outward event attributes |
| WP-07b | Case export with scrubbing turned off |
| WP-45 | Verify IIS and Query Store assumptions on real samples (VF-01) |
| WP-59 | Query Store microseconds-to-milliseconds guard |
| WP-65 | Confirm reads one snapshot or answers 409 |
| WP-65a | Case revision primitive |
| WP-65b | Revision bumps at every mutation site |

The WP-65b edge on WP-01b (it edits `internal/casestore/sqlite/cookiepolicy.go`, which WP-01b creates) is in plan.md §3.

## Edges

Dependencies, as in plan.md §3 (the authority; packet files repeat them in `## Depends on`):

```
01a -> 01b, 02a
01b -> 65b, 07a
02a -> 02b
02b -> 03, 04
03 -> 04, 05
04 -> 05
45  -> 02b, 04, 59
65a -> 06a, 65b, 65
06a -> 06b, 07a, 65
07a -> 07b, 65
07b -> 65
65b -> 65
```

Frontier at phase start (parallel, disjoint files): WP-45, WP-01a, WP-65a. Then WP-01b, WP-02a, WP-06a, WP-59. Then WP-02b, WP-06b, WP-07a, WP-65b. Then WP-03 and WP-07b. Then WP-04 and WP-65. Then WP-05.

Single writer per shared file in this phase (order of writers is the order in the cell):

| File | Writers, in order |
|---|---|
| `internal/importer/importer.go` | WP-01a only |
| `internal/casestore/cookiepolicy.go`, `memory.go` | WP-01a only |
| `internal/casestore/sqlite/cookiepolicy.go` | WP-01b → WP-65b (one-line bump edit in `SetCookieAllowlist`) |
| `internal/server/cases.go` | WP-01b (`importFileWith`) → WP-07a (`toEventDTO`, `caseEvents`) |
| `internal/importer/iis/w3c.go` | WP-45 (constant only, if refuted) → WP-02b → WP-03 (`AllowedAttrs` only) |
| `internal/importer/iis/common.go`, `privacy_helpers_test.go` | WP-02a only |
| `internal/importer/iis/httperr.go` | WP-04 only |
| `internal/importer/iis/iis_test.go` | WP-02b → WP-03 → WP-04 → WP-05 |
| `internal/importer/iis/conformance_test.go` | WP-05 only |
| `internal/evidence/caseexport_enums.go` | WP-02b → WP-03 → WP-04 |
| `internal/evidence/event.go`, `event_test.go` | WP-03 only |
| `internal/evidence/caseexport.go`, `caseexport_verify.go`, `caseexport_test.go` | WP-07b only |
| `internal/server/export.go` | WP-06a → WP-65 (test seam variable only) |
| `internal/server/case_views.go` | WP-06a → WP-07b → WP-65 |
| `internal/casestore/sqlite/case.go`, `tags.go` | WP-65b only |
| `internal/importer/sqlserver/querystore.go`, `querystore_test.go` | WP-59 only |

Overlaps: none run in parallel. Every pair of writers of one file is ordered in the cell order above. All pairs of writers are ordered by a dependency edge (directly or through the chain), including WP-01b before WP-07a on `internal/server/cases.go` and WP-45 before WP-02b on `internal/importer/iis/w3c.go`. `check-plan.ps1` enforces wave order (a dependency must be in an earlier wave) and reports same-wave write-scope overlaps; it does not turn a shared file into an edge, so a change to the edges in plan.md §3 must keep every shared-file pair in the order of the table.

## Review focus

- No raw value ever stored: W3C `cs(cookie)` is read only through `splitCookiePairs` and `HashCookie`; a name not in `Allowed` produces nothing; at most 4 `cookie:` attrs and 16 attrs per event. `HashCookie` returns "" for a nil policy or a key that is not 32 bytes.
- The per-case HMAC key (`cookie.hmac_key_v1`) and the allowlist (`cookie.allowlist`) live only in `meta`; they are not in the manifest, logs, errors, exports or `GET` bodies. `SetCookieAllowlist` rejects bad names (`ErrBadCookieName`) and more than 4 names; no HTTP route sets the allowlist yet.
- `"cookie:*"` is a permission in the allowed map only; a literal event key `cookie:*`, upper-case names and names with spaces or `;` are refused. All other attribute keys still match exactly.
- Server IP, query string, user agent, userinfo, fragment and referer query stay out of every byte (sentinel `SENTINEL-7f3a` tests in WP-02b and WP-05). A referer longer than 256 bytes after cleaning is dropped, never truncated.
- Until WP-07a lands, `client-ip`, `referer` and `cookie:*` would reach `GET .../events` and the exports; phase 01 merges as a unit and no real import runs before WP-07a/07b are in. `TestBuildCaseNoIPsOrUserIDs` and `TestFix5ExportPolicyCoversEveryImporter` must stay green after every packet.
- Export scrub is ON by default on every export surface; `ScrubSensitive=false` is explicit and the preview/confirm pair carries the previewId; confirm answers 409 when the case revision moved (WP-65a/65b/65).
- WP-02b and WP-04 fix a few existing tests minimally (they break when the new attrs appear); WP-05 does the final cleanup. `TestW3CTimeIsEndGap` is not edited anywhere.
- WP-45 notes contain counts only; no sample path, IP or cookie text appears in any committed file.

## Out of scope / needs owner decision

- No HTTP route or UI to set the cookie allowlist (MVP-2); the setter exists in the store only. Owner decision needed when the first real import should use cookie digests.
- Allowlist cap of 4 names and the name charset `[a-z0-9_.-]`, 48 characters (derived from the 16-attribute limit: 10 static + `client-ip` + `referer` + 4 cookies). Raising it needs a store change.
- A 32-hex digest is a pseudonym, not anonymous data; the export scrub (WP-07a/07b) is what removes it from shared files.
- HTTPERR `#Fields` is still required by the parser; VF-01c asked whether real files lack it. If a real sample shows no header the parser change is a new packet.
- Flipping `w3cTimeIsEnd` or dropping `gapW3CTimeEnd` (decided with WP-20 after VF-01b).
- The Mongo `system.profile` and `dm_os_wait_stats` checks (VF-01f, VF-01g) need a live server and stay `UNMEASURED`.
- Tracing, lanes and UI (phases 03 and 04).

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe vet ./internal/importer/... ./internal/evidence/... ./internal/casestore/... ./internal/server/...
.dev\toolchains\go\bin\go.exe test ./internal/importer/... ./internal/evidence/... ./internal/casestore/... ./internal/server/... -count=1
.dev\toolchains\go\bin\go.exe test ./internal/evidence/ -run "TestFix5ExportPolicyCoversEveryImporter|TestBuildCaseNoIPsOrUserIDs" -count=1
.dev\toolchains\go\bin\go.exe test ./internal/importer/iis/ -run "TestW3CTimeIsEndGap|TestVerify" -count=1 -v
cd web; npm test; npm run check
grep -n "never read into any value" internal/importer/iis/common.go          # expect no output
grep -n "CookiePolicy" internal/importer/importer.go                          # expect the type and the Options field
rg "\.unravis\.db" .                                                          # expect no output
```

All PASS, or each failure or skip written in `progress.md` (the `TestVerifyMeasure*` tests skip with a reason when there is no real sample).

## Phase-specific additions to plan.md §7

Browser check (step 2) against the wireframe: only the export surface changes in this phase (WP-06b adds the case id to the confirm call; WP-06a and WP-07b add the scrub setting and previewId on the server). In the dev server with the seeded demo case, at 1280 px and 375 px, light and dark, keyboard only once: open the case export, confirm the preview shows, scrub is ON by default, the confirm succeeds, a second confirm after an import in another tab shows the stale-preview message (409), console and network show no error. The toolbar scrub pill (WP-10) is phase 04; list it as `not in this phase`. Record each item as `matches`, `fixed` or `accepted deviation`.

Security canaries (step 3): (a) the sentinel `SENTINEL-7f3a` in query, user agent, non-allowlisted cookie value and referer query/fragment/userinfo is absent from events, report, `GET .../events`, every export format and server logs; (b) a raw allowlisted cookie value (`rawvalue-9x`) is absent everywhere; the key bytes of `cookie.hmac_key_v1` are absent from the manifest, logs, errors and exports; (c) server IP `192.0.2.10` absent, `client-ip` present only with scrub OFF; (d) scrub is the default on the preview, confirm and download paths; (e) a case with no policy (`Options.CookiePolicy == nil`) produces no `cookie:` attr; (f) `TestBuildCaseNoIPsOrUserIDs` green; (g) cookie digest equals `HashCookie` for the same policy and differs for a different key.

Performance (step 4): no absolute millisecond assertions (INV-5). Bounds asserted instead: at most 16 attrs and at most 4 `cookie:` attrs per event; at most 20 cookie pairs and 512 bytes per pair parsed per row; referer input at most 512 bytes; the projection does one pass over each event's attrs; the export confirm reads one snapshot, not two passes. Record the import row rate on `w3c_good.log` scaled to 100,000 rows before and after WP-02b as information only.

## Clumsy-user review (step 5)

One agent tries to break phase 01 and writes tests for what breaks: cookie names with upper case, spaces, `;`, empty string, 5 names, 49-character names; `SetCookieAllowlist` on a closed case; a cookie header of 100 KB and with 21+ pairs; duplicate cookie names; `c-ip` of `-`, `::1`, 65 characters, `1.2.3.4/32`; referer with userinfo, `javascript:`, invalid UTF-8, 300-character path; an HTTPERR file with no `c-ip` column; the same IIS file imported twice (digests equal, no duplicate events); an empty file and a header-only file; a reversed or empty export window and export of an empty case; confirm with a stale previewId, a missing previewId, and a double click on confirm; revision bump on a no-op write (must not bump); allowlist changed between preview and confirm (409).

## Commit

One commit for the phase on `feat/investigation-timeline`, after the gate is clean and the stop is recorded in `progress.md`. Never push, merge or tag. Stage explicit paths only (never `git add -A` or `git add .`). The tree holds about 269 unrelated modified files and `internal/server/case_views.go`, `cases.go`, `export.go` and `int_test.go` already show as modified before this phase: run `git diff --stat` on those four and stage only this phase's hunks (`git add -p`) if they carry unrelated changes.

```
git add .forge/investigation-timeline/ .dev/spec-src/
git add internal/importer/importer.go internal/importer/iis/common.go internal/importer/iis/w3c.go internal/importer/iis/httperr.go internal/importer/iis/privacy_helpers_test.go internal/importer/iis/httperr_clientip_test.go internal/importer/iis/iis_test.go internal/importer/iis/conformance_test.go internal/importer/iis/testdata/w3c_good.log internal/importer/iis/verify_notes.go internal/importer/iis/verify_test.go
git add internal/importer/sqlserver/querystore.go internal/importer/sqlserver/querystore_test.go
git add internal/evidence/event.go internal/evidence/event_test.go internal/evidence/caseexport_enums.go internal/evidence/attrproject.go internal/evidence/attrproject_test.go internal/evidence/caseexport.go internal/evidence/caseexport_verify.go internal/evidence/caseexport_test.go
git add internal/casestore/cookiepolicy.go internal/casestore/cookiepolicy_test.go internal/casestore/memory.go
git add internal/casestore/sqlite/cookiepolicy.go internal/casestore/sqlite/cookiepolicy_test.go internal/casestore/sqlite/cookie_attr_test.go internal/casestore/sqlite/revision.go internal/casestore/sqlite/revision_test.go internal/casestore/sqlite/revision_sites_test.go internal/casestore/sqlite/case.go internal/casestore/sqlite/tags.go
git add internal/server/cases.go internal/server/cookiepolicy_import_test.go internal/server/export.go internal/server/case_views.go internal/server/int_test.go internal/server/case_export_preview_test.go internal/server/case_events_scrub_test.go internal/server/case_revision_test.go internal/server/case_export_snapshot_test.go internal/server/case_export_scruboff_test.go
git add web/src/lib/cases.ts web/src/lib/components/CaseTools.svelte web/tests/import.test.cjs
```

Commit message: `Investigation timeline phase 01: IIS client IP, referer and keyed cookie digests, attribute projection, export scrub and snapshot confirm`.
