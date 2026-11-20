#Requires -Version 7.2
<#
.SYNOPSIS
  One command for a whole release: checks, tag, wait for the CI build, sign, publish, update the feed, verify on a clean machine.
  Run it on the owner's PC (the signing key never leaves it). See docs/auto-updater/OTA-RESEARCH-AND-PLAN.md section 7.

.DESCRIPTION
      ./tools/release/Release.ps1 -Version 0.1.0-rc.49 -KeyFile D:\secret\rc-1.key.json

  Stages (it stops at the first failure and tells you the -From value that continues from there):
    1 Preflight  Tag        on master, tree clean, up to date with origin, CI green on this commit, keys listed for the channel,
                            tag not used yet, Test-Release.ps1 passes.
    2 Tag        Tag        you type the tag again; it creates the tag and pushes it (this starts the CI build).
    3 Wait       Wait       waits for release.yml: build, build attestation, smoke test on two runners, then a DRAFT release.
    4 Sign       Sign       Sign-Release.ps1 step 1. YOU type the key passphrase and "yes". Nothing else can sign.
    5 Publish    Publish    you type "publish"; the draft becomes a public release.
    6 Feed       Feed       Sign-Release.ps1 -Feed: the signed pointer on the update-feed release moves to this version.
                            From here installed copies are offered the update.
    7 Verify     Verify     starts verify-release.yml on a clean runner and waits (public feed, tamper refusals, upgrade,
                            no downgrade, a standalone recorder updating itself). Needs a previous signed release.

  It never signs in CI, never prints a passphrase or key, and never publishes or tags without the typed words above.
  -DryRun runs only the preflight and prints what it would do. -Channel comes from the tag (a "-" in it means rc).

