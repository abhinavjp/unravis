<#
.SYNOPSIS
  Writes SHA256SUMS.txt for a set of release assets, in the usual "sha256sum" format.

.DESCRIPTION
  One line per file, lower-case hex hash, two spaces, then the bare file name (no path) so the
  file can be checked from inside the same folder the assets were downloaded into:
    Get-FileHash .\unravis-v1.0.0-windows-x64.zip -Algorithm SHA256
  or, where sha256sum is on PATH (Git for Windows, WSL, Linux):
    sha256sum -c SHA256SUMS.txt

.PARAMETER AssetDir
  Folder the named files live in.

.PARAMETER Names
  File names (relative to AssetDir, no path separators) to hash, in the order they should appear.

.PARAMETER OutFile
  Where to write the checksum file.
#>
param(
    [Parameter(Mandatory)] [string]$AssetDir,
    [Parameter(Mandatory)] [string[]]$Names,
    [Parameter(Mandatory)] [string]$OutFile
)

$ErrorActionPreference = 'Stop'

if ($Names.Count -eq 0) { throw 'New-Sha256Sums.ps1: no file names were given.' }

$lines = foreach ($name in $Names) {
    if ($name -match '[\\/]') { throw "New-Sha256Sums.ps1: '$name' must be a bare file name, not a path." }
    $path = Join-Path $AssetDir $name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "New-Sha256Sums.ps1: missing file: $path" }
    $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $name"
}

$lines | Set-Content -Encoding utf8 -LiteralPath $OutFile
Write-Host "Wrote $OutFile ($($Names.Count) file(s))"
