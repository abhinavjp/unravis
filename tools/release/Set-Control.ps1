#Requires -Version 7.2
<#
.SYNOPSIS
  Pauses updates, withdraws a version, or rolls a release out in stages, by publishing a signed control file on the update feed.
  See docs/auto-updater/CONTROL.md.

.EXAMPLES
  # Stop offering anything on the rc channel for now:
  ./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json -Pause -Note 'checking a problem'
  # Withdraw a bad version (machines that have not installed it will not be offered it):
  ./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json -Block 0.1.0-rc.54
  # Let 10 percent of machines take a release, then raise it later by running the script again with a bigger number:
  ./tools/release/Set-Control.ps1 -Channel stable -KeyFile D:\secret\stable-1.key.json -Rollout 0.1.0 -Percent 10
  # Lift everything: run with no switches.
  ./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json

.DESCRIPTION
  Each run replaces the whole control file for the channel, so say everything you want to hold in that one run.
  The file expires after -Days (default 30, at most 90): a forgotten pause ends by itself. It never installs anything; it can
  only hold a release back. Machines that cannot read it carry on as before, or keep the last one they accepted until it expires.
  The passphrase is read like Sign-Release.ps1 does: from Proton Pass with -ProtonPassRef, else typed (hidden).
#>
param(
    [Parameter(Mandatory)] [ValidateSet('rc', 'stable')] [string]$Channel,
    [Parameter(Mandatory)] [string]$KeyFile,
    [switch]$Pause,
    [string[]]$Block = @(),
    [string]$Rollout,
    [int]$Percent = 0,
    [string]$Note = '',
    [ValidateRange(1, 90)] [int]$Days = 30,
    [string]$Repo = 'abhinavjp/unravis',
    [string]$ProtonPassRef,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-control-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
function Invoke-Checked([string]$what, [scriptblock]$cmd) { & $cmd; if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)." } }
try {
    # The signing tool is built from a clean checkout that matches GitHub, never from a changed working folder.
    if (git -C $repoRoot status --porcelain) { throw 'The working folder has changes. Use a clean checkout of master.' }
    Invoke-Checked 'fetching master' { git -C $repoRoot fetch --quiet origin master }
    if ((git -C $repoRoot rev-parse HEAD) -ne (git -C $repoRoot rev-parse origin/master)) { throw 'This checkout is not the same as origin/master. Run git pull first.' }
    # What installed copies trust today is the release now on the feed. The signing code and key list are shown against it, like Sign-Release.ps1.
    Invoke-Checked 'reading the feed' { gh release download update-feed --repo $Repo --pattern "$Channel.json" --dir $work }
    $prevTag = 'v' + (Get-Content -Raw (Join-Path $work "$Channel.json") | ConvertFrom-Json).version
    git -C $repoRoot rev-parse --verify --quiet "refs/tags/$prevTag^{commit}" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "The feed's current release $prevTag is not in this clone. Run: git fetch --tags" }
    $prevKeys = Join-Path $work 'prev_trusted_keys.txt'
    git -C $repoRoot show "${prevTag}:internal/update/trusted_keys.txt" > $prevKeys
    if ($LASTEXITCODE -ne 0) { throw "Could not read the trusted keys of $prevTag." }
    $codeDiff = git -C $repoRoot diff --stat $prevTag HEAD -- tools/release/sigtool internal/update ':!internal/update/trusted_keys.txt'
    if ($codeDiff) { Write-Host "Signing and verifying code changed since ${prevTag}:"; $codeDiff | Write-Host; if ((Read-Host "You have read that diff (git diff) and it is yours. Type 'reviewed' to go on") -ne 'reviewed') { throw 'Stopped. Nothing was signed.' } }
    $keyDiff = git -C $repoRoot diff $prevTag HEAD -- internal/update/trusted_keys.txt
    if ($keyDiff) { Write-Host 'The list of trusted public keys CHANGED:'; $keyDiff | Write-Host; if ((Read-Host "Every added or removed key is one you made or removed on purpose. Type 'keys ok' to go on") -ne 'keys ok') { throw 'Stopped. Nothing was signed.' } }
    $tool = Join-Path $work 'sigtool.exe'
    Push-Location $repoRoot
    try { Invoke-Checked 'building the signing tool' { go build -mod=readonly -o $tool ./tools/release/sigtool } } finally { Pop-Location }

    $file = Join-Path $work "$Channel-control.json"
    if ($Percent -ne 0 -and -not $Rollout) { throw '-Percent needs -Rollout <version>.' }
    $a = @('control', '-channel', $Channel, '-days', $Days, '-out', $file)
    if ($Pause) { $a += '-pause' }
    if ($Block.Count) { $a += @('-block', ($Block -join ',')) }
    if ($Rollout) { $a += @('-rollout', $Rollout, '-percent', $Percent) }
    if ($Note) { $a += @('-note', $Note) }
    Invoke-Checked 'building the control file' { & $tool @a }
    Write-Host ''; Get-Content -Raw $file | Write-Host
    if (-not $Yes -and (Read-Host "Publish this control file for the $Channel channel? Type 'yes' to go on") -ne 'yes') { throw 'Stopped. Nothing was signed or published.' }

    $secure = $null
    if ($ProtonPassRef) {
        try { $secure = Get-ProtonPassSecret -Reference $ProtonPassRef; Write-Host 'Passphrase read from Proton Pass (not shown).' }
        catch { Write-Host "Proton Pass: $($_.Exception.Message)" -ForegroundColor Yellow; Write-Host 'Falling back to typing the passphrase.' -ForegroundColor Yellow }
    }
    if (-not $secure) { $secure = Read-Host "Passphrase for $KeyFile" -AsSecureString }
    $plain = [System.Net.NetworkCredential]::new('', $secure).Password
    try { $plain | & $tool sign -control -key $KeyFile -in $file -out "$file.sig" } finally { $plain = $null; $secure.Dispose() }
    if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
    Invoke-Checked 'verifying with the keys built into this checkout' { & $tool verify -control -in $file -sig "$file.sig" -trusted -channel $Channel -want $Channel }
    Invoke-Checked "verifying with the keys installed copies trust ($prevTag)" { & $tool verify -control -in $file -sig "$file.sig" -keys $prevKeys -channel $Channel -want $Channel }
    Invoke-Checked 'uploading to the update feed' { gh release upload update-feed $file "$file.sig" --repo $Repo --clobber }
    $back = Join-Path $work 'back'; New-Item -ItemType Directory -Path $back | Out-Null
    Invoke-Checked 'reading back the published pair' { gh release download update-feed --repo $Repo --pattern "$Channel-control.json*" --dir $back }
    Invoke-Checked 'verifying the published pair' { & $tool verify -control -in (Join-Path $back "$Channel-control.json") -sig (Join-Path $back "$Channel-control.json.sig") -keys $prevKeys -channel $Channel -want $Channel }
    Write-Host "Published. Machines pick it up at their next check (within about 30 minutes)." -ForegroundColor Green
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
