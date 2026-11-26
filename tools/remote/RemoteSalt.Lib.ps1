# Library for tools/remote/Invoke-RemoteSalt.ps1 (docs/ops/REMOTE-SALT.md). Dot-source it; it has no side effects.
# Everything that decides "is this allowed" lives here so Test-RemoteSalt.ps1 can prove it without a server.
# Works on Windows PowerShell 5.1 and PowerShell 7. Never prints, logs or returns a secret except through Get-RemoteConfig.
Set-StrictMode -Version 2

# ---------------------------------------------------------------- locations
function Get-RemoteHome {
    if ($env:UNRAVIS_REMOTE_HOME) { return $env:UNRAVIS_REMOTE_HOME }
    return (Join-Path $env:USERPROFILE '.unravis-remote')
}
function Get-RemoteEnvPath { return (Join-Path (Get-RemoteHome) '.env') }

# ---------------------------------------------------------------- file permissions (current user only)
function Get-CurrentSid { return [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value }

function Set-PrivateAcl {
    # Removes inherited rights and grants the current user (by SID, so naming never matters) full control. Nobody else.
    param([Parameter(Mandatory)][string]$Path)
    $sid = Get-CurrentSid
    $isDir = (Get-Item -LiteralPath $Path -Force).PSIsContainer
    $grant = if ($isDir) { "*${sid}:(OI)(CI)F" } else { "*${sid}:F" }
    $out = & icacls.exe $Path /inheritance:r /grant:r $grant 2>&1
    if ($LASTEXITCODE -ne 0) { throw "icacls failed for ${Path}: $out" }
}

function Get-AclProblems {
    # Returns a list of problems: any identity other than you, SYSTEM or Administrators with access to the path.
    param([Parameter(Mandatory)][string]$Path)
    $problems = New-Object System.Collections.Generic.List[string]
    $me = Get-CurrentSid
    $okSids = @($me, 'S-1-5-18', 'S-1-5-32-544')
    $acl = Get-Acl -LiteralPath $Path
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -ne 'Allow') { continue }
        $sid = $null
        try { $sid = $rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value } catch { $sid = [string]$rule.IdentityReference }
        if ($okSids -notcontains $sid) { $problems.Add("$Path is accessible to $($rule.IdentityReference)") }
    }
    return , $problems.ToArray()
}

function Get-LocationProblems {
    # The settings file must never sit inside a git repo or a synced folder.
    param([Parameter(Mandatory)][string]$Path)
    $problems = New-Object System.Collections.Generic.List[string]
    $full = [System.IO.Path]::GetFullPath($Path)
    if ($full -match '(?i)\\(OneDrive|Dropbox|Google Drive|iCloud Drive|Box Sync|Box)(\\|$| )') { $problems.Add("$full is inside a synced folder") }
    $dir = Split-Path -Parent $full
    while ($dir) {
        if (Test-Path -LiteralPath (Join-Path $dir '.git')) { $problems.Add("$full is inside the git repo at $dir"); break }
        $parent = Split-Path -Parent $dir
        if (-not $parent -or $parent -eq $dir) { break }
        $dir = $parent
    }
    return , $problems.ToArray()
}

# ---------------------------------------------------------------- .env parsing and validation
function Read-EnvFile {
    param([Parameter(Mandatory)][string]$Path)
    $map = @{}
    foreach ($raw in [System.IO.File]::ReadAllLines($Path)) {
        $line = $raw.Trim()
        if ($line -eq '' -or $line.StartsWith('#')) { continue }
        $i = $line.IndexOf('=')
        if ($i -lt 1) { throw "Bad line in the settings file (expected KEY=VALUE): a line has no '='." }
        $k = $line.Substring(0, $i).Trim().ToUpperInvariant()
        $v = $line.Substring($i + 1).Trim()
        if ($k -notmatch '^[A-Z][A-Z0-9_]*$') { throw "Bad key name in the settings file: '$k'." }
        if ($v.Length -ge 2 -and (($v[0] -eq '"' -and $v[$v.Length - 1] -eq '"') -or ($v[0] -eq "'" -and $v[$v.Length - 1] -eq "'"))) { $v = $v.Substring(1, $v.Length - 2) }
        $map[$k] = $v
    }
    return $map
}

