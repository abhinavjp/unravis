<#
.SYNOPSIS
  Offline tests for tools/remote (the read-only gate, quoting, redaction, permissions, dry runs). No server needed.
  Windows PowerShell 5.1 or PowerShell 7:  powershell -NoProfile -File tools\remote\Test-RemoteSalt.ps1
  Every secret below is an obvious fake.
#>
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'RemoteSalt.Lib.ps1')
$script:pass = 0; $script:fail = 0
function Check([string]$Name, [bool]$Cond) { if ($Cond) { $script:pass++ } else { $script:fail++; Write-Host "FAIL: $Name" } }
function Throws([scriptblock]$B) { try { & $B | Out-Null; return $false } catch { return $true } }
function Get-EmbeddedScript([string]$Command) {
    $b64 = [regex]::Match($Command, '^\$t=\[Text\.Encoding\]::UTF8\.GetString\(\[Convert\]::FromBase64String\(''([A-Za-z0-9+/]+={0,2})''\)\);& \(\[scriptblock\]::Create\(\$t\)\)\z').Groups[1].Value
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
}
# What Windows sees after Salt re-wraps a shell=powershell command as UTF-16 base64 -EncodedCommand.
function Get-SaltWrappedLength([string]$Command) { return [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Command)).Length + 80 }

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("remote-salt-test-" + [guid]::NewGuid().ToString('N'))
$originalRemoteHome = $env:UNRAVIS_REMOTE_HOME
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $fakeKey = Join-Path $tmp 'id_fake'; Set-Content -LiteralPath $fakeKey -Value 'not a real key'
    $logs = 'C:\ProgramData\Unravis', '/var/log/unravis'
    $cfg = [pscustomobject]@{ Host = '192.0.2.10'; Port = 22; User = 'ops'; Password = ''; KeyPath = $fakeKey; Fingerprint = ''; Targets = @('web01', 'web02'); LogPaths = $logs; UseSudo = $true; Mode = 'readonly' }
    $wcfg = $cfg.PSObject.Copy(); $wcfg.Mode = 'write'

    # ---- .env parsing
    $envf = Join-Path $tmp 'a.env'
    Set-Content -LiteralPath $envf -Value @('# comment', '', 'SALT_MASTER_HOST = 192.0.2.10', 'ssh_user="ops"', "SSH_PASSWORD='fake pass#1&2'", 'SSH_PORT=2222')
    $m = Read-EnvFile $envf
    Check 'env host' ($m['SALT_MASTER_HOST'] -eq '192.0.2.10')
    Check 'env key upper-cased, quotes stripped' ($m['SSH_USER'] -eq 'ops')
    Check 'env keeps # and & inside value' ($m['SSH_PASSWORD'] -eq 'fake pass#1&2')
    Set-Content -LiteralPath $envf -Value 'no equals here'
    Check 'env bad line throws' (Throws { Read-EnvFile $envf })

    # ---- config validation
    $base = @{ SALT_MASTER_HOST = '192.0.2.10'; SSH_USER = 'ops'; SSH_PASSWORD = 'fake-pass'; SSH_PORT = '22' }
    function Cfg([hashtable]$over) { $x = $base.Clone(); foreach ($k in $over.Keys) { $x[$k] = $over[$k] }; return $x }
    Check 'config ok' ((Get-RemoteConfig (Cfg @{})).Host -eq '192.0.2.10')
    Check 'config default readonly + sudo' ((Get-RemoteConfig (Cfg @{})).Mode -eq 'readonly' -and (Get-RemoteConfig (Cfg @{})).UseSudo)
    Check 'config empty host' (Throws { Get-RemoteConfig (Cfg @{ SALT_MASTER_HOST = '' }) })
    Check 'config host option injection' (Throws { Get-RemoteConfig (Cfg @{ SALT_MASTER_HOST = '-oProxyCommand=calc' }) })
    Check 'config host with space' (Throws { Get-RemoteConfig (Cfg @{ SALT_MASTER_HOST = 'a b' }) })
    Check 'config user option injection' (Throws { Get-RemoteConfig (Cfg @{ SSH_USER = '-oProxyCommand=x' }) })
    Check 'config bad port' (Throws { Get-RemoteConfig (Cfg @{ SSH_PORT = '99999' }) })
    Check 'config both auth' (Throws { Get-RemoteConfig (Cfg @{ SSH_KEY_PATH = $fakeKey }) })
    Check 'config no auth' (Throws { Get-RemoteConfig (Cfg @{ SSH_PASSWORD = '' }) })
    Check 'config key missing' (Throws { Get-RemoteConfig (Cfg @{ SSH_PASSWORD = ''; SSH_KEY_PATH = (Join-Path $tmp 'nope') }) })
    $ppk = Join-Path $tmp 'k.ppk'; Set-Content -LiteralPath $ppk -Value 'x'
    Check 'config ppk refused' (Throws { Get-RemoteConfig (Cfg @{ SSH_PASSWORD = ''; SSH_KEY_PATH = $ppk }) })
    Check 'config bad fingerprint' (Throws { Get-RemoteConfig (Cfg @{ SSH_HOST_KEY_FINGERPRINT = 'SHA256:short' }) })
    Check 'config wildcard target' (Throws { Get-RemoteConfig (Cfg @{ SALT_MINION_TARGETS = 'web*' }) })
    Check 'config compound target' (Throws { Get-RemoteConfig (Cfg @{ SALT_MINION_TARGETS = 'G@os:Windows' }) })
    Check 'config log path dotdot' (Throws { Get-RemoteConfig (Cfg @{ REMOTE_LOG_PATHS = 'C:\logs\..\x' }) })
    Check 'config bad mode' (Throws { Get-RemoteConfig (Cfg @{ REMOTE_MODE = 'yolo' }) })

    # ---- targets
    Check 'target ok' ($null -eq (Test-SaltTarget 'web01' $cfg))
    Check 'target not listed' ($null -ne (Test-SaltTarget 'db01' $cfg))
    Check 'target glob' ($null -ne (Test-SaltTarget '*' $cfg))
    Check 'target list' ($null -ne (Test-SaltTarget 'web01,web02' $cfg))
    $empty = $cfg.PSObject.Copy(); $empty.Targets = @()
    Check 'target none configured' ($null -ne (Test-SaltTarget 'web01' $empty))

    # ---- salt function gate
    Check 'ping allowed' ($null -eq (Test-SaltReadCall 'test.ping' @() $cfg))
    Check 'grains.item allowed' ($null -eq (Test-SaltReadCall 'grains.item' @('os', 'kernel') $cfg))
    Check 'service.status allowed' ($null -eq (Test-SaltReadCall 'service.status' @('Unravis') $cfg))
    Check 'file.read inside logs' ($null -eq (Test-SaltReadCall 'file.read' @('C:\ProgramData\Unravis\x.log') $cfg))
    Check 'file.read outside logs' ($null -ne (Test-SaltReadCall 'file.read' @('C:\Windows\System32\config\SAM') $cfg))
    Check 'file.read prefix trap' ($null -ne (Test-SaltReadCall 'file.read' @('C:\ProgramData\UnravisEvil\x.log') $cfg))
    Check 'file.read dotdot' ($null -ne (Test-SaltReadCall 'file.read' @('C:\ProgramData\Unravis\..\x') $cfg))
    foreach ($bad in 'state.apply', 'state.highstate', 'cmd.run', 'cmd.script', 'pkg.install', 'file.remove', 'file.write', 'service.restart', 'system.reboot', 'cp.push', 'saltutil.refresh_pillar', 'pillar.items', 'sys.doc', 'test.arg', 'grains.setval') {
        Check "salt fn refused: $bad" ($null -ne (Test-SaltReadCall $bad @('x') $cfg))
    }
    Check 'service name with semicolon' ($null -ne (Test-SaltReadCall 'service.status' @('x;reboot') $cfg))
    Check 'yaml dict arg refused' ($null -ne (Test-SaltReadCall 'grains.item' @('a: b') $cfg))
    Check 'yaml comment arg refused' ($null -ne (Test-SaltReadCall 'grains.item' @('os #x') $cfg))
    Check 'too many args' ($null -ne (Test-SaltReadCall 'test.ping' @('x') $cfg))
    Check 'salt-run allowed' ($null -eq (Test-SaltRunCall 'manage.up'))
    Check 'salt-run refused' ($null -ne (Test-SaltRunCall 'state.orchestrate'))

    # ---- free-form cmd.run gate
    $ok = @('tail -n 200 C:\ProgramData\Unravis\a.log', 'Get-Content C:\ProgramData\Unravis\a.log -Tail 50', 'cat /var/log/unravis/a.log', 'ls -la /var/log/unravis', 'Get-ChildItem C:\ProgramData\Unravis\*.log', 'tail -n 20 c:\programdata\unravis\a.log')
    foreach ($c in $ok) { Check "cmd allowed: $c" ($null -eq (Test-ReadOnlyCommand $c $cfg)) }
    $bad = @('rm -rf /var/log/unravis', 'tail -f /var/log/unravis/a.log', 'tail -F /var/log/unravis/a.log', 'cat /etc/shadow', 'cat /var/log/unravis/a.log; whoami', 'cat /var/log/unravis/a.log | sh',
        'cat /var/log/unravis/a.log && reboot', 'cat /var/log/unravis/a.log > /etc/x', 'cat $(whoami)', 'cat `id`', 'cat %COMSPEC%', 'cat /var/log/unravis/../../shadow', 'Get-Content -Path:C:\Windows\x', 'tail -fn 5 /var/log/unravis/a.log', 'tail --follow /var/log/unravis/a.log', 'Get-Content -Wai C:\ProgramData\Unravis\a.log', 'tail -n x /var/log/unravis/a.log', "cat \'/var/log/unravis/a.log", 'cat /var/log/unravis/a\x.log',
        'Get-Content -Wait C:\ProgramData\Unravis\a.log', 'cat secrets.txt', 'type id_rsa', 'cat', 'echo hi', 'Remove-Item C:\ProgramData\Unravis\a.log', 'Set-Content C:\ProgramData\Unravis\a.log x',
        "cat '/var/log/unravis/a.log", 'cat "/var/log/unravis/a.log"', 'cat /var/log/unravis/a.log: x', 'cat /var/log/unravis/a.log #x', "cat /var/log/unravis/a`nwhoami", 'grep -r pw /var/log/unravis',
        'cat --file=/etc/shadow', 'cat /var/log/unravisx/a.log', 'powershell -c whoami', 'curl http://x', 'Get-Content C:\ProgramData\Unravis\a.log -Encoding utf8 -Delete')
    foreach ($c in $bad) { Check "cmd refused: $c" ($null -ne (Test-ReadOnlyCommand $c $cfg)) }
    $nopath = $cfg.PSObject.Copy(); $nopath.LogPaths = @()
    Check 'cmd refused when REMOTE_LOG_PATHS empty' ($null -ne (Test-ReadOnlyCommand 'cat /var/log/unravis/a.log' $nopath))

    # ---- write gate
    Check 'write refused in readonly' ($null -ne (Test-WriteCall 'service.restart' 'Unravis' $cfg))
    Check 'write restart allowed in write mode' ($null -eq (Test-WriteCall 'service.restart' 'Unravis' $wcfg))
    Check 'write state.apply never' ($null -ne (Test-WriteCall 'state.apply' 'x' $wcfg))
    Check 'write protected sshd' ($null -ne (Test-WriteCall 'service.stop' 'sshd' $wcfg))
    Check 'write protected salt-minion' ($null -ne (Test-WriteCall 'service.restart' 'salt-minion' $wcfg))
    Check 'write bad service name' ($null -ne (Test-WriteCall 'service.restart' 'a;b' $wcfg))

    # ---- fixed Windows updates
    $tag = 'v0.1.0-rc.68'
    $updateTarget = 'BG-SRV-UK-01'
    $updateRoot = 'D:\UnravisUpdate'
    $appInstall = 'C:\Users\btlsupport1\AppData\Local\Programs\Unravis'
    $updatePaths = @('D:\UnravisBlackbox', $updateRoot, $appInstall)
    foreach ($valid in @($tag, 'v0.1.0', 'v12.34.56-rc.789')) {
        Check "valid release tag $valid" (Test-UpdateTag $valid)
    }
    foreach ($invalid in @('v0.1.0-rc.1;calc', '..\', '', ' ', " $tag", "$tag ", "$tag`n", 'v0.1.0-rc.1000', 'v123.1.0', 'V0.1.0-rc.1', 'v0.1.0-RC.1')) {
        Check "invalid release tag '$invalid'" ($null -ne (Test-WriteCall 'update.fetch' $invalid $wcfg))
        Check "no script for invalid tag '$invalid'" (Throws { New-UpdateCommand 'update.fetch' $invalid })
    }
    Check 'unknown update verb refused' ($null -ne (Test-WriteCall 'update.unknown' $tag $wcfg))
    $deletions = @{
        'update.fetch' = @('$marker', '$stage'); 'update.app' = @('$work')
        'update.rollback' = @('$work'); 'update.cleanup' = @('$stage')
    }
    foreach ($verb in @('update.fetch', 'update.blackbox', 'update.app', 'update.app-start', 'update.rollback', 'update.cleanup')) {
        Check "$verb requires write mode" ((Test-WriteCall $verb $tag $cfg) -match 'REMOTE_MODE is readonly')
        Check "$verb allowed with tag in write mode" ($null -eq (Test-WriteCall $verb $tag $wcfg))
        Check "$verb refused by Salt read gate" ($null -ne (Test-SaltReadCall $verb @($tag) $wcfg))
        $recipe = New-UpdateCommand $verb $tag
        $remote = New-SaltRemoteCommand 'salt' (@($updateTarget, 'cmd.run', $recipe.Cmd) + $recipe.Kwargs) $wcfg 170
        Check "$verb has one quoted YAML-safe Windows command" (
            $recipe.Cmd -cmatch '^\$t=\[Text\.Encoding\]::UTF8\.GetString\(\[Convert\]::FromBase64String\(''[A-Za-z0-9+/]+={0,2}''\)\);& \(\[scriptblock\]::Create\(\$t\)\)\z' -and
            $recipe.Cmd -notmatch "`n" -and
            (Test-YamlSafe $recipe.Cmd) -and
            $remote -ceq "sudo -n salt --out=json --no-color --static -t 170 '$updateTarget' 'cmd.run' '$($recipe.Cmd.Replace("'", "'\''"))' 'shell=powershell'"
        )
        Check "$verb fits Windows' command-line limit after Salt re-encodes it" ((Get-SaltWrappedLength $recipe.Cmd) -lt 31000)
        $source = Get-EmbeddedScript $recipe.Cmd
        $tokens = $null; $errors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$errors)
        Check "$verb embedded Windows PowerShell parses" ($errors.Count -eq 0)
        Check "$verb tag is a single-quoted literal" ($tokens[2].Text -ceq "'$tag'" -and [regex]::Matches($source, [regex]::Escape($tag)).Count -eq 1)
        Check "$verb has no expression evaluator" ($source -notmatch '\b(?:Invoke-Expression|iex)\b')
        $actualDeletes = @($ast.FindAll({
            param($node)
            $node -is [System.Management.Automation.Language.CommandAst] -and $node.GetCommandName() -eq 'Remove-Item'
        }, $true) | ForEach-Object {
            if ($_.CommandElements[1].Extent.Text -ne '-LiteralPath') { 'nonliteral path' }
            $_.CommandElements[2].Extent.Text
        })
        Check "$verb removes only tag staging or its own .old transaction directory" (($actualDeletes -join ',') -ceq ($deletions[$verb] -join ','))
        if ($actualDeletes -contains '$stage') {
            Check "$verb stages only under the tagged update directory" (
                $source.Contains('$stage = Join-Path $updateRoot $tag') -and $source.Contains("'$updateRoot'") -and
                $source.Contains('$marker = Join-Path $stage ''.verified''')
            )
        }
        if ($actualDeletes -contains '$work') {
            Check "$verb scratch removal is scoped to one .old in the app install" (
                $source.Contains("'$appInstall'") -and
                $source.Contains('$work = Join-Path $install (''.update-'' + [guid]::NewGuid().ToString(''N'') + ''.old'')')
            )
        }
    }
    $statusConfig = $cfg.PSObject.Copy()
    foreach ($missing in $updatePaths) {
        $statusConfig.LogPaths = @($updatePaths | Where-Object { $_ -ne $missing })
        Check "status requires an allowlist for $missing" (Throws { New-UpdateStatusCommand $statusConfig })
    }
    $statusConfig.LogPaths = $updatePaths
    $statusScript = Get-EmbeddedScript (New-UpdateStatusCommand $statusConfig).Cmd
    $tokens = $null; $errors = $null
    $null = [System.Management.Automation.Language.Parser]::ParseInput($statusScript, [ref]$tokens, [ref]$errors)
    Check 'status embedded script parses and filters versions and backups' (
        $errors.Count -eq 0 -and $statusScript -match 'blackboxManifestVersion' -and
        $statusScript -match 'appSetupStateVersion' -and $statusScript -match "Filter '\*\.old'" -and
        $statusScript -notmatch 'Remove-Item|Set-Content|Invoke-Expression|\biex\b'
    )

    # Exercise the embedded scripts with local ZIPs, a fake downloader, and a disposable install.
    $assetDirectory = Join-Path $tmp 'assets'
    $fakeStage = Join-Path $tmp 'staging'
    $fakeInstall = Join-Path $tmp 'installed'
    $fakeManifest = Join-Path $tmp 'manifest.json'
    $appInput = Join-Path $tmp 'app-input'
    $recorderInput = Join-Path $tmp 'recorder-input'
    $null = New-Item -ItemType Directory -Path @($assetDirectory, $fakeInstall, $appInput, $recorderInput)
    Set-Content -LiteralPath (Join-Path $appInput 'unravis.exe') -Value 'new app'
    Set-Content -LiteralPath (Join-Path $appInput 'unravis-setup.exe') -Value 'new setup'
    Set-Content -LiteralPath (Join-Path $recorderInput 'unravis-blackbox.exe') -Value 'new recorder'
    $appAsset = "unravis-$tag-windows-x64.zip"
    $recorderAsset = "unravis-blackbox-$tag-windows-x64.zip"
    Compress-Archive -Path (Join-Path $appInput '*') -DestinationPath (Join-Path $assetDirectory $appAsset)
    Compress-Archive -Path (Join-Path $recorderInput '*') -DestinationPath (Join-Path $assetDirectory $recorderAsset)
    $checksums = foreach ($asset in @($appAsset, $recorderAsset)) {
        '{0}  {1}' -f (Get-FileHash -LiteralPath (Join-Path $assetDirectory $asset)).Hash.ToLowerInvariant(), $asset
    }
    Set-Content -LiteralPath (Join-Path $assetDirectory 'SHA256SUMS.txt') -Value $checksums
    Set-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe') -Value 'old app'
    Set-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe.old') -Value 'earlier app'
    Set-Content -LiteralPath (Join-Path $fakeInstall 'unravis-setup.exe') -Value 'old setup'
    Set-Content -LiteralPath (Join-Path $fakeInstall 'unravis-blackbox.exe') -Value 'installed recorder'
    Set-Content -LiteralPath (Join-Path $fakeInstall 'setup-state.json') -Value '{"version":"0.1.0-rc.67"}'
    Set-Content -LiteralPath $fakeManifest -Value '{"version":"0.1.0-rc.66"}'
    $bin = New-Item -ItemType Directory -Path (Join-Path $fakeInstall 'bin')
    Set-Content -LiteralPath (Join-Path $bin.FullName 'keep.exe') -Value 'keep bin'

    function Run-EmbeddedFixture([string]$Source, [switch]$FailInstall, [switch]$ActiveSession) {
        $Source = $Source.Replace($updateRoot, $fakeStage.Replace("'", "''"))
        $Source = $Source.Replace($appInstall, $fakeInstall.Replace("'", "''"))
        $Source = $Source.Replace('D:\UnravisBlackbox\manifest.json', $fakeManifest.Replace("'", "''"))
        $Source = $Source.Replace("'Global\UnravisRemoteUpdate'", "'Local\UnravisUpdateFixture-$PID'")
        $fixture = Join-Path $tmp 'UpdateFixture.ps1'
        $setup = @'
param([string]$Assets, [switch]$FailInstall, [switch]$ActiveSession)
function Invoke-WebRequest([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile) {
    if ($Uri -notmatch '^https://github\.com/abhinavjp/unravis/releases/download/v0\.1\.0-rc\.68/([^/]+)\z') {
        throw "Unexpected URL: $Uri"
    }
    Copy-Item -LiteralPath (Join-Path $Assets $Matches[1]) -Destination $OutFile
}
function Get-Process { }
function Get-Service { }
function quser.exe {
    $global:LASTEXITCODE = 0
    ' USERNAME              SESSIONNAME        ID  STATE   IDLE TIME  LOGON TIME'
    if ($ActiveSession) { '>btlsupport1           rdp-tcp#1            2  Active          .  11/27/2026 5:00 PM' }
    else {
        ' btlsupport1                                2  Disc            .  11/27/2026 5:00 PM'
        '>otheruser             console             1  Active          .  11/27/2026 5:00 PM'
    }
}
function New-ScheduledTaskAction([string]$Execute, [string]$WorkingDirectory) {
    if (-not $ActiveSession -or $Execute -ne (Join-Path $WorkingDirectory 'unravis.exe')) { throw 'Unexpected scheduled executable.' }
    return $Execute
}
function New-ScheduledTaskPrincipal([string]$UserId, [string]$LogonType) {
    if ($UserId -ne 'btlsupport1' -or $LogonType -ne 'Interactive') { throw 'Unexpected task principal.' }
    return $UserId
}
function Register-ScheduledTask([string]$TaskName, [string]$Action, [string]$Principal) {
    $script:registeredTask = $TaskName
    $script:taskStartTime = [datetime]::MinValue
}
function Start-ScheduledTask([string]$TaskName) {
    if ($TaskName -ne $script:registeredTask) { throw 'Wrong task started.' }
    $script:taskStartTime = [datetime]::Now
}
function Get-ScheduledTaskInfo([string]$TaskName) {
    if ($TaskName -ne $script:registeredTask) { throw 'Wrong task checked.' }
    return @{ LastRunTime = $script:taskStartTime; LastTaskResult = 267009 }
}
function Unregister-ScheduledTask([string]$TaskName, [switch]$Confirm) {
    if ($TaskName -ne $script:registeredTask -or $Confirm) { throw 'Wrong task deletion.' }
    Write-Output 'Fake task removed.'
}
if ($FailInstall) {
    function Move-Item([string]$LiteralPath, [string]$Destination, [string]$ErrorAction) {
        if ($LiteralPath -match '\.old\\unravis-setup\.exe\z') { throw 'Simulated replacement failure.' }
        Microsoft.PowerShell.Management\Move-Item -LiteralPath $LiteralPath -Destination $Destination -ErrorAction $ErrorAction
    }
}
'@
        Set-Content -LiteralPath $fixture -Value ($setup + "`n" + $Source)
        $params = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $fixture, '-Assets', $assetDirectory)
        if ($FailInstall) { $params += '-FailInstall' }
        if ($ActiveSession) { $params += '-ActiveSession' }
        $prev = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
        try {
            $output = & (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') @params 2>&1 |
                ForEach-Object { "$_" } | Out-String
            return @{ Code = $LASTEXITCODE; Out = $output }
        } finally { $ErrorActionPreference = $prev }
    }
    $fetchSource = Get-EmbeddedScript (New-UpdateCommand 'update.fetch' $tag).Cmd
    $appSource = Get-EmbeddedScript (New-UpdateCommand 'update.app' $tag).Cmd
    $rollbackSource = Get-EmbeddedScript (New-UpdateCommand 'update.rollback' $tag).Cmd
    $stageTag = Join-Path $fakeStage $tag
    $verified = Join-Path $stageTag '.verified'
    $r = Run-EmbeddedFixture $appSource
    Check 'app runtime refuses a missing marker' ($r.Code -ne 0 -and $r.Out -match 'Run update.fetch')
    $r = Run-EmbeddedFixture $fetchSource
    Check "staging verifies and extracts two fake release ZIPs ($($r.Out.Trim()))" (
        $r.Code -eq 0 -and (Test-Path -LiteralPath $verified) -and
        (Test-Path -LiteralPath (Join-Path $stageTag 'app\unravis.exe')) -and
        (Test-Path -LiteralPath (Join-Path $stageTag 'blackbox\unravis-blackbox.exe'))
    )
    $r = Run-EmbeddedFixture (Get-EmbeddedScript (New-UpdateCommand 'update.blackbox' $tag).Cmd)
    Check 'recorder update refuses a missing service' ($r.Code -ne 0 -and $r.Out -match 'service is not installed')
    $r = Run-EmbeddedFixture $appSource -FailInstall
    Check "app restores live files and prior backups on failed rename ($($r.Out.Trim()))" (
        $r.Code -ne 0 -and $r.Out -match 'Simulated replacement failure' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe')) -eq 'old app' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe.old')) -eq 'earlier app'
    )
    $r = Run-EmbeddedFixture $appSource
    $receiptPath = Join-Path $fakeInstall "update-$tag.txt"
    Check "app runtime installs releases and preserves local state ($($r.Out.Trim()))" (
        $r.Code -eq 0 -and (Test-Path -LiteralPath $receiptPath) -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe')) -eq 'new app' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe.old')) -eq 'old app' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis-blackbox.exe')) -eq 'installed recorder' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'setup-state.json')) -eq '{"version":"0.1.0-rc.67"}' -and
        (Test-Path -LiteralPath (Join-Path $bin.FullName 'keep.exe'))
    )
    $r = Run-EmbeddedFixture $appSource
    Check 'identical app update keeps the prior backup' ($r.Code -eq 0 -and $r.Out -match 'backups are retained')
    $appStartSource = Get-EmbeddedScript (New-UpdateCommand 'update.app-start' $tag).Cmd
    $r = Run-EmbeddedFixture $appStartSource
    Check 'app start skips a disconnected user even when another user is active' ($r.Code -eq 0 -and $r.Out -match 'No active btlsupport1 session')
    $r = Run-EmbeddedFixture $appStartSource -ActiveSession
    Check 'app start uses an interactive principal and removes its one-shot task' ($r.Code -eq 0 -and $r.Out -match 'Started Unravis' -and $r.Out -match 'Fake task removed')
    $r = Run-EmbeddedFixture $statusScript
    $readStatus = $r.Out | ConvertFrom-Json
    Check 'status reports recorded versions and current backups' (
        $r.Code -eq 0 -and $readStatus.blackboxManifestVersion -eq '0.1.0-rc.66' -and
        $readStatus.appSetupStateVersion -eq '0.1.0-rc.67' -and $readStatus.updateDirectoryExists -and
        $readStatus.appBackups -contains 'unravis.exe.old'
    )
    $r = Run-EmbeddedFixture $rollbackSource
    Check "app restores backups and prints manual recorder step ($($r.Out.Trim()))" (
        $r.Code -eq 0 -and $r.Out -match 'unravis-blackbox.exe.old' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis.exe')) -eq 'old app' -and
        (Get-Content -LiteralPath (Join-Path $fakeInstall 'unravis-setup.exe')) -eq 'old setup' -and
        -not (Test-Path -LiteralPath (Join-Path $fakeInstall 'unravis.exe.old'))
    )
    Add-Content -LiteralPath (Join-Path $assetDirectory $recorderAsset) -Value 'corruption'
    $r = Run-EmbeddedFixture $fetchSource
    Check "checksum mismatch invalidates an earlier fetch ($($r.Out.Trim()))" (
        $r.Code -ne 0 -and $r.Out -match 'mismatched SHA-256' -and -not (Test-Path -LiteralPath $verified)
    )
    $neighbor = New-Item -ItemType Directory -Path (Join-Path $fakeStage 'v0.1.0-rc.69')
    $r = Run-EmbeddedFixture (Get-EmbeddedScript (New-UpdateCommand 'update.cleanup' $tag).Cmd)
    Check 'cleanup deletes exactly the selected tag' ($r.Code -eq 0 -and -not (Test-Path -LiteralPath $stageTag) -and (Test-Path -LiteralPath $neighbor.FullName))

    # ---- Blackbox verbs (B6)
    $bbcfg = $wcfg.PSObject.Copy(); $bbcfg.LogPaths = @('C:\ProgramData\Unravis Blackbox')
    Check 'bb write refused in readonly' ($null -ne (Test-BlackboxWrite 'blackbox.collector' 'redis.info' @('on') $cfg))
    Check 'bb collector on' ($null -eq (Test-BlackboxWrite 'blackbox.collector' 'redis.info' @('on') $wcfg))
    Check 'bb collector off every' ($null -eq (Test-BlackboxWrite 'blackbox.collector' 'host.tcp' @('off', 'every=60') $wcfg))
    foreach ($bad in @(@('redis.info; calc', @('on')), @('Redis', @('on')), @('', @('on')), @('a', @('on')), @('redis.info', @()), @('redis.info', @('maybe')), @('redis.info', @('on', 'every=x')), @('redis.info', @('on', 'every=60', 'extra')), @('redis.info', @('on', 'every=1234567')), @('redis.info', @('on', 'every=99999')), @('Redis.info', @('on')), @('redis.info', @('ON')), @('redis.info', @('on', 'EVERY=60')), @('redis.info', @('on', ('every=' + [string][char]0x0666 + [char]0x0660))))) {
        Check ("bb collector refused: " + ($bad[0]) + ' ' + ($bad[1] -join ' ')) ($null -ne (Test-BlackboxWrite 'blackbox.collector' $bad[0] $bad[1] $wcfg))
    }
    Check 'bb counter add ok' ($null -eq (Test-BlackboxWrite 'blackbox.counter.add' '\Processor(_Total)\% Processor Time' @() $wcfg))
    Check 'bb counter add no instance ok' ($null -eq (Test-BlackboxWrite 'blackbox.counter.add' '\Memory\Available MBytes' @() $wcfg))
    Check 'bb counter remove ok' ($null -eq (Test-BlackboxWrite 'blackbox.counter.remove' '\Memory\Available MBytes' @() $wcfg))
    foreach ($bad in @("\Memory\x'; calc; '", '\Memory\x" & calc', '\Memory\a & b', '\Memory\a^b', '\Memory\$env', 'Memory\x', '\Memory\a`b', '\Memory\%a%b', '\Net(*)\x', '\Memory\a: b', '\Memory\a #b', '')) {
        Check "bb counter refused: $bad" ($null -ne (Test-BlackboxWrite 'blackbox.counter.add' $bad @() $wcfg))
    }
    Check 'bb counter takes no args' ($null -ne (Test-BlackboxWrite 'blackbox.counter.add' '\Memory\Available MBytes' @('x') $wcfg))
    Check 'bb rollback ok' ($null -eq (Test-BlackboxWrite 'blackbox.rollback' 'last' @() $wcfg))
    Check 'bb rollback only lowercase last' ($null -ne (Test-BlackboxWrite 'blackbox.rollback' 'LAST' @() $wcfg))
    Check 'bb null args are no args' ($null -eq (Test-BlackboxWrite 'blackbox.rollback' 'last' $null $wcfg))
    Check 'bb rollback only last' ($null -ne (Test-BlackboxWrite 'blackbox.rollback' 'x' @() $wcfg))
    Check 'bb unknown verb refused' ($null -ne (Test-BlackboxWrite 'blackbox.apply' 'x' @() $wcfg))
    Check 'bb is not a service write' ($null -ne (Test-WriteCall 'blackbox.collector' 'redis.info' $wcfg))
    $c = New-BlackboxCommand -Fn 'blackbox.collector' -Name 'redis.info' -WriteArgs @('on', 'every=60')
    Check 'bb collector command' ($c.Cmd -eq "`$pf=`$env:ProgramW6432;if(!`$pf){`$pf=`$env:ProgramFiles};`$x=Join-Path `$pf 'Unravis Blackbox\unravis-blackbox.exe'; & `$x config set 'redis.info' on --every 60 --json; exit `$LASTEXITCODE" -and $c.Kwargs -contains 'shell=powershell')
    $c = New-BlackboxCommand -Fn 'blackbox.counter.add' -Name '\Memory\Available MBytes'
    Check 'bb counter command' ($c.Cmd.EndsWith("config set counters.extra add '\Memory\Available MBytes' --json; exit `$LASTEXITCODE"))
    Check 'bb commands are YAML safe' ((@('blackbox.show', 'blackbox.history', 'blackbox.status', 'blackbox.rollback') | ForEach-Object { Test-YamlSafe (New-BlackboxCommand -Fn $_).Cmd }) -notcontains $false)
    Check 'bb command does not start with an anchor' (-not (New-BlackboxCommand -Fn 'blackbox.show').Cmd.StartsWith('&'))
    Check 'bb unknown verb throws' (Throws { New-BlackboxCommand -Fn 'blackbox.apply' })
    Check 'write hash keeps the old shape without args' ((Get-WriteHash 'web01' 'service.restart' 'Unravis' '' '2026-11-26T10:00:00.0000000Z') -ne (Get-WriteHash 'web01' 'service.restart' 'Unravis' 'x' '2026-11-26T10:00:00.0000000Z'))
    $shaT = [System.Security.Cryptography.SHA256]::Create()
    $oldHash = -join ($shaT.ComputeHash([System.Text.Encoding]::UTF8.GetBytes('t|f|n|c')) | Select-Object -First 6 | ForEach-Object { $_.ToString('x2') })
    Check 'write hash without args is the old formula' ((Get-WriteHash 't' 'f' 'n' '' 'c') -eq $oldHash)
    Check 'write hash covers the args' ((Get-WriteHash 'web01' 'blackbox.collector' 'redis.info' 'on' 'c') -ne (Get-WriteHash 'web01' 'blackbox.collector' 'redis.info' 'off' 'c'))
    Check 'write hash is 12 hex' ((Get-WriteHash 'a' 'b' 'c' '' 'd') -match '^[0-9a-f]{12}$')

    # ---- pulling a bundle (B6)
    Check 'pull ok zip' ($null -eq (Test-PullPath 'C:\ProgramData\Unravis Blackbox\snapshots\case-1.zip' $bbcfg))
    Check 'pull ok manifest' ($null -eq (Test-PullPath 'C:\ProgramData\Unravis Blackbox\snapshots\case-1-2.manifest.json' $bbcfg))
    foreach ($bad in @('C:\ProgramData\Unravis Blackbox\bb-20261126-11.ndjson', 'C:\ProgramData\Unravis Blackbox\snapshots\snap-000001.json', 'C:\ProgramData\Unravis Blackbox\snapshots\a\b.zip', 'C:\ProgramData\Unravis Blackbox\snapshots\*.zip', 'C:\ProgramData\Unravis Blackbox\snapshots\..\x.zip', 'C:\Windows\snapshots\a.zip', '/var/log/snapshots/a.zip', 'C:\ProgramData\Unravis Blackbox\snapshots\.zip', 'C:\ProgramData\Unravis Blackbox\snapshots\a b.zip', "C:\ProgramData\Unravis Blackbox\snapshots\a'b.zip")) {
        Check "pull refused: $bad" ($null -ne (Test-PullPath $bad $bbcfg))
    }
    Check 'pull refused: snapshots folder below the allowed one' ($null -ne (Test-PullPath 'C:\ProgramData\Unravis Blackbox\deeper\snapshots\a.zip' $bbcfg))
    Check 'pull refused: device name' ($null -ne (Test-PullPath 'C:\ProgramData\Unravis Blackbox\snapshots\nul.zip' $bbcfg))
    Check 'pull refused: COM1 manifest' ($null -ne (Test-PullPath 'C:\ProgramData\Unravis Blackbox\snapshots\com1.manifest.json' $bbcfg))
    Check 'pull refused with no log paths' ($null -ne (Test-PullPath 'C:\ProgramData\Unravis Blackbox\snapshots\a.zip' $nopath))
    $pc = New-PullChunkCommand 'C:\ProgramData\Unravis Blackbox\snapshots\a.zip' 196608 196608 $true
    Check 'pull chunk text' ($pc.Contains("Open('C:\ProgramData\Unravis Blackbox\snapshots\a.zip'") -and $pc.Contains('Seek(196608') -and $pc.Contains('Get-FileHash') -and -not $pc.Contains('__'))
    Check 'pull chunk without hash' ((New-PullChunkCommand 'C:\ProgramData\Unravis Blackbox\snapshots\a.zip' 0 1024 $false) -notmatch 'Get-FileHash')
    Check 'pull chunk is YAML safe' (Test-YamlSafe $pc)
    Check 'pull chunk checks for links and hides progress' ($pc.Contains('ReparsePoint') -and $pc.Contains("ProgressPreference='SilentlyContinue'"))
    $sum = ('ab' * 32)
    $good = "1000|$sum|0|" + [Convert]::ToBase64String([byte[]](1, 2, 3))
    $pp = ConvertFrom-PullPiece $good 0 $true
    Check 'pull piece parsed' ($pp.Size -eq 1000 -and $pp.Hash -eq $sum -and $pp.Data.Length -eq 3)
    $noise = "#< CLIXML`r`nPreparing modules for first use.`r`n$good`r`n"
    Check 'pull piece ignores noise before the data line' ((ConvertFrom-PullPiece $noise 0 $true).Size -eq 1000)
    Check 'pull piece refuses a wrong offset' (Throws { ConvertFrom-PullPiece $good 5 $true })
    Check 'pull piece refuses no hash on the first piece' (Throws { ConvertFrom-PullPiece '1000|-|0|AQID' 0 $true })
    Check 'pull piece accepts a dash after the first' ((ConvertFrom-PullPiece '1000|-|10|AQID' 10 $false).Data.Length -eq 3)
    Check 'pull piece refuses garbage' (Throws { ConvertFrom-PullPiece 'Get-FileHash : file not found' 0 $true })
    Check 'pull piece refuses data after the base64' (Throws { ConvertFrom-PullPiece "1000|$sum|0|AQID extra" 0 $true })
    Check 'pull piece refuses text after the data line' (Throws { ConvertFrom-PullPiece "$good`r`nWARNING: x" 0 $true })
    Check 'pull piece refuses a short hash' (Throws { ConvertFrom-PullPiece '1000|abcd|0|AQID' 0 $true })
    Check 'retcode flag is added' ((New-SaltRemoteCommand 'salt' @('web01', 'test.ping') $cfg 30 -Retcode).Contains('--retcode-passthrough'))
    Check 'retcode flag is off by default' (-not (New-SaltRemoteCommand 'salt' @('web01', 'test.ping') $cfg 30).Contains('--retcode-passthrough'))

    # ---- path checks
    Check 'path exact folder' (Test-PathAllowed 'C:\ProgramData\Unravis' $logs)
    Check 'path prefix trap' (-not (Test-PathAllowed 'C:\ProgramData\UnravisEvil' $logs))
    Check 'path wildcard last segment' (Test-PathAllowed 'C:\ProgramData\Unravis\*.log' $logs)
    Check 'path wildcard dir refused' (-not (Test-PathAllowed 'C:\ProgramData\*\a.log' $logs))
    Check 'path linux case sensitive' (-not (Test-PathAllowed '/VAR/log/unravis/a' $logs))

    # ---- quoting of the remote command
    $r = New-SaltRemoteCommand 'salt' @('web01', 'cmd.run', "tail -n 5 'a b'") $cfg 30
    Check 'sudo prefix' ($r.StartsWith('sudo -n salt --out=json --no-color --static -t 30 '))
    Check 'quote escaping' ($r.Contains("'tail -n 5 '\''a b'\'''"))
    Check 'no sudo when off' ((New-SaltRemoteCommand 'salt-key' @('-L') ($cfg.PSObject.Copy() | ForEach-Object { $_.UseSudo = $false; $_ }) 30) -eq "salt-key --out=json --no-color '-L'")
    Check 'ShQuote hostile' ((ConvertTo-ShSingleQuoted "a'; reboot; '") -eq "'a'\''; reboot; '\'''")

    # ---- recipes
    $t = New-ReadRecipe -Recipe 'LogTail' -Os 'windows' -Path 'C:\ProgramData\Unravis\a.log' -Lines 100 -Config $cfg
    Check 'recipe win tail' ($t.Cmd -eq "Get-Content -LiteralPath 'C:\ProgramData\Unravis\a.log' -Tail 100" -and $t.Kwargs -contains 'shell=powershell')
    $t = New-ReadRecipe -Recipe 'LogTail' -Os 'linux' -Path '/var/log/unravis/a.log' -Lines 10 -Config $cfg
    Check 'recipe linux tail' ($t.Cmd -eq "tail -n 10 '/var/log/unravis/a.log'" -and $t.Kwargs -contains 'python_shell=False')
    Check 'recipe tail outside' (Throws { New-ReadRecipe -Recipe 'LogTail' -Os 'linux' -Path '/etc/shadow' -Config $cfg })
    Check 'recipe tail wildcard' (Throws { New-ReadRecipe -Recipe 'LogTail' -Os 'linux' -Path '/var/log/unravis/*.log' -Config $cfg })
    Check 'recipe grep pattern injection' (Throws { New-ReadRecipe -Recipe 'LogGrep' -Os 'linux' -Path '/var/log/unravis/a.log' -Pattern "x'; reboot; '" -Config $cfg })
    Check 'recipe grep pattern yaml' (Throws { New-ReadRecipe -Recipe 'LogGrep' -Os 'linux' -Path '/var/log/unravis/a.log' -Pattern 'Error: x' -Config $cfg })
    Check 'recipe lines too big' (Throws { New-ReadRecipe -Recipe 'LogTail' -Os 'linux' -Path '/var/log/unravis/a.log' -Lines 99999 -Config $cfg })
    Check 'recipe eventlog linux refused' (Throws { New-ReadRecipe -Recipe 'EventLog' -Os 'linux' -LogName 'Application' -Config $cfg })
    Check 'recipe eventlog name injection' (Throws { New-ReadRecipe -Recipe 'EventLog' -Os 'windows' -LogName "x'; calc; '" -Config $cfg })
    $t = New-ReadRecipe -Recipe 'EventLog' -Os 'windows' -LogName 'Application' -Lines 20 -Config $cfg
    Check 'recipe eventlog' ($t.Cmd.StartsWith("Get-WinEvent -LogName 'Application' -MaxEvents 20") -and $t.Cmd.Contains('Out-String'))

    # ---- ssh argument list: no secret, host verified, own known_hosts
    $pwCfg = $cfg.PSObject.Copy(); $pwCfg.KeyPath = ''; $pwCfg.Password = 'FAKE-PASSWORD-123'
    $a = (New-SshArgs $pwCfg 'echo hi') -join ' '
    Check 'ssh args hide password' (-not $a.Contains('FAKE-PASSWORD-123'))
    Check 'ssh args strict host key' ($a.Contains('StrictHostKeyChecking=yes') -and $a.Contains('-F NUL') -and $a.Contains('ClearAllForwardings=yes'))
    Check 'ssh args password mode' ($a.Contains('PubkeyAuthentication=no') -and -not $a.Contains('BatchMode'))
    Check 'ssh args host after --' ($a.Contains('-- 192.0.2.10 echo hi'))
    $a = (New-SshArgs $cfg 'echo hi') -join ' '
    Check 'ssh args key mode' ($a.Contains('BatchMode=yes') -and $a.Contains('PasswordAuthentication=no') -and $a.Contains('IdentitiesOnly=yes'))
    Check 'winarg plain' ((ConvertTo-WinArg 'abc') -eq 'abc')
    Check 'winarg spaces' ((ConvertTo-WinArg 'a b') -eq '"a b"')
    Check 'winarg trailing backslash' ((ConvertTo-WinArg 'C:\x y\') -eq '"C:\x y\\"')

    # ---- redaction
    Check 'redact exact secret' ((Protect-Text 'login with FAKE-PASSWORD-123 ok' @('FAKE-PASSWORD-123')) -eq 'login with [REDACTED] ok')
    Check 'redact password=' ((Protect-Text 'cfg password=hunter2, next' @()) -notmatch 'hunter2')
    Check 'redact bearer' ((Protect-Text 'authorization: Bearer abc.def.ghi' @()) -notmatch 'abc\.def')

    # ---- permissions and location
    $priv = Join-Path $tmp 'priv'; New-Item -ItemType Directory -Path $priv | Out-Null
    $pf = Join-Path $priv 'x.env'; Set-Content -LiteralPath $pf -Value 'k=v'
    Set-PrivateAcl $priv; Set-PrivateAcl $pf
    Check 'acl private after Set-PrivateAcl' ((Get-AclProblems $priv).Count -eq 0 -and (Get-AclProblems $pf).Count -eq 0)
    & icacls.exe $pf /grant '*S-1-5-32-545:R' | Out-Null
    Check 'acl detects Users group' ((Get-AclProblems $pf).Count -ge 1)
    Check 'location detects OneDrive' ((Get-LocationProblems 'C:\Users\x\OneDrive\x\.env').Count -ge 1)
    $repoRoot = (& git -C $PSScriptRoot rev-parse --show-toplevel 2>$null)
    if ($repoRoot) { Check 'location detects git repo' ((Get-LocationProblems (Join-Path $repoRoot 'tools\remote\.env')).Count -ge 1) }
    Check 'location ok for plain folder' ((Get-LocationProblems $pf).Count -eq 0)

    # ---- audit log: written, capped, no secret
    $env:UNRAVIS_REMOTE_HOME = $priv
    Write-Audit -Action 'Salt' -Decision 'ran' -Target 'web01' -Command 'salt web01 test.ping password=FAKE-PASSWORD-123' -Secrets @('FAKE-PASSWORD-123')
    $al = Get-Content -LiteralPath (Join-Path $priv 'audit.log') -Raw
    Check 'audit written' ($al -match 'test.ping')
    Check 'audit has no secret' ($al -notmatch 'FAKE-PASSWORD-123')
    [System.IO.File]::WriteAllText((Join-Path $priv 'audit.log'), ('x' * 270000))
    Write-Audit -Action 'Salt' -Decision 'ran'
    Check 'audit rotates over 256 KB' ((Test-Path (Join-Path $priv 'audit.log.1')) -and (Get-Item (Join-Path $priv 'audit.log')).Length -lt 2000)

    # ---- the script end to end, dry run (no connection). Password mode: the fake password must never show up.
    $ps = (Get-Process -Id $PID).Path
    $script1 = Join-Path $PSScriptRoot 'Invoke-RemoteSalt.ps1'
    $envFile = Join-Path $priv '.env'
    $testSettings = @('SALT_MASTER_HOST=192.0.2.10', 'SSH_USER=ops', 'SSH_PASSWORD=FAKE-PASSWORD-123',
        "SALT_MINION_TARGETS=web01,$updateTarget", ('REMOTE_LOG_PATHS=C:\ProgramData\Unravis,/var/log/unravis,' + ($updatePaths -join ',')))
    Set-Content -LiteralPath $envFile -Value ($testSettings + 'REMOTE_MODE=write')
    Set-PrivateAcl (Join-Path $priv '.env')
    Remove-Item (Join-Path $priv 'audit.log*') -Force
    function Run([string[]]$a) {
        $prev = $ErrorActionPreference; $ErrorActionPreference = 'Continue'   # Windows PowerShell 5.1 turns child stderr into a terminating error under 'Stop'
        try { $o = & $ps -NoProfile -ExecutionPolicy Bypass -File $script1 @a -DryRun 2>&1 | ForEach-Object { "$_" } | Out-String; $code = $LASTEXITCODE }
        finally { $ErrorActionPreference = $prev }
        return @{ Code = $code; Out = $o }
    }
    function RunLive([string[]]$a) {
        $prev = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
        try { $o = & $ps -NoProfile -ExecutionPolicy Bypass -File $script1 @a 2>&1 | ForEach-Object { "$_" } | Out-String; $code = $LASTEXITCODE }
        finally { $ErrorActionPreference = $prev }
        return @{ Code = $code; Out = $o }
    }
    $r = Run @('-Action', 'Salt', '-Target', 'web01', '-Fn', 'test.ping')
    Check 'dry run ping' ($r.Code -eq 0 -and $r.Out.Contains("sudo -n salt --out=json --no-color --static -t 45 'web01' 'test.ping'") -and $r.Out.Contains('nothing was sent'))
    Check 'dry run never shows password' (-not $r.Out.Contains('FAKE-PASSWORD-123'))
    $r = Run @('-Action', 'Salt', '-Target', 'web01', '-Fn', 'state.apply')
    Check 'script refuses state.apply' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    $r = Run @('-Action', 'Salt', '-Target', 'web*', '-Fn', 'test.ping')
    Check 'script refuses glob target' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    $r = Run @('-Action', 'Run', '-Target', 'web01', '-Cmd', 'rm -rf /')
    Check 'script refuses rm' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    $r = Run @('-Action', 'LogTail', '-Target', 'web01', '-Path', 'C:\ProgramData\Unravis\a.log', '-Os', 'windows')
    Check 'dry run logtail' ($r.Code -eq 0 -and $r.Out.Contains('Get-Content -LiteralPath') -and $r.Out.Contains('shell=powershell'))
    $r = Run @('-Action', 'LogTail', '-Target', 'web01', '-Path', 'C:\Windows\win.ini', '-Os', 'windows')
    Check 'script refuses path outside' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    $r = Run @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', 'service.restart', '-Name', 'Unravis')
    Check 'write request dry run saves nothing' ($r.Code -eq 0 -and $r.Out.Contains('nothing saved'))
    $r = RunLive @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', 'service.restart', '-Name', 'Unravis')
    Check 'write request is not run' ($r.Code -eq 0 -and $r.Out.Contains('NOT RUN'))
    $hash = [regex]::Match($r.Out, '-Hash ([0-9a-f]{12})').Groups[1].Value
    $r = Run @('-Action', 'WriteApprove', '-Hash', $hash)
    Check 'write approve needs approval note' ($r.Code -ne 0 -and $r.Out.Contains('ApprovalNote is required'))
    $r = Run @('-Action', 'WriteApprove', '-Hash', $hash, '-ApprovalNote', 'test go')
    Check 'write approve dry run' ($r.Code -eq 0 -and $r.Out.Contains('CHANGING THE SERVER'))
    $pf = Join-Path (Join-Path $priv 'pending') "$hash.json"
    $orig = Get-Content -LiteralPath $pf -Raw
    [System.IO.File]::WriteAllText($pf, $orig.Replace('Unravis', 'Other'))
    $r = Run @('-Action', 'WriteApprove', '-Hash', $hash, '-ApprovalNote', 'test go')
    Check 'write approve refuses an edited request' ($r.Code -ne 0 -and $r.Out.Contains('does not match'))
    Check 'edited request is removed' (-not (Test-Path -LiteralPath $pf))
    $r = Run @('-Action', 'BlackboxShow', '-Target', 'web01')
    Check 'dry run blackbox show' ($r.Code -eq 0 -and $r.Out.Contains('config show --json') -and $r.Out.Contains('shell=powershell') -and $r.Out.Contains('nothing was sent'))
    $r = Run @('-Action', 'BlackboxShow', '-Target', 'web99')
    Check 'blackbox show refuses a target not listed' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    $r = RunLive @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', 'blackbox.collector', '-Name', 'redis.info', '-FnArgs', 'on,every=60')
    Check 'blackbox write request is not run' ($r.Code -eq 0 -and $r.Out.Contains('NOT RUN') -and $r.Out.Contains("config set 'redis.info' on --every 60"))
    $bhash = [regex]::Match($r.Out, '-Hash ([0-9a-f]{12})').Groups[1].Value
    $r = Run @('-Action', 'WriteApprove', '-Hash', $bhash, '-ApprovalNote', 'test go')
    Check 'blackbox write approve dry run' ($r.Code -eq 0 -and $r.Out.Contains('CHANGING THE SERVER') -and $r.Out.Contains('config set') -and $r.Out.Contains('-t 170'))
    $bpf = Join-Path (Join-Path $priv 'pending') "$bhash.json"
    $borig = Get-Content -LiteralPath $bpf -Raw
    [System.IO.File]::WriteAllText($bpf, $borig.Replace('"on"', '"off"'))
    $r = Run @('-Action', 'WriteApprove', '-Hash', $bhash, '-ApprovalNote', 'test go')
    Check 'blackbox approve refuses edited args' ($r.Code -ne 0 -and $r.Out.Contains('does not match'))
    $r = Run @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', 'blackbox.collector', '-Name', 'redis.info; calc', '-FnArgs', 'on')
    Check 'blackbox write refuses a bad id' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))
    foreach ($case in @(@('blackbox.counter.add', '\Memory\Available MBytes', 'config set counters.extra add'), @('blackbox.counter.remove', '\Memory\Available MBytes', 'config set counters.extra remove'), @('blackbox.rollback', 'last', 'config rollback'))) {
        $r = RunLive @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', $case[0], '-Name', $case[1])
        Check ("blackbox request " + $case[0]) ($r.Code -eq 0 -and $r.Out.Contains('NOT RUN') -and $r.Out.Contains($case[2]))
        $ch = [regex]::Match($r.Out, '-Hash ([0-9a-f]{12})').Groups[1].Value
        $r = Run @('-Action', 'WriteApprove', '-Hash', $ch, '-ApprovalNote', 'test go')
        Check ("blackbox approve dry run " + $case[0]) ($r.Code -eq 0 -and $r.Out.Contains('CHANGING THE SERVER') -and $r.Out.Contains('--retcode-passthrough'))
    }
    # A request saved before the Blackbox verbs (no args field) still verifies.
    $oldCreated = (Get-Date).ToUniversalTime().ToString('o')
    $oldH = Get-WriteHash 'web01' 'service.restart' 'Unravis' '' $oldCreated
    $oldJson = '{ "target": "web01", "fn": "service.restart", "name": "Unravis", "created": "' + $oldCreated + '" }'
    [System.IO.File]::WriteAllText((Join-Path (Join-Path $priv 'pending') "$oldH.json"), $oldJson)
    $r = Run @('-Action', 'WriteApprove', '-Hash', $oldH, '-ApprovalNote', 'test go')
    Check 'an old request without args still approves' ($r.Code -eq 0 -and $r.Out.Contains('CHANGING THE SERVER'))
    $r = Run @('-Action', 'LogPull', '-Target', 'web01', '-Path', 'C:\ProgramData\Unravis\snapshots\case-1.zip')
    Check 'dry run pull' ($r.Code -eq 0 -and $r.Out.Contains('Would copy') -and $r.Out.Contains('nothing was sent'))
    $r = Run @('-Action', 'LogPull', '-Target', 'web01', '-Path', 'C:\ProgramData\Unravis\bb-20261126-11.ndjson')
    Check 'pull refuses a recording' ($r.Code -ne 0 -and $r.Out.Contains('REFUSED'))

    Set-Content -LiteralPath $envFile -Value ($testSettings + 'REMOTE_MODE=readonly')
    $r = Run @('-Action', 'UpdateStatus', '-Target', $updateTarget)
    Check 'update status dry run in readonly' ($r.Code -eq 0 -and $r.Out.Contains('READ-ONLY') -and $r.Out.Contains('nothing was sent'))
    $r = Run @('-Action', 'WriteRequest', '-Target', $updateTarget, '-Fn', 'update.fetch', '-Name', $tag)
    Check 'readonly refuses update requests' ($r.Code -ne 0 -and $r.Out.Contains('REMOTE_MODE is readonly'))
    Set-Content -LiteralPath $envFile -Value ($testSettings + 'REMOTE_MODE=write')
    $r = Run @('-Action', 'WriteRequest', '-Target', 'web01', '-Fn', 'update.fetch', '-Name', $tag)
    Check 'update request refuses another allowlisted minion' ($r.Code -ne 0 -and $r.Out.Contains('only available'))
    $r = Run @('-Action', 'WriteRequest', '-Target', $updateTarget, '-Fn', 'update.fetch', '-Name', $tag, '-FnArgs', 'extra')
    Check 'update request refuses extra arguments' ($r.Code -ne 0 -and $r.Out.Contains('no -FnArgs'))
    $updateRequest = @('-Action', 'WriteRequest', '-Target', $updateTarget, '-Fn', 'update.fetch', '-Name', $tag)
    $r = RunLive $updateRequest
    $updateHash = [regex]::Match($r.Out, '-Hash ([0-9a-f]{12})').Groups[1].Value
    Check 'update request saves an approval hash without SSH' ($r.Code -eq 0 -and $r.Out.Contains('NOT RUN') -and [bool]$updateHash)
    $updatePending = Join-Path $priv "pending\$updateHash.json"
    $updateRaw = Get-Content -LiteralPath $updatePending -Raw
    $updateCreated = [regex]::Match($updateRaw, '"created"\s*:\s*"([^"]+)"').Groups[1].Value
    Check 'update hash binds target verb and tag' (
        $updateHash -eq (Get-WriteHash $updateTarget 'update.fetch' $tag '' $updateCreated) -and
        $updateHash -ne (Get-WriteHash 'web01' 'update.fetch' $tag '' $updateCreated) -and
        $updateHash -ne (Get-WriteHash $updateTarget 'update.cleanup' $tag '' $updateCreated) -and
        $updateHash -ne (Get-WriteHash $updateTarget 'update.fetch' 'v0.1.0-rc.69' '' $updateCreated)
    )
    $approve = @('-Action', 'WriteApprove', '-Hash', $updateHash, '-ApprovalNote', 'test go FAKE-PASSWORD-123')
    $r = Run $approve
    Check 'update approval previews the fixed command and hides the password' (
        $r.Code -eq 0 -and $r.Out.Contains("update.fetch '$tag'") -and $r.Out.Contains("'cmd.run' '`$t=") -and
        -not $r.Out.Contains('FAKE-PASSWORD-123')
    )
    Set-Content -LiteralPath $envFile -Value ($testSettings + 'REMOTE_MODE=readonly')
    $r = Run $approve
    Check 'approval rechecks write mode' ($r.Code -ne 0 -and $r.Out.Contains('REMOTE_MODE is readonly'))
    Set-Content -LiteralPath $envFile -Value ($testSettings + 'REMOTE_MODE=write')
    [IO.File]::WriteAllText($updatePending, $updateRaw.Replace($tag, 'v0.1.0-rc.69'))
    $r = Run $approve
    Check 'approval rejects an edited update tag' ($r.Code -ne 0 -and $r.Out.Contains('does not match'))

    $expired = (Get-Date).ToUniversalTime().AddMinutes(-61).ToString('o')
    $expiredHash = Get-WriteHash $updateTarget 'update.fetch' $tag '' $expired
    [IO.File]::WriteAllText((Join-Path $priv "pending\$expiredHash.json"),
        (@{ target = $updateTarget; fn = 'update.fetch'; name = $tag; created = $expired } | ConvertTo-Json))
    $r = Run @('-Action', 'WriteApprove', '-Hash', $expiredHash, '-ApprovalNote', 'test go')
    Check 'update approval expires after 60 minutes' ($r.Code -ne 0 -and $r.Out.Contains('older than 60 minutes'))

    # Copy the entry point into the disposable fixture and replace only its SSH boundary.
    $r = RunLive $updateRequest
    $updateHash = [regex]::Match($r.Out, '-Hash ([0-9a-f]{12})').Groups[1].Value
    $testEntry = Join-Path $tmp 'TestEntry.ps1'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'RemoteSalt.Lib.ps1') -Destination $tmp
    $entry = Get-Content -LiteralPath $script1 -Raw
    $dotSource = '. (Join-Path $PSScriptRoot ''RemoteSalt.Lib.ps1'')'
    if (-not $entry.Contains($dotSource)) { throw 'Cannot install the fake SSH boundary.' }
    $fakeSsh = @'
