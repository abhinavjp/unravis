# WP-74a Shortcut writer in its own package
risk: medium    size: M    wave: 1    model: sonnet
## Outcome
The `.lnk` writer and reader move from the installer package into a small shared package, `internal/shelllink`, so the app (start at sign-in from the tray) and the installer create shortcuts with the same code. A new `ReadShortcutArgs` reads back the arguments of a shortcut. The installer keeps compiling and behaving the same: its old names become thin aliases.

## Write scope
- `internal/shelllink/shelllink.go` (new)
- `internal/shelllink/shelllink_test.go` (new)
- `internal/setup/lnk.go`

## Must not change
- The bytes produced for a given `Shortcut` (the round-trip test in `internal/setup/setup_test.go`, `TestShortcutRoundTrip`, must pass unchanged) and every other file.
- No new module dependency.

## Changes
1. Read `internal/setup/lnk.go` first. Move its whole content to `internal/shelllink/shelllink.go` (package `shelllink`) with the same exported names and the same bodies: `type Shortcut struct { Target, Args, WorkDir, Description, Icon string; IconIndex int32 }` (the type is `int32`, keep it), `func (s Shortcut) Bytes() ([]byte, error)`, `func ReadShortcutTarget(data []byte) (string, error)`, and the unexported helpers (`linkInfo`, `ansiPath`, `lnkCLSID`, the `lnkHas*` constants). No other file in the repository uses the unexported helpers.
2. Add `func ReadShortcutArgs(data []byte) (string, error)`. `ReadShortcutTarget` does not read StringData, so write a new walk, with a bounds check before every read (any overrun returns an error, never a panic). Check the header like `ReadShortcutTarget` does (size 0x4C, the CLSID). Then `pos := 0x4C`; if flags bit `0x1` (HasLinkTargetIDList) is set, `pos += 2 + uint16 at pos`; if flags bit `0x2` (HasLinkInfo) is set, `pos += uint32 at pos` (the LinkInfoSize). Then, for each flag in this order, read one string if the flag is set: `0x04` name, `0x08` relative path, `0x10` working directory, `0x20` command-line arguments, `0x40` icon location. A string is a `uint16` character count followed by `count*2` bytes of UTF-16LE when flag `0x80` (IsUnicode) is set, else `count` bytes. Return the arguments string, or `""` when flag `0x20` is not set.
3. `internal/setup/lnk.go` becomes aliases only: `type Shortcut = shelllink.Shortcut`, `var ReadShortcutTarget = shelllink.ReadShortcutTarget`, `var ReadShortcutArgs = shelllink.ReadShortcutArgs`. Nothing else stays in the file.
4. Tests in `shelllink_test.go`: `TestBytesRoundTripsTargetAndArgs` (target, args `--background`, workdir, icon: target and args read back equal), `TestReadShortcutArgsEmptyWhenNoArgs`, `TestReadShortcutArgsRejectsGarbage` (empty slice, 10 random bytes, a valid shortcut cut at half its length), `TestReadShortcutArgsRejectsTruncatedStringData` (a valid shortcut cut one byte inside the arguments string), `TestBytesIsDeterministic` (same input gives the same bytes), `TestUnicodeTargetAndArgsRoundTrip`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/shelllink -count=1 -v` -> 6 tests `PASS`.
- `go test ./internal/setup -run TestShortcutRoundTrip -count=1 -v` -> `PASS`; `go test ./internal/setup -count=1` -> `ok` (record any test that changed, expected none).
- `go build ./...` and `go vet ./internal/shelllink ./internal/setup` -> no output.
- `$env:GOOS='linux'; go vet ./internal/shelllink; Remove-Item Env:GOOS` -> no output (the package has no Windows-only code).

## Depends on
none
