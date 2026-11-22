<#
.SYNOPSIS
  Self-test for Test-NoUpx.ps1: the committed fixture fails, a clean generated PE passes, an empty
  folder and a non-PE file fail. Run: ./tools/release/Test-NoUpx.Tests.ps1  (exit 0 when all hold)
#>
param()
$ErrorActionPreference = 'Stop'
$guard = Join-Path $PSScriptRoot 'Test-NoUpx.ps1'
$gen = Join-Path $PSScriptRoot 'New-UpxFixture.ps1'
$fixture = Join-Path $PSScriptRoot '..\..\testdata\upx\fake-upx.exe'
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("noupx-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$failed = 0
function Check([string]$what, [bool]$ok) { if ($ok) { Write-Host "ok:   $what" } else { $script:failed++; Write-Host "FAIL: $what" } }
try {
    # Each run goes through a child process so the guard's exit code is seen without ending this script.
    $ps = (Get-Process -Id $PID).Path
    function Exit-Of([string]$p) { & $ps -NoProfile -File $guard -Path $p | Out-Null; $LASTEXITCODE }

    Check 'committed fixture (UPX0 section) fails' ((Exit-Of $fixture) -eq 1)

    $regen = Join-Path $tmp 'regen\fake-upx.exe'
    & $gen -Path $regen | Out-Null
    Check 'generator reproduces the committed fixture byte for byte' ([System.IO.File]::ReadAllBytes($regen) -join ',' -eq ([System.IO.File]::ReadAllBytes($fixture) -join ','))

    $clean = Join-Path $tmp 'clean\app.exe'
    & $gen -Path $clean -SectionName '.text' | Out-Null
    Check 'clean PE (.text section) passes' ((Exit-Of (Split-Path -Parent $clean)) -eq 0)

    $mixed = Join-Path $tmp 'mixed'
    New-Item -ItemType Directory -Path $mixed | Out-Null
    Copy-Item $clean (Join-Path $mixed 'a.exe'); Copy-Item $fixture (Join-Path $mixed 'b.exe')
    Check 'folder with one packed exe fails' ((Exit-Of $mixed) -eq 1)

    $empty = Join-Path $tmp 'empty'
    New-Item -ItemType Directory -Path $empty | Out-Null
    Check 'folder with no exe fails' ((Exit-Of $empty) -eq 1)

    $junk = Join-Path $tmp 'junk.exe'
    [System.IO.File]::WriteAllText($junk, 'not an exe')
    Check 'non-PE file fails' ((Exit-Of $junk) -eq 1)
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
if ($failed) { Write-Host "$failed check(s) failed."; exit 1 }
Write-Host 'All Test-NoUpx checks passed.'
exit 0
