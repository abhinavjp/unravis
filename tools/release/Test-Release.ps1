<#
.SYNOPSIS
  Self-test for the release checksum scripts (New-Sha256Sums.ps1 and step 1 of Invoke-Smoke.ps1).

.DESCRIPTION
  Builds throw-away asset folders under the temp directory and shows that the checksum check
  passes a good folder and fails a changed file, a missing SHA256SUMS.txt, a listed file that was
  not downloaded, a file that is not listed, and a malformed line. Also checks the format that
  New-Sha256Sums.ps1 writes and the mistakes it refuses. No installer is ever present in the test
  folders, so Invoke-Smoke.ps1 stops after its checksum step and installs nothing.

  Run from anywhere:  ./tools/release/Test-Release.ps1
  Exit code 0 when every check holds, 1 otherwise.
#>
param()

$ErrorActionPreference = 'Stop'
$sumsScript = Join-Path $PSScriptRoot 'New-Sha256Sums.ps1'
$smokeScript = Join-Path $PSScriptRoot 'Invoke-Smoke.ps1'
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
}
finally {
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue }
}

if ($script:failures.Count -gt 0) {
    Write-Host ''
    Write-Host "Release checksum self-test FAILED: $($script:failures.Count) of $($script:checks) check(s)."
    $script:failures | ForEach-Object { Write-Host "  $_" }
    exit 1
}
Write-Host ''
Write-Host "Release checksum self-test passed ($($script:checks) checks)."
exit 0
