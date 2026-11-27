#Requires -Version 7.2
<#
.SYNOPSIS
  The Unravis release manager: one command for a whole release, with nothing to type. Checks everything first, picks the version,
  key and passphrase itself, signs, publishes, moves the feed, reads the feed back, verifies on a clean machine, and withdraws the
  version by itself if that verify fails. Spec: docs/auto-updater/RELEASE-MANAGER.md. Threats: docs/auto-updater/THREAT-MODEL.md.

.DESCRIPTION
      ./tools/release/Release.ps1 -Channel rc -Yes

  Stages (it stops at the first failure, says exactly why, and the next run continues from there by itself):
    1 Preflight  changes nothing: tools, master clean and equal to GitHub, CI green on this commit (waits while it runs), every commit
                 since the feed's release came through a merged PR, version is the next one, feed readable and not about to expire,
                 key folder and key files sound, chosen key trusted by this commit and by the release on the feed, Proton Pass
                 passphrase opens that key, added trusted keys are all the owner's, signing tool depends on nothing new, no
                 PowerShell logging policy that could record the passphrase, Test-Release.ps1 passes.
    2 Tag        creates and pushes the tag (starts release.yml).
    3 Wait       waits for release.yml: build, attestation, smoke test, DRAFT release.
    4 Sign       Sign-Release.ps1: gates again on the tag's clean worktree, attestation, hashes, sign, check, upload.
    5 Publish    the draft becomes public (refused without the signature).
    6 Feed       Sign-Release.ps1 -Feed moves the channel's feed to this version; then the feed is read back and compared.
    7 Verify     verify-release.yml on a clean runner. If it fails, the version is withdrawn (signed control file) at once.

  Without -Yes the old typed confirmations stay. With -Yes nothing waits for input anywhere; a rule decides or the run stops.
  It never signs in CI and never prints a passphrase or key. Everything it does goes to the audit log (hash-chained).

