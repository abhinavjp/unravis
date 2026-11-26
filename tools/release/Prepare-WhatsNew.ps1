#Requires -Version 7.3
<#
.SYNOPSIS
  Makes the What's new notes and screenshots for the next release and opens ONE pull request into develop with them.
  Run it once when development for a release is finished, before Promote-Develop.ps1. docs/whatsnew/README.md.

.DESCRIPTION
  Your own working folder is not touched: everything happens in temporary worktrees that are deleted at the end.
    1. Works out the next version from the tags (-Version to say it yourself) and the release before it.
    2. Reads the note files waiting in docs/whatsnew/fragments (written by the pull requests that people can see).
    3. Builds the program from the tip of develop (and the previous release's program when a note asks for a "before" picture).
    4. Takes the screenshots the notes ask for from the demo build (tools/whatsnew/capture.mjs): web/public/whatsnew/<rcNN>/.
    5. Writes the version's entry into web/public/releases/notes.json (the notes plus the plain commit subjects since the last
       release as the full change list) and deletes the used note files (tools/whatsnew/compile-notes.mjs).
    6. Commits only those paths on the branch whatsnew/<version>, pushes it, and opens the pull request into develop.
  The notes file and pictures are inside the program, so the signed update carries them and the existing signature covers them.
  Nothing here touches the signed update list, the keys or the signing scripts.
  Nothing is pushed or opened with -NoPush; with -DryRun nothing is built or created at all.

.PARAMETER Channel   rc (default) or stable. Used only to work out the version.
.PARAMETER Version   X.Y.Z or X.Y.Z-rc.N (no leading v). Default: the next version of the channel from the tags.
.PARAMETER Base      Branch to start from and open the pull request into. Default develop.
.PARAMETER Date      Release date shown in the notes (yyyy-MM-dd). Default today.
.PARAMETER NoPush    Make the branch and commit in the worktree, keep the worktree and print where it is. Nothing leaves this PC.
.PARAMETER DryRun    Print the version, the waiting notes and what would be built. Creates nothing.
.PARAMETER Repo      owner/name. Default abhinavjp/unravis.
#>
[CmdletBinding()]
param(
    [ValidateSet('rc', 'stable')] [string]$Channel = 'rc',
    [string]$Version = '',
    [string]$Base = 'develop',
    [string]$Date = '',
    [switch]$NoPush,
    [switch]$DryRun,
    [string]$Repo = 'abhinavjp/unravis'
)
$ErrorActionPreference = 'Stop'
function Stop-Here([string]$msg) { Write-Host $msg -ForegroundColor Red; exit 1 }
function Invoke-Checked([string]$Exe, [string[]]$Arguments, [string]$What) {
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) { Stop-Here "$What failed (exit $LASTEXITCODE)." }
}
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path

foreach ($t in 'git', 'node', 'npm') { if (-not (Get-Command $t -ErrorAction SilentlyContinue)) { Stop-Here "$t is needed and was not found." } }
if (-not $DryRun -and -not $NoPush) {
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Stop-Here 'gh is needed to open the pull request (or pass -NoPush).' }
    & gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Stop-Here 'gh is not signed in (run: gh auth login).' }
}

