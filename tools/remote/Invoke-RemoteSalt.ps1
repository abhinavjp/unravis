<#
.SYNOPSIS
  Read-only access to a Salt master over SSH, for debugging Unravis / Blackbox on a remote server.
.DESCRIPTION
  Reads %USERPROFILE%\.unravis-remote\.env (see docs/ops/REMOTE-SALT.md), connects with the built-in OpenSSH client,
  and runs ONLY allow-listed read-only Salt calls. Every call is written to a capped audit log (no secrets).
  Anything that changes the server is refused unless REMOTE_MODE=write and a human approved that exact command.
  Blackbox: BlackboxShow / BlackboxHistory / BlackboxStatus read the recorder's settings; LogPull copies one bundle zip.
  -DryRun prints what would be run and connects to nothing.
.EXAMPLE
  .\Invoke-RemoteSalt.ps1 -Action Check
  .\Invoke-RemoteSalt.ps1 -Action Salt -Target web01 -Fn test.ping
  .\Invoke-RemoteSalt.ps1 -Action LogTail -Target web01 -Path 'C:\ProgramData\Unravis\logs\unravis.log' -Lines 300
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Check', 'ShowHostKey', 'TrustHost', 'Keys', 'SaltRun', 'Salt', 'Run', 'LogTail', 'LogGrep', 'LogList', 'EventLog', 'Service', 'MasterTail', 'WriteRequest', 'WriteApprove', 'BlackboxShow', 'BlackboxHistory', 'BlackboxStatus', 'UpdateStatus', 'LogPull', 'Audit')][string]$Action,
    [string]$Target,
    [string]$Fn,
    [string[]]$FnArgs = @(),
    [string]$Cmd,
    [string]$Path,
    [int]$Lines = 200,
    [string]$Pattern,
    [string]$LogName,
    [string]$Name,
    [ValidateSet('auto', 'windows', 'linux')][string]$Os = 'auto',
    [string]$Hash,
    [string]$ApprovalNote,
    [int]$TimeoutSec = 60,
    [int]$MaxPrint = 60000,
    [ValidateRange(16, 1024)][int]$ChunkKB = 192,
    [ValidateRange(1, 2048)][int]$MaxMB = 512,
    [switch]$DryRun
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'RemoteSalt.Lib.ps1')

$script:Cfg = $null
$script:Secrets = @()

function Get-LoadedConfig {
    $envPath = Get-RemoteEnvPath
    if (-not (Test-Path -LiteralPath $envPath)) { throw "No settings file at $envPath. Run tools\remote\Initialize-RemoteEnv.ps1 first." }
    $loc = Get-LocationProblems $envPath
    if ($loc.Count -gt 0) { throw ('Refusing to read the settings file: ' + ($loc -join '; ')) }
    $a1 = Get-AclProblems (Get-RemoteHome); $a2 = Get-AclProblems $envPath
    if ($a1.Count + $a2.Count -gt 0) { throw ('Refusing to read the settings file, others can access it: ' + (($a1 + $a2) -join '; ') + '. Run tools\remote\Initialize-RemoteEnv.ps1 to fix the permissions.') }
    $cfg = Get-RemoteConfig (Read-EnvFile $envPath)
    $script:Secrets = @($cfg.Password) | Where-Object { $_ }
    return $cfg
}

function Get-SaltWait { return [Math]::Max(10, [Math]::Min($TimeoutSec - 15, 120)) }

function Deny([string]$Reason) {
    Write-Audit -Action $Action -Decision 'denied' -Reason $Reason -Target $Target -Command ("$Fn $Cmd $Path $Pattern $Name " + ($FnArgs -join ',')) -Secrets $script:Secrets
    throw "REFUSED (read-only gate): $Reason"
}

function Assert-KnownHost {
    if (-not (Test-Path -LiteralPath (Get-KnownHostsPath))) {
        throw "The server is not trusted yet. Run -Action ShowHostKey, confirm the fingerprint, put it in SSH_HOST_KEY_FINGERPRINT, then run -Action TrustHost."
    }
}

function Write-Plan([string]$Title, [string]$Remote) {
    Write-Host ("[{0}] READ-ONLY. {1}" -f $Action, $Title)
    if ($DryRun) {
        Write-Host ("  ssh " + ((New-SshArgs $script:Cfg $Remote) -join ' '))
        Write-Host '  (dry run: nothing was sent)'
    }
}

