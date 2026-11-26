<#
.SYNOPSIS  Tests Clean-Scratch.ps1 against a throwaway folder tree. Touches nothing outside it. Exit code 1 on any failure.
#>
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$script = Join-Path $here 'Clean-Scratch.ps1'
. $script

$fail = 0
function Check { param([bool]$Cond, [string]$Name) if ($Cond) { Write-Host "ok    $Name" } else { Write-Host "FAIL  $Name"; $script:fail++ } }

$base = Join-Path ([IO.Path]::GetTempPath()) ('cs-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$root = Join-Path $base 'Temp\claude'
$proj = Join-Path $root 'proj1'
$proj2 = Join-Path $root 'proj2'
$now = [datetime]::UtcNow
$old = $now.AddDays(-3)
$denied = @()

# Set creation and write time on a whole tree, children first so a parent's time is not bumped afterwards.
function Set-All {
    param([string]$Path, [datetime]$When)
    $items = @(Get-ChildItem -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue | Sort-Object { $_.FullName.Length } -Descending)
    foreach ($i in $items) { $i.CreationTimeUtc = $When; $i.LastWriteTimeUtc = $When }
    $d = Get-Item -LiteralPath $Path -Force; $d.CreationTimeUtc = $When; $d.LastWriteTimeUtc = $When
}
function New-Sess {
    param([string]$Name, [datetime]$When, [string]$Proj = $proj)
    $d = Join-Path $Proj $Name
    New-Item -ItemType Directory -Force -Path (Join-Path $d 'scratchpad') | Out-Null
    [IO.File]::WriteAllBytes((Join-Path $d 'scratchpad\a.bin'), (New-Object byte[] 2048))
    Set-All $d $When
    return $d
}
function Run-Clean {
    param([bool]$Apply, [string[]]$Running = @(), [bool]$Wt = $false, [int]$Age = 1)
    $log = @()
    $r = Invoke-ScratchClean -RootPath $root -DoApply $Apply -AgeDays $Age -Clock $now -Running $Running -WithWorktrees $Wt -InformationAction SilentlyContinue -InformationVariable log
    $script:lastLog = ($log | ForEach-Object { "$_" }) -join "`n"
    return $r
}
function Show-Log { if ($script:lastLog) { Write-Host '      --- log ---'; Write-Host $script:lastLog; Write-Host '      -----------' } }
function Invoke-G { & git.exe -c user.email=t@t -c user.name=t -c protocol.file.allow=always @args 2>&1 | Out-Null; if ($LASTEXITCODE -ne 0) { throw "git $args failed" } }

try {
    New-Item -ItemType Directory -Force -Path $proj, $proj2 | Out-Null
    $oldDir = New-Sess 'old-session' $old
    $newDir = New-Sess 'new-session' $now.AddHours(-2)
    $inUse = New-Sess 'in-use-session-id' $old
    $withWt = New-Sess 'worktree-session' $old
    Set-Content -LiteralPath (Join-Path $withWt 'scratchpad\.git') -Value 'gitdir: x'
    Set-All $withWt $old
    Set-Content -LiteralPath (Join-Path $root 'stray.txt') -Value 'x'
    Set-Content -LiteralPath (Join-Path $proj 'loose.txt') -Value 'x'

    # ---- root guards
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\other')).Ok) 'root not named claude is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'x\claude')).Ok) 'root whose parent is not Temp is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\claude\..\claude')).Ok) 'root with .. is refused'
    Check (-not (Test-SafeRoot 'C:\').Ok) 'drive root is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\claude\nope')).Ok) 'missing root is refused'
    Check (-not (Test-SafeRoot '').Ok) 'empty root is refused'
    Check (Test-SafeRoot $root).Ok 'test root is accepted'
    Check (Test-Inside -Root $root -Child $oldDir -Depth 2) 'depth-2 child is inside'
    Check (-not (Test-Inside -Root $root -Child $proj -Depth 2)) 'depth-1 child is not depth 2'
    Check (-not (Test-Inside -Root $root -Child (Join-Path $base 'Temp\claude-evil\a\b') -Depth 2)) 'sibling with the same prefix is not inside'
    $jroot = Join-Path $base 'jx\Temp\claude'
    New-Item -ItemType Directory -Force -Path (Join-Path $base 'jx\Temp'), (Join-Path $base 'realclaude') | Out-Null
    $jmade = $false
    try { New-Item -ItemType Junction -Path $jroot -Target (Join-Path $base 'realclaude') | Out-Null; $jmade = $true } catch { }
    Check $jmade 'could create a junction for the root-link case'
    if ($jmade) { Check (-not (Test-SafeRoot $jroot).Ok) 'root that is a junction is refused' }

    # ---- dry run deletes nothing
    $running = @('claude.exe --session in-use-session-id --foo')
    $r = Run-Clean $false $running
    Check (Test-Path -LiteralPath $oldDir) 'dry run keeps the old folder'
    Check ($r.WouldFreeBytes -ge 2048) 'dry run reports bytes it would free'
    Check ($r.FreedBytes -eq 0) 'dry run frees nothing'

    # ---- apply
    $r = Run-Clean $true $running
    Check (-not (Test-Path -LiteralPath $oldDir)) 'old unused folder is deleted'
    if (Test-Path -LiteralPath $oldDir) { Show-Log }
    Check (-not (Test-Path -LiteralPath "$oldDir.deleting")) 'no .deleting leftovers'
    Check (Test-Path -LiteralPath $newDir) 'recent folder is kept'
    Check (Test-Path -LiteralPath $inUse) 'folder named by a running process id is kept'
    Check (Test-Path -LiteralPath $withWt) 'folder with a git worktree is kept'
    Check (Test-Path -LiteralPath (Join-Path $root 'stray.txt')) 'stray file at the root is untouched'
    Check (Test-Path -LiteralPath (Join-Path $proj 'loose.txt')) 'loose file at project level is untouched'
    Check ($r.FreedBytes -ge 2048) 'apply reports freed bytes'

    $r = Run-Clean $true $running $true
    Check (Test-Path -LiteralPath $withWt) 'a dangling worktree file (git cannot check it) is kept even with -IncludeWorktrees'
    Check (Test-Path -LiteralPath $inUse) 'in-use folder still kept with -IncludeWorktrees'

    # ---- in use by full path
    $sess2 = New-Sess 'path-named' $old
    $null = Run-Clean $true @("node.exe $sess2\x.js")
    Check (Test-Path -LiteralPath $sess2) 'folder named by full path in a command line is kept'

    # ---- a new file deep inside keeps the folder
    $deep = New-Sess 'deep-new' $old
    New-Item -ItemType Directory -Force -Path (Join-Path $deep 'a\b\c') | Out-Null
    Set-All $deep $old
    Set-Content -LiteralPath (Join-Path $deep 'a\b\c\fresh.txt') -Value 'new'
    $null = Run-Clean $true
    Check (Test-Path -LiteralPath $deep) 'a recent file deep inside keeps the folder'

    # ---- same session id under two projects, one recent: both stay
    $twinA = New-Sess 'twin-id' $old $proj
    $twinB = New-Sess 'twin-id' $now.AddHours(-1) $proj2
    $null = Run-Clean $true
    Check ((Test-Path -LiteralPath $twinA) -and (Test-Path -LiteralPath $twinB)) 'a session id kept under one project is kept under all'

    # ---- a leftover .deleting folder from a crashed run is retried
    $tag = [guid]::NewGuid().ToString('N').Substring(0, 8)
    $left = New-Sess "cl-$tag.deleting" $old
    $null = Run-Clean $true
    Check (-not (Test-Path -LiteralPath $left)) 'an old .deleting leftover is deleted'
    Check (-not (Test-Path -LiteralPath ($left + '2'))) 'no .deleting2 leftover'
    $leftBusy = New-Sess "cb-$tag.deleting" $old
    $fsl = [IO.File]::Open((Join-Path $leftBusy 'scratchpad\a.bin'), 'Open', 'Read', 'None')
    try {
        $null = Run-Clean $true
        Check (Test-Path -LiteralPath (Join-Path $leftBusy 'scratchpad\a.bin')) 'a .deleting leftover with an open file is kept (real rename probe)'
    } finally { $fsl.Dispose() }
    $null = Run-Clean $true
    Check (-not (Test-Path -LiteralPath $leftBusy)) 'the leftover is deleted once nothing holds it'

    # ---- same id under two projects, one member in use: nothing of the group is deleted, everything is renamed back
    $gA = New-Sess "gi-$tag" $old $proj
    $gB = New-Sess "gi-$tag" $old $proj2
    $fsg = [IO.File]::Open((Join-Path $gB 'scratchpad\a.bin'), 'Open', 'Read', 'None')
    try {
        $null = Run-Clean $true
        Check ((Test-Path -LiteralPath (Join-Path $gA 'scratchpad\a.bin')) -and (Test-Path -LiteralPath (Join-Path $gB 'scratchpad\a.bin'))) 'a twin group with one open file is kept whole'
        Check (-not (Test-Path -LiteralPath "$gA.deleting") -and -not (Test-Path -LiteralPath "$gB.deleting")) 'the group rename is rolled back'
    } finally { $fsg.Dispose() }
    $null = Run-Clean $true
    Check ((-not (Test-Path -LiteralPath $gA)) -and (-not (Test-Path -LiteralPath $gB))) 'the twin group is deleted together once free'

    # ---- a name ending in a dot is kept
    $dotName = '\\?\' + (Join-Path $proj "dt-$tag.")
    [void][IO.Directory]::CreateDirectory($dotName)
    $dotted = $true
    try { $dd = [IO.DirectoryInfo]::new($dotName); $dd.CreationTimeUtc = $old; $dd.LastWriteTimeUtc = $old } catch { $dotted = $false }
    $null = Run-Clean $true
    Check ([IO.Directory]::Exists($dotName)) 'a folder name ending in a dot is kept'
    try { [IO.Directory]::Delete($dotName, $true) } catch { }

    # ---- links
    $outside = Join-Path $base 'outside-data'
    New-Item -ItemType Directory -Force -Path $outside | Out-Null
    Set-Content -LiteralPath (Join-Path $outside 'precious.txt') -Value 'keep me'
    $linkIn = New-Sess 'link-inside' $old
    $linkOk = $true
    try { New-Item -ItemType Junction -Path (Join-Path $linkIn 'scratchpad\lnk') -Target $outside | Out-Null } catch { $linkOk = $false }
    Check $linkOk 'could create a junction inside a session'
    Set-All $linkIn $old
    $linkSess = Join-Path $proj 'link-as-session'
    try { New-Item -ItemType Junction -Path $linkSess -Target $outside | Out-Null } catch { $linkOk = $false }
    $linkProj = Join-Path $root 'link-as-project'
    try { New-Item -ItemType Junction -Path $linkProj -Target $outside | Out-Null } catch { $linkOk = $false }
    $null = Run-Clean $true
    Check (Test-Path -LiteralPath $linkIn) 'a session holding a link is kept'
    Check (Test-Path -LiteralPath $linkSess) 'a session folder that is a link is kept'
    Check (Test-Path -LiteralPath $linkProj) 'a project folder that is a link is kept'
    Check (Test-Path -LiteralPath (Join-Path $outside 'precious.txt')) 'link target data is untouched'

    # ---- an unreadable subfolder keeps the folder
    $unread = New-Sess 'unreadable' $old
    $locked = Join-Path $unread 'scratchpad\locked'
    New-Item -ItemType Directory -Force -Path $locked | Out-Null
    Set-Content -LiteralPath (Join-Path $locked 'x.txt') -Value 'x'
    Set-All $unread $old
    $denyOut = & icacls $locked /deny "$($env:USERNAME):(RD)" 2>&1
    Check ($LASTEXITCODE -eq 0) 'could deny read on a subfolder'
    if ($LASTEXITCODE -eq 0) { $denied += $locked }
    $null = Run-Clean $true
    Check (Test-Path -LiteralPath $unread) 'a folder with an unreadable part is kept'

    # ---- a project folder that cannot be listed: nothing is deleted this run
    $listOld = New-Sess 'list-old' $old
    $proj3 = Join-Path $root 'proj3'
    New-Item -ItemType Directory -Force -Path (Join-Path $proj3 'x') | Out-Null
    $d3 = & icacls $proj3 /deny "$($env:USERNAME):(RD)" 2>&1
    Check ($LASTEXITCODE -eq 0) 'could deny listing on a project folder'
    $null = Run-Clean $true
    Check (Test-Path -LiteralPath $listOld) 'an unlistable project folder keeps everything this run'
    & icacls $proj3 /remove:d "$($env:USERNAME)" 2>&1 | Out-Null
    $null = Run-Clean $true
    Check (-not (Test-Path -LiteralPath $listOld)) 'after the folder is readable again the old session is deleted'

    # ---- an open file handle blocks the rename, nothing is half deleted
    $held = New-Sess 'held-open' $old
    $fs = [IO.File]::Open((Join-Path $held 'scratchpad\a.bin'), 'Open', 'Read', 'None')
    try {
        $null = Run-Clean $true
        Check ((Test-Path -LiteralPath $held) -and (Test-Path -LiteralPath (Join-Path $held 'scratchpad\a.bin'))) 'a folder with an open file is kept intact'
        Check (-not (Test-Path -LiteralPath "$held.deleting")) 'no .deleting folder when the rename is refused'
    } finally { $fs.Dispose() }

    # ---- clones
    $git = Get-Command git -ErrorAction SilentlyContinue
    Check ($null -ne $git) 'git is available for the clone cases'
    if ($git) {
        $bare = Join-Path $base 'remote.git'
        $seed = Join-Path $base 'seed'
        New-Item -ItemType Directory -Force -Path $seed | Out-Null
        Invoke-G init -q -b main $seed
        Set-Content -LiteralPath (Join-Path $seed 'f.txt') -Value 'one'
        Invoke-G -C $seed add .
        Invoke-G -C $seed commit -q -m one
        Invoke-G clone -q --bare $seed $bare

        $cleanClone = Join-Path $proj 'clone-clean'
        New-Item -ItemType Directory -Force -Path $cleanClone | Out-Null
        Invoke-G clone -q $bare (Join-Path $cleanClone 'repo')
        Set-All $cleanClone $old
        $dirtyClone = Join-Path $proj 'clone-dirty'
        New-Item -ItemType Directory -Force -Path $dirtyClone | Out-Null
        Invoke-G clone -q $bare (Join-Path $dirtyClone 'repo')
        Set-Content -LiteralPath (Join-Path $dirtyClone 'repo\f.txt') -Value 'edited'
        Set-All $dirtyClone $old
        $aheadClone = Join-Path $proj 'clone-ahead'
        New-Item -ItemType Directory -Force -Path $aheadClone | Out-Null
        Invoke-G clone -q $bare (Join-Path $aheadClone 'repo')
        Set-Content -LiteralPath (Join-Path $aheadClone 'repo\g.txt') -Value 'new'
        Invoke-G -C (Join-Path $aheadClone 'repo') add .
        Invoke-G -C (Join-Path $aheadClone 'repo') commit -q -m local
        Set-All $aheadClone $old
        $wtClone = Join-Path $proj 'clone-with-worktree'
        New-Item -ItemType Directory -Force -Path $wtClone | Out-Null
        Invoke-G clone -q $bare (Join-Path $wtClone 'repo')
        Invoke-G -C (Join-Path $wtClone 'repo') worktree add -q (Join-Path $base 'linked-wt') -b side
        Set-All $wtClone $old
        $stashClone = Join-Path $proj 'clone-stash'
        New-Item -ItemType Directory -Force -Path $stashClone | Out-Null
        Invoke-G clone -q $bare (Join-Path $stashClone 'repo')
        Set-Content -LiteralPath (Join-Path $stashClone 'repo\f.txt') -Value 'stashed work'
        Invoke-G -C (Join-Path $stashClone 'repo') stash -q
        Set-All $stashClone $old
        # A real linked worktree, as its own session folder (its main repo lives elsewhere).
        $mainRepo = Join-Path $base 'mainrepo'
        Invoke-G clone -q $bare $mainRepo
        $wtClean = Join-Path $proj 'wt-clean'
        New-Item -ItemType Directory -Force -Path $wtClean | Out-Null
        Invoke-G -C $mainRepo worktree add -q (Join-Path $wtClean 'tree') -b wtclean
        Set-All $wtClean $old
        $wtDirty = Join-Path $proj 'wt-dirty'
        New-Item -ItemType Directory -Force -Path $wtDirty | Out-Null
        Invoke-G -C $mainRepo worktree add -q (Join-Path $wtDirty 'tree') -b wtdirty
        Set-Content -LiteralPath (Join-Path $wtDirty 'tree\f.txt') -Value 'dirty'
        Set-All $wtDirty $old

        $null = Run-Clean $true
        if (Test-Path -LiteralPath $cleanClone) { Show-Log }
        Check (-not (Test-Path -LiteralPath $cleanClone)) 'a clean clone with everything pushed is deleted'
        Check (Test-Path -LiteralPath $dirtyClone) 'a clone with uncommitted changes is kept'
        Check (Test-Path -LiteralPath $aheadClone) 'a clone with unpushed commits is kept'
        Check (Test-Path -LiteralPath $wtClone) 'a clone that other worktrees depend on is kept'
        Check (Test-Path -LiteralPath $stashClone) 'a clone whose only unsaved work is a stash is kept'
        Check (Test-Path -LiteralPath $wtClean) 'a linked worktree is kept by default'
        $null = Run-Clean $true @() $true
        if (Test-Path -LiteralPath $wtClean) { Show-Log }
        Check (-not (Test-Path -LiteralPath $wtClean)) 'a clean linked worktree is deleted with -IncludeWorktrees'
        Check (Test-Path -LiteralPath $wtDirty) 'a dirty linked worktree is kept even with -IncludeWorktrees'
        Check (Test-Path -LiteralPath $stashClone) 'the stash clone is still kept with -IncludeWorktrees'

        # The remote is gone: a clone of it can no longer prove its commits exist elsewhere only if it has local-only branches; a clone
        # whose remote-tracking refs still match has nothing unpushed, so it is deleted. A clone with no commits beyond remotes is safe.
        $bad = Join-Path $proj 'clone-broken'
        New-Item -ItemType Directory -Force -Path $bad | Out-Null
        Invoke-G clone -q $bare (Join-Path $bad 'repo')
        Set-Content -LiteralPath (Join-Path $bad 'repo\.git\HEAD') -Value 'garbage'
        Set-All $bad $old
        $null = Run-Clean $true
        Check (Test-Path -LiteralPath $bad) 'a clone git cannot read is kept'
    }

    # ---- age floor: 0 days is treated as 1
    $null = Run-Clean $true @() $false 0
    Check (Test-Path -LiteralPath $newDir) 'age 0 still keeps a 2-hour-old folder'

    # ---- the real process list works and includes this script
    $lines = @(Get-RunningLines)
    Check ($lines.Count -gt 3) 'the real process list can be read'
    Check (($lines | Where-Object { $_ -match 'Test-CleanScratch' }).Count -ge 1) 'the real process list includes this test run'

    # ---- end to end through the script file
    $sess3 = New-Sess 'e2e-old' $old
    $out = & pwsh -NoProfile -File $script -Root $root -ProcessLines @('x') -GoCacheLimitGB 0 2>&1 | Out-String
    Check ($out -match 'DRY RUN') 'script defaults to dry run'
    Check ($out -match 'would delete') 'dry run lists what it would delete'
    Check (Test-Path -LiteralPath $sess3) 'script dry run deleted nothing'
    $bad2 = & pwsh -NoProfile -File $script -Root (Join-Path $base 'Temp') -GoCacheLimitGB 0 2>&1 | Out-String
    Check ($bad2 -match 'Refusing root') 'script refuses a root that is not Temp\claude'
    foreach ($hook in @('-Root', '-ProcessLines', '-Now')) {
        $val = switch ($hook) { '-Root' { $root } '-ProcessLines' { 'x' } default { [string][datetime]::UtcNow } }
        $o = & pwsh -NoProfile -File $script -Apply $hook $val -GoCacheLimitGB 0 2>&1 | Out-String
        Check ($o -match 'test hooks and cannot be combined') "-Apply with $hook is refused"
    }
    Check (Test-Path -LiteralPath $sess3) 'refused -Apply runs deleted nothing'
}
finally {
    foreach ($d in $denied) { & icacls $d /remove:d "$($env:USERNAME)" 2>&1 | Out-Null }
    # Remove junctions first so their targets are never followed.
    Get-ChildItem -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint } |
        ForEach-Object { [IO.Directory]::Delete($_.FullName, $false) }
    if (Test-Path -LiteralPath $base) {
        Get-ChildItem -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue | ForEach-Object { try { $_.Attributes = 'Normal' } catch { } }
        Remove-Item -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if ($fail -gt 0) { Write-Host "$fail check(s) failed"; exit 1 }
Write-Host 'all checks passed'
