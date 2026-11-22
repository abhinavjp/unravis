<#
.SYNOPSIS
  Reads the PE header of a built exe and fails when a hardening flag is missing (BL-326). Windows or Linux (pure byte reading).
.DESCRIPTION
  Required: DYNAMIC_BASE, HIGH_ENTROPY_VA, NX_COMPAT, GUARD_CF (DllCharacteristics) and CET_COMPAT (the extended DLL characteristics
  entry of the debug directory, type 20, bit 0). ACG, strict handles and the image-load policy are set at run time and are not in the file.
#>
param([Parameter(Mandatory)][string]$Exe)
$ErrorActionPreference = 'Stop'
$b = [System.IO.File]::ReadAllBytes($Exe)
function U16($o) { [BitConverter]::ToUInt16($b, $o) }
function U32($o) { [BitConverter]::ToUInt32($b, $o) }
if ((U16 0) -ne 0x5A4D) { throw "$Exe is not a PE file (no MZ)" }
$pe = [int](U32 0x3C)
if ((U32 $pe) -ne 0x00004550) { throw "$Exe has no PE signature" }
$nsec = [int](U16 ($pe + 6))
$optSize = [int](U16 ($pe + 20))
$opt = $pe + 24
$magic = U16 $opt
if ($magic -ne 0x20B) { throw "$Exe is not a PE32+ image (magic 0x$($magic.ToString('X')))" }
$dll = U16 ($opt + 70)
$missing = @()
foreach ($f in @(@('DYNAMIC_BASE', 0x0040), @('HIGH_ENTROPY_VA', 0x0020), @('NX_COMPAT', 0x0100), @('GUARD_CF', 0x4000))) {
    if (($dll -band $f[1]) -eq 0) { $missing += $f[0] }
}
# Data directory 6 = debug. In PE32+ the directories start at optional header offset 112.
$dbgRva = U32 ($opt + 112 + 6 * 8)
$dbgSize = U32 ($opt + 112 + 6 * 8 + 4)
$cet = $false
if ($dbgRva -ne 0 -and $dbgSize -ne 0) {
    $secTable = $opt + $optSize
    $rawOf = {
        param($rva)
        for ($i = 0; $i -lt $nsec; $i++) {
            $s = $secTable + 40 * $i
            $va = U32 ($s + 12); $vs = U32 ($s + 8); $raw = U32 ($s + 20)
            if ($rva -ge $va -and $rva -lt ($va + $vs)) { return [int]($raw + ($rva - $va)) }
        }
        return -1
    }
    $off = & $rawOf $dbgRva
    if ($off -ge 0) {
        for ($e = 0; $e -lt [int]($dbgSize / 28); $e++) {
            $d = $off + 28 * $e
            $type = U32 ($d + 12)
            if ($type -eq 20) {
                $ptr = [int](U32 ($d + 24))
                if (((U32 $ptr) -band 1) -ne 0) { $cet = $true }
            }
        }
    }
}
if (-not $cet) { $missing += 'CET_COMPAT' }
'{0}: DllCharacteristics=0x{1}, CET_COMPAT={2}' -f $Exe, $dll.ToString('X4'), $cet
if ($missing.Count) { throw "Missing hardening flags in ${Exe}: $($missing -join ', ')" }
'PE hardening flags OK'