function Invoke-Planned {
    # Audits, runs (or dry-runs) one remote command, returns the result or $null on dry run.
    param([string]$Title, [string]$Remote, [string]$AuditNote = '', [switch]$NoAudit)
    Write-Plan $Title $Remote
    if ($DryRun) { Write-Audit -Action $Action -Decision 'dryrun' -Target $Target -Command $Remote -Secrets $script:Secrets; return $null }
    Assert-KnownHost
    $r = Invoke-RemoteSsh -Config $script:Cfg -RemoteCommand $Remote -TimeoutSec $TimeoutSec
    $note = if ($r.TimedOut) { 'timed out' } else { $AuditNote }
    if (-not $NoAudit) { Write-Audit -Action $Action -Decision 'ran' -Target $Target -Command $Remote -Note $note -ExitCode $r.Exit -Bytes $r.Out.Length -Secrets $script:Secrets }
    if ($r.TimedOut) { throw "Timed out after $TimeoutSec s. The command may still be running on the server; its state is unknown. Try a larger -TimeoutSec." }
    if ($r.Exit -ne 0) {
        $msg = $r.Err.Trim()
        $so = (Protect-Text $r.Out $script:Secrets).Trim()
        if ($so) { $msg = ($msg + "`n" + $(if ($so.Length -gt 1500) { $so.Substring(0, 1500) + ' ...' } else { $so })).Trim() }
        if ($msg -match 'Permission denied') { $msg += "`nHint: SSH sign-in failed (wrong user, password or key)." }
        elseif ($msg -match 'Host key verification failed|REMOTE HOST IDENTIFICATION') { $msg += "`nHint: the server's host key does not match the trusted one. Do not continue until you know why." }
        elseif ($msg -match 'sudo: a password is required|sudo: a terminal is required') { $msg += "`nHint: this user cannot run salt through 'sudo -n'. Set SALT_USE_SUDO=false if the user is root, or ask the server admin for passwordless sudo for salt." }
        elseif ($msg -match 'command not found|not found') { $msg += "`nHint: salt is not on the PATH of this SSH user." }
        throw ("Remote command failed (exit {0}): {1}" -f $r.Exit, $msg)
    }
    return $r
}

function Show-Text([string]$Text, [string]$Label, [int]$Last = 0) {
    $t = Protect-Text $Text $script:Secrets
    if ($Last -gt 0) { $ls = $t -split "`r?`n"; if ($ls.Count -gt $Last) { $t = ($ls | Select-Object -Last $Last) -join "`n" } }
    $dir = Join-Path (Get-RemoteHome) 'pulled'
    if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    $file = Join-Path $dir ("{0}-{1}-{2}.txt" -f (Get-Date -Format 'yyyyMMdd-HHmmss'), $Action, ($Label -replace '[^A-Za-z0-9._-]', '_'))
    [System.IO.File]::WriteAllText($file, $t, (New-Object System.Text.UTF8Encoding($false)))
    Get-ChildItem -LiteralPath $dir -File | Sort-Object LastWriteTime -Descending | Select-Object -Skip 20 | Remove-Item -Force -ErrorAction SilentlyContinue
    if ($t.Length -gt $MaxPrint) { Write-Host $t.Substring($t.Length - $MaxPrint); Write-Host "[showing the last $MaxPrint characters; full text saved]" } else { Write-Host $t }
    Write-Host "[saved: $file]"
}

function Show-SaltJson([string]$Json, [int]$Last = 0) {
    $obj = $null
    try { $obj = $Json | ConvertFrom-Json } catch { Show-Text $Json 'raw'; return }
    if ($null -eq $obj) { Show-Text '(the master returned no output)' 'empty'; return }
    if ($obj -isnot [pscustomobject]) { Show-Text ([string]$Json) 'raw'; return }
    foreach ($p in $obj.PSObject.Properties) {
        Write-Host "== $($p.Name) =="
        if ($p.Value -is [string]) { Show-Text $p.Value $p.Name $Last }
        else { Show-Text (ConvertTo-Json -InputObject $p.Value -Depth 8) $p.Name }
    }
}

