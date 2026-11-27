#Requires -Version 7.2
<#
.SYNOPSIS
  The "rc slot" of docs/ci/RC-CADENCE.md as a plain script: no model, nothing to type. A Windows scheduled task runs it every 2 hours (-Install).

.DESCRIPTION
  One run is one slot. The first "no" below ends it: one line in rc-slot.log, a fresh status.json, exit 0 and no console output (-DryRun prints
  the decision instead and changes nothing). Missing tools and failed promotions or releases exit 1.
    1 Single instance (lock file in the state folder; a second run says "busy").
    2 Tools: git, gh (signed in), pwsh, and Node exactly 22.23.5 in -NodeDir (put first on PATH for the child scripts).
    3 git fetch origin (tags too); the last rc tag is the newest v<x.y.z>-rc.<n> by n, then by version.
    4 Newest green develop: the newest completed ci.yml push run on develop that succeeded; its commit G must be on origin/develop.
    5 Product change between the tag and G (git diff <tag>...<G>): internal/, cmd/, web/src/, tools/release/, go.mod, go.sum, root *.go;
      tests, test data and *.md do not count.
    6 No release in flight: no open promote/* pull request into master, release lock not held (the lock branch is only read, never written).
    7 master does not already contain G.
  All seven passed: Promote-Develop.ps1 -UpTo G from a detached work tree (-WorkTree), then Release.ps1 -Channel rc -Yes from a work tree at
  origin/master, then the new rc tag and the "carries" list are read back and written to status.json. After a release (only then) the
  housekeeping script runs if it is installed. Nothing is ever installed on this PC. A signing passphrase, Proton Pass or UAC stop shows up as
  a "failed" run with that text as the reason.

  status.json: { at (UTC), outcome released|skipped|failed|busy, reason, rc, previous, gSha, carries[] }. It is written for EVERY run.

.PARAMETER Repo        GitHub repository (default abhinavjp/unravis).
.PARAMETER RepoFolder  The checkout used to read git. Default: "repo" in <StateDir>\rcslot.json, else the checkout this script is in.
.PARAMETER WorkTree    Detached work tree the scripts run from (default D:\Scratch\relwt). Created and refreshed here, cleaned after every run.
.PARAMETER NodeDir     Folder of node.exe 22.23.5.
.PARAMETER StateDir    Log, status.json and lock (default %LOCALAPPDATA%\Unravis\rcslot).
.PARAMETER DryRun      Decide and print; no promotion, no release, no log, no status.json (git fetch and read-only gh calls still run).
.PARAMETER Install     Copy this script to <StateDir>, save the repository folder and register the user-level task 'Unravis rc slot' (every 2 hours,
                       only while the user is logged on, so the user's gh and Proton Pass sessions work; no admin). Same refusals as
                       tools/maint/Housekeeping.ps1 -Install (Claude desktop app sandbox, redirected %LOCALAPPDATA%).
.PARAMETER Uninstall   Remove that task. Nothing else.
.PARAMETER Requester   Name shown to others in "busy" messages of the release lock (default rc-slot).
#>
[CmdletBinding()]
param(
    [string]$Repo = 'abhinavjp/unravis',
    [string]$RepoFolder = '',
    [string]$WorkTree = 'D:\Scratch\relwt',
    [string]$NodeDir = 'D:\Scratch\node-dl\x\node-v22.23.5-win-x64',
    [string]$StateDir = '',
    [switch]$DryRun,
    [switch]$Install,
    [switch]$Uninstall,
    [string]$Requester = 'rc-slot'
)

$script:RcTask = 'Unravis rc slot'
$script:NodeVersion = 'v22.23.5'
$script:LogKeep = 500
$script:Cfg = @{}

# ---- Small pure helpers -------------------------------------------------------------------------------------------------------

function Get-UtcStamp { param([datetime]$At = [datetime]::UtcNow) return $At.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture) }

# Token-like text never reaches the log, status.json or the console; a Proton Pass or passphrase stop keeps its words.
function Protect-Text {
    param([string]$Text)
    $t = [string]$Text
    $t = $t -replace '\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,})', '***'
    $t = $t -replace '(authorization:\s*)(bearer|token|basic)?\s*\S+', '$1***'
    $t = $t -replace '\b(passphrase|password|passwd|token|secret|api[_-]?key)(\s*=\s*)\S+', '$1$2***'
    return $t
}

# Remote text (commit subjects, a lock requester, tool output) becomes one short, plain line before it is stored.
function Format-OneLine {
    param([string]$Text, [int]$Max = 200)
    $t = ([regex]::Replace((Protect-Text $Text), '[\x00-\x1f\x7f]+', ' ')).Trim()
    if ($t.Length -gt $Max) { $t = $t.Substring(0, $Max) }
    return $t
}

function Get-ShortSha { param([string]$Sha) if ($Sha.Length -ge 7) { return $Sha.Substring(0, 7) } return $Sha }

# Product = what ships in a build. Tests, test data and every *.md are not.
function Test-ProductPath {
    param([string]$Path)
    $p = ([string]$Path).Trim().Trim('"').Replace('\', '/')
    if (-not $p) { return $false }
    if ($p -match '\.md$') { return $false }
    if ($p -match '_test\.go$|\.test\.ts$|\.spec\.ts$|\.test\.js$') { return $false }
    if ($p -match '(^|/)(testdata|tests|__tests__)/') { return $false }
    if ($p -match '^tools/release/Test-[^/]*\.ps1$') { return $false }
    if ($p -match '^(internal|cmd|web/src|tools/release)/') { return $true }
    if ($p -match '^go\.(mod|sum)$') { return $true }
    return ($p -match '^[^/]+\.go$')
}

# git diff runs with --no-renames, so a file moved out of a product folder lists its old path and still counts. Callers wrap the result in @().
function Get-ProductPaths {
    param([string[]]$Paths)
    return @($Paths | Where-Object { Test-ProductPath $_ })
}

function Get-RcKey {
    param([string]$Tag)
    $m = [regex]::Match(([string]$Tag).Trim(), '^v(\d+\.\d+\.\d+)-rc\.(\d+)$')
    if (-not $m.Success) { return $null }
    try { return [pscustomobject]@{ Rc = [long]$m.Groups[2].Value; Version = [version]$m.Groups[1].Value } } catch { return $null }
}

function Compare-RcKey {
    param($A, $B)
    if ($A.Rc -ne $B.Rc) { return $(if ($A.Rc -gt $B.Rc) { 1 } else { -1 }) }
    return $A.Version.CompareTo($B.Version)
}

function Get-LastRcTag {
    param([string[]]$Tags)
    $best = $null
    foreach ($t in $Tags) {
        $k = Get-RcKey $t
        if (-not $k) { continue }
        if (-not $best -or (Compare-RcKey $k $best.Key) -gt 0) { $best = @{ Tag = ([string]$t).Trim(); Key = $k } }
    }
    if ($best) { return $best.Tag }
    return $null
}

function Test-RcNewer {
    param([string]$Tag, [string]$Than)
    $a = Get-RcKey $Tag; $b = Get-RcKey $Than
    if (-not $a) { return $false }
    if (-not $b) { return $true }
    return ((Compare-RcKey $a $b) -gt 0)
}

# What the rc carries: first-parent subjects from develop, one line each, no repeats, at most $Max.
function Get-Carries {
    param([string[]]$Subjects, [int]$Max = 8, [int]$Width = 120)
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $out = [Collections.Generic.List[string]]::new()
    foreach ($s in $Subjects) {
        $t = Format-OneLine ([string]$s) $Width
        if (-not $t -or -not $seen.Add($t)) { continue }
        $out.Add($t)
        if ($out.Count -ge $Max) { break }
    }
    return $out.ToArray()
}

function New-Refusal { param([string]$Outcome, [string]$Reason) return [pscustomobject]@{ Outcome = $Outcome; Reason = $Reason } }

function New-SlotResult {
    param([string]$Outcome, [string]$Reason, [string]$Rc = '', [string]$Previous = '', [string]$GSha = '', [string[]]$Carries = @())
    return [pscustomobject]@{ Outcome = $Outcome; Reason = $Reason; Rc = $Rc; Previous = $Previous; GSha = $GSha; Carries = @($Carries) }
}

# Reads the release-lock answer (a gh api result). $null = free to go, else why not. Anything odd is a skip: never a guess that it is free.
function Get-LockRefusal {
    param($Api)
    $text = ($Api.Lines -join "`n")
    $unreadable = New-Refusal 'skipped' 'release lock unreadable'
    if ($Api.Code -ne 0) {
        if ($text -match 'HTTP 404|Not Found') { return $null }
        return $unreadable
    }
    $rec = $null
    try {
        $j = $text | ConvertFrom-Json -ErrorAction Stop
        $b64 = ([string]$j.content) -replace '\s', ''
        if (-not $b64) { return $unreadable }
        $rec = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64)) | ConvertFrom-Json -ErrorAction Stop
    } catch { return $unreadable }
    if ($null -eq $rec) { return $unreadable }
    $state = [string]$rec.state
    if ($state -eq 'free') { return $null }
    if ($state -ne 'held') { return $unreadable }
    # Held with merges explicitly allowed (the release is past tagging): go. Missing or odd holdMerges counts as "on hold".
    if ($rec.holdMerges -is [bool] -and -not $rec.holdMerges) { return $null }
    $who = Format-OneLine ([string]$rec.requester) 40
    if (-not $who) { $who = 'unknown' }
    return (New-Refusal 'skipped' "release lock held by $who")
}

# ---- Decision ---------------------------------------------------------------------------------------------------------------------

# Runs the steps in order; the first step that returns a refusal ends it. Steps are scriptblocks (param($ctx)) so tests can inject any.
function Get-SlotDecision {
    param([scriptblock[]]$Steps, [hashtable]$Ctx)
    foreach ($s in $Steps) {
        $r = @(& $s $Ctx) | Where-Object { $_ -and $_.PSObject.Properties['Outcome'] } | Select-Object -First 1
        if ($r) { return [pscustomobject]@{ Go = $false; Outcome = $r.Outcome; Reason = $r.Reason } }
    }
    return [pscustomobject]@{ Go = $true; Outcome = 'go'; Reason = 'all checks passed' }
}

function Get-SlotSteps {
    return @(
        { param($c) Step-Busy $c },
        { param($c) Step-Tools $c },
        { param($c) Step-LastTag $c },
        { param($c) Step-Green $c },
        { param($c) Step-Product $c },
        { param($c) Step-InFlight $c },
        { param($c) Step-NotOnMaster $c }
    )
}

# ---- Wrappers around everything outside this process (the tests replace these) ------------------------------------------------

function Invoke-RcNative {
    param([string]$Exe, [string[]]$CmdArgs, [string]$Dir = '')
    $ErrorActionPreference = 'Continue'
    $lines = @(); $code = 127
    if ($Dir) { Push-Location -LiteralPath $Dir }
    try {
        $out = & $Exe @CmdArgs 2>&1
        $code = $LASTEXITCODE
        foreach ($x in @($out)) { if ($null -ne $x) { $lines += [string]$x } }
    } catch { $lines = @([string]$_.Exception.Message); $code = 127 } finally { if ($Dir) { Pop-Location } }
    return [pscustomobject]@{ Code = $code; Lines = $lines }
}

function Invoke-RcGit { param([string]$Dir, [string[]]$GitArgs) return (Invoke-RcNative -Exe 'git' -CmdArgs (@('--no-optional-locks', '-c', 'core.fsmonitor=false', '-C', $Dir) + $GitArgs)) }

function Invoke-RcGh { param([string[]]$GhArgs) return (Invoke-RcNative -Exe 'gh' -CmdArgs $GhArgs) }

function Test-RcCommand { param([string]$Name) return [bool](Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue) }

function Invoke-RcNode { param([string[]]$NodeArgs) return (Invoke-RcNative -Exe (Join-Path $script:Cfg.NodeDir 'node.exe') -CmdArgs $NodeArgs) }

function Set-NodePath {
    $dir = $script:Cfg.NodeDir
    $sep = [IO.Path]::PathSeparator
    if (($env:PATH -split [regex]::Escape($sep)) -notcontains $dir) { $env:PATH = $dir + $sep + $env:PATH }
}

# Runs one of the release scripts in a work tree and keeps only the last 40 output lines (redacted). Code, Tail, Last (last non-empty line).
function Invoke-RcScript {
    param([string]$Dir, [string]$Script, [string[]]$ScriptArgs)
    $r = Invoke-RcNative -Exe 'pwsh' -CmdArgs (@('-NoProfile', '-File', $Script) + $ScriptArgs) -Dir $Dir
    $tail = @($r.Lines | Select-Object -Last 40 | ForEach-Object { Protect-Text $_ })
    $last = ($tail | Where-Object { $_.Trim() } | Select-Object -Last 1)
    return [pscustomobject]@{ Code = $r.Code; Tail = $tail; Last = [string]$last }
}

# ---- Lock file (one slot at a time) -------------------------------------------------------------------------------------------

# The OS drops the lock with the process, so a crash leaves nothing stale. Returns the open stream, or $null when another run holds it.
function Enter-SlotLock {
    param([string]$Dir)
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    try {
        $fs = [IO.FileStream]::new((Join-Path $Dir 'rc-slot.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
    } catch { return $null }
    $b = [Text.Encoding]::UTF8.GetBytes("$PID $(Get-UtcStamp)")
    $fs.SetLength(0); $fs.Write($b, 0, $b.Length); $fs.Flush()
    return $fs
}

# Dry runs must not create anything: look at the lock without taking it.
function Test-SlotLockHeld {
    param([string]$Dir)
    $p = Join-Path $Dir 'rc-slot.lock'
    if (-not (Test-Path -LiteralPath $p -PathType Leaf)) { return $false }
    try { [IO.FileStream]::new($p, 'Open', 'Read', 'None').Dispose(); return $false } catch { return $true }
}

function Get-LockAgeText {
    param([string]$Dir)
    try { return ('{0:N0} min' -f ([datetime]::UtcNow - (Get-Item -LiteralPath (Join-Path $Dir 'rc-slot.lock') -ErrorAction Stop).LastWriteTimeUtc).TotalMinutes) } catch { return 'unknown age' }
}

# ---- The seven checks ---------------------------------------------------------------------------------------------------------

function Step-Busy {
    param($c)
    $dir = $script:Cfg.StateDir
    $busy = New-Refusal 'busy' ('another rc slot is running (lock held, started {0} ago)' -f (Get-LockAgeText $dir))
    if ($script:Cfg.DryRun) { if (Test-SlotLockHeld $dir) { return $busy }; return $null }
    $c.Lock = Enter-SlotLock $dir
    if (-not $c.Lock) { return $busy }
}

function Step-Tools {
    param($c)
    foreach ($t in 'git', 'gh', 'pwsh') { if (-not (Test-RcCommand $t)) { return (New-Refusal 'failed' "$t not found") } }
    if ((Invoke-RcGh @('auth', 'status')).Code -ne 0) { return (New-Refusal 'failed' 'gh is not signed in (gh auth status failed)') }
    $n = Invoke-RcNode @('--version')
    $got = [string]($n.Lines | Select-Object -First 1)
    if ($n.Code -ne 0 -or $got.Trim() -ne $script:NodeVersion) {
        return (New-Refusal 'failed' ("Node $($script:NodeVersion.TrimStart('v')) not found in $($script:Cfg.NodeDir) (got '" + (Format-OneLine $got 40) + "')"))
    }
    Set-NodePath
}

function Step-LastTag {
    param($c)
    $rf = $script:Cfg.RepoFolder
    if ((Invoke-RcGit $rf @('fetch', '--tags', 'origin')).Code -ne 0) { return (New-Refusal 'failed' 'git fetch origin failed') }
    $t = Invoke-RcGit $rf @('tag', '--list', 'v*-rc.*')
    if ($t.Code -ne 0) { return (New-Refusal 'failed' 'git tag --list failed') }
    $tag = Get-LastRcTag $t.Lines
    if (-not $tag) { return (New-Refusal 'skipped' 'no rc tag found (the first rc is released by hand)') }
    $c.Tag = $tag
}

function Test-GitAncestor {
    param([string]$Sha, [string]$Ref)
    $r = Invoke-RcGit $script:Cfg.RepoFolder @('merge-base', '--is-ancestor', $Sha, $Ref)
    if ($r.Code -eq 0) { return $true }
    if ($r.Code -eq 1) { return $false }
    return $null
}

function Step-Green {
    param($c)
    $r = Invoke-RcGh @('run', 'list', '-R', $script:Cfg.Repo, '--workflow', 'ci.yml', '--branch', 'develop', '--event', 'push', '--status', 'completed', '--limit', '30', '--json', 'databaseId,headSha,conclusion')
    if ($r.Code -ne 0) { return (New-Refusal 'failed' 'gh run list failed') }
    try { $runs = @(($r.Lines -join "`n") | ConvertFrom-Json -ErrorAction Stop) } catch { return (New-Refusal 'failed' 'gh run list gave unreadable output') }
    $ok = @($runs | Where-Object { $_.conclusion -eq 'success' -and ([string]$_.headSha) -match '^[0-9a-f]{40}$' } | Sort-Object { [long]$_.databaseId } -Descending)
    if ($ok.Count -eq 0) { return (New-Refusal 'skipped' 'no green develop run') }
    $g = [string]$ok[0].headSha
    $c.GSha = $g
    $on = Test-GitAncestor $g 'origin/develop'
    if ($null -eq $on) { return (New-Refusal 'failed' 'git merge-base against origin/develop failed') }
    if (-not $on) { return (New-Refusal 'skipped' "green run commit $(Get-ShortSha $g) is not on origin/develop") }
}

function Step-Product {
    param($c)
    $d = Invoke-RcGit $script:Cfg.RepoFolder @('-c', 'core.quotepath=off', 'diff', '--name-only', '--no-renames', "$($c.Tag)...$($c.GSha)")
    if ($d.Code -ne 0) { return (New-Refusal 'failed' "git diff $($c.Tag)...$(Get-ShortSha $c.GSha) failed") }
    if (@(Get-ProductPaths $d.Lines).Count -eq 0) { return (New-Refusal 'skipped' "only docs/tests changed since $($c.Tag)") }
}

function Step-InFlight {
    param($c)
    $pr = Invoke-RcGh @('pr', 'list', '-R', $script:Cfg.Repo, '--base', 'master', '--state', 'open', '--limit', '100', '--json', 'headRefName')
    if ($pr.Code -ne 0) { return (New-Refusal 'failed' 'gh pr list failed') }
    try { $prs = @(($pr.Lines -join "`n") | ConvertFrom-Json -ErrorAction Stop) } catch { return (New-Refusal 'failed' 'gh pr list gave unreadable output') }
    $open = @($prs | Where-Object { ([string]$_.headRefName) -like 'promote/*' } | Select-Object -First 1)
    if ($open.Count -gt 0) { return (New-Refusal 'skipped' ('promotion pull request open (' + (Format-OneLine ([string]$open[0].headRefName) 60) + ')')) }
    return (Get-LockRefusal (Invoke-RcGh @('api', "repos/$($script:Cfg.Repo)/contents/lock.json?ref=release-lock")))
}

function Step-NotOnMaster {
    param($c)
    $on = Test-GitAncestor $c.GSha 'origin/master'
    if ($null -eq $on) { return (New-Refusal 'failed' 'git merge-base against origin/master failed') }
    if ($on) { return (New-Refusal 'skipped' 'nothing to promote (master already contains the green commit)') }
}

# ---- Work tree ----------------------------------------------------------------------------------------------------------------

function Test-SamePath {
    param([string]$A, [string]$B)
    $cmp = if ($IsWindows) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
    $x = [IO.Path]::GetFullPath($A).TrimEnd('\', '/'); $y = [IO.Path]::GetFullPath($B).TrimEnd('\', '/')
    return [string]::Equals($x, $y, $cmp)
}

# Reset and clean ONLY when git says the work tree's own top folder is exactly -WorkTree (and not the main checkout): git clean must never
# reach a parent repository. Returns $null when clean or when the folder is gone, else why not.
function Clear-WorkTree {
    $wt = $script:Cfg.WorkTree
    if (-not (Test-Path -LiteralPath $wt -PathType Container)) { return $null }
    $top = Invoke-RcGit $wt @('rev-parse', '--show-toplevel')
    $t = [string]($top.Lines | Select-Object -First 1)
    if ($top.Code -ne 0 -or -not $t -or -not (Test-SamePath $t $wt) -or (Test-SamePath $t $script:Cfg.RepoFolder)) { return "refusing to clean $wt (it is not a work tree of its own)" }
    foreach ($a in @('reset', '--hard', '-q'), @('clean', '-fdxq'), @('checkout', '--detach', '-q')) {
        if ((Invoke-RcGit $wt $a).Code -ne 0) { return "could not clean the work tree ($($a[0]) failed)" }
    }
    return $null
}

# Puts the work tree on exactly $Sha, detached. Returns $null on success, else why not.
function Update-WorkTree {
    param([string]$Sha)
    $wt = $script:Cfg.WorkTree; $rf = $script:Cfg.RepoFolder
    if (-not (Test-Path -LiteralPath (Join-Path $wt '.git'))) {
        if ((Test-Path -LiteralPath $wt) -and (Get-ChildItem -LiteralPath $wt -Force | Select-Object -First 1)) { return "work tree folder $wt exists and is not a work tree" }
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $wt) | Out-Null
        $null = Invoke-RcGit $rf @('worktree', 'prune')
        if ((Invoke-RcGit $rf @('worktree', 'add', '--detach', $wt, $Sha)).Code -ne 0) { return 'git worktree add failed' }
    } else {
        $why = Clear-WorkTree
        if ($why) { return $why }
        if ((Invoke-RcGit $wt @('checkout', '--detach', $Sha)).Code -ne 0) { return "git checkout --detach $(Get-ShortSha $Sha) failed" }
    }
    $head = Invoke-RcGit $wt @('rev-parse', 'HEAD')
    if ($head.Code -ne 0 -or [string]($head.Lines | Select-Object -First 1) -ne $Sha) { return 'the work tree is not at the expected commit' }
    return $null
}

function Get-RefSha {
    param([string]$Ref)
    $r = Invoke-RcGit $script:Cfg.RepoFolder @('rev-parse', '--verify', '--quiet', ('{0}^{{commit}}' -f $Ref))
    $s = [string]($r.Lines | Select-Object -First 1)
    if ($r.Code -eq 0 -and $s -match '^[0-9a-f]{40}$') { return $s }
    return $null
}

# ---- The release itself ---------------------------------------------------------------------------------------------------------

function Save-LastOutput {
    param([string]$Name, $Run)
    try {
        New-Item -ItemType Directory -Force -Path $script:Cfg.StateDir | Out-Null
        Set-Content -LiteralPath (Join-Path $script:Cfg.StateDir 'last-output.txt') -Value (@("$(Get-UtcStamp) $Name exit $($Run.Code)") + @($Run.Tail)) -Encoding utf8
    } catch { }
}

function Get-RunFailure {
    param([string]$Name, $Run)
    $why = if ($Run.Last) { Format-OneLine $Run.Last 300 } else { "exit code $($Run.Code)" }
    return "${Name}: $why"
}

function Invoke-SlotAct {
    param([hashtable]$Ctx)
    $prev = [string]$Ctx.Tag; $g = [string]$Ctx.GSha
    $fail = { param($why) New-SlotResult 'failed' $why '' $prev $g }
    $wt = $script:Cfg.WorkTree
    try {
        $dev = Get-RefSha 'origin/develop'
        if (-not $dev) { return (& $fail 'origin/develop cannot be read') }
        $why = Update-WorkTree $dev
        if ($why) { return (& $fail $why) }
        $run = Invoke-RcScript $wt (Join-Path $wt 'tools\release\Promote-Develop.ps1') @('-UpTo', $g, '-CiWaitMinutes', '60', '-Repo', $script:Cfg.Repo)
        Save-LastOutput 'promote' $run
        if ($run.Code -ne 0) { return (& $fail (Get-RunFailure 'promote' $run)) }

        if ((Invoke-RcGit $script:Cfg.RepoFolder @('fetch', 'origin', 'master')).Code -ne 0) { return (& $fail 'git fetch origin master failed') }
        if ((Test-GitAncestor $g 'origin/master') -ne $true) { return (& $fail "promote finished but master does not contain $(Get-ShortSha $g)") }
        $master = Get-RefSha 'origin/master'
        if (-not $master) { return (& $fail 'origin/master cannot be read') }
        $why = Update-WorkTree $master
        if ($why) { return (& $fail $why) }
        $run = Invoke-RcScript $wt (Join-Path $wt 'tools\release\Release.ps1') @('-Channel', 'rc', '-Yes', '-Requester', $script:Cfg.Requester, '-WaitMinutes', '30', '-Repo', $script:Cfg.Repo)
        Save-LastOutput 'release' $run
        if ($run.Code -ne 0) { return (& $fail (Get-RunFailure 'release' $run)) }
        return (Read-ReleaseResult -Previous $prev -GSha $g -Wt $wt)
    } finally {
        $null = Invoke-RcGit $script:Cfg.RepoFolder @('worktree', 'prune')
        $left = Clear-WorkTree
        if ($left) { Add-SlotLog -Dir $script:Cfg.StateDir -Outcome 'note' -Reason $left }
    }
}

function Read-ReleaseResult {
    param([string]$Previous, [string]$GSha, [string]$Wt)
    $note = ''
    $st = Invoke-RcScript $Wt (Join-Path $Wt 'tools\release\Release.ps1') @('-Status', '-Json', '-Repo', $script:Cfg.Repo)
    if ($st.Code -ne 0) { $note = '; Release.ps1 -Status failed' }
    $rl = Invoke-RcGh @('release', 'list', '-R', $script:Cfg.Repo, '--limit', '30', '--json', 'tagName,isDraft')
    $new = $null
    if ($rl.Code -eq 0) {
        try { $new = Get-LastRcTag @(($rl.Lines -join "`n" | ConvertFrom-Json -ErrorAction Stop) | Where-Object { -not $_.isDraft } | ForEach-Object { [string]$_.tagName }) } catch { $new = $null }
    }
    if (-not $new -or -not (Test-RcNewer $new $Previous)) { return (New-SlotResult 'failed' 'no new tag' '' $Previous $GSha) }
    $log = Invoke-RcGit $script:Cfg.RepoFolder @('log', '--first-parent', '--pretty=%s', "$Previous..$GSha")
    $carries = if ($log.Code -eq 0) { @(Get-Carries $log.Lines) } else { @() }
    return (New-SlotResult 'released' "released $new$note" $new $Previous $GSha $carries)
}

# ---- Log and status -------------------------------------------------------------------------------------------------------------

# One line per run, newest last; the file keeps its last 500 lines.
function Add-SlotLog {
    param([string]$Dir, [string]$Outcome, [string]$Reason)
    try {
        New-Item -ItemType Directory -Force -Path $Dir | Out-Null
        $f = Join-Path $Dir 'rc-slot.log'
        Add-Content -LiteralPath $f -Value ('{0} {1} {2}' -f (Get-UtcStamp), $Outcome, (Format-OneLine $Reason 300)) -Encoding utf8
        $all = @(Get-Content -LiteralPath $f)
        if ($all.Count -gt $script:LogKeep) { Set-Content -LiteralPath $f -Value $all[($all.Count - $script:LogKeep)..($all.Count - 1)] -Encoding utf8 }
    } catch { }
}

function Write-SlotStatus {
    param([string]$Dir, $Result)
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    $s = [ordered]@{
        at = (Get-UtcStamp); outcome = [string]$Result.Outcome; reason = (Format-OneLine ([string]$Result.Reason) 300)
        rc = [string]$Result.Rc; previous = [string]$Result.Previous; gSha = [string]$Result.GSha; carries = @($Result.Carries)
    }
    $tmp = Join-Path $Dir 'status.json.tmp'
    [IO.File]::WriteAllText($tmp, ($s | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $tmp -Destination (Join-Path $Dir 'status.json') -Force
}

function Invoke-PostReleaseHousekeeping {
    $hk = Join-Path $env:LOCALAPPDATA 'Unravis\maint\Housekeeping.ps1'
    if (-not (Test-Path -LiteralPath $hk -PathType Leaf)) { return }
    try {
        $p = Start-Process -FilePath 'pwsh' -ArgumentList @('-NoProfile', '-File', ('"{0}"' -f $hk), '-Event', 'periodic') -PassThru -WindowStyle Hidden
        if (-not $p.WaitForExit(600000)) { try { & taskkill /PID $p.Id /T /F 2>&1 | Out-Null } catch { } }
    } catch { }   # housekeeping is a courtesy: its failure never changes the slot
}

# ---- One slot ---------------------------------------------------------------------------------------------------------------------

# Returns the exit code. Steps, Act and AfterRelease can be injected (tests).
function Invoke-SlotRun {
    param([hashtable]$Cfg, [scriptblock[]]$Steps = $null, [scriptblock]$Act = $null, [scriptblock]$AfterRelease = $null)
    $script:Cfg = $Cfg
    if (-not $Steps) { $Steps = Get-SlotSteps }
    if (-not $Act) { $Act = { param($c) Invoke-SlotAct $c } }
    if (-not $AfterRelease) { $AfterRelease = { Invoke-PostReleaseHousekeeping } }
    $ctx = @{ Tag = ''; GSha = ''; Lock = $null }
    try {
        try {
            $d = Get-SlotDecision -Steps $Steps -Ctx $ctx
            if ($d.Go -and $Cfg.DryRun) {
                $res = New-SlotResult 'go' "would promote up to $(Get-ShortSha $ctx.GSha) and release an rc (last rc $($ctx.Tag))" '' $ctx.Tag $ctx.GSha
            } elseif ($d.Go) {
                $res = @(& $Act $ctx) | Select-Object -Last 1
            } else {
                $res = New-SlotResult $d.Outcome $d.Reason '' $ctx.Tag $ctx.GSha
            }
        } catch {
            $res = New-SlotResult 'failed' ('error: ' + $_.Exception.Message) '' $ctx.Tag $ctx.GSha
        }
        if ($Cfg.DryRun) { Write-Host "rc slot dry run: $($res.Outcome): $(Format-OneLine $res.Reason 300)"; return $(if ($res.Outcome -eq 'failed') { 1 } else { 0 }) }
        try { Add-SlotLog -Dir $Cfg.StateDir -Outcome $res.Outcome -Reason $res.Reason; Write-SlotStatus -Dir $Cfg.StateDir -Result $res } catch { }
        if ($res.Outcome -eq 'released') { try { & $AfterRelease } catch { } }
        if ($res.Outcome -in 'released', 'failed') { Write-Host "rc slot: $($res.Outcome): $(Format-OneLine $res.Reason 300)" }
        return $(if ($res.Outcome -eq 'failed') { 1 } else { 0 })
    } finally { if ($ctx.Lock) { $ctx.Lock.Dispose() } }
}

# ---- Install / Uninstall ------------------------------------------------------------------------------------------------------------

# Same refusals as Housekeeping -Install (the Claude desktop app sandbox redirects %LOCALAPPDATA% writes). Housekeeping.ps1 is dot-sourced
# INSIDE this function: it has parameters of its own (Install, DryRun, Repo ...) that must not overwrite ours, and its main block does not
# run when dot-sourced. Its wording is adjusted to this script.
function Get-RcInstallRefusal {
    param([string]$Self, [string]$LocalAppData, [object]$PackageName = $null, [scriptblock]$Probe = $null)
    $hk = Join-Path $PSScriptRoot '..\maint\Housekeeping.ps1'
    if (-not (Test-Path -LiteralPath $hk -PathType Leaf)) { return 'tools/maint/Housekeeping.ps1 was not found next to this script, so the install checks cannot run. Run -Install from the repository checkout.' }
    . $hk
    $m = Get-InstallRefusal -Self $Self -LocalAppData $LocalAppData -PackageName $PackageName -Probe $Probe
    if (-not $m) { return $null }
    return ($m -replace 'Housekeeping\.ps1', 'Invoke-RcSlot.ps1' -replace '%LOCALAPPDATA%\\Unravis\\maint', '%LOCALAPPDATA%\Unravis\rcslot' -replace 'hourly task', '2-hourly task')
}

function Resolve-RepoFolder {
    param([string]$Explicit, [string]$Dir)
    if ($Explicit) { return [IO.Path]::GetFullPath($Explicit) }
    $cfg = Join-Path $Dir 'rcslot.json'
    if (Test-Path -LiteralPath $cfg) {
        try { $c = Get-Content -LiteralPath $cfg -Raw | ConvertFrom-Json; if ($c.repo -and (Test-Path -LiteralPath $c.repo -PathType Container)) { return [string]$c.repo } } catch { }
    }
    $top = & git -C $PSScriptRoot rev-parse --show-toplevel 2>$null
    if ($LASTEXITCODE -eq 0 -and $top) { return [IO.Path]::GetFullPath([string]($top | Select-Object -First 1)) }
    return $PSScriptRoot
}

function Register-RcSlotTask {
    param([string]$RepoFolder, [string]$StateDir)
    & git -C $RepoFolder rev-parse --git-dir 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "$RepoFolder is not a git checkout" }
    New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
    $dest = Join-Path $StateDir 'Invoke-RcSlot.ps1'
    if (-not (Test-SamePath $PSCommandPath $dest)) { Copy-Item -LiteralPath $PSCommandPath -Destination $dest -Force }
    @{ repo = $RepoFolder } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $StateDir 'rcslot.json') -Encoding utf8
    $pwsh = (Get-Command pwsh -ErrorAction Stop).Source
    $arg = '-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "{0}"' -f $dest
    $action = New-ScheduledTaskAction -Execute $pwsh -Argument $arg
    $trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(5) -RepetitionInterval (New-TimeSpan -Hours 2) -RepetitionDuration (New-TimeSpan -Days 3650)
    # StartWhenAvailable: after sleep one catch-up slot runs, never two. Interactive logon: the user's gh and Proton Pass sessions are needed.
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 3) -MultipleInstances IgnoreNew
    $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $script:RcTask -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
    Write-Host "registered '$($script:RcTask)' (every 2 hours, user level), script copied to $StateDir, repository $RepoFolder"
}

# ---- Main. Dot-sourcing (the tests) only defines the functions.
if ($MyInvocation.InvocationName -ne '.') {
    if (-not $IsWindows) { Write-Host 'Windows only.'; exit 0 }
    try {
        $state = if ($StateDir) { $StateDir } else { Join-Path $env:LOCALAPPDATA 'Unravis\rcslot' }
        if ($Uninstall) { Unregister-ScheduledTask -TaskName $script:RcTask -Confirm:$false -ErrorAction SilentlyContinue; Write-Host 'task removed'; exit 0 }
        $folder = Resolve-RepoFolder -Explicit $RepoFolder -Dir $state
        if ($Install) {
            $refusal = Get-RcInstallRefusal -Self $PSCommandPath -LocalAppData $env:LOCALAPPDATA
            if ($refusal) { Write-Host $refusal; exit 1 }
            Register-RcSlotTask -RepoFolder $folder -StateDir $state
            exit 0
        }
        $env:GIT_TERMINAL_PROMPT = '0'; $env:GH_PROMPT_DISABLED = '1'
        $code = Invoke-SlotRun -Cfg @{ Repo = $Repo; RepoFolder = $folder; WorkTree = $WorkTree; NodeDir = $NodeDir; StateDir = $state; Requester = $Requester; DryRun = [bool]$DryRun }
        exit ([int]($code | Select-Object -Last 1))
    } catch {
        Write-Host "error: $(Format-OneLine $_.Exception.Message 300)"
        exit 1
    }
}