function ConvertTo-Bool {
    param([string]$Value, [bool]$Default)
    if ([string]::IsNullOrWhiteSpace($Value)) { return $Default }
    switch ($Value.Trim().ToLowerInvariant()) { { $_ -in 'true', 'yes', '1', 'on' } { return $true } { $_ -in 'false', 'no', '0', 'off' } { return $false } }
    throw "Expected true or false, got '$Value'."
}

function Split-List {
    param([string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value)) { return , @() }
    return , @($Value.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
}

function Get-RemoteConfig {
    # Validates a parsed .env map and returns a config object. Throws one plain sentence on the first problem.
    param([Parameter(Mandatory)][hashtable]$Map)
    function V([string]$k) { if ($Map.ContainsKey($k)) { return [string]$Map[$k] } else { return '' } }
    $h = V 'SALT_MASTER_HOST'
    if ($h -eq '') { throw 'SALT_MASTER_HOST is empty. Type the server IP or name in the settings file.' }
    if ($h -notmatch '^([A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?|[0-9A-Fa-f:]{2,45})$') { throw 'SALT_MASTER_HOST must be an IP address or a DNS name (letters, digits, dots, dashes).' }
    $port = 22
    if ((V 'SSH_PORT') -ne '') { if (-not [int]::TryParse((V 'SSH_PORT'), [ref]$port) -or $port -lt 1 -or $port -gt 65535) { throw 'SSH_PORT must be a number from 1 to 65535.' } }
    $user = V 'SSH_USER'
    if ($user -eq '') { throw 'SSH_USER is empty.' }
    if ($user -notmatch '^[A-Za-z0-9_][A-Za-z0-9._@\-]{0,63}$') { throw 'SSH_USER has characters that are not allowed.' }
    $pw = V 'SSH_PASSWORD'; $key = V 'SSH_KEY_PATH'
    if ($pw -ne '' -and $key -ne '') { throw 'Set SSH_PASSWORD or SSH_KEY_PATH, not both.' }
    if ($pw -eq '' -and $key -eq '') { throw 'Set SSH_PASSWORD or SSH_KEY_PATH.' }
    if ($key -ne '') {
        if ($key.StartsWith('-')) { throw 'SSH_KEY_PATH must be a full file path.' }
        if (-not (Test-Path -LiteralPath $key -PathType Leaf)) { throw 'SSH_KEY_PATH does not point at an existing file.' }
        if ($key -match '(?i)\.ppk$') { throw 'SSH_KEY_PATH is a PuTTY .ppk key. OpenSSH cannot use it: in PuTTYgen choose Conversions > Export OpenSSH key.' }
    }
    $fp = V 'SSH_HOST_KEY_FINGERPRINT'
    if ($fp -ne '' -and $fp -notmatch '^SHA256:[A-Za-z0-9+/]{43}$') { throw 'SSH_HOST_KEY_FINGERPRINT must look like SHA256:xxxxxxxx (43 characters after the colon).' }
    $targets = Split-List (V 'SALT_MINION_TARGETS')
    foreach ($t in $targets) { if ($t -notmatch '^[A-Za-z0-9][A-Za-z0-9._\-]{0,127}$') { throw "SALT_MINION_TARGETS entry '$t' must be an exact minion id (no wildcards)." } }
    $logPaths = Split-List (V 'REMOTE_LOG_PATHS')
    foreach ($p in $logPaths) { if (-not (Test-PathCharset $p)) { throw "REMOTE_LOG_PATHS entry '$p' has characters that are not allowed (letters, digits, space, . : \ / _ - only)." } if ($p -match '\.\.') { throw "REMOTE_LOG_PATHS entry '$p' must not contain '..'." } }
    $mode = (V 'REMOTE_MODE').Trim().ToLowerInvariant()
    if ($mode -eq '') { $mode = 'readonly' }
    if ($mode -notin 'readonly', 'write') { throw "REMOTE_MODE must be readonly or write." }
    return [pscustomobject]@{
        Host = $h; Port = $port; User = $user; Password = $pw; KeyPath = $key; Fingerprint = $fp
        Targets = $targets; LogPaths = $logPaths; UseSudo = (ConvertTo-Bool (V 'SALT_USE_SUDO') $true); Mode = $mode
    }
}

# ---------------------------------------------------------------- redaction
function Protect-Text {
    param([string]$Text, [string[]]$Secrets = @())
    if ($null -eq $Text) { return '' }
    foreach ($s in $Secrets) { if ($s -and $s.Length -ge 1) { $Text = $Text.Replace($s, '[REDACTED]') } }
    $Text = [regex]::Replace($Text, '(?i)\b(password|passwd|pwd|secret|api[_-]?key|token|authorization)(\s*[=:]\s*)(bearer\s+)?[^\s,;"'']+', '$1$2[REDACTED]')
    return $Text
}

# ---------------------------------------------------------------- audit log (capped, no secrets)
function Write-Audit {
    param([string]$Action, [string]$Decision, [string]$Reason = '', [string]$Target = '', [string]$Command = '', [string]$Note = '', [Nullable[int]]$ExitCode = $null, [int]$Bytes = 0, [string[]]$Secrets = @())
    $home1 = Get-RemoteHome
    if (-not (Test-Path -LiteralPath $home1)) { New-Item -ItemType Directory -Path $home1 -Force | Out-Null }
    $file = Join-Path $home1 'audit.log'
    if ((Test-Path -LiteralPath $file) -and (Get-Item -LiteralPath $file).Length -gt 262144) {
        Move-Item -LiteralPath $file -Destination "$file.1" -Force
    }
    $cmd = Protect-Text $Command $Secrets
    if ($cmd.Length -gt 400) { $cmd = $cmd.Substring(0, 400) + '...' }
    $entry = [ordered]@{ ts = (Get-Date).ToUniversalTime().ToString('o'); action = $Action; decision = $Decision; reason = (Protect-Text $Reason $Secrets); target = $Target; command = $cmd; note = (Protect-Text $Note $Secrets); exit = $ExitCode; bytes = $Bytes }
    Add-Content -LiteralPath $file -Value ($entry | ConvertTo-Json -Compress) -Encoding UTF8
}

# ---------------------------------------------------------------- argument safety
function Test-PathCharset { param([string]$Path) return ($Path -match '^[A-Za-z0-9_ .:\\/\-]{1,260}$') }

function Test-YamlSafe {
    # Salt reads CLI arguments as YAML: "key: value" would become a dict and " #" starts a comment.
    param([string]$Text)
    if ($Text -match ':(\s|$)') { return $false }
    if ($Text -match '(^|\s)#') { return $false }
    if ($Text -match '^[\[\]{}!&*|>%@`,?-]\s*$') { return $false }
    return $true
}

function Test-PathAllowed {
    # $Path must be under one of $Allowed folders. No '..', no odd characters. A wildcard is accepted only in the last segment.
    param([string]$Path, [string[]]$Allowed)
    if (-not $Allowed -or $Allowed.Count -eq 0) { return $false }
    if (-not (Test-PathCharsetWild $Path)) { return $false }
    if ($Path -match '\.\.') { return $false }
    $norm = $Path.Replace('\', '/')
    $last = $norm.LastIndexOf('/')
    $dirPart = if ($last -ge 0) { $norm.Substring(0, $last) } else { '' }
    if ($dirPart -match '[*?]') { return $false }
    $isWin = $norm -match '^[A-Za-z]:'
    $cmp = if ($isWin) { [System.StringComparison]::OrdinalIgnoreCase } else { [System.StringComparison]::Ordinal }
    foreach ($a in $Allowed) {
        $an = $a.Replace('\', '/').TrimEnd('/')
        if ($an -eq '') { continue }
        if ($norm.Equals($an, $cmp)) { return $true }
        if ($norm.StartsWith($an + '/', $cmp)) { return $true }
    }
    return $false
}
function Test-PathCharsetWild { param([string]$Path) return ($Path -match '^[A-Za-z0-9_ .:\\/\-*?]{1,260}$') }

function Test-SafeWord {
    # A single plain word: service name, grain name, log name. Letters, digits and . _ - only.
    param([string]$Text, [int]$Max = 80)
    return ($Text -match "^[A-Za-z0-9_][A-Za-z0-9_.\-]{0,$($Max - 1)}$")
}

function Test-SafePattern {
    param([string]$Text)
    if ($Text -notmatch '^[A-Za-z0-9_ .:\\/\-=,@\[\]*+?]{1,200}$') { return $false }
    return (Test-YamlSafe $Text)
}

function ConvertTo-ShSingleQuoted {
    # POSIX single-quote a value for the master's shell.
    param([string]$Text)
    return "'" + $Text.Replace("'", "'\''") + "'"
}

function Split-CommandWords {
    # Splits on spaces, keeping 'single quoted' text together (quotes removed). Returns $null when quotes do not balance.
    param([string]$Text)
    $words = New-Object System.Collections.Generic.List[string]
    $sb = New-Object System.Text.StringBuilder
    $inQ = $false; $has = $false
    foreach ($c in $Text.ToCharArray()) {
        if ($c -eq "'") { $inQ = -not $inQ; $has = $true; continue }
        if ($c -eq ' ' -and -not $inQ) { if ($has -or $sb.Length -gt 0) { $words.Add($sb.ToString()); [void]$sb.Clear(); $has = $false }; continue }
        [void]$sb.Append($c)
    }
    if ($inQ) { return $null }
    if ($has -or $sb.Length -gt 0) { $words.Add($sb.ToString()) }
    return , $words.ToArray()
}

# ---------------------------------------------------------------- the read-only gate
# Salt functions that only read, with how many arguments they take and how each is checked.
$script:ReadFns = @{
    'test.ping'        = @{ Min = 0; Max = 0; Kind = '' }
    'test.version'     = @{ Min = 0; Max = 0; Kind = '' }
    'grains.items'     = @{ Min = 0; Max = 0; Kind = '' }
    'grains.item'      = @{ Min = 1; Max = 8; Kind = 'word' }
    'status.uptime'    = @{ Min = 0; Max = 0; Kind = '' }
    'status.loadavg'   = @{ Min = 0; Max = 0; Kind = '' }
    'status.meminfo'   = @{ Min = 0; Max = 0; Kind = '' }
    'status.diskusage' = @{ Min = 0; Max = 0; Kind = '' }
    'service.status'   = @{ Min = 1; Max = 1; Kind = 'service' }
    'service.get_all'  = @{ Min = 0; Max = 0; Kind = '' }
    'file.file_exists' = @{ Min = 1; Max = 1; Kind = 'path' }
    'file.stats'       = @{ Min = 1; Max = 1; Kind = 'path' }
    'file.read'        = @{ Min = 1; Max = 1; Kind = 'path' }
}
$script:RunFns = @('manage.up', 'manage.down', 'manage.status', 'jobs.active', 'jobs.list_jobs')
$script:WriteFns = @('service.restart', 'service.start', 'service.stop')
$script:ProtectedServices = '^(salt-.*|salt|sshd?|openssh.*|ssh-agent|cron|crond|dbus.*|networking|network.*|systemd.*|winrm|rpcss|dhcp|dnscache|eventlog)$'

function Test-SaltTarget {
    param([string]$Target, $Config)
    if (-not $Target) { return 'no target given' }
    if ($Target -notmatch '^[A-Za-z0-9][A-Za-z0-9._\-]{0,127}$') { return 'target must be one exact minion id (no wildcards, lists or compound matches)' }
    if ($Config.Targets.Count -eq 0) { return 'SALT_MINION_TARGETS is empty: run -Action Keys, then put the minion id in the settings file' }
    if ($Config.Targets -notcontains $Target) { return "target '$Target' is not in SALT_MINION_TARGETS" }
    return $null
}

function Test-SaltReadCall {
    # Returns $null when allowed, otherwise the reason it is refused.
    param([string]$Fn, [string[]]$FnArgs = @(), $Config)
    if (-not $script:ReadFns.ContainsKey($Fn)) { return "'$Fn' is not on the read-only list" }
    $spec = $script:ReadFns[$Fn]
    if ($FnArgs.Count -lt $spec.Min -or $FnArgs.Count -gt $spec.Max) { return "'$Fn' takes $($spec.Min) to $($spec.Max) argument(s)" }
    foreach ($a in $FnArgs) {
        if (-not (Test-YamlSafe $a)) { return 'argument could be read as YAML (": " or " #"); rephrase it' }
        switch ($spec.Kind) {
            'word' { if (-not (Test-SafeWord $a)) { return "argument '$a' is not a plain name" } }
            'service' { if (-not (Test-SafeWord $a)) { return "service name '$a' is not a plain name" } }
            'path' { if (-not (Test-PathAllowed $a $Config.LogPaths)) { return "path '$a' is outside REMOTE_LOG_PATHS (or REMOTE_LOG_PATHS is empty)" } if ($a -match '[*?]') { return 'wildcards are not allowed here' } }
        }
    }
    return $null
}

$script:PathCommands = @('tail', 'head', 'cat', 'type', 'get-content', 'ls', 'dir', 'get-childitem')
$script:BannedFlags = '^-{1,2}(f|F|follow|wait|delete|exec|execdir|o|out|output|outfile)$'

function Test-ReadOnlyCommand {
    # Gate for a free-form cmd.run string. Returns $null when allowed, otherwise the reason it is refused.
    # Deliberately tiny: file readers and listers, nothing that writes, chains or follows.
    param([string]$Cmd, $Config)
    if ([string]::IsNullOrWhiteSpace($Cmd)) { return 'empty command' }
    if ($Cmd.Length -gt 400) { return 'command is too long' }
    if ($Cmd -notmatch '^[A-Za-z0-9_ .:\\/\-=,@''*?]+$') { return 'command has characters that are not allowed (no quotes other than single, no | & ; < > $ ` % ^ ( ) { } or line breaks)' }
    if (-not (Test-YamlSafe $Cmd)) { return 'command could be read as YAML (": " or " #"); rephrase it' }
    if ($Cmd -match '\.\.') { return "'..' is not allowed" }
    $w = Split-CommandWords $Cmd
    if ($null -eq $w) { return 'single quotes do not balance' }
    if ($w.Count -eq 0) { return 'empty command' }
    $first = $w[0].ToLowerInvariant()
    if ($script:PathCommands -notcontains $first) { return "'$($w[0])' is not on the read-only command list (tail, head, cat, type, Get-Content, ls, dir, Get-ChildItem)" }
    $paths = 0
    for ($i = 1; $i -lt $w.Count; $i++) {
        $t = $w[$i]
        if ($t.StartsWith('-')) {
            if ($t -match '[/\\:=]') { return "option '$t' may hide a path or value; write it as a separate word" }
            if ($t -match $script:BannedFlags) { return "option '$t' is not allowed (it follows, waits, deletes or writes)" }
            if ($t -notmatch '^-{1,2}[A-Za-z][A-Za-z0-9\-]*$') { return "option '$t' is not understood" }
            continue
        }
        if ($t -match '^[0-9]+$') { continue }
        if (-not (Test-PathAllowed $t $Config.LogPaths)) { return "'$t' is not a number or a path under REMOTE_LOG_PATHS (REMOTE_LOG_PATHS must be set and list the folder)" }
        $paths++
    }
    if ($paths -eq 0) { return 'give the full path of what to read' }
    return $null
}

function Test-WriteCall {
    # Write mode is v1-tiny: start, stop or restart one named, non-critical service.
    param([string]$Fn, [string]$ServiceName, $Config)
    if ($Config.Mode -ne 'write') { return 'REMOTE_MODE is readonly; set REMOTE_MODE=write in the settings file to request changes' }
    if ($script:WriteFns -notcontains $Fn) { return "'$Fn' is not available even in write mode (only service.start, service.stop, service.restart)" }
    if (-not (Test-SafeWord $ServiceName)) { return 'service name is not a plain name' }
    if ($ServiceName -match $script:ProtectedServices) { return "service '$ServiceName' is protected (it could cut off access); do it by hand" }
    return $null
}

function Test-SaltRunCall {
    param([string]$Fn)
    if ($script:RunFns -notcontains $Fn) { return "'$Fn' is not on the read-only salt-run list ($($script:RunFns -join ', '))" }
    return $null
}

# ---------------------------------------------------------------- building the remote command (one string, every word quoted)
function New-SaltRemoteCommand {
    # $Tool = salt | salt-run | salt-key. $Words are already validated; each is single-quoted for the master's shell.
    param([string]$Tool, [string[]]$Words, $Config, [int]$TimeoutSec = 30)
    $parts = New-Object System.Collections.Generic.List[string]
    if ($Config.UseSudo) { $parts.Add('sudo'); $parts.Add('-n') }
    $parts.Add($Tool)
    $parts.Add('--out=json'); $parts.Add('--no-color')
    if ($Tool -ne 'salt-key') { $parts.Add('--static') }
    if ($Tool -eq 'salt') { $parts.Add('-t'); $parts.Add([string]$TimeoutSec) }
    foreach ($x in $Words) { $parts.Add((ConvertTo-ShSingleQuoted $x)) }
    return ($parts -join ' ')
}

function New-ReadRecipe {
    # Builds the cmd.run string for a log recipe. Returns @{ Cmd; Shell; Kwargs } or throws on bad input. Os = windows | linux.
    param([string]$Recipe, [string]$Os, [string]$Path, [int]$Lines = 200, [string]$Pattern, [string]$LogName, $Config)
    if ($Lines -lt 1 -or $Lines -gt 5000) { throw 'Lines must be 1 to 5000.' }
    if ($Recipe -in 'LogTail', 'LogGrep', 'LogList') {
        if (-not (Test-PathAllowed $Path $Config.LogPaths)) { throw "path '$Path' is outside REMOTE_LOG_PATHS (or REMOTE_LOG_PATHS is empty)" }
        if (-not (Test-YamlSafe $Path)) { throw 'path could be read as YAML; rephrase it' }
    }
    $isWin = ($Os -eq 'windows')
    switch ($Recipe) {
        'LogTail' {
            if ($Path -match '[*?]') { throw 'wildcards are not allowed in LogTail; list the folder first' }
            if ($isWin) { return @{ Cmd = "Get-Content -LiteralPath '$Path' -Tail $Lines"; Kwargs = @('shell=powershell') } }
            return @{ Cmd = "tail -n $Lines '$Path'"; Kwargs = @('python_shell=False') }
        }
        'LogGrep' {
            if ($Path -match '[*?]') { throw 'wildcards are not allowed in LogGrep; list the folder first' }
            if (-not (Test-SafePattern $Pattern)) { throw 'pattern may only use letters, digits, space and . : \ / - = , @ [ ] * + ? (no ": " and no " #")' }
            if ($isWin) { return @{ Cmd = "Select-String -LiteralPath '$Path' -Pattern '$Pattern'"; Kwargs = @('shell=powershell'); LastLines = $Lines } }
            return @{ Cmd = "grep -n -i -m $Lines -e '$Pattern' '$Path'"; Kwargs = @('python_shell=False') }
        }
        'LogList' {
            if ($isWin) { return @{ Cmd = "Get-ChildItem -LiteralPath '$Path'"; Kwargs = @('shell=powershell') } }
            return @{ Cmd = "ls -la '$Path'"; Kwargs = @('python_shell=False') }
        }
        'EventLog' {
            if ($Os -ne 'windows') { throw 'EventLog is for Windows minions; use LogTail on Linux' }
            if ($LogName -notmatch '^[A-Za-z0-9][A-Za-z0-9 _.\-]{0,99}$') { throw 'LogName must be a plain event log name such as Application or System' }
            if (-not (Test-YamlSafe $LogName)) { throw 'LogName could be read as YAML' }
            if ($Lines -gt 500) { throw 'EventLog Lines must be 1 to 500' }
            return @{ Cmd = "Get-WinEvent -LogName '$LogName' -MaxEvents $Lines"; Kwargs = @('shell=powershell') }
        }
    }
    throw "unknown recipe '$Recipe'"
}

# ---------------------------------------------------------------- running ssh
function ConvertTo-WinArg {
    # Quotes one argument for a Windows command line (CommandLineToArgvW rules).
    param([string]$Arg)
    if ($Arg -ne '' -and $Arg -notmatch '[\s"]') { return $Arg }
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('"')
    $bs = 0
    foreach ($c in $Arg.ToCharArray()) {
        if ($c -eq '\') { $bs++; continue }
        if ($c -eq '"') { [void]$sb.Append('\', $bs * 2 + 1); [void]$sb.Append('"'); $bs = 0; continue }
        if ($bs -gt 0) { [void]$sb.Append('\', $bs); $bs = 0 }
        [void]$sb.Append($c)
    }
    if ($bs -gt 0) { [void]$sb.Append('\', $bs * 2) }
    [void]$sb.Append('"')
    return $sb.ToString()
}

function Get-KnownHostsPath { return (Join-Path (Get-RemoteHome) 'known_hosts') }

function New-SshArgs {
    # The full ssh argument list. Never contains a secret. -F NUL ignores the user's own ~/.ssh/config.
    param($Config, [string]$RemoteCommand)
    $kh = (Get-KnownHostsPath).Replace('\', '/')
    $a = New-Object System.Collections.Generic.List[string]
    $a.Add('-F'); $a.Add('NUL'); $a.Add('-T')
    foreach ($o in @('StrictHostKeyChecking=yes', "UserKnownHostsFile=$kh", 'GlobalKnownHostsFile=NUL', 'ConnectTimeout=15', 'ServerAliveInterval=15', 'ServerAliveCountMax=3',
            'ForwardAgent=no', 'ForwardX11=no', 'ClearAllForwardings=yes', 'PermitLocalCommand=no', 'LogLevel=ERROR', 'NumberOfPasswordPrompts=1')) { $a.Add('-o'); $a.Add($o) }
    if ($Config.KeyPath) {
        foreach ($o in @('BatchMode=yes', 'IdentitiesOnly=yes', 'PasswordAuthentication=no', 'KbdInteractiveAuthentication=no')) { $a.Add('-o'); $a.Add($o) }
        $a.Add('-i'); $a.Add($Config.KeyPath)
    } else {
        foreach ($o in @('PreferredAuthentications=password,keyboard-interactive', 'PubkeyAuthentication=no')) { $a.Add('-o'); $a.Add($o) }
    }
    $a.Add('-p'); $a.Add([string]$Config.Port)
    $a.Add('-l'); $a.Add($Config.User)
    $a.Add('--'); $a.Add($Config.Host)
    if ($RemoteCommand) { $a.Add($RemoteCommand) }
    return , $a.ToArray()
}

function Invoke-Process {
    # Runs an exe with captured output, a timeout and optional extra environment. Returns @{ Exit; Out; Err; TimedOut }.
    param([string]$File, [string[]]$Arguments, [hashtable]$ExtraEnv = @{}, [int]$TimeoutSec = 60, [string]$StdIn = $null, [int]$MaxBytes = 4194304)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $File
    $psi.Arguments = (($Arguments | ForEach-Object { ConvertTo-WinArg $_ }) -join ' ')
    $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true; $psi.RedirectStandardInput = $true
    $psi.StandardOutputEncoding = [System.Text.Encoding]::UTF8; $psi.StandardErrorEncoding = [System.Text.Encoding]::UTF8
    foreach ($k in $ExtraEnv.Keys) { $psi.EnvironmentVariables[$k] = [string]$ExtraEnv[$k] }
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    [void]$p.Start()
    $outTask = $p.StandardOutput.ReadToEndAsync(); $errTask = $p.StandardError.ReadToEndAsync()
    if ($null -ne $StdIn) { $p.StandardInput.Write($StdIn) }
    $p.StandardInput.Close()
    $timedOut = $false
    if (-not $p.WaitForExit($TimeoutSec * 1000)) { $timedOut = $true; try { $p.Kill() } catch {} }
    else { $p.WaitForExit() }
    $out = $outTask.Result; $err = $errTask.Result
    if ($out.Length -gt $MaxBytes) { $out = $out.Substring(0, $MaxBytes) + "`n[output cut at $MaxBytes characters]" }
    $code = if ($timedOut) { -1 } else { $p.ExitCode }
    return @{ Exit = $code; Out = $out; Err = $err; TimedOut = $timedOut }
}

function Invoke-RemoteSsh {
    # Runs one remote command over ssh. Password sign-in goes through SSH_ASKPASS with the password in the child's
    # environment only, never on a command line. Output and errors are scrubbed of the password before they return.
    param($Config, [string]$RemoteCommand, [int]$TimeoutSec = 60, [string]$SshExe = 'ssh.exe')
    $args1 = New-SshArgs $Config $RemoteCommand
    $env1 = @{}
    if ($Config.Password) {
        $env1['SSH_ASKPASS'] = (Join-Path $PSScriptRoot 'askpass.cmd')
        $env1['SSH_ASKPASS_REQUIRE'] = 'force'
        $env1['DISPLAY'] = 'unravis:0'
        $env1['UNRAVIS_SSH_PW'] = $Config.Password
    }
    $secrets = @($Config.Password) | Where-Object { $_ }
    $r = Invoke-Process -File $SshExe -Arguments $args1 -ExtraEnv $env1 -TimeoutSec $TimeoutSec
    $r.Out = Protect-Text $r.Out $secrets
    $r.Err = Protect-Text $r.Err $secrets
    return $r
}
