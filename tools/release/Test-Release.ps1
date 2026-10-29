<#
.SYNOPSIS
  Self-test for the release scripts: the checksum scripts (New-Sha256Sums.ps1 and step 1 of
  Invoke-Smoke.ps1) and the release-notes generator (New-ReleaseNotes.ps1).

.DESCRIPTION
  Builds throw-away asset folders under the temp directory and shows that the checksum check
  passes a good folder and fails a changed file, a missing SHA256SUMS.txt, a listed file that was
  not downloaded, a file that is not listed, and a malformed line. Also checks the format that
  New-Sha256Sums.ps1 writes and the mistakes it refuses. No installer is ever present in the test
  folders, so Invoke-Smoke.ps1 stops after its checksum step and installs nothing.

  The notes checks build a throw-away git repository under the temp directory (never this one)
  and run New-ReleaseNotes.ps1 for a first tag, a normal tag, a prerelease tag and a stable tag
  after prereleases, in Windows PowerShell 5.1 (powershell.exe -File), in PowerShell 7 when pwsh
  is installed, and in the host running this test.

  Run from anywhere:  ./tools/release/Test-Release.ps1
  Exit code 0 when every check holds, 1 otherwise.
#>
param()

$ErrorActionPreference = 'Stop'
$sumsScript = Join-Path $PSScriptRoot 'New-Sha256Sums.ps1'
$smokeScript = Join-Path $PSScriptRoot 'Invoke-Smoke.ps1'
$notesScript = Join-Path $PSScriptRoot 'New-ReleaseNotes.ps1'
$templateFile = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..\.github\release-notes-template.md')).Path
$hostExe = (Get-Process -Id $PID).Path
$tag = 'v0.0.0-selftest'
$logName = "smoke-$tag.log"
$names = @('unravis-v0.0.0-windows-x64.zip', 'unravis-blackbox-v0.0.0-windows-x64.zip', 'unravis-notes-v0.0.0.bin')
$root = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-release-selftest-' + [guid]::NewGuid().ToString('N'))
$script:checks = 0
$script:failures = [System.Collections.Generic.List[string]]::new()

function Assert-That {
    param([string]$Name, [bool]$Condition, [string]$Detail = '')
    $script:checks++
    if ($Condition) { Write-Host "ok   $Name" }
    else { Write-Host "FAIL $Name $Detail"; $script:failures.Add($Name) }
}

# The message a block throws, or $null when it does not throw.
function Get-ThrownMessage {
    param([scriptblock]$Block)
    try { & $Block | Out-Null; return $null } catch { return $_.Exception.Message }
}

# A folder with three small files and a SHA256SUMS.txt made by the real script.
function New-AssetFolder {
    param([string]$Label)
    $dir = Join-Path $root $Label
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $i = 0
    foreach ($n in $names) {
        $i++
        $bytes = [byte[]](1..(20 + $i) | ForEach-Object { ($_ * $i) % 251 })
        [System.IO.File]::WriteAllBytes((Join-Path $dir $n), $bytes)
    }
    & $sumsScript -AssetDir $dir -Names $names -OutFile (Join-Path $dir 'SHA256SUMS.txt') | Out-Null
    return $dir
}

# Runs Invoke-Smoke.ps1 in its own process (it calls exit and throws) and returns the exit code and its log text.
function Invoke-SmokeOn {
    param([string]$Dir)
    $installers = @(Get-ChildItem -LiteralPath $Dir -Filter 'unravis-setup-*-windows-x64.exe' -File -ErrorAction SilentlyContinue)
    if ($installers.Count -ne 0) { throw "Test folder $Dir has an installer; the self-test must never install anything." }
    # The child's output goes beside the asset folder, never into it: Invoke-Smoke.ps1 rejects files it does not know.
    $leaf = Split-Path -Leaf $Dir
    $stdout = Join-Path $root "$leaf.child.out.txt"
    $stderr = Join-Path $root "$leaf.child.err.txt"
    $argumentList = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $smokeScript + '"'),
        '-AssetDir', ('"' + $Dir + '"'), '-Tag', $tag)
    $proc = Start-Process -FilePath $hostExe -ArgumentList $argumentList -Wait -PassThru -NoNewWindow -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    $log = Join-Path $Dir $logName
    $text = ''
    if (Test-Path -LiteralPath $log) { $text = Get-Content -LiteralPath $log -Raw }
    return [pscustomobject]@{ Code = $proc.ExitCode; Log = $text }
}

