#Requires -Version 7
<#
.SYNOPSIS
  Frees disk space on a developer PC by pruning old Claude session scratch folders and capping the Go build cache.

.DESCRIPTION
  DRY RUN BY DEFAULT: without -Apply nothing is deleted; the script prints what it would delete and how much space that is.

  What it can delete (and nothing else):
    1. Session folders exactly two levels under  %LOCALAPPDATA%\Temp\claude\<project-hash>\<session-id>  that are old (newest write or
       creation time at least -MaxAgeDays ago, default 1) and not in use (see below).
    2. Every Go build cache, each when larger than -GoCacheLimitGB (default 8 GB, the runner cache cap) and no Go build is running:
       `go env GOCACHE`, %LOCALAPPDATA%\go-build, %USERPROFILE%\AppData\Local\Packages\Claude_*\LocalCache\Local\go-build (the Claude
       desktop app redirects LOCALAPPDATA there, so an unattended run does not see it through `go env`) and D:\GoCache\build. A folder
       must have a README that mentions `go clean -cache`. It is cleaned with `go clean -cache` / `-testcache` (GOCACHE set to that
       folder for the call only), or by rename-then-delete of its contents when go is absent or fails. The module cache is only measured.

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

  Known limits: a session that is open but idle (no process names it, no file open, not its working directory) and untouched for
  a day cannot be told from an abandoned one and may be deleted; a session folder is re-creatable scratch, so that is accepted.
  A process running elevated may hide its command line from a non-elevated run; the rename step still protects any open folder.
  When one session id has several project folders they are renamed together and rolled back together if any one fails.

  Never touched: anything outside the root, the repo, D:\secret, user data. A root that is not exactly <x>\Temp\claude is refused.

.PARAMETER Apply            Really delete. Without it the script only reports.
.PARAMETER MaxAgeDays       A session folder is old when its newest write is at least this many days ago. Minimum 1.
.PARAMETER GoCacheLimitGB   Clean each Go build cache that is over this size (default 8). 0 turns the Go cache step off.
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
    [double]$GoCacheLimitGB = 8,
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

function Get-BaseName { param([string]$Name) return ($Name -replace '\.deleting2?$', '') }

# Test-CloneAtRisk: a reason string when a clone (a `.git` folder) holds work that exists nowhere else or git cannot say; else $null.
function Test-CloneAtRisk {
    param([string]$GitDir)
    $repo = Split-Path -Parent $GitDir
    $wt = Join-Path $GitDir 'worktrees'
    if ((Test-Path -LiteralPath $wt -PathType Container) -and (Get-ChildItem -LiteralPath $wt -Force -ErrorAction SilentlyContinue | Select-Object -First 1)) {
        return 'other worktrees depend on this clone'
    }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) { return 'a clone, and git is not available to check it' }
    # fsmonitor off: a scratch repo's own config must not make this unattended run execute a command.
    $st = & git --no-optional-locks -c core.fsmonitor=false -C $repo status --porcelain --untracked-files=all 2>$null
    if ($LASTEXITCODE -ne 0) { return 'a clone that git could not check' }
    if ($st) { return 'a clone with uncommitted changes' }
    # --all covers branches, tags, stash and a detached HEAD.
    $un = & git --no-optional-locks -c core.fsmonitor=false -C $repo log --all --not --remotes -n 1 --format=%H 2>$null
    if ($LASTEXITCODE -ne 0) { return 'a clone that git could not check' }
    if ($un) { return 'a clone with unpushed commits, tags or a stash' }
    return $null
}

