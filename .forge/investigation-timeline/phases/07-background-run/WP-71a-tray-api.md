# WP-71a Tray package API and validation
risk: low    size: S    wave: 1    model: haiku
## Outcome
A new package `internal/tray` defines what the notification-area icon needs from the app (a tooltip, a menu, callbacks) as plain Go types with validation, plus a `Run` stub that reports `ErrUnsupported` off Windows. No window or Windows call yet; the Windows pieces are WP-71b to WP-71d.

## Write scope
- `internal/tray/tray.go` (new)
- `internal/tray/tray_other.go` (new)
- `internal/tray/tray_test.go` (new)

## Must not change
- Every file outside `internal/tray`. No new module dependency.

## Changes
1. `tray.go` (package `tray`):
   - `type Item struct { ID uint32; Text string; Checked, Separator, Disabled bool }`.
   - `type Options struct { Tooltip string; Menu func() []Item; OnSelect func(id uint32); OnOpen func(); OnEndSession func(); Logf func(format string, args ...any) }`.
   - `var ErrUnsupported = errors.New("tray: not supported on this platform")`.
   - Constants `MaxItems = 16`, `MaxTextRunes = 64`, `MaxTooltipRunes = 127`.
   - `func ValidateItems(items []Item) error`: at most `MaxItems`; a separator has empty text and `ID` 0; any other item has non-empty text of at most `MaxTextRunes` runes, no control characters, and a non-zero ID that is unique in the list.
   - `func (o Options) validate() error`: `Menu`, `OnSelect` and `OnOpen` must be set; the tooltip is at most `MaxTooltipRunes` runes; `Logf` may be nil (replace by a no-op in a helper `o.logf`).
2. `tray_other.go` (`//go:build !windows`): `func Run(ctx context.Context, o Options) error` returns `ErrUnsupported` after `o.validate()` passes (a validation error wins).
3. Tests in `tray_test.go`: `TestValidateItemsAcceptsAGoodMenu`, `TestValidateItemsRejectsDuplicateAndZeroIDs`, `TestValidateItemsRejectsTooManyItems`, `TestValidateItemsRejectsLongOrEmptyText`, `TestValidateItemsSeparatorMustBeBare`, `TestOptionsValidateRequiresCallbacks`, `TestOptionsValidateLimitsTheTooltip` (a 128-rune tooltip fails, 127 passes, counting runes not bytes). Tests are platform-neutral so they run on Windows and in the linux vet.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/tray -run "TestValidate|TestOptionsValidate" -count=1 -v` -> 7 tests `PASS`.
- `go vet ./internal/tray` -> no output; `$env:GOOS='linux'; go vet ./internal/tray; Remove-Item Env:GOOS` -> no output.

## Depends on
none
