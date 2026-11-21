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

if ($CheckAudit) {
    $c = Test-AuditChain $audit
    if ($c.Ok) { Write-Host "Audit log OK: $($c.Lines) entries, chain unbroken ($audit)." -ForegroundColor Green; exit 0 }
    Write-Host "Audit log BROKEN at line $($c.BrokenAt) of $($c.Lines): that line or the one before it was changed or removed ($audit)." -ForegroundColor Red; exit 1
}

$script:stage = 'Preflight'
$script:report = [ordered]@{}
function Stage([string]$name, [string]$what) { $script:stage = $name; Write-Host "`n[$($order.IndexOf($name) + 1)/7] $what" -ForegroundColor Cyan }
$script:extra = @{}
$script:ownsVersion = $false   # state.json is only written once this run has settled which release it is working on
$script:withdrawOnly = $false
$script:addedKeys = @()
$script:checkedHead = $null
function Done([string]$name) { if (-not $CheckOnly) { Set-ReleaseState $data $Version $name 'running' '' $script:extra } }
function Fail([string]$msg, [string]$status = 'stopped') {
    Write-Host "`nSTOPPED at $script:stage : $msg" -ForegroundColor Red
    if ($script:ownsVersion -and -not $CheckOnly) {
        Set-ReleaseState $data $Version (Get-LastDone) $status $msg $script:extra
        try { Write-AuditEntry $audit @{ event = 'stopped'; version = $Version; stage = $script:stage; reason = $msg } } catch {}
        if ($status -ne 'withdrawn') { Write-Host "Fix it, then run the same command again: it continues at $script:stage by itself." -ForegroundColor Yellow }
    }
    exit 1
}
function Get-LastDone { $i = $order.IndexOf($script:stage); if ($i -le 0) { return '' } else { return $order[$i - 1] } }
function Run([string]$what, [scriptblock]$cmd) { $out = & $cmd 2>&1; if ($LASTEXITCODE -ne 0) { throw "$what failed: $($out | Out-String)" }; $out }
function Confirm-Typed([string]$word, [string]$why) {
    Write-Host $why
    if ($Yes) { Write-Host "-Yes: continuing without typing '$word'." -ForegroundColor Yellow; return }
    $a = Read-Host "Type '$word' to continue, anything else stops"
    if ($a -ne $word) { Write-Host 'Stopped by you. Nothing further was done.'; exit 2 }
}
function Find-Run([string]$workflow, [string]$branchOrTag, [string]$sha, [int]$timeoutSec) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        $runs = gh run list --repo $Repo --workflow $workflow --branch $branchOrTag --limit 10 --json databaseId,headSha,createdAt | ConvertFrom-Json
        $hit = $runs | Where-Object { -not $sha -or $_.headSha -eq $sha } | Sort-Object createdAt -Descending | Select-Object -First 1
        if ($hit) { return $hit.databaseId }
        Start-Sleep -Seconds 5
    }
    return $null
}
# CI on a commit: waits while a run is queued or going (a release right after a merge used to stop here), fails on a red run.
function Wait-CiGreen([string]$sha, [int]$minutes) {
    $deadline = (Get-Date).AddMinutes($minutes); $said = $false; $seenAt = Get-Date
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
        Start-Sleep -Seconds 30
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
    $lock = try { Enter-ReleaseLock $data } catch { Write-Host $_.Exception.Message -ForegroundColor Red; exit 1 }
    foreach ($t in 'git', 'gh', 'go') { if (-not (Get-Command $t -ErrorAction SilentlyContinue)) { Fail "$t is not installed or not on PATH." } }
    & gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Fail 'gh is not signed in (run: gh auth login).' }
    try { Run 'git fetch' { git fetch origin $Branch --tags --quiet } | Out-Null } catch { Fail $_.Exception.Message }
    $tags = @(git tag -l 'v*')

    # ---- which version, and where to start -------------------------------------------------------------------------------------
    $state = Get-ReleaseState $data
    $resuming = $false
    # A done or withdrawn release is finished; -CheckOnly never resumes. The same -Version as the stopped run also resumes it.
    if (-not $CheckOnly -and $state -and $state.version -and @('done', 'withdrawn') -notcontains $state.status -and (-not $Version -or $Version -eq $state.version)) {
        $sv = [string]$state.version
        $svChannel = if ($sv.Contains('-')) { 'rc' } else { 'stable' }
        $newest = Get-NewestVersion $tags
        if ((-not $askedChannel -or $askedChannel -eq $svChannel) -and (-not $newest -or (Compare-SemVer $sv $newest) -ge 0)) {
            $Version = $sv; $resuming = $true
            foreach ($k in 'key', 'passref', 'prev_feed') { if ($state.$k) { $script:extra[$k] = [string]$state.$k } }
            Write-Host "Unfinished release $Version found (last finished stage: '$($state.stage)', $($state.status): $($state.reason)). Continuing it." -ForegroundColor Yellow
        }
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
    if ($From -eq 'Tag') { $From = if (git ls-remote --tags origin "refs/tags/$tag") { 'Wait' } else { 'Preflight' } }
    if ($resuming -and $state.status -eq 'withdraw-pending') {
        # Verify did not pass last time and the withdraw failed: only the withdraw is left to do. Never verify again.
        $From = 'Verify'; $script:withdrawOnly = $true
        Write-Host "The last run's verify did not pass and its withdraw failed. Retrying the withdraw only." -ForegroundColor Yellow
    }
    if (-not $From) { Write-Host "Release $tag already finished." -ForegroundColor Green; exit 0 }
    $script:ownsVersion = $true
    if ($CheckOnly -and $From -ne 'Preflight') { Fail '-CheckOnly only works from Preflight.' }
    function Reach([string]$s) { $order.IndexOf($s) -ge $order.IndexOf($From) }
    $script:stage = $From
    if (-not $CheckOnly) { Write-AuditEntry $audit @{ event = 'run-start'; version = $Version; from = $From; yes = [bool]$Yes; machine = [Environment]::MachineName; user = [Environment]::UserName; head = (git rev-parse HEAD).Trim() } }

    # ---- keys (every run: the Sign stage and a withdraw need them) --------------------------------------------------------------
    $tagExists = [bool](git ls-remote --tags origin "refs/tags/$tag")
    $refCommit = if ($tagExists -and (git tag -l $tag)) { (git rev-list -n 1 $tag).Trim() } else { (git rev-parse HEAD).Trim() }
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
    # The keys installed copies accept today: those of the release on the feed (for a resumed run past Feed, the release that was on
    # the feed before this one, kept in state.json).
    $oldTag = if ($feed -and $feed.Tag -eq $tag -and $script:extra.prev_feed) { $script:extra.prev_feed } elseif ($feed) { $feed.Tag } else { $null }
    if ($feed -and $feed.Tag -eq $tag -and -not $script:extra.prev_feed) { Write-Host "  note: the feed already serves $tag and the release before it is not recorded; key checks use $tag's own list." -ForegroundColor Yellow }
    $trustedOld = if ($oldTag) { ConvertFrom-TrustedKeys ((git show "${oldTag}:$script:ReleaseTrustedKeysPath") -join "`n") } else { $null }
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
        if ((git rev-parse --abbrev-ref HEAD) -ne $Branch) { Fail "Not on '$Branch'. Releases are made from $Branch." }
        if (git status --porcelain) { Fail 'The working tree has changes. A release is built from a clean commit.' }
        $head = (git rev-parse HEAD).Trim(); $remote = (git rev-parse "origin/$Branch").Trim()
        if ($head -ne $remote) { Fail "Local $Branch ($($head.Substring(0,7))) differs from origin/$Branch ($($remote.Substring(0,7))). Run git pull --ff-only first." }
        $script:checkedHead = $head
        if ($tagExists) { Fail "Tag $tag is already on GitHub. Run again without -From to continue that release, or pass a newer -Version." }
        if ((git tag -l $tag) -and (git rev-list -n 1 $tag).Trim() -ne $head) { Fail "A local tag $tag points at another commit. Delete it (git tag -d $tag) if it was never pushed." }
        $existing = (gh release list --repo $Repo --limit 100 --json tagName | ConvertFrom-Json).tagName
        if ($existing -contains $tag) { Fail "A release $tag already exists." }
        try { Assert-NextVersion $Version @($tags | Where-Object { $_ -ne $tag }) } catch { Fail $_.Exception.Message }
        Wait-CiGreen $head 45
        Write-Host "CI green on $($head.Substring(0,7))."
        $risks = Get-PassphraseLoggingRisks
        if ($risks.Count) { Fail "This PC could log the passphrase: $($risks -join '; '). Turn that policy off for release runs." }
        if ($feedBase) {
            $prs = Get-CommitPullRequests $repoRoot $Repo $feedBase.Tag $head
            if ($prs.Missing.Count) { Fail "These commits on $Branch since $($feedBase.Tag) did not come through a merged pull request: $(($prs.Missing | ForEach-Object { $_.Substring(0,7) }) -join ', '). Find out who pushed them before releasing." }
            $script:report.sensitive = Get-SensitivePullRequests $repoRoot $feedBase.Tag $head $prs.Prs
            $kc = Compare-KeyLists ((git show "$($feedBase.Tag):$script:ReleaseTrustedKeysPath") -join "`n") $trustedNewText $owner
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
                        git show "$($feed.Tag):$script:ReleaseTrustedKeysPath" | Set-Content -LiteralPath (Join-Path $fd 'keys.txt')
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
        Write-Host "Preflight OK: $Branch at $($head.Substring(0,7)), CI green, $tag is free, key $($chosen.Name)." -ForegroundColor Green
        if ($CheckOnly) { Write-Host "`nCHECK ONLY. Would release $tag with key $($chosen.Name). Nothing was changed." -ForegroundColor Yellow; exit 0 }
        Write-AuditEntry $audit @{ event = 'preflight-ok'; version = $Version; commit = $head; key = $chosen.Name; key_pub = $chosen.Pub; feed = "$($feed.Tag)"; sensitive_prs = @($script:report.sensitive | Where-Object { $_ }) }
        Done 'Preflight'
    }
    # 2 Tag -------------------------------------------------------------------------------------------------------------------
    if (Reach 'Tag') {
        Stage 'Tag' 'Tag and push'
        # Tag exactly the commit preflight checked, and only while master still is that commit (another session may pull meanwhile).
        if (-not $script:checkedHead) { Fail 'No preflight ran in this run; refusing to tag. Run again without -From.' }
        $head = $script:checkedHead
        git fetch origin $Branch --quiet 2>&1 | Out-Null
        if ((git rev-parse HEAD).Trim() -ne $head -or (git rev-parse "origin/$Branch").Trim() -ne $head) { Fail "HEAD or origin/$Branch moved away from $($head.Substring(0,7)) after preflight. Run again: preflight checks the new commit." }
        $remoteTag = (git ls-remote --tags origin "refs/tags/$tag")
        $localTag = (git tag -l $tag)
        if ($localTag -and (git rev-list -n 1 $tag).Trim() -ne $head -and -not $remoteTag) { Fail "Local tag $tag points at a different commit than HEAD ($($head.Substring(0,7)))." }
        if (-not $remoteTag) {
            Confirm-Typed $tag "This creates tag $tag on $($head.Substring(0,7)) ($(git log -1 --format=%s)) and pushes it. CI then builds a DRAFT release."
            try {
                if (-not $localTag) { Run 'git tag' { git -c tag.gpgSign=false tag $tag $head } | Out-Null }
                Run 'git push tag' { git push origin $tag } | Out-Null
            } catch {
                # Not on GitHub: drop the local tag so the next run starts clean from preflight.
                if (-not (git ls-remote --tags origin "refs/tags/$tag")) { git tag -d $tag 2>&1 | Out-Null }
                Fail $_.Exception.Message
            }
            Write-Host "Pushed $tag."
            Write-AuditEntry $audit @{ event = 'tagged'; version = $Version; commit = $head }
        } else { Write-Host "Tag $tag is already on GitHub; continuing." }
        Done 'Tag'
    }
    # 3 Wait ------------------------------------------------------------------------------------------------------------------
    if (Reach 'Wait') {
        Stage 'Wait' 'Waiting for the CI build, attestation, smoke test and draft'
        $sha = (git rev-list -n 1 $tag).Trim()
        $id = if ($RunId) { $RunId } else { Find-Run 'release.yml' $tag $sha 180 }
        if (-not $id) { Fail 'The release.yml run did not start within 3 minutes. Check the Actions tab.' }
        gh run watch $id --repo $Repo --exit-status --interval 20 | Out-Host
        if ($LASTEXITCODE -ne 0) { Fail "release.yml run $id failed: https://github.com/$Repo/actions/runs/$id" }
        $drafts = gh release list --repo $Repo --limit 30 --json tagName,isDraft | ConvertFrom-Json | Where-Object { $_.tagName -eq $tag }
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
        $rel = @((gh api "repos/$Repo/releases?per_page=50" 2>$null) -join "`n" | ConvertFrom-Json | Where-Object { $_.tag_name -eq $tag }) | Select-Object -First 1
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
        if ($SkipVerify) { Write-Host "`nVerify skipped by -SkipVerify. Run the verify-release workflow yourself." -ForegroundColor Yellow; Set-ReleaseState $data $Version 'Feed' 'stopped' 'verify skipped' $script:extra; exit 0 }
        Stage 'Verify' 'Verify on a clean machine (verify-release.yml)'
        $id = $null; $why = $null
        if ($script:withdrawOnly) { $why = "an earlier verify did not pass: $($state.reason)" }
        if (-not $why -and -not $Previous -and -not $FirstRelease) {
            $list = gh release list --repo $Repo --limit 50 --json tagName,isDraft,publishedAt
            if ($LASTEXITCODE -ne 0) { $why = 'the list of earlier releases could not be read, so the upgrade test cannot run' }
            $rels = if ($why) { @() } else { @(($list -join "`n") | ConvertFrom-Json) }
            foreach ($r in ($rels | Where-Object { -not $_.isDraft -and $_.tagName -match '^v[0-9]' -and $_.tagName -ne $tag -and ($_.tagName.Contains('-') -eq ($channel -eq 'rc')) } | Sort-Object publishedAt -Descending)) {
                $assets = (gh api "repos/$Repo/releases/tags/$($r.tagName)" --jq '.assets[].name' 2>$null)
                if ($assets -contains 'update-manifest.json.sig') { $Previous = $r.tagName; break }
            }
            # A missing previous release would quietly drop the upgrade test; treat it as a verify that cannot run.
            if (-not $why -and -not $Previous) { $why = 'no earlier signed release was found to upgrade from, so the upgrade test cannot run' }
        }
        # A one-off id in the run's name, so this run is found and no other (another session's verify, or a clock that is off).
        $nonce = [guid]::NewGuid().ToString('N').Substring(0, 12)
        $wf = @('workflow', 'run', 'verify-release.yml', '--repo', $Repo, '--ref', $Branch, '-f', "tag=$tag", '-f', "nonce=$nonce")
        if ($Previous) { $wf += @('-f', "previous=$Previous"); Write-Host "Upgrading from $Previous." } else { Write-Host 'No earlier signed release: part A only (feed, offline verify, tamper refusals).' }
        if (-not $why) { try { Run 'start verify' { gh @wf } | Out-Null } catch { $why = "the verify run could not be started: $($_.Exception.Message)" } }
        if (-not $why) {
            $deadline = (Get-Date).AddMinutes(3)
            while (-not $id -and (Get-Date) -lt $deadline) {
                Start-Sleep -Seconds 10
                $id = (gh run list --repo $Repo --workflow verify-release.yml --limit 20 --json databaseId,displayTitle | ConvertFrom-Json | Where-Object { "$($_.displayTitle)" -match [regex]::Escape($nonce) } | Select-Object -First 1).databaseId
            }
            if (-not $id) { $why = "the verify run (id $nonce) did not show up within 3 minutes" }
        }
        if ($id -and -not $why) {
            gh run watch $id --repo $Repo --exit-status --interval 30 | Out-Host
            if ($LASTEXITCODE -ne 0) { $why = "it failed its clean-machine check: https://github.com/$Repo/actions/runs/$id" }
        }
        if ($why) {
            # Not proven good while every copy is already offered it: withdraw it now (signed control file that blocks this version,
            # keeping any pause or blocks already in force). An unverified release is treated like a failed one.
            Write-Host "`nVERIFY DID NOT PASS: $why" -ForegroundColor Red
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
            if ((git status --porcelain)) { Fail "Verify did not pass ($why). The withdraw needs a clean checkout here. Clean it and run again (it only withdraws), or run: $manual" 'withdraw-pending' }
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
