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
    # A release whose installed copies must also accept this control file (Release.ps1 passes the release that was on the feed
    # before the one it withdraws). Its trusted keys are checked as well as those of the release now on the feed.
    [string]$TrustBaseline,
    # Keep what the signed control file now in force holds back (pause, withdrawn versions), after checking its signature.
    [switch]$KeepInForce,
    [switch]$AllowBehindMaster,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-control-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
function Invoke-Checked([string]$what, [scriptblock]$cmd) { & $cmd; if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)." } }
$wt = $null
try {
    # The signing tool is built from a clean checkout that matches GitHub, never from a changed working folder.
    if (git -C $repoRoot status --porcelain) { throw 'The working folder has changes. Use a clean checkout of master.' }
    Invoke-Checked 'fetching master' { git -C $repoRoot fetch --quiet origin master }
    if ((git -C $repoRoot rev-parse HEAD) -ne (git -C $repoRoot rev-parse origin/master)) {
        # -AllowBehindMaster (Release.ps1's automatic withdraw): this checkout may be an older commit of master, never anything else.
        # It is not fast-forwarded first, so the scripts that run are the ones the release run started with.
        git -C $repoRoot merge-base --is-ancestor HEAD origin/master
        if (-not $AllowBehindMaster -or $LASTEXITCODE -ne 0) { throw 'This checkout is not the same as origin/master. Run git pull first.' }
    }
    # What installed copies trust today is the release now on the feed. The signing code and key list are shown against it, like Sign-Release.ps1.
    Invoke-Checked 'reading the feed' { gh release download update-feed --repo $Repo --pattern "$Channel.json" --dir $work }
    $prevTag = 'v' + (Get-Content -Raw (Join-Path $work "$Channel.json") | ConvertFrom-Json).version
    git -C $repoRoot rev-parse --verify --quiet "refs/tags/$prevTag^{commit}" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "The feed's current release $prevTag is not in this clone. Run: git fetch --tags" }
    if ($AllowBehindMaster) {
        # An older master commit is fine, but never one older than the release on the feed (the PR check would cover nothing).
        git -C $repoRoot merge-base --is-ancestor $prevTag HEAD
        if ($LASTEXITCODE -ne 0) { throw "This checkout is older than $prevTag, the release on the feed. Run git pull first." }
    }
    $prevKeys = Join-Path $work 'prev_trusted_keys.txt'
    git -C $repoRoot show "${prevTag}:internal/update/trusted_keys.txt" > $prevKeys
    if ($LASTEXITCODE -ne 0) { throw "Could not read the trusted keys of $prevTag." }
    # Same gates as Sign-Release.ps1 (ReleaseLib.ps1): typed words without -Yes, fixed rules with -Yes.
    $keyDir = Split-Path -Parent (Resolve-Path -LiteralPath $KeyFile).Path
    $audit = Join-Path (Get-ReleaseDataDir) 'audit.jsonl'
    # The signing tool is built from a throw-away worktree of this commit, never from the working folder itself.
    $head = (git -C $repoRoot rev-parse HEAD).Trim()
    $wt = New-ToolWorktree $repoRoot $head $work
    $tool = $wt.Tool
    $null = Invoke-SigningGates -RepoRoot $repoRoot -Repo $Repo -Baseline $prevTag -Commit $head -KeyDir $keyDir -Yes:$Yes -Context "control $Channel" -AuditPath $audit -SourceDir $wt.Dir -Tool $tool

    # The feed's JSON names the baseline for every gate; a token holder could edit it. Its signature must hold with the keys of the
    # release it names (an old genuine pair can be replayed, but it then names its own older release, which only widens the checks).
    $feedSig = Join-Path $work "$Channel.json.sig"
    Invoke-Checked 'downloading the feed signature' { gh release download update-feed --repo $Repo --pattern "$Channel.json.sig" --output $feedSig --clobber }
    $named = (& $tool feedsig -in (Join-Path $work "$Channel.json") -sig $feedSig -keys $prevKeys -want $Channel 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or "v$named" -ne $prevTag) { throw "The feed file does not carry a valid signature for $prevTag ($named). Someone may have changed it. Not signing." }

    if ($KeepInForce) {
        $cur = Join-Path $work 'current-control'; New-Item -ItemType Directory -Path $cur | Out-Null
        gh release download update-feed --repo $Repo --pattern "$Channel-control.json*" --dir $cur 2>&1 | Out-Null
        $cj = Join-Path $cur "$Channel-control.json"
        if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $cj) -and (Test-Path -LiteralPath "$cj.sig")) {
            $exp = try { ([datetime](Get-Content -Raw -LiteralPath $cj | ConvertFrom-Json).expires).ToUniversalTime() } catch { [datetime]::MaxValue }
            & $tool verify -control -in $cj -sig "$cj.sig" -keys $prevKeys -channel $Channel -want $Channel 2>&1 | Out-Null
            # Expired: nothing is in force. Not expired but not verifying: stop rather than drop holds that may be real.
            if ($LASTEXITCODE -ne 0 -and $exp -gt (Get-Date).ToUniversalTime()) { throw "The $Channel control file on the feed does not verify with the keys of $prevTag, so its holds cannot be kept safely. Check it, then run without -KeepInForce naming every hold." }
            if ($LASTEXITCODE -eq 0) {
                $old = Get-Content -Raw -LiteralPath $cj | ConvertFrom-Json
                if ($old.pause) { $Pause = $true }
                $Block = @(@($old.blocked) + $Block | Where-Object { $_ } | Select-Object -Unique)
                Write-Host "Keeping the holds in force: pause=$([bool]$old.pause), withdrawn: $($Block -join ', ')."
            } else { Write-Host 'The control file on the feed has expired; nothing to keep from it.' -ForegroundColor Yellow }
        }
    }
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

    $secure = Get-SigningPassphrase -ProtonPassRef $ProtonPassRef -KeyFile $KeyFile -Yes:$Yes
    try { Invoke-WithPassphrase $secure $tool @('sign', '-control', '-key', $KeyFile, '-in', $file, '-out', "$file.sig") } finally { $secure.Dispose() }
    if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
    Invoke-Checked 'verifying with the keys built into this checkout' { & $tool verify -control -in $file -sig "$file.sig" -trusted -channel $Channel -want $Channel }
    Invoke-Checked "verifying with the keys installed copies trust ($prevTag)" { & $tool verify -control -in $file -sig "$file.sig" -keys $prevKeys -channel $Channel -want $Channel }
    if ($TrustBaseline) {
        if ($TrustBaseline -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "-TrustBaseline '$TrustBaseline' is not a release tag." }
        $baseKeys = Join-Path $work 'base_trusted_keys.txt'
        git -C $repoRoot show "${TrustBaseline}:internal/update/trusted_keys.txt" > $baseKeys
        if ($LASTEXITCODE -ne 0) { throw "Could not read the trusted keys of $TrustBaseline." }
        Invoke-Checked "verifying with the keys copies on $TrustBaseline trust" { & $tool verify -control -in $file -sig "$file.sig" -keys $baseKeys -channel $Channel -want $Channel }
    }
    Invoke-Checked 'uploading to the update feed' { gh release upload update-feed $file "$file.sig" --repo $Repo --clobber }
    $back = Join-Path $work 'back'; New-Item -ItemType Directory -Path $back | Out-Null
    Invoke-Checked 'reading back the published pair' { gh release download update-feed --repo $Repo --pattern "$Channel-control.json*" --dir $back }
    Invoke-Checked 'verifying the published pair' { & $tool verify -control -in (Join-Path $back "$Channel-control.json") -sig (Join-Path $back "$Channel-control.json.sig") -keys $prevKeys -channel $Channel -want $Channel }
    Write-Host "Published. Machines pick it up at their next check (within about 30 minutes)." -ForegroundColor Green
} finally {
    if ($wt) { Remove-ToolWorktree $repoRoot $wt.Dir }
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