# Runs git in a throw-away repository. Windows PowerShell 5.1 treats git's stderr chatter (hints, "fatal:") as an
# error under 'Stop', so the preference is relaxed here and the exit code is what counts.
function Invoke-TestGit {
    param([string]$Repo, [string[]]$GitArgs)
    $ErrorActionPreference = 'Continue'
    $config = @('-c', 'user.name=Release Test', '-c', 'user.email=release-test@example.invalid', '-c', 'commit.gpgsign=false',
        '-c', 'tag.gpgsign=false', '-c', 'core.autocrlf=false', '-c', ('core.hooksPath=' + (Join-Path $root 'no-hooks')))
    $out = & git -C $Repo @config @GitArgs 2>&1
    if ($LASTEXITCODE -ne 0) { throw "git $($GitArgs -join ' ') failed in ${Repo}: $out" }
}

$script:commitNo = 0
function Add-TestCommit {
    param([string]$Repo, [string]$Subject)
    $script:commitNo++
    [System.IO.File]::WriteAllText((Join-Path $Repo 'file.txt'), "change $($script:commitNo)`n")
    Invoke-TestGit $Repo @('add', 'file.txt')
    Invoke-TestGit $Repo @('commit', '-m', $Subject)
}

function New-TestRepo {
    param([string]$Label)
    $repo = Join-Path $root $Label
    New-Item -ItemType Directory -Force -Path $repo | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $root 'no-hooks') | Out-Null
    Invoke-TestGit $repo @('init')
    return $repo
}

# Runs New-ReleaseNotes.ps1 in its own process under the given host and returns the exit code, its output and the notes text.
function Invoke-NotesOn {
    param([string]$HostPath, [string]$Repo, [string]$Tag, [switch]$Signed)
    $leaf = (Split-Path -Leaf $Repo) + '-' + $Tag + '-' + [guid]::NewGuid().ToString('N').Substring(0, 6)
    $notes = Join-Path $root "$leaf.notes.md"
    $stdout = Join-Path $root "$leaf.child.out.txt"
    $stderr = Join-Path $root "$leaf.child.err.txt"
    $argumentList = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $notesScript + '"'),
        '-Tag', $Tag, '-Version', $Tag.Substring(1), '-TemplatePath', ('"' + $templateFile + '"'),
        '-OutFile', ('"' + $notes + '"'), '-RepoRoot', ('"' + $Repo + '"'))
    if ($Signed) { $argumentList += '-Signed' }
    $proc = Start-Process -FilePath $HostPath -ArgumentList $argumentList -Wait -PassThru -NoNewWindow -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    $text = ''
    if (Test-Path -LiteralPath $notes) { $text = [System.IO.File]::ReadAllText($notes) }
    $log = ''
    foreach ($f in @($stdout, $stderr)) { if (Test-Path -LiteralPath $f) { $log += [System.IO.File]::ReadAllText($f) } }
    return [pscustomobject]@{ Code = $proc.ExitCode; Notes = $text; Log = $log; Path = $notes }
}

# Windows PowerShell 5.1 and PowerShell 7 (when installed), then the host running this test if it is neither.
function Get-NotesHosts {
    $found = [ordered]@{}
    $ps51 = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if (Test-Path -LiteralPath $ps51) { $found['powershell 5.1'] = $ps51 }
    $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($pwsh) { $found['pwsh 7'] = $pwsh.Source }
    if ($found.Values -notcontains $hostExe) { $found['this host'] = $hostExe }
    return $found
}

$savedCleanVm = $env:CLEAN_VM_VERIFIED

