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

$pw0 = (Get-Command pwsh).Source
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

    # ---- ignored files are in no branch: a worktree holding one is kept, one holding only rebuildable folders goes
    Add-Content -LiteralPath (Join-Path $repo '.git\info\exclude') -Value @('secret.local', 'node_modules/')
    $wtIgn = Join-Path $scratch 'wt-ignored'; G $repo worktree add -q -b b-ign $wtIgn | Out-Null
    Set-Content -LiteralPath (Join-Path $wtIgn 'secret.local') -Value 'customer extract'; Set-Worktree-Time $wtIgn $now.AddDays(-2)
    $wtNm = Join-Path $scratch 'wt-nodemod'; G $repo worktree add -q -b b-nm $wtNm | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $wtNm 'node_modules') | Out-Null; Set-Content -LiteralPath (Join-Path $wtNm 'node_modules\x.js') -Value '1'; Set-Worktree-Time $wtNm $now.AddDays(-2)
    $null = Run $true $running
    Check ((Test-Path (Join-Path $wtIgn 'secret.local'))) 'worktree holding an ignored file is kept'
    Check (-not (Test-Path $wtNm)) 'worktree holding only node_modules is removed'

    # ---- a worktree that is some process's working directory cannot be renamed, so it stays
    $wtCwd = Join-Path $scratch 'wt-cwd'; G $repo worktree add -q -b b-cwd $wtCwd | Out-Null; Set-Worktree-Time $wtCwd $now.AddDays(-2)
    $holder = Start-Process -FilePath $pw0 -ArgumentList '-NoProfile', '-Command', 'Start-Sleep 120' -WorkingDirectory $wtCwd -PassThru -WindowStyle Hidden
    try {
        Start-Sleep -Milliseconds 1500
        $null = Run $true $running
        Check (Test-Path $wtCwd) 'worktree that is a process working directory is kept'
    } finally { try { $holder.Kill() } catch { }; $holder.WaitForExit(5000) | Out-Null }

    # ---- a worktree inside a live Claude session folder is kept
    $liveWt = Join-Path $claude 'hashD\jjjjjjjj-livewt\wt'; New-Item -ItemType Directory -Force -Path (Split-Path $liveWt) | Out-Null
    G $repo worktree add -q -b b-livewt $liveWt | Out-Null; Set-Worktree-Time $liveWt $now.AddDays(-2)
    $lf = Join-Path $projectDir 'jjjjjjjj-livewt.jsonl'; Set-Content -LiteralPath $lf -Value '{}'; (Get-Item -LiteralPath $lf).LastWriteTimeUtc = $clock.AddMinutes(-10)
    $null = Run $true $running
    Check (Test-Path $liveWt) 'worktree of a session with a recent transcript is kept'

    # ---- prune only touches folders that are gone from our own roots
    $wtGone = Join-Path $base 'gone-wt'; G $repo worktree add -q -b b-gone $wtGone | Out-Null
    $wtGoneMoved = $wtGone + '-moved'; Move-Item -LiteralPath $wtGone -Destination $wtGoneMoved
    $wtPr = Join-Path $scratch 'wt-prune'; G $repo worktree add -q -b b-pr $wtPr | Out-Null; Set-Worktree-Time $wtPr $now.AddDays(-2)
    $null = Run $true $running
    Check (-not (Test-Path $wtPr)) 'a merged worktree is removed in the same run'
    Check (((G $repo worktree list --porcelain) -join ' ') -match 'gone-wt') 'a missing worktree outside the roots keeps its registration'
    Move-Item -LiteralPath $wtGoneMoved -Destination $wtGone

    # ---- agent worktrees under <repo>\.claude\worktrees follow the same safety rules (the script adds that root itself)
    $agentRoot = Join-Path $repo '.claude\worktrees'
    $agRoots = @($scratch, $agentRoot)
    function RunAg { param([string[]]$Protect = @()) Invoke-HousekeepingRun -Repo $repo -DoApply $true -Clock $clock -TempRoot $temp -ProjectsRoot $projects -ScratchRoots $agRoots -Running $running -Protect $Protect -GoCacheLimitGB 0 6>$null }
    function New-AgentWt { param([string]$Name) $w = Join-Path $agentRoot $Name; G $repo worktree add -q -b ("b-" + $Name) $w | Out-Null; Set-Worktree-Time $w $now.AddDays(-2); return $w }
    $agMerged = New-AgentWt 'agent-merged'
    $agDirty = New-AgentWt 'agent-dirty'; Set-Content -LiteralPath (Join-Path $agDirty 'n.txt') -Value 'x'
    $agUnmerged = New-AgentWt 'agent-unmerged'; Set-Content -LiteralPath (Join-Path $agUnmerged 'u.txt') -Value 'u'; G $agUnmerged add u.txt | Out-Null; G $agUnmerged commit -q -m work | Out-Null; Set-Worktree-Time $agUnmerged $now.AddDays(-2)
    $agNamed = New-AgentWt 'agent-named'
    $agSub = New-AgentWt 'agent-cwdsub'; New-Item -ItemType Directory -Force -Path (Join-Path $agSub 'sub') | Out-Null; Set-Worktree-Time $agSub $now.AddDays(-2)
    $agLive = New-AgentWt 'agent-live'
    $agLiveProj = Join-Path $projects (ConvertTo-ProjectName $agLive); New-Item -ItemType Directory -Force -Path $agLiveProj | Out-Null
    $lt = Join-Path $agLiveProj 'sess.jsonl'; Set-Content -LiteralPath $lt -Value '{}'; (Get-Item -LiteralPath $lt).LastWriteTimeUtc = $clock.AddMinutes(-10)
    $running = $running + @("C:\tool\x.exe --cwd $agNamed")
    $r = RunAg @((Join-Path $agSub 'sub'))
    Check (-not (Test-Path $agMerged)) 'agent worktree: merged, clean, idle worktree under .claude\worktrees is removed'
    Check ((G $repo branch --list b-agent-merged) -match 'b-agent-merged') 'agent worktree: its branch is never deleted'
    Check (Test-Path (Join-Path $agDirty 'n.txt')) 'agent worktree: an untracked file keeps it'
    Check (Test-Path $agUnmerged) 'agent worktree: unmerged commits keep it'
    Check (Test-Path $agNamed) 'agent worktree: a process naming it keeps it'
    Check (Test-Path $agSub) 'agent worktree: a protected checkout inside it (current directory in a subfolder) keeps it'
    Check (Test-Path $agLive) 'agent worktree: a recently changed transcript of its own Claude project keeps it'
    Check (Test-Path $repo) 'agent worktree: the main checkout is never removed'
    $null = Run $true $running
    Check (Test-Path $agUnmerged) 'agent worktree: not removed by a run whose roots do not include .claude\worktrees'

    # ---- the Go build cache: every location, per-location report, cap, fallback, never a real cache
    function New-FakeCache {
        param([string]$Dir, [int]$MB, [bool]$Readme = $true)
        New-Item -ItemType Directory -Force -Path (Join-Path $Dir '3f') | Out-Null
        if ($Readme) { Set-Content -LiteralPath (Join-Path $Dir 'README') -Value 'This directory holds cached build artifacts from the Go build system. Run "go clean -cache" if the directory is getting too large.' }
        [IO.File]::WriteAllBytes((Join-Path $Dir '3f\x-d'), (New-Object byte[] ($MB * 1MB)))
    }
    $gcRoot = Join-Path $base 'gocaches'
    $gcLocal = Join-Path $gcRoot 'Local'; $gcHome = Join-Path $gcRoot 'Home'
    $gcA = Join-Path $gcLocal 'go-build'
    $gcPkg1 = Join-Path $gcHome 'AppData\Local\Packages\Claude_abc123\LocalCache\Local\go-build'
    $gcPkg2 = Join-Path $gcHome 'AppData\Local\Packages\Claude_zzz999\LocalCache\Local\go-build'
    $gcOtherPkg = Join-Path $gcHome 'AppData\Local\Packages\Other_xyz\LocalCache\Local\go-build'
    $gcExtra = Join-Path $gcRoot 'GoCache\build'
    foreach ($c in $gcA, $gcPkg1, $gcPkg2, $gcOtherPkg, $gcExtra) { New-FakeCache $c 2 }
    $found = @(Get-GoCacheLocations -GoEnvDir $gcA -LocalAppData $gcLocal -UserProfile $gcHome -ExtraDirs @($gcExtra, (Join-Path $gcRoot 'missing\build')))
    Check ($found.Count -eq 4) 'go cache locations: go env + localappdata (same folder once), two Claude_* packages and the extra folder; a missing one is skipped'
    Check (($found.Path -contains $gcPkg1) -and ($found.Path -contains $gcPkg2) -and ($found.Path -contains $gcExtra) -and ($found.Path -contains $gcA)) 'go cache locations: the Packages\Claude_* folders are found by glob'
    Check (-not ($found.Path -contains $gcOtherPkg)) 'go cache locations: a package that is not Claude_* is never touched'

    $lim = 0.005   # GB, about 5 MB, so 8 MB is over and 2 MB is under
    $big = Join-Path $gcRoot 'big'; $small = Join-Path $gcRoot 'small'; $nonCache = Join-Path $gcRoot 'notgo'; $bigGo = Join-Path $gcRoot 'biggo'; $huge = Join-Path $gcRoot 'huge'
    New-FakeCache $big 8; New-FakeCache $small 2; New-FakeCache $nonCache 8 $false; New-FakeCache $bigGo 8; New-FakeCache $huge 20
    $locs = @([pscustomobject]@{ Label = 'pkg-big'; Path = $big }, [pscustomobject]@{ Label = 'small'; Path = $small }, [pscustomobject]@{ Label = 'notgo'; Path = $nonCache })
    $modDir = Join-Path $gcRoot 'modcache'; New-Item -ItemType Directory -Force -Path $modDir | Out-Null; [IO.File]::WriteAllBytes((Join-Path $modDir 'm.bin'), (New-Object byte[] (3MB)))

    $g = Invoke-GoCacheClean -DoApply $false -LimitGB $lim -Running @() -Locations $locs -UseGo $false -ModCacheDir $modDir 6>$null
    Check ((Test-Path (Join-Path $big '3f\x-d')) -and $g.WouldFreeBytes -ge 8MB -and $g.FreedBytes -eq 0) 'go cache dry run: reports what it would free and deletes nothing'
    Check (($g.Locations | Where-Object Label -eq 'pkg-big').Status -eq 'would clean') 'go cache dry run: the over-limit location is marked'
    $g = Invoke-GoCacheClean -DoApply $true -LimitGB $lim -Running @() -Locations $locs -UseGo $false -ModCacheDir $modDir 6>$null
    Check ((-not (Test-Path (Join-Path $big '3f\x-d'))) -and (Test-Path (Join-Path $big 'README'))) 'go cache cap: a location over the limit is emptied (rename and delete), README kept'
    Check ((Test-Path (Join-Path $small '3f\x-d'))) 'go cache cap: a location under the limit is left alone'
    Check ((Test-Path (Join-Path $nonCache '3f\x-d'))) 'go cache: a folder without the Go README is never touched'
    Check ((($g.Locations | Where-Object Label -eq 'pkg-big').FreedBytes -ge 8MB) -and (($g.Locations | Where-Object Label -eq 'small').FreedBytes -eq 0)) 'go cache: GB freed is reported per location'
    Check ((Test-Path (Join-Path $modDir 'm.bin')) -and $g.ModCacheBytes -ge 3MB) 'go module cache: measured and reported, never deleted'

    if (Get-Command go -ErrorAction SilentlyContinue) {
        $envBefore = $env:GOCACHE
        $g = Invoke-GoCacheClean -DoApply $true -LimitGB $lim -Running @() -Locations @([pscustomobject]@{ Label = 'viago'; Path = $bigGo }) -ModCacheDir $modDir 6>$null
        Check (-not (Test-Path (Join-Path $bigGo '3f\x-d'))) 'go cache: go clean -cache with GOCACHE set to the location empties it'
        Check ($env:GOCACHE -eq $envBefore) 'go cache: GOCACHE is restored after the call'
    } else { Write-Host 'skip  go clean path (go is not installed)' }

    New-FakeCache $big 8
    $goBusy = @('C:\Program Files\Go\bin\go.exe test ./...')
    $g = Invoke-GoCacheClean -DoApply $true -LimitGB $lim -Running $goBusy -Locations @([pscustomobject]@{ Label = 'pkg-big'; Path = $big }, [pscustomobject]@{ Label = 'pkg-huge'; Path = $huge }) -UseGo $false 6>$null
    Check ((Test-Path (Join-Path $big '3f\x-d')) -and (Test-Path (Join-Path $huge '3f\x-d'))) 'go cache: nothing is cleaned while a Go build runs'
    Check (@($g.Notes).Count -eq 1 -and ($g.Notes[0] -match 'go cache over limit but a build is running') -and ($g.Notes[0] -match 'pkg-huge')) 'go cache: a cache over 3x the limit with a build running is called out (one over 1x is not)'
    $g = Invoke-GoCacheClean -DoApply $true -LimitGB 0 -Running @() -Locations @([pscustomobject]@{ Label = 'huge'; Path = $huge }) -UseGo $false 6>$null
    Check ((Test-Path (Join-Path $huge '3f\x-d')) -and $g.Locations.Count -eq 0) 'go cache: limit 0 turns the step off'

    $rg = Invoke-HousekeepingRun -Repo $repo -DoApply $true -Clock $clock -TempRoot $temp -ProjectsRoot $projects -ScratchRoots @($scratch) -Running $goBusy -GoCacheLimitGB $lim -GoLocations @([pscustomobject]@{ Label = 'pkg-huge'; Path = $huge }) -UseGo $false -ModCacheDir $modDir 6>$null
    $line = Format-RunLine -Ev 'periodic' -DoApply $true -R $rg
    Check ($line -match 'go cache over limit but a build is running' -and $line -match 'go \[pkg-huge 0\.02 GB over limit, build running\]' -and $line -match 'modcache 0\.0[0-9] GB') 'run line: names the blocked cache and its size (not a 0.00 GB freed), and the module cache size'
    $rg = Invoke-HousekeepingRun -Repo $repo -DoApply $true -Clock $clock -TempRoot $temp -ProjectsRoot $projects -ScratchRoots @($scratch) -Running @() -GoCacheLimitGB $lim -GoLocations @([pscustomobject]@{ Label = 'pkg-huge'; Path = $huge }) -UseGo $false 6>$null
    $line = Format-RunLine -Ev 'periodic' -DoApply $true -R $rg
    Check ((-not (Test-Path (Join-Path $huge '3f\x-d'))) -and $line -match 'go \[pkg-huge freed 0\.0[1-9] GB of 0\.02 GB\]') 'run line: a cleaned location shows its freed GB'
    $rg = Invoke-HousekeepingRun -Repo $repo -DoApply $true -Clock $clock -TempRoot $temp -ProjectsRoot $projects -ScratchRoots @($scratch) -Running @() -GoCacheLimitGB $lim -GoLocations @([pscustomobject]@{ Label = 'pkg-x'; Path = $nonCache }, [pscustomobject]@{ Label = 'go-env'; Path = $small }) -UseGo $false 6>$null
    $line = Format-RunLine -Ev 'periodic' -DoApply $true -R $rg
    Check ($line -match 'pkg-x skipped \(no Go README\)' -and $line -match 'go-env 0\.00 GB under limit') 'run line: a skipped or under-limit cache is named as such, never shown as a bare 0.00 GB'

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
        $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event stop 2>&1
        Check ($LASTEXITCODE -eq 0 -and -not $o) 'stop hook inside the throttle window: exit 0, prints nothing'
        Check ((Get-Item (Join-Path $state 'housekeeping.state')).LastWriteTimeUtc -eq $before) 'throttled hook does no work (state untouched)'
        Remove-Item (Join-Path $state 'housekeeping.state')

        # lock: a second run exits at once while the first holds the lock
        $lk = [IO.File]::Open((Join-Path $state 'housekeeping.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
        try {
            $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event periodic -Repo $repo 2>&1
            Check ($LASTEXITCODE -eq 0 -and -not $o) 'a second run while the lock is held exits 0 silently'
            Check (-not (Test-Path (Join-Path $state 'housekeeping.log'))) 'and does no work (no log line)'
            $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event manual -Repo $repo 2>&1
            Check (($o -join ' ') -match 'in progress') 'manual run says another run is in progress'
        } finally { $lk.Dispose() }

        # end to end, real apply, on the throwaway tree: an old session of this repo and a merged worktree under Temp\claude
        $e2eSess = New-Session 'hashC' 'gggggggg-e2e' $true $now.AddDays(-3) $now.AddDays(-3)
        $e2eWt = Join-Path $claude 'wtroot\wt-e2e'; New-Item -ItemType Directory -Force -Path (Split-Path $e2eWt) | Out-Null
        G $repo worktree add -q -b b-e2e $e2eWt | Out-Null; Set-Worktree-Time $e2eWt $now.AddDays(-3)
        $e2eAg = Join-Path $repo '.claude\worktrees\agent-e2e'; New-Item -ItemType Directory -Force -Path (Split-Path $e2eAg) | Out-Null
        G $repo worktree add -q -b b-agent-e2e $e2eAg | Out-Null; Set-Worktree-Time $e2eAg $now.AddDays(-3)
        $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event manual -Repo $repo 2>&1
        Check ($LASTEXITCODE -eq 0) 'manual apply run exits 0'
        Check (-not (Test-Path $e2eSess)) 'end to end: the old session of this repo is deleted'
        Check (-not (Test-Path $e2eWt)) 'end to end: the merged worktree is removed'
        Check (-not (Test-Path $e2eAg)) 'end to end: the merged agent worktree under <repo>\.claude\worktrees is removed (root added by the script)'
        Check (Test-Path $sLive) 'end to end: the live session is still there'
        $log = Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw
        Check ($log -match 'manual apply: scratch') 'one summary line is logged'

        # dry run through the script
        $e2eSess2 = New-Session 'hashC' 'hhhhhhhh-e2e2' $true $now.AddDays(-3) $now.AddDays(-3)
        $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event manual -DryRun -Repo $repo 2>&1
        Check ((Test-Path $e2eSess2) -and (($o -join ' ') -match 'dry run')) 'script -DryRun deletes nothing and says so'
        Remove-Item -LiteralPath (Join-Path $state 'housekeeping.state') -ErrorAction SilentlyContinue
        $null = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event periodic -DryRun -Repo $repo 2>&1
        Check ((Test-Path $e2eSess2) -and ((Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw) -match 'periodic dry run')) '-DryRun is honoured for a periodic run too'

        # log cap
        1..800 | ForEach-Object { Add-LogLine -Dir $state -Line ('line ' + $_ + ' ' + ('x' * 60)) }
        Check ((Get-Item (Join-Path $state 'housekeeping.log')).Length -lt 40KB) 'the log stays under its cap'

        # a hook outside the throttle window returns at once, silently, and the detached worker does the run
        Remove-Item -LiteralPath (Join-Path $state 'housekeeping.state') -ErrorAction SilentlyContinue
        $hookSess = New-Session 'hashC' 'iiiiiiii-hook' $true $now.AddDays(-3) $now.AddDays(-3)
        $sw = [Diagnostics.Stopwatch]::StartNew()
        $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event stop -Repo $repo 2>&1
        $took = $sw.Elapsed.TotalSeconds
        Check ($LASTEXITCODE -eq 0 -and -not $o) 'stop hook: exit 0, prints nothing'
        Write-Host ("      (hook returned in {0:N1}s)" -f $took)
        $deadline = [datetime]::UtcNow.AddSeconds(90)
        while ((Test-Path -LiteralPath $hookSess) -and [datetime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 500 }
        Check (-not (Test-Path -LiteralPath $hookSess)) 'stop hook: the background worker ran and deleted the old session'
        # The worker logs after the session step, the worktree step (a fetch) and the go step: wait for the line, do not guess a delay.
        $deadline = [datetime]::UtcNow.AddSeconds(60)
        while (-not ((Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw) -match ' stop apply:') -and [datetime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 500 }
        Check ((Get-Content -LiteralPath (Join-Path $state 'housekeeping.log') -Raw) -match ' stop apply:') 'stop hook: the worker logged its run'

        # a broken repo argument never makes a hook fail
        $o = & $pw -NoProfile -NonInteractive -File $hk -GoCacheLimitGB 0 -Event periodic -Repo (Join-Path $base 'no-such-repo') 2>&1
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
