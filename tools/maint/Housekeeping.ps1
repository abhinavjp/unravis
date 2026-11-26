#Requires -Version 7
<#
.SYNOPSIS
  Permanent housekeeping for this repository's session leftovers on a developer PC (Windows). Cheap to call often, safe to run twice at once.

.DESCRIPTION
  Entry point for the Claude Code hooks in .claude/settings.json (Stop, SessionEnd), for the hourly scheduled task (-Install) and for
  people (-Event manual, dry run with -DryRun). It reuses Clean-Scratch.ps1 (same folder) for the session scratch folders and the Go
  build cache and adds what that script does not do:
    1. Only sessions of THIS repository: a session folder under %LOCALAPPDATA%\Temp\claude\<hash>\<session-id> is judged only when
       <session-id>.jsonl is a transcript in the Claude project folder of this repository or of one of its registered git worktrees
       (~\.claude\projects\<path with every non-letter/digit as ->). Anything else is another repository's and is left alone.
    2. A session whose transcript (or subagent folder) changed within -IdleMinutes (default 120) is treated as alive and kept, whatever
       the age of its scratch. A running process that names the session id or the folder also keeps it (Clean-Scratch rules).
    3. Registered worktrees of this repository under D:\Scratch or the Temp\claude root are removed with a plain
       `git worktree remove` (never forced, never deleting a branch) only when: clean (no untracked files), no process names the
       path, untouched for a day, HEAD contained in origin/develop or origin/master after a fresh fetch (a failed fetch keeps
       everything), not the main checkout or the checkout running this script. Missing worktree folders are pruned.
    4. %TEMP%\unravis-probe* and %TEMP%\unravis-dev-* folders, with the Clean-Scratch age, link, in-use and clone-at-risk rules.
  Not touched: %TEMP%\uci-* and the Homestretch agent's work areas and caches (the agent prunes and caps them itself, see
  docs/ci/LOCAL-PIPELINE.md), other repositories, source, user data. Age alone never deletes anything.

  Hook events (stop, sessionend) check a throttle FIRST (one file timestamp), then start a hidden worker and return at once; they print
  nothing and always exit 0, so they can never block Claude. periodic (the scheduled task) and manual run in the calling process.
  A lock file makes a second run exit at once while one is running. The log (one line per run) is capped at 32 KB.
  Throttle: stop and periodic 50 minutes, sessionend 15 minutes, manual none.

.PARAMETER Event      stop | sessionend | periodic | manual (default manual).
.PARAMETER DryRun     Manual runs only: report, delete nothing.
.PARAMETER Repo       Repository folder. Default: the one saved by -Install, else the checkout this script is in.
.PARAMETER Install    Copy the scripts to %LOCALAPPDATA%\Unravis\maint, save the repository folder, and register the hourly user-level task
                      'Unravis-Housekeeping' (this is what makes the cleanup run on a checkout that does not have the hooks yet). Re-run after
                      the scripts change. It also removes the older daily 'Unravis-CleanScratch' task.
.PARAMETER Uninstall  Remove that task.
.PARAMETER Worker     Internal: a hook started this process in the background.
#>
[CmdletBinding()]
param(
    [ValidateSet('stop', 'sessionend', 'periodic', 'manual')][string]$Event = 'manual',
    [switch]$DryRun,
    [string]$Repo,
    [switch]$Install,
    [switch]$Uninstall,
    [switch]$Worker
)

$script:HkTask = 'Unravis-Housekeeping'
$script:HkLogCap = 32KB

function Get-StateDir { return (Join-Path $env:LOCALAPPDATA 'Unravis\maint') }

function Get-ThrottleMinutes { param([string]$Ev) switch ($Ev) { 'sessionend' { 15 } 'manual' { 0 } default { 50 } } }