function Get-MinionOs {
    if ($Os -ne 'auto') { return $Os }
    if ($DryRun) { return 'windows' }
    Assert-KnownHost
    $remote = New-SaltRemoteCommand 'salt' @($Target, 'grains.item', 'kernel') $script:Cfg 30
    $r = Invoke-RemoteSsh -Config $script:Cfg -RemoteCommand $remote -TimeoutSec 45
    if ($r.TimedOut) { throw "Timed out asking $Target for its operating system. Pass -Os windows or -Os linux." }
    if ($r.Exit -ne 0) { throw ("Could not ask the master about $Target (exit {0}): {1} {2}" -f $r.Exit, $r.Err.Trim(), (Protect-Text $r.Out $script:Secrets).Trim()) }
    try { $o = $r.Out | ConvertFrom-Json; $k = [string]($o.PSObject.Properties | Select-Object -First 1).Value.kernel } catch { $k = '' }
    Write-Audit -Action 'DetectOs' -Decision 'ran' -Target $Target -Command $remote -ExitCode $r.Exit -Note $k -Secrets $script:Secrets
    if ($k -match '(?i)windows') { return 'windows' }
    if ($k -match '(?i)linux|darwin|bsd') { return 'linux' }
    throw "Could not tell the minion's operating system (kernel='$k'). Is '$Target' answering? Pass -Os windows or -Os linux."
}

function Assert-Target { $e = Test-SaltTarget $Target $script:Cfg; if ($e) { Deny $e } }
function Assert-UpdateTarget {
    if ($Target -ne $script:UpdateTarget) { Deny "Unravis update actions are only available for $script:UpdateTarget" }
}

function Invoke-ReadRecipe([string]$Recipe) {
    Assert-Target
    $osName = Get-MinionOs
    try { $rec = New-ReadRecipe -Recipe $Recipe -Os $osName -Path $Path -Lines $Lines -Pattern $Pattern -LogName $LogName -Config $script:Cfg } catch { Deny $_.Exception.Message }
    $words = @($Target, 'cmd.run', $rec.Cmd) + $rec.Kwargs
    $remote = New-SaltRemoteCommand 'salt' $words $script:Cfg (Get-SaltWait)
    $r = Invoke-Planned "$Recipe on $Target ($osName)" $remote
    if ($r) { $last = if ($rec.ContainsKey('LastLines')) { $rec.LastLines } else { 0 }; Show-SaltJson $r.Out $last }
}

function Invoke-BlackboxRead([string]$Verb) {
    Assert-Target
    $rec = New-BlackboxCommand -Fn $Verb
    # No -Retcode here: a read only shows what the recorder printed, and the master's salt (28 Nov, BG-SRV-UK-01) refuses --retcode-passthrough.
    $remote = New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $rec.Cmd) + $rec.Kwargs) $script:Cfg (Get-SaltWait)
    $r = Invoke-Planned "$($rec.Text) on $Target" $remote
    if ($r) { Show-SaltJson $r.Out }
}

switch ($Action) {
    'Audit' {
        $f = Join-Path (Get-RemoteHome) 'audit.log'
        if (Test-Path -LiteralPath $f) { Get-Content -LiteralPath $f -Tail ([Math]::Min([Math]::Max($Lines, 1), 500)) } else { Write-Host 'No audit log yet.' }
        return
    }
}

$script:Cfg = Get-LoadedConfig

