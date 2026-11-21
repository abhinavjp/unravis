<#
.SYNOPSIS
  Fails when a shipped exe was packed with UPX (BL-421).

.DESCRIPTION
  Reads the PE section table of every .exe under -Path (a folder, searched recursively, or one file)
  and fails on a section whose name starts with UPX (UPX0, UPX1, UPX2, UPX!). A packed exe hides its
  code from scanners and trips antivirus, so none may ship. A file that is not a PE is reported as a failure.
  Runs in Windows PowerShell 5.1 and PowerShell 7.

  Run:  ./tools/release/Test-NoUpx.ps1 -Path dist
  Exit code 0 when no exe is packed (and at least one was found), 1 otherwise.
#>
param([Parameter(Mandatory)] [string]$Path)

$ErrorActionPreference = 'Stop'

function Get-PeSectionNames([string]$File) {
    $b = [System.IO.File]::ReadAllBytes($File)
    if ($b.Length -lt 0x40 -or $b[0] -ne 0x4D -or $b[1] -ne 0x5A) { throw 'not a PE file (no MZ header).' }
    $pe = [BitConverter]::ToInt32($b, 0x3C)
    if ($pe -lt 0 -or $pe + 24 -gt $b.Length -or $b[$pe] -ne 0x50 -or $b[$pe + 1] -ne 0x45 -or $b[$pe + 2] -ne 0 -or $b[$pe + 3] -ne 0) { throw 'not a PE file (no PE signature).' }
    $count = [BitConverter]::ToUInt16($b, $pe + 6)
    $optSize = [BitConverter]::ToUInt16($b, $pe + 20)
    $table = $pe + 24 + $optSize
    if ($table + 40 * $count -gt $b.Length) { throw 'section table runs past the end of the file.' }
    for ($i = 0; $i -lt $count; $i++) {
        $raw = [System.Text.Encoding]::ASCII.GetString($b, $table + 40 * $i, 8)
        $raw.TrimEnd([char]0)
    }
}

if (-not (Test-Path -LiteralPath $Path)) { Write-Host "FAIL: $Path does not exist."; exit 1 }
$files = @(if ((Get-Item -LiteralPath $Path).PSIsContainer) { Get-ChildItem -LiteralPath $Path -Recurse -File -Filter *.exe } else { Get-Item -LiteralPath $Path })
if ($files.Count -eq 0) { Write-Host "FAIL: no .exe found under $Path."; exit 1 }

$bad = 0
foreach ($f in $files) {
    try {
        $names = @(Get-PeSectionNames $f.FullName)
        $upx = @($names | Where-Object { $_ -like 'UPX*' })
        if ($upx.Count -gt 0) { $bad++; Write-Host "FAIL: $($f.FullName) is packed with UPX (sections: $($upx -join ', '))." }
        else { Write-Host "ok:   $($f.Name) ($($names.Count) sections, no UPX)" }
    } catch {
        $bad++; Write-Host "FAIL: $($f.FullName): $($_.Exception.Message)"
    }
}
if ($bad) { exit 1 }
exit 0
