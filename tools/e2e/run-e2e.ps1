# Runs the end-to-end suite against a built demo exe.
#   -Exe <path>     the exe to test (default: the prebuilt demo, or UNRAVIS_EXE if set)
#   -Only <ids>     scenario ids to run, for example CASE-10,SEC-08 (matches the start of a test title)
#   -Headed         show the browser
# The JSON report goes to report/results.json (git-ignored).
param(
  [string]$Exe = '',
  [string[]]$Only = @(),
  [switch]$Headed
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

if ($Exe -ne '') {
  if (-not (Test-Path -LiteralPath $Exe)) { throw "The exe was not found: $Exe" }
  $env:UNRAVIS_EXE = (Resolve-Path -LiteralPath $Exe).Path
}

New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot 'report') | Out-Null

$args2 = @('playwright', 'test')
if ($Only.Count -gt 0) {
  $ids = @()
  foreach ($chunk in $Only) { $ids += ($chunk -split '[,\s]+' | Where-Object { $_ -ne '' }) }
  $pattern = '\b(' +(($ids | ForEach-Object { [regex]::Escape($_) }) -join '|') + '):'
  $args2 += @('--grep', $pattern)
}
if ($Headed) { $args2 += '--headed' }

& npx @args2
exit $LASTEXITCODE
