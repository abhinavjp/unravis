<#
.SYNOPSIS
  Promotes develop (or develop up to a chosen commit) to master with ONE pull request and ONE full CI run.
  The release train, docs/ci/RELEASE-TRAIN.md.

.DESCRIPTION
  What it does, in order. Nothing is checked out or changed on this PC: branches and the merge are made on GitHub.
    0. Checks the holds (freeze windows): `unravis-ci hold check --project <Project>`. An active hold stops it with the hold's
       reason, owner and end (exit 5). Holds that cannot be read stop it too: not knowing is not "free". Checked again before the merge.
    1. Reads the release lock (read only, never written here). A held lock with merges on hold stops it (or waits, -WaitMinutes).
    2. Picks the commit: the tip of develop, or -UpTo <sha> (a commit that is on develop).
    3. Refuses when master already contains that commit ("nothing to promote").
    4. Creates the branch promote/<sha7> at that commit, merges master into it on GitHub (when master has commits it lacks).
    4b. Rebuilds the generated web files (web/dist, cmd/unravis-setup/ui, cmd/unravis-lite/ui) on THIS PC with Node 22.23.5 in a
       temporary clone of the promote branch (tools/release/promote-dist.mjs), and pushes ONE extra commit when they differ from what
       the branch holds. Pull requests into develop do not commit those files (docs/pipeline/dist-on-promotion.md). It builds twice and
       runs web/scripts/check-dist.cjs, so a build that does not give the same bytes stops the promotion before any pull request exists.
       Then opens a pull request into master listing every pull request being promoted.
    5. Waits for the required check `build` (the full ci.yml: Windows tests, race, exe build, screenshots, docs, ...).
    6. Merges with a MERGE commit, not a squash, so every develop commit stays in master's history (release notes read it).
  The release manager (Release.ps1) then cuts from master as before; its "CI green on master" wait is on the push run of the merge commit.

.PARAMETER UpTo       Promote develop up to this commit (full or short sha). Default: the tip of develop.
.PARAMETER DryRun     Do steps 0 to 3 and print the plan. Creates nothing.
.PARAMETER NoMerge    Open the pull request and stop (the check runs; merge it yourself or run again).
.PARAMETER WaitMinutes  The lock is held with merges on hold: wait this long (checks every 30 s) instead of refusing. Default 0.
.PARAMETER CiWaitMinutes  How long to wait for `build`. Default 45.
.PARAMETER SkipNotesCheck  Do not stop when What's new notes are still waiting in docs/whatsnew/fragments (see "What's new check" below).
.PARAMETER Repo       owner/name. Default abhinavjp/unravis.
.PARAMETER Project    The project id the holds are checked for (a hold for "all" counts too). Default unravis.
.PARAMETER CiStore    The CI store folder that holds holds.json. Default: the one unravis-ci uses (under LOCALAPPDATA).
.PARAMETER CiExe      The unravis-ci.exe that runs the hold check. Default: built from this checkout (cmd/unravis-ci) into
                      %TEMP%, else unravis-ci.exe on PATH. Without one the promotion is refused.

.NOTES
  What's new check (read only, before anything is created): people-visible changes bring a small note file in docs/whatsnew/fragments/.
  tools/release/Prepare-WhatsNew.ps1 turns the waiting notes into the release's What's new entry and pictures and deletes them. If note
  files are still waiting on the commit being promoted, the release would go out without its What's new, so this stops and says what to run.
  No waiting notes (nothing visible, or already prepared) is fine. -SkipNotesCheck passes anyway. docs/whatsnew/README.md.
#>
[CmdletBinding()]
param(
    [string]$UpTo = '',
    [switch]$DryRun,
    [switch]$NoMerge,
    [switch]$SkipNotesCheck,
    [int]$WaitMinutes = 0,
    [int]$CiWaitMinutes = 45,
    [string]$Repo = 'abhinavjp/unravis',
    [string]$Project = 'unravis',
    [string]$CiStore = '',
    [string]$CiExe = ''
)
$ErrorActionPreference = 'Stop'
# The Node version CI builds the web files with (ci.yml, release.yml, fast-gate.yml: node-version). tools/ci/test-promote-dist.mjs checks they agree.
$DistNodeVersion = '22.23.5'
function Stop-Here([string]$msg) { Write-Host $msg -ForegroundColor Red; exit 1 }
# Not named Gh: PowerShell finds commands case-insensitively, so a function Gh made "& gh" call itself (call depth overflow).
function Invoke-Gh { $out = & gh @args 2>&1; if ($LASTEXITCODE -ne 0) { throw "gh $($args -join ' ') failed: $($out | Out-String)" }; ($out | Out-String).Trim() }

