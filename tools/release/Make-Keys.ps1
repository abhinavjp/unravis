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
# ReleaseLib.ps1 sets GOFLAGS='' GOWORK=off GOENV=off GOTOOLCHAIN=auto, so Go settings kept outside git (go env -w, a GOFLAGS overlay,
# a go.work) cannot swap the code of the tool that is about to read the passphrases. It also gives New-ToolWorktree (below).
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue   # the tool would prefer it over the typed passphrase
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$status = git -C $repo status --porcelain
if ($LASTEXITCODE -ne 0) { throw 'git could not read this folder.' }
if ($status) { throw 'This folder has uncommitted changes. Run this from a clean checkout of a commit you reviewed.' }
$head = (git -C $repo rev-parse HEAD).Trim()
$full = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir)
$repoSlash = $repo.TrimEnd('\') + '\'
if (($full.TrimEnd('\') + '\').StartsWith($repoSlash, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'Choose a folder outside the repository for the keys.' }
New-Item -ItemType Directory -Force -Path $full | Out-Null
$tool = Join-Path $full 'sigtool.exe'
Write-Host "Building the key tool from commit $head (a throw-away worktree of that commit, not this folder)"
$work = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-keys-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$wt = $null
try {
    $wt = New-ToolWorktree $repo $head $work
    Copy-Item -LiteralPath $wt.Tool -Destination $tool -Force
} finally {
    if ($wt) { Remove-ToolWorktree $repo $wt.Dir }
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

function New-UpdateKey([string]$role, [string]$name) {
    $keyFile = Join-Path $full "$name.key.json"
    $pubFile = Join-Path $full "$name.pub"
    if ((Test-Path $keyFile) -or (Test-Path $pubFile)) { throw "$name already exists in $full. Not overwriting." }
    $a = Read-Host "Passphrase for $name (12+ characters, plain keyboard characters: letters, digits, punctuation)" -AsSecureString
    $b = Read-Host "Same passphrase again" -AsSecureString
    $pa = [System.Net.NetworkCredential]::new('', $a).Password
    if ($pa.Length -lt 12) { throw 'The passphrase is shorter than 12 characters.' }
    if ($pa -ne [System.Net.NetworkCredential]::new('', $b).Password) { throw 'The two passphrases differ.' }
    try {
        $pa | & $tool keygen -role $role -out $keyFile -pub $pubFile
        if ($LASTEXITCODE -ne 0) { throw "keygen failed for $name. Delete the partial files in $full and run it again." }
    } finally { $pa = $null; $a.Dispose(); $b.Dispose() }
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
