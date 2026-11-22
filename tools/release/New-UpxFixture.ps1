<#
.SYNOPSIS
  Writes a tiny PE file with one section for the Test-NoUpx.ps1 self-test. No real UPX is involved.

.DESCRIPTION
  The file has a DOS header, a PE64 header and one empty section header. It is not runnable.
  Default section name is UPX0, which Test-NoUpx.ps1 must reject. Use -SectionName .text for a clean one.
  Run from anywhere:  ./tools/release/New-UpxFixture.ps1 -Path testdata/upx/fake-upx.exe
#>
param(
    [Parameter(Mandatory)] [string]$Path,
    [ValidateLength(1, 8)] [string]$SectionName = 'UPX0'
)
$ErrorActionPreference = 'Stop'

$ms = New-Object System.IO.MemoryStream
$w = New-Object System.IO.BinaryWriter($ms)
# DOS header: "MZ", zeros, e_lfanew = 0x40 at offset 0x3C.
$dos = New-Object byte[] 64
$dos[0] = 0x4D; $dos[1] = 0x5A; $dos[0x3C] = 0x40
$w.Write($dos)
# PE signature and COFF header: machine amd64, 1 section, optional header 0xF0 bytes.
$w.Write([byte[]](0x50, 0x45, 0, 0))
$w.Write([uint16]0x8664); $w.Write([uint16]1)
$w.Write([uint32]0); $w.Write([uint32]0); $w.Write([uint32]0)
$w.Write([uint16]0xF0); $w.Write([uint16]0x0022)
# Optional header: PE32+ magic, rest zero.
$opt = New-Object byte[] 0xF0
$opt[0] = 0x0B; $opt[1] = 0x02
$w.Write($opt)
# Section header: 8-byte name padded with zeros, rest zero (40 bytes in all).
$name = New-Object byte[] 8
[System.Text.Encoding]::ASCII.GetBytes($SectionName).CopyTo($name, 0)
$w.Write($name)
$w.Write((New-Object byte[] 32))
$w.Flush()

$full = if ([System.IO.Path]::IsPathRooted($Path)) { $Path } else { Join-Path (Get-Location).ProviderPath $Path }
$dir = Split-Path -Parent $full
if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir | Out-Null }
[System.IO.File]::WriteAllBytes($full, $ms.ToArray())
Write-Host "Wrote $full ($($ms.Length) bytes, section $SectionName)."