switch ($Action) {
    'Check' {
        $c = $script:Cfg
        $auth = if ($c.KeyPath) { 'key file' } else { 'password' }
        Write-Host ("Settings OK: {0}@{1}:{2}, sign-in by {3}, sudo={4}, mode={5}, targets={6}, log folders={7}" -f $c.User, $c.Host, $c.Port, $auth, $c.UseSudo, $c.Mode, $c.Targets.Count, $c.LogPaths.Count)
        Write-Host ("Pinned host fingerprint: {0}. Trusted host file: {1}." -f ($(if ($c.Fingerprint) { 'yes' } else { 'NO' })), ($(if (Test-Path -LiteralPath (Get-KnownHostsPath)) { 'yes' } else { 'NO' })))
        if ($DryRun) { Write-Host 'Dry run: no connection made.'; return }
        Assert-KnownHost
        $r = Invoke-RemoteSsh -Config $c -RemoteCommand 'echo unravis-remote-ok' -TimeoutSec 30
        Write-Audit -Action 'Check' -Decision 'ran' -Command 'echo unravis-remote-ok' -ExitCode $r.Exit -Secrets $script:Secrets
        if ($r.Exit -ne 0) { throw ("SSH failed: " + $r.Err.Trim()) }
        Write-Host 'SSH sign-in: OK'
        $Action = 'Keys'
        $remote = New-SaltRemoteCommand 'salt-key' @('-L') $c 30
        try { $k = Invoke-Planned 'Salt keys' $remote; Show-SaltJson $k.Out } catch { Write-Host "Salt keys: $($_.Exception.Message)" }
        foreach ($t in $c.Targets) {
            $script:Target = $t; $Target = $t; $Action = 'Salt'
            $remote = New-SaltRemoteCommand 'salt' @($t, 'test.ping') $c 30
            try { $p = Invoke-Planned "test.ping $t" $remote; Show-SaltJson $p.Out } catch { Write-Host "test.ping ${t}: $($_.Exception.Message)" }
        }
    }
    'ShowHostKey' {
        if ($DryRun) { Write-Host '(dry run: would read the server host key without logging in; nothing sent)'; return }
        $keys = @(Get-ServerHostKeys $script:Cfg)
        $tmp = Join-Path (Get-RemoteHome) 'scan.tmp'
        foreach ($k in $keys) { [System.IO.File]::WriteAllText($tmp, $k + "`n"); $fp = Invoke-Process -File (Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe') -Arguments @('-l', '-f', $tmp) -TimeoutSec 15; Write-Host $fp.Out.Trim() }
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        Write-Host 'Compare one of these SHA256 values with the server admin, then paste it into SSH_HOST_KEY_FINGERPRINT and run -Action TrustHost.'
        Write-Audit -Action 'ShowHostKey' -Decision 'ran' -Target $script:Cfg.Host
    }
    'TrustHost' {
        if (-not $script:Cfg.Fingerprint) { throw 'SSH_HOST_KEY_FINGERPRINT is empty. Run -Action ShowHostKey, confirm the fingerprint, put it in the settings file.' }
        if ($DryRun) { Write-Host '(dry run: would read the server host key and write known_hosts only if it matches the pin; nothing sent)'; return }
        $keys = @(Get-ServerHostKeys $script:Cfg)
        $tmp = Join-Path (Get-RemoteHome) 'scan.tmp'
        $match = New-Object System.Collections.Generic.List[string]
        foreach ($k in $keys) {
            [System.IO.File]::WriteAllText($tmp, $k + "`n")
            $fp = Invoke-Process -File (Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe') -Arguments @('-l', '-f', $tmp) -TimeoutSec 15
            if ($fp.Out -match [regex]::Escape($script:Cfg.Fingerprint)) { $match.Add($k) }
        }
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        if ($match.Count -eq 0) { Write-Audit -Action 'TrustHost' -Decision 'denied' -Reason 'fingerprint mismatch' -Target $script:Cfg.Host; throw 'The server did not present a key with that fingerprint. NOT trusting it. Do not continue until you know why.' }
        [System.IO.File]::WriteAllLines((Get-KnownHostsPath), $match.ToArray())
        Write-Audit -Action 'TrustHost' -Decision 'ran' -Target $script:Cfg.Host -Note 'known_hosts written'
        Write-Host "Trusted: $($match.Count) key(s) with the pinned fingerprint written to $(Get-KnownHostsPath)."
    }
    'Keys' {
        $remote = New-SaltRemoteCommand 'salt-key' @('-L') $script:Cfg 30
        $r = Invoke-Planned 'List Salt minion keys' $remote
        if ($r) { Show-SaltJson $r.Out }
    }
    'SaltRun' {
        $e = Test-SaltRunCall $Fn; if ($e) { Deny $e }
        $remote = New-SaltRemoteCommand 'salt-run' @($Fn) $script:Cfg 30
        $r = Invoke-Planned "salt-run $Fn" $remote
        if ($r) { Show-Text $r.Out "saltrun-$Fn" }
    }
    'Salt' {
        Assert-Target
        $e = Test-SaltReadCall $Fn $FnArgs $script:Cfg; if ($e) { Deny $e }
        $remote = New-SaltRemoteCommand 'salt' (@($Target, $Fn) + $FnArgs) $script:Cfg (Get-SaltWait)
        $r = Invoke-Planned "$Fn on $Target" $remote
        if ($r) { Show-SaltJson $r.Out }
    }
    'Run' {
        Assert-Target
        $e = Test-ReadOnlyCommand $Cmd $script:Cfg; if ($e) { Deny $e }
        $osName = Get-MinionOs
        $firstWord = ($Cmd.Trim() -split '\s+')[0].ToLowerInvariant()
        $okFirst = if ($osName -eq 'windows') { @('get-content', 'type', 'dir', 'get-childitem') } else { @('tail', 'head', 'cat', 'ls') }
        if ($okFirst -notcontains $firstWord) { Deny "'$firstWord' does not run on a $osName minion; use one of: $($okFirst -join ', ')" }
        $kw = if ($osName -eq 'windows') { @('shell=powershell') } else { @('python_shell=False') }
        $remote = New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $Cmd) + $kw) $script:Cfg (Get-SaltWait)
        $r = Invoke-Planned "cmd.run on $Target ($osName): $Cmd" $remote
        if ($r) { Show-SaltJson $r.Out }
    }
    'BlackboxShow' { Invoke-BlackboxRead 'blackbox.show' }
    'BlackboxHistory' { Invoke-BlackboxRead 'blackbox.history' }
    'BlackboxStatus' { Invoke-BlackboxRead 'blackbox.status' }
    'UpdateStatus' {
        Assert-Target
        Assert-UpdateTarget
        try { $rec = New-UpdateStatusCommand $script:Cfg } catch { Deny $_.Exception.Message }
        $remote = New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $rec.Cmd) + $rec.Kwargs) $script:Cfg (Get-SaltWait)
        $r = Invoke-Planned "Read Unravis update status on $Target" $remote
        if ($r) { Show-SaltJson $r.Out }
    }
    'LogTail' { Invoke-ReadRecipe 'LogTail' }
    'LogGrep' { Invoke-ReadRecipe 'LogGrep' }
    'LogList' { Invoke-ReadRecipe 'LogList' }
    'EventLog' { Invoke-ReadRecipe 'EventLog' }
    'Service' {
        $Fn = 'service.status'; $FnArgs = @($Name); Assert-Target
        $e = Test-SaltReadCall $Fn $FnArgs $script:Cfg; if ($e) { Deny $e }
        $remote = New-SaltRemoteCommand 'salt' @($Target, $Fn, $Name) $script:Cfg 30
        $r = Invoke-Planned "service.status $Name on $Target" $remote
        if ($r) { Show-SaltJson $r.Out }
    }
    'MasterTail' {
        if ($Lines -lt 1 -or $Lines -gt 5000) { Deny 'Lines must be 1 to 5000' }
        if (-not (Test-PathAllowed $Path $script:Cfg.LogPaths) -or $Path -match '[*?]') { Deny "path '$Path' is outside REMOTE_LOG_PATHS (or REMOTE_LOG_PATHS is empty)" }
        $remote = "tail -n $Lines " + (ConvertTo-ShSingleQuoted $Path)
        $r = Invoke-Planned "tail the master's $Path" $remote
        if ($r) { Show-Text $r.Out 'master' }
    }
    'WriteRequest' {
        Assert-Target
        $isBb = $Fn -like 'blackbox.*'
        # Started with -File, PowerShell hands 'on,every=60' over as one string, so split on commas.
        $wargs = @()
        if ($isBb) { $wargs = @($FnArgs | ForEach-Object { $_ -split ',' } | Where-Object { $_ -ne '' }) }
        $e = if ($isBb) { Test-BlackboxWrite $Fn $Name $wargs $script:Cfg } else { Test-WriteCall $Fn $Name $script:Cfg }
        if ($e) { Deny $e }
        if ($Fn -in $script:UpdateFns) {
            Assert-UpdateTarget
            if ($FnArgs.Count) { Deny 'Update actions take a release tag in -Name and no -FnArgs' }
        }
        $shown = if ($isBb) { (New-BlackboxCommand -Fn $Fn -Name $Name -WriteArgs $wargs).Text } else { "$Fn $Name" }
        if ($DryRun) { Write-Host "(dry run: would record a change request for $shown on $Target; nothing saved, nothing sent)"; return }
        $created = (Get-Date).ToUniversalTime().ToString('o')
        $h = Get-WriteHash $Target $Fn $Name ($wargs -join "`n") $created
        $dir = Join-Path (Get-RemoteHome) 'pending'
        if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
        @{ target = $Target; fn = $Fn; name = $Name; args = @($wargs); created = $created } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $dir "$h.json") -Encoding UTF8
        Write-Audit -Action 'WriteRequest' -Decision 'pending' -Target $Target -Command $shown -Note "hash $h" -Secrets $script:Secrets
        Write-Host "CHANGE REQUESTED, NOT RUN: $shown on minion '$Target'."
        Write-Host "Show this to nachi in the thread. Run it only after he says go (this gate relies on the operator; it is not a technical lock): -Action WriteApprove -Hash $h -ApprovalNote '<his message id>'. Valid for 60 minutes."
    }
    'WriteApprove' {
        if ($Hash -notmatch '^[0-9a-f]{12}$') { Deny 'Hash must be the 12-character code printed by WriteRequest' }
        if ([string]::IsNullOrWhiteSpace($ApprovalNote)) { Deny 'ApprovalNote is required: quote the thread message where nachi said go' }
        $f = Join-Path (Join-Path (Get-RemoteHome) 'pending') "$Hash.json"
        if (-not (Test-Path -LiteralPath $f)) { Deny 'no pending request with that hash' }
        $raw = Get-Content -LiteralPath $f -Raw
        $p = $raw | ConvertFrom-Json
        $createdRaw = [regex]::Match($raw, '"created"\s*:\s*"([^"]+)"').Groups[1].Value   # PowerShell 7 turns the date into a DateTime; keep the exact text
        if (-not $createdRaw) { Remove-Item -LiteralPath $f -Force; Deny 'the saved request is damaged; make a new one' }
        $age = (Get-Date).ToUniversalTime() - [datetime]::Parse($createdRaw, [System.Globalization.CultureInfo]::InvariantCulture, [System.Globalization.DateTimeStyles]::RoundtripKind)
        if ($age.TotalMinutes -gt 60 -or $age.TotalSeconds -lt -60) { Remove-Item -LiteralPath $f -Force; Deny 'the request is older than 60 minutes (or dated in the future); make a new one' }
        $pargs = @()
        if ($p.PSObject.Properties['args']) { $pargs = @($p.args | Where-Object { $null -ne $_ }) }   # a request made before the Blackbox verbs has no args
        $h2 = Get-WriteHash $p.target $p.fn $p.name ($pargs -join "`n") $createdRaw
        if ($h2 -ne $Hash) { Remove-Item -LiteralPath $f -Force; Deny 'the saved request does not match its hash (it was edited); make a new one' }
        $Target = $p.target; $Fn = $p.fn; $Name = $p.name
        Assert-Target
        $isBb = $Fn -like 'blackbox.*'
        $isUpdate = $Fn -in $script:UpdateFns
        $chunkRemotes = @()
        if ($isBb) {
            $e = Test-BlackboxWrite $Fn $Name $pargs $script:Cfg; if ($e) { Deny $e }
            $rec = New-BlackboxCommand -Fn $Fn -Name $Name -WriteArgs $pargs
            # The recorder restarts and watches itself for 60 s, so allow the master and the SSH call 3 minutes.
            $remote = New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $rec.Cmd) + $rec.Kwargs) $script:Cfg 170 -Retcode
            $shown = $rec.Text; $approveSec = [Math]::Max($TimeoutSec, 200)
        } else {
            $e = Test-WriteCall $Fn $Name $script:Cfg; if ($e) { Deny $e }
            $shown = "$Fn '$Name'"
            if ($isUpdate) {
                Assert-UpdateTarget
                if ($pargs.Count) { Deny 'Update actions take a release tag in -Name and no extra arguments' }
                $rec = New-UpdateCommand $Fn $Name
                $wait = if ($Fn -eq 'update.fetch') { [Math]::Max($TimeoutSec - 30, 600) } else { [Math]::Max($TimeoutSec - 30, 170) }
                $approveSec = $wait + 30
                $sendCmd = if ($rec.Chunks.Count) { $rec.Run } else { $rec.Cmd }
                $remote = New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $sendCmd) + $rec.Kwargs) $script:Cfg $wait
                $chunkRemotes = @($rec.Chunks | ForEach-Object { New-SaltRemoteCommand 'salt' (@($Target, 'cmd.run', $_) + $rec.Kwargs) $script:Cfg 60 })
            } else {
                $remote = New-SaltRemoteCommand 'salt' @($Target, $Fn, $Name) $script:Cfg 60
                $approveSec = [Math]::Max($TimeoutSec, 90)
            }
        }
        Write-Host ("[WriteApprove] CHANGING THE SERVER: {0} on {1}. Approval: {2}" -f $shown, $Target, (Protect-Text $ApprovalNote $script:Secrets))
        if ($DryRun) { Write-Host ("  ssh " + ((New-SshArgs $script:Cfg $remote) -join ' ')); Write-Host '  (dry run: nothing was sent)'; return }
        Assert-KnownHost
        # Consume the request first (rename is atomic), so two approvals cannot both run, and log the attempt before it starts.
        try { Rename-Item -LiteralPath $f -NewName "$Hash.running" -ErrorAction Stop } catch { Deny 'that request was already used' }
        $auditCommand = if ($isUpdate) { $shown } else { $remote }
        Write-Audit -Action 'WriteApprove' -Decision 'starting' -Target $Target -Command $auditCommand -Note "approved: $ApprovalNote" -Secrets $script:Secrets
        try {
            $chunkNumber = 0
            foreach ($piece in @($chunkRemotes)) {
                $chunkNumber++
                $c = Invoke-RemoteSsh -Config $script:Cfg -RemoteCommand $piece -TimeoutSec 90
                if ($c.TimedOut -or $c.Exit -ne 0 -or $c.Out -notmatch ('CHUNK-OK ' + $chunkNumber + '/')) {
                    throw ("Sending the update script failed at piece $chunkNumber of $(@($chunkRemotes).Count); nothing was run. Output: " + (Protect-Text ($c.Out + ' ' + $c.Err) $script:Secrets).Trim())
                }
            }
            $r = Invoke-RemoteSsh -Config $script:Cfg -RemoteCommand $remote -TimeoutSec $approveSec
        } catch {
            Write-Audit -Action 'WriteApprove' -Decision 'failed' -Target $Target -Command $auditCommand -Note 'SSH invocation failed; state unknown' -Secrets $script:Secrets
            throw (Protect-Text $_.Exception.Message $script:Secrets)
        } finally {
            Remove-Item -LiteralPath (Join-Path (Split-Path -Parent $f) "$Hash.running") -Force -ErrorAction SilentlyContinue
        }
        Write-Audit -Action 'WriteApprove' -Decision 'ran' -Target $Target -Command $auditCommand -Note $(if ($r.TimedOut) { 'timed out; state unknown' } else { "approved: $ApprovalNote" }) -ExitCode $r.Exit -Bytes $r.Out.Length -Secrets $script:Secrets
        if ($r.TimedOut) { throw $(if ($isUpdate) { 'Timed out. The update state is unknown. Check with -Action UpdateStatus before asking again.' } elseif ($isBb) { 'Timed out. The change may or may not have happened; run -Action BlackboxShow and -Action BlackboxHistory before asking again.' } else { 'Timed out. The service change may or may not have happened; check with -Action Service before asking again.' }) }
        if ($r.Exit -ne 0) {
            $hint = if ($isBb) { " The change may be refused, rolled back by the recorder, or half done: run -Action BlackboxShow and -Action BlackboxHistory before asking again." } else { '' }
            throw ("Remote command failed (exit {0}): {1} {2}{3}" -f $r.Exit, $r.Err.Trim(), (Protect-Text $r.Out $script:Secrets).Trim(), $hint)
        }
        if ($isUpdate -and (Protect-Text $r.Out $script:Secrets) -notmatch 'UPDATE-OK') { throw ('The update step did not report success. Check with -Action UpdateStatus before asking again. Output: ' + (Protect-Text $r.Out $script:Secrets).Trim()) }
        Show-SaltJson $r.Out
    }
    'LogPull' {
        Assert-Target
        $e = Test-PullPath $Path $script:Cfg; if ($e) { Deny $e }
        $chunk = $ChunkKB * 1024
        $dir = Join-Path (Get-RemoteHome) 'bundles'
        $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
        if ($DryRun) {
            $c0 = New-PullChunkCommand $Path 0 $chunk $true
            $remote = New-SaltRemoteCommand 'salt' @($Target, 'cmd.run', $c0, 'shell=powershell') $script:Cfg (Get-SaltWait) -Retcode
            Write-Host "[LogPull] READ-ONLY. Would copy $Path from $Target in $ChunkKB KB pieces (at most $MaxMB MB) to $dir and check its SHA-256$(if ($Path -match '(?i)\.zip\z') { ', then fetch its manifest and compare' })."
            Write-Host ("  ssh " + ((New-SshArgs $script:Cfg $remote) -join ' ')); Write-Host '  (dry run: nothing was sent)'; return
        }
        Assert-KnownHost
        if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
        # One audit line to start and one at the end; the pieces themselves are not audited one by one (they would push every other record out of the capped log).
        Write-Audit -Action 'LogPull' -Decision 'starting' -Target $Target -Command "pull $Path" -Secrets $script:Secrets
        function Receive-Bundle([string]$RemotePath) {
            $leaf = ($RemotePath -split '\\')[-1]
            $dest = Join-Path $dir ("{0}-{1}-{2}" -f $Target, $stamp, $leaf)
            $out = [System.IO.File]::Open($dest, 'CreateNew', 'Write', 'None')
            $ok = $false
            try {
                $offset = [long]0; $size = [long]-1; $want = ''; $pieces = 0
                while ($size -lt 0 -or $offset -lt $size) {
                    $cmd = New-PullChunkCommand $RemotePath $offset $chunk ($pieces -eq 0)
                    $remote = New-SaltRemoteCommand 'salt' @($Target, 'cmd.run', $cmd, 'shell=powershell') $script:Cfg (Get-SaltWait) -Retcode
                    $r = Invoke-Planned "pull piece $($pieces + 1) of $leaf from $Target (offset $offset)" $remote '' -NoAudit
                    $val = ''
                    try { $obj = $r.Out | ConvertFrom-Json; $val = [string]($obj.PSObject.Properties | Select-Object -First 1).Value } catch { $val = '' }
                    try { $piece = ConvertFrom-PullPiece $val $offset ($pieces -eq 0) } catch {
                        $shownText = (Protect-Text $val $script:Secrets); if ($shownText.Length -gt 200) { $shownText = $shownText.Substring(0, 200) }
                        throw ("Piece $($pieces + 1): $($_.Exception.Message). The minion said: $shownText")
                    }
                    if ($pieces -eq 0) {
                        $size = $piece.Size; $want = $piece.Hash
                        if ($size -gt ([long]$MaxMB * 1MB)) { throw "The file is $([Math]::Round($size / 1MB, 1)) MB; the limit is $MaxMB MB (-MaxMB)." }
                        $free = (New-Object System.IO.DriveInfo ([System.IO.Path]::GetPathRoot($dest))).AvailableFreeSpace
                        if ($size * 2 -gt $free) { throw 'Not enough free space on this PC for that file.' }
                    } elseif ($piece.Size -ne $size) { throw 'The file changed size while it was being pulled. Make a new bundle and pull that.' }
                    if ($piece.Data.Length -eq 0 -and $offset -lt $size) { throw 'The minion returned an empty piece before the end of the file.' }
                    $out.Write($piece.Data, 0, $piece.Data.Length)
                    $offset += $piece.Data.Length; $pieces++
                    if ($pieces % 10 -eq 0) { Write-Host ("  {0} of {1} bytes" -f $offset, $size) }
                    if ($pieces -gt 5000) { throw 'Stopped after 5000 pieces. Use a bigger -ChunkKB or a smaller bundle.' }
                }
                $out.Flush(); $out.Dispose(); $out = $null
                $got = (Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant()
                if ($got -ne $want) { throw "The copy does not match the file on the minion (expected $want, got $got). The copy was deleted." }
                $ok = $true
                Write-Host "Pulled ${leaf}: $size bytes in $pieces pieces, SHA-256 matches the minion's own hash: $got"
                return @{ Path = $dest; Sha = $got; Bytes = $size; Pieces = $pieces }
            } finally {
                if ($null -ne $out) { $out.Dispose() }
                if (-not $ok) { Remove-Item -LiteralPath $dest -Force -ErrorAction SilentlyContinue }
            }
        }
        $note = ''
        try {
            $zip = Receive-Bundle $Path
            $note = "ok $($zip.Bytes) bytes in $($zip.Pieces) pieces sha256 $($zip.Sha)"
            Write-Host "Saved: $($zip.Path)"
            if ($Path -match '(?i)\.zip\z') {
                $manPath = $Path -replace '(?i)\.zip\z', '.manifest.json'
                $verdict = ''
                try {
                    $man = Receive-Bundle $manPath
                    $want = [string](Get-Content -LiteralPath $man.Path -Raw | ConvertFrom-Json).ZipSHA256
                    if ($want.ToLowerInvariant() -eq $zip.Sha) { $verdict = "matches the recorder's manifest (ZipSHA256)" }
                    else { $verdict = "DOES NOT match the recorder's manifest (manifest says '$want'). Do not trust this bundle: $($zip.Path)" }
                } catch { $verdict = "could not be compared with the manifest ($($_.Exception.Message)); pull the manifest by hand" }
                Write-Host "Bundle SHA-256 $verdict"
                $note += "; manifest check: $verdict"
            } else {
                Write-Host 'A manifest alone is data only; the zip it describes was not pulled.'
            }
            Write-Audit -Action 'LogPull' -Decision 'ran' -Target $Target -Command "pull $Path" -Note $note -Bytes ([int][Math]::Min([long]$zip.Bytes, 2000000000)) -Secrets $script:Secrets
        } catch {
            Write-Audit -Action 'LogPull' -Decision 'failed' -Target $Target -Command "pull $Path" -Reason $_.Exception.Message -Secrets $script:Secrets
            throw
        } finally {
            # Keep the newest 10 pulled files only.
            Get-ChildItem -LiteralPath $dir -File -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -Skip 10 | Remove-Item -Force -ErrorAction SilentlyContinue
        }
    }
}