# --- 0. holds (freeze windows) -----------------------------------------------------------------------------------------
# The check is unravis-ci's own (`hold check`, exit 5 while a hold covers the project), so the script and the CLI cannot disagree
# about what "active" means. It runs before anything on GitHub is read or made (so also in -DryRun), and again before the merge
# (a hold may be added while CI runs). Fail closed: an exit other than 0 (free) or 5 (held), or no checker at all, refuses.
function Resolve-HoldChecker {
    $ErrorActionPreference = 'Continue' # go build and the check write to stderr; their exit codes decide, not the stream
    if ($CiExe) {
        if (-not (Test-Path -LiteralPath $CiExe -PathType Leaf)) { Stop-Here "-CiExe $CiExe does not exist, so the holds cannot be checked. Promote is refused." }
        return (Resolve-Path -LiteralPath $CiExe).Path
    }
    $root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
    $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($go -and (Test-Path -LiteralPath (Join-Path $root 'cmd\unravis-ci\main.go'))) {
        $outDir = Join-Path ([System.IO.Path]::GetTempPath()) 'unravis-promote-hold'
        $null = New-Item -ItemType Directory -Force -Path $outDir
        $exe = Join-Path $outDir 'unravis-ci.exe'
        Push-Location $root
        try { $buildOut = & $go.Source build -o $exe ./cmd/unravis-ci 2>&1; $buildExit = $LASTEXITCODE } finally { Pop-Location }
        if ($buildExit -eq 0) { return $exe }
        Write-Host "Could not build unravis-ci from this checkout for the hold check: $($buildOut | Out-String)" -ForegroundColor Yellow
    }
    $onPath = Get-Command unravis-ci -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($onPath) { return $onPath.Source }
    Stop-Here 'No unravis-ci to check the holds with (no Go to build it from this checkout, none on PATH). Promote is refused until the holds can be checked; pass -CiExe <path to unravis-ci.exe>.'
}
$HoldChecker = Resolve-HoldChecker
function Assert-NotHeld([string]$When) {
    $ErrorActionPreference = 'Continue'
    $holdArgs = @('hold', 'check', '--project', $Project)
    if ($CiStore) { $holdArgs += @('--state-dir', $CiStore) }
    $holdOut = (& $HoldChecker @holdArgs 2>&1 | Out-String).Trim()
    $holdExit = $LASTEXITCODE
    if ($holdExit -eq 0) { Write-Host "Holds: none active for $Project ($When)."; return }
    if ($holdExit -eq 5) {
        Write-Host $holdOut -ForegroundColor Red
        Write-Host "Promote refused ($When): a hold is active for $Project. Wait until it ends, or have it lifted (unravis-ci hold lift <id> --reason ...)." -ForegroundColor Red
        exit 5
    }
    Stop-Here "Promote refused ($When): the holds could not be checked (unravis-ci hold check exit $holdExit): $holdOut"
}
Assert-NotHeld 'before start'

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
$dev = Invoke-Gh api "repos/$Repo/git/ref/heads/develop" --jq '.object.sha'
$master = Invoke-Gh api "repos/$Repo/git/ref/heads/master" --jq '.object.sha'
if ($UpTo) {
    $sha = Invoke-Gh api "repos/$Repo/commits/$UpTo" --jq '.sha'
    $cmp = Invoke-Gh api "repos/$Repo/compare/${sha}...${dev}" --jq '.status'
    if ($cmp -notin 'identical', 'ahead') { Stop-Here "-UpTo $UpTo is not on develop (it is not an ancestor of the develop tip $($dev.Substring(0,7)))." }
} else { $sha = $dev }
$short = $sha.Substring(0, 7)

