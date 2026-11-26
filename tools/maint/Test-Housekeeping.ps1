<#
.SYNOPSIS  Tests Housekeeping.ps1 (and the owner filter it adds to Clean-Scratch.ps1) on a throwaway tree. Touches nothing outside it. Exit 1 on any failure.
#>
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$hk = Join-Path $here 'Housekeeping.ps1'
. (Join-Path $here 'Clean-Scratch.ps1')
. $hk

$fail = 0
function Check { param([bool]$Cond, [string]$Name) if ($Cond) { Write-Host "ok    $Name" } else { Write-Host "FAIL  $Name"; $script:fail++ } }
function G { param([string]$Dir) $o = & git -c user.email=t@t -c user.name=t -c protocol.file.allow=always -C $Dir @args 2>&1; if ($LASTEXITCODE -ne 0) { throw "git $args failed: $o" }; return $o }

$base = Join-Path ([IO.Path]::GetTempPath()) ('hk-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$local = Join-Path $base 'Local'
$temp = Join-Path $local 'Temp'
$claude = Join-Path $temp 'claude'
$home2 = Join-Path $base 'Home'
$projects = Join-Path $home2 '.claude\projects'
$scratch = Join-Path $base 'Scratch'
$origin = Join-Path $base 'origin.git'
$repo = Join-Path $base 'repo'
$now = [datetime]::UtcNow
$clock = $now.AddDays(3)   # everything real is "old" by then; things meant to be recent are stamped just before $clock

function Set-All {
    param([string]$Path, [datetime]$When)
    foreach ($i in @(Get-ChildItem -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue | Sort-Object { $_.FullName.Length } -Descending)) { $i.CreationTimeUtc = $When; $i.LastWriteTimeUtc = $When }
    $d = Get-Item -LiteralPath $Path -Force; $d.CreationTimeUtc = $When; $d.LastWriteTimeUtc = $When
}
function Set-Worktree-Time {
    param([string]$Wt, [datetime]$When)
    $m = [regex]::Match((Get-Content -LiteralPath (Join-Path $Wt '.git') -Raw), 'gitdir:\s*(.+)')
    if ($m.Success) { Set-All $m.Groups[1].Value.Trim() $When }
    Set-All $Wt $When
}
function New-Session {
    param([string]$Hash, [string]$Id, [bool]$Mine, [datetime]$ScratchTime, [datetime]$TranscriptTime)
    $d = Join-Path $claude "$Hash\$Id"
    New-Item -ItemType Directory -Force -Path (Join-Path $d 'scratchpad') | Out-Null
    [IO.File]::WriteAllBytes((Join-Path $d 'scratchpad\a.bin'), (New-Object byte[] 4096))
    Set-All $d $ScratchTime
    if ($Mine) {
        $f = Join-Path $projectDir "$Id.jsonl"
        Set-Content -LiteralPath $f -Value '{}'
        (Get-Item -LiteralPath $f).LastWriteTimeUtc = $TranscriptTime
    }
    return $d
}
function Run { param([bool]$Apply, [string[]]$Running = @(), [string[]]$Protect = @()) Invoke-HousekeepingRun -Repo $repo -DoApply $Apply -Clock $clock -TempRoot $temp -ProjectsRoot $projects -ScratchRoots @($scratch) -Running $Running -Protect $Protect -GoCacheLimitGB 0 6>$null }

try {
    # ---- a real repository with a remote, develop and master, and registered worktrees
    New-Item -ItemType Directory -Force -Path $claude, $projects, $scratch | Out-Null
    & git init --bare -q -b develop $origin
    & git clone -q $origin $repo 2>&1 | Out-Null
    G $repo checkout -q -b develop | Out-Null
    Set-Content -LiteralPath (Join-Path $repo 'a.txt') -Value 'a'
    G $repo add a.txt | Out-Null; G $repo commit -q -m init | Out-Null
    G $repo push -q origin develop | Out-Null; G $repo push -q origin develop:master | Out-Null
    $projectDir = Join-Path $projects (ConvertTo-ProjectName $repo)
    New-Item -ItemType Directory -Force -Path $projectDir | Out-Null
    Check ((ConvertTo-ProjectName 'D:\Projects\api-audit-explorer') -ceq 'D--Projects-api-audit-explorer') 'project folder name matches the Claude convention'

    # worktrees
    $wtMerged = Join-Path $scratch 'wt-merged'; G $repo worktree add -q -b b-merged $wtMerged | Out-Null
    $wtDirty = Join-Path $scratch 'wt-dirty'; G $repo worktree add -q -b b-dirty $wtDirty | Out-Null
    Set-Content -LiteralPath (Join-Path $wtDirty 'new.txt') -Value 'untracked'
    $wtUnmerged = Join-Path $scratch 'wt-unmerged'; G $repo worktree add -q -b b-unmerged $wtUnmerged | Out-Null
    Set-Content -LiteralPath (Join-Path $wtUnmerged 'u.txt') -Value 'u'; G $wtUnmerged add u.txt | Out-Null; G $wtUnmerged commit -q -m work | Out-Null
    $wtActive = Join-Path $scratch 'wt-active'; G $repo worktree add -q -b b-active $wtActive | Out-Null
    $wtRecent = Join-Path $scratch 'wt-recent'; G $repo worktree add -q -b b-recent $wtRecent | Out-Null
    $wtOutside = Join-Path $base 'elsewhere-wt'; G $repo worktree add -q -b b-outside $wtOutside | Out-Null
    foreach ($w in $wtMerged, $wtDirty, $wtUnmerged, $wtActive, $wtOutside) { Set-Worktree-Time $w $now.AddDays(-2) }
    Set-Worktree-Time $wtRecent $clock.AddHours(-1)

    # sessions: mine (old, live transcript, no transcript), another repository's, and a second hash dir
    $sOld = New-Session 'hashA' 'aaaaaaaa-old' $true $now.AddDays(-2) $now.AddDays(-2)
    $sLive = New-Session 'hashA' 'bbbbbbbb-live' $true $now.AddDays(-2) $clock.AddMinutes(-10)
    $sNoTr = New-Session 'hashA' 'cccccccc-nomap' $false $now.AddDays(-2) $now
    $sOther = New-Session 'hashB' 'dddddddd-other' $false $now.AddDays(-2) $now
    $sRun = New-Session 'hashB' 'eeeeeeee-run' $true $now.AddDays(-2) $now.AddDays(-2)
    $otherProj = Join-Path $projects 'D--Some-Other-Repo'; New-Item -ItemType Directory -Force -Path $otherProj | Out-Null
    Set-Content -LiteralPath (Join-Path $otherProj 'ffffffff-foreign.jsonl') -Value '{}'
    $sForeign = New-Session 'hashB' 'ffffffff-foreign' $false $now.AddDays(-2) $now
    # temp pattern folders
    $tProbe = Join-Path $temp 'unravis-probe-1'; New-Item -ItemType Directory -Force -Path $tProbe | Out-Null; [IO.File]::WriteAllBytes((Join-Path $tProbe 'x.bin'), (New-Object byte[] 4096)); Set-All $tProbe $now.AddDays(-2)
    $tDevRun = Join-Path $temp 'unravis-dev-live'; New-Item -ItemType Directory -Force -Path $tDevRun | Out-Null; Set-All $tDevRun $now.AddDays(-2)
    $tUci = Join-Path $temp 'uci-keepme'; New-Item -ItemType Directory -Force -Path $tUci | Out-Null; Set-All $tUci $now.AddDays(-9)
    $tOtherTemp = Join-Path $temp 'something-else'; New-Item -ItemType Directory -Force -Path $tOtherTemp | Out-Null; Set-All $tOtherTemp $now.AddDays(-9)

    $script:ProcessLinesOverride = @('x')   # the re-check after a rename reads this, not the real process list
    $running = @("C:\tool\x.exe --cwd $wtActive", 'node server.js --dir ' + $tDevRun, 'claude.exe --resume eeeeeeee-run')

    # ---- dry run changes nothing
    $r = Run $false $running
    Check ((Test-Path $sOld) -and (Test-Path $wtMerged) -and (Test-Path $tProbe)) 'dry run deletes nothing'
    Check ($r.ScratchWould -gt 0 -and $r.WtWould -ge 1 -and $r.TempWould -gt 0) 'dry run reports what it would free'

    # ---- real run
    $r = Run $true $running
    Check (-not (Test-Path $sOld)) 'old session of this repo with an old transcript is deleted'
    Check (Test-Path $sLive) 'old scratch is KEPT when its transcript changed within the idle window'
    Check (Test-Path $sNoTr) 'session without a transcript in this repo is kept'
    Check (Test-Path $sOther) 'session in another hash folder with no transcript is untouched'
    Check (Test-Path $sForeign) "another repository's session (transcript in another project folder) is untouched"
    Check (Test-Path $sRun) 'a session named by a running process is kept'
    Check (-not (Test-Path $wtMerged)) 'merged, clean, idle worktree is removed'
    Check ((Test-Path $wtDirty) -and (Test-Path (Join-Path $wtDirty 'new.txt'))) 'worktree with an untracked file is kept'
    Check (Test-Path $wtUnmerged) 'worktree with unmerged commits is kept'
    Check (Test-Path $wtActive) 'worktree named by a running process is kept'
    Check (Test-Path $wtRecent) 'recently touched worktree is kept'
    Check (Test-Path $wtOutside) 'worktree outside the scratch folders is kept'
    Check (Test-Path $repo) 'main checkout is kept'
    Check ((G $repo branch --list b-merged) -match 'b-merged') 'the branch of a removed worktree still exists'
    Check (-not (Test-Path $tProbe)) 'unravis-probe folder is deleted'
    Check (Test-Path $tDevRun) 'unravis-dev folder named by a running process is kept'
    Check ((Test-Path $tUci) -and (Test-Path $tOtherTemp)) 'uci-* and unrelated Temp folders are untouched'
    Check ($r.ScratchFreed -gt 0 -and $r.WtRemoved -eq 1) 'run reports what it freed'

    # ---- idempotent: a second run finds nothing more
    $r2 = Run $true $running
    Check ($r2.ScratchFreed -eq 0 -and $r2.WtRemoved -eq 0 -and $r2.TempFreed -eq 0) 'second run changes nothing'

    # ---- the checkout in use is protected even when it would qualify
    $wtSelf = Join-Path $scratch 'wt-self'; G $repo worktree add -q -b b-self $wtSelf | Out-Null; Set-Worktree-Time $wtSelf $now.AddDays(-2)
    $null = Run $true $running @($wtSelf)
    Check (Test-Path $wtSelf) 'the checkout that runs the script is never removed'

    # ---- fetch failure keeps everything
    $wtF = Join-Path $scratch 'wt-fetch'; G $repo worktree add -q -b b-fetch $wtF | Out-Null; Set-Worktree-Time $wtF $now.AddDays(-2)
    G $repo remote set-url origin (Join-Path $base 'missing.git') | Out-Null
    $null = Run $true $running
    Check (Test-Path $wtF) 'a failed fetch keeps the worktree'
    G $repo remote set-url origin $origin | Out-Null

    # ---- owner filter edge cases
    $script:ProcessLinesOverride = $null
    Check ([bool](Get-OwnerVerdict -Id 'x' -OwnerDirs @() -IdleMinutes 5 -Clock $clock)) 'an empty owner list keeps every session'
    Check ($null -eq (Get-OwnerVerdict -Id 'x' -OwnerDirs $null -IdleMinutes 5 -Clock $clock)) 'no owner list means no owner filter (Clean-Scratch alone)'

    # ---- the script itself: silence, exit 0, throttle, lock, real end-to-end run on the throwaway tree
    $pw = (Get-Command pwsh).Source
    $saveL = $env:LOCALAPPDATA; $saveU = $env:USERPROFILE
    $saveG = $env:GOCACHE; $env:GOCACHE = Join-Path $base 'no-go-cache'
    $env:LOCALAPPDATA = $local; $env:USERPROFILE = $home2
    try {
        $state = Join-Path $local 'Unravis\maint'
        New-Item -ItemType Directory -Force -Path $state | Out-Null
        Set-Content -LiteralPath (Join-Path $state 'housekeeping.state') -Value 'x'
        $before = (Get-Item (Join-Path $state 'housekeeping.state')).LastWriteTimeUtc
        $o = & $pw -NoProfile -NonInteractive -File $hk -Event stop 2>&1
        Check ($LASTEXITCODE -eq 0 -and -not $o) 'stop hook inside the throttle window: exit 0, prints nothing'
        Check ((Get-Item (Join-Path $state 'housekeeping.state')).LastWriteTimeUtc -eq $before) 'throttled hook does no work (state untouched)'
        Remove-Item (Join-Path $state 'housekeeping.state')

        # lock: a second run exits at once while the first holds the lock
        $lk = [IO.File]::Open((Join-Path $state 'housekeeping.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
        try {
            $o = & $pw -NoProfile -NonInteractive -File $hk -Event periodic -Repo $repo 2>&1
            Check ($LASTEXITCODE -eq 0 -and -not $o) 'a second run while the lock is held exits 0 silently'
            Check (-not (Test-Path (Join-Path $state 'housekeeping.log'))) 'and does no work (no log line)'
            $o = & $pw -NoProfile -NonInteractive -File $hk -Event manual -Repo $repo 2>&1
            Check (($o -join ' ') -match 'in progress') 'manual run says another run is in progress'
        } finally { $lk.Dispose() }

        # end to end, real apply, on the throwaway tree: an old session of this repo and a merged worktree under Temp\claude
        $e2eSess = New-Session 'hashC' 'gggggggg-e2e' $true $now.AddDays(-3) $now.AddDays(-3)
        $e2eWt = Join-Path $claude 'wtroot\wt-e2e'; New-Item -ItemType Directory -Force -Path (Split-Path $e2eWt) | Out-Null
        G $repo worktree add -q -b b-e2e $e2eWt | Out-Null; Set-Worktree-Time $e2eWt $now.AddDays(-3)
        $o = & $pw -NoProfile -NonInteractive -File $hk -Event manual -Repo $repo 2>&1
        Check ($LASTEXITCODE -eq 0) 'manual apply run exits 0'
        Check (-not (Test-Path $e2eSess)) 'end to end: the old session of this repo is deleted'
        Check (-not (Test-Path $e2eWt)) 'end to end: the merged worktree is removed'
        Check (Test-Path $sLive) 'end to end: the live session is still there'
        $log = Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw
        Check ($log -match 'manual apply: scratch') 'one summary line is logged'

        # dry run through the script
        $e2eSess2 = New-Session 'hashC' 'hhhhhhhh-e2e2' $true $now.AddDays(-3) $now.AddDays(-3)
        $o = & $pw -NoProfile -NonInteractive -File $hk -Event manual -DryRun -Repo $repo 2>&1
        Check ((Test-Path $e2eSess2) -and (($o -join ' ') -match 'dry run')) 'script -DryRun deletes nothing and says so'

        # log cap
        1..800 | ForEach-Object { Add-LogLine -Dir $state -Line ('line ' + $_ + ' ' + ('x' * 60)) }
        Check ((Get-Item (Join-Path $state 'housekeeping.log')).Length -lt 40KB) 'the log stays under its cap'

        # a hook outside the throttle window returns at once, silently, and the detached worker does the run
        Remove-Item -LiteralPath (Join-Path $state 'housekeeping.state') -ErrorAction SilentlyContinue
        $hookSess = New-Session 'hashC' 'iiiiiiii-hook' $true $now.AddDays(-3) $now.AddDays(-3)
        $sw = [Diagnostics.Stopwatch]::StartNew()
        $o = & $pw -NoProfile -NonInteractive -File $hk -Event stop -Repo $repo 2>&1
        $took = $sw.Elapsed.TotalSeconds
        Check ($LASTEXITCODE -eq 0 -and -not $o) 'stop hook: exit 0, prints nothing'
        Write-Host ("      (hook returned in {0:N1}s)" -f $took)
        $deadline = [datetime]::UtcNow.AddSeconds(90)
        while ((Test-Path -LiteralPath $hookSess) -and [datetime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 500 }
        Check (-not (Test-Path -LiteralPath $hookSess)) 'stop hook: the background worker ran and deleted the old session'
        Start-Sleep -Seconds 1
        Check ((Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw) -match ' stop apply:') 'stop hook: the worker logged its run'

        # a broken repo argument never makes a hook fail
        $o = & $pw -NoProfile -NonInteractive -File $hk -Event periodic -Repo (Join-Path $base 'no-such-repo') 2>&1
        Check ($LASTEXITCODE -eq 0 -and -not $o) 'periodic run with a bad repository: exit 0, silent'
    } finally { $env:LOCALAPPDATA = $saveL; $env:USERPROFILE = $saveU; $env:GOCACHE = $saveG }
} catch {
    Write-Host "FAIL  test crashed: $($_.Exception.Message)"; Write-Host $_.ScriptStackTrace; $fail++
} finally {
    # Windows keeps git's read-only object files: clear the attribute, then delete the throwaway tree (it is under %TEMP%, named hk-test-*).
    if ($base -and (Split-Path -Leaf $base) -like 'hk-test-*' -and (Test-Path -LiteralPath $base)) {
        Get-ChildItem -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue | ForEach-Object { try { $_.Attributes = 'Normal' } catch { } }
        Remove-Item -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue
    }
}
if ($fail -gt 0) { Write-Host "$fail check(s) failed"; exit 1 }
Write-Host 'all checks passed'
