#Requires -Version 7.2
<#
.SYNOPSIS
  Makes the four update signing keys on the owner's own PC (docs/auto-updater/KEYS.md). Free, offline, standard library only.

.DESCRIPTION
  Run it from a clean, reviewed checkout of the repository (no uncommitted changes):
      pwsh ./tools/release/Make-Keys.ps1 -OutDir D:\secret
  It builds the key tool from that commit, then makes rc-1, rc-2, stable-1 and stable-2. For each key it asks for a passphrase
  twice (hidden). Use a different passphrase for each key. The passphrase goes to the tool on standard input only.
  The private keys (*.key.json, encrypted) and public keys (*.pub) are written to -OutDir. The tool refuses to overwrite a file.
  At the end it prints the four public key lines. With -AddToTrusted it also appends them to internal/update/trusted_keys.txt,
  which you then commit and push (public keys are safe to publish).

  Never commit a *.key.json. Put -1 keys on two USB sticks and the -2 keys on a different stick kept somewhere else.
#>
param(
    [Parameter(Mandatory)] [string]$OutDir,
    [switch]$AddToTrusted
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue   # the tool would prefer it over the typed passphrase
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if (git -C $repo status --porcelain) { throw 'This folder has uncommitted changes. Run this from a clean checkout of a commit you reviewed.' }
$head = (git -C $repo rev-parse HEAD).Trim()
$full = [System.IO.Path]::GetFullPath($OutDir)
if ($full.StartsWith($repo, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'Choose a folder outside the repository for the keys.' }
New-Item -ItemType Directory -Force -Path $full | Out-Null
$tool = Join-Path $full 'sigtool.exe'
Write-Host "Building the key tool from commit $head"
Push-Location $repo
try { go build -mod=readonly -o $tool ./tools/release/sigtool; if ($LASTEXITCODE -ne 0) { throw 'go build failed.' } } finally { Pop-Location }

function New-UpdateKey([string]$role, [string]$name) {
    $keyFile = Join-Path $full "$name.key.json"
    $pubFile = Join-Path $full "$name.pub"
    if ((Test-Path $keyFile) -or (Test-Path $pubFile)) { throw "$name already exists in $full. Not overwriting." }
    $a = Read-Host "Passphrase for $name (12+ characters, plain keyboard characters: letters, digits, punctuation)" -AsSecureString
    $b = Read-Host "Same passphrase again" -AsSecureString
    $pa = [System.Net.NetworkCredential]::new('', $a).Password
    if ($pa.Length -lt 12) { throw 'The passphrase is shorter than 12 characters.' }
    if ($pa -ne [System.Net.NetworkCredential]::new('', $b).Password) { throw 'The two passphrases differ.' }
    $pa | & $tool keygen -role $role -out $keyFile -pub $pubFile
    if ($LASTEXITCODE -ne 0) { throw "keygen failed for $name." }
}
New-UpdateKey rc rc-1
New-UpdateKey rc rc-2
New-UpdateKey stable stable-1
New-UpdateKey stable stable-2

$lines = foreach ($n in 'rc-1', 'rc-2', 'stable-1', 'stable-2') { (Get-Content (Join-Path $full "$n.pub") -Raw).Trim() }
Write-Host "`nPublic keys:"; $lines | ForEach-Object { Write-Host "  $_" }
if ($AddToTrusted) {
    $trusted = Join-Path $repo 'internal\update\trusted_keys.txt'
    $existing = Get-Content $trusted -Raw
    if (-not $existing.EndsWith("`n")) { Add-Content -Path $trusted -Value '' }
    foreach ($l in $lines) { if (-not $existing.Contains($l)) { Add-Content -Path $trusted -Value $l } }
    Write-Host "`nAdded to internal\update\trusted_keys.txt. Review it with 'git diff', then commit and push."
} else {
    Write-Host "`nAdd these lines to internal\update\trusted_keys.txt."
}
Write-Host "`nNow back up the *.key.json files: -1 keys on two USB sticks, -2 keys on a different stick kept elsewhere. Never commit them."