# --- 3. is there anything to promote? --------------------------------------------------------------------------------
$cmp = Invoke-Gh api "repos/$Repo/compare/${master}...${sha}" --jq '{ahead: .ahead_by, behind: .behind_by}' | ConvertFrom-Json
if ($cmp.ahead -eq 0) { Write-Host "Nothing to promote: master already contains $short."; exit 0 }
# Invoke-Gh returns ONE string; a command that prints several lines must be split, or a loop over it runs once on "a`nb".
function Split-Lines([string]$Text) { return @($Text -split "\r?\n" | Where-Object { $_ }) }
$lines = Split-Lines (Invoke-Gh api "repos/$Repo/compare/${master}...${sha}" --paginate --jq '.commits[] | "- " + (.commit.message | split("\n")[0]) + " (" + .sha[0:7] + ")"')
Write-Host "Promoting develop up to ${short}: $($cmp.ahead) commit(s) not on master; master is $($cmp.behind) commit(s) ahead of that point."
$lines | ForEach-Object { Write-Host "  $_" }

# Every commit on develop's first-parent line (the pull request merges) must be the merge commit of a pull request merged into
# develop: no direct push, no admin bypass. Only the first-parent line is judged, as ReleaseLib.ps1 does on master's side (BL-470),
# so a merge of master into develop, or a pull request branch that has a merge commit inside it, does not stop a promotion.
# This stops it before a promotion pull request exists. The walk ends at the first commit that master already contains.
$unreviewed = @(); $walked = 0; $c = $sha
while ($c) {
    if ([int](Invoke-Gh api "repos/$Repo/compare/master...$c" --jq '.ahead_by') -eq 0) { break }
    $walked++
    if ($walked -gt 500) { Stop-Here 'More than 500 commits to promote on the first-parent line: promote in steps with -UpTo.' }
    $n = Invoke-Gh api "repos/$Repo/commits/$c/pulls" --jq "[.[] | select(.merged_at != null and .merge_commit_sha == `"$c`" and .base.ref == `"develop`")] | length"
    if ([int]$n -lt 1) { $unreviewed += $c.Substring(0, 7) }
    $c = Invoke-Gh api "repos/$Repo/commits/$c" --jq '.parents[0].sha // empty'
}
if ($unreviewed.Count) { Stop-Here "These develop commits are not the merge of a pull request into develop: $($unreviewed -join ', '). Promote up to the last good commit with -UpTo, or fix develop by pull request." }
Write-Host "Every one of the $walked first-parent commit(s) is the merge of a pull request into develop."

$allowMerge = Invoke-Gh api "repos/$Repo" --jq '.allow_merge_commit'
if ($allowMerge -ne 'true') { Stop-Here 'This repository does not allow merge commits (Settings > General > Pull Requests > Allow merge commits). The promotion must be a merge commit so develop history stays in master. Turn it on once, then run again.' }

# The web files are rebuilt on this PC (step 4b): Node must be the pinned version and git and npm must be there. Checked now, before
# anything is created on GitHub, so a missing tool never leaves a half-made promotion.
$nodeNow = ''
try { $nodeNow = ((& node --version 2>$null) | Out-String).Trim() } catch { $nodeNow = '' }
if ($nodeNow -ne "v$DistNodeVersion") { Stop-Here "Node $DistNodeVersion is needed to rebuild the web files for the promotion (found: '$nodeNow'). Install it (nvm, fnm, or the installer) and run again." }
foreach ($tool in 'git', 'npm') { if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Stop-Here "$tool is needed to rebuild the web files for the promotion and was not found." } }
Write-Host "Web files: will be rebuilt with Node $DistNodeVersion and committed to the promote branch when they differ (docs/pipeline/dist-on-promotion.md)."
# What's new check: note files that nobody turned into the release's entry yet. Read only; a missing folder is just "none waiting".
$waitingNotes = @()
try { $waitingNotes = @(Split-Lines (Invoke-Gh api "repos/$Repo/contents/docs/whatsnew/fragments?ref=$sha" --jq '.[] | select(.type == "file" and (.name | endswith(".json"))) | .name')) } catch {
    # Only "the folder does not exist" (404) means nothing is waiting. Any other failure (rate limit, network, token) must not look like "none waiting".
    if ($_.Exception.Message -match 'HTTP 404|Not Found') { $waitingNotes = @() }
    else { Stop-Here "Could not check docs/whatsnew/fragments for waiting What's new notes: $($_.Exception.Message) (-SkipNotesCheck to promote without the check.)" }
}
if ($waitingNotes.Count -and -not $SkipNotesCheck) {
    Stop-Here "$($waitingNotes.Count) What's new note file(s) are still waiting in docs/whatsnew/fragments ($($waitingNotes -join ', ')). Run tools\release\Prepare-WhatsNew.ps1, merge the pull request it makes into develop, then promote again. (-SkipNotesCheck to promote without them.)"
}
if ($waitingNotes.Count) { Write-Host "What's new: $($waitingNotes.Count) note file(s) waiting; -SkipNotesCheck given, so they are not blocking." -ForegroundColor Yellow }
else { Write-Host "What's new: no note files waiting." }
if ($DryRun) { Write-Host 'Dry run: nothing created.'; exit 0 }

