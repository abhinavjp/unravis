<#
.SYNOPSIS
  Promotes develop (or develop up to a chosen commit) to master with ONE pull request and ONE full CI run.
  The release train, docs/ci/RELEASE-TRAIN.md.

.DESCRIPTION
  What it does, in order. Nothing is checked out or changed on this PC: branches and the merge are made on GitHub.
    1. Reads the release lock (read only, never written here). A held lock with merges on hold stops it (or waits, -WaitMinutes).
    2. Picks the commit: the tip of develop, or -UpTo <sha> (a commit that is on develop).
    3. Refuses when master already contains that commit ("nothing to promote").
    4. Creates the branch promote/<sha7> at that commit, merges master into it on GitHub (when master has commits it lacks),
       opens a pull request into master listing every pull request being promoted.
    5. Waits for the required check `build` (the full ci.yml: Windows tests, race, exe build, screenshots, docs, ...).
    6. Merges with a MERGE commit, not a squash, so every develop commit stays in master's history (release notes read it).
  The release manager (Release.ps1) then cuts from master as before; its "CI green on master" wait is on the push run of the merge commit.

.PARAMETER UpTo       Promote develop up to this commit (full or short sha). Default: the tip of develop.
.PARAMETER DryRun     Do steps 1 to 3 and print the plan. Creates nothing.
.PARAMETER NoMerge    Open the pull request and stop (the check runs; merge it yourself or run again).
.PARAMETER WaitMinutes  The lock is held with merges on hold: wait this long (checks every 30 s) instead of refusing. Default 0.
.PARAMETER CiWaitMinutes  How long to wait for `build`. Default 45.
.PARAMETER Repo       owner/name. Default abhinavjp/unravis.
#>
[CmdletBinding()]
param(
    [string]$UpTo = '',
    [switch]$DryRun,
    [switch]$NoMerge,
    [int]$WaitMinutes = 0,
    [int]$CiWaitMinutes = 45,
    [string]$Repo = 'abhinavjp/unravis'
)
$ErrorActionPreference = 'Stop'
function Stop-Here([string]$msg) { Write-Host $msg -ForegroundColor Red; exit 1 }
function Gh { $out = & gh @args 2>&1; if ($LASTEXITCODE -ne 0) { throw "gh $($args -join ' ') failed: $($out | Out-String)" }; ($out | Out-String).Trim() }

& gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Stop-Here 'gh is not signed in (run: gh auth login).' }

# --- 1. the release lock (read only) ---------------------------------------------------------------------------------
function Get-Lock {
    $json = & gh api "repos/$Repo/contents/lock.json?ref=release-lock" --jq '.content' 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $json) { return $null }   # no lock branch yet: nobody can hold it
    $text = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String((($json -join '') -replace '\s', '')))
    return $text | ConvertFrom-Json
}
$deadline = (Get-Date).AddMinutes($WaitMinutes); $said = $false
while ($true) {
    $lock = Get-Lock
    $holding = $lock -and $lock.state -eq 'held' -and $lock.holdMerges
    if (-not $holding) { break }
    if ((Get-Date) -ge $deadline) { Stop-Here "The release lock is held and merges to master are on hold (stage $($lock.stage)). A release is running; promote after it finishes, or pass -WaitMinutes." }
    if (-not $said) { Write-Host "Release lock is held (stage $($lock.stage)); waiting up to $WaitMinutes min."; $said = $true }
    Start-Sleep -Seconds 30
}
Write-Host 'Release lock: free for merges.'

# --- 2. the commit ---------------------------------------------------------------------------------------------------
$dev = Gh api "repos/$Repo/git/ref/heads/develop" --jq '.object.sha'
$master = Gh api "repos/$Repo/git/ref/heads/master" --jq '.object.sha'
if ($UpTo) {
    $sha = Gh api "repos/$Repo/commits/$UpTo" --jq '.sha'
    $cmp = Gh api "repos/$Repo/compare/${sha}...${dev}" --jq '.status'
    if ($cmp -notin 'identical', 'ahead') { Stop-Here "-UpTo $UpTo is not on develop (it is not an ancestor of the develop tip $($dev.Substring(0,7)))." }
} else { $sha = $dev }
$short = $sha.Substring(0, 7)

