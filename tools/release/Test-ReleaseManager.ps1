#Requires -Version 7.2
<#
.SYNOPSIS
  Self-test for the release manager's checks (ReleaseLib.ps1): key files and key choice, trusted-key changes, versions, the
  signing tool's dependency rule, the audit log chain, lock, state, and the no-prompt passphrase rules. Also parses every script
  in tools/release so a syntax error fails CI, not a release.
  Uses throw-away folders under the temp directory and fake key files (no real key, no passphrase, no network).
  Run from anywhere:  ./tools/release/Test-ReleaseManager.ps1     Exit code 0 when every check holds.
#>
param()
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
$script:checks = 0
$script:failures = [System.Collections.Generic.List[string]]::new()
function Assert-That([string]$Name, [bool]$Condition, [string]$Detail = '') {
    $script:checks++
    if ($Condition) { Write-Host "ok   $Name" } else { Write-Host "FAIL $Name $Detail"; $script:failures.Add($Name) }
}
function Get-ThrownMessage([scriptblock]$Block) { try { & $Block | Out-Null; return $null } catch { return $_.Exception.Message } }
$root = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-rm-selftest-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root | Out-Null
$env:UNRAVIS_RELEASE_DATA = Join-Path $root 'data'

try {
    # ---- every script parses ------------------------------------------------------------------------------------------------
    foreach ($f in Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' -File) {
        $tokens = $null; $errs = $null
        [System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$tokens, [ref]$errs) | Out-Null
        Assert-That "parses: $($f.Name)" ($errs.Count -eq 0) (($errs | ForEach-Object { "line $($_.Extent.StartLineNumber): $($_.Message)" }) -join '; ')
    }

    # ---- fake key files -----------------------------------------------------------------------------------------------------
    function New-FakeKey([string]$Dir, [string]$Name, [string]$Role, [byte]$Fill, [string]$PubLine = $null) {
        $pub = [byte[]]::new(32); for ($i = 0; $i -lt 32; $i++) { $pub[$i] = ($Fill + $i) % 256 }
        $hex = ([System.BitConverter]::ToString($pub) -replace '-', '').ToLowerInvariant()
        $j = [ordered]@{ v = 1; role = $Role; pub = [Convert]::ToBase64String($pub); salt = 'AAAAAAAAAAAAAAAAAAAAAA=='; rounds = 600000; nonce = 'AAAAAAAAAAAAAAAA'; sealed = 'AAAA' }
        Set-Content -LiteralPath (Join-Path $Dir "$Name.key.json") -Value ($j | ConvertTo-Json)
        if ($null -eq $PubLine) { $PubLine = "$Role $hex" }
        if ($PubLine -ne '') { Set-Content -LiteralPath (Join-Path $Dir "$Name.pub") -Value $PubLine }
        return $hex
    }
    $kd = Join-Path $root 'keys'; New-Item -ItemType Directory -Path $kd | Out-Null
    $rc1 = New-FakeKey $kd 'rc-1' 'rc' 1
    $rc2 = New-FakeKey $kd 'rc-2' 'rc' 50
    $st1 = New-FakeKey $kd 'stable-1' 'stable' 100
    $bad = New-FakeKey $kd 'rc-3' 'rc' 150 "rc $('ab' * 32)"     # .pub does not match the key file
    Set-Content -LiteralPath (Join-Path $kd 'junk.key.json') -Value 'not json {'
    $owner = Get-OwnerKeys $kd
    Assert-That 'owner keys: all five files listed' ($owner.Count -eq 5)
    Assert-That 'owner keys: rc-1 read without the passphrase' ((($owner | Where-Object Name -eq 'rc-1').Pub -eq $rc1) -and (($owner | Where-Object Name -eq 'rc-1').Role -eq 'rc'))
    Assert-That 'owner keys: a .pub that does not match is a problem' ([bool]($owner | Where-Object Name -eq 'rc-3').Problem)
    Assert-That 'owner keys: a damaged file is a problem' ([bool]($owner | Where-Object Name -eq 'junk').Problem)
    Assert-That 'owner keys: a missing folder gives none' ((Get-OwnerKeys (Join-Path $root 'nope')).Count -eq 0)

    $listText = "# comment`nrc $rc1`nrc $rc2`nstable $st1`n"
    $list = ConvertFrom-TrustedKeys $listText
    Assert-That 'trusted keys: three parsed, comment skipped' ($list.Count -eq 3)

    # ---- trusted-key changes ------------------------------------------------------------------------------------------------
    $evil = 'ee' * 32
    $c = Compare-KeyLists "rc $rc1`nstable $st1" "rc $rc1`nrc $rc2`nrc $evil" $owner
    Assert-That 'key change: two added' ($c.Added.Count -eq 2)
    Assert-That 'key change: one removed' ($c.Removed.Count -eq 1 -and $c.Removed[0].Role -eq 'stable')
    Assert-That 'key change: the key with no owner file is foreign' ($c.Foreign.Count -eq 1 -and $c.Foreign[0].Pub -eq $evil)
    $c = Compare-KeyLists "rc $rc1" "stable $rc1" $owner
    Assert-That 'key change: an owner key listed under the wrong role is foreign' ($c.Foreign.Count -eq 1)
    $c = Compare-KeyLists "rc $rc1" "rc $rc1`nrc $('ab' * 32)" $owner
    Assert-That 'key change: a key whose owner file is broken is foreign' ($c.Foreign.Count -eq 1)

    # ---- choosing the key ---------------------------------------------------------------------------------------------------
    $s = Select-SigningKey 'rc' $owner $list $list ''
    Assert-That 'select: rc-1 first' ($s.Key.Name -eq 'rc-1' -and $s.Usable.Count -eq 2)
    Assert-That 'select: broken files are named' ((($s.Problems -join ' ') -match 'rc-3') -and (($s.Problems -join ' ') -match 'junk'))
    $s = Select-SigningKey 'rc' $owner $list (ConvertFrom-TrustedKeys "rc $rc2") ''
    Assert-That 'select: a key the feed release does not trust is skipped for the next' ($s.Key.Name -eq 'rc-2' -and (($s.Problems -join ' ') -match 'rc-1: is not trusted by the release now on the feed'))
    $s = Select-SigningKey 'rc' $owner (ConvertFrom-TrustedKeys "rc $rc2") $list ''
    Assert-That 'select: a key not in this commit''s list is skipped' ($s.Key.Name -eq 'rc-2' -and (($s.Problems -join ' ') -match 'rc-1: is not in'))
    $s = Select-SigningKey 'rc' $owner $list $null ''
    Assert-That 'select: first release (no feed) needs only this commit''s list' ($s.Key.Name -eq 'rc-1')
    $s = Select-SigningKey 'rc' $owner $list $list 'D:\secret\stable-1.key.json'
    Assert-That 'select: a stable key asked for an rc release is refused and said' (-not $s.Key -and (($s.Problems -join ' ') -match 'stable-1: is a stable key'))
    $s = Select-SigningKey 'rc' $owner $list $list 'rc-9'
    Assert-That 'select: a missing key asked for is said' (-not $s.Key -and (($s.Problems -join ' ') -match 'rc-9: no such key file'))
    $s = Select-SigningKey 'stable' @() $list $list ''
    Assert-That 'select: no key file at all is said' (-not $s.Key -and (($s.Problems -join ' ') -match 'no stable key file'))
    Assert-That 'pass ref: rc-1 maps to its Proton Pass entry' ((Get-ProtonPassRefForKey 'rc-1') -eq 'pass://Work/Unravis Passphrases/rc.1 passphrase')
    Assert-That 'pass ref: every mapped reference is well formed' (@($script:ReleasePassRefs.Values | Where-Object { -not (Test-ProtonPassReference $_) }).Count -eq 0)

    # ---- versions -----------------------------------------------------------------------------------------------------------
    Assert-That 'semver: rc.10 after rc.9' ((Compare-SemVer '0.1.0-rc.10' '0.1.0-rc.9') -eq 1)
    Assert-That 'semver: release after its rc' ((Compare-SemVer '0.1.0' '0.1.0-rc.99') -eq 1)
    Assert-That 'semver: 0.2.0-rc.1 after 0.1.9' ((Compare-SemVer 'v0.2.0-rc.1' '0.1.9') -eq 1)
    Assert-That 'semver: equal' ((Compare-SemVer '1.2.3' 'v1.2.3') -eq 0)
    $tags = @('v0.1.0-rc.9', 'v0.1.0-rc.54', 'v0.1.0-rc.53', 'update-feed', 'vbad')
    Assert-That 'next: rc.54 -> rc.55' ((Get-NextVersion $tags 'rc') -eq '0.1.0-rc.55')
    Assert-That 'next: stable after rc.54 is 0.1.0' ((Get-NextVersion $tags 'stable') -eq '0.1.0')
    Assert-That 'next: rc after a stable is the next patch rc.1' ((Get-NextVersion ($tags + 'v0.1.0') 'rc') -eq '0.1.1-rc.1')
    Assert-That 'next: stable after a stable is refused' ([bool](Get-ThrownMessage { Get-NextVersion @('v0.1.0') 'stable' }))
    Assert-That 'assert-next: rc.55 ok' (-not (Get-ThrownMessage { Assert-NextVersion '0.1.0-rc.55' $tags }))
    Assert-That 'assert-next: rc.54 again refused' ([bool](Get-ThrownMessage { Assert-NextVersion '0.1.0-rc.54' $tags }))
    Assert-That 'assert-next: an older one refused' ([bool](Get-ThrownMessage { Assert-NextVersion '0.1.0-rc.20' $tags }))
    Assert-That 'assert-next: beta.1 refused (no such channel)' ([bool](Get-ThrownMessage { Assert-NextVersion '0.2.0-beta.1' $tags }))

    # ---- signing tool dependencies ------------------------------------------------------------------------------------------
    $deps = @('fmt', 'crypto/ed25519', 'vendor/golang.org/x/crypto/chacha20', 'api-audit-explorer/internal/update', 'api-audit-explorer/tools/release/sigtool')
    Assert-That 'deps: today''s set is allowed' ((Get-ForbiddenSigtoolDeps $deps).Count -eq 0)
    $f = Get-ForbiddenSigtoolDeps ($deps + 'github.com/evil/exfil' + 'api-audit-explorer/internal/server')
    Assert-That 'deps: a third-party module and another internal package are refused' ($f.Count -eq 2)

    # ---- audit log ----------------------------------------------------------------------------------------------------------
    $log = Join-Path $root 'audit.jsonl'
    Write-AuditEntry $log @{ event = 'a'; n = 1 }
    Write-AuditEntry $log @{ event = 'b'; list = @('x', 'y') }
    Write-AuditEntry $log @{ event = 'c' }
    $r = Test-AuditChain $log
    Assert-That 'audit: three lines, chain ok' ($r.Ok -and $r.Lines -eq 3)
    $lines = Get-Content $log
    $lines[1] = $lines[1].Replace('"b"', '"B"'); Set-Content -LiteralPath $log -Value $lines
    $r = Test-AuditChain $log
    Assert-That 'audit: an edited line breaks the chain at the next line' (-not $r.Ok -and $r.BrokenAt -eq 3)
    $lines = Get-Content $log; Set-Content -LiteralPath $log -Value @($lines[0], $lines[2])
    Assert-That 'audit: a removed line breaks the chain' (-not (Test-AuditChain $log).Ok)
    Assert-That 'audit: a field named like a secret is refused' ([bool](Get-ThrownMessage { Write-AuditEntry (Join-Path $root 'x.jsonl') @{ passphrase = 'x' } }))
    Assert-That 'audit: missing log is ok and empty' ((Test-AuditChain (Join-Path $root 'none.jsonl')).Lines -eq 0)

    # ---- lock and state -----------------------------------------------------------------------------------------------------
    $d = Get-ReleaseDataDir
    $l1 = Enter-ReleaseLock $d
    try { Assert-That 'lock: a second run is refused' ([bool](Get-ThrownMessage { Enter-ReleaseLock $d })) } finally { $l1.Dispose() }
    $l2 = Enter-ReleaseLock $d; $l2.Dispose()
    Assert-That 'lock: free again after the first run ends' $true
    Set-ReleaseState $d '0.1.0-rc.55' 'Sign' 'stopped' 'network'
    $st = Get-ReleaseState $d
    Assert-That 'state: round trip' ($st.version -eq '0.1.0-rc.55' -and $st.stage -eq 'Sign' -and $st.status -eq 'stopped')
    $order = 'Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify'
    Assert-That 'resume: after Sign comes Publish' ((Get-ResumeStage $order 'Sign') -eq 'Publish')
    Assert-That 'resume: nothing done yet starts at Preflight' ((Get-ResumeStage $order '') -eq 'Preflight')
    Assert-That 'resume: after Verify there is nothing' ($null -eq (Get-ResumeStage $order 'Verify'))

    # ---- passphrase: never a prompt under -Yes; the pipe reaches the program ----------------------------------------------------
    $m = Get-ThrownMessage { Get-SigningPassphrase -ProtonPassRef '' -KeyFile 'k' -Yes }
    Assert-That 'passphrase: -Yes without a reference stops instead of asking' ($m -match 'never asks')
    $m = Get-ThrownMessage { Get-SigningPassphrase -ProtonPassRef 'not a ref' -KeyFile 'k' -Yes }
    Assert-That 'passphrase: -Yes with a broken reference stops instead of asking' ($m -match 'Not asking')
    $hostExe = (Get-Process -Id $PID).Path
    $sec = [System.Security.SecureString]::new(); foreach ($ch in 'pipe-check-value'.ToCharArray()) { $sec.AppendChar($ch) }
    $null = Invoke-WithPassphrase $sec $hostExe @('-NoProfile', '-NonInteractive', '-Command', 'if ([Console]::In.ReadLine() -eq ''pipe-check-value'') { exit 0 } else { exit 3 }')
    Assert-That 'passphrase: Invoke-WithPassphrase puts it on the program''s standard input' ($LASTEXITCODE -eq 0) "exit $LASTEXITCODE"
    $sec.Dispose()
    Assert-That 'logging risks: returns a list' ($null -ne (Get-PassphraseLoggingRisks))
} finally {
    Remove-Item Env:UNRAVIS_RELEASE_DATA -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host "`n$($script:checks) checks, $($script:failures.Count) failed."
if ($script:failures.Count) { exit 1 }
exit 0
