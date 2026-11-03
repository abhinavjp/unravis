# WP-70e Build unravis.exe as a windowless program
risk: medium    size: S    wave: 4    model: haiku
## Outcome
`build.ps1` links `dist\unravis.exe` (and its compat copy `api-audit-explorer.exe`) with `-H=windowsgui`, so double-clicking it opens no console window, and it checks the result: the app exe must have PE subsystem 2 (GUI) while the key-keeper and black-box exes stay at 3 (console). The two developer launch scripts wait for the now-windowless exe to finish, because PowerShell and cmd do not wait for a GUI program on their own.

## Write scope
- `build.ps1`
- `run.ps1`
- `Start Live.cmd`

## Must not change
- Every other file. The key-keeper and black-box builds, the `-X` version flags, the web build, the payload copy and the `-Release` signing steps keep working as they do today.
- `build.ps1` is NOT run by this packet or by the WP-58 gate (it rewrites `cmd/unravis-setup/payload`); proof is static.

## Changes
1. `build.ps1`: right after `$LdFlags` is set add `$GuiLdFlags = "$LdFlags -H=windowsgui"`. Use `$GuiLdFlags` only on the `go build` line that writes `dist\unravis.exe` and, if the compat copy `api-audit-explorer.exe` has its own `go build` line, on that line too (a file copy stays as it is). Read `build.ps1` first and name the lines in the progress note.
2. `build.ps1`: add `function Get-PeSubsystem([string]$Path)`: open with `[System.IO.File]::OpenRead`, read the int32 at offset 0x3C (the PE header offset), then the uint16 at that offset + 24 + 68, return it. After the builds call it on `dist\unravis.exe` (must be 2) and on every other `.exe` that the script builds into `dist` (must be 3); on a mismatch `throw "<file>: PE subsystem <value>, expected <n>"`.
3. `run.ps1`: replace the two lines `& $Executable @Arguments` and the `$LASTEXITCODE` check with the lines below (a windowless exe is not waited for by `&`; the array keeps paths with spaces whole; there is no `-NoNewWindow`, which means nothing for a GUI program):
```powershell
# unravis.exe is a windowless (GUI) program; & would not wait for it.
$quoted = $Arguments | ForEach-Object { '"{0}"' -f $_ }
$p = Start-Process -FilePath $Executable -ArgumentList $quoted -Wait -PassThru
if ($p.ExitCode -ne 0) { throw "Unravis exited with code $($p.ExitCode)." }
```
4. `Start Live.cmd`: change the line that starts the exe to `start "" /wait "%~dp0dist\unravis.exe" --port 0 %*`. The `if errorlevel 1` block keeps working because `start /wait` returns the exit code. Keep the rest.

## Proof
- Parse check (no build): `[System.Management.Automation.Language.Parser]::ParseFile("$PWD\build.ps1",[ref]$null,[ref]$e); $e.Count` -> `0`; same for `run.ps1`.
- `Select-String -Path build.ps1 -Pattern "windowsgui"` -> exactly one line (the `$GuiLdFlags` definition); `Select-String -Path build.ps1 -Pattern "GuiLdFlags"` -> that line plus the `unravis.exe` build line (and the compat copy line only if it is built separately).
- `Select-String -Path build.ps1 -Pattern "Get-PeSubsystem"` -> the function and at least three call sites (app, key-keeper, black box).
- `Select-String -Path run.ps1 -Pattern "Start-Process.*-Wait"` -> one match; `Select-String -Path run.ps1 -Pattern "NoNewWindow"` -> no output; `Select-String -Path "Start Live.cmd" -Pattern 'start "" /wait'` -> one match.
- The built-binary proof (`TestGuiBuildHasTheWindowsSubsystem`) is WP-70f.

## Depends on
WP-70d
