#Requires -Version 7.2
<#
.SYNOPSIS
  Two jobs, neither prints a secret.
  1) With -Reference: checks that a Proton Pass reference gives one clean line, and says only yes/no and the length.
       ./tools/release/Test-ProtonPass.ps1 -Reference 'pass://Release/Unravis rc-1 key/password'
  2) Without it: self-test of the reader (tools/release/ProtonPass.ps1) against a fake pass-cli: good answer, empty, two lines, exit code,
     a hang, a bad reference, no CLI. Exit code 0 only when every case behaves. Run by CI.
#>
param([string]$Reference)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')

if ($Reference) {
    try {
        $s = Get-ProtonPassSecret -Reference $Reference
        Write-Host "OK: Proton Pass gave one clean line of $($s.Length) characters. (Not shown.)" -ForegroundColor Green
        $s.Dispose(); exit 0
    } catch { Write-Host "NOT OK: $($_.Exception.Message)" -ForegroundColor Red; exit 1 }
}

$work = Join-Path ([IO.Path]::GetTempPath()) ('unravis-pp-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$fake = Join-Path $work 'fake-pass-cli.ps1'
@'
param([string]$a, [string]$b, [string]$c)
switch ($env:FAKE_MODE) {
    'ok'      { [Console]::Out.Write("correct horse battery`r`n"); exit 0 }
    'utf8'    { $o = [Console]::OpenStandardOutput(); $b = [Text.Encoding]::UTF8.GetBytes("Gr\u00fcn-\u00a3-key`n"); $o.Write($b, 0, $b.Length); $o.Flush(); exit 0 }
    'noeol'   { [Console]::Out.Write("correct horse battery"); exit 0 }
    'empty'   { exit 0 }
    'blank'   { [Console]::Out.Write("`r`n"); exit 0 }
    'two'     { [Console]::Out.Write("Password: x`nsecond line`n"); exit 0 }
    'fail'    { [Console]::Error.Write("not logged in: SECRET-LEAK-CHECK"); exit 1 }
    'hang'    { Start-Sleep -Seconds 60; exit 0 }
}
'@ | Set-Content -LiteralPath $fake -Encoding utf8
$pwsh = (Get-Process -Id $PID).Path
$prefix = @('-NoProfile', '-File', $fake)
$ref = 'pass://Release/Key one/password'
$failed = 0
function Case([string]$name, [string]$mode, [scriptblock]$check, [string]$r = $ref, [int]$timeout = 30) {
    $env:FAKE_MODE = $mode
    $res = $null; $err = $null
    try { $res = Get-ProtonPassSecret -Reference $r -CliPath $pwsh -CliArgsPrefix $prefix -TimeoutSec $timeout } catch { $err = $_.Exception.Message }
    $ok = & $check $res $err
    if ($ok) { Write-Host "PASS $name" -ForegroundColor Green } else { Write-Host "FAIL $name (error: $err)" -ForegroundColor Red; $script:failed++ }
}
function Plain($s) { [System.Net.NetworkCredential]::new('', $s).Password }
Case 'good answer becomes the passphrase, newline removed' 'ok' { param($r, $e) $r -and (Plain $r) -eq 'correct horse battery' -and -not $e }
Case 'UTF-8 passphrase survives' 'utf8' { param($r, $e) $r -and (Plain $r) -eq ('Gr' + [char]0xfc + 'n-' + [char]0xa3 + '-key') }
Case 'no trailing newline works' 'noeol' { param($r, $e) $r -and (Plain $r) -eq 'correct horse battery' }
Case 'empty output is refused' 'empty' { param($r, $e) -not $r -and $e -match 'nothing' }
Case 'a blank line is refused' 'blank' { param($r, $e) -not $r -and $e -match 'empty' }
Case 'two lines are refused' 'two' { param($r, $e) -not $r -and $e -match 'more than one line' -and $e -notmatch 'second line' }
Case 'non-zero exit is refused and its text is not echoed' 'fail' { param($r, $e) -not $r -and $e -match 'exit code 1' -and $e -notmatch 'SECRET-LEAK-CHECK' }
Case 'a hang is cut off' 'hang' { param($r, $e) -not $r -and $e -match 'did not answer' } $ref 3
Case 'the real item and field names (dots, hyphens, spaces) are accepted' 'ok' { param($r, $e) $r -and (Plain $r) -eq 'correct horse battery' } 'pass://Work/Unravis Passphrases/rc.1 passphrase'
Case 'stable-1 field name is accepted' 'ok' { param($r, $e) $r -and -not $e } 'pass://Work/Unravis Passphrases/stable-1 passphrase'
Case 'a reference with an option in it is refused' 'ok' { param($r, $e) -not $r -and $e -match 'must look like' } '--help'
Case 'a reference with a missing field is refused' 'ok' { param($r, $e) -not $r -and $e -match 'must look like' } 'pass://Vault/Item'
Case 'a reference that is a shell trick is refused' 'ok' { param($r, $e) -not $r -and $e -match 'must look like' } 'pass://V/I/password; calc'
$env:FAKE_MODE = 'ok'
$saved = $env:PATH; $env:PATH = $work
try { $threw = $false; try { Get-ProtonPassSecret -Reference $ref | Out-Null } catch { $threw = $_.Exception.Message -match 'not installed' } ; if ($threw) { Write-Host 'PASS missing pass-cli is refused' -ForegroundColor Green } else { Write-Host 'FAIL missing pass-cli' -ForegroundColor Red; $failed++ } } finally { $env:PATH = $saved }
Remove-Item Env:FAKE_MODE -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
if ($failed) { Write-Host "$failed case(s) failed." -ForegroundColor Red; exit 1 }
Write-Host 'Proton Pass reader self-test passed.' -ForegroundColor Green; exit 0
