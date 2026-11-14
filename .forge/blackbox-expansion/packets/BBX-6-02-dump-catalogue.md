# BBX-6-02 Crash-dump file catalogue (names only, never content)
Backlog: BL-120 | Group: 6 | Model: performance | Reason: file-system scan with caps and a strict no-content rule.
Depends on: BBX-1-05 | Blocks: none
Reviewer: Ultimate (batch).

## Goal
Catalogue crash-dump FILES (name, size, modified time, target) on the existing 5-minute cadence — never read or copy dump content. The Blackbox never creates dumps.

## Context
- New file `internal/blackbox/dumps.go` (no tag; uses os.ReadDir) + test `internal/blackbox/dumps_test.go` (temp dirs). Group pattern as BBX-2-01 (init RegisterGroup("dumps", true, ...); optional).
- Existing cadence: the log catalogue runs every `logsEvery = 5m` (recorder.go:25, 167-171) and is cached. Mirror that: this group re-scans at most every 5 min (cache last scan time + result; return the cached result on other ticks).
- Scan fixed roots: `C:\Windows\Minidump`, `%LOCALAPPDATA%\CrashDumps`, `%ProgramData%\Microsoft\Windows\WER\ReportArchive` (and ReportQueue). Match `*.dmp` (case-insensitive). Read only DirEntry.Info() (name/size/mtime) — NEVER open file content.
- `DumpsGroup{Dumps []DumpEntry}`; DumpEntry{Name, Size, MTime, Target} (groups_types.go). Target = a process name parsed from the file name when obvious (e.g. `w3wp.exe.1234.dmp` → "w3wp"), else empty. Name is the file name only.
- Caps: depth 2, max 500 visited entries, max 200 dumps listed (newest first). Owner ruling: the Blackbox never creates dumps.
- Rules: **never the content**; **no text** beyond file names/target; **missing never 0**; **light** (caps + 5-min cadence).

## Do
1. `catalogueDumps(roots []string, now) []DumpEntry` — WalkDir/ReadDir with the caps; only d.Info(); skip symlinks; parse target from name; sort newest-first; cap 200. Pure-ish (injectable roots), tested with temp dirs.
2. Group Collect: if now - lastScan < 5m → return cached; else scan, cache, return *DumpsGroup (nil if none).
3. Never open a dump file's content; only stat.

## Do not
- Do NOT read, copy, or create dump content (stat only).
- Do NOT scan beyond the fixed roots/caps; no goroutine/ticker.
- Do NOT emit 0 for "no dumps" (nil group).
- Do NOT include full paths beyond the file name (Name = base name; Target = parsed process name).

## Tests to write first (temp dirs)
- `TestDumpsCatalogue` — fake .dmp files → entries with name/size/mtime/target.
- `TestDumpsNoContent` — a .dmp with secret bytes → entry has size only; assert the code never reads content (the bytes never appear in output).
- `TestDumpsCaps` — >500 visited / >200 dumps capped.
- `TestDumpsCadence` — second call within 5 min returns cached (no re-scan; inject a scan counter).
- `TestDumpsEmptyNil` — no dumps → nil.
- `TestDumpsTargetParse` — "w3wp.exe.1234.dmp" → target "w3wp".

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: caps + 5-min cache; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestDumpsEmptyNil)
- [ ] No text/content captured — names/sizes/times/target only (TestDumpsNoContent)
- [ ] Never creates dumps (code review)
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — owner confirms on a real server with a crash dump that the catalogue lists name/size/time/target and never reads content. Fake tests verify logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-6-02.md`.