Push-Location $repoRoot
$worktrees = @()
try {
    Invoke-Checked git @('fetch', 'origin', $Base, '--tags', '--quiet', '--force') 'git fetch'
    $tags = @(git tag -l 'v*')
    if (-not $Version) { try { $Version = Get-NextVersion $tags $Channel } catch { Stop-Here $_.Exception.Message } }
    try { Assert-NextVersion $Version $tags } catch { Stop-Here $_.Exception.Message }
    $prevVersion = Get-NewestVersion $tags
    $prevTag = "v$prevVersion"
    if (-not $Date) { $Date = (Get-Date).ToString('yyyy-MM-dd') }
    $baseSha = (git rev-parse "origin/$Base").Trim()

    # Waiting notes, read from the base commit (what the pull request will be built on), not from this working folder.
    $fragNames = @(git ls-tree --name-only "$baseSha" 'docs/whatsnew/fragments/' | Where-Object { $_ -like '*.json' })
    $needBefore = $false
    foreach ($f in $fragNames) {
        $j = (git show "${baseSha}:$f") -join "`n" | ConvertFrom-Json
        foreach ($h in @($j.highlights)) { if ($h -and $h.PSObject.Properties['before']) { $needBefore = $true } }
    }
    Write-Host "Next version: $Version (after $prevTag), date $Date."
    Write-Host "Base: origin/$Base at $($baseSha.Substring(0, 7)). Waiting note files: $($fragNames.Count)$(if ($fragNames.Count) { ' (' + (($fragNames | ForEach-Object { Split-Path $_ -Leaf }) -join ', ') + ')' })."
    Write-Host "Previous release's program needed for a 'before' picture: $(if ($needBefore) { 'yes' } else { 'no' })."
    if ($DryRun) { Write-Host 'Dry run: nothing built or created.'; exit 0 }

    $key = $Version -replace '[^0-9A-Za-z.-]', '-'
    $work = Join-Path ([System.IO.Path]::GetTempPath()) "unravis-whatsnew-$key"
    if (Test-Path -LiteralPath $work) { Stop-Here "$work already exists (an earlier run?). Remove it (git worktree remove --force `"$work`") and run again." }
    $branch = "whatsnew/$key"
    & git worktree prune   # forget worktrees whose folder was deleted by hand, so their branch can be reused
    Invoke-Checked git @('worktree', 'add', '-q', '-B', $branch, $work, $baseSha) 'git worktree add'
    $worktrees += $work
    Write-Host "Working in $work on branch $branch."

    # The previous release's program, for "before" pictures. Its web files are committed in its tag, so no web build is needed.
    $beforeExe = ''
    if ($needBefore) {
        $oldDir = Join-Path ([System.IO.Path]::GetTempPath()) "unravis-whatsnew-$key-before"
        if (Test-Path -LiteralPath $oldDir) { Stop-Here "$oldDir already exists. Remove it and run again." }
        Invoke-Checked git @('worktree', 'add', '-q', '--detach', $oldDir, $prevTag) 'git worktree add (previous release)'
        $worktrees += $oldDir
        Push-Location $oldDir
        try { & (Join-Path $oldDir 'build.ps1') -SkipWeb -SkipTests -Unpinned; if (-not $?) { Stop-Here "Building $prevTag failed." } } finally { Pop-Location }
        $beforeExe = Join-Path $oldDir 'dist\unravis.exe'
        if (-not (Test-Path -LiteralPath $beforeExe)) { Stop-Here "Building $prevTag did not make dist\unravis.exe." }
    }

    # The new program, with a fresh web build (generated web files are only rebuilt at promotion, so the base does not have them).
    Push-Location $work
    try {
        & (Join-Path $work 'build.ps1') -SkipTests -Unpinned
        if (-not $?) { Stop-Here 'Building the program failed.' }
        $exe = Join-Path $work 'dist\unravis.exe'
        if (-not (Test-Path -LiteralPath $exe)) { Stop-Here 'The build did not make dist\unravis.exe.' }

        if ($fragNames.Count) {
            Push-Location (Join-Path $work 'tools\screens')
            try {
                Invoke-Checked npm @('ci', '--no-audit', '--no-fund') 'npm ci (tools/screens)'
                Invoke-Checked npx @('playwright', 'install', 'chromium') 'playwright install'
            } finally { Pop-Location }
            $capArgs = @('tools/whatsnew/capture.mjs', '--version', $Version, '--exe', $exe)
            if ($beforeExe) { $capArgs += @('--before-exe', $beforeExe) }
            & node @capArgs
            if ($LASTEXITCODE -ne 0) { Stop-Here "Taking the pictures failed (exit $LASTEXITCODE). A note or a step is wrong, or the tools cannot run; the message above says which." }
        }

        & node tools/whatsnew/compile-notes.mjs --version $Version --date $Date --since-tag $prevTag --consume
        if ($LASTEXITCODE -ne 0) { Stop-Here "Writing the notes failed (exit $LASTEXITCODE)." }

        # Only the paths this step owns. The build made other files (web/dist, cmd/*/ui); they are never committed here.
        $paths = @('docs/whatsnew/fragments', 'web/public/releases/notes.json', 'web/public/whatsnew')
        Invoke-Checked git @('add', '--all', '--') $paths 'git add'
        & git diff --cached --quiet
        if ($LASTEXITCODE -eq 0) { Stop-Here "Nothing to commit: the notes for $Version are already there." }
        $msg = "What's new: notes and pictures for $Version`n`nMade by tools/release/Prepare-WhatsNew.ps1 from the note files in docs/whatsnew/fragments.`n`nCo-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
        Invoke-Checked git @('-c', 'commit.gpgsign=false', 'commit', '-q', '-m', $msg) 'git commit'
        Write-Host "Committed on $branch." -ForegroundColor Green

        if ($NoPush) {
            Write-Host "-NoPush: nothing was pushed. Look at it in $work (branch $branch); remove it with: git worktree remove --force `"$work`"" -ForegroundColor Yellow
            $worktrees = @($worktrees | Where-Object { $_ -ne $work })   # keep this one
        } else {
            & git ls-remote --exit-code --heads origin $branch *> $null
            if ($LASTEXITCODE -eq 0) { Stop-Here "The branch $branch is already on origin (an earlier run). Merge or close its pull request and delete the branch, then run again." }
            Invoke-Checked git @('push', '-u', 'origin', $branch) 'git push'
            $body = @"
## What and why
What's new notes and screenshots for $Version, made by ``tools/release/Prepare-WhatsNew.ps1`` from the note files in ``docs/whatsnew/fragments`` ($($fragNames.Count) used). Full list since ${prevTag}: the commit subjects, in plain words.

## Gate evidence (docs/ci/THREAD-GATE.md)
- Only ``docs/whatsnew/fragments``, ``web/public/releases/notes.json`` and ``web/public/whatsnew`` are committed. No generated files.
- Pictures were taken from the demo build of this commit; "before" pictures (if any) from the $prevTag program.

## Failures that are not from this change
none
"@
            $url = & gh pr create --repo $Repo --base $Base --head $branch --title "What's new: notes and pictures for $Version" --body $body
            if ($LASTEXITCODE -ne 0) { Stop-Here 'The branch is pushed but the pull request could not be opened.' }
            Write-Host "Pull request: $url" -ForegroundColor Green
        }
    } finally { Pop-Location }
} finally {
    foreach ($w in $worktrees) {
        & git worktree remove --force $w 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Warning "Could not remove the temporary folder $w (something may still be using it). Remove it with: git worktree remove --force `"$w`"" }
    }
    Pop-Location
}
