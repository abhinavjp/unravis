#Requires -Version 7.2
<#
.SYNOPSIS  Tests Invoke-RcSlot.ps1 with fake data only: no network, no gh, no git against a real repository. Everything outside the process is replaced
           by small fakes; files go to a throwaway folder under %TEMP%. Prints PASS or FAIL per case, exit 1 on any failure.
#>
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$script:RcSlotPath = Join-Path $here 'Invoke-RcSlot.ps1'
. $script:RcSlotPath

$fail = 0
function Check { param([bool]$Cond, [string]$Name) if ($Cond) { Write-Host "PASS  $Name" } else { Write-Host "FAIL  $Name"; $script:fail++ } }
function Same { param($A, $B, [string]$Name) $ok = ($A -ceq $B); Check $ok $Name; if (-not $ok) { Write-Host "      got '$A', want '$B'" } }

$base = Join-Path ([IO.Path]::GetTempPath()) ('rcslot-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$sha = 'a' * 40
$sha2 = 'b' * 40
function New-StateDir { $d = Join-Path $base ([guid]::NewGuid().ToString('N').Substring(0, 6)); return $d }
function New-LockApi {
    param([string]$Json, [int]$Code = 0)
    $c = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Json))
    return [pscustomobject]@{ Code = $Code; Lines = @((@{ content = $c; sha = 'x' } | ConvertTo-Json -Compress)) }
}
function Ok { param([string[]]$Lines = @()) return [pscustomobject]@{ Code = 0; Lines = $Lines } }
function Bad { param([string[]]$Lines = @('boom'), [int]$Code = 1) return [pscustomobject]@{ Code = $Code; Lines = $Lines } }