.PARAMETER Channel   rc or stable. With no -Version the next version of this channel is worked out from the tags.
.PARAMETER Version   X.Y.Z or X.Y.Z-rc.N (no leading v). Optional; must be newer than every tag.
.PARAMETER KeyDir    Folder with the *.key.json files (default D:\secret). The key is picked from here.
.PARAMETER KeyFile   Use this key file instead of picking one. Must still pass every key check.
.PARAMETER PassphraseFromProtonPass  Proton Pass reference to use instead of the one that belongs to the key (RELEASING.md table).
.PARAMETER From      Start at this stage. Default: Preflight, or the stage after the last one that finished for an unfinished release.
.PARAMETER Yes       Hands-off (owner's standing approval, rc and stable). Every check still runs; any failure stops.
.PARAMETER CheckOnly Run the preflight (including the passphrase check) and stop. Changes nothing. -DryRun is the old name.
.PARAMETER CheckAudit  Check the audit log's hash chain and stop.
.PARAMETER FirstRelease  Only for the first release of a channel (nothing on the feed yet).
.PARAMETER Previous  Published tag to upgrade from in the verify stage. Default: the newest earlier signed release.
.PARAMETER SkipVerify  Stop after the feed update.
.PARAMETER RunId     Continue at Wait with this release.yml run.
.PARAMETER WaitMinutes  Another release holds the lock: wait this long for it (checks every 10 to 60 s) instead of refusing at once.
.PARAMETER Requester    Who is asking (a thread name). Shown to everyone else in "busy" messages and in -Status. Default: user@machine.
.PARAMETER LastGreen    Release the newest master commit whose CI is green now, instead of waiting for the head's CI.
.PARAMETER Again        Release even though the newest tag of this channel already holds this commit and finished.
.PARAMETER CiWaitMinutes  How long to wait for CI on the master head before giving up (default 45).
.PARAMETER RunWatchMinutes  How long to wait for one GitHub Actions run (the release build, the verify run) before giving up (default 120, 1 to 170: the lock lease is 180 minutes and the wait sends no heartbeat, so it must end before the lease does; the build, smoke test and draft take up to 90 minutes in a row, plus queue time). A run still going then is NOT treated as good: the release stays unpublished and the same command can be run again.
.PARAMETER Status    Print where the release machinery is (lock, state, last audit entries) and stop. -Json for a machine-readable form.
.PARAMETER Pinned    (internal) The commit this run releases. Set only by the bootstrap; do not pass it by hand.
.PARAMETER LockId    (internal) The release-lock record this run belongs to. Set only by the bootstrap.
#>
param(
    [ValidateSet('rc', 'stable')] [string]$Channel,
    [string]$Version,
    [string]$KeyFile,
    [string]$KeyDir = 'D:\secret',
    [ValidateSet('', 'Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify')] [string]$From = '',
    [switch]$FirstRelease,
    [switch]$Yes,
    [string]$PassphraseFromProtonPass,
    [string]$Previous,
    [switch]$SkipVerify,
    [Alias('DryRun')] [switch]$CheckOnly,
    [switch]$CheckAudit,
    [long]$RunId,
    [int]$WaitMinutes = 0,
    [string]$Requester,
    [switch]$LastGreen,
    [switch]$Again,
    [int]$CiWaitMinutes = 45,
    [ValidateRange(1, 170)] [int]$RunWatchMinutes = 120,
    [switch]$Status,
    [switch]$Json,
    [string]$Pinned,
    [string]$LockId,
    [string]$Branch = 'master',
    [string]$Repo = 'abhinavjp/unravis'
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
. (Join-Path $PSScriptRoot 'ReleaseLib.ps1')
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue
# git must never wait for a person either (credential prompt, tag message editor).
$env:GIT_TERMINAL_PROMPT = '0'; $env:GIT_EDITOR = 'false'

# PowerShell names ignore case: $channel below would overwrite -Channel, so keep what was asked for apart.
$askedChannel = [string]$PSBoundParameters['Channel']
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$order = 'Preflight', 'Tag', 'Wait', 'Sign', 'Publish', 'Feed', 'Verify'
$data = Get-ReleaseDataDir
$audit = Join-Path $data 'audit.jsonl'
# No git hook of the shared clone may run during a release (worktree add and checkout run post-checkout hooks): hooks are pointed at an
# empty folder for this process and everything it starts.
$env:GIT_CONFIG_COUNT = '2'; $env:GIT_CONFIG_KEY_0 = 'core.hooksPath'; $env:GIT_CONFIG_VALUE_0 = (Join-Path $data 'no-hooks')
$env:GIT_CONFIG_KEY_1 = 'core.fsmonitor'; $env:GIT_CONFIG_VALUE_1 = 'false'   # a program named in the clone's config must not run either

if ($CheckAudit) {
    $c = Test-AuditChain $audit
    if ($c.Ok) { Write-Host "Audit log OK: $($c.Lines) entries, chain unbroken ($audit)." -ForegroundColor Green; exit 0 }
    Write-Host "Audit log BROKEN at line $($c.BrokenAt) of $($c.Lines): that line or the one before it was changed or removed ($audit)." -ForegroundColor Red; exit 1
}


if ($Status) { Show-ReleaseStatus $Repo $data ([bool]$Json); exit 0 }

# ---- bootstrap: take the lock, pin the commit, run the release from a private copy of it ---------------------------------------------
# What this closes: two threads cutting a release at once, a merge landing mid-release (CI on master cancels the older run), and a
# shared working folder that is on another branch or half edited. The bootstrap holds both locks, picks the commit, makes a private
# worktree of exactly that commit and runs THIS script from it (so the code that sees the passphrase is the code of that commit).
$ReleaseProtocol = 2   # a bootstrap only re-runs a Release.ps1 that carries this marker (older ones know nothing of -Pinned and the locks)
$boundParams = $PSBoundParameters
function Invoke-ReleaseBootstrap {
    $callerDir = (Get-Location).Path   # relative -KeyFile / -KeyDir mean this folder, not the repository
    $repoRoot0 = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
    $machine = [Environment]::MachineName
    $hostId = Get-HostId   # the lock record on GitHub carries this opaque id, never the machine or user name
    $me = if ($Requester) { $Requester } elseif ($env:UNRAVIS_RELEASE_REQUESTER) { $env:UNRAVIS_RELEASE_REQUESTER } else { "unnamed-$($hostId.Substring(0, 6))" }
    foreach ($t in 'git', 'gh') { if (-not (Get-Command $t -ErrorAction SilentlyContinue)) { Write-Host "$t is not installed or not on PATH." -ForegroundColor Red; return 1 } }
    & gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Write-Host 'gh is not signed in (run: gh auth login).' -ForegroundColor Red; return 1 }
    $asked = [string]$boundParams['Channel']
    if (-not $asked -and -not $Version -and -not (Get-ReleaseState $data)) { Write-Host 'Pass -Channel rc or -Channel stable (or -Version).' -ForegroundColor Red; return 1 }

    # 1. the locks: this PC (the OS drops it if the process dies), then GitHub (everyone else)
    $id = [guid]::NewGuid().ToString('N')
    $deadline = (Get-Date).AddMinutes($WaitMinutes); $attempt = 0; $osLock = $null; $stale = $null
    while ($true) {
        $busy = $null
        $osLock = try { Enter-ReleaseLock $data } catch { $busy = $_.Exception.Message; $null }
        $runBusy = $false
        if ($osLock) {
            # A release process of an earlier (killed) bootstrap may still be running: it holds run.lock. Then nobody takes over.
            $probe = try { Enter-RunLock $data } catch { $null }
            if ($probe) { $probe.Dispose() } else { $runBusy = $true; $busy = 'BUSY: a release process started from this PC earlier is still running (run.lock is held), although its starter is gone. Wait for it to finish, or end that pwsh process. Run -Status.' }
        }
        if ($osLock -and -not $runBusy) {
            try {
                $cur = Read-RemoteLock $Repo
                $alive = [bool]$cur.Record -and (Test-HolderAlive ([int]$cur.Record.pid) $cur.Record.started)
                $b = Test-ReleaseLockBusy $cur.Record $hostId (Get-Date) $true $alive
                if ($b.Busy) { $busy = Format-LockBusy $cur.Record (Get-Date) $hostId }
                else {
                    if ($b.Stale) { $stale = $b.Reason; Write-Host "Taking over: $stale" -ForegroundColor Yellow }
                    $rec = New-LockRecord @{ id = $id; host = $hostId; pid = $PID; requester = $me; started = [DateTime]::UtcNow.ToString('o')
                                             heartbeat = [DateTime]::UtcNow.ToString('o'); channel = $asked; version = [string]$Version; stage = 'Starting'; holdMerges = (-not $CheckOnly); last = $cur.Record.last }
                    try { $null = Write-RemoteLock $Repo $rec $cur.Sha "release lock: $me starts"; $busy = $null }
                    catch { if ($_.Exception.Message -match '^LOCK-CONFLICT') { $busy = 'BUSY: somebody claimed the release lock a moment before you. Run -Status, or pass -WaitMinutes.' } else { throw } }
                }
            } catch { $osLock.Dispose(); $osLock = $null; Write-Host $_.Exception.Message -ForegroundColor Red; return 1 }
        }
        if (-not $busy) { break }
        if ($osLock) { $osLock.Dispose(); $osLock = $null }
        if ((Get-Date) -ge $deadline) { Write-Host $busy -ForegroundColor Red; return 3 }
        $attempt++
        Write-Host "$busy`nWaiting (until $($deadline.ToString('HH:mm:ss'))) ..." -ForegroundColor Yellow
        Start-Sleep -Seconds ([Math]::Min(60, 10 * $attempt))
    }
    $nonce = [guid]::NewGuid().ToString('N')   # handed to the re-run through its environment: the lock id on GitHub is public and proves nothing
    Set-LockInfo $data @{ id = $id; pid = $PID; requester = $me; version = [string]$Version; stage = 'Starting'; started = [DateTime]::UtcNow.ToString('o'); nonce = $nonce }
    $runStart = [DateTime]::UtcNow
    Write-AuditEntry $audit @{ event = 'lock-acquired'; id = $id; requester = $me; machine = $machine; stale = "$stale"; hold_merges = (-not $CheckOnly) }
    $code = 1; $wt = $null; $pinned = $null
    try {
        Push-Location $repoRoot0
        try {
            # 2. fetch, then decide what is being released
            for ($i = 1; $i -le 2; $i++) { git fetch origin $Branch --tags --quiet 2>&1 | Out-Null; if ($LASTEXITCODE -eq 0) { break }; Start-Sleep -Seconds 3 }
            if ($LASTEXITCODE -ne 0) { Write-Host 'git fetch failed (network?).' -ForegroundColor Red; return 1 }
            $tags = @(git tag -l 'v*')
            if ($LASTEXITCODE -ne 0) { Write-Host 'git could not list the version tags of this clone.' -ForegroundColor Red; return 1 }
            $state = Get-ReleaseState $data
            $plan = Get-ResumePlan $state $asked $Version $tags ([bool]$CheckOnly)
            $how = ''; $fromTag = $false
            $tagOnGitHub = $false
            if ($plan.Resuming) {
                $lr = git ls-remote --tags origin "refs/tags/v$($plan.Version)"
                if ($LASTEXITCODE -ne 0) { Write-Host "git could not ask GitHub whether the tag v$($plan.Version) exists (network?). Nothing was done; run again." -ForegroundColor Red; return 1 }
                $tagOnGitHub = [bool]$lr
            }
            if ($tagOnGitHub) {
                # The tag exists: the build is of that commit, whatever master has become.
                # (A failed git prints nothing, and .Trim() on nothing throws before the exit code is read: so ask first, trim after.)
                $pinned = git rev-list -n 1 "v$($plan.Version)"
                if ($LASTEXITCODE -ne 0 -or -not $pinned) { Write-Host "The tag v$($plan.Version) is on GitHub but not in this clone." -ForegroundColor Red; return 1 }
                $pinned = "$pinned".Trim(); $how = "resume of v$($plan.Version)"; $fromTag = $true
                # Anyone who can push a tag could name a commit of their own here, and its scripts would then run with the passphrase.
                # The commit must be the one THIS PC's audit log says it tagged for this version.
                $taggedBefore = Get-AuditTaggedCommit $audit $plan.Version
                if (-not $taggedBefore -or $taggedBefore -ne $pinned) { Write-Host "Tag v$($plan.Version) is on commit $($pinned.Substring(0, 7)), but this PC's audit log says it tagged $(if ($taggedBefore) { $taggedBefore.Substring(0, 7) } else { 'nothing' }) for that version. Not resuming: someone else made or moved that tag. Run -Status and look at the tag on GitHub." -ForegroundColor Red; return 1 }
            } elseif ($CheckOnly) {
                $pinned = git rev-parse "origin/$Branch"
                if ($LASTEXITCODE -ne 0 -or -not $pinned) { Write-Host "git could not find origin/$Branch (git fetch?). Nothing was done." -ForegroundColor Red; return 1 }
                $pinned = "$pinned".Trim(); $how = 'check only (the run itself waits for CI)'
            } else {
                $started = Get-Date; $lastBeat = $started; $stop = $started.AddMinutes($CiWaitMinutes); $n = 0; $said = $false
                while ($true) {
                    git fetch origin $Branch --quiet 2>&1 | Out-Null
                    if ($LASTEXITCODE -ne 0) { Write-Host 'git fetch failed (network?).' -ForegroundColor Red; return 1 }
                    $commits = @(git rev-list --first-parent -n 20 "origin/$Branch")
                    if ($LASTEXITCODE -ne 0 -or -not $commits.Count) { Write-Host "git could not list the newest commits of origin/$Branch." -ForegroundColor Red; return 1 }
                    $runs = @(gh run list --repo $Repo --workflow ci.yml --branch $Branch --event push --limit 40 --json headSha,status,conclusion,databaseId | ConvertFrom-Json)
                    if ($LASTEXITCODE -ne 0) { Write-Host 'gh could not list CI runs.' -ForegroundColor Red; return 1 }
                    $sel = Select-PinnedCommit $commits $runs -LastGreen:$LastGreen
                    if ($sel.Status -eq 'green') { $pinned = $sel.Commit; $how = $sel.Why; break }
                    if ($sel.Status -eq 'red') { Write-Host "CI failed on master ($($commits[0].Substring(0,7))): https://github.com/$Repo/actions/workflows/ci.yml . Fix master first, or pass -LastGreen to release the newest green commit." -ForegroundColor Red; return 1 }
                    if ($sel.Status -eq 'none') { Write-Host "$($sel.Why). Nothing to release." -ForegroundColor Red; return 1 }
                    if ($sel.Why -match 'norun' -and ((Get-Date) - $started).TotalMinutes -gt 5) { Write-Host "No CI run (ci.yml) on master $($commits[0].Substring(0,7)) after 5 minutes. Was it pushed through a PR?" -ForegroundColor Red; return 1 }
                    if ((Get-Date) -gt $stop) { Write-Host "CI on master $($commits[0].Substring(0,7)) is still not green after $CiWaitMinutes minutes. Pass -LastGreen to release the newest green commit." -ForegroundColor Red; return 1 }
                    if (-not $said) { Write-Host "CI on master $($commits[0].Substring(0,7)) is not finished ($($sel.Why)); waiting up to $CiWaitMinutes min. Merges to master are on hold while this waits (lock: hold merges)." ; $said = $true }
                    $n++; Start-Sleep -Seconds (Get-BackoffSeconds $n 30)
                    if (((Get-Date) - $lastBeat).TotalMinutes -ge 10) {   # keep the lease alive during a long CI wait
                        try { $null = Update-RemoteLock $Repo $id @{ stage = 'WaitCI' } 'release lock: waiting for CI'; $lastBeat = Get-Date } catch { Write-Host $_.Exception.Message -ForegroundColor Red; return 1 }
                    }
                }
            }
            # Whatever way it was chosen, the commit must be part of master and not older than the newest release.
            git merge-base --is-ancestor $pinned "origin/$Branch"
            if ($LASTEXITCODE -ne 0) { Write-Host "Commit $($pinned.Substring(0, 7)) is not part of origin/$Branch. Not releasing it." -ForegroundColor Red; return 1 }
            $mergedTags = @(git tag --merged "origin/$Branch" -l 'v*')   # a stray tag off master must not block every release
            if ($LASTEXITCODE -ne 0) { Write-Host "git could not list the release tags of origin/$Branch." -ForegroundColor Red; return 1 }
            $newestAll = Get-NewestVersion $mergedTags
            if ($newestAll -and -not $fromTag) {
                git merge-base --is-ancestor "v$newestAll" $pinned 2>$null
                if ($LASTEXITCODE -ne 0) { Write-Host "Commit $($pinned.Substring(0, 7)) does not contain v$newestAll, the newest release. A release must not go back in history (-LastGreen picked an older commit?)." -ForegroundColor Red; return 1 }
            }
            # The scripts running this bootstrap must be master's own at that commit: the bootstrap holds the locks and chooses what runs.
            # (Not on a resume: that run is of the tag's own commit, vouched for by the audit log above, and master may have moved on.)
            if (-not $fromTag) { foreach ($f in 'Release.ps1', 'ReleaseLib.ps1', 'ProtonPass.ps1') {
                $want = (git rev-parse "${pinned}:tools/release/$f" 2>$null)
                $have = (git hash-object -- (Join-Path $PSScriptRoot $f) 2>$null)
                if ($want -and $have -and $want.Trim() -ne $have.Trim()) { Write-Host "tools/release/$f here is not the one at the pinned commit $($pinned.Substring(0, 7)) (an edited or older copy). Run the release from an up-to-date clean checkout of master." -ForegroundColor Red; return 1 }
            } }
            $pinnedScript = (git show "${pinned}:tools/release/Release.ps1" 2>$null) -join "`n"
            if ($pinnedScript -notmatch '\$ReleaseProtocol = 2') { Write-Host "The Release.ps1 of the pinned commit $($pinned.Substring(0, 7)) is from before the lock and pinned-commit change, so it cannot be run from here. Finish this release with the old command from a checkout of that commit (docs/auto-updater/RELEASING.md), or run -Again after a newer commit." -ForegroundColor Red; return 1 }
            Write-Host "Pinned commit $($pinned.Substring(0,7)): $how." -ForegroundColor Green
            Write-AuditEntry $audit @{ event = 'pinned'; id = $id; commit = $pinned; why = $how }
            # 3. nothing new? (two sessions asking for an rc at the same time: the second finds it already done)
            if (-not $plan.Resuming -and -not $Again -and -not $CheckOnly) {
                $kind = if ($Version) { if ($Version.Contains('-')) { 'rc' } else { 'stable' } } else { $asked }
                $kindTags = @($tags | Where-Object { (ConvertTo-SemVer $_) -and ($_.Contains('-') -eq ($kind -eq 'rc')) })
                $nt = Get-NewestVersion $kindTags
                $ntc = if ($nt) { git rev-list -n 1 "v$nt" } else { '' }
                if ($nt -and ($LASTEXITCODE -ne 0 -or -not $ntc)) { Write-Host "git could not find the commit of v$nt." -ForegroundColor Red; return 1 }
                $ntc = "$ntc".Trim()
                $ar = Get-AlreadyReleased $kind $(if ($nt) { "v$nt" } else { '' }) $ntc $pinned $state
                if ($ar -eq 'done') { Write-Host "Nothing new: $($pinned.Substring(0,7)) is already released as v$nt ($kind). Nothing was done. Merge something first, or pass -Again." -ForegroundColor Green; $code = 0; return 0 }
                if ($ar -eq 'withdrawn') { Write-Host "v$nt holds this same commit and was WITHDRAWN. Releasing it again would build the same files. Merge the fix first, or pass -Again." -ForegroundColor Red; return 1 }
            }
            $null = Update-RemoteLock $Repo $id @{ commit = $pinned; stage = 'Pinned'; holdMerges = ((-not $CheckOnly) -and -not $fromTag) } "release lock: pinned $($pinned.Substring(0,7))"
            Set-LockInfo $data @{ id = $id; pid = $PID; requester = $me; version = [string]$Version; stage = 'Pinned'; started = $runStart.ToString('o'); nonce = $nonce }   # same step name as the GitHub record
            # 4. a private worktree of exactly that commit; this script runs again from there
            $workRoot = Join-Path $data 'work'
            if (Test-Path -LiteralPath $workRoot) { foreach ($d in Get-ChildItem -LiteralPath $workRoot -Directory) { git worktree remove --force $d.FullName 2>&1 | Out-Null; Remove-Item -LiteralPath $d.FullName -Recurse -Force -ErrorAction SilentlyContinue } }
            git worktree prune 2>&1 | Out-Null
            New-Item -ItemType Directory -Force -Path $workRoot | Out-Null
            $wt = Join-Path $workRoot ('r' + (Get-Date -Format 'MMddHHmmss'))
            git -c core.longpaths=true worktree add --detach $wt $pinned 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath (Join-Path $wt 'tools\release\Release.ps1'))) { Write-Host "Could not make a worktree of $($pinned.Substring(0,7)) at $wt." -ForegroundColor Red; return 1 }
            $fwd = @('-NoProfile', '-File', (Join-Path $wt 'tools\release\Release.ps1'))
            foreach ($kv in $boundParams.GetEnumerator()) {
                if (@('WaitMinutes', 'Requester', 'LastGreen', 'Again', 'CiWaitMinutes', 'Pinned', 'LockId') -contains $kv.Key) { continue }
                if ($kv.Value -is [System.Management.Automation.SwitchParameter]) { if ($kv.Value.IsPresent) { $fwd += "-$($kv.Key)" } }
                elseif ([string]::IsNullOrEmpty("$($kv.Value)")) { continue }   # an empty value would swallow the next argument on PowerShell 7.2
                elseif ($kv.Key -eq 'KeyFile' -or $kv.Key -eq 'KeyDir') { $fwd += "-$($kv.Key)"; $fwd += [System.IO.Path]::GetFullPath([string]$kv.Value, $callerDir) }
                else { $fwd += "-$($kv.Key)"; $fwd += "$($kv.Value)" }
            }
            $fwd += @('-Pinned', $pinned, '-LockId', $id)
            $exe = (Get-Process -Id $PID).Path
            $env:UNRAVIS_RELEASE_NONCE = $nonce
            # What the run prints is also kept (redacted, time-stamped, flushed per line) in <data>\logs for the releases dashboard. The
            # copy is best effort: if the file cannot be made the run goes on exactly as before, and the exit code is the run's own.
            try { $runLog = New-RunLogPath (Get-ReleaseLogDir $data); Remove-OldReleaseLogs (Split-Path $runLog) } catch { $runLog = $null }
            $logNames = @($machine, [Environment]::UserName)
            # The worker prints UTF-8 (the check marks and dots of gh and git). Read it as UTF-8, not as this console's OEM code page, or the
            # screen and the saved log show "Γ£ô" for a check mark. Put the console back afterwards.
            $prevEnc = $null; try { $prevEnc = [Console]::OutputEncoding; [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { $prevEnc = $null }
            try {
                if ($runLog) { & $exe @fwd | Write-ReleaseRunLog -Path $runLog -Names $logNames | Out-Host }
                else { & $exe @fwd | Out-Host }   # the run's own output goes to the screen, not into this function's result
            } finally {
                Remove-Item Env:UNRAVIS_RELEASE_NONCE -ErrorAction SilentlyContinue
                if ($prevEnc) { try { [Console]::OutputEncoding = $prevEnc } catch {} }
            }
            $code = $LASTEXITCODE
        } finally { Pop-Location }
    } finally {
        $st = Get-ReleaseState $data
        # Only a state.json written by THIS run counts; an older release's "done" must not be reported as this run's result.
        $fresh = $false; try { $fresh = [bool]$st -and (([datetime]$st.updated).ToUniversalTime() -ge $runStart) } catch {}
        if (-not $fresh) { $st = $null }
        $outcome = if ($CheckOnly) { 'check-only' } elseif ($code -eq 0 -and $st -and $st.status -eq 'done') { 'done' } elseif ($st) { [string]$st.status } elseif ($code -eq 0) { 'nothing-new' } else { 'stopped' }
        $freeFields = @{ state = 'free'; holdMerges = $false; stage = 'idle'; reason = '' }
        if ($outcome -ne 'nothing-new') { $freeFields.last = @{ version = $(if ($st) { [string]$st.version } else { '' }); outcome = $outcome; at = [DateTime]::UtcNow.ToString('o'); requester = $me; reason = $(if ($st) { [string]$st.reason } else { '' }) } }
        try { $null = Update-RemoteLock $Repo $id $freeFields "release lock: free ($outcome)" }
        catch { if ($_.Exception.Message -match 'lock lost') { Write-Host $_.Exception.Message -ForegroundColor Yellow } else { Write-Host "Could not free the release lock on GitHub: $($_.Exception.Message) The next run on this PC takes it over." -ForegroundColor Yellow } }
        try { Write-AuditEntry $audit @{ event = 'lock-released'; id = $id; outcome = $outcome } } catch {}
        if ($wt) { Push-Location $repoRoot0; try { git worktree remove --force $wt 2>&1 | Out-Null; git worktree prune 2>&1 | Out-Null } finally { Pop-Location } }
        Remove-Item -LiteralPath (Join-Path $data 'lock-info.json') -Force -ErrorAction SilentlyContinue
        if ($osLock) { $osLock.Dispose() }
        # After the locks are free (nobody waits for this): keep the GitHub Actions logs of this release for the dashboard's tests and logs.
        if (-not $CheckOnly -and $st -and $st.version -and $pinned) { Save-ReleaseRunLogs $Repo $data $audit ([string]$st.version) ([string]$pinned) $runStart @($machine, [Environment]::UserName) }
    }
    return $code
}
if (-not $Pinned) { exit (Invoke-ReleaseBootstrap) }
if (-not $LockId) { throw '-Pinned and -LockId are set by the bootstrap only. Run Release.ps1 without them.' }
# Proof that the bootstrap on this PC started this run (the lock id on GitHub is public), and this process's own lock: while it lives,
# nobody takes the release over or removes its worktree, even if the bootstrap that started it was killed.
$li = try { Get-Content -Raw -LiteralPath (Join-Path $data 'lock-info.json') | ConvertFrom-Json } catch { $null }
if (-not $env:UNRAVIS_RELEASE_NONCE -or -not $li -or $li.nonce -ne $env:UNRAVIS_RELEASE_NONCE -or $li.id -ne $LockId) { throw 'This run was not started by the release bootstrap on this PC (-Pinned and -LockId are not for typing). Run Release.ps1 without them.' }
Remove-Item Env:UNRAVIS_RELEASE_NONCE -ErrorAction SilentlyContinue
# gh and git print UTF-8; read it as UTF-8 here and print UTF-8 back to the bootstrap (see Invoke-ReleaseBootstrap).
try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}
$runLock = try { Enter-RunLock $data } catch { throw 'Another release process is already running on this PC (run.lock is held).' }