# --- 4. branch, bring master in, pull request ------------------------------------------------------------------------
$branch = "promote/$short"
$existing = & gh api "repos/$Repo/git/ref/heads/$branch" --jq '.object.sha' 2>$null
if ($LASTEXITCODE -eq 0 -and $existing) {
    # Reuse only a branch that holds the chosen commit (it may also hold master merged in); anything else is somebody else's work.
    # Judged at the commit read above ($existing), not at the branch name, so a push that lands during these checks is not covered by them.
    $st = Invoke-Gh api "repos/$Repo/compare/${sha}...${existing}" --jq '.status'
    if ($st -notin 'identical', 'ahead') { Stop-Here "Branch $branch exists but does not contain $short. Delete it on GitHub (or use another -UpTo) and run again." }
    # What it holds beyond the chosen commit may be only commits master already has and this script's own "Merge master into ..."
    # commits; anything else is unreviewed code that would ride into master with the promotion.
    $extra = Split-Lines (Invoke-Gh api "repos/$Repo/compare/${sha}...${existing}" --paginate --jq '.commits[] | .sha + " " + (.commit.message | split("\n")[0])')
    $foreign = @()
    # The subject "Merge master into ..." is only a claim: anyone with a push token can write it on a commit that carries code. Such a
    # commit is accepted only when it is a CLEAN merge of master (its second parent is on master and its tree is what git's own merge
    # of its parents gives: Test-CleanMasterMerge, the rule Get-PromotionSideCommits applies when the release is signed). One fetch
    # into a throw-away bare repository judges them all.
    $mergeIns = @{}
    $claimed = @($extra | Where-Object { ($_ -split ' ', 2)[1] -like "Merge master into $branch*" } | ForEach-Object { ($_ -split ' ', 2)[0] })
    if ($claimed.Count) {
        $tok = (& gh auth token 2>$null | Out-String).Trim()
        if (-not $tok) { Stop-Here 'No GitHub token (gh auth token) to fetch the branch for the merge check.' }
        try { $mergeIns = Test-RemoteMergeIns "https://github.com/$Repo.git" $branch $claimed $tok } catch { Stop-Here "Could not check the 'Merge master into' commits of ${branch}: $($_.Exception.Message)" }
    }
    # The generated web files commit of an earlier run of this script (step 4b) is judged by the SAME rule as the release manager
    # (Get-PromotionSideCommits in ReleaseLib.ps1): fixed subject, one parent, only files under the three generated folders (case
    # sensitive: -cnotmatch), AND it sits at the tip end of the promote branch: only other generated commits and CLEAN merges of master
    # may come after it. So the first-parent line of the branch is walked from its tip ($existing) down to the chosen commit, and the
    # position rule is Get-BuriedGeneratedCommits (ReleaseLib.ps1, the same function the release manager's rule is written as). A
    # branch that fails it is refused here, before a pull request is opened or merged, instead of being promoted and then refused by
    # Release.ps1 as "Missing". A merge of master counts as one only when Test-RemoteMergeIns (above) found it clean. The commit API
    # pages its file list (read with --paginate) and holds at most 3000 files: more is not trusted.
    $readCommit = {
        param($c)
        $info = Invoke-Gh api "repos/$Repo/commits/$c" --jq '(.parents | map(.sha) | join(" ")) + "|" + (.commit.message | split("\n")[0])'
        $parentText, $subject = "$info" -split '\|', 2
        $isGenerated = $false
        if ("$subject" -clike 'Rebuild web files for promotion*') {
            $gfiles = Split-Lines (Invoke-Gh api "repos/$Repo/commits/$c" --paginate --jq '.files[] | .filename, (.previous_filename // empty)')
            $isGenerated = (Test-GeneratedFileList $gfiles).Ok
        }
        [pscustomobject]@{ Parents = @("$parentText" -split ' ' | Where-Object { $_ }); Subject = "$subject"; IsGenerated = $isGenerated }
    }
    try { $chain = @(Get-PromoteBranchChain ([string]$existing) $sha $readCommit $mergeIns) }
    catch { Stop-Here "Branch ${branch}: $($_.Exception.Message). Delete the branch on GitHub and run again." }
    $okDist = @{}   # sha -> true: generated-files commits on the first-parent line that pass the test, accepted in the loop below
    foreach ($ch in $chain) { if ($ch.Kind -eq 'dist') { $okDist[$ch.Sha] = $true } }
    $buried = @(Get-BuriedGeneratedCommits $chain)
    if ($buried.Count -gt 0) {
        Stop-Here "Branch $branch has the generated web files commit $(($buried | ForEach-Object { "$_".Substring(0, 7) }) -join ', ') BELOW another commit. The release manager accepts that commit only at the tip end of the promote branch (ReleaseLib.ps1 Get-PromotionSideCommits), so this promotion would merge and then be refused as Missing. Delete the branch $branch on GitHub and run again (a fresh branch gets the web files commit on top)."
    }
    foreach ($e in $extra) {
        $esha, $emsg = $e -split ' ', 2
        if ($emsg -like "Merge master into $branch*") {
            if ($mergeIns.ContainsKey($esha) -and $mergeIns[$esha].Ok) { continue }
            $foreign += "$($esha.Substring(0, 7)) ($(if ($mergeIns.ContainsKey($esha)) { $mergeIns[$esha].Why } else { 'not checked' }))"; continue
        }
        # A generated web files commit that passed the test AND the position rule above (it is on the first-parent line).
        if ($okDist.ContainsKey($esha)) { continue }
        if ([int](Invoke-Gh api "repos/$Repo/compare/master...$esha" --jq '.ahead_by') -eq 0) { continue }
        $foreign += $esha.Substring(0, 7)
    }
    if ($foreign.Count) { Stop-Here "Branch $branch has commits that are not on master and not 'Merge master into $branch': $($foreign -join ', '). Delete the branch on GitHub and run again." }
    Write-Host "Branch $branch already exists; reusing it."
    $headAfterMerge = $existing.Trim().ToLowerInvariant()
}
else { $null = Invoke-Gh api "repos/$Repo/git/refs" -f "ref=refs/heads/$branch" -f "sha=$sha"; $headAfterMerge = $sha.ToLowerInvariant() }
if ($cmp.behind -gt 0) {
    $mergeOut = & gh api "repos/$Repo/merges" -f "base=$branch" -f 'head=master' -f "commit_message=Merge master into $branch" 2>&1
    $mergeExit = $LASTEXITCODE
    if ($mergeExit -eq 0) {
        # 201 returns the new merge commit (its first parent must be the head this run checked); 204 (master already in) returns nothing.
        $mj = $null; try { $mj = ($mergeOut | Out-String) | ConvertFrom-Json } catch { $mj = $null }
        if ($mj -and $mj.sha) {
            if (@($mj.parents).Count -ne 2 -or "$($mj.parents[0].sha)".ToLowerInvariant() -ne $headAfterMerge) { Stop-Here "The merge of master into $branch did not build on the head this run checked ($($headAfterMerge.Substring(0, 7))): someone pushed to the branch meanwhile. Nothing was merged. Delete the branch on GitHub if it is not yours, and run again." }
            $headAfterMerge = "$($mj.sha)".ToLowerInvariant()
        }
    }
    if ($mergeExit -ne 0) { Stop-Here "master could not be merged into $branch on GitHub (a conflict, most likely). Merge origin/master into develop by pull request (a merge commit), resolve it there (for the three generated web folders take master's files: git restore --source=origin/master --staged --worktree -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui; it also deletes the files master does not have), then run again. The branch $branch is left for you to delete." }
}
# $headAfterMerge: the head this run starts from (the branch as made or reused, with master merged in), tracked from the commits
# checked above. Nothing may be added to it except by this script's own web files commit (step 4b); the pull request is merged only
# at the head chosen below (SUP-1).
# --- 4b. the generated web files, built once for the whole promotion ------------------------------------------------------
# Runs before the pull request is opened, so its CI run starts once, on the final head. Idempotent: a branch that already holds the
# rebuilt files gets no new commit. A failure here stops the promotion (nothing is merged; the branch is left for the next run).
# The script's own output is shown as it comes and also read: it must end with DIST_COMMIT=none or DIST_COMMIT=<sha>. Without that line
# (Node exited 0 without doing the work) the promotion stops here instead of opening a pull request that check-dist would fail.
$distLines = @(& node (Join-Path $PSScriptRoot 'promote-dist.mjs') --repo $Repo --branch $branch --node-version $DistNodeVersion | ForEach-Object { Write-Host $_; $_ })
$distExit = $LASTEXITCODE
if ($distExit -ne 0) { Stop-Here "Rebuilding the web files for $branch failed (see above). Nothing was merged. Fix the cause and run again; the branch $branch is reused." }
$distDone = @($distLines | Where-Object { "$_" -match '^DIST_COMMIT=(none|[0-9a-f]{40})\s*$' })
if ($distDone.Count -ne 1) { Stop-Here "promote-dist.mjs did not report DIST_COMMIT=none or DIST_COMMIT=<sha>, so it is not known that the web files were rebuilt. Nothing was merged. Run it again, or ask for help; the branch $branch is reused." }