# Claude names a project folder after the working directory with every character that is not a letter or digit replaced by '-'.
function ConvertTo-ProjectName { param([string]$Path) return [regex]::Replace([IO.Path]::GetFullPath($Path).TrimEnd('\', '/'), '[^A-Za-z0-9]', '-') }

function Get-GitLines {
    param([string]$Dir, [string[]]$GitArgs)
    $out = & git --no-optional-locks -c core.fsmonitor=false -C $Dir @GitArgs 2>$null
    if ($LASTEXITCODE -ne 0) { return $null }
    # $null means git failed; an empty array means git answered with nothing (the leading comma keeps it from collapsing to $null).
    $lines = @(); foreach ($x in $out) { $lines += [string]$x }
    return , $lines
}

# Get-RepoWorktrees: every registered worktree path of the repository, the main checkout first. $null when git cannot say.
function Get-RepoWorktrees {
    param([string]$Repo)
    $l = Get-GitLines -Dir $Repo -GitArgs @('worktree', 'list', '--porcelain')
    if ($null -eq $l) { return $null }
    $paths = @()
    foreach ($x in $l) { if ($x -like 'worktree *') { $paths += [IO.Path]::GetFullPath($x.Substring(9).Trim()).TrimEnd('\') } }
    if ($paths.Count -eq 0) { return $null }
    return $paths
}

function Get-OwnerDirs {
    param([string[]]$Worktrees, [string]$ProjectsRoot)
    $dirs = @()
    foreach ($w in $Worktrees) {
        $d = Join-Path $ProjectsRoot (ConvertTo-ProjectName $w)
        if (Test-Path -LiteralPath $d -PathType Container) { $dirs += $d }
    }
    return $dirs
}

function Test-UnderAny {
    param([string]$Path, [string[]]$Roots)
    $p = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    foreach ($r in $Roots) {
        if (-not $r) { continue }
        $rr = [IO.Path]::GetFullPath($r).TrimEnd('\')
        if ($p.StartsWith($rr + '\', [StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}

# Remove-OneFolder: rename (Windows refuses while a process uses it), judge again, delete. Returns $null when deleted, else the reason it stayed.
function Remove-OneFolder {
    param([string]$Path, [datetime]$Cutoff, [string[]]$Running)
    $parent = Split-Path -Parent $Path
    $orig = Split-Path -Leaf $Path
    $tmpName = if ($orig -match '\.deleting$') { (Get-BaseName $orig) + '.deleting2' } else { (Get-BaseName $orig) + '.deleting' }
    $tmp = Join-Path $parent $tmpName
    if (Test-Path -LiteralPath $tmp) { return "$tmpName already exists" }
    try { Rename-Item -LiteralPath $Path -NewName $tmpName -ErrorAction Stop } catch { return 'in use (cannot be renamed)' }
    $again = $null
    try { $again = Get-SessionVerdict -Dir (Get-Item -LiteralPath $tmp -Force) -Cutoff $Cutoff -Running $Running -WithWorktrees $false } catch { $again = $null }
    if (-not $again -or -not $again.Delete) {
        try { Rename-Item -LiteralPath $tmp -NewName $orig -ErrorAction Stop } catch { }
        return 'changed while checking'
    }
    try { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction Stop } catch { return 'not fully deleted, the next run retries' }
    return $null
}

# Invoke-TempPatternClean: %TEMP%\unravis-probe* and unravis-dev-* (this repository's own throwaway folders).
function Invoke-TempPatternClean {
    param([string]$TempRoot, [bool]$DoApply, [int]$AgeDays, [datetime]$Clock, [string[]]$Running)
    $r = [pscustomobject]@{ FreedBytes = 0L; WouldFreeBytes = 0L; Kept = 0 }
    if (-not $TempRoot -or -not (Test-Path -LiteralPath $TempRoot -PathType Container)) { return $r }
    $cutoff = $Clock.ToUniversalTime().AddDays(-[math]::Max(1, $AgeDays))
    foreach ($pat in 'unravis-probe*', 'unravis-dev-*') {
        foreach ($d in @(Get-ChildItem -LiteralPath $TempRoot -Directory -Filter $pat -Force -ErrorAction SilentlyContinue)) {
            $v = Get-SessionVerdict -Dir $d -Cutoff $cutoff -Running $Running -WithWorktrees $false
            if (-not $v.Delete) { $r.Kept++; Write-Host ("keep  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
            if (-not $DoApply) { $r.WouldFreeBytes += $v.Bytes; Write-Host ("would delete  {0}  [{1}]" -f $v.Path, (Format-Size $v.Bytes)); continue }
            $why = Remove-OneFolder -Path $v.Path -Cutoff $cutoff -Running $Running
            if ($why) { $r.Kept++; Write-Host "keep  $($v.Path): $why" } else { $r.FreedBytes += $v.Bytes; Write-Host ("deleted  {0}  [{1}]" -f $v.Path, (Format-Size $v.Bytes)) }
        }
    }
    return $r
}

# Get-WorktreeKeepReason: why a registered worktree must stay, or $null when it may be removed (before the fetch/merged check).
function Get-WorktreeKeepReason {
    param([string]$Path, [string[]]$Running, [datetime]$Clock, [int]$AgeHours, [string[]]$Roots, [string[]]$Protect)
    if (-not (Test-UnderAny -Path $Path -Roots $Roots)) { return 'outside the scratch folders' }
    foreach ($p in $Protect) { if ($p -and ([IO.Path]::GetFullPath($p).TrimEnd('\') -ieq $Path)) { return 'the main checkout or the checkout in use' } }
    $it = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $it) { return $null }
    if ($it.Attributes -band [IO.FileAttributes]::ReparsePoint) { return 'is a link' }
    foreach ($line in $Running) { if ($line.IndexOf($Path, [StringComparison]::OrdinalIgnoreCase) -ge 0) { return 'a running process names it' } }
    $newest = $it.LastWriteTimeUtc
    $gitFile = Join-Path $Path '.git'
    $adminDir = $null
    if (Test-Path -LiteralPath $gitFile -PathType Leaf) {
        $m = [regex]::Match((Get-Content -LiteralPath $gitFile -Raw -ErrorAction SilentlyContinue), 'gitdir:\s*(.+)')
        if ($m.Success) { $adminDir = $m.Groups[1].Value.Trim() }
    }
    $probe = @(Get-ChildItem -LiteralPath $Path -Force -ErrorAction SilentlyContinue)
    if ($adminDir -and (Test-Path -LiteralPath $adminDir -PathType Container)) {
        $probe += @(Get-ChildItem -LiteralPath $adminDir -Force -File -ErrorAction SilentlyContinue)
        $probe += @(Get-Item -LiteralPath (Join-Path $adminDir 'logs\HEAD') -Force -ErrorAction SilentlyContinue)
    }
    foreach ($e in $probe) { if ($e -and $e.LastWriteTimeUtc -gt $newest) { $newest = $e.LastWriteTimeUtc } }
    if ($newest -ge $Clock.ToUniversalTime().AddHours(-$AgeHours)) { return 'touched within the last ' + $AgeHours + ' hours' }
    $st = Get-GitLines -Dir $Path -GitArgs @('status', '--porcelain', '--untracked-files=all')
    if ($null -eq $st) { return 'git could not check it' }
    if ($st.Count -gt 0) { return 'uncommitted or untracked files' }
    return $null
}

function Test-ContainedInRemoteMain {
    param([string]$Path)
    foreach ($ref in 'origin/develop', 'origin/master') {
        & git --no-optional-locks -c core.fsmonitor=false -C $Path rev-parse --verify --quiet "$ref^{commit}" 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) { continue }
        & git --no-optional-locks -c core.fsmonitor=false -C $Path merge-base --is-ancestor HEAD $ref 2>$null
        if ($LASTEXITCODE -eq 0) { return $true }
    }
    return $false
}

# Invoke-WorktreeClean: removes merged, clean, idle registered worktrees of the repository. Never forced, never deletes a branch.
function Invoke-WorktreeClean {
    param([string]$Repo, [string[]]$Worktrees, [bool]$DoApply, [datetime]$Clock, [string[]]$Running, [string[]]$Roots, [string[]]$Protect, [int]$AgeHours = 24)
    $r = [pscustomobject]@{ Removed = 0; WouldRemove = 0; Kept = 0; Pruned = $false }
    $cands = @()
    foreach ($w in @($Worktrees | Select-Object -Skip 1)) {
        $why = Get-WorktreeKeepReason -Path $w -Running $Running -Clock $Clock -AgeHours $AgeHours -Roots $Roots -Protect (@($Repo) + $Protect)
        if ($why) { $r.Kept++; Write-Host "keep  worktree $w : $why"; continue }
        $cands += $w
    }
    if ($cands.Count -eq 0) { return $r }
    $fetched = $false
    if ($DoApply) {
        $env:GIT_TERMINAL_PROMPT = '0'
        & git --no-optional-locks -C $Repo fetch --quiet --no-tags origin '+refs/heads/develop:refs/remotes/origin/develop' '+refs/heads/master:refs/remotes/origin/master' 2>$null
        $fetched = ($LASTEXITCODE -eq 0)
        if (-not $fetched) { Write-Host 'could not fetch origin: no worktree is removed this run' }
    }
    foreach ($w in $cands) {
        if (-not (Test-Path -LiteralPath $w)) { continue }  # a missing folder is only pruned below
        if ($DoApply -and -not $fetched) { $r.Kept++; continue }
        if (-not (Test-ContainedInRemoteMain -Path $w)) { $r.Kept++; Write-Host "keep  worktree $w : its commits are not in origin/develop or origin/master"; continue }
        if (-not $DoApply) { $r.WouldRemove++; Write-Host "would remove worktree $w (clean, idle, merged; refs not refreshed in a dry run)"; continue }
        & git --no-optional-locks -C $Repo worktree remove -- $w 2>$null
        if ($LASTEXITCODE -eq 0) { $r.Removed++; Write-Host "removed worktree $w" } else { $r.Kept++; Write-Host "keep  worktree $w : git refused to remove it" }
    }
    if ($DoApply) { & git --no-optional-locks -C $Repo worktree prune 2>$null; $r.Pruned = ($LASTEXITCODE -eq 0) }
    return $r
}

# Invoke-HousekeepingRun: the whole sweep. Everything it may touch comes in through the parameters, so the tests run it on a throwaway tree.
function Invoke-HousekeepingRun {
    param(
        [string]$Repo, [bool]$DoApply, [datetime]$Clock, [string]$TempRoot, [string]$ProjectsRoot,
        [string[]]$ScratchRoots, [string[]]$Running, [string[]]$Protect = @(),
        [int]$IdleMinutes = 120, [int]$AgeDays = 1, [double]$GoCacheLimitGB = 3
    )
    $out = [ordered]@{ ScratchFreed = 0L; ScratchWould = 0L; ScratchKept = 0; WtRemoved = 0; WtWould = 0; WtKept = 0; TempFreed = 0L; TempWould = 0L; GoFreed = 0L; GoWould = 0L; Note = '' }
    $wts = Get-RepoWorktrees -Repo $Repo
    if (-not $wts) { $out.Note = 'git could not list the worktrees: nothing done'; return [pscustomobject]$out }
    $owners = @(Get-OwnerDirs -Worktrees $wts -ProjectsRoot $ProjectsRoot)
    $s = Invoke-ScratchClean -RootPath (Join-Path $TempRoot 'claude') -DoApply $DoApply -AgeDays $AgeDays -Clock $Clock -Running $Running -WithWorktrees $false -OwnerDirs $owners -IdleMinutes $IdleMinutes
    $out.ScratchFreed = $s.FreedBytes; $out.ScratchWould = $s.WouldFreeBytes; $out.ScratchKept = $s.Kept
    $w = Invoke-WorktreeClean -Repo $Repo -Worktrees $wts -DoApply $DoApply -Clock $Clock -Running $Running -Roots (@($ScratchRoots) + (Join-Path $TempRoot 'claude')) -Protect $Protect
    $out.WtRemoved = $w.Removed; $out.WtWould = $w.WouldRemove; $out.WtKept = $w.Kept
    $t = Invoke-TempPatternClean -TempRoot $TempRoot -DoApply $DoApply -AgeDays $AgeDays -Clock $Clock -Running $Running
    $out.TempFreed = $t.FreedBytes; $out.TempWould = $t.WouldFreeBytes
    $g = Invoke-GoCacheClean -DoApply $DoApply -LimitGB $GoCacheLimitGB -Running $Running
    $out.GoFreed = $g.FreedBytes; $out.GoWould = $g.WouldFreeBytes
    return [pscustomobject]$out
}

function Format-RunLine {
    param([string]$Ev, [bool]$DoApply, $R)
    if ($R.Note) { return ('{0:yyyy-MM-ddTHH:mmZ} {1} {2}' -f [datetime]::UtcNow, $Ev, $R.Note) }
    if ($DoApply) {
        return ('{0:yyyy-MM-ddTHH:mmZ} {1} apply: scratch {2} kept {3}; worktrees removed {4} kept {5}; temp {6}; go {7}' -f [datetime]::UtcNow, $Ev, (Format-Size $R.ScratchFreed), $R.ScratchKept, $R.WtRemoved, $R.WtKept, (Format-Size $R.TempFreed), (Format-Size $R.GoFreed))
    }
    return ('{0:yyyy-MM-ddTHH:mmZ} {1} dry run: scratch {2} would go; worktrees {3} would go; temp {4}; go {5}' -f [datetime]::UtcNow, $Ev, (Format-Size $R.ScratchWould), $R.WtWould, (Format-Size $R.TempWould), (Format-Size $R.GoWould))
}

# Add-LogLine: one line per run; over the cap the file is cut to its last 100 lines.
function Add-LogLine {
    param([string]$Dir, [string]$Line)
    try {
        $f = Join-Path $Dir 'housekeeping.log'
        Add-Content -LiteralPath $f -Value $Line -Encoding utf8
        if ((Get-Item -LiteralPath $f).Length -gt $script:HkLogCap) {
            $keep = @(Get-Content -LiteralPath $f -Tail 100)
            Set-Content -LiteralPath $f -Value $keep -Encoding utf8
        }
    } catch { }
}

function Get-RepoFolder {
    param([string]$Explicit, [string]$Dir)
    if ($Explicit) { return $Explicit }
    $cfg = Join-Path $Dir 'housekeeping.json'
    if (Test-Path -LiteralPath $cfg) {
        try { $c = Get-Content -LiteralPath $cfg -Raw | ConvertFrom-Json; if ($c.repo -and (Test-Path -LiteralPath $c.repo -PathType Container)) { return [string]$c.repo } } catch { }
    }
    $top = Get-GitLines -Dir $PSScriptRoot -GitArgs @('rev-parse', '--show-toplevel')
    if ($top) { return [IO.Path]::GetFullPath($top[0]) }
    return $null
}

function Register-HousekeepingTask {
    param([string]$RepoFolder)
    $dir = Get-StateDir
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $self = [IO.Path]::GetFullPath($PSCommandPath)
    if ($self -match '(?i)\\Temp\\claude\\') { throw 'Run -Install from the repository checkout, not from a Temp\claude folder: the cleanup itself prunes it.' }
    Copy-Item -LiteralPath $self -Destination (Join-Path $dir 'Housekeeping.ps1') -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Clean-Scratch.ps1') -Destination (Join-Path $dir 'Clean-Scratch.ps1') -Force
    @{ repo = $RepoFolder } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $dir 'housekeeping.json') -Encoding utf8
    $pwsh = (Get-Command pwsh -ErrorAction Stop).Source
    $arg = '-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "{0}" -Event periodic' -f (Join-Path $dir 'Housekeeping.ps1')
    $action = New-ScheduledTaskAction -Execute $pwsh -Argument $arg
    $trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(5) -RepetitionInterval (New-TimeSpan -Hours 1) -RepetitionDuration (New-TimeSpan -Days 3650)
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Minutes 30) -MultipleInstances IgnoreNew
    Register-ScheduledTask -TaskName $script:HkTask -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
    Unregister-ScheduledTask -TaskName 'Unravis-CleanScratch' -Confirm:$false -ErrorAction SilentlyContinue
    Write-Host "registered '$($script:HkTask)' (hourly, user level), scripts copied to $dir, repository $RepoFolder"
}

# ---- Main. Dot-sourcing (the tests) only defines the functions.
if ($MyInvocation.InvocationName -ne '.') {
    $isHook = $Event -in 'stop', 'sessionend'
    $alwaysZero = $Event -ne 'manual'
    try {
        if (-not $IsWindows) { if ($alwaysZero) { exit 0 } else { Write-Host 'Windows only.'; exit 0 } }
        $dir = Get-StateDir
        if ($Install) { . (Join-Path $PSScriptRoot 'Clean-Scratch.ps1'); Register-HousekeepingTask -RepoFolder (Get-RepoFolder -Explicit $Repo -Dir $dir); exit 0 }
        if ($Uninstall) { Unregister-ScheduledTask -TaskName $script:HkTask -Confirm:$false -ErrorAction SilentlyContinue; Write-Host 'task removed'; exit 0 }

        # Throttle first: one timestamp read, before anything slow (process list, git).
        $stateFile = Join-Path $dir 'housekeeping.state'
        $mins = Get-ThrottleMinutes $Event
        if (-not $Worker -and $mins -gt 0 -and (Test-Path -LiteralPath $stateFile)) {
            if (((Get-Date) - (Get-Item -LiteralPath $stateFile).LastWriteTime).TotalMinutes -lt $mins) { exit 0 }
        }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        if ($isHook -and -not $Worker) {
            # Claim the slot, start the real work hidden in the background and return at once.
            Set-Content -LiteralPath $stateFile -Value ([datetime]::UtcNow.ToString('o'))
            $a = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $PSCommandPath, '-Event', $Event, '-Worker')
            if ($Repo) { $a += @('-Repo', $Repo) }
            Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $a -WindowStyle Hidden | Out-Null
            exit 0
        }

        # Only one run at a time: the OS drops this lock with the process, so a crash leaves nothing stale.
        $lock = $null
        try { $lock = [IO.File]::Open((Join-Path $dir 'housekeeping.lock'), 'OpenOrCreate', 'ReadWrite', 'None') } catch { if ($alwaysZero) { exit 0 } else { Write-Host 'another housekeeping run is in progress'; exit 0 } }
        try {
            Set-Content -LiteralPath $stateFile -Value ([datetime]::UtcNow.ToString('o'))
            . (Join-Path $PSScriptRoot 'Clean-Scratch.ps1')
            $repoFolder = Get-RepoFolder -Explicit $Repo -Dir $dir
            if (-not $repoFolder) { Add-LogLine -Dir $dir -Line ('{0:yyyy-MM-ddTHH:mmZ} {1} no repository found' -f [datetime]::UtcNow, $Event); exit 0 }
            $doApply = -not ($DryRun -and $Event -eq 'manual')
            $running = @(Get-RunningLines)   # throws when the list cannot be trusted: then nothing is deleted
            $protect = @($PSScriptRoot | ForEach-Object { (Get-GitLines -Dir $_ -GitArgs @('rev-parse', '--show-toplevel')) } | Where-Object { $_ })
            $protect += (Get-Location).Path
            $args2 = @{
                Repo = $repoFolder; DoApply = $doApply; Clock = [datetime]::UtcNow; TempRoot = (Join-Path $env:LOCALAPPDATA 'Temp')
                ProjectsRoot = (Join-Path $env:USERPROFILE '.claude\projects'); ScratchRoots = @('D:\Scratch'); Running = $running; Protect = $protect
            }
            if ($alwaysZero) { $r = Invoke-HousekeepingRun @args2 6>$null } else { $r = Invoke-HousekeepingRun @args2 }
            $line = Format-RunLine -Ev $Event -DoApply $doApply -R $r
            Add-LogLine -Dir $dir -Line $line
            if (-not $alwaysZero) { Write-Host $line }
        } finally { if ($lock) { $lock.Dispose() } }
    } catch {
        try { Add-LogLine -Dir (Get-StateDir) -Line ('{0:yyyy-MM-ddTHH:mmZ} {1} error: {2}' -f [datetime]::UtcNow, $Event, ($_.Exception.Message -replace '\s+', ' ')) } catch { }
        if (-not $alwaysZero) { Write-Host "error: $($_.Exception.Message)" }
    }
    exit 0
}
