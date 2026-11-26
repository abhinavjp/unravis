<#
.SYNOPSIS  Tests Clean-Scratch.ps1 against a throwaway folder tree. Touches nothing outside it. Exit code 1 on any failure.
#>
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $here 'Clean-Scratch.ps1')

$fail = 0
function Check { param([bool]$Cond, [string]$Name) if ($Cond) { Write-Host "ok    $Name" } else { Write-Host "FAIL  $Name"; $script:fail++ } }

$base = Join-Path ([IO.Path]::GetTempPath()) ('cs-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$root = Join-Path $base 'Temp\claude'
$proj = Join-Path $root 'proj1'
$now = [datetime]::UtcNow
$old = $now.AddDays(-3)

function New-Sess {
    param([string]$Name, [datetime]$When, [string]$Proj = $proj)
    $d = Join-Path $Proj $Name
    New-Item -ItemType Directory -Force -Path (Join-Path $d 'scratchpad') | Out-Null
    $f = Join-Path $d 'scratchpad\a.bin'
    [IO.File]::WriteAllBytes($f, (New-Object byte[] 2048))
    foreach ($p in @($f, (Join-Path $d 'scratchpad'), $d)) { (Get-Item -LiteralPath $p).LastWriteTimeUtc = $When }
    return $d
}

try {
    $oldDir = New-Sess 'old-session' $old
    $newDir = New-Sess 'new-session' $now.AddHours(-2)
    $inUse = New-Sess 'in-use-session-id' $old
    $withWt = New-Sess 'worktree-session' $old
    Set-Content -LiteralPath (Join-Path $withWt 'scratchpad\.git') -Value 'gitdir: x'
    (Get-Item -LiteralPath (Join-Path $withWt 'scratchpad\.git')).LastWriteTimeUtc = $old
    foreach ($p in @($withWt, (Join-Path $withWt 'scratchpad'))) { (Get-Item -LiteralPath $p).LastWriteTimeUtc = $old }
    $withLink = New-Sess 'link-session' $old
    $outside = Join-Path $base 'outside-data'
    New-Item -ItemType Directory -Force -Path $outside | Out-Null
    Set-Content -LiteralPath (Join-Path $outside 'precious.txt') -Value 'keep me'
    $linkMade = $false
    try { New-Item -ItemType Junction -Path (Join-Path $withLink 'scratchpad\lnk') -Target $outside | Out-Null; $linkMade = $true } catch { Write-Host 'note: could not make a junction, link case skipped' }
    foreach ($p in @($withLink, (Join-Path $withLink 'scratchpad'))) { (Get-Item -LiteralPath $p).LastWriteTimeUtc = $old }
    # A file at the project level and a stray file at the root must be left alone.
    Set-Content -LiteralPath (Join-Path $root 'stray.txt') -Value 'x'
    Set-Content -LiteralPath (Join-Path $proj 'loose.txt') -Value 'x'

    $running = @('claude.exe --session in-use-session-id --foo')

    # Root checks
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\other')).Ok) 'root not named claude is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'x\claude')).Ok) 'root whose parent is not Temp is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\claude\..\claude')).Ok) 'root with .. is refused'
    Check (-not (Test-SafeRoot 'C:\').Ok) 'drive root is refused'
    Check (-not (Test-SafeRoot (Join-Path $base 'Temp\claude\nope')).Ok) 'missing or wrong root is refused'
    Check (-not (Test-SafeRoot '').Ok) 'empty root is refused'
    Check (Test-SafeRoot $root).Ok 'test root is accepted'
    Check (Test-Inside -Root $root -Child $oldDir -Depth 2) 'depth-2 child is inside'
    Check (-not (Test-Inside -Root $root -Child $proj -Depth 2)) 'depth-1 child is not depth 2'
    Check (-not (Test-Inside -Root $root -Child (Join-Path $base 'Temp\claude-evil\a\b') -Depth 2)) 'sibling with the same prefix is not inside'

    # Dry run deletes nothing
    $r = Invoke-ScratchClean -RootPath $root -DoApply $false -AgeDays 1 -Clock $now -Running $running -WithWorktrees $false
    Check (Test-Path -LiteralPath $oldDir) 'dry run keeps the old folder'
    Check ($r.WouldFreeBytes -ge 2048) 'dry run reports bytes it would free'
    Check ($r.FreedBytes -eq 0) 'dry run frees nothing'

    # Apply
    $r = Invoke-ScratchClean -RootPath $root -DoApply $true -AgeDays 1 -Clock $now -Running $running -WithWorktrees $false
    Check (-not (Test-Path -LiteralPath $oldDir)) 'old unused folder is deleted'
    Check (Test-Path -LiteralPath $newDir) 'recent folder is kept'
    Check (Test-Path -LiteralPath $inUse) 'folder named by a running process is kept'
    Check (Test-Path -LiteralPath $withWt) 'folder with a git worktree is kept'
    if ($linkMade) {
        Check (Test-Path -LiteralPath $withLink) 'folder holding a link is kept'
        Check (Test-Path -LiteralPath (Join-Path $outside 'precious.txt')) 'link target data is untouched'
    }
    Check (Test-Path -LiteralPath (Join-Path $root 'stray.txt')) 'stray file at the root is untouched'
    Check (Test-Path -LiteralPath (Join-Path $proj 'loose.txt')) 'loose file at project level is untouched'
    Check ($r.FreedBytes -ge 2048) 'apply reports freed bytes'

    # Worktree folders go only when asked
    $r = Invoke-ScratchClean -RootPath $root -DoApply $true -AgeDays 1 -Clock $now -Running $running -WithWorktrees $true
    Check (-not (Test-Path -LiteralPath $withWt)) 'worktree folder is deleted with -IncludeWorktrees'
    Check (Test-Path -LiteralPath $inUse) 'in-use folder still kept with -IncludeWorktrees'

    # A running process naming the full path also protects it
    $sess2 = New-Sess 'path-named' $old
    $r = Invoke-ScratchClean -RootPath $root -DoApply $true -AgeDays 1 -Clock $now -Running @("node.exe $sess2\x.js") -WithWorktrees $false
    Check (Test-Path -LiteralPath $sess2) 'folder named by full path in a command line is kept'

    # Age floor: 0 days is treated as 1
    $r = Invoke-ScratchClean -RootPath $root -DoApply $true -AgeDays 0 -Clock $now -Running @() -WithWorktrees $false
    Check (Test-Path -LiteralPath $newDir) 'age 0 still keeps a 2-hour-old folder'

    # End to end through the script file, dry run default
    $sess3 = New-Sess 'e2e-old' $old
    $out = & pwsh -NoProfile -File (Join-Path $here 'Clean-Scratch.ps1') -Root $root -ProcessLines @('x') -GoCacheLimitGB 0 2>&1 | Out-String
    Check ($out -match 'DRY RUN') 'script defaults to dry run'
    Check ($out -match 'would delete') 'dry run lists what it would delete'
    Check (Test-Path -LiteralPath $sess3) 'script dry run deleted nothing'
    $bad = & pwsh -NoProfile -File (Join-Path $here 'Clean-Scratch.ps1') -Root (Join-Path $base 'Temp') -GoCacheLimitGB 0 2>&1 | Out-String
    Check ($bad -match 'Refusing root') 'script refuses a root that is not Temp\claude'
}
finally {
    # Remove the junction first so the target is never followed.
    Get-ChildItem -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint } |
        ForEach-Object { [IO.Directory]::Delete($_.FullName, $false) }
    if (Test-Path -LiteralPath $base) { Remove-Item -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue }
}

if ($fail -gt 0) { Write-Host "$fail check(s) failed"; exit 1 }
Write-Host 'all checks passed'
