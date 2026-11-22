#Requires -Version 7.2
<#
.SYNOPSIS
  Self-test for the release manager's checks (ReleaseLib.ps1): key files and key choice, trusted-key changes, versions, the
  signing tool's dependency rule, the develop side of a promotion merge, the audit log chain, lock, state, and the no-prompt passphrase rules. Also parses every script
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

    # ---- release train: the develop side of a promotion merge (real git, throw-away repo, no network) ----------------------------
    $g = Join-Path $root 'train'
    New-Item -ItemType Directory -Path $g | Out-Null
    function G { git -C $g -c user.name=t -c user.email=t@t -c commit.gpgsign=false @args; if ($LASTEXITCODE -ne 0) { throw "git $args failed" } }
    G init -q -b master
    Set-Content -LiteralPath (Join-Path $g 'a.txt') 'a'; G add -A; G commit -q -m A
    G checkout -q -b develop
    New-Item -ItemType Directory -Path (Join-Path $g 'tools/release') -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $g 'tools/release/x.ps1') 'x'; G add -A; G commit -q -m D1   # a squash commit into develop
    Set-Content -LiteralPath (Join-Path $g 'd.txt') 'd'; G add -A; G commit -q -m D2
    $d1 = (G rev-parse HEAD~1).Trim(); $d2 = (G rev-parse HEAD).Trim()
    G checkout -q master
    Set-Content -LiteralPath (Join-Path $g 'h.txt') 'h'; G add -A; G commit -q -m H                    # a hotfix on master
    $h = (G rev-parse HEAD).Trim()
    G checkout -q -b promote $d2
    G merge -q --no-edit master                                                                          # GitHub's "merge master in"
    $mi = (G rev-parse HEAD).Trim()
    G checkout -q master
    G merge -q --no-ff --no-edit promote                                                                 # the promotion pull request
    $p = (G rev-parse HEAD).Trim()
    $side = Get-PromotionSideCommits $g $p
    Assert-That 'train: a promotion lists every develop commit and the merge of master' ((($side | ForEach-Object Sha) -join ',') -eq "$mi,$d2,$d1") (($side | ForEach-Object Sha) -join ',')
    Assert-That 'train: develop commits must be pull requests' (@($side | Where-Object { $_.Kind -eq 'pr' }).Count -eq 2)
    Assert-That 'train: a clean merge of master is accepted' (@($side | Where-Object { $_.Sha -eq $mi -and $_.Kind -eq 'mergein' -and $_.Ok }).Count -eq 1)
    Assert-That 'train: a plain commit is not a promotion' ((Get-PromotionSideCommits $g $h).Count -eq 0)
    $sens = Get-SensitivePullRequests $g $h $p @{ $p = 7 } @($side | ForEach-Object Sha)
    Assert-That 'train: the sensitive report names the develop commit, not only the promotion' ($sens -contains $d1.Substring(0, 7)) ($sens -join ',')
    Assert-That 'train: develop commits off the sensitive paths are not named' ($sens -notcontains $d2.Substring(0, 7)) ($sens -join ',')
    Assert-That 'train: promotion head refs' ((Test-PromotionHeadRef 'develop') -and (Test-PromotionHeadRef 'promote/1a2b3c4') -and -not (Test-PromotionHeadRef 'feature/promote') -and -not (Test-PromotionHeadRef 'Develop'))
    # A "merge master in" that also changes a file (content no pull request carried) is refused.
    G checkout -q -b promote2 $d2
    G merge -q --no-commit $h
    Set-Content -LiteralPath (Join-Path $g 'evil.txt') 'e'; G add -A; G commit -q -m 'Merge master into promote2'
    $bad = (G rev-parse HEAD).Trim()
    G checkout -q -b master2 $h
    G merge -q --no-ff --no-edit promote2
    $side2 = Get-PromotionSideCommits $g (G rev-parse HEAD).Trim()
    Assert-That 'train: a merge of master with extra changes is refused' (@($side2 | Where-Object { $_.Sha -eq $bad -and -not $_.Ok }).Count -eq 1) (($side2 | ForEach-Object { "$($_.Sha.Substring(0,7)) $($_.Kind) $($_.Ok) $($_.Why)" }) -join '; ')
    # A merge on the develop side whose second parent is not on master (a side branch merged without a pull request) is refused.
    G checkout -q -b side $d1
    Set-Content -LiteralPath (Join-Path $g 's.txt') 's'; G add -A; G commit -q -m S
    G checkout -q -b promote3 $d2
    G merge -q --no-edit side
    $bad3 = (G rev-parse HEAD).Trim()
    G checkout -q -b master3 $h
    G merge -q --no-ff --no-edit promote3
    $side3 = Get-PromotionSideCommits $g (G rev-parse HEAD).Trim()
    Assert-That 'train: a merge of a branch that is not master is refused' (@($side3 | Where-Object { $_.Sha -eq $bad3 -and -not $_.Ok }).Count -eq 1)
    # The generated web files commit of a promotion (docs/pipeline/dist-on-promotion.md): accepted only with its subject, only the three
    # generated folders (case sensitive), and only above every develop pull request commit.
    function Get-Side([string]$Branch, [scriptblock]$Make) {
        G checkout -q -b $Branch $d2
        & $Make
        G checkout -q -b "m-$Branch" $h
        G merge -q --no-ff --no-edit $Branch
        return Get-PromotionSideCommits $g (G rev-parse HEAD).Trim()
    }
    function Set-DistFile([string]$Rel, [string]$Text) { $f = Join-Path $g $Rel; New-Item -ItemType Directory -Path (Split-Path $f) -Force | Out-Null; Set-Content -LiteralPath $f $Text }
    $script:dd = @{}
    $sideA = Get-Side 'promote4' {
        Set-DistFile 'web/dist/index.html' 'built'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.ok = (G rev-parse HEAD).Trim()
        Set-DistFile 'web/dist/assets/old.js' 'x'; G add -A; G commit -q -m 'Update the web files'; $script:dd.subject = (G rev-parse HEAD).Trim()   # generated paths, another subject
    }
    Assert-That 'train: generated files under another subject are treated as a pull request commit' (@($sideA | Where-Object { $_.Sha -eq $script:dd.subject -and $_.Kind -eq 'pr' -and -not $_.Ok }).Count -eq 1)
    Assert-That 'train: a generated commit buried below another commit is refused' (@($sideA | Where-Object { $_.Sha -eq $script:dd.ok -and $_.Kind -eq 'pr' -and -not $_.Ok }).Count -eq 1)
    # A direct-push generated commit with a develop pull request MERGE commit (two parents) on top of it: the position rule must see through
    # the merge commit, as develop's own history is made of them.
    $sideBuried = Get-Side 'promote11' {
        Set-DistFile 'web/dist/evil.js' 'e'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.buried = (G rev-parse HEAD).Trim()
        G checkout -q -b feat11 $d2
        Set-Content -LiteralPath (Join-Path $g 'feat11.txt') 'f'; G add -A; G commit -q -m 'F11'
        G checkout -q promote11
        G merge -q --no-ff -m 'Merge pull request #11 from x/feat11' feat11; $script:dd.prmerge = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a generated commit buried below a pull request merge commit is refused' (@($sideBuried | Where-Object { $_.Sha -eq $script:dd.buried -and $_.Kind -eq 'pr' -and -not $_.Ok }).Count -eq 1) (($sideBuried | ForEach-Object { "$($_.Sha.Substring(0,7)) $($_.Kind) $($_.Ok)" }) -join '; ')
    Assert-That 'train: the pull request merge commit itself is not taken for a merge of master' (@($sideBuried | Where-Object { $_.Sha -eq $script:dd.prmerge -and $_.Kind -eq 'mergein' -and -not $_.Ok }).Count -eq 1)
    $sideB = Get-Side 'promote5' {
        Set-DistFile 'web/dist/index.html' 'built'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.first = (G rev-parse HEAD).Trim()
        G merge -q --no-edit $h   # a re-run after master moved: the merge of master comes after the generated commit
        $script:dd.mi = (G rev-parse HEAD).Trim()
        Set-DistFile 'web/dist/index.html' 'built again'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.second = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a generated commit at the tip end is accepted' (@($sideB | Where-Object { $_.Sha -eq $script:dd.second -and $_.Kind -eq 'dist' -and $_.Ok }).Count -eq 1) (($sideB | ForEach-Object { "$($_.Sha.Substring(0,7)) $($_.Kind) $($_.Ok)" }) -join '; ')
    Assert-That 'train: a re-run (generated, merge of master, generated) is accepted' (@($sideB | Where-Object { $_.Sha -eq $script:dd.first -and $_.Kind -eq 'dist' -and $_.Ok }).Count -eq 1 -and @($sideB | Where-Object { $_.Sha -eq $script:dd.mi -and $_.Kind -eq 'mergein' -and $_.Ok }).Count -eq 1)
    $sideC = Get-Side 'promote6' {
        Set-DistFile 'web/dist/index.html' 'built'; Set-DistFile 'sneaky.txt' 's'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.sneaky = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: that subject with another file in the commit is treated as a pull request commit' (@($sideC | Where-Object { $_.Sha -eq $script:dd.sneaky -and $_.Kind -eq 'pr' -and -not $_.Ok }).Count -eq 1)
    $sideD = Get-Side 'promote7' {
        Set-DistFile 'cmd/unravis-setup/UI/x.js' 'x'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.upper = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a path with another letter case (UI, not ui) is refused' (@($sideD | Where-Object { $_.Sha -eq $script:dd.upper -and $_.Kind -eq 'pr' -and -not $_.Ok }).Count -eq 1)
    $sideE = Get-Side 'promote8' {
        Set-DistFile 'web/dist/index.html' 'built'; Set-DistFile 'web/dist/assets/gone.js' 'x'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'
        G rm -q web/dist/assets/gone.js; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.del = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a generated commit that only deletes generated files is accepted' (@($sideE | Where-Object { $_.Sha -eq $script:dd.del -and $_.Kind -eq 'dist' -and $_.Ok }).Count -eq 1)
    $sideF = Get-Side 'promote9' {
        Set-DistFile "web/dist/$([char]0xE9).js" 'x'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.accent = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a non-ASCII generated file name is not misread (quotepath)' (@($sideF | Where-Object { $_.Sha -eq $script:dd.accent -and $_.Kind -eq 'dist' -and $_.Ok }).Count -eq 1)
    # A merge commit with that subject (two parents) is never the generated commit.
    G checkout -q -b side2 $d1
    Set-DistFile 'web/dist/s.js' 's'; G add -A; G commit -q -m S2
    $sideG = Get-Side 'promote10' {
        G merge -q --no-edit --no-ff -m 'Rebuild web files for promotion 1a2b3c4' side2; $script:dd.mergey = (G rev-parse HEAD).Trim()
    }
    Assert-That 'train: a merge commit with the generated subject is not the generated commit' (@($sideG | Where-Object { $_.Sha -eq $script:dd.mergey -and $_.Kind -ne 'dist' }).Count -eq 1)

    # ---- Promote-Develop.ps1 reuses a branch only when the generated commit sits where Get-PromotionSideCommits wants it ----------
    # (sweep 2026-11-22, finding 5). Both sides use Get-BuriedGeneratedCommits for the position; Promote-Develop builds its chain with
    # Get-PromoteBranchChain. Callers wrap the call in @(): zero, one and many results must all count right.
    function New-Chain([string[]]$Kinds) { $list = @(); for ($i = 0; $i -lt $Kinds.Count; $i++) { $list += [pscustomobject]@{ Sha = "s$($i + 1)"; Kind = $Kinds[$i] } }; return , $list }
    function Get-Buried([string[]]$Kinds) { return , @(Get-BuriedGeneratedCommits (New-Chain $Kinds)) }
    Assert-That 'position rule: a clean chain (generated commit on top) buries nothing, and Count is 0' ((Get-Buried 'dist').Count -eq 0)
    Assert-That 'position rule: merges of master and further generated commits above it are fine' ((Get-Buried 'dist', 'mergein', 'dist', 'mergein').Count -eq 0)
    Assert-That 'position rule: a merge of master on top of a generated commit is fine (the re-run after master moved)' ((Get-Buried 'mergein', 'dist').Count -eq 0)
    Assert-That 'position rule: a develop pull request commit above a generated commit buries exactly that one' ((Get-Buried 'other', 'dist').Count -eq 1 -and (Get-Buried 'other', 'dist')[0] -eq 's2')
    Assert-That 'position rule: a merge that is not a clean merge of master (kind other) above it buries the ones below' (((Get-Buried 'dist', 'other', 'dist', 'dist') -join ',') -eq 's3,s4')
    Assert-That 'position rule: an empty chain (branch equals the chosen commit) buries nothing' ((Get-Buried @()).Count -eq 0)
    Assert-That 'position rule: Get-PromoteBranchChain of an empty line is empty, not an array holding an array' (@(Get-PromoteBranchChain 'x' 'x' { throw 'not called' } @{}).Count -eq 0)

    # The code Promote-Develop runs (Get-PromoteBranchChain, then Get-BuriedGeneratedCommits) over real git, against the answer of
    # Get-PromotionSideCommits (what Release.ps1 decides) for the same branch. $ReadCommit here is what the GitHub API gives there.
    $readGit = {
        param($c)
        $cp = @((git -C $g rev-list --parents -n 1 $c) -split ' ' | Select-Object -Skip 1)
        $subj = (git -C $g log -n 1 --format=%s $c)
        $gen = $false
        if ($cp.Count -eq 1) {
            $names = @(git -C $g -c core.quotepath=off diff --no-renames --name-only "$c^1" $c)
            $gen = ($names.Count -gt 0 -and @($names | Where-Object { $_ -cnotmatch '^(web/dist|cmd/unravis-setup/ui|cmd/unravis-lite/ui)/' }).Count -eq 0)
        }
        [pscustomobject]@{ Parents = $cp; Subject = "$subj"; IsGenerated = $gen }
    }
    function Get-PromoteDecision([string]$Branch, [string]$Master) {
        $tip = (git -C $g rev-parse $Branch).Trim()
        $mi = @{}
        foreach ($c in @(git -C $g rev-list --first-parent "$d2..$tip")) { $mi[$c] = Test-CleanMasterMerge $g $c $Master }
        $chain = @(Get-PromoteBranchChain $tip $d2 $readGit $mi)
        return [pscustomobject]@{ Chain = $chain; Buried = @(Get-BuriedGeneratedCommits $chain) }
    }
    # The release manager's refusals of generated-looking commits: subject and shape right, yet not accepted as 'dist'.
    function Get-ReleaseRefusedGenerated([object[]]$Side) {
        return @($Side | Where-Object { $_.Kind -eq 'pr' -and -not $_.Ok -and (& $readGit $_.Sha).IsGenerated -and (& $readGit $_.Sha).Subject.StartsWith('Rebuild web files for promotion') } | ForEach-Object Sha)
    }
    # Branch, the scenario's side list from Get-PromotionSideCommits, the commits that must be refused as buried.
    $sideH = Get-Side 'promote12' {
        Set-DistFile 'web/dist/index.html' 'built'; G add -A; G commit -q -m 'Rebuild web files for promotion 1a2b3c4'; $script:dd.h1 = (G rev-parse HEAD).Trim()
        G merge -q --no-edit --no-ff -m 'Merge master into promote/12' $h   # a clean merge of master ON TOP of the generated commit
        $script:dd.h2 = (G rev-parse HEAD).Trim()
    }
    $scen = @(
        @{ Name = 'promote4';  Side = $sideA;      Buried = @($script:dd.ok) },        # generated commit below a commit with another subject
        @{ Name = 'promote11'; Side = $sideBuried; Buried = @($script:dd.buried) },    # below a pull request merge commit
        @{ Name = 'promote5';  Side = $sideB;      Buried = @() },                     # generated, merge of master, generated
        @{ Name = 'promote12'; Side = $sideH;      Buried = @() },                     # generated, then a clean merge of master on top
        @{ Name = 'promote6';  Side = $sideC;      Buried = @() },                     # other file in the commit: it is 'other', nothing is below it
        @{ Name = 'promote8';  Side = $sideE;      Buried = @() }
    )
    foreach ($sc in $scen) {
        $dec = Get-PromoteDecision $sc.Name $h
        $want = @($sc.Buried | Sort-Object)
        $gotP = @($dec.Buried | Sort-Object)
        $gotR = @(Get-ReleaseRefusedGenerated $sc.Side | Sort-Object)
        Assert-That "position rule: $($sc.Name): Promote-Develop buries exactly [$($want -join ',')]" (($gotP -join ',') -eq ($want -join ',') -and $gotP.Count -eq $want.Count) "got [$($gotP -join ',')]"
        # Release.ps1 refuses exactly the commits Promote-Develop calls buried (a generated commit that fails its own test is a pull request commit to both).
        Assert-That "position rule: $($sc.Name): Release.ps1's refused generated commits equal Promote-Develop's" ((@($gotR | Where-Object { $want -contains $_ }) -join ',') -eq ($want -join ',')) "release refused [$($gotR -join ',')]"
        Assert-That "position rule: $($sc.Name): both sides agree on accept or refuse" (($dec.Buried.Count -gt 0) -eq ($gotR.Count -gt 0)) "promote buried $($dec.Buried.Count), release refused $($gotR.Count)"
    }
    Assert-That 'position rule: the check is not vacuous (two scenarios are buried, four are clean)' (@($scen | Where-Object { $_.Buried.Count -gt 0 }).Count -eq 2 -and @($scen | Where-Object { $_.Buried.Count -eq 0 }).Count -eq 4)
    $pd = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Promote-Develop.ps1')
    Assert-That 'promote: branch reuse runs Get-PromoteBranchChain and Get-BuriedGeneratedCommits (wrapped in @()) before a pull request exists' ($pd -match '\$chain = @\(Get-PromoteBranchChain' -and $pd -match '\$buried = @\(Get-BuriedGeneratedCommits \$chain\)' -and $pd -match '\$buried\.Count -gt 0' -and $pd.IndexOf('$buried = @(Get-BuriedGeneratedCommits') -lt $pd.IndexOf('gh pr list'))
    Assert-That 'promote: a generated-looking commit is accepted only through the position rule' ($pd -notmatch '\$np = \[int\]' -and $pd -match 'okDist')

    # ---- promotion safety (SUP-1): only the head that was built and checked is merged; "Merge master into" is a claim, not a proof ----
    $sha40 = 'a' * 40; $other40 = 'b' * 40
    Assert-That 'promote head: the expected head passes' ($null -eq (Get-ThrownMessage { Assert-PromotionHead $sha40 $sha40 'promote/aaaaaaa' }))
    $msgHead = Get-ThrownMessage { Assert-PromotionHead $sha40 $other40 'promote/aaaaaaa' }
    Assert-That 'promote head: a different head is refused with a clear message' ($msgHead -and $msgHead -match 'Someone pushed' -and $msgHead -match 'Nothing was merged') "$msgHead"
    Assert-That 'promote head: an empty head is refused' ($null -ne (Get-ThrownMessage { Assert-PromotionHead $sha40 '' 'promote/aaaaaaa' }))
    Assert-That 'promote head: no expected head is refused' ($null -ne (Get-ThrownMessage { Assert-PromotionHead '' $sha40 'promote/aaaaaaa' }))

    $mr = Join-Path $root 'mergein'
    New-Item -ItemType Directory -Path $mr | Out-Null
    function M { git -C $mr -c user.name=t -c user.email=t@t -c commit.gpgsign=false @args; if ($LASTEXITCODE -ne 0) { throw "git $args failed" } }
    M init -q -b master
    Set-Content -LiteralPath (Join-Path $mr 'a.txt') 'a'; M add -A; M commit -q -m A
    M checkout -q -b develop
    Set-Content -LiteralPath (Join-Path $mr 'd.txt') 'd'; M add -A; M commit -q -m D1
    $mDev = (M rev-parse HEAD).Trim()
    M checkout -q master
    Set-Content -LiteralPath (Join-Path $mr 'h.txt') 'h'; M add -A; M commit -q -m H
    $mHot = (M rev-parse HEAD).Trim()
    # good: what GitHub's merges API makes (a clean merge of master into the promote branch)
    M checkout -q -b promote/good $mDev
    M merge -q --no-ff -m 'Merge master into promote/good' master
    $mGood = (M rev-parse HEAD).Trim()
    # bad 1: a merge of master that also carries a file no pull request had, under the same subject
    M checkout -q -b promote/extra $mDev
    M merge -q --no-commit --no-ff master
    Set-Content -LiteralPath (Join-Path $mr 'evil.txt') 'e'; M add -A; M commit -q -m 'Merge master into promote/extra'
    $mExtra = (M rev-parse HEAD).Trim()
    # bad 2: one parent, only the subject says "Merge master into" (code pushed by hand)
    M checkout -q -b promote/plain $mDev
    Set-Content -LiteralPath (Join-Path $mr 'evil2.txt') 'e'; M add -A; M commit -q -m 'Merge master into promote/plain'
    $mPlain = (M rev-parse HEAD).Trim()
    # bad 3: a merge whose second parent is not on master
    M checkout -q -b side $mDev
    Set-Content -LiteralPath (Join-Path $mr 's.txt') 's'; M add -A; M commit -q -m S
    M checkout -q -b promote/side $mDev
    M merge -q --no-ff -m 'Merge master into promote/side' side
    $mSide = (M rev-parse HEAD).Trim()
    foreach ($case in @(@('good', $mGood, $true), @('extra', $mExtra, $false), @('plain', $mPlain, $false), @('side', $mSide, $false))) {
        $v = Test-RemoteMergeIns $mr "promote/$($case[0])" @($case[1]) ''
        Assert-That "promote merge-in: '$($case[0])' is $(if ($case[2]) { 'accepted' } else { 'refused' })" ($v.ContainsKey($case[1]) -and ($v[$case[1]].Ok -eq $case[2])) ($v.Values | ForEach-Object { $_.Why }) 
    }
    Assert-That 'promote merge-in: a sha that is not on the branch is refused' (-not (Test-RemoteMergeIns $mr 'promote/good' @($sha40) '')[$sha40].Ok)
    $envBefore = "$env:GIT_CONFIG_COUNT|$env:GIT_CONFIG_KEY_0|$env:GIT_CONFIG_VALUE_0|$env:GIT_TERMINAL_PROMPT"
    $leftBefore = @(Get-ChildItem ([System.IO.Path]::GetTempPath()) -Filter 'unravis-mergein-*' -ErrorAction SilentlyContinue).Count
    $v2 = Test-RemoteMergeIns $mr 'promote/good' @($mGood) 'tok-not-printed'
    Assert-That 'promote merge-in: the throw-away repository is removed and the git environment is put back' ($v2[$mGood].Ok -and "$env:GIT_CONFIG_COUNT|$env:GIT_CONFIG_KEY_0|$env:GIT_CONFIG_VALUE_0|$env:GIT_TERMINAL_PROMPT" -eq $envBefore -and @(Get-ChildItem ([System.IO.Path]::GetTempPath()) -Filter 'unravis-mergein-*' -ErrorAction SilentlyContinue).Count -eq $leftBefore)

    $pd = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Promote-Develop.ps1')
    Assert-That 'promote: the merge is pinned to the expected head, not to the head read last' ($pd -match '--match-head-commit \$expectedHead' -and $pd -notmatch '--match-head-commit \$head\b')
    Assert-That 'promote: the head is compared on every wait loop and once before the pull request' ($pd -match '(?s)\$head = .*?headRefOid.*?Assert-HeadIsExpected \$head' -and $pd -match '(?s)Assert-HeadIsExpected \(\(Invoke-Gh api[^\r\n]*git/ref/heads/\$branch')
    Assert-That 'promote: the expected head is the DIST_COMMIT sha, else the head after the merge' ($pd -match '\$expectedHead = if \(\$distSha -ne ''none''\) \{ \$distSha \} else \{ \$headAfterMerge \}')
    Assert-That 'promote: a "Merge master into" subject alone is not enough' ($pd -match 'Test-RemoteMergeIns' -and $pd -notmatch '(?m)^\s*if \(\$emsg -like "Merge master into \$branch\*"\) \{ continue \}')
    Assert-That 'promote: the reused branch is judged at the commit read first, not at its name' ($pd -match 'compare/\$\{sha\}\.\.\.\$\{existing\}')

    # A pull request head that GitHub shows late is not "someone pushed"; one that stays different is.
    $seq = [System.Collections.Generic.Queue[string]]::new(); foreach ($x in @($other40, $other40, $sha40)) { $seq.Enqueue($x) }
    $lag = Read-SettledPromotionHead { $seq.Dequeue() } $sha40 3 0
    Assert-That 'promote head: a lagging head that catches up on a later read is accepted' ($lag -eq $sha40 -and $seq.Count -eq 0) "$lag"
    $seq2 = [System.Collections.Generic.Queue[string]]::new(); foreach ($x in @($sha40, $other40)) { $seq2.Enqueue($x) }
    $fast = Read-SettledPromotionHead { $seq2.Dequeue() } $sha40 3 0
    Assert-That 'promote head: a matching first read stops reading' ($fast -eq $sha40 -and $seq2.Count -eq 1)
    $rdCnt = [pscustomobject]@{ n = 0 }
    $stuck = Read-SettledPromotionHead { $rdCnt.n++; $other40.ToUpperInvariant() } $sha40 3 0
    Assert-That 'promote head: a head that stays different is read 3 times and then refused' ($rdCnt.n -eq 3 -and (Get-ThrownMessage { Assert-PromotionHead $sha40 $stuck 'promote/aaaaaaa' }) -match 'Someone pushed') "$($rdCnt.n) $stuck"
    Assert-That 'promote head: the wait loop reads through Read-SettledPromotionHead' ((Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Promote-Develop.ps1')) -match 'Read-SettledPromotionHead \{ Invoke-Gh pr view')

    # New-ToolWorktree: a build that fails leaves no registered worktree behind (Make-Keys, Set-Control and Refresh-Feed call it).
    $tw = Join-Path $root 'toolwt'
    New-Item -ItemType Directory -Path (Join-Path $tw 'tools/release/sigtool') -Force | Out-Null
    function TW { git -C $tw -c user.name=t -c user.email=t@t -c commit.gpgsign=false @args; if ($LASTEXITCODE -ne 0) { throw "git $args failed" } }
    TW init -q -b master
    Set-Content -LiteralPath (Join-Path $tw 'go.mod') "module example.test/tw`n`ngo 1.21`n"
    Set-Content -LiteralPath (Join-Path $tw 'tools/release/sigtool/main.go') "package main`nfunc main() { this is not go }`n"
    TW add -A; TW commit -q -m broken
    $twWork = Join-Path $root 'toolwt-work'; New-Item -ItemType Directory -Path $twWork | Out-Null
    $twErr = Get-ThrownMessage { New-ToolWorktree $tw 'HEAD' $twWork }
    Remove-Item -LiteralPath $twWork -Recurse -Force -ErrorAction SilentlyContinue   # what the callers' finally blocks do with their work folder
    $twList = @(git -C $tw worktree list --porcelain)
    Assert-That 'tool worktree: a failed build is reported' ($twErr -match 'building the signing tool failed') "$twErr"
    Assert-That 'tool worktree: a failed build leaves no registered or prunable worktree' (@($twList | Where-Object { $_ -like 'worktree *' }).Count -eq 1 -and @($twList | Where-Object { $_ -like 'prunable*' }).Count -eq 0 -and -not (Test-Path (Join-Path $tw '.git/worktrees/src'))) ($twList -join '; ')
    Set-Content -LiteralPath (Join-Path $tw 'tools/release/sigtool/main.go') "package main`nfunc main() {}`n"
    TW add -A; TW commit -q -m fixed
    $twWork2 = Join-Path $root 'toolwt-work2'; New-Item -ItemType Directory -Path $twWork2 | Out-Null
    $twOk = New-ToolWorktree $tw 'HEAD' $twWork2
    Assert-That 'tool worktree: a good build returns the tool and the worktree' ((Test-Path -LiteralPath $twOk.Tool) -and (Test-Path -LiteralPath $twOk.Dir))
    Remove-ToolWorktree $tw $twOk.Dir
    Assert-That 'tool worktree: Remove-ToolWorktree unregisters it' (@(git -C $tw worktree list --porcelain | Where-Object { $_ -like 'worktree *' }).Count -eq 1)

    # ---- Make-Keys (SUP-3): the Go environment is neutralised and the key tool is built from a throw-away worktree of HEAD --------
    $mk = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Make-Keys.ps1')
    $mkTok = $null; $mkErr = $null
    $mkAst = [System.Management.Automation.Language.Parser]::ParseInput($mk, [ref]$mkTok, [ref]$mkErr)
    $mkCmds = @($mkAst.FindAll({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true))
    $dotLib = @($mkCmds | Where-Object { $_.InvocationOperator -eq 'Dot' -and $_.Extent.Text -match 'ReleaseLib\.ps1' })
    $newWt = @($mkCmds | Where-Object { $_.GetCommandName() -eq 'New-ToolWorktree' })
    $goBuild = @($mkCmds | Where-Object { $_.GetCommandName() -eq 'go' })
    Assert-That 'make-keys: ReleaseLib (GOFLAGS, GOWORK, GOENV, GOTOOLCHAIN) is loaded before anything is built' ($dotLib.Count -eq 1 -and $newWt.Count -eq 1 -and $dotLib[0].Extent.StartOffset -lt $newWt[0].Extent.StartOffset)
    Assert-That 'make-keys: it never runs go itself (no build in the working folder)' ($goBuild.Count -eq 0)
    Assert-That 'make-keys: the throw-away worktree is made from the checked HEAD' ($newWt.Count -eq 1 -and $newWt[0].Extent.Text -match '\$head')
    $hostPs = (Get-Process -Id $PID).Path
    # What the dot-sourced file does to the environment, with hostile values set first (a child shell, so this run's own env is untouched).
    $goEnv = & $hostPs -NoProfile -Command "`$env:GOFLAGS='-overlay=x.json'; `$env:GOWORK='x.work'; `$env:GOENV='x.env'; `$env:GOTOOLCHAIN='local'; . '$((Join-Path $PSScriptRoot 'ReleaseLib.ps1') -replace "'", "''")'; `"`$env:GOFLAGS|`$env:GOWORK|`$env:GOENV|`$env:GOTOOLCHAIN`""
    Assert-That 'make-keys: the library neutralises hostile Go settings' ("$goEnv".Trim() -eq '|off|off|auto') "$goEnv"

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

    # ---- run logs for the dashboard: secrets never reach the file, and the tee never changes what the run does -------------------
    $red = New-LogRedactor @('DESKTOP-ABC', 'nachi')
    $fakeTok = 'ghp_' + ('a1B2' * 9)
    $cases = [ordered]@{
        "push with $fakeTok done"                  = '<token hidden>'
        'Authorization: Bearer abcdefghijklmnop1234' = '<hidden>'
        'passphrase=hunter2hunter2'                = '<hidden>'
        'loaded C:\Users\nachi\keys\release.key'   = '<key file>'
        'on DESKTOP-ABC as someone'                = '<name>'
        "$([char]27)[36;1mcolour$([char]27)[0m"    = 'colour'
        '/home/runner/work and /Users/bob/x'       = '<user>'
    }
    foreach ($k in $cases.Keys) {
        $got = Protect-LogLine $k $red
        Assert-That "log redaction: $($cases[$k])" ($got.Contains($cases[$k])) "got '$got'"
        Assert-That "log redaction: nothing secret left in '$($cases[$k])' case" (-not ($got -match 'ghp_a1B2|hunter2|release\.key|DESKTOP-ABC|nachi|abcdefghijklmnop1234')) "got '$got'"
    }
    $red2 = New-LogRedactor @('Abhinav')
    foreach ($c in @(@('GH_TOKEN=abc123secret', 'abc123secret'), @('SIGNING_PASSPHRASE=hunter2', 'hunter2'), @('keyPassword=hunter2', 'hunter2'), @('passphrase: correct horse battery', 'battery'), @('-----BEGIN PGP PRIVATE KEY BLOCK-----', 'BEGIN PGP'))) {
        $got = Protect-LogLine $c[0] (New-LogRedactor)
        # The label must not print the raw case: a BEGIN header in a check name would itself be hidden by the redactor in the CI log.
        Assert-That "log redaction: a $($c[0].Length)-character case hides its secret" (-not $got.Contains($c[1])) "got '$got'"
    }
    $uk = New-LogRedactor
    $ukOut = '-----BEGIN PGP PRIVATE KEY BLOCK-----', 'ok   log redaction: nothing secret left', '--- PASS: TestX (0.00s)' | ForEach-Object { Protect-LogLine $_ $uk }
    Assert-That 'log redaction: a key header with no END line hides only itself' (($ukOut -join '|') -eq '<private key hidden>|ok   log redaction: nothing secret left|--- PASS: TestX (0.00s)') "got '$($ukOut -join '|')'"
    $ck = New-LogRedactor
    $pre = "job`tstep`t2026-11-22T11:32:17.6419460Z "
    $ckOut = @('-----BEGIN RSA PRIVATE KEY-----', 'MIIEvQIBADANBgkqhkiG9w0B', 'Proc-Type: 4,ENCRYPTED', 'AAAABBBB', '-----END RSA PRIVATE KEY-----', 'ok   after the key') | ForEach-Object { Protect-LogLine ($pre + $_) $ck -KeepTabs }
    Assert-That 'log redaction: a key block behind a CI prefix is hidden whole and the next line is kept' ((($ckOut | Select-Object -First 5) -join '|') -eq ((1..5 | ForEach-Object { '<private key hidden>' }) -join '|') -and $ckOut[5].EndsWith('ok   after the key')) "got '$($ckOut -join '|')'"
    $ck2 = New-LogRedactor
    $ck2Out = @('-----BEGIN PRIVATE KEY-----', 'MIIEvQIBADANBgkqhkiG9w0B', 'ok   a sentence ends a block that lost its END line') | ForEach-Object { Protect-LogLine ($pre + $_) $ck2 -KeepTabs }
    Assert-That 'log redaction: a block that lost its END line ends at the first sentence' ($ck2Out[2].EndsWith('ok   a sentence ends a block that lost its END line')) "got '$($ck2Out -join '|')'"
    $b64 = 'MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7'
    foreach ($ep in @('#7 0.512 ', "     2`t", '> ', '+ ', '"')) {
        $ek = New-LogRedactor
        $ekOut = @('-----BEGIN PRIVATE KEY-----', $b64, $b64, 'Xk2pQ9=', '-----END PRIVATE KEY-----') | ForEach-Object { Protect-LogLine ($pre + $ep + $_) $ek -KeepTabs }
        Assert-That "log redaction: key lines echoed with the prefix '$($ep.Trim())' stay hidden" (-not (($ekOut -join '|') -match 'MIIE|Xk2p|Bgkq')) "got '$($ekOut -join '|')'"
    }
    $ek2 = New-LogRedactor
    $null = Protect-LogLine '-----BEGIN PRIVATE KEY-----' $ek2
    Assert-That 'log redaction: a no-break space line ends an unclosed block, as in Go' ((Protect-LogLine ([string][char]0xA0 + 'Xk2pQ9=') $ek2) -ne '<private key hidden>')
    Assert-That 'log redaction: a passed-test line is kept'((Protect-LogLine '--- PASS: TestX (0.00s)' (New-LogRedactor)) -eq '--- PASS: TestX (0.00s)')
    $gotUrl = Protect-LogLine 'https://github.com/abhinavjp/unravis/actions/runs/1 by Abhinav' $red2
    Assert-That 'log redaction: a user name inside a repository name is kept, the name alone is hidden' ($gotUrl.Contains('github.com/abhinavjp/unravis') -and -not $gotUrl.Contains('by Abhinav')) "got '$gotUrl'"
    $kr = New-LogRedactor
    $blk = 'before', '-----BEGIN PRIVATE KEY-----', 'MIIEvQIBADANBgkqhkiG9w0B', '-----END PRIVATE KEY-----', 'after' | ForEach-Object { Protect-LogLine $_ $kr }
    Assert-That 'log redaction: a private key block is hidden line by line' (($blk -join '|') -eq 'before|<private key hidden>|<private key hidden>|<private key hidden>|after')
    Assert-That 'log redaction: an ordinary line is unchanged' ((Protect-LogLine 'ok  pkg  1.2s' (New-LogRedactor)) -eq 'ok  pkg  1.2s')
    Assert-That 'log redaction: -KeepTabs keeps the tab separators of a CI log line' ((Protect-LogLine "job`tstep`ttext" (New-LogRedactor) -KeepTabs) -eq "job`tstep`ttext")

    $logs = Get-ReleaseLogDir $env:UNRAVIS_RELEASE_DATA
    $runLog = New-RunLogPath $logs
    Assert-That 'run log: the file name is run-<utc time>.log' ((Split-Path $runLog -Leaf) -match '^run-\d{8}T\d{6}Z\.log$')
    $hostExe = (Get-Process -Id $PID).Path
    $child = "Write-Host '[1/7] Preflight'; Write-Host 'token ' + '$fakeTok'.ToString(); Write-Host 'line two'; exit 7"
    $seen = @(& $hostExe -NoProfile -Command $child | Write-ReleaseRunLog -Path $runLog | ForEach-Object { "$_" })
    Assert-That 'run log: the exit code of the run survives the tee' ($LASTEXITCODE -eq 7) "exit $LASTEXITCODE"
    Assert-That 'run log: every line still reaches the screen, unchanged' ($seen.Count -eq 3 -and $seen[0] -eq '[1/7] Preflight' -and $seen[2] -eq 'line two') ($seen -join '|')
    $saved = @(Get-Content -LiteralPath $runLog)
    Assert-That 'run log: one time-stamped line per output line' ($saved.Count -eq 3 -and ($saved | Where-Object { $_ -notmatch '^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ ' }).Count -eq 0) ($saved -join '|')
    Assert-That 'run log: the saved copy has the token hidden' (-not ($saved -join "`n").Contains('ghp_a1B2')) ($saved -join '|')
    $big = New-RunLogPath $logs; $big = $big -replace '\d{6}Z', '000001Z'
    $n = @(1..30 | Write-ReleaseRunLog -Path $big -MaxLines 10 | ForEach-Object { $_ })
    Assert-That 'run log: a cap stops the file, not the run' ($n.Count -eq 30 -and @(Get-Content -LiteralPath $big).Count -eq 11)
    $blocked = Join-Path $logs 'is-a-folder.log'; New-Item -ItemType Directory -Path $blocked | Out-Null
    $n = @(1..3 | Write-ReleaseRunLog -Path $blocked | ForEach-Object { $_ })
    Assert-That 'run log: a file that cannot be written does not stop the run' ($n.Count -eq 3)

    foreach ($i in 1..8) { Set-Content -LiteralPath (Join-Path $logs ('run-2026010{0}T000000Z.log' -f $i)) -Value 'x'; (Get-Item (Join-Path $logs ('run-2026010{0}T000000Z.log' -f $i))).LastWriteTimeUtc = [datetime]"2026-01-0${i}T00:00:00Z" }
    Set-Content -LiteralPath (Join-Path $logs 'notes.txt') -Value 'keep me'
    Remove-OldReleaseLogs $logs 5
    $left = @(Get-ChildItem -LiteralPath $logs -File | Where-Object { $_.Name -match '^(run-|ci-)' })
    Assert-That 'run log: only the newest N log files are kept' ($left.Count -eq 5) "$($left.Count) left"
    Assert-That 'run log: other files in the folder are never touched' (Test-Path -LiteralPath (Join-Path $logs 'notes.txt'))
    Assert-That 'ci log: a bad run id, tag or kind is refused before anything runs' (-not (Save-CiRunLog 'o/r' '12; calc' 'v1.0.0' 'ci' $logs) -and -not (Save-CiRunLog 'o/r' '12' '../x' 'ci' $logs) -and -not (Save-CiRunLog 'o/r' '12' 'v1.0.0' 'other' $logs))

    $aud = Join-Path $env:UNRAVIS_RELEASE_DATA 'audit-runs.jsonl'
    $now = [DateTime]::UtcNow
    foreach ($e in @(@{ event = 'built'; version = '9.9.9-rc.1'; run = '111' }, @{ event = 'verify-failed'; version = '9.9.9-rc.1'; run = '222' }, @{ event = 'verified'; version = '9.9.9-rc.1'; run = '333' }, @{ event = 'built'; version = '9.9.8'; run = '444' }, @{ event = 'built'; version = '9.9.9-rc.1'; run = 'not-a-number' })) { Write-AuditEntry $aud $e }
    $ids = Get-ReleaseAuditRuns $aud '9.9.9-rc.1' $now.AddMinutes(-5)
    Assert-That 'ci log: run ids come from this version''s audit entries (newest of each kind, digits only)' ($ids.release -eq '111' -and $ids.verify -eq '333') "$($ids | ConvertTo-Json -Compress)"
    $ids = Get-ReleaseAuditRuns $aud '9.9.9-rc.1' $now.AddMinutes(5)
    Assert-That 'ci log: entries older than this run are ignored' ($ids.release -eq '' -and $ids.verify -eq '')
    $rel2 = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'Release.ps1')
    Assert-That 'bootstrap: the worker output goes through the run log tee and the exit code is read right after' ($rel2 -match 'Write-ReleaseRunLog -Path \$runLog' -and $rel2 -match '(?s)finally \{\s*Remove-Item Env:UNRAVIS_RELEASE_NONCE[^\r\n]*\r?\n[^\r\n]*\r?\n\s*\}\s*\r?\n\s*\$code = \$LASTEXITCODE')
    Assert-That 'bootstrap: the console is read as UTF-8 around the worker and put back afterwards (no check mark shown as "Γ£ô")' ($rel2 -match '(?s)\$prevEnc = \[Console\]::OutputEncoding; \[Console\]::OutputEncoding = \[System\.Text\.UTF8Encoding\]::new\(\$false\).*?& \$exe @fwd.*?if \(\$prevEnc\) \{[^\r\n]*OutputEncoding = \$prevEnc')
    Assert-That 'worker: prints UTF-8 back to the bootstrap' ($rel2 -match '(?s)Remove-Item Env:UNRAVIS_RELEASE_NONCE -ErrorAction SilentlyContinue\r?\n#[^\r\n]*\r?\ntry \{ \[Console\]::OutputEncoding = \[System\.Text\.UTF8Encoding\]::new\(\$false\) \} catch \{\}')
    $prevEnc = [Console]::OutputEncoding
    try {
        [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
        $u8 = (New-RunLogPath $logs) -replace '\d{2}Z', '59Z'
        $mark = [string][char]0x2713
        $child8 = "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new(`$false); [Console]::Out.Write(([string][char]0x2713) + ' ok' + [char]10); [Console]::Out.Flush()"
        $seen8 = @(& $hostExe -NoProfile -Command $child8 | Write-ReleaseRunLog -Path $u8 | ForEach-Object { "$_" })
        $saved8 = @(Get-Content -LiteralPath $u8 -Encoding UTF8)
        Assert-That 'run log: a check mark printed as UTF-8 reaches the screen and the saved log intact' ($seen8.Count -eq 1 -and $seen8[0] -eq "$mark ok" -and $saved8.Count -eq 1 -and $saved8[0].EndsWith("$mark ok")) (($seen8 + $saved8) -join '|')
    } finally { [Console]::OutputEncoding = $prevEnc }
    Assert-That 'bootstrap: CI logs are saved only after the locks are free' ($rel2 -match '(?s)\$osLock\.Dispose\(\) \}\s*#[^\r\n]*\r?\n\s*if \(-not \$CheckOnly -and \$st[^\r\n]*Save-ReleaseRunLogs')
} finally {
    Remove-Item Env:UNRAVIS_RELEASE_DATA -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host "`n$($script:checks) checks, $($script:failures.Count) failed."
if ($script:failures.Count) { exit 1 }
exit 0