# The one head this run may merge: the commit the web files step pushed, or (nothing to rebuild) the branch as it was before that step.
$distSha = (("$($distDone[0])" -replace '^DIST_COMMIT=', '').Trim())
$expectedHead = if ($distSha -ne 'none') { $distSha } else { $headAfterMerge }
if ($distSha -ne 'none') {
    # The web files commit must sit directly on the head this run checked (or a commit pushed during the build would ride under it) and
    # change only the three generated folders (a renamed file counts with its old name too): refused here, before a pull request, not
    # after the merge by Release.ps1. Assert-DistCommit (ReleaseLib.ps1) throws; there is no condition here to get the wrong way round.
    $distParents = @(Split-Lines (Invoke-Gh api "repos/$Repo/commits/$distSha" --jq '.parents[].sha'))
    $distFiles = Split-Lines (Invoke-Gh api "repos/$Repo/commits/$distSha" --paginate --jq '.files[] | .filename, (.previous_filename // empty)')
    try { Assert-DistCommit $distSha $headAfterMerge $distParents $distFiles $branch } catch { Stop-Here $_.Exception.Message }
}
function Assert-HeadIsExpected([string]$Actual) { try { Assert-PromotionHead $expectedHead $Actual $branch } catch { Stop-Here $_.Exception.Message } }
Assert-HeadIsExpected ((Invoke-Gh api "repos/$Repo/git/ref/heads/$branch" --jq '.object.sha').ToLowerInvariant())

