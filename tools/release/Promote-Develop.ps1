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
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
function Get-Lock {
    # Read-RemoteLock treats only "no lock branch" (404) as free and THROWS on any other failure (network, rate limit, token),
    # so a failed read can never look like a free lock.
    return (Read-RemoteLock $Repo).Record
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
# Gh returns ONE string; a command that prints several lines must be split, or a loop over it runs once on "a`nb".
function Split-Lines([string]$Text) { return @($Text -split "\r?\n" | Where-Object { $_ }) }
$lines = Split-Lines (Gh api "repos/$Repo/compare/${master}...${sha}" --paginate --jq '.commits[] | "- " + (.commit.message | split("\n")[0]) + " (" + .sha[0:7] + ")"')
Write-Host "Promoting develop up to ${short}: $($cmp.ahead) commit(s) not on master; master is $($cmp.behind) commit(s) ahead of that point."
$lines | ForEach-Object { Write-Host "  $_" }

# Every commit on develop's first-parent line (the pull request merges) must be the merge commit of a pull request merged into
# develop: no direct push, no admin bypass. Only the first-parent line is judged, as ReleaseLib.ps1 does on master's side (BL-470),
# so a merge of master into develop, or a pull request branch that has a merge commit inside it, does not stop a promotion.
# This stops it before a promotion pull request exists. The walk ends at the first commit that master already contains.
$unreviewed = @(); $walked = 0; $c = $sha
while ($c) {
    if ([int](Gh api "repos/$Repo/compare/master...$c" --jq '.ahead_by') -eq 0) { break }
    $walked++
    if ($walked -gt 500) { Stop-Here 'More than 500 commits to promote on the first-parent line: promote in steps with -UpTo.' }
    $n = Gh api "repos/$Repo/commits/$c/pulls" --jq "[.[] | select(.merged_at != null and .merge_commit_sha == `"$c`" and .base.ref == `"develop`")] | length"
    if ([int]$n -lt 1) { $unreviewed += $c.Substring(0, 7) }
    $c = Gh api "repos/$Repo/commits/$c" --jq '.parents[0].sha // empty'
}
if ($unreviewed.Count) { Stop-Here "These develop commits are not the merge of a pull request into develop: $($unreviewed -join ', '). Promote up to the last good commit with -UpTo, or fix develop by pull request." }
Write-Host "Every one of the $walked first-parent commit(s) is the merge of a pull request into develop."

$allowMerge = Gh api "repos/$Repo" --jq '.allow_merge_commit'
if ($allowMerge -ne 'true') { Stop-Here 'This repository does not allow merge commits (Settings > General > Pull Requests > Allow merge commits). The promotion must be a merge commit so develop history stays in master. Turn it on once, then run again.' }
if ($DryRun) { Write-Host 'Dry run: nothing created.'; exit 0 }

# --- 4. branch, bring master in, pull request ------------------------------------------------------------------------
$branch = "promote/$short"
$existing = & gh api "repos/$Repo/git/ref/heads/$branch" --jq '.object.sha' 2>$null
if ($LASTEXITCODE -eq 0 -and $existing) {
    # Reuse only a branch that holds the chosen commit (it may also hold master merged in); anything else is somebody else's work.
    $st = Gh api "repos/$Repo/compare/${sha}...${branch}" --jq '.status'
    if ($st -notin 'identical', 'ahead') { Stop-Here "Branch $branch exists but does not contain $short. Delete it on GitHub (or use another -UpTo) and run again." }
    # What it holds beyond the chosen commit may be only commits master already has and this script's own "Merge master into ..."
    # commits; anything else is unreviewed code that would ride into master with the promotion.
    $extra = Split-Lines (Gh api "repos/$Repo/compare/${sha}...${branch}" --paginate --jq '.commits[] | .sha + " " + (.commit.message | split("\n")[0])')
    $foreign = @()
    foreach ($e in $extra) {
        $esha, $emsg = $e -split ' ', 2
        if ($emsg -like "Merge master into $branch*") { continue }
        if ([int](Gh api "repos/$Repo/compare/master...$esha" --jq '.ahead_by') -eq 0) { continue }
        $foreign += $esha.Substring(0, 7)
    }
    if ($foreign.Count) { Stop-Here "Branch $branch has commits that are not on master and not 'Merge master into $branch': $($foreign -join ', '). Delete the branch on GitHub and run again." }
    Write-Host "Branch $branch already exists; reusing it."
}
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
    if ($url -notmatch '/pull/(\d+)') { Stop-Here "Could not read the pull request number from: $url" }
    $pr = [int]$Matches[1]   # gh may print warnings before the address
    Write-Host "Opened pull request #$pr ($url)."
}
if ($NoMerge) { Write-Host 'Stopped before merging (-NoMerge).'; exit 0 }

# --- 5. wait for `build` ---------------------------------------------------------------------------------------------
$until = (Get-Date).AddMinutes($CiWaitMinutes); $state = ''
while ($true) {
    # Judge only the ci.yml run of THIS pull request (event pull_request, on the head commit), never the checks of other events or
    # workflows that happen to sit on the same commit (the push run of develop, the fast gate). Its job `build` is the gate.
    $head = Gh pr view $pr --repo $Repo --json headRefOid --jq '.headRefOid'
    $runs = @(& gh run list --repo $Repo --workflow ci.yml --event pull_request --commit $head --json databaseId,status,createdAt | ConvertFrom-Json | Sort-Object createdAt -Descending)
    if ($runs.Count -and $runs[0].status -eq 'completed') {
        $jobs = (& gh run view $runs[0].databaseId --repo $Repo --json jobs | ConvertFrom-Json).jobs
        $b = @($jobs | Where-Object { $_.name -eq 'build' })
        if ($b.Count -and $b[0].conclusion -eq 'success') { break }
        Stop-Here "build did not pass in the ci.yml run of pull request #$pr (run $($runs[0].databaseId), build: $(if ($b.Count) { $b[0].conclusion } else { 'missing' })). Fix it on develop (or revert the bad commit there), then run again."
    }
    if ((Get-Date) -ge $until) { Stop-Here "build did not finish within $CiWaitMinutes min. Pull request #$pr is left open; run again to carry on." }
    if ($state -ne 'waiting') { Write-Host "Waiting for build on #$pr ..."; $state = 'waiting' }
    Start-Sleep -Seconds 30
}
Write-Host 'build is green.'

# --- 6. lock again, then merge (a release may have started while CI ran) ---------------------------------------------
$lock = Get-Lock
if ($lock -and $lock.state -eq 'held' -and $lock.holdMerges) { Stop-Here "A release took the lock while CI ran (stage $($lock.stage)). Pull request #$pr stays open and green; run again after the release." }
& gh pr merge $pr --repo $Repo --merge --match-head-commit $head 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { Stop-Here "The merge was refused (master may have moved: run again, it brings master in and waits for build once more)." }
Write-Host "Promoted develop up to ${short} to master. The full CI run on master's new commit is the one the release manager waits for." -ForegroundColor Green