function Invoke-RemoteSsh($Config, [string]$RemoteCommand, [int]$TimeoutSec) {
    $latest = Get-Content -LiteralPath (Join-Path (Get-RemoteHome) 'audit.log') -Tail 1 | ConvertFrom-Json
    if ($latest.decision -ne 'starting' -or $latest.command -notlike 'update.fetch*') { throw 'Missing update start audit.' }
    if ($RemoteCommand -notmatch "'cmd\.run' '\`$t=" -or $TimeoutSec -lt 630) { throw 'Unexpected fake update call.' }
    if (Test-Path -LiteralPath (Join-Path (Get-RemoteHome) "pending\$Hash.json")) { throw 'Approval was not consumed before running.' }
    return @{ Exit = 0; TimedOut = $false; Out = '{"BG-SRV-UK-01":"fake update ran UPDATE-OK"}'; Err = '' }
}
'@
    Set-Content -LiteralPath $testEntry -Value ($entry.Replace($dotSource, $dotSource + "`n" + $fakeSsh))
    Set-Content -LiteralPath (Join-Path $priv 'known_hosts') -Value 'offline fixture'
    $actualEntry = $script1
    $script1 = $testEntry
    $approve = @('-Action', 'WriteApprove', '-Hash', $updateHash, '-ApprovalNote', 'test go')
    try {
        $r = RunLive $approve
        Check 'approved update runs once through fake SSH' ($r.Code -eq 0 -and $r.Out.Contains('fake update ran'))
        $r = RunLive $approve
        Check 'used update approval cannot run again' ($r.Code -ne 0 -and $r.Out.Contains('no pending request'))
    } finally { $script1 = $actualEntry }
    $updateAudit = @(Get-Content -LiteralPath (Join-Path $priv 'audit.log') | ForEach-Object { $_ | ConvertFrom-Json } |
        Where-Object { $_.action -eq 'WriteApprove' -and $_.command -eq "update.fetch '$tag'" })
    Check 'update approval audits start and outcome in order' (($updateAudit.decision -join ',') -eq 'starting,ran' -and $updateAudit[1].exit -eq 0)

    $audit = Get-Content -LiteralPath (Join-Path $priv 'audit.log') -Raw
    Check 'audit records denials and runs' ($audit -match '"decision":"denied"' -and $audit -match '"decision":"dryrun"')
    Check 'audit never has the password' ($audit -notmatch 'FAKE-PASSWORD-123')
    # settings readable by everyone are refused
    & icacls.exe (Join-Path $priv '.env') /grant '*S-1-5-32-545:R' | Out-Null
    $r = Run @('-Action', 'Check')
    Check 'script refuses an openly readable settings file' ($r.Code -ne 0 -and $r.Out.Contains('others can access'))
}
finally {
    $env:UNRAVIS_REMOTE_HOME = $originalRemoteHome
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host ("{0} passed, {1} failed" -f $script:pass, $script:fail)
if ($script:fail -gt 0) { exit 1 }
exit 0   # a refused child run above leaves a non-zero $LASTEXITCODE, which pwsh -command would otherwise return