$prList = & gh pr list --repo $Repo --head $branch --base master --state open --json number --jq '.[0].number' 2>$null
if ($prList) { $pr = [int]$prList; Write-Host "Pull request #$pr already open." }
else {
    $body = @"
Promotes develop up to ``$short`` to master (the release train, docs/ci/RELEASE-TRAIN.md). This is the one full CI run for these changes.

Merge it with a merge commit (not a squash): the promotion script does that.

The branch ends with one generated commit, "Rebuild web files for promotion", holding the rebuilt web/dist, cmd/unravis-setup/ui and cmd/unravis-lite/ui (pull requests into develop do not commit them). check-dist in this run proves they equal a fresh build.

$($lines -join "`n")
"@
    $url = Invoke-Gh pr create --repo $Repo --base master --head $branch --title "Promote develop $short to master" --body $body
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
    # GitHub moves a pull request's head a moment after a push: read again (3 reads, 5 s apart) before calling a difference a push by someone else.
    $head = Read-SettledPromotionHead { Invoke-Gh pr view $pr --repo $Repo --json headRefOid --jq '.headRefOid' } $expectedHead
    Assert-HeadIsExpected $head
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
Assert-NotHeld "before merging pull request #$pr; it stays open"
& gh pr merge $pr --repo $Repo --merge --match-head-commit $expectedHead 2>&1 | Out-Host
if ($LASTEXITCODE -ne 0) { Stop-Here "The merge was refused (master may have moved: run again, it brings master in and waits for build once more)." }
Write-Host "Promoted develop up to ${short} to master. The full CI run on master's new commit is the one the release manager waits for." -ForegroundColor Green