.PARAMETER Version     X.Y.Z or X.Y.Z-rc.N (no leading v).
.PARAMETER KeyFile     The encrypted private key for this channel (rc-1.key.json or stable-1.key.json).
.PARAMETER From        Continue an earlier run at this stage (Preflight, Tag, Wait, Sign, Publish, Feed, Verify).
.PARAMETER FirstRelease  Only for the first release of a channel (nothing on the feed yet); passed to Sign-Release.ps1.
.PARAMETER Previous    The published tag to upgrade from in the verify stage. Default: the newest earlier release that has a signed manifest.
.PARAMETER SkipVerify  Stop after the feed update. Use it only when you will run verify-release.yml yourself.
.PARAMETER Branch      The branch a release is made from (default master).
#>
param(
    [Parameter(Mandatory)] [string]$Version,
    [string]$KeyFile,
    [ValidateSet('Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify')] [string]$From = 'Preflight',
    [switch]$FirstRelease,
    [string]$Previous,
    [switch]$SkipVerify,
    [switch]$DryRun,
    [string]$Branch = 'master',
    [string]$Repo = 'abhinavjp/unravis'
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue

if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "Version '$Version' is not X.Y.Z or X.Y.Z-pre.N (lower case, no leading v)." }
$tag = "v$Version"
$channel = if ($Version.Contains('-')) { 'rc' } else { 'stable' }
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$order = 'Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify'
function Reach([string]$stage) { $order.IndexOf($stage) -ge $order.IndexOf($From) }
function Fail([string]$stage, [string]$msg) { Write-Host "`nSTOPPED at $stage : $msg" -ForegroundColor Red; Write-Host "Fix it, then continue with: ./tools/release/Release.ps1 -Version $Version -KeyFile <key> -From $stage" -ForegroundColor Yellow; exit 1 }
function Stage([string]$n, [string]$name) { Write-Host "`n[$n/7] $name" -ForegroundColor Cyan }
function Run([string]$what, [scriptblock]$cmd) { $out = & $cmd 2>&1; if ($LASTEXITCODE -ne 0) { throw "$what failed: $($out | Out-String)" }; $out }
function Confirm-Typed([string]$word, [string]$why) {
    Write-Host $why
    $a = Read-Host "Type '$word' to continue, anything else stops"
    if ($a -ne $word) { Write-Host 'Stopped by you. Nothing further was done.'; exit 2 }
}
function Find-Run([string]$workflow, [string]$branchOrTag, [string]$sha, [int]$timeoutSec) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        $runs = gh run list --repo $Repo --workflow $workflow --branch $branchOrTag --limit 10 --json databaseId,headSha,createdAt | ConvertFrom-Json
        $hit = $runs | Where-Object { -not $sha -or $_.headSha -eq $sha } | Sort-Object createdAt -Descending | Select-Object -First 1
        if ($hit) { return $hit.databaseId }
        Start-Sleep -Seconds 5
    }
    return $null
}
Push-Location $repoRoot
try {
    # 1 Preflight -------------------------------------------------------------------------------------------------------------
    if (Reach 'Preflight') {
        Stage 1 'Preflight'
        foreach ($t in 'git', 'gh', 'go') { if (-not (Get-Command $t -ErrorAction SilentlyContinue)) { Fail 'Preflight' "$t is not installed or not on PATH." } }
        & gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Fail 'Preflight' 'gh is not signed in (run: gh auth login).' }
        if ((git rev-parse --abbrev-ref HEAD) -ne $Branch) { Fail 'Preflight' "Not on '$Branch'. Releases are made from $Branch." }
        if (git status --porcelain) { Fail 'Preflight' 'The working tree has changes. Commit or stash them; a release is built from a clean commit.' }
        Run 'git fetch' { git fetch origin $Branch --tags --quiet } | Out-Null
        $head = (git rev-parse HEAD).Trim(); $remote = (git rev-parse "origin/$Branch").Trim()
        if ($head -ne $remote) { Fail 'Preflight' "Local $Branch ($($head.Substring(0,7))) differs from origin/$Branch ($($remote.Substring(0,7))). Pull or push first." }
        $ci = gh run list --repo $Repo --workflow ci.yml --commit $head --json conclusion,status | ConvertFrom-Json
        if (-not ($ci | Where-Object { $_.conclusion -eq 'success' })) { Fail 'Preflight' "No green CI run (ci.yml) on commit $($head.Substring(0,7)). Wait for it or fix it." }
        $keys = Get-Content -LiteralPath (Join-Path $repoRoot 'internal\update\trusted_keys.txt') | Where-Object { $_ -match "^$channel\s+[0-9a-f]{64}\s*$" }
        if (-not $keys) { Fail 'Preflight' "internal/update/trusted_keys.txt has no '$channel' key. Run Make-Keys.ps1 first (docs/auto-updater/KEYS.md); a build with no keys accepts no update." }
        if ((git tag -l $tag) -and $From -eq 'Preflight') { Fail 'Preflight' "Tag $tag already exists locally." }
        if (gh api "repos/$Repo/git/ref/tags/$tag" 2>$null) { if ($From -in 'Preflight', 'Tag') { Fail 'Preflight' "Tag $tag already exists on GitHub." } }
        $existing = (gh release list --repo $Repo --limit 100 --json tagName | ConvertFrom-Json).tagName
        if ($From -eq 'Preflight' -and $existing -contains $tag) { Fail 'Preflight' "A release $tag already exists." }
        if (-not $KeyFile -and -not $DryRun) { Fail 'Preflight' 'Pass -KeyFile (the encrypted private key for this channel).' }
        if ($KeyFile -and -not (Test-Path -LiteralPath $KeyFile)) { Fail 'Preflight' "Key file not found: $KeyFile" }
        & (Join-Path $PSScriptRoot 'Test-Release.ps1') | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail 'Preflight' 'Test-Release.ps1 failed.' }
        Write-Host "Preflight OK: $Branch at $($head.Substring(0,7)), CI green, $channel key listed, $tag is free." -ForegroundColor Green
        if ($DryRun) {
            Write-Host "`nDRY RUN. Would: tag+push $tag, wait for the CI draft, sign (you type the passphrase), publish, update the $channel feed, verify on a clean runner." -ForegroundColor Yellow
            exit 0
        }
    }
    # 2 Tag -------------------------------------------------------------------------------------------------------------------
    if (Reach 'Tag') {
        Stage 2 'Tag and push'
        $head = (git rev-parse HEAD).Trim()
        if (-not (git tag -l $tag)) {
            Confirm-Typed $tag "This creates tag $tag on $($head.Substring(0,7)) ($(git log -1 --format=%s)) and pushes it. CI then builds a DRAFT release."
            Run 'git tag' { git tag $tag $head } | Out-Null
            Run 'git push tag' { git push origin $tag } | Out-Null
            Write-Host "Pushed $tag."
        } else { Write-Host "Tag $tag already exists locally; continuing." }
    }
    # 3 Wait ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Wait') {
        Stage 3 'Waiting for the CI build, attestation, smoke test and draft'
        $sha = (git rev-list -n 1 $tag).Trim()
        $id = Find-Run 'release.yml' $tag $sha 180
        if (-not $id) { Fail 'Wait' 'The release.yml run did not start within 3 minutes. Check the Actions tab.' }
        gh run watch $id --repo $Repo --exit-status --interval 20 | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail 'Wait' "release.yml run $id failed: https://github.com/$Repo/actions/runs/$id" }
        $rel = gh release view $tag --repo $Repo --json isDraft 2>$null | ConvertFrom-Json
        # a draft is not always found by "view"; list instead (same trick release.yml uses)
        $drafts = gh release list --repo $Repo --limit 30 --json tagName,isDraft | ConvertFrom-Json | Where-Object { $_.tagName -eq $tag }
        if (-not $drafts) { Fail 'Wait' "CI finished but no release $tag was found." }
        Write-Host "CI finished. Draft release $tag is ready." -ForegroundColor Green
    }
    # 4 Sign ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Sign') {
        Stage 4 'Sign (you type the passphrase and yes)'
        if (-not $KeyFile) { Fail 'Sign' 'Pass -KeyFile.' }
        $a = @{ Tag = $tag; KeyFile = $KeyFile }
        if ($FirstRelease) { $a.FirstRelease = $true }
        & (Join-Path $PSScriptRoot 'Sign-Release.ps1') @a
        if ($LASTEXITCODE -ne 0) { Fail 'Sign' 'Sign-Release.ps1 did not finish.' }
    }
    # 5 Publish ---------------------------------------------------------------------------------------------------------------
    if (Reach 'Publish') {
        Stage 5 'Publish the release'
        $notes = gh release view $tag --repo $Repo --json body --jq .body 2>$null
        if ($notes) { Write-Host ($notes -split "`n" | Select-Object -First 15 | Out-String) }
        Confirm-Typed 'publish' "This makes $tag public ($channel channel). Nobody is offered it until the feed step."
        Run 'publish' { gh release edit $tag --repo $Repo --draft=false } | Out-Null
        Write-Host "$tag is published." -ForegroundColor Green
    }
    # 6 Feed ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Feed') {
        Stage 6 'Update the signed feed'
        $a = @{ Tag = $tag; Feed = $true }
        if ($FirstRelease) { $a.FirstRelease = $true }
        & (Join-Path $PSScriptRoot 'Sign-Release.ps1') @a
        if ($LASTEXITCODE -ne 0) { Fail 'Feed' 'Sign-Release.ps1 -Feed did not finish.' }
        Write-Host "The $channel feed now points at $tag. Installed copies will be offered it at their next check." -ForegroundColor Green
    }
    # 7 Verify ----------------------------------------------------------------------------------------------------------------
    if (Reach 'Verify') {
        if ($SkipVerify) { Write-Host "`nVerify skipped by -SkipVerify. Run the verify-release workflow yourself." -ForegroundColor Yellow; exit 0 }
        Stage 7 'Verify on a clean machine (verify-release.yml)'
        if (-not $Previous -and -not $FirstRelease) {
            # newest earlier published release on the same kind of channel that carries a signed manifest
            foreach ($r in (gh release list --repo $Repo --limit 50 --json tagName,isDraft,publishedAt | ConvertFrom-Json | Where-Object { -not $_.isDraft -and $_.tagName -match '^v[0-9]' -and $_.tagName -ne $tag } | Sort-Object publishedAt -Descending)) {
                $assets = (gh release view $r.tagName --repo $Repo --json assets --jq '.assets[].name' 2>$null)
                if ($assets -contains 'update-manifest.json.sig') { $Previous = $r.tagName; break }
            }
        }
        $wf = @('workflow', 'run', 'verify-release.yml', '--repo', $Repo, '--ref', $Branch, '-f', "tag=$tag")
        if ($Previous) { $wf += @('-f', "previous=$Previous"); Write-Host "Upgrading from $Previous." } else { Write-Host 'No earlier signed release: part A only (feed, offline verify, tamper refusals).' }
        $before = Get-Date
        Run 'start verify' { gh @wf } | Out-Null
        Start-Sleep -Seconds 10
        $id = $null
        $deadline = (Get-Date).AddMinutes(2)
        while (-not $id -and (Get-Date) -lt $deadline) {
            $id = (gh run list --repo $Repo --workflow verify-release.yml --limit 5 --json databaseId,createdAt | ConvertFrom-Json | Where-Object { [datetime]$_.createdAt -ge $before.ToUniversalTime().AddSeconds(-30) } | Sort-Object createdAt -Descending | Select-Object -First 1).databaseId
            if (-not $id) { Start-Sleep -Seconds 5 }
        }
        if (-not $id) { Fail 'Verify' 'The verify run did not start.' }
        gh run watch $id --repo $Repo --exit-status --interval 30 | Out-Host
        if ($LASTEXITCODE -ne 0) {
            Write-Host "`nVERIFY FAILED: https://github.com/$Repo/actions/runs/$id" -ForegroundColor Red
            Write-Host "The release is already public and on the feed. A pause switch is not built yet (BL-155). Decide now: publish a fixed newer version, or ask Claude for the pause." -ForegroundColor Red
            exit 1
        }
        Write-Host "`nVerified on a clean machine: https://github.com/$Repo/actions/runs/$id" -ForegroundColor Green
    }
    Write-Host "`nRelease $tag is done." -ForegroundColor Green
    exit 0
} finally { Pop-Location }