try {
    New-Item -ItemType Directory -Force -Path $root | Out-Null

    # 1. A good folder passes and installs nothing.
    $dir = New-AssetFolder 'good'
    $r = Invoke-SmokeOn $dir
    Assert-That 'good folder: smoke exits 0' ($r.Code -eq 0) "(exit $($r.Code))"
    Assert-That 'good folder: all assets match' ($r.Log -match 'All 3 asset\(s\) match SHA256SUMS\.txt') $r.Log
    Assert-That 'good folder: no install was attempted' ($r.Log -match 'No installer asset in this release') $r.Log

    # 2. A changed asset fails with a mismatch for that file.
    $dir = New-AssetFolder 'tampered'
    $stream = [System.IO.File]::Open((Join-Path $dir $names[0]), [System.IO.FileMode]::Append)
    $stream.WriteByte(7)
    $stream.Close()
    $r = Invoke-SmokeOn $dir
    Assert-That 'changed asset: smoke fails' ($r.Code -ne 0) "(exit $($r.Code))"
    Assert-That 'changed asset: names the file' ($r.Log -match ('Checksum mismatch for ' + [regex]::Escape($names[0]))) $r.Log

    # 3. A missing SHA256SUMS.txt fails.
    $dir = New-AssetFolder 'no-sums'
    Remove-Item -LiteralPath (Join-Path $dir 'SHA256SUMS.txt')
    $r = Invoke-SmokeOn $dir
    Assert-That 'missing checksum file: smoke fails' ($r.Code -ne 0) "(exit $($r.Code))"
    Assert-That 'missing checksum file: says it is missing' ($r.Log -match 'FAILED: Missing .*SHA256SUMS\.txt') $r.Log

    # 4. A file that is listed but was not downloaded fails.
    $dir = New-AssetFolder 'not-downloaded'
    Remove-Item -LiteralPath (Join-Path $dir $names[1])
    $r = Invoke-SmokeOn $dir
    Assert-That 'listed but not downloaded: smoke fails' ($r.Code -ne 0) "(exit $($r.Code))"
    Assert-That 'listed but not downloaded: names the file' ($r.Log -match ([regex]::Escape($names[1]) + '.* was not downloaded')) $r.Log

    # 5. A file that is not listed fails.
    $dir = New-AssetFolder 'unlisted'
    [System.IO.File]::WriteAllBytes((Join-Path $dir 'extra.zip'), [byte[]](1, 2, 3))
    $r = Invoke-SmokeOn $dir
    Assert-That 'unlisted file: smoke fails' ($r.Code -ne 0) "(exit $($r.Code))"
    Assert-That 'unlisted file: names the file' ($r.Log -match 'extra\.zip was downloaded but is not in SHA256SUMS\.txt') $r.Log

    # 6. A malformed line fails.
    $dir = New-AssetFolder 'malformed'
    [System.IO.File]::WriteAllText((Join-Path $dir 'SHA256SUMS.txt'), "this is not a checksum line`n", (New-Object System.Text.UTF8Encoding($false)))
    $r = Invoke-SmokeOn $dir
    Assert-That 'malformed line: smoke fails' ($r.Code -ne 0) "(exit $($r.Code))"
    Assert-That 'malformed line: says so' ($r.Log -match 'Malformed SHA256SUMS\.txt line') $r.Log

    # 7. The file New-Sha256Sums.ps1 writes: one lower-case hash, two spaces and a bare name per line, LF endings, no byte order mark.
    $dir = New-AssetFolder 'format'
    $bytes = [System.IO.File]::ReadAllBytes((Join-Path $dir 'SHA256SUMS.txt'))
    $text = [System.Text.Encoding]::UTF8.GetString($bytes)
    Assert-That 'format: no byte order mark' (-not ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF))
    Assert-That 'format: LF endings only, ends with a newline' (($text -notmatch "`r") -and $text.EndsWith("`n"))
    $lines = @($text.TrimEnd("`n") -split "`n")
    Assert-That 'format: one line per name' ($lines.Count -eq $names.Count) "($($lines.Count) lines)"
    for ($i = 0; $i -lt $names.Count; $i++) {
        $expected = (Get-FileHash -LiteralPath (Join-Path $dir $names[$i]) -Algorithm SHA256).Hash.ToLowerInvariant() + '  ' + $names[$i]
        Assert-That "format: line $($i + 1) is hash, two spaces, name, in the given order" ($lines[$i] -ceq $expected) $lines[$i]
    }

    # 8. New-Sha256Sums.ps1 refuses what it should.
    $out = Join-Path $dir 'refused.txt'
    $m = Get-ThrownMessage { & $sumsScript -AssetDir $dir -Names @('does-not-exist.zip') -OutFile $out }
    Assert-That 'refuses a missing file' ($m -match 'missing file') "($m)"
    $m = Get-ThrownMessage { & $sumsScript -AssetDir $dir -Names @('sub\' + $names[0]) -OutFile $out }
    Assert-That 'refuses a path instead of a bare name' ($m -match 'bare file name') "($m)"
    $m = Get-ThrownMessage { & $sumsScript -AssetDir $dir -Names @() -OutFile $out }
    Assert-That 'refuses an empty name list' ($null -ne $m) '(it did not throw)'
    Assert-That 'writes nothing when it refuses' (-not (Test-Path -LiteralPath $out))

    # 9. Release notes. A throw-away repository with: a first tag on a root commit, a normal tag, a prerelease tag,
    #    a second prerelease and a stable tag after the prereleases.
    Remove-Item Env:CLEAN_VM_VERIFIED -ErrorAction SilentlyContinue
    $hosts = Get-NotesHosts
    $ps51Path = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    Assert-That 'notes: Windows PowerShell 5.1 (powershell.exe -File) is among the hosts tested' ($hosts.Contains('powershell 5.1') -or -not (Test-Path -LiteralPath $ps51Path)) 'powershell.exe was not used'

    $repo = New-TestRepo 'notes-repo'
    Add-TestCommit $repo 'Add the first thing'
    Invoke-TestGit $repo @('tag', 'v1.0.0')   # first tag, on the root commit: no tag before it, no commit before it
    Add-TestCommit $repo 'Fix the second thing'
    Add-TestCommit $repo 'CI: pin the runner image'
    Invoke-TestGit $repo @('tag', 'v1.1.0')
    Add-TestCommit $repo 'Add the third thing'
    Invoke-TestGit $repo @('tag', 'v1.2.0-rc.1')
    Add-TestCommit $repo 'CI: retry the flaky test'
    Add-TestCommit $repo 'Fix the fourth thing'
    Invoke-TestGit $repo @('tag', 'v1.2.0-rc.2')
    Add-TestCommit $repo 'Polish the fifth thing'
    Invoke-TestGit $repo @('tag', 'v1.2.0')

    # A first tag that has earlier commits but no earlier tag, so `git describe` has nothing to find.
    $firstRepo = New-TestRepo 'notes-first'
    Add-TestCommit $firstRepo 'Start the project'
    Add-TestCommit $firstRepo 'CI: add the build job'
    Add-TestCommit $firstRepo 'Add the thing'
    Invoke-TestGit $firstRepo @('tag', 'v0.1.0')

    foreach ($h in $hosts.Keys) {
        $hp = $hosts[$h]

        $r = Invoke-NotesOn $hp $firstRepo 'v0.1.0'
        Assert-That "notes [$h] first tag (no earlier tag): exits 0" ($r.Code -eq 0) "(exit $($r.Code)) $($r.Log)"
        Assert-That "notes [$h] first tag (no earlier tag): says first release" ($r.Notes -match 'Upgrading from the first release') $r.Notes
        Assert-That "notes [$h] first tag (no earlier tag): lists the whole log" (($r.Notes -match '- Start the project \([0-9a-f]+\)') -and ($r.Notes -match '- Add the thing \([0-9a-f]+\)'))

        $r = Invoke-NotesOn $hp $repo 'v1.0.0'
        Assert-That "notes [$h] first tag on the root commit: exits 0" ($r.Code -eq 0) "(exit $($r.Code)) $($r.Log)"
        Assert-That "notes [$h] first tag on the root commit: lists its commit" ($r.Notes -match '- Add the first thing \(') $r.Notes

        $r = Invoke-NotesOn $hp $repo 'v1.1.0'
        Assert-That "notes [$h] normal tag: exits 0" ($r.Code -eq 0) "(exit $($r.Code)) $($r.Log)"
        Assert-That "notes [$h] normal tag: compares with v1.0.0" ($r.Notes -match 'Upgrading from v1\.0\.0 ') $r.Notes
        Assert-That "notes [$h] normal tag: only commits since v1.0.0" (($r.Notes -match '- Fix the second thing \(') -and ($r.Notes -notmatch 'Add the first thing'))
        Assert-That "notes [$h] normal tag: every placeholder is filled" ($r.Notes -notmatch '\{\{') $r.Notes
        Assert-That "notes [$h] normal tag: title and date" (($r.Notes -match '(?m)^# Unravis 1\.1\.0\r?$') -and ($r.Notes -match 'Released \d{4}-\d{2}-\d{2}\.'))
        $bytes = [System.IO.File]::ReadAllBytes($r.Path)
        Assert-That "notes [$h] normal tag: no byte order mark" (-not ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF))

        # Grouping: CI: commits sit in a collapsed block after the other bullets, inside the "What changed" section.
        $whatAt = $r.Notes.IndexOf('## What changed')
        $fixAt = $r.Notes.IndexOf('Fix the second thing')
        $detailsAt = $r.Notes.IndexOf('<details>')
        $ciAt = $r.Notes.IndexOf('CI: pin the runner image')
        $upgradeAt = $r.Notes.IndexOf('## Upgrade notes')
        Assert-That "notes [$h] grouping: CI commit is under 'Build and test changes'" (($r.Notes -match '<summary>Build and test changes \(1\)</summary>') -and $detailsAt -ge 0 -and $ciAt -gt $detailsAt)
        Assert-That "notes [$h] grouping: other commits first, CI block last, before Upgrade notes" (($whatAt -ge 0) -and ($fixAt -gt $whatAt) -and ($detailsAt -gt $fixAt) -and ($upgradeAt -gt $ciAt))

        $r = Invoke-NotesOn $hp $repo 'v1.2.0-rc.1'
        Assert-That "notes [$h] prerelease tag: exits 0" ($r.Code -eq 0) "(exit $($r.Code)) $($r.Log)"
        Assert-That "notes [$h] prerelease tag: compares with v1.1.0" (($r.Notes -match 'Upgrading from v1\.1\.0 ') -and ($r.Notes -match '- Add the third thing \(')) $r.Notes
        Assert-That "notes [$h] prerelease tag: no CI block when there is no CI commit" ($r.Notes -notmatch '<details>')

        $r = Invoke-NotesOn $hp $repo 'v1.2.0-rc.2'
        Assert-That "notes [$h] second prerelease: compares with the nearest tag (rc.1)" (($r.Code -eq 0) -and ($r.Notes -match 'Upgrading from v1\.2\.0-rc\.1 ') -and ($r.Notes -notmatch 'Add the third thing')) "$($r.Log) $($r.Notes)"

        $r = Invoke-NotesOn $hp $repo 'v1.2.0'
        Assert-That "notes [$h] stable tag after prereleases: compares with the previous stable tag" (($r.Code -eq 0) -and ($r.Notes -match 'Upgrading from v1\.1\.0 ') -and ($r.Notes -match '- Add the third thing \(') -and ($r.Notes -match '- Polish the fifth thing \(')) "$($r.Log) $($r.Notes)"
        Assert-That "notes [$h] stable tag after prereleases: the CI commit is collapsed" (($r.Notes -match '<summary>Build and test changes \(1\)</summary>') -and ($r.Notes -match 'CI: retry the flaky test') -and ($r.Notes -notmatch 'CI: pin the runner image'))
    }

    # 10. Signing notice and the clean-PC line (host-independent, so run once in this host).
    $r = Invoke-NotesOn $hostExe $repo 'v1.1.0'
    Assert-That 'notes: unsigned notice is kept' ($r.Notes -match 'This release is not signed') $r.Notes
    Assert-That 'notes: "Not yet verified on a clean PC." is added by default' ($r.Notes -match 'Not yet verified on a clean PC\.') $r.Notes
    $r = Invoke-NotesOn $hostExe $repo 'v1.1.0' -Signed
    Assert-That 'notes: signed notice names the installer for the tag' ($r.Notes -match 'Get-AuthenticodeSignature \.\\unravis-setup-v1\.1\.0-windows-x64\.exe') $r.Notes
    Assert-That 'notes: signed notice still says not yet verified on a clean PC' ($r.Notes -match 'Not yet verified on a clean PC\.')
    $env:CLEAN_VM_VERIFIED = 'true'
    $r = Invoke-NotesOn $hostExe $repo 'v1.1.0'
    Assert-That 'notes: CLEAN_VM_VERIFIED=true removes the clean-PC line' (($r.Code -eq 0) -and ($r.Notes -notmatch 'Not yet verified on a clean PC') -and ($r.Notes -match 'This release is not signed')) "$($r.Log) $($r.Notes)"
    $env:CLEAN_VM_VERIFIED = 'false'
    $r = Invoke-NotesOn $hostExe $repo 'v1.1.0'
    Assert-That 'notes: CLEAN_VM_VERIFIED=false keeps the clean-PC line' ($r.Notes -match 'Not yet verified on a clean PC\.') $r.Notes
    Remove-Item Env:CLEAN_VM_VERIFIED -ErrorAction SilentlyContinue
}
finally {
    if ($null -eq $savedCleanVm) { Remove-Item Env:CLEAN_VM_VERIFIED -ErrorAction SilentlyContinue } else { $env:CLEAN_VM_VERIFIED = $savedCleanVm }
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue }
}

if ($script:failures.Count -gt 0) {
    Write-Host ''
    Write-Host "Release self-test FAILED: $($script:failures.Count) of $($script:checks) check(s)."
    $script:failures | ForEach-Object { Write-Host "  $_" }
    exit 1
}
Write-Host ''
Write-Host "Release self-test passed ($($script:checks) checks)."
exit 0