# Get-SessionVerdict looks at one session folder and says keep or delete, and why.
function Get-SessionVerdict {
    param([IO.DirectoryInfo]$Dir, [datetime]$Cutoff, [string[]]$Running, [bool]$WithWorktrees)
    $v = [ordered]@{ Path = $Dir.FullName; Id = (Get-BaseName $Dir.Name); Bytes = 0L; Delete = $false; Why = '' }
    if ($Dir.Attributes -band [IO.FileAttributes]::ReparsePoint) { $v.Why = 'is a link'; return [pscustomobject]$v }
    if ($Dir.Name -match '[. ]$') { $v.Why = 'name ends in a dot or space (Windows may treat it as another folder)'; return [pscustomobject]$v }
    $newest = $Dir.LastWriteTimeUtc
    if ($Dir.CreationTimeUtc -gt $newest) { $newest = $Dir.CreationTimeUtc }
    $bytes = 0L
    $gitFiles = @()
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
            if ($e.Name -eq '.git') { $gitFiles += $e.FullName }
        }
    }
    $v.Bytes = $bytes
    if ($newest -ge $Cutoff) { $v.Why = 'written recently (' + $newest.ToString('yyyy-MM-dd HH:mm') + ' UTC)'; return [pscustomobject]$v }
    if ($gitFiles.Count -gt 0 -and -not $WithWorktrees) { $v.Why = 'holds a git worktree'; return [pscustomobject]$v }
    # A linked worktree (.git file) is only deleted with -IncludeWorktrees, and then only when it holds nothing unsaved.
    foreach ($g in @($gitDirs) + @($gitFiles)) {
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

# Get-OwnerVerdict: when $OwnerDirs is given (the Claude project folders of this repository and its worktrees), a session folder is
# only eligible when its transcript <id>.jsonl is in one of them and nothing wrote to that transcript (or its subagent folder)
# within $IdleMinutes. Returns the reason to KEEP the folder, or $null when it may be judged further. $null OwnerDirs = no filter.
function Get-OwnerVerdict {
    param([string]$Id, [string[]]$OwnerDirs, [int]$IdleMinutes, [datetime]$Clock)
    if ($null -eq $OwnerDirs) { return $null }
    $latest = $null
    foreach ($d in $OwnerDirs) {
        foreach ($p in @((Join-Path $d ($Id + '.jsonl')), (Join-Path $d $Id))) {
            $it = Get-Item -LiteralPath $p -Force -ErrorAction SilentlyContinue
            if ($it -and (-not $latest -or $it.LastWriteTimeUtc -gt $latest)) { $latest = $it.LastWriteTimeUtc }
        }
    }
    if (-not $latest) { return 'not a session of this repository (no transcript in its project folders)' }
    if ($latest -ge $Clock.ToUniversalTime().AddMinutes(-$IdleMinutes)) { return 'its transcript changed within the last ' + $IdleMinutes + ' minutes (session may be alive)' }
    return $null
}

# The process list for the re-check after the rename: a fresh read, unless the tests supplied one.
function Get-FreshRunning { param([string[]]$Fallback) if ($null -ne $script:ProcessLinesOverride) { return $Fallback }; return @(Get-RunningLines) }

function Invoke-ScratchClean {
    [CmdletBinding()]
    param([string]$RootPath, [bool]$DoApply, [int]$AgeDays, [datetime]$Clock, [string[]]$Running, [bool]$WithWorktrees, [string[]]$OwnerDirs = $null, [int]$IdleMinutes = 120)
    $safe = Test-SafeRoot $RootPath
    if (-not $safe.Ok) { throw "Refusing root '$RootPath': $($safe.Why)" }
    $root = $safe.Full
    $cutoff = $Clock.ToUniversalTime().AddDays(-[math]::Max(1, $AgeDays))
    $freed = 0L; $would = 0L; $kept = 0; $failed = 0

    $verdicts = @()
    $rootErrs = $null
    $projs = @(Get-ChildItem -LiteralPath $root -Directory -Force -ErrorAction SilentlyContinue -ErrorVariable rootErrs)
    $listFailed = [bool]($rootErrs -and $rootErrs.Count -gt 0)
    foreach ($proj in $projs) {
        if ($proj.Attributes -band [IO.FileAttributes]::ReparsePoint) { Write-Host "keep  $($proj.FullName): is a link"; $kept++; continue }
        $projErrs = $null
        $sessions = @(Get-ChildItem -LiteralPath $proj.FullName -Directory -Force -ErrorAction SilentlyContinue -ErrorVariable projErrs)
        if ($projErrs -and $projErrs.Count -gt 0) { $listFailed = $true }
        foreach ($sess in $sessions) {
            $notMine = Get-OwnerVerdict -Id (Get-BaseName $sess.Name) -OwnerDirs $OwnerDirs -IdleMinutes $IdleMinutes -Clock $Clock
            if ($notMine) { $verdicts += [pscustomobject][ordered]@{ Path = $sess.FullName; Id = (Get-BaseName $sess.Name); Bytes = 0L; Delete = $false; Why = $notMine }; continue }
            $verdicts += Get-SessionVerdict -Dir $sess -Cutoff $cutoff -Running $Running -WithWorktrees $WithWorktrees
        }
    }
    # A folder that could not be listed may hold the twin of a session that must stay, so nothing is deleted this run.
    if ($listFailed) {
        Write-Host 'could not list every project folder: keeping everything this run'
        foreach ($v in $verdicts) { $v.Delete = $false; $v.Why = 'the folder tree could not be listed completely' }
    }
    # One session id can have folders under several projects: if any of them must stay, they all stay.
    $mustKeep = @{}
    foreach ($v in $verdicts) { if (-not $v.Delete) { $mustKeep[$v.Id.ToLowerInvariant()] = $true } }

    $groups = [ordered]@{}
    foreach ($v in $verdicts) {
        if ($v.Delete -and $mustKeep.ContainsKey($v.Id.ToLowerInvariant())) { $v.Delete = $false; $v.Why = 'the same session id is kept under another project' }
        if (-not $v.Delete) { $kept++; Write-Host ("keep  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
        if (-not (Test-Inside -Root $root -Child $v.Path -Depth 2)) { $kept++; Write-Host "keep  $($v.Path): outside the expected depth"; continue }
        if (-not $DoApply) { $would += $v.Bytes; Write-Host ("would delete  {0}  [{1}]  {2}" -f $v.Path, (Format-Size $v.Bytes), $v.Why); continue }
        $k = $v.Id.ToLowerInvariant()
        if (-not $groups.Contains($k)) { $groups[$k] = New-Object System.Collections.ArrayList }
        [void]$groups[$k].Add($v)
    }

    # Apply, one session id (all its project folders) at a time: rename every member first, re-check every member, and only then
    # delete. If any step fails for any member, every member is renamed back and the whole group stays.
    foreach ($k in @($groups.Keys)) {
        $moved = @()
        $problem = $null
        foreach ($v in $groups[$k]) {
            $parent = Split-Path -Parent $v.Path
            $orig = Split-Path -Leaf $v.Path
            # Always a real rename, also for a leftover from a crashed run: Windows refuses it while a process has a file open inside
            # or has the folder as its working directory, which is the in-use probe.
            $tmpName = if ($orig -match '\.deleting$') { (Get-BaseName $orig) + '.deleting2' } else { (Get-BaseName $orig) + '.deleting' }
            $tmpPath = Join-Path $parent $tmpName
            if (Test-Path -LiteralPath $tmpPath) { $problem = "$($v.Path): $tmpName already exists"; break }
            try { Rename-Item -LiteralPath $v.Path -NewName $tmpName -ErrorAction Stop }
            catch { $problem = ("{0}: in use (cannot be renamed): {1}" -f $v.Path, $_.Exception.Message); break }
            $moved += [pscustomobject]@{ V = $v; Orig = $orig; Tmp = $tmpPath }
        }
        if (-not $problem) {
            foreach ($m in $moved) {
                $again = $null
                try { $again = Get-SessionVerdict -Dir (Get-Item -LiteralPath $m.Tmp -Force) -Cutoff $cutoff -Running (Get-FreshRunning $Running) -WithWorktrees $WithWorktrees }
                catch { $again = $null }
                if (-not $again -or -not $again.Delete) {
                    $problem = ("{0}: changed while checking: {1}" -f $m.V.Path, $(if ($again) { $again.Why } else { 'could not re-check' }))
                    break
                }
            }
        }
        if ($problem) {
            foreach ($m in $moved) {
                try { Rename-Item -LiteralPath $m.Tmp -NewName $m.Orig -ErrorAction Stop }
                catch { $failed++; Write-Host "warning: could not rename $($m.Tmp) back to $($m.Orig): the next run re-checks it" }
            }
            $kept += $groups[$k].Count
            Write-Host "keep  session $k (all of its folders): $problem"
            continue
        }
        foreach ($m in $moved) {
            try {
                Remove-Item -LiteralPath $m.Tmp -Recurse -Force -ErrorAction Stop
                $freed += $m.V.Bytes; Write-Host ("deleted  {0}  [{1}]" -f $m.V.Path, (Format-Size $m.V.Bytes))
            } catch {
                $failed++; Write-Host ("not fully deleted (left as {0}, the next run retries): {1}" -f $m.Tmp, $_.Exception.Message)
                $left = (Get-ChildItem -LiteralPath $m.Tmp -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
                $freed += [math]::Max(0L, $m.V.Bytes - [long]$left)
            }
        }
    }
    return [pscustomobject]@{ FreedBytes = $freed; WouldFreeBytes = $would; Kept = $kept; Failed = $failed }
}

# Get-GoCacheLocations: every folder that may hold a Go build cache, deduplicated. A process started by the Claude desktop app has
# %LOCALAPPDATA% redirected into Packages\Claude_*\LocalCache\Local, so its builds fill a cache that `go env GOCACHE` of an
# unattended run (the scheduled task) never reports (incident 2026-11-27: 84 GiB). Only existing, non-link folders are returned.
function Get-GoCacheLocations {
    param([string]$GoEnvDir, [string]$LocalAppData, [string]$UserProfile, [string[]]$ExtraDirs = @('D:\GoCache\build'))
    $cands = @()
    if ($GoEnvDir) { $cands += [pscustomobject]@{ Label = 'go-env'; Path = $GoEnvDir } }
    if ($LocalAppData) { $cands += [pscustomobject]@{ Label = 'localappdata'; Path = (Join-Path $LocalAppData 'go-build') } }
    if ($UserProfile) {
        $pk = Join-Path $UserProfile 'AppData\Local\Packages'
        if (Test-Path -LiteralPath $pk -PathType Container) {
            foreach ($d in @(Get-ChildItem -LiteralPath $pk -Directory -Filter 'Claude_*' -Force -ErrorAction SilentlyContinue)) {
                $cands += [pscustomobject]@{ Label = 'pkg-' + $d.Name; Path = (Join-Path $d.FullName 'LocalCache\Local\go-build') }
            }
        }
    }
    foreach ($x in $ExtraDirs) { if ($x) { $cands += [pscustomobject]@{ Label = 'extra'; Path = $x } } }
    $seen = @{}; $found = @()
    foreach ($c in $cands) {
        if (-not [IO.Path]::IsPathRooted($c.Path)) { continue }
        $full = [IO.Path]::GetFullPath($c.Path).TrimEnd('\')
        $item = Get-Item -LiteralPath $full -Force -ErrorAction SilentlyContinue
        if (-not $item -or -not $item.PSIsContainer) { continue }
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { Write-Host "go cache: $full is a link, skipped"; continue }
        $k = $full.ToLowerInvariant()
        if ($seen.ContainsKey($k)) { continue }
        $seen[$k] = $true
        $found += [pscustomobject]@{ Label = $c.Label; Path = $full }
    }
    return $found
}

function Get-FolderBytes { param([string]$Dir) return [long](Get-ChildItem -LiteralPath $Dir -Recurse -Force -File -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum }

# Clear-GoCacheFolder empties one Go cache folder. `go clean -cache` runs with GOCACHE set to that folder for this call only (restored
# after), then `go clean -testcache`. When go is absent or fails, every entry except README is renamed and then deleted (a rename Windows
# refuses means the entry is in use and stays). Returns how it was done.
function Clear-GoCacheFolder {
    param([string]$Dir, [bool]$UseGo)
    if ($UseGo) {
        $oldCache = $env:GOCACHE; $oldTc = $env:GOTOOLCHAIN; $ok = $false
        Push-Location -LiteralPath $env:USERPROFILE
        try {
            $env:GOCACHE = $Dir; $env:GOTOOLCHAIN = 'local'
            & go clean -cache 2>&1 | Out-Null
            $ok = ($LASTEXITCODE -eq 0)
            if ($ok) { & go clean -testcache 2>&1 | Out-Null }
        } catch { $ok = $false } finally {
            if ($null -eq $oldCache) { Remove-Item Env:GOCACHE -ErrorAction SilentlyContinue } else { $env:GOCACHE = $oldCache }
            if ($null -eq $oldTc) { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue } else { $env:GOTOOLCHAIN = $oldTc }
            Pop-Location
        }
        if ($ok) { return 'go clean' }
        Write-Host "go cache: go clean failed for $Dir, falling back to rename and delete"
    }
    foreach ($c in @(Get-ChildItem -LiteralPath $Dir -Force -ErrorAction SilentlyContinue)) {
        if ($c.Name -ieq 'README') { continue }
        if ($c.Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        $tmpName = $c.Name + '.deleting-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
        try { Rename-Item -LiteralPath $c.FullName -NewName $tmpName -ErrorAction Stop } catch { continue }
        try { Remove-Item -LiteralPath (Join-Path $Dir $tmpName) -Recurse -Force -ErrorAction Stop } catch { Write-Host "go cache: could not fully delete $tmpName in $Dir, the next run retries" }
    }
    return 'rename and delete'
}

# Invoke-GoCacheClean checks EVERY Go build cache location (see Get-GoCacheLocations), cleans each one that is over the limit while no
# Go build is running, and reports the GB freed per location. GOMODCACHE is measured and reported, never deleted.
# Test hooks: -Locations replaces the discovery, -UseGo $false forces the rename-and-delete path, -ModCacheDir replaces `go env`.
function Invoke-GoCacheClean {
    param([bool]$DoApply, [double]$LimitGB, [string[]]$Running, [object[]]$Locations = $null, [bool]$UseGo = $true, [string]$ModCacheDir = '')
    $out = [pscustomobject]@{ FreedBytes = 0L; WouldFreeBytes = 0L; Locations = @(); Notes = @(); ModCacheBytes = -1L; ModCachePath = '' }
    if ($LimitGB -le 0) { Write-Host 'go cache: step is off'; return $out }
    $haveGo = $UseGo -and [bool](Get-Command go -ErrorAction SilentlyContinue)
    $goEnvDir = $null; $modDir = $ModCacheDir
    if ($haveGo -and ($null -eq $Locations -or -not $modDir)) {
        # Ask Go from a neutral folder and without toolchain downloads.
        Push-Location -LiteralPath $env:USERPROFILE
        $oldTc = $env:GOTOOLCHAIN; $env:GOTOOLCHAIN = 'local'
        try { $ge = @(& go env GOCACHE GOMODCACHE 2>$null) } finally { if ($null -eq $oldTc) { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue } else { $env:GOTOOLCHAIN = $oldTc }; Pop-Location }
        if ($ge.Count -ge 1) { $goEnvDir = [string]$ge[0] }
        if (-not $modDir -and $ge.Count -ge 2) { $modDir = [string]$ge[1] }
    }
    $locs = if ($null -ne $Locations) { @($Locations) } else { @(Get-GoCacheLocations -GoEnvDir $goEnvDir -LocalAppData $env:LOCALAPPDATA -UserProfile $env:USERPROFILE) }
    if ($locs.Count -eq 0) { Write-Host 'go cache: no cache folder found, skipped' }
    $busy = $Running | Where-Object { $_ -match '(?i)(^|[\\"\s])(go|compile|asm|cgo)(\.exe)?("|\s|$)' }
    foreach ($l in $locs) {
        $full = [IO.Path]::GetFullPath($l.Path).TrimEnd('\')
        $rec = [pscustomobject]@{ Label = $l.Label; Path = $full; Bytes = 0L; FreedBytes = 0L; WouldBytes = 0L; Status = '' }
        $out.Locations += $rec
        $readme = Join-Path $full 'README'
        if (-not (Test-Path -LiteralPath $full -PathType Container)) { $rec.Status = 'missing'; continue }
        if (-not (Test-Path -LiteralPath $readme) -or -not ((Get-Content -LiteralPath $readme -Raw -ErrorAction SilentlyContinue) -match 'go clean -cache')) {
            $rec.Status = 'not a go cache'; Write-Host "go cache: $full does not look like a Go build cache, skipped"; continue
        }
        $rec.Bytes = Get-FolderBytes $full
        if ($rec.Bytes -le $LimitGB * 1GB) { $rec.Status = 'under limit'; Write-Host ("go cache: {0} {1} is under the {2} GB limit, left alone" -f $full, (Format-Size $rec.Bytes), $LimitGB); continue }
        if ($busy) {
            $rec.Status = 'busy'; Write-Host ("go cache: {0} [{1}] is over the limit but a Go build is running, skipped" -f $full, (Format-Size $rec.Bytes))
            if ($rec.Bytes -gt 3 * $LimitGB * 1GB) { $out.Notes += ('go cache over limit but a build is running: {0} {1} (over 3x the {2} GB limit)' -f $rec.Label, (Format-Size $rec.Bytes), $LimitGB) }
            continue
        }
        if (-not $DoApply) { $rec.Status = 'would clean'; $rec.WouldBytes = $rec.Bytes; $out.WouldFreeBytes += $rec.Bytes; Write-Host ("would clean the go cache {0} [{1}]" -f $full, (Format-Size $rec.Bytes)); continue }
        $how = Clear-GoCacheFolder -Dir $full -UseGo $haveGo
        $rec.FreedBytes = [math]::Max(0L, $rec.Bytes - (Get-FolderBytes $full))
        $rec.Status = 'cleaned'; $out.FreedBytes += $rec.FreedBytes
        Write-Host ("cleaned the go cache {0} [{1} freed, {2}]" -f $full, (Format-Size $rec.FreedBytes), $how)
    }
    if ($modDir -and [IO.Path]::IsPathRooted($modDir) -and (Test-Path -LiteralPath $modDir -PathType Container)) {
        $out.ModCachePath = $modDir; $out.ModCacheBytes = Get-FolderBytes $modDir
        Write-Host ("go module cache: {0} [{1}] (reported only, never deleted)" -f $modDir, (Format-Size $out.ModCacheBytes))
    }
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
