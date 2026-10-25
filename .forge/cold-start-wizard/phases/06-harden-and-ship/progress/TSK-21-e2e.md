# TSK-21 item 1: Go end-to-end test

Status: done. New file `internal/server/e2e_wizard_test.go` (package server). No production code changed. Nothing committed.

## Results

- `go vet ./internal/server/`: clean.
- `go test ./internal/server/ -run E2E -count=1`: pass. Also pass at `-count=5` (no flake).
- `go test ./internal/server/ -count=1` (whole package, with the other agents' p06 files present): pass, 22.8 s.
- gofmt clean.

## Harness

- A real SQLite case store plus a real master key session (set up and unlocked over the API), with Argon2 reduced to the test parameters the case-file tests already use.
- The scan runs against the discover package's fake host (the seam `discover_test.go` uses). The fixtures are written into `t.TempDir()`, read back from there, and loaded into the fake file system and the in-memory opener at Windows paths. The scan itself does not read the real disk.
- Fixtures: web-a has readable IIS logs and a log4net app (a rolled file plus the live file). web-b has an IIS-named log that is binary junk, and a log4net app whose log is binary junk. Both web-b findings are demoted by verification.
- Helpers are prefixed `e2e` or are methods on `e2eFix`. They reuse (and do not redeclare) the `discFix`, `cfsPC` and `caseEnv` helpers.

## What is covered

1. `TestE2E_WizardScanCreateTagImport`: trusted scan with verification, create case, PUT environments (`Production`, `  Staging `, blank, `production` become `Production`, `Staging`), import the two verified findings, POST a source tag per source linked to its import record, then assert:
   - events came from the right importers under host `web-a` (6 IIS requests, 2 log4net rate events);
   - the environments, both tags (env, portal, api, kind) and the case counts persist;
   - each tag points at an import with rows;
   - deleting a tag leaves every event;
   - the log holds no passphrase, log text or path.
2. `TestE2E_UnreadableFindingIsNeverImportable`: the junk IIS and junk log4net findings are verified=false with no importer and an unreadable wording key. They are refused with 422 `not-importable` alone and when mixed with a good finding (the whole request is refused, nothing is stored, no limiter slot is held). Locate is checked on the junk log4net finding. Pointed at the web-b folder, the web-b logs folder, a missing folder and the junk IIS folder it stays unverified (reasons `no-log-files`, `wrong-format`, `not-found`, `wrong-format`) and is still refused. Pointed at web-a's readable logs folder it becomes verified, then imports (5 rows). The junk IIS finding stays refused throughout.
3. `TestE2E_ForgedFindingsAreRefused`: a forged id, a real id plus a forged one, and a path sent as an id are refused (400 or 404) and store nothing.
4. `TestE2E_ExportRestoreKeepsEventsAndTags`: wizard-made case, export to a folder, then:
   - the case file holds none of the title, tags, host or log text in plain;
   - a restore with the wrong passphrase is refused (422 `bad-case-file`) and creates no case;
   - the correct restore makes a new case id with the same title;
   - events match field by field (source, kind, host, unit, time zone rule, attributes, start, line);
   - import record count, environments and tags are preserved;
   - the source case is untouched and the passphrase never reaches the log.
5. `TestE2E_NewRoutesRefuseWithoutTokenAndWithForeignOrigin` (17 subtests): locate, probe/reachable, GET case, environments (GET, PUT), sources (GET, POST, DELETE), mongo-sources, master-key status, setup, unlock, lock, rewrap, reset-damaged, cases/export, cases/restore. Each is called with real ids and bodies and gets 401 for no token, a wrong token, a token of the wrong length and a token with a trailing space. It gets 403 for 9 foreign Origins (other host, other port, localhost, https, `null`, trailing path, userinfo, `file://`) and 5 foreign Hosts. Refusal bodies hold no case title, case id, passphrase, path or log text. Afterwards, the environments, tags, case list, export folder (still empty) and `masterkey.dat` bytes are unchanged and the key is still unlocked.
6. `TestE2E_MasterKeySetupRefusesForeignOrigin`: on a PC with no master key, a foreign page cannot set one (no `masterkey.dat`, state stays `none`).
7. `TestE2E_RefusalDoesNotRevealWhichCaseExists`: without a token, an existing case and a missing one give the same 401 body on every case sub-route.

## Findings and defects

No product defect found; no `TestBug_` test was needed.

Observations (not defects):

- Events carry no env, portal or api tags (`eventDTO` has no such fields). Tags live in a separate case-level table. The web wizard posts them with `importId: 0` and kind `discover` (`wizardSubmit.ts` line 283). The task wording "events carry the tags" is therefore asserted at case level: tag rows persist and are linked to the import records whose rows are the events. If per-event tags are a real requirement, they do not exist yet. Also, tags posted with `importId: 0` are not tied to any particular import.
- A fixture file that is denied to the account (`FS.Deny` on a log file) never shows up as a finding at all (the applog detector lists the folder and skips it), so the "cannot open" demotion cannot be reached through a scan on that seam. A junk file gives the demoted-finding case instead (`discover.wrongformat.shows`). The other demotion keys (cannotopen, emptyfile, verifytimeout) are covered by `internal/discover/verify_test.go`.
- log4net lines import as aggregated rate events, so the event count is per minute, not per line (2, not 5).

## Could not be covered

- Real IIS, Mongo, Redis and SQL hosts: UNMEASURED. The scan runs on the fake host; the discover package's real-OS tests are separate.
- A real second PC for case-file restore: restore here runs on the same PC with the same master key, so it makes a copy under a new id. A foreign-key file (different PC) is not exercised here; the unit tests in `case_file_test.go` cover the wrong-key path.
- Mongo sources have no scan fixture, so only the route's auth is tested.
- `probe/reachable` is tested for auth only, not for the SSRF block list or its rate limit (covered by the probe tests).
- Rate-limit and unlock-throttle behaviour under repeated wrong keys is not part of this file.
- `-race` was not run.
