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
    $deps = @('api-audit-explorer/internal/update', 'api-audit-explorer/tools/release/sigtool', '')
    Assert-That 'deps: today''s non-standard set is allowed' ((Get-ForbiddenSigtoolDeps $deps).Count -eq 0)
    Assert-That 'deps: golang.org/x/sys/windows (pinned, used by internal/update) is allowed' ((Get-ForbiddenSigtoolDeps ($deps + 'golang.org/x/sys/windows')).Count -eq 0)
    $f = Get-ForbiddenSigtoolDeps ($deps + 'github.com/evil/exfil' + 'api-audit-explorer/internal/server' + 'exfil' + 'golang.org/x/sys/unix' + 'golang.org/x/net/http2')
    Assert-That 'deps: a module, another internal package, a dot-less replaced module and other x/ packages are refused' ($f.Count -eq 5)
    $real = Get-SigtoolNonStdDeps (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
    Assert-That 'deps: the real signing tool passes the rule' ((Get-ForbiddenSigtoolDeps $real).Count -eq 0) ($real -join ', ')

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
    Assert-That 'lock: free again after the first run ends' (-not (Get-ThrownMessage { (Enter-ReleaseLock $d).Dispose() }))
    Set-ReleaseState $d '0.1.0-rc.55' 'Sign' 'stopped' 'network' @{ key = 'rc-2'; passref = 'pass://Work/Unravis Passphrases/rc.2 passphrase'; prev_feed = 'v0.1.0-rc.54' }
    $st = Get-ReleaseState $d
    Assert-That 'state: round trip' ($st.version -eq '0.1.0-rc.55' -and $st.stage -eq 'Sign' -and $st.status -eq 'stopped')
    Assert-That 'state: key, Proton Pass reference and the previous feed release are kept' ($st.key -eq 'rc-2' -and $st.prev_feed -eq 'v0.1.0-rc.54' -and $st.passref -match 'rc\.2')
    Assert-That 'state: a field named like a secret is refused' ([bool](Get-ThrownMessage { Set-ReleaseState $d 'x' 'Tag' 'running' '' @{ passphrase = 'x' } }))
    $order = 'Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify'
    Assert-That 'resume: after Sign comes Publish' ((Get-ResumeStage $order 'Sign') -eq 'Publish')
    Assert-That 'resume: nothing done yet starts at Preflight' ((Get-ResumeStage $order '') -eq 'Preflight')
    Assert-That 'resume: after Verify there is nothing' ($null -eq (Get-ResumeStage $order 'Verify'))

    # ---- lock details, pinned commit, resume plan, nothing-new rule ---------------------------------------------------------------
    $l1 = Enter-ReleaseLock $d
    try {
        Set-LockInfo $d @{ id = 'abc'; pid = 4242; requester = 'thread-OTA'; version = '0.1.0-rc.56'; stage = 'Wait'; started = '2026-11-21T22:00:00Z' }
        $m = Get-ThrownMessage { Enter-ReleaseLock $d }
        Assert-That 'lock: a refused run is told who holds it, which version and step' ($m -match 'thread-OTA' -and $m -match 'rc\.56' -and $m -match "'Wait'" -and $m -match 'WaitMinutes') $m
    } finally { $l1.Dispose() }
    $now = [datetime]'2026-11-21T23:00:00Z'
    $held = [pscustomobject]@{ state = 'held'; host = 'PC-A'; pid = 4242; version = '0.1.0-rc.56'; channel = 'rc'; stage = 'Wait'; requester = 'thread-OTA'; started = '2026-11-21T22:30:00Z'; heartbeat = '2026-11-21T22:55:00Z'; lease_minutes = 180; holdMerges = $false }
    $free = [pscustomobject]@{ state = 'free'; host = 'PC-A' }
    Assert-That 'lock busy: no record is free' (-not (Test-ReleaseLockBusy $null 'PC-A' $now $true).Busy)
    Assert-That 'lock busy: a free record is free' (-not (Test-ReleaseLockBusy $free 'PC-A' $now $true).Busy)
    Assert-That 'lock busy: held by a live process on this PC is busy' (Test-ReleaseLockBusy $held 'PC-A' $now $false).Busy
    $t = Test-ReleaseLockBusy $held 'PC-A' $now $true
    Assert-That 'lock busy: held from this PC but the PC lock is free = crashed run, taken over' (-not $t.Busy -and $t.Stale)
    Assert-That 'lock busy: another PC with a fresh heartbeat is busy' (Test-ReleaseLockBusy $held 'PC-B' $now $true).Busy
    $t = Test-ReleaseLockBusy $held 'PC-A' $now $true $true
    Assert-That 'lock busy: same PC, own lock free, but the recorded process is alive (other data folder) = busy, not taken over' ($t.Busy -and -not $t.Stale)
    Assert-That 'holder alive: this very process counts, a made-up pid does not' ((Test-HolderAlive $PID ([DateTime]::UtcNow.AddMinutes(1))) -and -not (Test-HolderAlive 2147483000 '2026-01-01T00:00:00Z'))
    Assert-That 'host id: opaque, stable, no machine name in it' ((Get-HostId) -eq (Get-HostId) -and (Get-HostId) -notmatch [regex]::Escape([Environment]::MachineName) -and (Get-HostId).Length -eq 12)
    $old = $held.PSObject.Copy(); $old.heartbeat = '2026-11-21T18:00:00Z'
    $t = Test-ReleaseLockBusy $old 'PC-B' $now $true
    Assert-That 'lock busy: another PC silent for longer than the lease is stale' (-not $t.Busy -and $t.Stale)
    $msg = Format-LockBusy $held $now
    Assert-That 'lock busy: the message names version, step and who, and not a machine' ($msg -match 'rc\.56' -and $msg -match "'Wait'" -and $msg -match 'thread-OTA' -and $msg -notmatch 'PC-A' -and $msg -match '5 min ago') $msg
    Assert-That 'lock busy: the message says whether it is this PC' ((Format-LockBusy $held $now 'PC-A') -match 'this PC' -and (Format-LockBusy $held $now 'PC-B') -match 'another PC')
    Assert-That 'lock record: a field that could hold a secret is refused' ([bool](Get-ThrownMessage { New-LockRecord @{ passphrase = 'x' } }))
    Assert-That 'lock record: defaults hold merges and carry the lease' ((New-LockRecord @{ id = 'x' }).holdMerges -eq $true -and (New-LockRecord @{}).lease_minutes -eq 180)

    $c1 = 'a' * 40; $c2 = 'b' * 40; $c3 = 'c' * 40
    $run = { param($sha, $st, $co, $id) [pscustomobject]@{ headSha = $sha; status = $st; conclusion = $co; databaseId = $id } }
    $sel = Select-PinnedCommit @($c1, $c2, $c3) @((& $run $c1 'completed' 'success' 30), (& $run $c2 'completed' 'success' 20))
    Assert-That 'pin: a green head is pinned' ($sel.Status -eq 'green' -and $sel.Commit -eq $c1)
    $sel = Select-PinnedCommit @($c1, $c2, $c3) @((& $run $c1 'in_progress' $null 30), (& $run $c2 'completed' 'success' 20))
    Assert-That 'pin: an unfinished head waits (does not fall back unless asked)' ($sel.Status -eq 'waiting' -and -not $sel.Commit)
    $sel = Select-PinnedCommit @($c1, $c2, $c3) @((& $run $c1 'in_progress' $null 30), (& $run $c2 'completed' 'success' 20)) -LastGreen
    Assert-That 'pin: -LastGreen takes the newest green commit' ($sel.Status -eq 'green' -and $sel.Commit -eq $c2)
    $sel = Select-PinnedCommit @($c1, $c2) @((& $run $c1 'completed' 'failure' 30))
    Assert-That 'pin: a red head is refused' ($sel.Status -eq 'red')
    $sel = Select-PinnedCommit @($c1, $c2) @((& $run $c1 'completed' 'failure' 5), (& $run $c1 'completed' 'success' 9))
    Assert-That 'pin: the newest run of a commit decides (a green re-run outranks an older red one)' ($sel.Status -eq 'green')
    $sel = Select-PinnedCommit @($c1, $c2) @((& $run $c1 'completed' 'success' 5), (& $run $c1 'completed' 'failure' 9))
    Assert-That 'pin: ... and a red re-run outranks an older green one' ($sel.Status -eq 'red')
    $sel = Select-PinnedCommit @($c1) @() -LastGreen
    Assert-That 'pin: -LastGreen with no green commit says none' ($sel.Status -eq 'none')
    $sel = Select-PinnedCommit @($c1) @()
    Assert-That 'pin: a head with no CI run yet waits' ($sel.Status -eq 'waiting' -and $sel.Why -match 'norun')
    $sel = Select-PinnedCommit @($c1) @((& $run $c1 'completed' 'cancelled' 5))
    Assert-That 'pin: a cancelled head is stopped with a hint (it would never finish)' ($sel.Status -eq 'red' -and $sel.Why -match 'LastGreen')
    Assert-That 'lock busy: a damaged heartbeat counts as expired, it does not throw' (-not (Test-ReleaseLockBusy ([pscustomobject]@{ state = 'held'; host = 'PC-A'; heartbeat = 'garbage'; version = 'x' }) 'PC-B' $now $true).Busy)
    Assert-That 'safe text: control characters and long text are cut' ((Get-SafeText "a`n b`e[31m") -notmatch "[`n`e]" -and (Get-SafeText ('x' * 500)).Length -lt 70)
    $ownRun = Enter-RunLock $d
    try { Assert-That 'run lock: a second holder is refused' ([bool](Get-ThrownMessage { Enter-RunLock $d })) } finally { $ownRun.Dispose() }

    $tags2 = @('v0.1.0-rc.54', 'v0.1.0-rc.55')
    $stOpen = [pscustomobject]@{ version = '0.1.0-rc.56'; stage = 'Tag'; status = 'stopped' }
    Assert-That 'resume plan: an unfinished newer release is continued' ((Get-ResumePlan $stOpen 'rc' '' $tags2 $false).Resuming)
    Assert-That 'resume plan: -CheckOnly never resumes' (-not (Get-ResumePlan $stOpen 'rc' '' $tags2 $true).Resuming)
    Assert-That 'resume plan: another -Version is a different release' (-not (Get-ResumePlan $stOpen 'rc' '0.1.0-rc.57' $tags2 $false).Resuming)
    Assert-That 'resume plan: the other channel is not resumed' (-not (Get-ResumePlan $stOpen 'stable' '' $tags2 $false).Resuming)
    Assert-That 'resume plan: a done release is not resumed' (-not (Get-ResumePlan ([pscustomobject]@{ version = '0.1.0-rc.56'; stage = 'Verify'; status = 'done' }) 'rc' '' $tags2 $false).Resuming)
    Assert-That 'resume plan: a state older than the newest tag is not resumed' (-not (Get-ResumePlan ([pscustomobject]@{ version = '0.1.0-rc.54'; stage = 'Tag'; status = 'stopped' }) 'rc' '' $tags2 $false).Resuming)
    $done55 = [pscustomobject]@{ version = '0.1.0-rc.55'; stage = 'Verify'; status = 'done' }
    Assert-That 'nothing new: same commit, release finished = done' ((Get-AlreadyReleased 'rc' 'v0.1.0-rc.55' $c1 $c1 $done55) -eq 'done')
    Assert-That 'nothing new: master moved on = go on' ($null -eq (Get-AlreadyReleased 'rc' 'v0.1.0-rc.55' $c1 $c2 $done55))
    Assert-That 'nothing new: same commit but withdrawn = refused' ((Get-AlreadyReleased 'rc' 'v0.1.0-rc.55' $c1 $c1 ([pscustomobject]@{ version = '0.1.0-rc.55'; status = 'withdrawn' })) -eq 'withdrawn')
    Assert-That 'nothing new: no tag of this kind yet = go on' ($null -eq (Get-AlreadyReleased 'stable' '' '' $c1 $done55))
    Assert-That 'backoff: grows and is capped' ((Get-BackoffSeconds 1) -eq 5 -and (Get-BackoffSeconds 3) -eq 15 -and (Get-BackoffSeconds 50) -eq 30 -and (Get-BackoffSeconds 50 60) -eq 60)

    # ---- the lock record on GitHub, against a fake gh that behaves like the contents API ------------------------------------------
    $script:fake = @{ exists = $false; sha = ''; text = ''; n = 0; blob = '' }
    function gh {
        $a = @($args); $global:LASTEXITCODE = 0
        $path = $a | Where-Object { $_ -like 'repos/*' } | Select-Object -First 1
        $mi = [array]::IndexOf($a, '-X'); $method = if ($mi -ge 0) { $a[$mi + 1] } else { 'GET' }
        $f = @{}; for ($k = 0; $k -lt $a.Count - 1; $k++) { if ($a[$k] -eq '-f') { $kv = $a[$k + 1] -split '=', 2; $f[$kv[0]] = $kv[1] } }
        $stdin = (@($input) -join '')
        $fail = { param($code, $why) $global:LASTEXITCODE = 1; "gh: $why (HTTP $code)" }
        if ($path -like '*/contents/lock.json*' -and $method -eq 'GET') {
            if (-not $script:fake.exists -or $script:fake.fileGone) { return (& $fail 404 'Not Found') }
            return (@{ sha = $script:fake.sha; content = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($script:fake.text)) } | ConvertTo-Json -Compress)
        }
        if ($path -like '*/contents/lock.json*' -and $method -eq 'PUT') {
            if (-not $f.ContainsKey('sha')) {
                if (-not $script:fake.fileGone) { return (& $fail 422 'Invalid request. "sha" wasn''t supplied.') }
                $script:fake.fileGone = $false
            } elseif ($f['sha'] -ne $script:fake.sha) { return (& $fail 409 'lock.json does not match') }
            $script:fake.n++; $script:fake.sha = "s$($script:fake.n)"; $script:fake.text = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($f['content']))
            return (@{ content = @{ sha = $script:fake.sha } } | ConvertTo-Json -Compress)
        }
        if ($path -like '*/git/blobs') { $script:fake.blob = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($f['content'])); return '{"sha":"blob1"}' }
        if ($path -like '*/git/trees') { return '{"sha":"tree1"}' }
        if ($path -like '*/git/commits') { return '{"sha":"commit1"}' }
        if ($path -like '*/git/refs') {
            if ($script:fake.exists) { return (& $fail 422 'Reference already exists') }
            $script:fake.exists = $true; $script:fake.sha = 'blob1'; $script:fake.text = $script:fake.blob; return '{}'
        }
        $global:LASTEXITCODE = 1; "fake gh: unexpected call $a"
    }
    $r0 = Read-RemoteLock 'o/r'
    Assert-That 'remote lock: no branch yet reads as free' ($r0.Sha -eq '' -and $null -eq $r0.Record)
    $rec1 = New-LockRecord @{ id = 'run-1'; host = 'PC-A'; requester = 'thread-A'; version = '0.1.0-rc.56'; stage = 'Starting'; started = '2026-11-21T22:00:00Z'; heartbeat = '2026-11-21T22:00:00Z' }
    $sha1 = Write-RemoteLock 'o/r' $rec1 '' 'first'
    $r1 = Read-RemoteLock 'o/r'
    Assert-That 'remote lock: the first claim creates the branch and reads back' ($r1.Sha -eq $sha1 -and $r1.Record.id -eq 'run-1' -and $r1.Record.state -eq 'held')
    Assert-That 'remote lock: a second first-claim loses' ((Get-ThrownMessage { Write-RemoteLock 'o/r' (New-LockRecord @{ id = 'run-2' }) '' 'second' }) -match '^LOCK-CONFLICT')
    Assert-That 'remote lock: a write with a stale sha loses' ((Get-ThrownMessage { Write-RemoteLock 'o/r' (New-LockRecord @{ id = 'run-2' }) 'old-sha' 'stale' }) -match '^LOCK-CONFLICT')
    $null = Update-RemoteLock 'o/r' 'run-1' @{ stage = 'Wait'; holdMerges = $false }
    $r2 = Read-RemoteLock 'o/r'
    Assert-That 'remote lock: the owner can change the step and keeps the other fields' ($r2.Record.stage -eq 'Wait' -and $r2.Record.holdMerges -eq $false -and $r2.Record.requester -eq 'thread-A' -and $r2.Sha -ne $sha1)
    Assert-That 'remote lock: another run cannot change it ("lock lost")' ((Get-ThrownMessage { Update-RemoteLock 'o/r' 'run-2' @{ stage = 'Tag' } }) -match 'lock lost')
    $null = Update-RemoteLock 'o/r' 'run-1' @{ state = 'free'; stage = 'idle'; last = @{ version = '0.1.0-rc.56'; outcome = 'done' } }
    $r3 = Read-RemoteLock 'o/r'
    Assert-That 'remote lock: freeing keeps what the last run was' ($r3.Record.state -eq 'free' -and $r3.Record.last.outcome -eq 'done' -and -not (Test-ReleaseLockBusy $r3.Record 'PC-B' $now $true).Busy)
    # lock.json deleted by hand while the branch stays: the next claim still works (creates the file on the branch)
    $script:fake.text = ''; $script:fake.exists = $true; $script:fake.sha = 'orphan'; $script:fake.fileGone = $true
    $rg = Read-RemoteLock 'o/r'
    Assert-That 'remote lock: branch without lock.json reads as free' ($rg.Sha -eq '' -and $null -eq $rg.Record)
    $shaG = Write-RemoteLock 'o/r' (New-LockRecord @{ id = 'run-9' }) '' 'again'
    Assert-That 'remote lock: a claim on a branch whose lock.json was deleted works' ((Read-RemoteLock 'o/r').Record.id -eq 'run-9')
    Remove-Item function:gh

    # ---- the audit log names the commit a version was tagged on; the bootstrap never runs a tag the log does not vouch for ---------
    $al = Join-Path $root 'audit-tagged.jsonl'
    Write-AuditEntry $al @{ event = 'tagged'; version = '0.1.0-rc.56'; commit = ('a' * 40) }
    Write-AuditEntry $al @{ event = 'tagged'; version = '0.1.0-rc.57'; commit = ('b' * 40) }
    Assert-That 'audit tagged: the commit of that version' ((Get-AuditTaggedCommit $al '0.1.0-rc.56') -eq ('a' * 40))
    Write-AuditEntry $al @{ event = 'tagging'; version = '0.1.0-rc.58'; commit = ('c' * 40) }
    Write-AuditEntry $al @{ event = 'tag-failed'; version = '0.1.0-rc.58'; commit = ('c' * 40) }
    Assert-That 'audit tagged: a push that failed vouches for nothing' ((Get-AuditTaggedCommit $al '0.1.0-rc.58') -eq '')
    Assert-That 'audit tagged: unknown version gives nothing' ((Get-AuditTaggedCommit $al '0.1.0-rc.99') -eq '')
    Assert-That 'audit tagged: no log gives nothing' ((Get-AuditTaggedCommit (Join-Path $root 'none.jsonl') '0.1.0-rc.56') -eq '')
    # Things that broke or would break unseen (these cannot be run without a release, so the text is held to them):
    $rel = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Release.ps1')
    Assert-That 'bootstrap: the re-run is not -NonInteractive (typed confirmations without -Yes must still work)' ($rel -notmatch "'-NonInteractive'")
    Assert-That 'bootstrap: git hooks are switched off for the run' ($rel -match 'GIT_CONFIG_KEY_0.*core\.hooksPath')
    Assert-That 'bootstrap: every pin is checked to be part of master' ($rel -match 'merge-base --is-ancestor \$pinned')
    Assert-That 'bootstrap: a resumed pin must match the audit log''s tagged commit' ($rel -match 'Get-AuditTaggedCommit')
    Assert-That 'bootstrap: the checks of a fresh pin are gated on "pinned from a tag", not on resuming' ($rel -match 'if \(\$newestAll -and -not \$fromTag\)')
    Assert-That 'bootstrap: the tag is logged before it is pushed' ($rel -match "(?s)event = 'tagging'.*git push origin")
    Assert-That 'run: the run proves it was started by the bootstrap and takes its own lock' ($rel -match 'UNRAVIS_RELEASE_NONCE' -and $rel -match 'Enter-RunLock')
    Assert-That 'bootstrap: a release.ps1 without the protocol marker is not re-run' ($rel -match 'ReleaseProtocol = 2')

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
