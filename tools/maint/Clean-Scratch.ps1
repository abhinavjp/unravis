<#
.SYNOPSIS
  Frees disk space on a developer PC by pruning old Claude session scratch folders and capping the Go build cache.

.DESCRIPTION
  DRY RUN BY DEFAULT: without -Apply nothing is deleted; the script prints what it would delete and how much space that is.

  What it can delete (and nothing else):
    1. Session folders two levels under  %LOCALAPPDATA%\Temp\claude\<project-hash>\<session-id>  whose newest file write is older than
       -MaxAgeDays (default 1 day) and that no running process mentions.
    2. The Go build cache (`go clean -cache`) when it is larger than -GoCacheLimitGB (default 3 GB) and no Go build is running.

  What it will never delete: anything outside the root above, the repo, worktrees (a folder holding a linked-worktree `.git` file is kept
  unless -IncludeWorktrees), folders holding a link (junction or symlink) of any kind, secrets, user data, a session folder with a recent
  write or one that a running process names. A root that is not exactly <something>\Temp\claude is refused.

  The Go cache is a cache: it rebuilds on the next build. Scratch folders are session leftovers (clones, build outputs, task logs).

.PARAMETER Apply            Really delete. Without it the script only reports.
.PARAMETER MaxAgeDays       A session folder is old when its newest write is at least this many days ago. Minimum 1.
.PARAMETER GoCacheLimitGB   Clean the Go build cache when it is over this size. 0 turns the Go cache step off.
.PARAMETER IncludeWorktrees Also delete old folders that hold a linked git worktree (`.git` file). Off by default.
.PARAMETER Root             Test hook: another root, which must still be named <x>\Temp\claude.
.PARAMETER ProcessLines     Test hook: command lines to treat as the running processes instead of asking Windows.
.PARAMETER Now              Test hook: the current time (UTC).
.PARAMETER RegisterTask     Create a per-user daily scheduled task that runs this script with -Apply, then exit.
.PARAMETER UnregisterTask   Remove that task, then exit.
#>
[CmdletBinding()]
param(
    [switch]$Apply,
    [int]$MaxAgeDays = 1,
    [double]$GoCacheLimitGB = 3,
    [switch]$IncludeWorktrees,
    [string]$Root,
    [string[]]$ProcessLines,
    [datetime]$Now = [datetime]::UtcNow,
    [switch]$RegisterTask,
    [switch]$UnregisterTask
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:TaskName = 'Unravis-CleanScratch'

function Get-DefaultRoot {
    if (-not $env:LOCALAPPDATA) { return $null }
    return (Join-Path $env:LOCALAPPDATA 'Temp\claude')
}

# Test-SafeRoot says whether $Path is a root this script may prune: an existing real folder named <x>\Temp\claude, not a link.
function Test-SafeRoot {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return @{ Ok = $false; Why = 'no root' } }
    try { $full = [IO.Path]::GetFullPath($Path).TrimEnd('\', '/') } catch { return @{ Ok = $false; Why = 'bad path' } }
    if ($Path -match '\.\.') { return @{ Ok = $false; Why = 'path has ..' } }
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

function Get-RunningLines {
    if ($null -ne $script:ProcessLinesOverride) { return $script:ProcessLinesOverride }
    $lines = @()
    foreach ($p in Get-CimInstance Win32_Process -ErrorAction SilentlyContinue) {
        if ($p.CommandLine) { $lines += $p.CommandLine }
    }
    return $lines
}

function Format-Size { param([double]$Bytes) '{0:N2} GB' -f ($Bytes / 1GB) }

# Get-SessionVerdict looks at one session folder and says keep or delete, and why.
function Get-SessionVerdict {
    param([IO.DirectoryInfo]$Dir, [datetime]$Cutoff, [string[]]$Running, [bool]$WithWorktrees)
    $v = [ordered]@{ Path = $Dir.FullName; Bytes = 0L; Delete = $false; Why = '' }
    if ($Dir.Attributes -band [IO.FileAttributes]::ReparsePoint) { $v.Why = 'is a link'; return [pscustomobject]$v }
    $newest = $Dir.LastWriteTimeUtc
    $bytes = 0L
    $hasWorktree = $false
    # -Force sees hidden files; links are listed but never entered.
    foreach ($e in Get-ChildItem -LiteralPath $Dir.FullName -Recurse -Force -ErrorAction SilentlyContinue) {
        if ($e.Attributes -band [IO.FileAttributes]::ReparsePoint) { $v.Why = 'holds a link: ' + $e.Name; return [pscustomobject]$v }
        if ($e.LastWriteTimeUtc -gt $newest) { $newest = $e.LastWriteTimeUtc }
        if (-not $e.PSIsContainer) {
            $bytes += $e.Length
            if ($e.Name -eq '.git') { $hasWorktree = $true }
        }
    }
    $v.Bytes = $bytes
    if ($newest -ge $Cutoff) { $v.Why = 'written recently (' + $newest.ToString('yyyy-MM-dd HH:mm') + ' UTC)'; return [pscustomobject]$v }
    if ($hasWorktree -and -not $WithWorktrees) { $v.Why = 'holds a git worktree'; return [pscustomobject]$v }
    $id = $Dir.Name
    foreach ($line in $Running) {
        if ($line.IndexOf($id, [StringComparison]::OrdinalIgnoreCase) -ge 0 -or
            $line.IndexOf($Dir.FullName, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
            $v.Why = 'a running process names it'; return [pscustomobject]$v
        }
    }
    $v.Delete = $true
    $v.Why = 'old (' + $newest.ToString('yyyy-MM-dd HH:mm') + ' UTC) and not in use'
    return [pscustomobject]$v
}

function Invoke-ScratchClean {
    param([string]$RootPath, [bool]$DoApply, [int]$AgeDays, [datetime]$Clock, [string[]]$Running, [bool]$WithWorktrees)
    $safe = Test-SafeRoot $RootPath
    if (-not $safe.Ok) { throw "Refusing root '$RootPath': $($safe.Why)" }
    $root = $safe.Full
    $cutoff = $Clock.ToUniversalTime().AddDays(-[math]::Max(1, $AgeDays))
    $freed = 0L; $would = 0L; $kept = 0; $failed = 0
    foreach ($proj in Get-ChildItem -LiteralPath $root -Directory -Force -ErrorAction SilentlyContinue) {
        if ($proj.Attributes -band [IO.FileAttributes]::ReparsePoint) { Write-Host "keep  $($proj.FullName): is a link"; continue }
        foreach ($sess in Get-ChildItem -LiteralPath $proj.FullName -Directory -Force -ErrorAction SilentlyContinue) {
            $v = Get-SessionVerdict -Dir $sess -Cutoff $cutoff -Running $Running -WithWorktrees $WithWorktrees
            if (-not $v.Delete) { $kept++; Write-Host ("keep  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
            if (-not (Test-Inside -Root $root -Child $v.Path -Depth 2)) { $kept++; Write-Host "keep  $($v.Path): outside the expected depth"; continue }
            if (-not $DoApply) { $would += $v.Bytes; Write-Host ("would delete  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
            try {
                Remove-Item -LiteralPath $v.Path -Recurse -Force -ErrorAction Stop
                $freed += $v.Bytes; Write-Host ("deleted  {0}  [{1}]" -f $v.Path, (Format-Size $v.Bytes))
            } catch {
                $failed++; Write-Host ("kept (could not delete all of it, probably in use)  {0}: {1}" -f $v.Path, $_.Exception.Message)
                # Whatever was removed before the failure still counts; recount what is left.
                $left = (Get-ChildItem -LiteralPath $v.Path -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
                if ($left) { $freed += [math]::Max(0L, $v.Bytes - [long]$left) } else { $freed += $v.Bytes }
            }
        }
    }
    return [pscustomobject]@{ FreedBytes = $freed; WouldFreeBytes = $would; Kept = $kept; Failed = $failed }
}

function Invoke-GoCacheClean {
    param([bool]$DoApply, [double]$LimitGB, [string[]]$Running)
    $out = [pscustomobject]@{ FreedBytes = 0L; WouldFreeBytes = 0L }
    if ($LimitGB -le 0) { Write-Host 'go cache: step is off'; return $out }
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) { Write-Host 'go cache: go is not installed, skipped'; return $out }
    $dir = (& go env GOCACHE 2>$null | Select-Object -First 1)
    if (-not $dir -or -not [IO.Path]::IsPathRooted($dir) -or -not (Test-Path -LiteralPath $dir -PathType Container)) {
        Write-Host 'go cache: no cache folder, skipped'; return $out
    }
    $full = [IO.Path]::GetFullPath($dir).TrimEnd('\')
    # Only the folder Go itself names, and only if it looks like a Go cache (Go writes README and trim.txt into it).
    if (-not (Test-Path -LiteralPath (Join-Path $full 'README')) -or (Split-Path -Leaf $full) -in @('', 'Temp', 'Users')) {
        Write-Host "go cache: $full does not look like a Go build cache, skipped"; return $out
    }
    $bytes = [long](Get-ChildItem -LiteralPath $full -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
    if ($bytes -le $LimitGB * 1GB) { Write-Host ("go cache: {0} is under the {1} GB limit, left alone" -f (Format-Size $bytes), $LimitGB); return $out }
    $busy = $Running | Where-Object { $_ -match '(?i)(^|[\\"\s])(go|compile|link|asm|cgo|gopls)(\.exe)?("|\s|$)' }
    if ($busy) { Write-Host 'go cache: a Go build or tool is running, skipped'; return $out }
    if (-not $DoApply) { Write-Host ("would clean the go cache {0} [{1}]" -f $full, (Format-Size $bytes)); $out.WouldFreeBytes = $bytes; return $out }
    & go clean -cache
    $after = [long](Get-ChildItem -LiteralPath $full -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
    $out.FreedBytes = [math]::Max(0L, $bytes - $after)
    Write-Host ("cleaned the go cache {0} [{1} freed]" -f $full, (Format-Size $out.FreedBytes))
    return $out
}

function Register-CleanTask {
    $self = $PSCommandPath
    $pwsh = (Get-Command pwsh -ErrorAction Stop).Source
    $arg = '-NoProfile -ExecutionPolicy Bypass -File "{0}" -Apply' -f $self
    $action = New-ScheduledTaskAction -Execute $pwsh -Argument $arg
    $trigger = New-ScheduledTaskTrigger -Daily -At '13:00'
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Minutes 30)
    # Runs as the signed-in user, not elevated: it can only delete what that user can.
    Register-ScheduledTask -TaskName $script:TaskName -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
    Write-Host "registered the daily task '$($script:TaskName)' (13:00, user level)"
}

# Main. Dot-sourcing the file (for the tests) only defines the functions.
if ($MyInvocation.InvocationName -ne '.') {
    if ($UnregisterTask) { Unregister-ScheduledTask -TaskName $script:TaskName -Confirm:$false -ErrorAction SilentlyContinue; Write-Host 'task removed'; return }
    if ($RegisterTask) { Register-CleanTask; return }
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
}