$script:stage = 'Preflight'
$script:report = [ordered]@{}
function Stage([string]$name, [string]$what) {
    $script:stage = $name; Write-Host "`n[$($order.IndexOf($name) + 1)/7] $what" -ForegroundColor Cyan
    if (-not $CheckOnly) { Set-LockStage $name }
}
# The GitHub lock record says which step this run is at (anyone can read it), and proves the record is still this run's.
function Set-LockStage([string]$stage, [hashtable]$more = @{}) {
    $f = @{ stage = $stage }
    foreach ($k in $more.Keys) { $f[$k] = $more[$k] }
    try { $null = Update-RemoteLock $Repo $LockId $f "release lock: $Version $stage" }
    catch { if ($_.Exception.Message -match 'lock lost') { $script:ownsVersion = $false; Fail $_.Exception.Message } else { Write-Host "  (lock record not updated: $($_.Exception.Message))" -ForegroundColor Yellow } }
}
$script:extra = @{}
$script:ownsVersion = $false   # state.json is only written once this run has settled which release it is working on
$script:withdrawOnly = $false
$script:addedKeys = @()
$script:checkedHead = $null
function Done([string]$name) { if (-not $CheckOnly) { Set-ReleaseState $data $Version $name 'running' '' $script:extra } }
function Fail([string]$msg, [string]$status = 'stopped') {
    # A withdraw-only run (the last verify did not pass) must never leave 'withdraw-pending': any other status would let a later run verify that release again.
    if ($script:withdrawOnly -and $status -eq 'stopped') { $status = 'withdraw-pending' }
    Write-Host "`nSTOPPED at $script:stage : $msg" -ForegroundColor Red
    if ($script:ownsVersion -and -not $CheckOnly) {
        # The reason can be the output of a failed command (Run throws it whole): it goes to state.json and audit.jsonl, which the Releases app shows.
        $safe = Protect-ReasonText $msg   # the console line above is redacted when the run log is read
        Set-ReleaseState $data $Version (Get-LastDone) $status $safe $script:extra
        try { Write-AuditEntry $audit @{ event = 'stopped'; version = $Version; stage = $script:stage; reason = $safe } } catch {}
        if ($status -ne 'withdrawn') { Write-Host "Fix it, then run the same command again: it continues at $script:stage by itself." -ForegroundColor Yellow }
    }
    exit 1
}
# The tag push threw. The local tag always goes: a stale one on this commit would block every later run once master has moved on.
# If the push did land, the next run fetches the tag back and the 'tagging' line (written before the push) still vouches for it.
# 'tag-failed' is only written when GitHub itself answered "no such tag" (if it could not be asked, nobody knows).
function Stop-AfterFailedTagPush([string]$tag, [string]$head, [string]$pushError) {
    $lrAfter = git ls-remote --tags origin "refs/tags/$tag"
    $asked = ($LASTEXITCODE -eq 0)
    git tag -d $tag 2>&1 | Out-Null
    if ($asked -and -not $lrAfter) { Write-AuditEntry $audit @{ event = 'tag-failed'; version = $Version; commit = $head } }
    Fail $pushError
}
function Get-LastDone { $i = $order.IndexOf($script:stage); if ($i -le 0) { return '' } else { return $order[$i - 1] } }
function Run([string]$what, [scriptblock]$cmd) { $out = & $cmd 2>&1; if ($LASTEXITCODE -ne 0) { throw "$what failed: $($out | Out-String)" }; $out }
function Confirm-Typed([string]$word, [string]$why) {
    Write-Host $why
    if ($Yes) { Write-Host "-Yes: continuing without typing '$word'." -ForegroundColor Yellow; return }
    $a = Read-Host "Type '$word' to continue, anything else stops"
    if ($a -ne $word) { Write-Host 'Stopped by you. Nothing further was done.'; exit 2 }
}
# Every gh call whose answer drives a decision goes through Invoke-GhOrFail / Get-GhJson (or checks $LASTEXITCODE itself): a gh that
# failed must stop the run in plain words, never read as "no answer = nothing there". Test-ReleaseManager.ps1 runs these with a fake gh.
function Invoke-GhOrFail([string]$what, [string[]]$ghArgs) {
    $out = & gh @ghArgs
    if ($LASTEXITCODE -ne 0) { Fail "gh could not $what (exit code $LASTEXITCODE). Check the network and 'gh auth status', then run the same command again." }
    return @($out | ForEach-Object { "$_" })
}
function ConvertFrom-GhLines([string[]]$lines, [string]$what) {
    $text = ($lines -join "`n").Trim()
    if (-not $text) { return @() }
    try { return @($text | ConvertFrom-Json) } catch { Fail "gh answered with something that cannot be read while it was asked to $what." }
}
function Get-GhJson([string]$what, [string[]]$ghArgs) { return @(ConvertFrom-GhLines (Invoke-GhOrFail $what $ghArgs) $what) }
function Find-Run([string]$workflow, [string]$branchOrTag, [string]$sha, [int]$timeoutSec) {
    $deadline = (Get-Date).AddSeconds($timeoutSec); $k = 0; $ghFailed = $false
    while ((Get-Date) -lt $deadline) {
        $out = & gh run list --repo $Repo --workflow $workflow --branch $branchOrTag --limit 10 --json databaseId,headSha,createdAt
        $ghFailed = ($LASTEXITCODE -ne 0)   # a blip (failed call or an answer that is not JSON) is retried until the deadline; only a last failure stops the run
        if (-not $ghFailed) {
            $runs = @(); $text = (@($out | ForEach-Object { "$_" }) -join "`n").Trim()
            if ($text) { try { $runs = @($text | ConvertFrom-Json) } catch { $ghFailed = $true } }
        }
        if (-not $ghFailed) {
            $hit = $runs | Where-Object { -not $sha -or $_.headSha -eq $sha } | Sort-Object createdAt -Descending | Select-Object -First 1
            if ($hit) { return $hit.databaseId }
        }
        $k++; Start-Sleep -Seconds ([Math]::Min(10, 2 * $k))
    }
    if ($ghFailed) { Fail "gh could not list the $workflow runs (network or sign-in?), so the run of $branchOrTag could not be found. Run the same command again." }
    return $null
}
# gh run watch has no time limit, so a run is polled instead: returns @{ Done; Conclusion; Problem }. Done is $false when the run did not
# finish in time or its state could not be read for 10 minutes in a row; Problem says which. Never throws; the caller decides what that means.
$script:RunPollSeconds = 15; $script:RunUnreadableMinutes = 10
function Wait-RunFinished([long]$runId, [double]$minutes) {
    $deadline = (Get-Date).AddMinutes($minutes); $started = Get-Date; $badSince = $null; $seen = ''; $lastSaid = $started
    while ($true) {
        $out = & gh run view $runId --repo $Repo --json status,conclusion
        $bad = ($LASTEXITCODE -ne 0); $r = $null
        if (-not $bad) {
            try { $r = (($out | ForEach-Object { "$_" }) -join "`n") | ConvertFrom-Json } catch { $r = $null }
            if (-not $r -or -not $r.status) { $bad = $true }
            elseif ($r.status -eq 'completed') { return [pscustomobject]@{ Done = $true; Conclusion = [string]$r.conclusion; Problem = '' } }
            elseif ($r.status -ne $seen -or ((Get-Date) - $lastSaid).TotalMinutes -ge 5) {
                $seen = [string]$r.status; $lastSaid = Get-Date
                Write-Host ("  run {0}: {1} ({2:N0} min so far, gives up after {3:N0})" -f $runId, $seen, ((Get-Date) - $started).TotalMinutes, $minutes)
            }
        }
        if (-not $bad) { $badSince = $null }
        else {
            if (-not $badSince) { $badSince = Get-Date }
            if (((Get-Date) - $badSince).TotalMinutes -ge $script:RunUnreadableMinutes) { return [pscustomobject]@{ Done = $false; Conclusion = ''; Problem = ('could not be read from GitHub for {0:N0} minutes (network or sign-in?)' -f $script:RunUnreadableMinutes) } }
        }
        if ((Get-Date) -ge $deadline) { return [pscustomobject]@{ Done = $false; Conclusion = ''; Problem = ('did not finish within {0:N0} minutes' -f $minutes) } }
        Start-Sleep -Seconds $script:RunPollSeconds
    }
}
# CI on a commit: waits while a run is queued or going (a release right after a merge used to stop here), fails on a red run.
function Wait-CiGreen([string]$sha, [int]$minutes) {
    $deadline = (Get-Date).AddMinutes($minutes); $said = $false; $seenAt = Get-Date; $n = 0; $lastBeat = Get-Date
    while ($true) {
        $runs = @(gh run list --repo $Repo --workflow ci.yml --commit $sha --json conclusion,status,databaseId | ConvertFrom-Json)
        if ($LASTEXITCODE -ne 0) { Fail 'gh could not list CI runs.' }
        # The newest run decides (a re-run that failed outranks an older green one).
        $last = $runs | Sort-Object { [long]$_.databaseId } -Descending | Select-Object -First 1
        if ($last -and $last.status -eq 'completed') {
            if ($last.conclusion -eq 'success') { return }
            Fail "CI (ci.yml) is $($last.conclusion) on $($sha.Substring(0,7)): https://github.com/$Repo/actions/runs/$($last.databaseId). Fix master first."
        }
        if (-not $runs.Count -and ((Get-Date) - $seenAt).TotalMinutes -gt 5) { Fail "No CI run (ci.yml) on $($sha.Substring(0,7)) after 5 minutes. Was it pushed through a PR?" }
        if ((Get-Date) -gt $deadline) { Fail "CI on $($sha.Substring(0,7)) is still running after $minutes minutes." }
        if (-not $said) { Write-Host "CI on $($sha.Substring(0,7)) is still running; waiting (up to $minutes min)."; $said = $true }
        # sign of life on the GitHub record every 4 minutes, so -Status does not show a long CI wait as a dead run
        if (((Get-Date) - $lastBeat).TotalMinutes -ge 4) {
            try { $null = Update-RemoteLock $Repo $LockId @{ stage = 'WaitCI' } 'release lock: waiting for CI'; $lastBeat = Get-Date }
            catch { if ($_.Exception.Message -match 'lock lost') { Fail $_.Exception.Message } }
        }
        $n++; Start-Sleep -Seconds (Get-BackoffSeconds $n 30)
    }
}
# The release now on the feed for this channel: @{ Version; Tag; Expires } or $null when the feed has none.
function Get-FeedRelease([string]$ch) {
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-feed-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $names = gh release view update-feed --repo $Repo --json assets --jq '.assets[].name' 2>&1
        if ($LASTEXITCODE -ne 0) { if ("$names" -match 'not found') { return $null }; Fail "Could not read the update-feed release: $names" }
        if (@($names) -notcontains "$ch.json") { return $null }
        gh release download update-feed --repo $Repo --pattern "$ch.json" --dir $tmp 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "Could not download $ch.json from the update feed." }
        $j = Get-Content -Raw (Join-Path $tmp "$ch.json") | ConvertFrom-Json
        return [pscustomobject]@{ Version = [string]$j.version; Tag = "v$($j.version)"; Expires = ([datetime]$j.expires).ToUniversalTime() }
    } finally { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue }
}