try {
    New-Item -ItemType Directory -Force -Path $base | Out-Null

    # ---- product vs docs/test paths
    foreach ($p in 'internal/a/b.go', 'cmd/x/main.go', 'web/src/App.svelte', 'web/src/lib/a.ts', 'tools/release/Release.ps1', 'tools/release/promote-dist.mjs',
                   'go.mod', 'go.sum', 'main.go', 'internal/my pkg/file name.go', 'internal\a.go', '"internal/q r.go"') {
        Check (Test-ProductPath $p) "product: $p"
    }
    foreach ($p in 'internal/a/b_test.go', 'web/src/a.test.ts', 'web/src/a.spec.ts', 'web/src/a.test.js', 'internal/x/testdata/f.go', 'internal/x/tests/f.go',
                   'web/src/__tests__/a.ts', 'tools/release/Test-Release.ps1', 'tools/release/Test-NoUpx.Tests.ps1', 'docs/ci/RC-CADENCE.md', 'internal/x/README.md',
                   'web/src/readme.md', 'tools/release/README.md', 'internal/my pkg/a b_test.go', 'docs/my notes.md', 'docs/a.go', '.github/workflows/ci.yml',
                   'web/package.json', 'tools/ci/x.mjs', 'go.work', '') {
        Check (-not (Test-ProductPath $p)) "not product: '$p'"
    }
    Check (@(Get-ProductPaths @('internal/old.go', 'docs/new.md')).Count -eq 1) 'rename out of a product folder still counts (old path is listed)'
    Check (@(Get-ProductPaths @('docs/a.md', 'docs/b.md')).Count -eq 0) 'rename inside docs is not a product change'
    Check (@(Get-ProductPaths @('docs/a.md', 'internal/my pkg/x y.go')).Count -eq 1) 'mixed list with a space in a path: one product path'
    Check (@(Get-ProductPaths @()).Count -eq 0) 'empty diff has no product path'

    # ---- rc tag ordering
    Same (Get-LastRcTag @('v0.1.0-rc.9', 'v0.1.0-rc.10', 'v0.1.0-rc.2')) 'v0.1.0-rc.10' 'rc.9 < rc.10'
    Same (Get-LastRcTag @('v0.1.0-rc.10', 'v0.1.1-rc.10', 'v0.1.0-rc.10')) 'v0.1.1-rc.10' 'same rc number: the higher version wins'
    Same (Get-LastRcTag @('v0.1.0', 'v0.1.0-beta.1', 'junk', 'v0.1.0-rc.x', 'v0.1.0-rc.5-extra', 'v0.1.0-rc.3 ')) 'v0.1.0-rc.3' 'other tags are ignored, whitespace trimmed'
    Check ($null -eq (Get-LastRcTag @('v1.0.0', 'x'))) 'no rc tag gives $null'
    Check ($null -eq (Get-LastRcTag @())) 'empty tag list gives $null'
    Check ((Test-RcNewer 'v0.1.0-rc.10' 'v0.1.0-rc.9') -and -not (Test-RcNewer 'v0.1.0-rc.9' 'v0.1.0-rc.10') -and -not (Test-RcNewer 'v0.1.0-rc.9' 'v0.1.0-rc.9')) 'Test-RcNewer compares numbers, not text'

    # ---- decision: order, first "no" wins, later steps never run
    $calls = [Collections.Generic.List[string]]::new()
    function New-FakeSteps { param([int]$FailAt = 0, [int[]]$AlsoFail = @())
        $steps = @()
        foreach ($i in 1..7) {
            $n = $i; $f = ($i -eq $FailAt) -or ($AlsoFail -contains $i)
            $steps += { param($c) $calls.Add("s$n"); if ($f) { return (New-Refusal 'skipped' "reason $n") } }.GetNewClosure()
        }
        return $steps
    }
    foreach ($i in 1..7) {
        $calls.Clear()
        $d = Get-SlotDecision -Steps (New-FakeSteps -FailAt $i) -Ctx @{}
        Check ((-not $d.Go) -and $d.Reason -eq "reason $i" -and $calls.Count -eq $i) "decision: step $i says no with its own reason and later steps do not run"
    }
    $calls.Clear(); $d = Get-SlotDecision -Steps (New-FakeSteps -FailAt 3 -AlsoFail 2, 5) -Ctx @{}
    Check ($d.Reason -eq 'reason 2') 'decision: with several "no" the first one wins'
    $d = Get-SlotDecision -Steps (New-FakeSteps) -Ctx @{}
    Check ($d.Go -and $calls.Count -ge 7) 'decision: all seven yes gives go'
    Check ((Get-SlotSteps).Count -eq 7) 'the real decision has seven steps'

    # ---- the real steps against a fake world (git/gh/node/tool wrappers replaced)
    $script:W = $null
    function Reset-World {
        $script:W = @{
            Tools = @('git', 'gh', 'pwsh'); AuthCode = 0; Node = 'v22.23.5'; FetchCode = 0
            Tags = @('v0.1.0-rc.9', 'v0.1.0-rc.10'); OnDevelop = 0; OnMaster = 1
            Runs = (@(@{ databaseId = 3; headSha = $sha; conclusion = 'failure' }, @{ databaseId = 2; headSha = $sha2; conclusion = 'success' }, @{ databaseId = 1; headSha = $sha; conclusion = 'success' }) | ConvertTo-Json -Compress)
            RunsCode = 0; Diff = @('internal/x/a.go', 'docs/a.md'); Prs = '[]'; Lock = (New-LockApi '{"state":"free"}')
        }
        $script:W.Runs = (@(@{ databaseId = 3; headSha = $sha; conclusion = 'failure' }, @{ databaseId = 7; headSha = $sha2; conclusion = 'success' }, @{ databaseId = 1; headSha = $sha; conclusion = 'success' }) | ConvertTo-Json -Compress)
    }
    function Test-RcCommand { param([string]$Name) return ($script:W.Tools -contains $Name) }
    function Set-NodePath { }
    function Invoke-RcNode { param([string[]]$NodeArgs) if ($script:W.Node) { return (Ok @($script:W.Node)) } return (Bad @('not found') 127) }
    function Invoke-RcGh {
        param([string[]]$GhArgs)
        $j = $GhArgs -join ' '
        if ($j -eq 'auth status') { return [pscustomobject]@{ Code = $script:W.AuthCode; Lines = @('x') } }
        if ($j -like 'run list*') { return [pscustomobject]@{ Code = $script:W.RunsCode; Lines = @($script:W.Runs) } }
        if ($j -like 'pr list*') { return (Ok @($script:W.Prs)) }
        if ($j -like 'api *lock.json?ref=release-lock') { return $script:W.Lock }
        throw "unexpected gh call: $j"
    }
    function Invoke-RcGit {
        param([string]$Dir, [string[]]$GitArgs)
        $j = $GitArgs -join ' '
        if ($j -eq 'fetch --tags origin') { return [pscustomobject]@{ Code = $script:W.FetchCode; Lines = @() } }
        if ($j -like 'tag --list*') { return (Ok $script:W.Tags) }
        if ($j -like 'merge-base --is-ancestor * origin/develop') { return [pscustomobject]@{ Code = $script:W.OnDevelop; Lines = @() } }
        if ($j -like 'merge-base --is-ancestor * origin/master') { return [pscustomobject]@{ Code = $script:W.OnMaster; Lines = @() } }
        if ($j -like '-c core.quotepath=off diff --name-only --no-renames v0.1.0-rc.10...*') { return (Ok $script:W.Diff) }
        throw "unexpected git call: $j"
    }
    function Get-Decision {
        param([switch]$DryRun, [string]$Dir = (New-StateDir))
        $script:Cfg = @{ Repo = 'o/r'; RepoFolder = 'R'; WorkTree = 'W'; NodeDir = 'N'; StateDir = $Dir; Requester = 'rc-slot'; DryRun = [bool]$DryRun }
        $ctx = @{ Tag = ''; GSha = ''; Lock = $null }
        try { $d = Get-SlotDecision -Steps (Get-SlotSteps) -Ctx $ctx } finally { if ($ctx.Lock) { $ctx.Lock.Dispose() } }
        return [pscustomobject]@{ D = $d; Ctx = $ctx }
    }
    function Expect-No { param($R, [string]$Outcome, [string]$Pattern, [string]$Name) Check ((-not $R.D.Go) -and $R.D.Outcome -eq $Outcome -and $R.D.Reason -match $Pattern) $Name; if (-not ((-not $R.D.Go) -and $R.D.Outcome -eq $Outcome -and $R.D.Reason -match $Pattern)) { Write-Host "      got $($R.D.Outcome): $($R.D.Reason)" } }

    Reset-World
    $r = Get-Decision
    Check ($r.D.Go -and $r.Ctx.Tag -eq 'v0.1.0-rc.10' -and $r.Ctx.GSha -eq $sha2) 'world: all seven pass, newest green run (by id) and newest rc tag are picked'

    Reset-World; $held = Enter-SlotLock ($d1 = New-StateDir)
    try { $r = Get-Decision -Dir $d1; Expect-No $r 'busy' 'another rc slot is running' '1 busy: the lock is held' } finally { $held.Dispose() }
    Reset-World; $script:W.Tools = @('git', 'pwsh'); Expect-No (Get-Decision) 'failed' '^gh not found' '2 tools: gh missing is a failure'
    Reset-World; $script:W.Tools = @('gh', 'pwsh'); Expect-No (Get-Decision) 'failed' '^git not found' '2 tools: git missing'
    Reset-World; $script:W.Tools = @('git', 'gh'); Expect-No (Get-Decision) 'failed' '^pwsh not found' '2 tools: pwsh missing'
    Reset-World; $script:W.AuthCode = 1; Expect-No (Get-Decision) 'failed' 'not signed in' '2 tools: gh not authenticated'
    Reset-World; $script:W.Node = 'v22.23.4'; Expect-No (Get-Decision) 'failed' 'Node 22\.23\.5 not found' '2 tools: wrong Node version'
    Reset-World; $script:W.Node = $null; Expect-No (Get-Decision) 'failed' 'Node 22\.23\.5 not found' '2 tools: no Node'
    Reset-World; $script:W.FetchCode = 1; Expect-No (Get-Decision) 'failed' 'git fetch origin failed' '3 fetch failure is a failure'
    Reset-World; $script:W.Tags = @('v0.1.0'); Expect-No (Get-Decision) 'skipped' 'no rc tag' '3 no rc tag: skip'
    Reset-World; $script:W.Runs = (@(@{ databaseId = 3; headSha = $sha; conclusion = 'failure' }) | ConvertTo-Json -Compress); Expect-No (Get-Decision) 'skipped' '^no green develop run$' '4 no green run'
    Reset-World; $script:W.Runs = '[]'; Expect-No (Get-Decision) 'skipped' '^no green develop run$' '4 empty run list'
    Reset-World; $script:W.Runs = 'not json'; Expect-No (Get-Decision) 'failed' 'unreadable' '4 unreadable run list fails closed'
    Reset-World; $script:W.RunsCode = 1; Expect-No (Get-Decision) 'failed' 'gh run list failed' '4 gh failure'
    Reset-World; $script:W.OnDevelop = 1; Expect-No (Get-Decision) 'skipped' 'not on origin/develop' '4 green commit not on develop'
    Reset-World; $script:W.OnDevelop = 128; Expect-No (Get-Decision) 'failed' 'merge-base' '4 git error on the ancestor check fails closed'
    Reset-World; $script:W.Diff = @('docs/a.md', 'internal/a_test.go'); Expect-No (Get-Decision) 'skipped' '^only docs/tests changed since v0\.1\.0-rc\.10$' '5 only docs and tests changed'
    Reset-World; $script:W.Diff = @(); Expect-No (Get-Decision) 'skipped' '^only docs/tests changed' '5 empty diff'
    Reset-World; $script:W.Prs = '[{"headRefName":"feature/x"},{"headRefName":"promote/abc1234"}]'; Expect-No (Get-Decision) 'skipped' 'promotion pull request open \(promote/abc1234\)' '6 open promote PR'
    Reset-World; $script:W.Prs = '[{"headRefName":"feature/x"}]'; Check ((Get-Decision).D.Go) '6 an unrelated open PR does not block'
    Reset-World; $script:W.Prs = '<html>'; Expect-No (Get-Decision) 'failed' 'unreadable' '6 unreadable PR list fails closed'
    Reset-World; $script:W.Lock = New-LockApi '{"state":"held","requester":"nachi-thread","holdMerges":true}'; Expect-No (Get-Decision) 'skipped' '^release lock held by nachi-thread$' '6 lock held'
    Reset-World; $script:W.OnMaster = 0; Expect-No (Get-Decision) 'skipped' 'nothing to promote' '7 master already has the green commit'
    Reset-World; $script:W.OnMaster = 128; Expect-No (Get-Decision) 'failed' 'merge-base' '7 git error fails closed'
    Reset-World; $script:W.Diff = @('docs/a.md'); $script:W.Lock = New-LockApi '{"state":"held","holdMerges":true}'; $script:W.OnMaster = 0
    Expect-No (Get-Decision) 'skipped' 'only docs/tests' 'order: with docs-only, lock held and master containing G, the docs reason (earliest) wins'
    Reset-World; $script:W.Tools = @('git'); $held = Enter-SlotLock ($d1 = New-StateDir)
    try { Expect-No (Get-Decision -Dir $d1) 'busy' 'another rc slot' 'order: busy beats missing tools' } finally { $held.Dispose() }

    # ---- lock states
    Check ($null -eq (Get-LockRefusal (Bad @('gh: Not Found (HTTP 404)')))) 'lock: 404 is free'
    Check ($null -eq (Get-LockRefusal (New-LockApi '{"state":"free","holdMerges":false}'))) 'lock: free'
    $x = Get-LockRefusal (New-LockApi '{"state":"held","requester":"alice","holdMerges":true}')
    Check ($x -and $x.Outcome -eq 'skipped' -and $x.Reason -eq 'release lock held by alice') 'lock: held with holdMerges true is a skip naming the requester'
    Check ($null -eq (Get-LockRefusal (New-LockApi '{"state":"held","requester":"alice","holdMerges":false}'))) 'lock: held with holdMerges false (past tagging) is not a block'
    $x = Get-LockRefusal (New-LockApi '{"state":"held","requester":"alice"}')
    Check ($x -and $x.Reason -match 'held by alice') 'lock: held without holdMerges counts as on hold'
    $x = Get-LockRefusal (New-LockApi '{"state":"held","requester":"alice","holdMerges":"false"}')
    Check ($x -and $x.Reason -match 'held by alice') 'lock: holdMerges as a string is not trusted'
    foreach ($case in @(@('garbage json', (New-LockApi 'this is {not json')), @('empty content', (New-LockApi '')), @('json null', (New-LockApi 'null')), @('unknown state', (New-LockApi '{"state":"banana"}')),
                        @('not base64', [pscustomobject]@{ Code = 0; Lines = @('{"content":"!!!","sha":"x"}') }), @('not json at all', [pscustomobject]@{ Code = 0; Lines = @('<html>') }),
                        @('other gh error', (Bad @('gh: HTTP 502 Bad Gateway'))))) {
        $x = Get-LockRefusal $case[1]
        Check ($x -and $x.Outcome -eq 'skipped' -and $x.Reason -eq 'release lock unreadable') "lock: $($case[0]) is a skip, never free"
    }
    $x = Get-LockRefusal (New-LockApi (@{ state = 'held'; holdMerges = $true; requester = ("x`n" + ('y' * 200)) } | ConvertTo-Json))
    Check ($x.Reason.Length -lt 80 -and $x.Reason -notmatch "`n") 'lock: a long requester with a newline is cut to one short line'

    # ---- single instance
    $sd = New-StateDir
    $h1 = Enter-SlotLock $sd; $h2 = Enter-SlotLock $sd
    Check ($h1 -and -not $h2) 'single instance: the second lock attempt is refused'
    Check (Test-SlotLockHeld $sd) 'single instance: a held lock is seen without taking it'
    $h1.Dispose()
    Check (-not (Test-SlotLockHeld $sd)) 'single instance: released lock is free again'
    $h3 = Enter-SlotLock $sd; Check ([bool]$h3) 'single instance: the lock can be taken again after release'; $h3.Dispose()
    Check (-not (Test-SlotLockHeld (New-StateDir))) 'single instance: no lock file means free'

    # ---- log trim, status shape, run outcomes
    $ld = New-StateDir
    foreach ($i in 1..650) { Add-SlotLog -Dir $ld -Outcome 'skipped' -Reason "line $i" }
    $lines = @(Get-Content -LiteralPath (Join-Path $ld 'rc-slot.log'))
    Check ($lines.Count -eq 500 -and $lines[0] -match 'line 151$' -and $lines[-1] -match 'line 650$') 'log: trimmed to the last 500 lines'
    Check ($lines[-1] -match '^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ skipped line 650$') 'log: one line is timestamp, outcome, reason'
    Add-SlotLog -Dir $ld -Outcome 'failed' -Reason "two`nlines  token=abc123SECRET and ghp_0123456789abcdefABCDEF0123"
    $last = (Get-Content -LiteralPath (Join-Path $ld 'rc-slot.log') -Tail 1)
    Check ($last -notmatch 'abc123SECRET' -and $last -notmatch 'ghp_0123' -and $last -match 'token=\*\*\*') 'log: a token-like value is masked and a newline cannot split the line'

    function Read-Status { param([string]$Dir) return (Get-Content -LiteralPath (Join-Path $Dir 'status.json') -Raw | ConvertFrom-Json) }
    $names = 'at,outcome,reason,rc,previous,gSha,carries'
    function Run-Slot {
        param([scriptblock]$Step, [scriptblock]$Act = { throw 'act must not run' }, [string]$Dir = (New-StateDir), [switch]$DryRun)
        $script:AfterCalls = 0
        $cfg = @{ Repo = 'o/r'; RepoFolder = 'R'; WorkTree = 'W'; NodeDir = 'N'; StateDir = $Dir; Requester = 'rc-slot'; DryRun = [bool]$DryRun }
        $out = & { Invoke-SlotRun -Cfg $cfg -Steps @($Step) -Act $Act -AfterRelease { $script:AfterCalls++ } } 6>&1
        $code = [int]($out | Where-Object { $_ -is [int] } | Select-Object -Last 1)
        $text = (@($out | Where-Object { $_ -isnot [int] } | ForEach-Object { "$_" }) -join "`n")
        return [pscustomobject]@{ Code = $code; Text = $text; Dir = $Dir }
    }
    $skipStep = { param($c) $c.Tag = 'v0.1.0-rc.10'; $c.GSha = $sha; return (New-Refusal 'skipped' 'only docs/tests changed since v0.1.0-rc.10') }
    $x = Run-Slot $skipStep
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 0 -and -not $x.Text) 'skipped run: exit 0 and silent'
    Check ((($s.PSObject.Properties.Name) -join ',') -eq $names -and $s.outcome -eq 'skipped' -and $s.reason -match 'only docs' -and $s.previous -eq 'v0.1.0-rc.10' -and $s.gSha -eq $sha -and $s.rc -eq '') 'status.json (skipped): shape and values'
    $raw = Get-Content -LiteralPath (Join-Path $x.Dir 'status.json') -Raw   # ConvertFrom-Json turns the time into a [datetime], so the text is checked
    Check ($raw -match '"at": "\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ"' -and $raw -match '"carries":\s*\[\s*\]') 'status.json (skipped): UTC time and an empty carries array'
    Check (@(Get-Content -LiteralPath (Join-Path $x.Dir 'rc-slot.log')).Count -eq 1 -and -not (Test-Path (Join-Path $x.Dir 'status.json.tmp')) -and $script:AfterCalls -eq 0) 'skipped run: one log line, no temp file, no housekeeping'

    $x = Run-Slot { param($c) return (New-Refusal 'busy' 'another rc slot is running') }
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 0 -and -not $x.Text -and $s.outcome -eq 'busy' -and ((Get-Content -LiteralPath (Join-Path $x.Dir 'rc-slot.log') -Raw) -match ' busy ')) 'busy run: exit 0, silent, logged, status written'

    $x = Run-Slot { param($c) return (New-Refusal 'failed' 'gh not found') }
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 1 -and $s.outcome -eq 'failed' -and $s.reason -eq 'gh not found') 'failed run (missing tool): exit 1, status failed'

    $goStep = { param($c) $c.Tag = 'v0.1.0-rc.66'; $c.GSha = $sha }
    $x = Run-Slot $goStep -Act { param($c) New-SlotResult 'released' 'released v0.1.0-rc.67' 'v0.1.0-rc.67' $c.Tag $c.GSha @('Add thing', 'Fix other') }
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 0 -and $script:AfterCalls -eq 1) 'released run: exit 0 and housekeeping runs once'
    Check ((($s.PSObject.Properties.Name) -join ',') -eq $names -and $s.outcome -eq 'released' -and $s.rc -eq 'v0.1.0-rc.67' -and $s.previous -eq 'v0.1.0-rc.66' -and $s.gSha -eq $sha -and @($s.carries).Count -eq 2 -and $s.carries[0] -eq 'Add thing') 'status.json (released): shape and values'
    $x = Run-Slot $goStep -Act { param($c) New-SlotResult 'released' 'released v0.1.0-rc.67' 'v0.1.0-rc.67' $c.Tag $c.GSha @('Only one') }
    Check ((Get-Content -LiteralPath (Join-Path $x.Dir 'status.json') -Raw) -match '"carries":\s*\[\s*"Only one"\s*\]') 'status.json: a single carry is still an array'
    $x = Run-Slot $goStep -Act { param($c) New-SlotResult 'failed' 'release: Proton Pass needs the passphrase' '' $c.Tag $c.GSha } -Dir (New-StateDir)
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 1 -and $s.outcome -eq 'failed' -and $s.reason -match 'Proton Pass' -and $script:AfterCalls -eq 0) 'failed run (release stop): exit 1, the Proton Pass text is kept, no housekeeping'
    $x = Run-Slot $goStep -Act { param($c) throw 'kaboom' }
    $s = Read-Status $x.Dir
    Check ($x.Code -eq 1 -and $s.outcome -eq 'failed' -and $s.reason -match 'kaboom') 'an exception in the release is a failed run, not a crash'
    $hd = New-StateDir; $held = Enter-SlotLock $hd; $held.Dispose()
    $ran = $false
    $x = Run-Slot { param($c) $c.Lock = Enter-SlotLock $hd } -Act { param($c) New-SlotResult 'skipped' 'x' } -Dir $hd
    Check (-not (Test-SlotLockHeld $hd)) 'the lock is released at the end of a run'

    # ---- -DryRun: prints the decision, writes nothing
    $dd = New-StateDir
    $x = Run-Slot { param($c) return (New-Refusal 'skipped' 'no green develop run') } -Dir $dd -DryRun
    Check ($x.Code -eq 0 -and $x.Text -match 'dry run: skipped: no green develop run' -and -not (Test-Path -LiteralPath $dd)) '-DryRun (skip): prints the decision, creates nothing (not even the state folder)'
    $x = Run-Slot $goStep -Dir $dd -DryRun
    Check ($x.Code -eq 0 -and $x.Text -match 'would promote up to aaaaaaa' -and -not (Test-Path -LiteralPath $dd) -and $script:AfterCalls -eq 0) '-DryRun (go): says what it would do; act and housekeeping never run, nothing written'
    Reset-World
    $script:Cfg = @{ Repo = 'o/r'; RepoFolder = 'R'; WorkTree = 'W'; NodeDir = 'N'; StateDir = $dd; Requester = 'x'; DryRun = $true }
    $out = & { Invoke-SlotRun -Cfg $script:Cfg -Act { throw 'act must not run' } } 6>&1
    Check ((($out | Out-String) -match 'dry run: go: would promote') -and -not (Test-Path -LiteralPath $dd)) '-DryRun with the real steps: decision printed, state folder not created'

    # ---- carries
    $c = @(Get-Carries @('A', 'B', 'A', '', '  ', 'C'))
    Check (($c -join '|') -eq 'A|B|C') 'carries: de-duplicated, blanks dropped, order kept'
    $c = @(Get-Carries @(('x' * 200)))
    Check ($c.Count -eq 1 -and $c[0].Length -eq 120) 'carries: truncated to 120 characters'
    $c = @(Get-Carries @((('x' * 130) + '1'), (('x' * 130) + '2')))
    Check ($c.Count -eq 1) 'carries: two subjects equal after truncation count once'
    $c = @(Get-Carries @(1..12 | ForEach-Object { "subject $_" }))
    Check ($c.Count -eq 8 -and $c[7] -eq 'subject 8') 'carries: at most 8'
    Check (@(Get-Carries @()).Count -eq 0 -and @(Get-Carries $null).Count -eq 0) 'carries: nothing gives an empty list'

    # ---- work tree: it only ever cleans a folder git says is its own top level
    $script:GitLog = [Collections.Generic.List[string]]::new()
    $wtDir = Join-Path $base 'wt'; $repoDir = Join-Path $base 'repo'
    $script:Cfg = @{ Repo = 'o/r'; RepoFolder = $repoDir; WorkTree = $wtDir; NodeDir = 'N'; StateDir = (New-StateDir); Requester = 'rc-slot'; DryRun = $false }
    $script:Top = $wtDir; $script:Head = $sha
    function Invoke-RcGit {
        param([string]$Dir, [string[]]$GitArgs)
        $j = $GitArgs -join ' '; $script:GitLog.Add("$Dir :: $j")
        if ($j -eq 'rev-parse --show-toplevel') { return (Ok @($script:Top)) }
        if ($j -eq 'rev-parse HEAD') { return (Ok @($script:Head)) }
        return (Ok)
    }
    New-Item -ItemType Directory -Force -Path $wtDir | Out-Null
    $script:Top = $repoDir
    Check ((Clear-WorkTree) -match 'refusing to clean' -and -not ($script:GitLog | Where-Object { $_ -match 'clean' })) 'work tree: not cleaned when git says its top level is the main checkout'
    $script:Top = $base
    Check ((Clear-WorkTree) -match 'refusing to clean' -and -not ($script:GitLog | Where-Object { $_ -match 'clean' })) 'work tree: not cleaned when git says its top level is a parent folder'
    $script:Top = $wtDir; $script:GitLog.Clear()
    Check ($null -eq (Clear-WorkTree) -and ($script:GitLog | Where-Object { $_ -match 'reset --hard' }) -and ($script:GitLog | Where-Object { $_ -match 'clean -fdxq' }) -and ($script:GitLog | Where-Object { $_ -match 'checkout --detach' })) 'work tree: reset, clean -fdx and detach when it is its own top level'
    Check ($script:GitLog.Count -gt 0 -and -not ($script:GitLog | Where-Object { $_ -notlike "$wtDir ::*" })) 'work tree: every clean command runs inside the work tree folder'
    Remove-Item -LiteralPath $wtDir -Recurse -Force
    $script:GitLog.Clear()
    Check ($null -eq (Clear-WorkTree) -and $script:GitLog.Count -eq 0) 'work tree: a missing folder needs no cleaning and runs no git'
    $script:GitLog.Clear()
    Check ($null -eq (Update-WorkTree $sha) -and ($script:GitLog | Where-Object { $_ -like "$repoDir :: worktree add --detach $wtDir $sha" })) 'work tree: created with worktree add --detach when missing'
    New-Item -ItemType Directory -Force -Path $wtDir | Out-Null; Set-Content -LiteralPath (Join-Path $wtDir 'stray.txt') -Value 'x'
    Check ((Update-WorkTree $sha) -match 'is not a work tree') 'work tree: a non-empty folder that is not a work tree is left alone'
    Set-Content -LiteralPath (Join-Path $wtDir '.git') -Value 'gitdir: x'
    $script:GitLog.Clear()
    Check ($null -eq (Update-WorkTree $sha) -and ($script:GitLog | Where-Object { $_ -like "$wtDir :: checkout --detach $sha" })) 'work tree: an existing one is refreshed with checkout --detach <sha>'
    $script:Head = $sha2
    Check ((Update-WorkTree $sha) -match 'not at the expected commit') 'work tree: a wrong HEAD after the refresh is an error'
    $script:Head = $sha

    # ---- the release itself (scripts and git faked)
    $script:Runs = [Collections.Generic.List[object]]::new()
    $script:Promote = Ok; $script:Release = Ok; $script:RelList = '[{"tagName":"v0.1.0-rc.67","isDraft":false}]'; $script:MasterHas = 0
    $script:Cleared = 0
    function Update-WorkTree { param([string]$Sha) $script:Runs.Add("wt $Sha"); return $null }
    function Clear-WorkTree { $script:Cleared++; return $null }
    function Invoke-RcScript {
        param([string]$Dir, [string]$Script, [string[]]$ScriptArgs)
        $script:Runs.Add((Split-Path -Leaf $Script) + ' ' + ($ScriptArgs -join ' '))
        $r = if ($Script -like '*Promote-Develop*') { $script:Promote } elseif ($ScriptArgs -contains '-Status') { Ok } else { $script:Release }
        return [pscustomobject]@{ Code = $r.Code; Tail = $r.Lines; Last = [string]($r.Lines | Select-Object -Last 1) }
    }
    function Invoke-RcGit {
        param([string]$Dir, [string[]]$GitArgs)
        $j = $GitArgs -join ' '
        if ($j -like 'rev-parse --verify --quiet origin/develop*') { return (Ok @($sha2)) }
        if ($j -like 'rev-parse --verify --quiet origin/master*') { return (Ok @($sha)) }
        if ($j -eq 'fetch origin master') { return (Ok) }
        if ($j -like 'merge-base --is-ancestor * origin/master') { return [pscustomobject]@{ Code = $script:MasterHas; Lines = @() } }
        if ($j -like 'log --first-parent --pretty=%s v0.1.0-rc.66..*') { return (Ok @('Add thing', 'Add thing', 'Fix other')) }
        if ($j -eq 'worktree prune') { return (Ok) }
        throw "unexpected git call: $j"
    }
    function Invoke-RcGh { param([string[]]$GhArgs) if (($GhArgs -join ' ') -like 'release list*') { return (Ok @($script:RelList)) }; throw 'unexpected gh' }
    $script:Cfg = @{ Repo = 'o/r'; RepoFolder = 'R'; WorkTree = $wtDir; NodeDir = 'N'; StateDir = (New-StateDir); Requester = 'rc-slot'; DryRun = $false }
    $ctx = @{ Tag = 'v0.1.0-rc.66'; GSha = $sha }
    function Act { $script:Runs.Clear(); $script:Cleared = 0; return (Invoke-SlotAct $ctx) }

    $r = Act
    Check ($r.Outcome -eq 'released' -and $r.Rc -eq 'v0.1.0-rc.67' -and $r.Previous -eq 'v0.1.0-rc.66' -and $r.GSha -eq $sha -and ($r.Carries -join '|') -eq 'Add thing|Fix other') 'act: released, rc number read back, carries from the log'
    Check ($script:Runs[0] -eq "wt $sha2" -and $script:Runs[1] -eq "Promote-Develop.ps1 -UpTo $sha -CiWaitMinutes 60 -Repo o/r") 'act: promote runs from a work tree at the origin/develop tip with -UpTo G -CiWaitMinutes 60'
    Check ($script:Runs[2] -eq "wt $sha" -and $script:Runs[3] -eq 'Release.ps1 -Channel rc -Yes -Requester rc-slot -WaitMinutes 30 -Repo o/r') 'act: release runs from a work tree at the origin/master tip with -Channel rc -Yes'
    Check (-not ($script:Runs | Where-Object { $_ -match 'Install|Setup' }) -and $script:Cleared -ge 1) 'act: no installer is run and the work tree is cleaned afterwards'
    $script:Promote = Bad @('x', 'promote said: CI build red') 1
    $r = Act
    Check ($r.Outcome -eq 'failed' -and $r.Reason -eq 'promote: promote said: CI build red' -and -not ($script:Runs | Where-Object { $_ -like 'Release.ps1*' }) -and $script:Cleared -ge 1) 'act: a failed promotion stops there, reason is its last line, work tree still cleaned'
    $script:Promote = Ok; $script:Release = Bad @('Proton Pass: passphrase prompt needed (token=abc123SECRET)') 1
    $r = Act
    Check ($r.Outcome -eq 'failed' -and $r.Reason -match '^release: Proton Pass: passphrase prompt needed' -and $r.Reason -notmatch 'abc123SECRET') 'act: a release stop keeps its Proton Pass text but never a secret'
    $script:Release = Ok; $script:RelList = '[{"tagName":"v0.1.0-rc.66","isDraft":false},{"tagName":"v0.1.0-rc.68","isDraft":true}]'
    $r = Act
    Check ($r.Outcome -eq 'failed' -and $r.Reason -eq 'no new tag') 'act: no newer rc tag (a draft does not count) is a failure'
    $script:RelList = 'garbage'
    Check ((Act).Reason -eq 'no new tag') 'act: an unreadable release list is a failure'
    $script:RelList = '[{"tagName":"v0.1.0-rc.67","isDraft":false}]'; $script:MasterHas = 1
    $r = Act
    Check ($r.Outcome -eq 'failed' -and $r.Reason -match 'master does not contain aaaaaaa|master does not contain' -and -not ($script:Runs | Where-Object { $_ -like 'Release.ps1*' })) 'act: promotion exited 0 but master lacks the commit: no release'
    $script:MasterHas = 0

    # ---- -Install refusal (Housekeeping's seam), and that dot-sourcing Housekeeping defines functions only
    $self = 'C:\Users\u\src\unravis\tools\release\Invoke-RcSlot.ps1'; $localOk = 'C:\Users\u\AppData\Local'
    $why = Get-RcInstallRefusal -Self $self -LocalAppData $localOk -PackageName '' -Probe { 'redirected' }
    Check ($why -match 'redirected' -and $why -match 'Invoke-RcSlot\.ps1' -and $why -notmatch 'Housekeeping\.ps1' -and $why -match 'rcslot') 'install: refused when the probe says redirected (message names this script and folder)'
    Check ([bool](Get-RcInstallRefusal -Self $self -LocalAppData $localOk -PackageName '' -Probe { 'failed' })) 'install: refused when the probe cannot finish'
    Check ((Get-RcInstallRefusal -Self $self -LocalAppData $localOk -PackageName 'Claude_1.2.3.0_x64__abc' -Probe { 'direct' }) -match 'Claude desktop app') 'install: refused inside the Claude desktop app'
    Check ($null -eq (Get-RcInstallRefusal -Self $self -LocalAppData $localOk -PackageName '' -Probe { 'direct' })) 'install: allowed when the probe sees no redirect'
    $Repo = 'mine'
    $null = Get-RcInstallRefusal -Self $self -LocalAppData $localOk -PackageName '' -Probe { 'direct' }
    Check ($Repo -eq 'mine' -and -not $Install.IsPresent -and -not (Get-Variable -Name Event -ErrorAction SilentlyContinue) -and -not (Get-Variable -Name Worker -ErrorAction SilentlyContinue)) 'install: the Housekeeping parameters do not leak into this script'
    $o = @(& { . (Join-Path $here '..\maint\Housekeeping.ps1'); (Get-Command Get-InstallRefusal -CommandType Function).Name })
    Check ($o.Count -eq 1 -and $o[0] -eq 'Get-InstallRefusal') 'dot-sourcing Housekeeping.ps1 defines functions only (no output, no side effects, no exit)'
    $src = Get-Content -LiteralPath $script:RcSlotPath -Raw
    Check ($src -match [regex]::Escape('if ($MyInvocation.InvocationName -ne ''.'')')) 'Invoke-RcSlot.ps1 has the dot-source guard'
    Check ($src -notmatch "'PUT'|'POST'|Write-RemoteLock|--method|-X'") 'Invoke-RcSlot.ps1 has no write call to GitHub (the lock branch is only read)'
    Check ($src -notmatch 'Remove-Item') 'Invoke-RcSlot.ps1 deletes nothing itself'
} catch {
    Write-Host "FAIL  test crashed: $($_.Exception.Message)"; Write-Host $_.ScriptStackTrace; $fail++
} finally {
    if ($base -and (Split-Path -Leaf $base) -like 'rcslot-test-*' -and (Test-Path -LiteralPath $base)) { Remove-Item -LiteralPath $base -Recurse -Force -ErrorAction SilentlyContinue }
}
if ($fail -gt 0) { Write-Host "$fail check(s) failed"; exit 1 }
Write-Host 'all checks passed'