# --- 3. is there anything to promote? --------------------------------------------------------------------------------
$cmp = Gh api "repos/$Repo/compare/${master}...${sha}" --jq '{ahead: .ahead_by, behind: .behind_by}' | ConvertFrom-Json
if ($cmp.ahead -eq 0) { Write-Host "Nothing to promote: master already contains $short."; exit 0 }
$lines = @(Gh api "repos/$Repo/compare/${master}...${sha}" --paginate --jq '.commits[] | "- " + (.commit.message | split("\n")[0]) + " (" + .sha[0:7] + ")"')
Write-Host "Promoting develop up to ${short}: $($cmp.ahead) commit(s) not on master; master is $($cmp.behind) commit(s) ahead of that point."
$lines | ForEach-Object { Write-Host "  $_" }
if ($DryRun) { Write-Host 'Dry run: nothing created.'; exit 0 }

$allowMerge = Gh api "repos/$Repo" --jq '.allow_merge_commit'
if ($allowMerge -ne 'true') { Stop-Here 'This repository does not allow merge commits (Settings > General > Pull Requests > Allow merge commits). The promotion must be a merge commit so develop history stays in master. Turn it on once, then run again.' }

# --- 4. branch, bring master in, pull request ------------------------------------------------------------------------
$branch = "promote/$short"
$existing = & gh api "repos/$Repo/git/ref/heads/$branch" --jq '.object.sha' 2>$null
if ($LASTEXITCODE -eq 0 -and $existing) { Write-Host "Branch $branch already exists; reusing it." }
else { $null = Gh api "repos/$Repo/git/refs" -f "ref=refs/heads/$branch" -f "sha=$sha" }
if ($cmp.behind -gt 0) {
    $null = & gh api "repos/$Repo/merges" -f "base=$branch" -f 'head=master' -f "commit_message=Merge master into $branch" 2>&1
    if ($LASTEXITCODE -ne 0) { Stop-Here "master could not be merged into $branch on GitHub (a conflict, most likely). Merge origin/master into develop by pull request, resolve it there, then run again. The branch $branch is left for you to delete." }
}
$prList = & gh pr list --repo $Repo --head $branch --base master --state open --json number --jq '.[0].number' 2>$null
if ($prList) { $pr = [int]$prList; Write-Host "Pull request #$pr already open." }
else {
    $body = @"
Promotes develop up to ``$short`` to master (the release train, docs/ci/RELEASE-TRAIN.md). This is the one full CI run for these changes.

Merge it with a merge commit (not a squash): the promotion script does that.

$($lines -join "`n")
"@
    $url = Gh pr create --repo $Repo --base master --head $branch --title "Promote develop $short to master" --body $body
    $pr = [int]($url -replace '.*/', '')
    Write-Host "Opened pull request #$pr ($url)."
}
if ($NoMerge) { Write-Host 'Stopped before merging (-NoMerge).'; exit 0 }

# --- 5. wait for `build` ---------------------------------------------------------------------------------------------
$until = (Get-Date).AddMinutes($CiWaitMinutes); $state = ''
while ($true) {
    $roll = & gh pr view $pr --repo $Repo --json statusCheckRollup,mergeStateStatus | ConvertFrom-Json
    $b = @($roll.statusCheckRollup | Where-Object { $_.name -eq 'build' })
    if ($b.Count -and $b[0].status -eq 'COMPLETED') {
        if ($b[0].conclusion -eq 'SUCCESS') { break }
        Stop-Here "build finished as $($b[0].conclusion) on pull request #$pr. Fix it on develop (or revert the bad commit there), then run again."
    }
    if ((Get-Date) -ge $until) { Stop-Here "build did not finish within $CiWaitMinutes min. Pull request #$pr is left open; run again to carry on." }
    if ($state -ne 'waiting') { Write-Host "Waiting for build on #$pr ..."; $state = 'waiting' }
    Start-Sleep -Seconds 30
}
Write-Host 'build is green.'

# --- 6. lock again, then merge (a release may have started while CI ran) ---------------------------------------------
$lock = Get-Lock
if ($lock -and $lock.state -eq 'held' -and $lock.holdMerges) { Stop-Here "A release took the lock while CI ran (stage $($lock.stage)). Pull request #$pr stays open and green; run again after the release." }
& gh pr merge $pr --repo $Repo --merge 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { Stop-Here "The merge was refused (master may have moved: run again, it brings master in and waits for build once more)." }
Write-Host "Promoted develop up to ${short} to master. The full CI run on master's new commit is the one the release manager waits for." -ForegroundColor Green
