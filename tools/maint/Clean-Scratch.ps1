#Requires -Version 7
<#
.SYNOPSIS
  Frees disk space on a developer PC by pruning old Claude session scratch folders and capping the Go build cache.

.DESCRIPTION
  DRY RUN BY DEFAULT: without -Apply nothing is deleted; the script prints what it would delete and how much space that is.

  What it can delete (and nothing else):
    1. Session folders exactly two levels under  %LOCALAPPDATA%\Temp\claude\<project-hash>\<session-id>  that are old (newest write or
       creation time at least -MaxAgeDays ago, default 1) and not in use (see below).
    2. The Go build cache (`go clean -cache`) when it is larger than -GoCacheLimitGB (default 3 GB) and no Go build is running.

  A session folder is KEPT when any of these is true:
    - something in it was written or created recently, or part of it could not be read (so "old" cannot be proven);
    - it, or any folder or file in it, is a link (junction, symlink, cloud placeholder);
    - a running process names its id or path (resumed sessions show their id on the command line; others do not, which is why the
      rename step below also exists);
    - the same session id is kept under another project folder (one session can have folders under several projects);
    - it holds a git worktree (a `.git` file), a clone that other worktrees depend on, or a clone with uncommitted or unpushed work
      (git failing also keeps it);  -IncludeWorktrees lifts only the `.git` FILE rule.
  Before a folder is deleted it is renamed to `<id>.deleting` in the same parent. Windows refuses that rename while a process has a
  file open in the folder or has it as its working directory, so a live folder is never half deleted. After the rename the checks run
  again, and the rename is undone if anything changed.

  With -Apply the process list must be readable (this script's own process must appear in it) or the run aborts.

  Never touched: anything outside the root, the repo, D:\secret, user data. A root that is not exactly <x>\Temp\claude is refused.

.PARAMETER Apply            Really delete. Without it the script only reports.
.PARAMETER MaxAgeDays       A session folder is old when its newest write is at least this many days ago. Minimum 1.
.PARAMETER GoCacheLimitGB   Clean the Go build cache when it is over this size. 0 turns the Go cache step off.
.PARAMETER IncludeWorktrees Also delete old folders that hold a linked git worktree (`.git` file). Off by default.
.PARAMETER LogPath          Append a transcript of the run to this file (the scheduled task uses it).
.PARAMETER Root             Test hook (dry run only): another root, which must still be named <x>\Temp\claude.
.PARAMETER ProcessLines     Test hook (dry run only): command lines to treat as the running processes.
.PARAMETER Now              Test hook (dry run only): the current time (UTC).
.PARAMETER RegisterTask     Copy this script to %LOCALAPPDATA%\Unravis\maint and create a per-user daily scheduled task, then exit.
.PARAMETER UnregisterTask   Remove that task, then exit.
#>
[CmdletBinding()]
param(
    [switch]$Apply,
    [int]$MaxAgeDays = 1,
    [double]$GoCacheLimitGB = 3,
    [switch]$IncludeWorktrees,
    [string]$LogPath,
    [string]$Root,
    [string[]]$ProcessLines,
    [datetime]$Now = [datetime]::UtcNow,
    [switch]$RegisterTask,
    [switch]$UnregisterTask
)

$script:TaskName = 'Unravis-CleanScratch'
$script:ProcessLinesOverride = $null

function Get-DefaultRoot {
    if (-not $env:LOCALAPPDATA) { return $null }
    return (Join-Path $env:LOCALAPPDATA 'Temp\claude')
}

# Test-SafeRoot says whether $Path is a root this script may prune: an existing real folder named <x>\Temp\claude, not a link.
function Test-SafeRoot {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return @{ Ok = $false; Why = 'no root' } }
    if ($Path -match '\.\.') { return @{ Ok = $false; Why = 'path has ..' } }
    try { $full = [IO.Path]::GetFullPath($Path).TrimEnd('\', '/') } catch { return @{ Ok = $false; Why = 'bad path' } }
    if ((Split-Path -Leaf $full) -ine 'claude') { return @{ Ok = $false; Why = 'leaf is not claude' } }
    $parent = Split-Path -Parent $full
    if (-not $parent -or (Split-Path -Leaf $parent) -ine 'Temp') { return @{ Ok = $false; Why = 'parent is not Temp' } }
    if (-not (Test-Path -LiteralPath $full -PathType Container)) { return @{ Ok = $false; Why = 'root missing' } }
    $item = Get-Item -LiteralPath $full -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { return @{ Ok = $false; Why = 'root is a link' } }
    return @{ Ok = $true; Why = ''; Full = $full }
}

# Test-Inside is true only when $Child is exactly $Depth folder levels below $Root (case-insensitive, separators normalised).
function Test-Inside {
    param([string]$Root, [string]$Child, [int]$Depth)
    $r = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
    $c = [IO.Path]::GetFullPath($Child).TrimEnd('\', '/')
    if (-not $c.StartsWith($r + '\', [StringComparison]::OrdinalIgnoreCase)) { return $false }
    $rest = $c.Substring($r.Length + 1)
    if ($rest -match '\.\.') { return $false }
    return (($rest -split '\\').Count -eq $Depth)
}

# Get-RunningLines returns the command line of every process. It THROWS when Windows cannot list them, and when this script's own
# process is missing from the answer, so an empty answer can never switch the in-use protection off.
function Get-RunningLines {
    if ($null -ne $script:ProcessLinesOverride) { return $script:ProcessLinesOverride }
    $all = @(Get-CimInstance Win32_Process -ErrorAction Stop)
    if (-not ($all | Where-Object { $_.ProcessId -eq $PID })) { throw 'the process list does not include this script, so it cannot be trusted' }
    $lines = @()
    foreach ($p in $all) { if ($p.CommandLine) { $lines += $p.CommandLine } }
    return $lines
}

function Format-Size { param([double]$Bytes) '{0:N2} GB' -f ($Bytes / 1GB) }

function Get-BaseName { param([string]$Name) return ($Name -replace '\.deleting$', '') }

# Test-CloneAtRisk: a reason string when a clone (a `.git` folder) holds work that exists nowhere else or git cannot say; else $null.
function Test-CloneAtRisk {
    param([string]$GitDir)
    $repo = Split-Path -Parent $GitDir
    $wt = Join-Path $GitDir 'worktrees'
    if ((Test-Path -LiteralPath $wt -PathType Container) -and (Get-ChildItem -LiteralPath $wt -Force -ErrorAction SilentlyContinue | Select-Object -First 1)) {
        return 'other worktrees depend on this clone'
    }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) { return 'a clone, and git is not available to check it' }
    $st = & git --no-optional-locks -c safe.directory='*' -C $repo status --porcelain 2>$null
    if ($LASTEXITCODE -ne 0) { return 'a clone that git could not check' }
    if ($st) { return 'a clone with uncommitted changes' }
    $un = & git --no-optional-locks -c safe.directory='*' -C $repo log --branches --not --remotes -n 1 --format=%H 2>$null
    if ($LASTEXITCODE -ne 0) { return 'a clone that git could not check' }
    if ($un) { return 'a clone with unpushed commits' }
    return $null
}

# Get-SessionVerdict looks at one session folder and says keep or delete, and why.
function Get-SessionVerdict {
    param([IO.DirectoryInfo]$Dir, [datetime]$Cutoff, [string[]]$Running, [bool]$WithWorktrees)
    $v = [ordered]@{ Path = $Dir.FullName; Id = (Get-BaseName $Dir.Name); Bytes = 0L; Delete = $false; Why = '' }
    if ($Dir.Attributes -band [IO.FileAttributes]::ReparsePoint) { $v.Why = 'is a link'; return [pscustomobject]$v }
    $newest = $Dir.LastWriteTimeUtc
    if ($Dir.CreationTimeUtc -gt $newest) { $newest = $Dir.CreationTimeUtc }
    $bytes = 0L
    $gitFile = $false
    $gitDirs = @()
    $errs = $null
    $entries = Get-ChildItem -LiteralPath $Dir.FullName -Recurse -Force -ErrorAction SilentlyContinue -ErrorVariable errs
    if ($errs -and $errs.Count -gt 0) { $v.Why = 'could not read all of it, so "old" cannot be proven'; return [pscustomobject]$v }
    foreach ($e in $entries) {
        if ($e.Attributes -band [IO.FileAttributes]::ReparsePoint) { $v.Why = 'holds a link: ' + $e.Name; return [pscustomobject]$v }
        if ($e.LastWriteTimeUtc -gt $newest) { $newest = $e.LastWriteTimeUtc }
        if ($e.CreationTimeUtc -gt $newest) { $newest = $e.CreationTimeUtc }
        if ($e.PSIsContainer) {
            if ($e.Name -eq '.git') { $gitDirs += $e.FullName }
        } else {
            $bytes += $e.Length
            if ($e.Name -eq '.git') { $gitFile = $true }
        }
    }
    $v.Bytes = $bytes
    if ($newest -ge $Cutoff) { $v.Why = 'written recently (' + $newest.ToString('yyyy-MM-dd HH:mm') + ' UTC)'; return [pscustomobject]$v }
    if ($gitFile -and -not $WithWorktrees) { $v.Why = 'holds a git worktree'; return [pscustomobject]$v }
    foreach ($g in $gitDirs) {
        $risk = Test-CloneAtRisk -GitDir $g
        if ($risk) { $v.Why = $risk + ' (' + $g + ')'; return [pscustomobject]$v }
    }
    foreach ($line in $Running) {
        if ($line.IndexOf($v.Id, [StringComparison]::OrdinalIgnoreCase) -ge 0 -or
            $line.IndexOf($Dir.FullName, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
            $v.Why = 'a running process names it'; return [pscustomobject]$v
        }
    }
    $v.Delete = $true
    $v.Why = 'old (' + $newest.ToString('yyyy-MM-dd HH:mm') + ' UTC) and not in use'
    return [pscustomobject]$v
}

# The process list for the re-check after the rename: a fresh read, unless the tests supplied one.
function Get-FreshRunning { param([string[]]$Fallback) if ($null -ne $script:ProcessLinesOverride) { return $Fallback }; return @(Get-RunningLines) }

function Invoke-ScratchClean {
    [CmdletBinding()]
    param([string]$RootPath, [bool]$DoApply, [int]$AgeDays, [datetime]$Clock, [string[]]$Running, [bool]$WithWorktrees)
    $safe = Test-SafeRoot $RootPath
    if (-not $safe.Ok) { throw "Refusing root '$RootPath': $($safe.Why)" }
    $root = $safe.Full
    $cutoff = $Clock.ToUniversalTime().AddDays(-[math]::Max(1, $AgeDays))
    $freed = 0L; $would = 0L; $kept = 0; $failed = 0

    $verdicts = @()
    foreach ($proj in Get-ChildItem -LiteralPath $root -Directory -Force -ErrorAction SilentlyContinue) {
        if ($proj.Attributes -band [IO.FileAttributes]::ReparsePoint) { Write-Host "keep  $($proj.FullName): is a link"; $kept++; continue }
        foreach ($sess in Get-ChildItem -LiteralPath $proj.FullName -Directory -Force -ErrorAction SilentlyContinue) {
            $verdicts += Get-SessionVerdict -Dir $sess -Cutoff $cutoff -Running $Running -WithWorktrees $WithWorktrees
        }
    }
    # One session id can have folders under several projects: if any of them must stay, they all stay.
    $mustKeep = @{}
    foreach ($v in $verdicts) { if (-not $v.Delete) { $mustKeep[$v.Id.ToLowerInvariant()] = $true } }

    foreach ($v in $verdicts) {
        if ($v.Delete -and $mustKeep.ContainsKey($v.Id.ToLowerInvariant())) { $v.Delete = $false; $v.Why = 'the same session id is kept under another project' }
        if (-not $v.Delete) { $kept++; Write-Host ("keep  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
        if (-not (Test-Inside -Root $root -Child $v.Path -Depth 2)) { $kept++; Write-Host "keep  $($v.Path): outside the expected depth"; continue }
        if (-not $DoApply) { $would += $v.Bytes; Write-Host ("would delete  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }

        # Rename first: Windows refuses while a process has a file open inside, or has the folder as its working directory.
        $parent = Split-Path -Parent $v.Path
        $orig = Split-Path -Leaf $v.Path
        $tmpName = (Get-BaseName $orig) + '.deleting'
        $tmpPath = Join-Path $parent $tmpName
        if ($orig -ne $tmpName) {
            if (Test-Path -LiteralPath $tmpPath) { $kept++; Write-Host "keep  $($v.Path): $tmpName already exists"; continue }
            try { Rename-Item -LiteralPath $v.Path -NewName $tmpName -ErrorAction Stop }
            catch { $kept++; Write-Host ("keep  {0}: in use (cannot be renamed): {1}" -f $v.Path, $_.Exception.Message); continue }
        }
        # Check again on the renamed folder with a fresh process list; undo the rename if anything changed.
        $again = $null
        try { $again = Get-SessionVerdict -Dir (Get-Item -LiteralPath $tmpPath -Force) -Cutoff $cutoff -Running (Get-FreshRunning $Running) -WithWorktrees $WithWorktrees }
        catch { $again = $null }
        if (-not $again -or -not $again.Delete) {
            $why = if ($again) { $again.Why } else { 'could not re-check' }
            if ($orig -ne $tmpName) { try { Rename-Item -LiteralPath $tmpPath -NewName $orig -ErrorAction Stop } catch { Write-Host "warning: could not rename $tmpPath back to $orig" } }
            $kept++; Write-Host ("keep  {0}: changed while checking: {1}" -f $v.Path, $why); continue
        }
        try {
            Remove-Item -LiteralPath $tmpPath -Recurse -Force -ErrorAction Stop
            $freed += $v.Bytes; Write-Host ("deleted  {0}  [{1}]" -f $v.Path, (Format-Size $v.Bytes))
        } catch {
            $failed++; Write-Host ("not fully deleted (left as {0}, the next run retries): {1}" -f $tmpPath, $_.Exception.Message)
            $left = (Get-ChildItem -LiteralPath $tmpPath -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
            $freed += [math]::Max(0L, $v.Bytes - [long]$left)
        }
    }
    return [pscustomobject]@{ FreedBytes = $freed; WouldFreeBytes = $would; Kept = $kept; Failed = $failed }
}

function Invoke-GoCacheClean {
    param([bool]$DoApply, [double]$LimitGB, [string[]]$Running)
    $out = [pscustomobject]@{ FreedBytes = 0L; WouldFreeBytes = 0L }
    if ($LimitGB -le 0) { Write-Host 'go cache: step is off'; return $out }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Write-Host 'go cache: go is not installed, skipped'; return $out }
    # Ask Go from a neutral folder and without toolchain downloads.
    Push-Location -LiteralPath $env:USERPROFILE
    $oldTc = $env:GOTOOLCHAIN; $env:GOTOOLCHAIN = 'local'
    try { $dir = (& go env GOCACHE 2>$null | Select-Object -First 1) } finally { $env:GOTOOLCHAIN = $oldTc; Pop-Location }
    if (-not $dir -or -not [IO.Path]::IsPathRooted($dir) -or -not (Test-Path -LiteralPath $dir -PathType Container)) {
        Write-Host 'go cache: no cache folder, skipped'; return $out
    }
    $full = [IO.Path]::GetFullPath($dir).TrimEnd('\')
    $readme = Join-Path $full 'README'
    if (-not (Test-Path -LiteralPath $readme) -or -not ((Get-Content -LiteralPath $readme -Raw -ErrorAction SilentlyContinue) -match 'go clean -cache')) {
        Write-Host "go cache: $full does not look like a Go build cache, skipped"; return $out
    }
    $bytes = [long](Get-ChildItem -LiteralPath $full -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
    if ($bytes -le $LimitGB * 1GB) { Write-Host ("go cache: {0} is under the {1} GB limit, left alone" -f (Format-Size $bytes), $LimitGB); return $out }
    $busy = $Running | Where-Object { $_ -match '(?i)(^|[\\"\s])(go|compile|asm|cgo)(\.exe)?("|\s|$)' }
    if ($busy) { Write-Host 'go cache: a Go build is running, skipped'; return $out }
    if (-not $DoApply) { Write-Host ("would clean the go cache {0} [{1}]" -f $full, (Format-Size $bytes)); $out.WouldFreeBytes = $bytes; return $out }
    & go clean -cache
    if ($LASTEXITCODE -ne 0) { Write-Host "go cache: go clean failed (exit $LASTEXITCODE)" }
    $after = [long](Get-ChildItem -LiteralPath $full -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
    $out.FreedBytes = [math]::Max(0L, $bytes - $after)
    Write-Host ("cleaned the go cache {0} [{1} freed]" -f $full, (Format-Size $out.FreedBytes))
    return $out
}

function Register-CleanTask {
    $selfFull = [IO.Path]::GetFullPath($PSCommandPath)
    $tempRoot = [IO.Path]::GetFullPath($env:TEMP).TrimEnd('\')
    if ($selfFull.StartsWith($tempRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or $selfFull -match '(?i)\\Temp\\claude\\') {
        throw 'Run -RegisterTask from the repository checkout, not from a Temp folder: the task would run a file the cleanup itself deletes.'
    }
    # A stable copy, so a later checkout or branch switch cannot change what the unattended job runs.
    $dest = Join-Path $env:LOCALAPPDATA 'Unravis\maint'
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    $copy = Join-Path $dest 'Clean-Scratch.ps1'
    Copy-Item -LiteralPath $selfFull -Destination $copy -Force
    $log = Join-Path $dest 'clean-scratch.log'
    $pwsh = (Get-Command pwsh -ErrorAction Stop).Source
    $arg = '-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "{0}" -Apply -LogPath "{1}"' -f $copy, $log
    $action = New-ScheduledTaskAction -Execute $pwsh -Argument $arg
    $trigger = New-ScheduledTaskTrigger -Daily -At '13:00'
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Minutes 30)
    # Runs as the signed-in user, not elevated: it can only delete what that user can.
    Register-ScheduledTask -TaskName $script:TaskName -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
    Write-Host "registered the daily task '$($script:TaskName)' (13:00, user level), script copy $copy, log $log"
}

# Main. Dot-sourcing the file (for the tests) only defines the functions.
if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    if ($UnregisterTask) { Unregister-ScheduledTask -TaskName $script:TaskName -Confirm:$false -ErrorAction SilentlyContinue; Write-Host 'task removed'; return }
    if ($RegisterTask) { Register-CleanTask; return }
    if ($Apply -and ($PSBoundParameters.ContainsKey('ProcessLines') -or $PSBoundParameters.ContainsKey('Now') -or $PSBoundParameters.ContainsKey('Root'))) {
        throw '-Root, -ProcessLines and -Now are test hooks and cannot be combined with -Apply.'
    }
    if ($LogPath) { Start-Transcript -LiteralPath $LogPath -Append | Out-Null }
    try {
        $script:ProcessLinesOverride = $ProcessLines
        $rootPath = if ($Root) { $Root } else { Get-DefaultRoot }
        Write-Host ($(if ($Apply) { 'APPLY: deleting' } else { 'DRY RUN: nothing is deleted (add -Apply to delete)' }))
        $running = @(Get-RunningLines)
        $s = Invoke-ScratchClean -RootPath $rootPath -DoApply $Apply.IsPresent -AgeDays $MaxAgeDays -Clock $Now -Running $running -WithWorktrees $IncludeWorktrees.IsPresent
        $g = Invoke-GoCacheClean -DoApply $Apply.IsPresent -LimitGB $GoCacheLimitGB -Running $running
        if ($Apply) {
            Write-Host ("freed {0} (scratch) + {1} (go cache); kept {2} folders; {3} not fully deleted" -f (Format-Size $s.FreedBytes), (Format-Size $g.FreedBytes), $s.Kept, $s.Failed)
        } else {
            Write-Host ("would free {0} (scratch) + {1} (go cache); kept {2} folders" -f (Format-Size $s.WouldFreeBytes), (Format-Size $g.WouldFreeBytes), $s.Kept)
        }
    } finally {
        if ($LogPath) { Stop-Transcript | Out-Null }
    }
}
