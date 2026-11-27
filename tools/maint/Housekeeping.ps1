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
    3. Registered worktrees of this repository under D:\Scratch, the Temp\claude root or <repo>\.claude\worktrees (agent worktrees; one
       whose own Claude project folder changed within -IdleMinutes also stays) are removed with a plain
       `git worktree remove` (never forced, never deleting a branch) only when: clean (no untracked files), no process names the
       path, untouched for a day, HEAD contained in origin/develop or origin/master after a fresh fetch (a failed fetch keeps
       everything), not the main checkout or the checkout running this script. Missing worktree folders are pruned.
    4. %TEMP%\unravis-probe* and %TEMP%\unravis-dev-* folders, with the Clean-Scratch age, link, in-use and clone-at-risk rules.
    5. Every Go build cache over -GoCacheLimitGB (default 8): go env GOCACHE, %LOCALAPPDATA%\go-build, the Claude desktop app's
       Packages\Claude_*\LocalCache\Local\go-build and D:\GoCache\build (Clean-Scratch.ps1 has the rules). The log line shows the GB
       freed per location and the module cache size (measured, never deleted). A cache over 3x the limit that cannot be cleaned
       because a Go build is running is logged as "go cache over limit but a build is running"; builds are never killed.
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
.PARAMETER GoCacheLimitGB  Clean each Go build cache over this size (default 8, the runner cache cap). 0 turns the Go cache step off.
#>
[CmdletBinding()]
param(
    [ValidateSet('stop', 'sessionend', 'periodic', 'manual')][string]$Event = 'manual',
    [switch]$DryRun,
    [string]$Repo,
    [switch]$Install,
    [switch]$Uninstall,
    [switch]$Worker,
    [double]$GoCacheLimitGB = 8
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
    param([string]$Path, [string[]]$Running, [datetime]$Clock, [int]$AgeHours, [string[]]$Roots, [string[]]$Protect, [string[]]$OwnerDirs = $null, [int]$IdleMinutes = 120)
    if (-not (Test-UnderAny -Path $Path -Roots $Roots)) { return 'outside the scratch folders' }
    # A protected folder equal to the worktree, or inside it (a session whose working directory is a subfolder), keeps it.
    foreach ($p in $Protect) {
        if (-not $p) { continue }
        $pp = [IO.Path]::GetFullPath($p).TrimEnd('\')
        if ($pp -ieq $Path -or $pp.StartsWith($Path + '\', [StringComparison]::OrdinalIgnoreCase)) { return 'the main checkout or the checkout in use' }
    }
    $it = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $it) { return $null }
    if ($it.Attributes -band [IO.FileAttributes]::ReparsePoint) { return 'is a link' }
    $fwd = $Path.Replace("\", "/")
    foreach ($line in $Running) { if ($line.IndexOf($Path, [StringComparison]::OrdinalIgnoreCase) -ge 0 -or $line.IndexOf($fwd, [StringComparison]::OrdinalIgnoreCase) -ge 0) { return 'a running process names it' } }
    # A worktree inside a Claude session folder follows that session: a recently written transcript means it may be in use.
    $sm = [regex]::Match($Path, '(?i)\\Temp\\claude\\[^\\]+\\([^\\]+)(\\|$)')
    if ($sm.Success -and $null -ne $OwnerDirs) {
        $ov = Get-OwnerVerdict -Id $sm.Groups[1].Value -OwnerDirs $OwnerDirs -IdleMinutes $IdleMinutes -Clock $Clock
        if ($ov -like 'its transcript changed*') { return 'its Claude session may be alive' }
    }
    # A worktree that has its own Claude project folder (an agent worktree) is in use while a transcript there changed recently.
    if ($null -ne $OwnerDirs) {
        $own = ConvertTo-ProjectName $Path
        foreach ($d in $OwnerDirs) {
            if ((Split-Path -Leaf $d) -ine $own) { continue }
            foreach ($f in @(Get-ChildItem -LiteralPath $d -Force -ErrorAction SilentlyContinue)) {
                if ($f.LastWriteTimeUtc -ge $Clock.ToUniversalTime().AddMinutes(-$IdleMinutes)) { return 'its Claude session may be alive' }
            }
        }
    }
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
    # Ignored files (customer extracts, local settings, probe scripts) are in no branch: only rebuildable folders may be ignored.
    $ig = Get-GitLines -Dir $Path -GitArgs @('status', '--porcelain', '--ignored=traditional', '--untracked-files=normal')
    if ($null -eq $ig) { return 'git could not check it' }
    foreach ($l in $ig) {
        if ($l -notmatch '^!! (.+)$') { continue }
        $rel = $Matches[1].Trim('"')
        if ($rel -notmatch '(^|/)(node_modules|dist|\.cache|\.vite|coverage)/$') { return 'ignored files that are in no branch (' + $rel + ')' }
    }
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
    param([string]$Repo, [string[]]$Worktrees, [bool]$DoApply, [datetime]$Clock, [string[]]$Running, [string[]]$Roots, [string[]]$Protect, [int]$AgeHours = 24, [string[]]$OwnerDirs = $null, [int]$IdleMinutes = 120)
    $r = [pscustomobject]@{ Removed = 0; WouldRemove = 0; Kept = 0; Pruned = $false }
    $cands = @()
    foreach ($w in @($Worktrees | Select-Object -Skip 1)) {
        $why = Get-WorktreeKeepReason -Path $w -Running $Running -Clock $Clock -AgeHours $AgeHours -Roots $Roots -Protect (@($Repo) + $Protect) -OwnerDirs $OwnerDirs -IdleMinutes $IdleMinutes
        if ($why) { $r.Kept++; Write-Host "keep  worktree $w : $why"; continue }
        $cands += $w
    }
    if ($cands.Count -eq 0) { return $r }
    $fetched = $false
    if ($DoApply) {
        $env:GIT_TERMINAL_PROMPT = '0'; $env:GCM_INTERACTIVE = 'never'; $env:GIT_SSH_COMMAND = 'ssh -o BatchMode=yes'
        # A hard time limit: a stalled network or a credential prompt must not hold the lock for ever.
        try {
            $fa = @('--no-optional-locks', '-C', ('"{0}"' -f $Repo), 'fetch', '--quiet', '--no-tags', 'origin', '+refs/heads/develop:refs/remotes/origin/develop', '+refs/heads/master:refs/remotes/origin/master')
            $fp = Start-Process -FilePath 'git' -ArgumentList $fa -PassThru -WindowStyle Hidden
            if ($fp.WaitForExit(120000)) { $fetched = ($fp.ExitCode -eq 0) } else { try { & taskkill /PID $fp.Id /T /F 2>&1 | Out-Null } catch { } }
        } catch { $fetched = $false }
        if (-not $fetched) { Write-Host 'could not fetch origin: no worktree is removed this run' }
    }
    foreach ($w in $cands) {
        if (-not (Test-Path -LiteralPath $w)) { continue }  # a missing folder is only pruned below
        if ($DoApply -and -not $fetched) { $r.Kept++; continue }
        if (-not (Test-ContainedInRemoteMain -Path $w)) { $r.Kept++; Write-Host "keep  worktree $w : its commits are not in origin/develop or origin/master"; continue }
        if (-not $DoApply) { $r.WouldRemove++; Write-Host "would remove worktree $w (clean, idle, merged; refs not refreshed in a dry run)"; continue }
        # Rename probe: a folder that is some process's working directory cannot be renamed, so it stays.
        $leaf = Split-Path -Leaf $w
        $probeName = $leaf + '.removing'
        if (Test-Path -LiteralPath (Join-Path (Split-Path -Parent $w) $probeName)) { $r.Kept++; Write-Host "keep  worktree $w : a .removing folder is in the way"; continue }
        try { Rename-Item -LiteralPath $w -NewName $probeName -ErrorAction Stop } catch { $r.Kept++; Write-Host "keep  worktree $w : in use (cannot be renamed)"; continue }
        try { Rename-Item -LiteralPath (Join-Path (Split-Path -Parent $w) $probeName) -NewName $leaf -ErrorAction Stop } catch { $r.Kept++; Write-Host "keep  worktree $w : could not be renamed back, left as $probeName"; continue }
        & git --no-optional-locks -C $Repo worktree remove -- $w 2>$null
        if ($LASTEXITCODE -eq 0) { $r.Removed++; Write-Host "removed worktree $w" } else { $r.Kept++; Write-Host "keep  worktree $w : git refused to remove it" }
    }
    # Prune only when every worktree whose folder is gone lies under the roots: a worktree on an unplugged drive keeps its registration.
    if ($DoApply) {
        $gone = @($Worktrees | Select-Object -Skip 1 | Where-Object { -not (Test-Path -LiteralPath $_) })
        $onlyOurs = $true
        foreach ($g in $gone) { if (-not (Test-UnderAny -Path $g -Roots $Roots)) { $onlyOurs = $false } }
        if ($onlyOurs) { & git --no-optional-locks -C $Repo worktree prune 2>$null; $r.Pruned = ($LASTEXITCODE -eq 0) }
    }
    return $r
}

# Invoke-HousekeepingRun: the whole sweep. Everything it may touch comes in through the parameters, so the tests run it on a throwaway tree.
function Invoke-HousekeepingRun {
    param(
        [string]$Repo, [bool]$DoApply, [datetime]$Clock, [string]$TempRoot, [string]$ProjectsRoot,
        [string[]]$ScratchRoots, [string[]]$Running, [string[]]$Protect = @(),
        [int]$IdleMinutes = 120, [int]$AgeDays = 1, [double]$GoCacheLimitGB = 8, [object[]]$GoLocations = $null, [bool]$UseGo = $true, [string]$ModCacheDir = ''
    )
    $out = [ordered]@{ ScratchFreed = 0L; ScratchWould = 0L; ScratchKept = 0; WtRemoved = 0; WtWould = 0; WtKept = 0; TempFreed = 0L; TempWould = 0L; GoFreed = 0L; GoWould = 0L; GoLocs = @(); GoNotes = @(); ModBytes = -1L; Note = ''; Err = '' }
    $wts = Get-RepoWorktrees -Repo $Repo
    if (-not $wts) { $out.Note = 'git could not list the worktrees: nothing done'; return [pscustomobject]$out }
    $owners = @(Get-OwnerDirs -Worktrees $wts -ProjectsRoot $ProjectsRoot)
    $s = Invoke-ScratchClean -RootPath (Join-Path $TempRoot 'claude') -DoApply $DoApply -AgeDays $AgeDays -Clock $Clock -Running $Running -WithWorktrees $false -OwnerDirs $owners -IdleMinutes $IdleMinutes
    $out.ScratchFreed = $s.FreedBytes; $out.ScratchWould = $s.WouldFreeBytes; $out.ScratchKept = $s.Kept
    try {
        $w = Invoke-WorktreeClean -Repo $Repo -Worktrees $wts -DoApply $DoApply -Clock $Clock -Running $Running -Roots (@($ScratchRoots) + (Join-Path $TempRoot 'claude')) -Protect $Protect -OwnerDirs $owners -IdleMinutes $IdleMinutes
        $out.WtRemoved = $w.Removed; $out.WtWould = $w.WouldRemove; $out.WtKept = $w.Kept
    } catch { $out.Err = 'worktree step failed: ' + ($_.Exception.Message -replace '\s+', ' ') }
    $t = Invoke-TempPatternClean -TempRoot $TempRoot -DoApply $DoApply -AgeDays $AgeDays -Clock $Clock -Running $Running
    $out.TempFreed = $t.FreedBytes; $out.TempWould = $t.WouldFreeBytes
    try {
        $g = Invoke-GoCacheClean -DoApply $DoApply -LimitGB $GoCacheLimitGB -Running $Running -Locations $GoLocations -UseGo $UseGo -ModCacheDir $ModCacheDir
        $out.GoFreed = $g.FreedBytes; $out.GoWould = $g.WouldFreeBytes; $out.GoLocs = @($g.Locations); $out.GoNotes = @($g.Notes); $out.ModBytes = $g.ModCacheBytes
    } catch { $out.Err = (($out.Err + '; go cache step failed: ' + ($_.Exception.Message -replace '\s+', ' ')).TrimStart('; ')) }
    return [pscustomobject]$out
}

# Format-GoPart: "go [label GB, label GB]" with the GB freed (or, in a dry run, that would go) per location, then the module cache size.
function Format-GoPart {
    param([bool]$DoApply, $R)
    # Every location says what happened to it, so a skipped cache can never read as "0.00 GB".
    $parts = @()
    foreach ($l in @($R.GoLocs)) {
        $sz = Format-Size $l.Bytes
        $parts += switch ($l.Status) {
            'cleaned' { '{0} freed {1} of {2}' -f $l.Label, (Format-Size $l.FreedBytes), $sz }
            'would clean' { '{0} would free {1}' -f $l.Label, $sz }
            'under limit' { '{0} {1} under limit' -f $l.Label, $sz }
            'busy' { '{0} {1} over limit, build running' -f $l.Label, $sz }
            'not a go cache' { '{0} skipped (no Go README)' -f $l.Label }
            default { '{0} {1}' -f $l.Label, $l.Status }
        }
    }
    $s = 'go [' + ($parts -join '; ') + ']'
    if ($R.ModBytes -ge 0) { $s += '; modcache ' + (Format-Size $R.ModBytes) }
    return $s
}

function Format-RunLine {
    param([string]$Ev, [bool]$DoApply, $R)
    if ($R.Note) { return ('{0:yyyy-MM-ddTHH:mmZ} {1} {2}' -f [datetime]::UtcNow, $Ev, $R.Note) }
    $e = if ($R.Err) { '; ' + $R.Err } else { '' }
    foreach ($n in @($R.GoNotes)) { $e += '; ' + $n }
    $go = Format-GoPart -DoApply $DoApply -R $R
    if ($DoApply) {
        return (('{0:yyyy-MM-ddTHH:mmZ} {1} apply: scratch {2} kept {3}; worktrees removed {4} kept {5}; temp {6}; {7}' -f [datetime]::UtcNow, $Ev, (Format-Size $R.ScratchFreed), $R.ScratchKept, $R.WtRemoved, $R.WtKept, (Format-Size $R.TempFreed), $go) + $e)
    }
    return (('{0:yyyy-MM-ddTHH:mmZ} {1} dry run: scratch {2} would go; worktrees {3} would go; temp {4}; {5}' -f [datetime]::UtcNow, $Ev, (Format-Size $R.ScratchWould), $R.WtWould, (Format-Size $R.TempWould), $go) + $e)
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
            $age = ((Get-Date) - (Get-Item -LiteralPath $stateFile).LastWriteTime).TotalMinutes
            if ($age -ge 0 -and $age -lt $mins) { exit 0 }   # a stamp in the future (clock change) does not throttle
        }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        if ($isHook -and -not $Worker) {
            # Claim the slot, start the real work hidden in the background and return at once.
            $a = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', ('"{0}"' -f $PSCommandPath), '-Event', $Event, '-Worker')
            if ($Repo) { $a += @('-Repo', ('"{0}"' -f $Repo)) }
            if ($PSBoundParameters.ContainsKey('GoCacheLimitGB')) { $a += @('-GoCacheLimitGB', $GoCacheLimitGB.ToString([Globalization.CultureInfo]::InvariantCulture)) }
            Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $a -WindowStyle Hidden | Out-Null
            Set-Content -LiteralPath $stateFile -Value ([datetime]::UtcNow.ToString('o'))   # claimed only once the worker really started
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
            $doApply = -not $DryRun   # -DryRun wins for every event
            $running = @(Get-RunningLines)   # throws when the list cannot be trusted: then nothing is deleted
            $protect = @($PSScriptRoot | ForEach-Object { (Get-GitLines -Dir $_ -GitArgs @('rev-parse', '--show-toplevel')) } | Where-Object { $_ })
            $protect += (Get-Location).Path
            $args2 = @{
                Repo = $repoFolder; DoApply = $doApply; Clock = [datetime]::UtcNow; TempRoot = (Join-Path $env:LOCALAPPDATA 'Temp')
                ProjectsRoot = (Join-Path $env:USERPROFILE '.claude\projects'); ScratchRoots = @('D:\Scratch', (Join-Path $repoFolder '.claude\worktrees')); Running = $running; Protect = $protect
                GoCacheLimitGB = $GoCacheLimitGB
            }
            if ($alwaysZero) { $r = Invoke-HousekeepingRun @args2 6>$null } else { $r = Invoke-HousekeepingRun @args2 }
            $line = Format-RunLine -Ev $Event -DoApply $doApply -R $r
            Add-LogLine -Dir $dir -Line $line
            if (-not $alwaysZero) { Write-Host $line }
        } finally { if ($lock) { $lock.Dispose() } }
    } catch {
        try { Add-LogLine -Dir (Get-StateDir) -Line ('{0:yyyy-MM-ddTHH:mmZ} {1} error: {2}' -f [datetime]::UtcNow, $Event, ($_.Exception.Message -replace '\s+', ' ')) } catch { }
        if (-not $alwaysZero) { Write-Host "error: $($_.Exception.Message)"; exit 1 }
    }
    exit 0
}