$lock = $null
Push-Location $repoRoot
try {
    # The bootstrap holds the locks (this PC and GitHub); this run proves on every stage that the GitHub record is still its own.
    foreach ($t in 'git', 'gh', 'go') { if (-not (Get-Command $t -ErrorAction SilentlyContinue)) { Fail "$t is not installed or not on PATH." } }
    & gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Fail 'gh is not signed in (run: gh auth login).' }
    try { Run 'git fetch' { git fetch origin $Branch --tags --quiet } | Out-Null } catch { Fail $_.Exception.Message }
    $tags = @(git tag -l 'v*')
    if ($LASTEXITCODE -ne 0) { Fail 'git could not list the version tags of this clone.' }

    # ---- which version, and where to start -------------------------------------------------------------------------------------
    $state = Get-ReleaseState $data
    $resuming = $false
    # A done or withdrawn release is finished; -CheckOnly never resumes. The same -Version as the stopped run also resumes it.
    $plan = Get-ResumePlan $state $askedChannel $Version $tags ([bool]$CheckOnly)
    if ($plan.Resuming) {
        $Version = $plan.Version; $resuming = $true
        foreach ($k in 'key', 'passref', 'prev_feed') { if ($state.$k) { $script:extra[$k] = [string]$state.$k } }
        Write-Host "Unfinished release $Version found (last finished stage: '$($state.stage)', $($state.status): $($state.reason)). Continuing it." -ForegroundColor Yellow
    }
    if (-not $Version) {
        if (-not $askedChannel) { Fail 'Pass -Channel rc or -Channel stable (or -Version).' }
        try { $Version = Get-NextVersion $tags $askedChannel } catch { Fail $_.Exception.Message }
        # Compared with the rc feed: an rc follows the rc feed, a stable release takes the X.Y.Z of the rc on the feed.
        $fr = Get-FeedRelease 'rc'
        if ($fr) {
            # A stray tag (typo, or a token holder) must not move every later version: the guess stays next to the signed feed.
            $a1 = ConvertTo-SemVer $Version; $b1 = ConvertTo-SemVer $fr.Version
            $sameCore = $a1.Major -eq $b1.Major -and $a1.Minor -eq $b1.Minor -and $a1.Patch -eq $b1.Patch
            $nextPatch = $askedChannel -eq 'rc' -and $a1.Major -eq $b1.Major -and $a1.Minor -eq $b1.Minor -and $a1.Patch -eq $b1.Patch + 1
            if (-not ($sameCore -or $nextPatch)) { Fail "The next version from the tags is $Version, far from the $askedChannel feed ($($fr.Version)). A stray tag? Pass -Version to say what you want." }
        }
        Write-Host "Next $askedChannel version: $Version"
    }
    if (-not (ConvertTo-SemVer $Version)) { Fail "Version '$Version' is not X.Y.Z or X.Y.Z-rc.N (lower case, no leading v)." }
    $tag = "v$Version"
    $channel = if ($Version.Contains('-')) { 'rc' } else { 'stable' }
    if ($askedChannel -and $askedChannel -ne $channel) { Fail "Version $Version is a $channel version, not $askedChannel." }
    if (-not $From) {
        $From = if ($resuming) { Get-ResumeStage $order ([string]$state.stage) } else { 'Preflight' }
        # A tag is only ever made right after a preflight in the same run (master may have moved since the last one).
        # If the tag is already on GitHub, the build is what comes next.
    }
    if ($From -eq 'Tag') {
        $lr = git ls-remote --tags origin "refs/tags/$tag"
        if ($LASTEXITCODE -ne 0) { Fail "git could not ask GitHub whether the tag $tag exists (network?). Nothing was done; run the same command again." }
        $From = if ($lr) { 'Wait' } else { 'Preflight' }
    }
    if ($resuming -and $state.status -eq 'withdraw-pending') {
        # Verify did not pass last time and the withdraw failed: only the withdraw is left to do. Never verify again.
        $From = 'Verify'; $script:withdrawOnly = $true
        Write-Host "The last run's verify did not pass and its withdraw failed. Retrying the withdraw only." -ForegroundColor Yellow
    }
    if (-not $From) { Write-Host "Release $tag already finished." -ForegroundColor Green; exit 0 }
    $script:ownsVersion = $true
    if (-not $CheckOnly) { Set-LockStage $From @{ version = $Version; channel = $channel } }
    if ($CheckOnly -and $From -ne 'Preflight') { Fail '-CheckOnly only works from Preflight.' }
    function Reach([string]$s) { $order.IndexOf($s) -ge $order.IndexOf($From) }
    $script:stage = $From
    if (-not $CheckOnly) { Write-AuditEntry $audit @{ event = 'run-start'; version = $Version; from = $From; yes = [bool]$Yes; machine = [Environment]::MachineName; user = [Environment]::UserName; head = "$(git rev-parse HEAD)".Trim() } }

    # ---- keys (every run: the Sign stage and a withdraw need them) --------------------------------------------------------------
    $lrTag = git ls-remote --tags origin "refs/tags/$tag"
    if ($LASTEXITCODE -ne 0) { Fail "git could not ask GitHub whether the tag $tag exists (network?). Nothing was tagged or published; run the same command again." }
    $tagExists = [bool]$lrTag
    $refCommit = if ($tagExists -and (git tag -l $tag)) { git rev-list -n 1 $tag } else { git rev-parse HEAD }
    if ($LASTEXITCODE -ne 0 -or -not $refCommit) { Fail "git could not find the commit of $tag (or of this checkout's HEAD)." }
    $refCommit = "$refCommit".Trim()
    if ($tagExists -and $refCommit -ne $Pinned) { Fail "Tag $tag is on commit $($refCommit.Substring(0,7)) but this run was pinned to $($Pinned.Substring(0,7)). Another run made that tag; run -Status." }
    $feed = Get-FeedRelease $channel
    if (-not $feed -and -not $FirstRelease) { Fail "The feed has no $channel.json. If this really is the first $channel release, add -FirstRelease." }
    if ($feed -and $FirstRelease -and $feed.Tag -ne $tag) { Fail "-FirstRelease was passed, but the feed already points at $($feed.Tag)." }
    if ($feed -and $feed.Tag -eq $tag) { $feedBase = $null } else { $feedBase = $feed }
    if ($feed) {
        git rev-parse --verify --quiet "refs/tags/$($feed.Tag)^{commit}" | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "The feed's release $($feed.Tag) is not in this clone (git fetch --tags)." }
    }
    if ($KeyFile) { $KeyDir = Split-Path -Parent $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($KeyFile) }
    if (-not (Test-Path -LiteralPath $KeyDir -PathType Container)) { Fail "Key folder $KeyDir not found. Is the key drive plugged in? (-KeyDir to use another folder)" }
    $owner = Get-OwnerKeys $KeyDir
    $trustedNewText = (git show "${refCommit}:$script:ReleaseTrustedKeysPath") -join "`n"
    if ($LASTEXITCODE -ne 0 -or -not $trustedNewText.Trim()) { Fail "git could not read $script:ReleaseTrustedKeysPath at $($refCommit.Substring(0,7))." }
    # The keys installed copies accept today: those of the release on the feed (for a resumed run past Feed, the release that was on
    # the feed before this one, kept in state.json).
    $oldTag = if ($feed -and $feed.Tag -eq $tag -and $script:extra.prev_feed) { $script:extra.prev_feed } elseif ($feed) { $feed.Tag } else { $null }
    if ($feed -and $feed.Tag -eq $tag -and -not $script:extra.prev_feed) { Write-Host "  note: the feed already serves $tag and the release before it is not recorded; key checks use $tag's own list." -ForegroundColor Yellow }
    $trustedOldText = if ($oldTag) { (git show "${oldTag}:$script:ReleaseTrustedKeysPath") -join "`n" } else { $null }
    if ($oldTag -and ($LASTEXITCODE -ne 0 -or -not $trustedOldText.Trim())) { Fail "git could not read $script:ReleaseTrustedKeysPath at $oldTag (git fetch --tags?)." }
    $trustedOld = if ($oldTag) { ConvertFrom-TrustedKeys $trustedOldText } else { $null }
    $sel = Select-SigningKey $channel $owner (ConvertFrom-TrustedKeys $trustedNewText) $trustedOld $KeyFile
    foreach ($p in $sel.Problems) { Write-Host "  key problem: $p" -ForegroundColor Yellow }
    if (-not $sel.Usable.Count) { Fail "No usable $channel key in ${KeyDir}: $($sel.Problems -join '; ')" }
    if ($KeyFile -and -not $sel.Key) { Fail "The key you passed cannot be used: $($sel.Problems -join '; ')" }
    $candidates = if ($KeyFile) { @($sel.Key) } else { @($sel.Usable) }
    $chosen = $candidates[0]
    if ($resuming -and $script:extra.key -and -not $KeyFile) {
        # Use the key preflight checked (its passphrase opened it), not simply the first usable one.
        $chosen = $candidates | Where-Object { $_.Name -eq $script:extra.key } | Select-Object -First 1
        if (-not $chosen) { Fail "The key preflight chose, $($script:extra.key), cannot be used now: $($sel.Problems -join '; ')" }
    }
    $passRef = if ($PassphraseFromProtonPass) { $PassphraseFromProtonPass } elseif ($resuming -and $script:extra.passref -and $chosen.Name -eq $script:extra.key) { $script:extra.passref } else { Get-ProtonPassRefForKey $chosen.Name }
    if ($Yes -and -not $passRef -and $resuming) { Fail "No Proton Pass entry is known for key $($chosen.Name), and -Yes never asks for a passphrase. Pass -PassphraseFromProtonPass." }
    if (-not $resuming) { $script:extra = @{ prev_feed = "$($feed.Tag)" } }

    # 1 Preflight -------------------------------------------------------------------------------------------------------------
    if (Reach 'Preflight') {
        Stage 'Preflight' 'Preflight (changes nothing)'
        # The bootstrap pinned a commit and made this worktree from it: nothing here depends on the shared working folder or on master
        # staying where it was. The commit must still be part of master (a force-push could have removed it).
        $dirty = git status --porcelain
        if ($LASTEXITCODE -ne 0) { Fail 'git could not tell whether the release worktree is clean.' }
        if ($dirty) { Fail 'The release worktree has changes. A release is built from a clean commit.' }
        $head = git rev-parse HEAD
        if ($LASTEXITCODE -ne 0 -or -not $head) { Fail 'git could not read the commit of the release worktree.' }
        $head = "$head".Trim()
        if ($head -ne $Pinned) { Fail "This worktree is at $($head.Substring(0,7)), not the pinned commit $($Pinned.Substring(0,7))." }
        git fetch origin $Branch --quiet 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "git fetch of origin/$Branch failed (network?), so it cannot be checked that the pinned commit is still part of it." }
        git merge-base --is-ancestor $head "origin/$Branch"
        if ($LASTEXITCODE -ne 0) { Fail "The pinned commit $($head.Substring(0,7)) is no longer part of origin/$Branch (history was rewritten?). Not releasing it." }
        $script:checkedHead = $head
        # BL-194: which Go builds the signing tool. 'local' is what is installed; the plain call is what go.mod asks for (GOTOOLCHAIN=auto
        # fetches and checksum-verifies a newer one when go.mod needs it). Both go into the audit log so a switch is visible afterwards.
        $prevTc = $env:GOTOOLCHAIN
        try { $env:GOTOOLCHAIN = 'local'; $goInstalled = ((go version 2>&1 | Out-String).Trim() -replace '\s+', ' ') } finally { $env:GOTOOLCHAIN = $prevTc }
        $goUsed = ((go version 2>&1 | Out-String).Trim() -replace '\s+', ' ')
        $script:report.go = @{ installed = $goInstalled; used = $goUsed }
        if ($goInstalled -ne $goUsed) { Write-Host "Go: installed '$goInstalled', go.mod asks for '$goUsed' (toolchain fetched and checked by Go)." -ForegroundColor Yellow }
        if ($tagExists) { Fail "Tag $tag is already on GitHub. Run again without -From to continue that release, or pass a newer -Version." }
        if (git tag -l $tag) {
            $ltc = git rev-list -n 1 $tag
            if ($LASTEXITCODE -ne 0 -or -not $ltc) { Fail "git could not find the commit of the local tag $tag. Delete it (git tag -d $tag) if it was never pushed." }
            if ("$ltc".Trim() -ne $head) { Fail "A local tag $tag points at another commit. Delete it (git tag -d $tag) if it was never pushed." }
        }
        $existing = @((Get-GhJson 'list the existing releases' @('release', 'list', '--repo', $Repo, '--limit', '100', '--json', 'tagName')).tagName)
        if ($existing -contains $tag) { Fail "A release $tag already exists." }
        try { Assert-NextVersion $Version @($tags | Where-Object { $_ -ne $tag }) } catch { Fail $_.Exception.Message }
        Wait-CiGreen $head $CiWaitMinutes
        Write-Host "CI green on $($head.Substring(0,7))."
        $risks = Get-PassphraseLoggingRisks
        if ($risks.Count) { Fail "This PC could log the passphrase: $($risks -join '; '). Turn that policy off for release runs." }
        if ($feedBase) {
            $prs = Get-CommitPullRequests $repoRoot $Repo $feedBase.Tag $head
            if ($prs.Missing.Count) { Fail "These commits on $Branch since $($feedBase.Tag) (or, inside a promotion of develop, a pull request merged into develop or a clean merge of master) did not come through a merged pull request: $(($prs.Missing | ForEach-Object { $_.Substring(0,7) }) -join ', '). Find out who pushed them before releasing." }
            $script:report.sensitive = Get-SensitivePullRequests $repoRoot $feedBase.Tag $head $prs.Prs $prs.Develop
            $feedKeysText = (git show "$($feedBase.Tag):$script:ReleaseTrustedKeysPath") -join "`n"
            if ($LASTEXITCODE -ne 0 -or -not $feedKeysText.Trim()) { Fail "git could not read $script:ReleaseTrustedKeysPath at $($feedBase.Tag) (git fetch --tags?)." }
            $kc = Compare-KeyLists $feedKeysText $trustedNewText $owner
            if ($kc.Foreign.Count) { Fail "trusted_keys.txt gained a key you hold no key file for in ${KeyDir}: $(($kc.Foreign | ForEach-Object { "$($_.Role) $($_.Pub)" }) -join '; '). Someone else's key; do not release." }
            $script:addedKeys = @($kc.Added)
            if ($kc.Added.Count -or $kc.Removed.Count) { Write-Host "Trusted keys changed since $($feedBase.Tag): $($kc.Added.Count) added (all yours), $($kc.Removed.Count) removed." }
            $left = ($feedBase.Expires - (Get-Date).ToUniversalTime()).TotalDays
            if ($left -lt 30) { Write-Host ("The $channel feed expires in {0:N0} days; this release renews it." -f $left) -ForegroundColor Yellow }
        }
        if (-not $feedBase) {
            # First release of the channel: every listed key must be one the owner holds.
            $kc = Compare-KeyLists '' $trustedNewText $owner
            $script:addedKeys = @($kc.Added)
            if ($kc.Foreign.Count) { Fail "trusted_keys.txt lists a key you hold no key file for in ${KeyDir}: $(($kc.Foreign | ForEach-Object { "$($_.Role) $($_.Pub)" }) -join '; '). Someone else's key; do not release." }
        }
        # The signing tool (it will see the passphrase) may depend only on the standard library and internal/update. Built and listed
        # from a throw-away worktree of HEAD, never from the working folder (hidden or ignored files there do not count).
        $wt = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-pre-' + [guid]::NewGuid().ToString('N'))
        try { Run 'git worktree add' { git worktree add --detach $wt $head } | Out-Null } catch { Fail $_.Exception.Message }
        try {
            try { $bad = Get-ForbiddenSigtoolDeps (Get-SigtoolNonStdDeps $wt) } catch { Fail $_.Exception.Message }
            if ($bad.Count) { Fail "The signing tool depends on code outside the standard library and internal/update: $($bad -join ', ')." }
        # The passphrase must open the key now, before anything is tagged. A key whose passphrase fails is skipped for the next one.
        if ($Yes -or $PassphraseFromProtonPass) {
            $tool = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-keycheck-' + [guid]::NewGuid().ToString('N') + '.exe')
            try {
                Push-Location $wt
                try { Run 'building the key check tool' { go build -mod=readonly -o $tool ./tools/release/sigtool } | Out-Null } catch { Fail $_.Exception.Message } finally { Pop-Location }
                if ($feed) {
                    # The feed's JSON names the baseline of every check above; it must carry a real signature for the release it names.
                    $fd = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-feedsig-' + [guid]::NewGuid().ToString('N'))
                    New-Item -ItemType Directory -Path $fd | Out-Null
                    try {
                        gh release download update-feed --repo $Repo --pattern "$channel.json*" --dir $fd 2>&1 | Out-Null
                        if ($LASTEXITCODE -ne 0) { Fail 'Could not download the feed pair to check its signature.' }
                        $feedKeys = git show "$($feed.Tag):$script:ReleaseTrustedKeysPath"
                        if ($LASTEXITCODE -ne 0) { Fail "git could not read $script:ReleaseTrustedKeysPath at $($feed.Tag) (git fetch --tags?)." }
                        $feedKeys | Set-Content -LiteralPath (Join-Path $fd 'keys.txt')
                        $named = (& $tool feedsig -in (Join-Path $fd "$channel.json") -sig (Join-Path $fd "$channel.json.sig") -keys (Join-Path $fd 'keys.txt') -want $channel 2>&1 | Out-String).Trim()
                        if ($LASTEXITCODE -ne 0 -or "v$named" -ne $feed.Tag) { Fail "The $channel feed file does not carry a valid signature for $($feed.Tag) ($named). Someone may have changed it; do not release." }
                    } finally { Remove-Item -LiteralPath $fd -Recurse -Force -ErrorAction SilentlyContinue }
                    Write-Host "Feed signature OK: it really names $($feed.Tag)."
                }
                $ok = $null; $why = @()
                foreach ($k in $candidates) {
                    $ref = if ($PassphraseFromProtonPass) { $PassphraseFromProtonPass } else { Get-ProtonPassRefForKey $k.Name }
                    if (-not $ref) { $why += "$($k.Name): no Proton Pass entry is known for it (pass -PassphraseFromProtonPass)"; continue }
                    try { $sec = Get-ProtonPassSecret -Reference $ref } catch { $why += "$($k.Name): Proton Pass: $($_.Exception.Message)"; continue }
                    # sigtool's own error (no secret in it) says which: wrong passphrase, damaged file, wrong role.
                    try { $kout = Invoke-WithPassphrase $sec $tool @('keycheck', '-key', $k.Path, '-role', $channel) 2>&1 } finally { $sec.Dispose() }
                    if ($LASTEXITCODE -eq 0) { $ok = $k; $passRef = $ref; break }
                    $why += "$($k.Name) with '$ref': $(if ($kout) { (@($kout) | ForEach-Object { "$_" }) -join ' ' -replace '^sigtool:\s*', '' } else { 'the passphrase does not open this key file, or the file is damaged' })"
                }
            if (-not $ok) { Fail "No $channel key could be opened: $($why -join '; ')" }
            # A key file is only proof of ownership if it opens. Every key added to trusted_keys.txt must open with its own Proton Pass
            # entry, so a planted look-alike JSON file in the key folder does not count.
            foreach ($a in @($script:addedKeys)) {
                $f = $owner | Where-Object { -not $_.Problem -and $_.Role -eq $a.Role -and $_.Pub -eq $a.Pub } | Select-Object -First 1
                $ref = if ($f) { Get-ProtonPassRefForKey $f.Name } else { $null }
                if (-not $f -or -not $ref) { Fail "Added trusted key $($a.Role) $($a.Pub.Substring(0,16))... has no key file with a known Proton Pass entry. Not releasing." }
                try { $sec = Get-ProtonPassSecret -Reference $ref } catch { Fail "Added trusted key $($f.Name): Proton Pass: $($_.Exception.Message)" }
                try { $null = Invoke-WithPassphrase $sec $tool @('keycheck', '-key', $f.Path, '-role', $a.Role) 2>&1 } finally { $sec.Dispose() }
                if ($LASTEXITCODE -ne 0) { Fail "Added trusted key $($f.Name) does not open with its Proton Pass entry: it is not proven to be yours. Not releasing." }
                Write-Host "  added key $($f.Name) opens with its own passphrase."
            }
            } finally { Remove-Item -LiteralPath $tool -Force -ErrorAction SilentlyContinue }
            foreach ($w in $why) { Write-Host "  skipped $w" -ForegroundColor Yellow }
            $chosen = $ok
            Write-Host "Key: $($chosen.Name) ($($chosen.Pub.Substring(0,16))...), passphrase from Proton Pass opens it." -ForegroundColor Green
        } else { Write-Host "Key: $($chosen.Name). No Proton Pass reference: you will type the passphrase at the Sign stage." }
        } finally { git worktree remove --force $wt 2>&1 | Out-Null; git worktree prune 2>&1 | Out-Null }
        $script:extra = @{ key = $chosen.Name; passref = "$passRef"; prev_feed = "$($feed.Tag)" }
        & (Join-Path $PSScriptRoot 'Test-Release.ps1') | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail 'Test-Release.ps1 failed.' }
        & (Join-Path $PSScriptRoot 'Test-NoUpx.Tests.ps1') | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail 'Test-NoUpx.Tests.ps1 failed.' }
        Write-Host "Preflight OK: $Branch at $($head.Substring(0,7)), CI green, $tag is free, key $($chosen.Name)." -ForegroundColor Green
        if ($CheckOnly) { Write-Host "`nCHECK ONLY. Would release $tag with key $($chosen.Name). Nothing was changed." -ForegroundColor Yellow; exit 0 }
        Write-AuditEntry $audit @{ event = 'preflight-ok'; version = $Version; commit = $head; go_installed = "$($script:report.go.installed)"; go_used = "$($script:report.go.used)"; key = $chosen.Name; key_pub = $chosen.Pub; feed = "$($feed.Tag)"; sensitive_prs = @($script:report.sensitive | Where-Object { $_ }) }
        Done 'Preflight'
    }
    # 2 Tag -------------------------------------------------------------------------------------------------------------------
    if (Reach 'Tag') {
        Stage 'Tag' 'Tag and push'
        # Tag exactly the commit preflight checked, and only while master still is that commit (another session may pull meanwhile).
        if (-not $script:checkedHead) { Fail 'No preflight ran in this run; refusing to tag. Run again without -From.' }
        $head = $script:checkedHead
        # master may have moved on meanwhile; that is fine. What is tagged is the pinned commit, and it must still be part of master.
        git fetch origin $Branch --quiet 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "git fetch of origin/$Branch failed (network?). Nothing was tagged; run the same command again." }
        git merge-base --is-ancestor $head "origin/$Branch"
        if ($LASTEXITCODE -ne 0 -or "$(git rev-parse HEAD)".Trim() -ne $head) { Fail "The pinned commit $($head.Substring(0,7)) is not part of origin/$Branch any more, or this worktree changed. Run again." }
        $remoteTag = (git ls-remote --tags origin "refs/tags/$tag")
        if ($LASTEXITCODE -ne 0) { Fail "git could not ask GitHub whether the tag $tag exists (network?). Nothing was tagged; run the same command again." }
        $localTag = (git tag -l $tag)
        if ($localTag -and -not $remoteTag) {
            $ltc = git rev-list -n 1 $tag
            if ($LASTEXITCODE -ne 0 -or -not $ltc) { Fail "git could not find the commit of the local tag $tag. Delete it (git tag -d $tag) if it was never pushed." }
            if ("$ltc".Trim() -ne $head) { Fail "Local tag $tag points at a different commit than HEAD ($($head.Substring(0,7)))." }
        }
        if (-not $remoteTag) {
            Confirm-Typed $tag "This creates tag $tag on $($head.Substring(0,7)) ($(git log -1 --format=%s)) and pushes it. CI then builds a DRAFT release."
            # Written BEFORE the push: if the push succeeds but its answer is lost, this line is what lets a later run resume that tag.
            Write-AuditEntry $audit @{ event = 'tagging'; version = $Version; commit = $head }
            try {
                if (-not $localTag) { Run 'git tag' { git -c tag.gpgSign=false tag $tag $head } | Out-Null }
                Run 'git push tag' { git push origin $tag } | Out-Null
            } catch { Stop-AfterFailedTagPush $tag $head $_.Exception.Message }
            Write-Host "Pushed $tag."
            Write-AuditEntry $audit @{ event = 'tagged'; version = $Version; commit = $head }
        } else { Write-Host "Tag $tag is already on GitHub; continuing." }
        # The commit is fixed from here on (the tag): merges to master no longer have to wait.
        Set-LockStage 'Tag' @{ holdMerges = $false }
        Done 'Tag'
    }
    # 3 Wait ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Wait') {
        Stage 'Wait' 'Waiting for the CI build, attestation, smoke test and draft'
        $sha = git rev-list -n 1 $tag
        if ($LASTEXITCODE -ne 0 -or -not $sha) { Fail "git could not find the commit of the tag $tag in this clone (git fetch --tags?). Run the same command again." }
        $sha = "$sha".Trim()
        $id = if ($RunId) { $RunId } else { Find-Run 'release.yml' $tag $sha 180 }
        if (-not $id) { Fail 'The release.yml run did not start within 3 minutes. Check the Actions tab.' }
        $runUrl = "https://github.com/$Repo/actions/runs/$id"
        $res = Wait-RunFinished $id $RunWatchMinutes
        if (-not $res.Done) { Fail "The release.yml run $id $($res.Problem): $runUrl . Release $tag is NOT published and nobody is offered it. Look at the run on GitHub: if it is stuck, cancel it there. Then run the same command again (it continues at this step; add -RunId $id to wait for this same run, or -RunWatchMinutes up to 170 to wait longer)." }
        if ($res.Conclusion -ne 'success') { Fail "release.yml run $id failed ($($res.Conclusion)): $runUrl" }
        $drafts = @(Get-GhJson 'list the releases' @('release', 'list', '--repo', $Repo, '--limit', '30', '--json', 'tagName,isDraft')) | Where-Object { $_.tagName -eq $tag }
        if (-not $drafts) { Fail "CI finished but no release $tag was found." }
        Write-Host "CI finished. Draft release $tag is ready." -ForegroundColor Green
        Write-AuditEntry $audit @{ event = 'built'; version = $Version; run = "$id" }
        Done 'Wait'
    }
    # 4 Sign ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Sign') {
        Stage 'Sign' "Sign with $($chosen.Name)"
        $a = @{ Tag = $tag; KeyFile = $chosen.Path; Repo = $Repo }
        if ($Yes) { $a.Yes = $true }
        if ($FirstRelease) { $a.FirstRelease = $true }
        if ($passRef) { $a.ProtonPassRef = $passRef }
        try { & (Join-Path $PSScriptRoot 'Sign-Release.ps1') @a } catch { Fail $_.Exception.Message }
        Done 'Sign'
    }
    # 5 Publish ---------------------------------------------------------------------------------------------------------------
    if (Reach 'Publish') {
        Stage 'Publish' 'Publish the release'
        $rel = @(Get-GhJson 'read the releases' @('api', "repos/$Repo/releases?per_page=50") | Where-Object { $_.tag_name -eq $tag }) | Select-Object -First 1
        if (-not $rel) { Fail "Release $tag not found." }
        if (@($rel.assets.name) -notcontains 'update-manifest.json.sig') { Fail "The draft has no update-manifest.json.sig. Run the Sign stage first (-From Sign). Publishing without it would leave a public release nobody can be offered." }
        if ($rel.draft) {
            if ($rel.body) { Write-Host (($rel.body -split "`n" | Select-Object -First 15) -join "`n") }
            Confirm-Typed 'publish' "This makes $tag public ($channel channel). Nobody is offered it until the feed step."
            try { Run 'publish' { gh release edit $tag --repo $Repo --draft=false } | Out-Null } catch { Fail $_.Exception.Message }
            Write-AuditEntry $audit @{ event = 'published'; version = $Version }
        }
        Write-Host "$tag is published." -ForegroundColor Green
        Done 'Publish'
    }
    # 6 Feed ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Feed') {
        Stage 'Feed' 'Update the signed feed'
        Confirm-Typed 'feed' "This moves the $channel feed to $tag. From now on every installed copy on this channel is offered the update."
        $a = @{ Tag = $tag; Feed = $true; Repo = $Repo }
        if ($FirstRelease) { $a.FirstRelease = $true }
        try { & (Join-Path $PSScriptRoot 'Sign-Release.ps1') @a } catch { Fail $_.Exception.Message }
        # Read the feed back: what GitHub now serves must be byte for byte the signed pair of this release.
        $back = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-feedback-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path (Join-Path $back 'feed'), (Join-Path $back 'rel') | Out-Null
        try {
            gh release download update-feed --repo $Repo --pattern "$channel.json*" --dir (Join-Path $back 'feed') 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) { Fail 'Could not read the feed back after updating it.' }
            gh release download $tag --repo $Repo --pattern 'update-manifest.json*' --dir (Join-Path $back 'rel') 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) { Fail "Could not download the signed manifest of $tag." }
            $fm = Get-FileSha256 (Join-Path $back "feed/$channel.json"); $rm = Get-FileSha256 (Join-Path $back 'rel/update-manifest.json')
            $fs = Get-FileSha256 (Join-Path $back "feed/$channel.json.sig"); $rs = Get-FileSha256 (Join-Path $back 'rel/update-manifest.json.sig')
            if ($fm -ne $rm -or $fs -ne $rs) { Fail "The feed GitHub serves is not the signed pair of $tag (someone else changed it, or the upload half failed). Run -From Feed again." }
        } finally { Remove-Item -LiteralPath $back -Recurse -Force -ErrorAction SilentlyContinue }
        Write-Host "The $channel feed now serves $tag (read back and matched, manifest sha256 $($fm.Substring(0,16))...)." -ForegroundColor Green
        Write-AuditEntry $audit @{ event = 'feed-updated'; version = $Version; manifest_sha256 = $fm; sig_sha256 = $fs }
        $script:report.manifest = $fm
        Done 'Feed'
    }
    # 7 Verify ----------------------------------------------------------------------------------------------------------------
    if (Reach 'Verify') {
        # A pending withdraw is not a verify that can be skipped: skipping would overwrite 'withdraw-pending' and a later run would call the release done.
        if ($SkipVerify -and $script:withdrawOnly) { Fail '-SkipVerify cannot skip a pending withdraw: the last verify did not pass, so this release still has to be withdrawn. Run the same command again without -SkipVerify.' 'withdraw-pending' }
        if ($SkipVerify) { Write-Host "`nVerify skipped by -SkipVerify. Run the verify-release workflow yourself." -ForegroundColor Yellow; Set-ReleaseState $data $Version 'Feed' 'stopped' 'verify skipped' $script:extra; exit 0 }
        Stage 'Verify' 'Verify on a clean machine (verify-release.yml)'
        $id = $null; $why = $null
        if ($script:withdrawOnly) { $why = "an earlier verify did not pass: $($state.reason)" }
        if (-not $why -and -not $Previous -and -not $FirstRelease) {
            $list = gh release list --repo $Repo --limit 50 --json tagName,isDraft,publishedAt
            if ($LASTEXITCODE -ne 0) { $why = 'the list of earlier releases could not be read, so the upgrade test cannot run' }
            $rels = @(); $unread = @()
            if (-not $why) { try { $rels = @(($list -join "`n") | ConvertFrom-Json) } catch { $why = 'the list of earlier releases could not be read, so the upgrade test cannot run' } }
            foreach ($r in ($rels | Where-Object { -not $_.isDraft -and $_.tagName -match '^v[0-9]' -and $_.tagName -ne $tag -and ($_.tagName.Contains('-') -eq ($channel -eq 'rc')) } | Sort-Object publishedAt -Descending)) {
                $assets = (gh api "repos/$Repo/releases/tags/$($r.tagName)" --jq '.assets[].name' 2>$null)
                if ($LASTEXITCODE -ne 0) { $unread += $r.tagName; continue }   # try the next older one; said below if none is found
                if ($assets -contains 'update-manifest.json.sig') { $Previous = $r.tagName; break }
            }
            # A missing previous release would quietly drop the upgrade test; treat it as a verify that cannot run.
            if (-not $why -and -not $Previous) { $why = if ($unread.Count) { "the files of $($unread -join ', ') could not be read from GitHub, so no earlier signed release was found and the upgrade test cannot run" } else { 'no earlier signed release was found to upgrade from, so the upgrade test cannot run' } }
        }
        # A one-off id in the run's name, so this run is found and no other (another session's verify, or a clock that is off).
        $nonce = [guid]::NewGuid().ToString('N').Substring(0, 12)
        $wf = @('workflow', 'run', 'verify-release.yml', '--repo', $Repo, '--ref', $Branch, '-f', "tag=$tag", '-f', "nonce=$nonce")
        if ($Previous) { $wf += @('-f', "previous=$Previous"); Write-Host "Upgrading from $Previous." } else { Write-Host 'No earlier signed release: part A only (feed, offline verify, tamper refusals).' }
        if (-not $why) { try { Run 'start verify' { gh @wf } | Out-Null } catch { $why = "the verify run could not be started: $($_.Exception.Message)" } }
        if (-not $why) {
            $deadline = (Get-Date).AddMinutes(3)
            while (-not $id -and (Get-Date) -lt $deadline) {
                Start-Sleep -Seconds 4
                $vl = gh run list --repo $Repo --workflow verify-release.yml --limit 20 --json databaseId,displayTitle
                if ($LASTEXITCODE -ne 0) { continue }   # a failed list is tried again until the 3 minutes are over; then it is said below
                $vtext = (@($vl | ForEach-Object { "$_" }) -join "`n").Trim()
                if (-not $vtext) { continue }
                # An answer that is not JSON is tried again like a failed list (it would otherwise throw and skip the withdraw below).
                try { $id = ($vtext | ConvertFrom-Json | Where-Object { "$($_.displayTitle)" -match [regex]::Escape($nonce) } | Select-Object -First 1).databaseId } catch { $id = $null }
            }
            if (-not $id) { $why = "the verify run (id $nonce) did not show up within 3 minutes (or GitHub could not be asked)" }
        }
        if ($id -and -not $why) {
            # Not finished in time counts as not proven good, like a failed run: the withdraw below keeps copies from being offered it.
            $vres = Wait-RunFinished $id $RunWatchMinutes
            if (-not $vres.Done) { $why = "the clean-machine check $($vres.Problem): https://github.com/$Repo/actions/runs/$id" }
            elseif ($vres.Conclusion -ne 'success') { $why = "it failed its clean-machine check ($($vres.Conclusion)): https://github.com/$Repo/actions/runs/$id" }
        }
        if ($why) {
            # Not proven good while every copy is already offered it: withdraw it now (signed control file that blocks this version,
            # keeping any pause or blocks already in force). An unverified release is treated like a failed one.
            Write-Host "`nVERIFY DID NOT PASS: $why" -ForegroundColor Red
            # Saved before anything else can go wrong (an audit write, Ctrl+C at the passphrase prompt, a killed slot): from here on the only
            # thing a later run may do with this release is withdraw it, never verify it again.
            if (-not $script:withdrawOnly) { Set-ReleaseState $data $Version 'Feed' 'withdraw-pending' (Protect-ReasonText $why) $script:extra }   # (a retry already is withdraw-pending, with the first reason)
            if (-not $script:withdrawOnly) { Write-AuditEntry $audit @{ event = 'verify-failed'; version = $Version; run = "$id"; reason = $why } }
            $script:stage = 'Verify'
            Write-Host "Withdrawing $Version (signed control file) so machines that have not installed it are not offered it." -ForegroundColor Yellow
            if ($Yes -and -not $passRef) { Fail "Verify did not pass ($why) and no Proton Pass entry is known to sign the withdraw. Run Set-Control.ps1 -Block $Version now." 'withdraw-pending' }
            # 90 days, the longest a control file lives; a newer release (or Set-Control) must follow before then.
            $c = @{ Channel = $channel; KeyFile = $chosen.Path; Block = @($Version); Note = "Withdrawn: $Version did not pass its clean-machine check."; Repo = $Repo; Days = 90 }
            if ($Yes) { $c.Yes = $true }
            if ($passRef) { $c.ProtonPassRef = $passRef }
            # The release that was on the feed before this one: installed copies on it must accept the control file.
            $base = if ($script:extra.prev_feed) { $script:extra.prev_feed } else { $null }
            if ($base -and $base -ne $tag) { $c.TrustBaseline = $base }
            # Set-Control keeps the holds already in force after checking their signature, and signs from this checkout even if master
            # moved on meanwhile (no fast-forward: the scripts that run stay the ones this release run started with).
            $c.KeepInForce = $true; $c.AllowBehindMaster = $true
            $manual = "./tools/release/Set-Control.ps1 -Channel $channel -KeyFile '$($chosen.Path)' -Block $Version -Days 90 -KeepInForce -Yes -ProtonPassRef '$passRef'"
            $dirty = git status --porcelain
            if ($LASTEXITCODE -ne 0) { Fail "Verify did not pass ($why) and git could not tell whether this checkout is clean, so the withdraw did not start. Run again (it only withdraws), or run: $manual" 'withdraw-pending' }
            if ($dirty) { Fail "Verify did not pass ($why). The withdraw needs a clean checkout here. Clean it and run again (it only withdraws), or run: $manual" 'withdraw-pending' }
            try { & (Join-Path $PSScriptRoot 'Set-Control.ps1') @c } catch {
                Fail "Verify did not pass ($why) AND the withdraw failed: $($_.Exception.Message). Run again (it only withdraws) or run: $manual" 'withdraw-pending'
            }
            Write-AuditEntry $audit @{ event = 'withdrawn'; version = $Version; run = "$id" }
            Fail "Verify did not pass ($why). $Version is WITHDRAWN on the $channel feed. Fix the cause; the next run makes a newer version." 'withdrawn'
        }
        Write-Host "`nVerified on a clean machine: https://github.com/$Repo/actions/runs/$id" -ForegroundColor Green
        Write-AuditEntry $audit @{ event = 'verified'; version = $Version; run = "$id" }
        $script:report.verify = "https://github.com/$Repo/actions/runs/$id"
    }
    Set-ReleaseState $data $Version 'Verify' 'done' '' $script:extra
    Write-AuditEntry $audit @{ event = 'done'; version = $Version }
    Write-Host "`nRelease $tag is done." -ForegroundColor Green
    Write-Host "  key:        $($chosen.Name) ($($chosen.Pub.Substring(0,16))...)"
    if ($script:report.manifest) { Write-Host "  manifest:   sha256 $($script:report.manifest)" }
    if ($script:report.verify) { Write-Host "  verify run: $($script:report.verify)" }
    if ($script:report.sensitive) { Write-Host "  PRs that changed build, signing or trust code: $($script:report.sensitive -join ', ')" -ForegroundColor Yellow }
    Write-Host "  audit log:  $audit"
    exit 0
} finally {
    Pop-Location
    if ($lock) { $lock.Dispose() }
}
