# Shared helpers for the release scripts (docs/auto-updater/RELEASE-MANAGER.md). Dot-source it:  . ./tools/release/ReleaseLib.ps1
#
# Rules this file keeps:
#  - Nothing here reads, prints or stores a passphrase except Get-SigningPassphrase, which only returns a SecureString.
#  - Every check fails closed: when in doubt it throws, with a message that says what is wrong and what to do.
#  - Under -Yes (the owner's standing approval) nothing here ever waits for typed input. A check that would ask a person
#    either decides by a fixed rule or stops the run.
#  - The pure functions (no git, gh or file system writes) are tested by Test-ReleaseManager.ps1 in CI.

$script:ReleaseTrustedKeysPath = 'internal/update/trusted_keys.txt'
# Go settings outside git (go env -w, GOFLAGS overlays, a go.work) could swap the code the signing tool is built from. The release
# scripts build with none of them: module mode from the checkout only. GOTOOLCHAIN is reset to Go's default ('auto': the go.mod
# version, fetched and checked against the Go checksum database when the installed Go is older), so a stray setting cannot pick it.
$env:GOFLAGS = ''; $env:GOWORK = 'off'; $env:GOENV = 'off'; $env:GOTOOLCHAIN = 'auto'
# The code that sees the passphrase or decides what installed copies accept.
# All of tools/release: the PowerShell scripts read and pipe the passphrase too.
# Case-insensitive: on Windows Internal/Update/x.go lands in the same folder and is compiled in.
$script:ReleaseSigningPaths = @(':(icase)tools/release', ':(icase)internal/update', ':(icase,exclude)internal/update/trusted_keys.txt')
# Paths whose PRs are named in the release report (they change what is built, signed or trusted).
$script:ReleaseSensitivePaths = @(':(icase)tools/release', ':(icase)internal/update', ':(icase).github/workflows', ':(icase)go.mod', ':(icase)go.sum', ':(icase)installer')
# Proton Pass entries for each key (docs/auto-updater/RELEASING.md, vault "Work", item "Unravis Passphrases").
$script:ReleasePassRefs = @{
    'rc-1'     = 'pass://Work/Unravis Passphrases/rc.1 passphrase'
    'rc-2'     = 'pass://Work/Unravis Passphrases/rc.2 passphrase'
    'stable-1' = 'pass://Work/Unravis Passphrases/stable-1 passphrase'
    'stable-2' = 'pass://Work/Unravis Passphrases/stable-2 passphrase'
}

# ---- keys -------------------------------------------------------------------------------------------------------------------

# Lines "rc <64 hex>" / "stable <64 hex>" from a trusted_keys.txt text. Comments and blank lines are skipped.
function ConvertFrom-TrustedKeys([string]$Text) {
    $out = @()
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -match '^\s*(stable|rc)\s+([0-9a-fA-F]{64})\s*$') { $out += [pscustomobject]@{ Role = $Matches[1]; Pub = $Matches[2].ToLowerInvariant() } }
    }
    return , $out
}

# What one *.key.json says about itself without the passphrase: role and public key are stored in clear (and bound into the
# sealed part, so an edit breaks the file). Problem is set when the file cannot be used.
function Get-KeyFileInfo([string]$Path) {
    $name = [System.IO.Path]::GetFileName($Path) -replace '\.key\.json$', ''
    $info = [pscustomobject]@{ Name = $name; Path = $Path; Role = $null; Pub = $null; Problem = $null }
    try { $j = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json } catch { $info.Problem = 'not readable JSON (damaged or not a key file)'; return $info }
    if ($j.v -ne 1) { $info.Problem = 'not an Unravis key file (version is not 1)'; return $info }
    if ($j.role -ne 'rc' -and $j.role -ne 'stable') { $info.Problem = "has no valid role ('$($j.role)')"; return $info }
    try { $raw = [Convert]::FromBase64String([string]$j.pub) } catch { $info.Problem = 'public key field is damaged'; return $info }
    if ($raw.Length -ne 32) { $info.Problem = 'public key field is damaged'; return $info }
    if (-not $j.sealed -or -not $j.salt -or -not $j.nonce) { $info.Problem = 'encrypted part is missing'; return $info }
    $info.Role = [string]$j.role
    $info.Pub = ([System.BitConverter]::ToString($raw) -replace '-', '').ToLowerInvariant()
    return $info
}

# Every key file in the owner's key folder, plus a check that a .pub beside it (if any) says the same.
function Get-OwnerKeys([string]$Dir) {
    $out = @()
    if (-not $Dir -or -not (Test-Path -LiteralPath $Dir -PathType Container)) { return , $out }
    foreach ($f in (Get-ChildItem -LiteralPath $Dir -Filter '*.key.json' -File | Sort-Object Name)) {
        $k = Get-KeyFileInfo $f.FullName
        $pubFile = Join-Path $Dir "$($k.Name).pub"
        if (-not $k.Problem -and (Test-Path -LiteralPath $pubFile)) {
            $p = ("$(Get-Content -Raw -LiteralPath $pubFile)").Trim() -split '\s+'
            if ($p.Count -ne 2 -or $p[0] -ne $k.Role -or $p[1].ToLowerInvariant() -ne $k.Pub) { $k.Problem = "does not match $($k.Name).pub beside it (one of the two was changed or swapped)" }
        }
        $out += $k
    }
    return , $out
}

# Added and removed keys between two trusted_keys.txt texts. Foreign = added keys the owner holds no key file for.
function Compare-KeyLists([string]$OldText, [string]$NewText, $OwnerKeys) {
    $old = ConvertFrom-TrustedKeys $OldText
    $new = ConvertFrom-TrustedKeys $NewText
    $id = { param($k) "$($k.Role) $($k.Pub)" }
    $oldIds = @($old | ForEach-Object { & $id $_ })
    $newIds = @($new | ForEach-Object { & $id $_ })
    $ownIds = @($OwnerKeys | Where-Object { -not $_.Problem } | ForEach-Object { & $id $_ })
    $added = @($new | Where-Object { $oldIds -notcontains (& $id $_) })
    $removed = @($old | Where-Object { $newIds -notcontains (& $id $_) })
    $foreign = @($added | Where-Object { $ownIds -notcontains (& $id $_) })
    return [pscustomobject]@{ Added = $added; Removed = $removed; Foreign = $foreign }
}

# Picks the key to sign with and says exactly what is wrong with every key it could not use.
#  TrustedNew: keys built into the commit being released. TrustedOld: keys of the release on the feed (what installed copies
#  accept today), or $null for a channel's first release. Preferred: a key name or path the caller asked for.
function Select-SigningKey([string]$Channel, $OwnerKeys, $TrustedNew, $TrustedOld, [string]$Preferred) {
    $problems = @()
    $usable = @()
    $inList = { param($k, $list) @($list | Where-Object { $_.Role -eq $k.Role -and $_.Pub -eq $k.Pub }).Count -gt 0 }
    foreach ($k in $OwnerKeys) {
        $why = $null
        if ($k.Problem) { $why = $k.Problem }
        elseif ($k.Role -ne $Channel) { $why = $null; continue }   # a key of the other channel is not a problem, just not for this run
        elseif (-not (& $inList $k $TrustedNew)) { $why = "is not in $script:ReleaseTrustedKeysPath of the commit being released (that build would refuse its own update)" }
        elseif ($null -ne $TrustedOld -and -not (& $inList $k $TrustedOld)) { $why = 'is not trusted by the release now on the feed (installed copies would refuse the update)' }
        if ($why) { $problems += "$($k.Name): $why" } else { $usable += $k }
    }
    $pick = $null
    if ($Preferred) {
        $want = [System.IO.Path]::GetFileName($Preferred) -replace '\.key\.json$', ''
        $pick = $usable | Where-Object { $_.Name -eq $want } | Select-Object -First 1
        if (-not $pick) {
            $known = $OwnerKeys | Where-Object { $_.Name -eq $want } | Select-Object -First 1
            if (-not $known) { $problems = @("${want}: no such key file in the key folder") + $problems }
            elseif ($known.Role -ne $Channel -and -not $known.Problem) { $problems = @("${want}: is a $($known.Role) key, this is a $Channel release") + $problems }
        }
    } else {
        $pick = $usable | Select-Object -First 1
    }
    if (-not @($OwnerKeys | Where-Object { $_.Role -eq $Channel }).Count -and -not $problems.Count) { $problems += "no $Channel key file (*.key.json) in the key folder" }
    return [pscustomobject]@{ Key = $pick; Usable = $usable; Problems = $problems }
}

function Get-ProtonPassRefForKey([string]$Name) { return $script:ReleasePassRefs[$Name] }

# ---- versions ---------------------------------------------------------------------------------------------------------------

function ConvertTo-SemVer([string]$V) {
    $v2 = $V -replace '^v', ''
    if ($v2 -notmatch '^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9a-z.]+))?$') { return $null }
    return [pscustomobject]@{ Text = $v2; Major = [int64]$Matches[1]; Minor = [int64]$Matches[2]; Patch = [int64]$Matches[3]; Pre = $Matches[4] }
}

# -1, 0 or 1. A release sorts after its own prereleases; prerelease parts compare as numbers when both are numbers.
function Compare-SemVer([string]$A, [string]$B) {
    $x = ConvertTo-SemVer $A; $y = ConvertTo-SemVer $B
    if (-not $x -or -not $y) { throw "Not a version: '$A' or '$B'." }
    foreach ($p in 'Major', 'Minor', 'Patch') { if ($x.$p -ne $y.$p) { return [Math]::Sign($x.$p - $y.$p) } }
    if (-not $x.Pre -and -not $y.Pre) { return 0 }
    if (-not $x.Pre) { return 1 }
    if (-not $y.Pre) { return -1 }
    $xs = $x.Pre -split '\.'; $ys = $y.Pre -split '\.'
    for ($i = 0; $i -lt [Math]::Max($xs.Count, $ys.Count); $i++) {
        if ($i -ge $xs.Count) { return -1 }
        if ($i -ge $ys.Count) { return 1 }
        $a1 = $xs[$i]; $b1 = $ys[$i]
        $an = $a1 -match '^[0-9]+$'; $bn = $b1 -match '^[0-9]+$'
        if ($an -and $bn) { $c = [Math]::Sign([int64]$a1 - [int64]$b1) }
        elseif ($an) { $c = -1 } elseif ($bn) { $c = 1 }
        else { $c = [Math]::Sign([string]::CompareOrdinal($a1, $b1)) }
        if ($c -ne 0) { return $c }
    }
    return 0
}

# The newest version among tags like v1.2.3 / v1.2.3-rc.4 (other tags, e.g. update-feed, are ignored).
function Get-NewestVersion([string[]]$Tags) {
    $best = $null
    foreach ($t in $Tags) { if ((ConvertTo-SemVer $t) -and $t -match '^v') { if (-not $best -or (Compare-SemVer $t $best) -gt 0) { $best = $t -replace '^v', '' } } }
    return $best
}

# The next version for a channel. rc: after X.Y.Z-rc.N comes X.Y.Z-rc.N+1, after X.Y.Z comes X.Y.(Z+1)-rc.1.
# stable: the X.Y.Z of the newest rc; refused if the newest version is already a stable one (say -Version then).
function Get-NextVersion([string[]]$Tags, [string]$Channel) {
    $n = Get-NewestVersion $Tags
    if (-not $n) { throw 'No earlier version tag found. Pass -Version for the first release.' }
    $s = ConvertTo-SemVer $n
    if ($Channel -eq 'rc') {
        if ($s.Pre -match '^rc\.([0-9]+)$') { return "$($s.Major).$($s.Minor).$($s.Patch)-rc.$([int64]$Matches[1] + 1)" }
        if (-not $s.Pre) { return "$($s.Major).$($s.Minor).$($s.Patch + 1)-rc.1" }
        throw "The newest tag v$n is not an rc. Pass -Version."
    }
    if (-not $s.Pre) { throw "The newest version v$n is already stable. Pass -Version for the next stable release." }
    return "$($s.Major).$($s.Minor).$($s.Patch)"
}

# A new version must sort after every version already tagged, and must fit its channel.
function Assert-NextVersion([string]$Version, [string[]]$Tags) {
    if (-not (ConvertTo-SemVer $Version)) { throw "Version '$Version' is not X.Y.Z or X.Y.Z-rc.N (lower case, no leading v)." }
    $s = ConvertTo-SemVer $Version
    if ($s.Pre -and $s.Pre -notmatch '^rc\.[0-9]+$') { throw "Prerelease '$($s.Pre)' is not rc.N. Only rc and stable channels exist." }
    $n = Get-NewestVersion $Tags
    if ($n -and (Compare-SemVer $Version $n) -le 0) { throw "Version $Version is not newer than the newest tag v$n. Installed copies never go back, so it would never be offered." }
}

# ---- signing-code checks ----------------------------------------------------------------------------------------------------

# Packages sigtool is built from must be the Go standard library or the two packages of this repository it is meant to use.
# Input: the NON-standard packages, as printed by Get-SigtoolNonStdDeps (go itself decides what is standard, so a module named
# without a dot cannot pass as standard). Output: the packages that are not allowed.
function Get-ForbiddenSigtoolDeps([string[]]$Lines) {
    # golang.org/x/sys/windows: internal/update uses it on Windows (version pinned in go.mod and go.sum, both sensitive paths).
    $ok = @('api-audit-explorer/internal/update', 'api-audit-explorer/tools/release/sigtool', 'golang.org/x/sys/windows')
    return , @($Lines | Where-Object { $_ } | ForEach-Object { $_.Trim() } | Where-Object { $_ -and -not ($ok -contains $_) })
}
function Get-SigtoolNonStdDeps([string]$Dir) {
    Push-Location $Dir
    try { $d = @(go list -mod=readonly -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./tools/release/sigtool) } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0 -or -not $d.Count) { throw 'go list could not read the signing tool''s dependencies. Not signing.' }
    return , $d
}

# SHA-256 of a text, lower-case hex.
function Get-TextSha256([string]$Text) {
    return ([System.BitConverter]::ToString([System.Security.Cryptography.SHA256]::HashData([System.Text.Encoding]::UTF8.GetBytes($Text))) -replace '-', '').ToLowerInvariant()
}
function Get-FileSha256([string]$Path) { return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant() }

# First-parent commits in Base..Head that are not the merge of a pull request on GitHub (a direct push to master).
# Returns @{ Missing = sha[]; Prs = @{ sha = number } }. Uses gh (REST); any gh error counts as "not shown to be a PR".
function Get-CommitPullRequests([string]$RepoRoot, [string]$Repo, [string]$Base, [string]$Head) {
    $shas = @(git -C $RepoRoot rev-list --first-parent "$Base..$Head")
    if ($LASTEXITCODE -ne 0) { throw "git rev-list $Base..$Head failed." }
    $missing = @(); $prs = @{}
    # One call per commit, run side by side (a release since the last one is often 10 to 40 commits). Nothing is cached on disk:
    # a cache file an agent could edit would let a direct push pass as "merged".
    $found = @($shas | ForEach-Object -ThrottleLimit 8 -Parallel {
        $c = $_
        # The commit must BE the PR's merge (squash) commit: a PR that GitHub marks merged only because its head was pushed straight
        # to master names another merge commit. (Checked live 21 Nov 2026: every first-parent commit since rc.53 matches.) Branch
        # rules on master (BL-197) are the real stop for direct pushes.
        $json = gh api "repos/$($using:Repo)/commits/$c/pulls" 2>$null
        $n = $null
        if ($LASTEXITCODE -eq 0 -and $json) {
            $n = @(($json -join "`n") | ConvertFrom-Json | Where-Object { $_.merged_at -and $_.base.ref -eq 'master' -and $_.merge_commit_sha -eq $c } | Select-Object -First 1).number
        }
        [pscustomobject]@{ Sha = $c; N = $n }
    })
    foreach ($c in $shas) {
        $hit = $found | Where-Object { $_.Sha -eq $c } | Select-Object -First 1
        if (-not $hit -or -not $hit.N) { $missing += $c } else { $prs[$c] = [int]$hit.N }
    }
    return [pscustomobject]@{ Missing = $missing; Prs = $prs }
}

# PR numbers (from the map above) whose commit changed a sensitive path.
function Get-SensitivePullRequests([string]$RepoRoot, [string]$Base, [string]$Head, $Prs) {
    $hits = @(git -C $RepoRoot rev-list --first-parent "$Base..$Head" -- $script:ReleaseSensitivePaths)
    return , @($hits | ForEach-Object { if ($Prs.ContainsKey($_)) { "#$($Prs[$_])" } else { $_.Substring(0, 7) } } | Select-Object -Unique)
}

# The two signing gates, shared by Sign-Release.ps1, Set-Control.ps1 and Refresh-Feed.ps1.
#  Baseline: the tag of the release on the feed (what installed copies run), or $null for a channel's first release.
#  Commit:   the commit whose signing code and key list are about to be used.
# Without -Yes a person types 'reviewed' / 'keys ok', as before. With -Yes nobody is asked; the rules decide:
#  signing code changed -> the commit must be on GitHub's master, every first-parent commit since Baseline must be a merged PR,
#                          and sigtool may depend only on the standard library and internal/update. The diff hash is logged.
#  key list changed     -> every ADDED key must have its private key file in KeyDir (made by Make-Keys). Removed keys are fine.
#  first release        -> every listed key must have its key file in KeyDir.
function Invoke-SigningGates {
    param([string]$RepoRoot, [string]$Repo, [string]$Baseline, [string]$Commit, [string]$KeyDir, [switch]$Yes, [string]$Context, [string]$AuditPath, [string]$SourceDir, [string]$Tool)
    if (-not $SourceDir) { $SourceDir = $RepoRoot }
    $r = [ordered]@{ CodeChanged = $false; DiffSha256 = $null; KeysAdded = @(); KeysRemoved = @() }
    $owner = Get-OwnerKeys $KeyDir
    $ask = { param($word, $q) if ((Read-Host "$q Type '$word' to go on") -ne $word) { throw 'Stopped. Nothing was signed or published.' } }
    $newKeys = (git -C $RepoRoot show "${Commit}:$script:ReleaseTrustedKeysPath") -join "`n"
    if ($LASTEXITCODE -ne 0) { throw "Could not read $script:ReleaseTrustedKeysPath at $Commit." }
    if ($Yes) {
        # With nobody reading along, only code that is on GitHub's master through merged PRs is ever signed, whatever it changes.
        $cmp = gh api "repos/$Repo/compare/master...$Commit" --jq .status 2>$null
        if ($LASTEXITCODE -ne 0 -or ($cmp -ne 'behind' -and $cmp -ne 'identical')) { throw "$Context ($($Commit.Substring(0, 7))) is not part of GitHub's master (compare says '$cmp'). Not signing. Merge it through a pull request first." }
        if ($Baseline) {
            # The commit must come after the release on the feed; an older one would make the PR range empty and pass.
            git -C $RepoRoot merge-base --is-ancestor $Baseline $Commit
            if ($LASTEXITCODE -ne 0) { throw "$Context ($($Commit.Substring(0, 7))) does not contain $Baseline, the release on the feed. Not signing an older or side commit." }
            $pr = Get-CommitPullRequests $RepoRoot $Repo $Baseline $Commit
            if ($pr.Missing.Count) { throw "These commits on master since $Baseline did not come through a merged pull request: $(($pr.Missing | ForEach-Object { $_.Substring(0, 7) }) -join ', '). Not signing." }
        }
        # SourceDir is a clean checkout of Commit (a throw-away worktree).
        $bad = Get-ForbiddenSigtoolDeps (Get-SigtoolNonStdDeps $SourceDir)
        if ($bad.Count) { throw "The signing tool depends on code outside the standard library and internal/update: $($bad -join ', '). Not signing." }
    }
    if (-not $Baseline) {
        Write-Host 'Trusted public keys in this release:'; $newKeys | Write-Host
        if ($Yes) {
            $c = Compare-KeyLists '' $newKeys $owner
            if ($c.Foreign.Count) { throw "The trusted key list holds keys you have no key file for in ${KeyDir}: $(($c.Foreign | ForEach-Object { "$($_.Role) $($_.Pub)" }) -join '; '). Not signing." }
            Assert-KeysOpen $c.Added $owner $Tool
            Write-Host "-Yes: every trusted key has its key file in $KeyDir and opens with its own passphrase." -ForegroundColor Yellow
            $r.KeysAdded = @($c.Added | ForEach-Object { "$($_.Role) $($_.Pub.Substring(0, 16))" })
        } else { & $ask 'keys ok' "These are your keys and nobody else's." }
        if ($AuditPath) { Write-AuditEntry $AuditPath @{ event = 'signing-gates'; context = $Context; baseline = 'first-release'; commit = $Commit; keys_added = $r.KeysAdded; mode = $(if ($Yes) { 'auto' } else { 'typed' }) } }
        return [pscustomobject]$r
    }
    $codeDiff = git -C $RepoRoot diff --stat $Baseline $Commit -- $script:ReleaseSigningPaths
    if ($LASTEXITCODE -ne 0) { throw "git diff $Baseline $Commit failed; cannot tell whether the signing code changed. Not signing." }
    if ($codeDiff) {
        $r.CodeChanged = $true
        Write-Host "Signing and verifying code changed since ${Baseline}:"; $codeDiff | Write-Host
        $diffText = (git -C $RepoRoot diff $Baseline $Commit -- $script:ReleaseSigningPaths) -join "`n"
        $r.DiffSha256 = Get-TextSha256 $diffText
        if ($Yes) { Write-Host "-Yes: the signing code is on master through merged PRs; diff sha256 $($r.DiffSha256)." -ForegroundColor Yellow }
        else { & $ask 'reviewed' 'You have read that diff (git diff) and it is yours.' }
    }
    $oldKeys = (git -C $RepoRoot show "${Baseline}:$script:ReleaseTrustedKeysPath") -join "`n"
    if ($LASTEXITCODE -ne 0) { throw "Could not read $script:ReleaseTrustedKeysPath at $Baseline." }
    $c = Compare-KeyLists $oldKeys $newKeys $owner
    $r.KeysAdded = @($c.Added | ForEach-Object { "$($_.Role) $($_.Pub.Substring(0, 16))" })
    $r.KeysRemoved = @($c.Removed | ForEach-Object { "$($_.Role) $($_.Pub.Substring(0, 16))" })
    if ($c.Added.Count -or $c.Removed.Count) {
        Write-Host "The list of trusted public keys CHANGED since ${Baseline}: $($c.Added.Count) added, $($c.Removed.Count) removed."
        if ($Yes) {
            if ($c.Foreign.Count) { throw "A trusted key was added that is not one of yours (no key file for it in $KeyDir): $(($c.Foreign | ForEach-Object { "$($_.Role) $($_.Pub)" }) -join '; '). Not signing." }
            Assert-KeysOpen $c.Added $owner $Tool
            Write-Host '-Yes: every added key has its key file and opens with its own passphrase; removed keys only narrow trust.' -ForegroundColor Yellow
        } else { & $ask 'keys ok' 'Every added or removed key is one you made or removed on purpose.' }
    }
    if ($AuditPath) { Write-AuditEntry $AuditPath @{ event = 'signing-gates'; context = $Context; baseline = $Baseline; commit = $Commit; code_changed = $r.CodeChanged; diff_sha256 = "$($r.DiffSha256)"; keys_added = $r.KeysAdded; keys_removed = $r.KeysRemoved; mode = $(if ($Yes) { 'auto' } else { 'typed' }) } }
    return [pscustomobject]$r
}

# A key file is proof of ownership only if it opens: its public key is in clear, so a look-alike JSON file would pass a file check.
# Every key in Keys must have its file in the owner's folder and open with its own Proton Pass entry (sigtool keycheck via Tool).
function Assert-KeysOpen($Keys, $OwnerKeys, [string]$Tool) {
    foreach ($a in @($Keys)) {
        if (-not $a) { continue }
        if (-not $Tool) { throw 'Internal error: no signing tool to check added keys with. Not signing.' }
        $f = $OwnerKeys | Where-Object { -not $_.Problem -and $_.Role -eq $a.Role -and $_.Pub -eq $a.Pub } | Select-Object -First 1
        $ref = if ($f) { Get-ProtonPassRefForKey $f.Name } else { $null }
        if (-not $f -or -not $ref) { throw "Trusted key $($a.Role) $($a.Pub.Substring(0, 16))... has no key file with a known Proton Pass entry. Not signing." }
        $sec = Get-SigningPassphrase -ProtonPassRef $ref -KeyFile $f.Path -Yes
        try { $null = Invoke-WithPassphrase $sec $Tool @('keycheck', '-key', $f.Path, '-role', $a.Role) 2>&1 } finally { $sec.Dispose() }
        if ($LASTEXITCODE -ne 0) { throw "Trusted key $($f.Name) does not open with its Proton Pass entry: not proven to be yours. Not signing." }
    }
}

# Builds sigtool from a throw-away worktree of Commit (never the working folder: ignored or hidden files there do not count).
# Returns @{ Dir = worktree; Tool = exe }. The caller removes the worktree with Remove-ToolWorktree.
function New-ToolWorktree([string]$RepoRoot, [string]$Commit, [string]$Work) {
    $dir = Join-Path $Work 'src'
    git -C $RepoRoot worktree add --detach $dir $Commit 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "git worktree add for $Commit failed." }
    $tool = Join-Path $Work 'sigtool.exe'
    Push-Location $dir
    try { go build -mod=readonly -o $tool ./tools/release/sigtool; if ($LASTEXITCODE -ne 0) { throw 'building the signing tool failed.' } } finally { Pop-Location }
    return [pscustomobject]@{ Dir = $dir; Tool = $tool }
}
function Remove-ToolWorktree([string]$RepoRoot, [string]$Dir) {
    if ($Dir -and (Test-Path -LiteralPath $Dir)) { git -C $RepoRoot worktree remove --force $Dir 2>&1 | Out-Null }
    git -C $RepoRoot worktree prune 2>&1 | Out-Null
}

# ---- passphrase -------------------------------------------------------------------------------------------------------------

# The key passphrase as a SecureString. From Proton Pass when a reference is given. Under -Yes it never falls back to typing
# (a hidden prompt in a window nobody watches stalls the release); it throws with the reason instead.
function Get-SigningPassphrase([string]$ProtonPassRef, [string]$KeyFile, [switch]$Yes) {
    $risks = Get-PassphraseLoggingRisks
    if ($risks.Count) { throw "This PC could log the passphrase: $($risks -join '; '). Turn that policy off for release runs. Not reading it." }
    if ($ProtonPassRef) {
        try { $s = Get-ProtonPassSecret -Reference $ProtonPassRef; Write-Host 'Passphrase read from Proton Pass (not shown).'; return $s }
        catch {
            if ($Yes) { throw "Proton Pass: $($_.Exception.Message) Not asking for the passphrase under -Yes. Fix pass-cli (pass-cli info), then continue." }
            Write-Host "Proton Pass: $($_.Exception.Message)" -ForegroundColor Yellow; Write-Host 'Falling back to typing the passphrase.' -ForegroundColor Yellow
        }
    } elseif ($Yes) { throw 'No Proton Pass reference for this key, and -Yes never asks for a passphrase. Pass -PassphraseFromProtonPass.' }
    return (Read-Host "Passphrase for $KeyFile" -AsSecureString)
}

# Runs one sigtool command with the passphrase on its standard input. The plain text lives only for that call.
# (A scriptblock would not pass its pipeline input on to a program inside it, so the program and its arguments are given.)
function Invoke-WithPassphrase([System.Security.SecureString]$Secure, [string]$Tool, [string[]]$Arguments) {
    $plain = [System.Net.NetworkCredential]::new('', $Secure).Password
    try { $plain | & $Tool @Arguments } finally { $plain = $null }
}

# PowerShell settings that would write what a script pipes to a program into a log (Group Policy). Returns the reasons.
function Get-PassphraseLoggingRisks {
    $out = @()
    if (-not $IsWindows) { return , $out }
    $checks = @(@('Transcription', 'EnableTranscripting', 'PowerShell transcription'), @('ModuleLogging', 'EnableModuleLogging', 'PowerShell module logging'))
    # pwsh 7 reads PowerShellCore policies (and the Windows PowerShell ones when told to); check both, plus its config file.
    foreach ($hive in 'HKLM:', 'HKCU:') {
        foreach ($base in "$hive\SOFTWARE\Policies\Microsoft\Windows\PowerShell", "$hive\SOFTWARE\Policies\Microsoft\PowerShellCore") {
            foreach ($p in $checks) {
                $v = (Get-ItemProperty -LiteralPath "$base\$($p[0])" -Name $p[1] -ErrorAction SilentlyContinue).($p[1])
                if ($v -eq 1) { $out += "$($p[2]) is on ($base policy)" }
            }
        }
    }
    $cfg = Join-Path $PSHOME 'powershell.config.json'
    if (Test-Path -LiteralPath $cfg) {
        try {
            $j = Get-Content -Raw -LiteralPath $cfg | ConvertFrom-Json
            foreach ($scope in @($j.PowerShellPolicies, $j.'Microsoft.PowerShell:PowerShellPolicies')) {
                if (-not $scope) { continue }
                if ($scope.Transcription.EnableTranscripting) { $out += "PowerShell transcription is on ($cfg)" }
                if ($scope.ModuleLogging.EnableModuleLogging) { $out += "PowerShell module logging is on ($cfg)" }
            }
        } catch { $out += "$cfg cannot be read, so its logging settings are unknown" }
    }
    return , $out
}

# ---- audit log, state, lock -------------------------------------------------------------------------------------------------

function Get-ReleaseDataDir {
    $base = if ($env:UNRAVIS_RELEASE_DATA) { $env:UNRAVIS_RELEASE_DATA } elseif ($IsWindows) { Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Unravis\release' } else { Join-Path $HOME '.local/share/unravis-release' }
    New-Item -ItemType Directory -Force -Path $base | Out-Null
    return $base
}

# Appends one JSON line. Each line carries prev = SHA-256 of the line before it, so an edited or removed line breaks the chain.
# Only plain values are written; a field whose name mentions a pass or secret is refused (defence against a careless caller).
function Write-AuditEntry([string]$Path, [hashtable]$Fields) {
    foreach ($k in $Fields.Keys) { if ($k -match 'pass|secret|token') { throw "Audit field '$k' is not allowed." } }
    $prev = '0' * 64
    if (Test-Path -LiteralPath $Path) {
        $last = Get-Content -LiteralPath $Path -Tail 1
        if ($last) { $prev = Get-TextSha256 $last }
    }
    $e = [ordered]@{ ts = [DateTime]::UtcNow.ToString('o'); prev = $prev }
    foreach ($k in ($Fields.Keys | Sort-Object)) { $e[$k] = $Fields[$k] }
    $line = $e | ConvertTo-Json -Compress -Depth 4
    [System.IO.File]::AppendAllText($Path, $line + "`n", [System.Text.UTF8Encoding]::new($false))
}

# Checks the chain. Returns @{ Ok; Lines; BrokenAt (1-based line, 0 when fine) }.
function Test-AuditChain([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return [pscustomobject]@{ Ok = $true; Lines = 0; BrokenAt = 0 } }
    $lines = @(Get-Content -LiteralPath $Path | Where-Object { $_ -ne '' })
    $prev = '0' * 64
    for ($i = 0; $i -lt $lines.Count; $i++) {
        try { $e = $lines[$i] | ConvertFrom-Json } catch { return [pscustomobject]@{ Ok = $false; Lines = $lines.Count; BrokenAt = $i + 1 } }
        if ($e.prev -ne $prev) { return [pscustomobject]@{ Ok = $false; Lines = $lines.Count; BrokenAt = $i + 1 } }
        $prev = Get-TextSha256 $lines[$i]
    }
    return [pscustomobject]@{ Ok = $true; Lines = $lines.Count; BrokenAt = 0 }
}

# One release at a time on this PC. The OS drops the lock if the process dies, so a crash never leaves it stuck.
# lock-info.json (beside it) says who holds it, so a second run can answer "busy: who, which step" instead of a bare refusal.
function Enter-ReleaseLock([string]$Dir) {
    $p = Join-Path $Dir 'release.lock'
    try { return [System.IO.File]::Open($p, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None) }
    catch {
        $who = ''
        try {
            $i = Get-Content -Raw -LiteralPath (Join-Path $Dir 'lock-info.json') | ConvertFrom-Json
            $who = " Held by $($i.requester) (pid $($i.pid)) for $($i.version), at '$($i.stage)', since $($i.started)."
        } catch {}
        throw "Another release is running on this PC (release.lock is held).$who Wait for it, or pass -WaitMinutes."
    }
}
function Set-LockInfo([string]$Dir, [hashtable]$Fields) {
    $tmp = Join-Path $Dir 'lock-info.json.tmp'
    [System.IO.File]::WriteAllText($tmp, ($Fields | ConvertTo-Json), [System.Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $tmp -Destination (Join-Path $Dir 'lock-info.json') -Force
}

# ---- release lock on GitHub (one release at a time for everyone; readable from any session) ---------------------------------------
# A branch "release-lock" with one file, lock.json. Writes are compare-and-swap through the contents API (the file's blob sha must be
# the one that was read), so two claims at once cannot both win. It is advisory for everything except Release.ps1: other sessions
# read it to see "busy, who, which step" and whether merges to master should wait. It carries no secret and is never trusted for
# signing: every signing decision still comes from git, the feed and the checks.
$script:ReleaseLockBranch = 'release-lock'
$script:ReleaseLockLeaseMinutes = 180

function Invoke-GhApi([string[]]$Arguments, [string]$Body) {
    # Returns @{ Ok; Status; Json; Text }. Never throws; a transient failure is retried by the caller. Body (JSON) goes in on stdin.
    $out = if ($Body) { $Body | gh api @Arguments --input - 2>&1 } else { gh api @Arguments 2>&1 }
    $code = $LASTEXITCODE
    $text = ($out | ForEach-Object { "$_" }) -join "`n"
    $status = if ($text -match 'HTTP (\d{3})') { [int]$Matches[1] } else { 0 }
    $json = $null
    if ($code -eq 0 -and $text) { try { $json = $text | ConvertFrom-Json } catch {} }
    return [pscustomobject]@{ Ok = ($code -eq 0); Status = $status; Json = $json; Text = $text }
}

# What the lock says now: @{ Sha = blob sha ('' when the branch does not exist yet); Record = object or $null }.
function Read-RemoteLock([string]$Repo) {
    for ($try = 1; $try -le 3; $try++) {
        $r = Invoke-GhApi @("repos/$Repo/contents/lock.json?ref=$script:ReleaseLockBranch")
        if ($r.Ok -and $r.Json.content) {
            $rec = $null
            try { $rec = ([System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(([string]$r.Json.content -replace '\s', ''))) | ConvertFrom-Json) } catch { throw "lock.json on the $script:ReleaseLockBranch branch is not readable. Fix or delete that branch." }
            return [pscustomobject]@{ Sha = [string]$r.Json.sha; Record = $rec }
        }
        if ($r.Status -eq 404 -or $r.Text -match 'Not Found') { return [pscustomobject]@{ Sha = ''; Record = $null } }
        Start-Sleep -Seconds (2 * $try)
    }
    throw 'Could not read the release lock from GitHub (network or token). A release needs GitHub anyway; try again.'
}

# Writes the record. ExpectSha is the blob sha that was read ('' to create the branch). Returns the new blob sha, or throws a message
# starting with LOCK-CONFLICT when somebody else wrote first.
function Write-RemoteLock([string]$Repo, $Record, [string]$ExpectSha, [string]$Message) {
    $json = ($Record | ConvertTo-Json -Depth 6)
    $b64 = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($json + "`n"))
    for ($try = 1; $try -le 3; $try++) {
        if ($ExpectSha) {
            $r = Invoke-GhApi @('-X', 'PUT', "repos/$Repo/contents/lock.json", '-f', "message=$Message", '-f', "content=$b64", '-f', "branch=$script:ReleaseLockBranch", '-f', "sha=$ExpectSha")
            if ($r.Ok) { return [string]$r.Json.content.sha }
        } else {
            # First write ever: an orphan commit holding only lock.json. Creating the ref fails if it exists, which is the compare step.
            $blob = Invoke-GhApi @('-X', 'POST', "repos/$Repo/git/blobs", '-f', "content=$b64", '-f', 'encoding=base64')
            if (-not $blob.Ok) { Start-Sleep -Seconds (2 * $try); continue }
            $tree = Invoke-GhApi @('-X', 'POST', "repos/$Repo/git/trees") (@{ tree = @(@{ path = 'lock.json'; mode = '100644'; type = 'blob'; sha = [string]$blob.Json.sha }) } | ConvertTo-Json -Depth 4 -Compress)
            if (-not $tree.Ok) { Start-Sleep -Seconds (2 * $try); continue }
            $commit = Invoke-GhApi @('-X', 'POST', "repos/$Repo/git/commits", '-f', "message=$Message", '-f', "tree=$($tree.Json.sha)")
            if (-not $commit.Ok) { Start-Sleep -Seconds (2 * $try); continue }
            $ref = Invoke-GhApi @('-X', 'POST', "repos/$Repo/git/refs", '-f', "ref=refs/heads/$script:ReleaseLockBranch", '-f', "sha=$($commit.Json.sha)")
            if ($ref.Ok) { return [string]$blob.Json.sha }
            $r = $ref
        }
        if ($r.Status -eq 409 -or $r.Status -eq 422 -or $r.Text -match 'does not match|already exists') { throw "LOCK-CONFLICT: somebody else changed the release lock first ($($r.Status))." }
        Start-Sleep -Seconds (2 * $try)
    }
    throw 'Could not write the release lock to GitHub (network or token).'
}

# Pure. The lock record. No secret can be put in it.
function New-LockRecord([hashtable]$Fields) {
    $r = [ordered]@{ v = 1; state = 'held'; id = ''; machine = ''; user = ''; pid = 0; requester = ''; started = ''; heartbeat = ''
                     channel = ''; version = ''; stage = ''; commit = ''; holdMerges = $true; lease_minutes = $script:ReleaseLockLeaseMinutes; reason = ''; last = $null }
    foreach ($k in $Fields.Keys) {
        if ($k -match 'pass|secret|token|key') { throw "Lock field '$k' is not allowed." }
        $r[$k] = $Fields[$k]
    }
    return $r
}

# Pure. Is this record a live claim on the release? LocalFree = this process holds release.lock on this PC, so no other process
# here can be running a release: a "held" record from this PC is then a crashed run's.
function Test-ReleaseLockBusy($Rec, [string]$Machine, [datetime]$Now, [bool]$LocalFree) {
    if (-not $Rec -or $Rec.state -ne 'held') { return [pscustomobject]@{ Busy = $false; Stale = $false; Reason = 'free' } }
    if ($Rec.machine -eq $Machine) {
        if ($LocalFree) { return [pscustomobject]@{ Busy = $false; Stale = $true; Reason = "stale: $($Rec.version) was started on this PC and its process is gone (crashed run); it is continued, not dropped" } }
        return [pscustomobject]@{ Busy = $true; Stale = $false; Reason = 'running on this PC' }
    }
    $lease = if ($Rec.lease_minutes) { [double]$Rec.lease_minutes } else { $script:ReleaseLockLeaseMinutes }
    $age = ($Now.ToUniversalTime() - ([datetime]$Rec.heartbeat).ToUniversalTime()).TotalMinutes
    if ($age -gt $lease) { return [pscustomobject]@{ Busy = $false; Stale = $true; Reason = ("stale: no sign of life from $($Rec.machine) for {0:N0} minutes (lease $lease)" -f $age) } }
    return [pscustomobject]@{ Busy = $true; Stale = $false; Reason = "running on $($Rec.machine)" }
}

# Pure. One line a person or an agent can act on.
function Format-LockBusy($Rec, [datetime]$Now) {
    $age = ''
    try { $age = ', last sign of life {0:N0} min ago' -f ($Now.ToUniversalTime() - ([datetime]$Rec.heartbeat).ToUniversalTime()).TotalMinutes } catch {}
    $v = if ($Rec.version) { $Rec.version } else { '(choosing the version)' }
    return "BUSY: a release is running: $v ($($Rec.channel)), step '$($Rec.stage)', started $($Rec.started) by $($Rec.requester) on $($Rec.machine)$age. Hold merges to master: $($Rec.holdMerges). Pass -WaitMinutes N to queue behind it, or run -Status."
}

# Claims or changes the record while holding it. Throws 'lock lost' when the record now belongs to another run.
function Update-RemoteLock([string]$Repo, [string]$Id, [hashtable]$Fields, [string]$Message = 'release lock') {
    for ($try = 1; $try -le 2; $try++) {
        $cur = Read-RemoteLock $Repo
        if (-not $cur.Record -or $cur.Record.id -ne $Id) { throw 'lock lost: the release lock now belongs to another run (or was removed). Stopping.' }
        $rec = [ordered]@{}
        foreach ($p in $cur.Record.PSObject.Properties) { $rec[$p.Name] = $p.Value }
        foreach ($k in $Fields.Keys) {
            if ($k -match 'pass|secret|token|key') { throw "Lock field '$k' is not allowed." }
            $rec[$k] = $Fields[$k]
        }
        $rec['heartbeat'] = [DateTime]::UtcNow.ToString('o')
        try { return (Write-RemoteLock $Repo $rec $cur.Sha $Message) } catch { if ($_.Exception.Message -notmatch '^LOCK-CONFLICT' -or $try -eq 2) { throw } }
    }
}

# ---- choosing what to release -----------------------------------------------------------------------------------------------

# Pure. Which master commit to release, from the CI runs on master (newest run per commit decides). Commits are newest first
# (first-parent history of origin/master). Result.Status: green (Commit is set), waiting (head's CI is not finished), red (head's CI
# failed), none (LastGreen asked and no recent commit has a green run).
function Select-PinnedCommit([string[]]$Commits, $Runs, [switch]$LastGreen) {
    $verdict = {
        param($sha)
        $r = @($Runs | Where-Object { $_.headSha -eq $sha } | Sort-Object { [long]$_.databaseId } -Descending | Select-Object -First 1)
        if (-not $r.Count -or -not $r[0]) { return 'norun' }
        if ($r[0].status -ne 'completed') { return 'waiting' }
        if ($r[0].conclusion -eq 'success') { return 'green' }
        if ($r[0].conclusion -eq 'cancelled') { return 'cancelled' }
        return 'red'
    }
    if (-not $Commits.Count) { return [pscustomobject]@{ Status = 'none'; Commit = $null; Why = 'no commits' } }
    $head = & $verdict $Commits[0]
    if ($head -eq 'green') { return [pscustomobject]@{ Status = 'green'; Commit = $Commits[0]; Why = 'master head has green CI' } }
    if ($LastGreen) {
        foreach ($c in $Commits) { if ((& $verdict $c) -eq 'green') { return [pscustomobject]@{ Status = 'green'; Commit = $c; Why = "newest master commit with green CI (head is $head)" } } }
        return [pscustomobject]@{ Status = 'none'; Commit = $null; Why = 'no recent master commit has green CI' }
    }
    if ($head -eq 'red') { return [pscustomobject]@{ Status = 'red'; Commit = $null; Why = 'CI failed on master head' } }
    return [pscustomobject]@{ Status = 'waiting'; Commit = $null; Why = "CI on master head is $head" }
}

# Pure. Is the unfinished release in state.json the one to continue? Same rule Release.ps1 always used, kept here so the bootstrap
# (which pins a commit) and the run (which chooses the version) cannot disagree.
function Get-ResumePlan($State, [string]$AskedChannel, [string]$Version, [string[]]$Tags, [bool]$CheckOnly) {
    $no = [pscustomobject]@{ Resuming = $false; Version = $null }
    if ($CheckOnly -or -not $State -or -not $State.version) { return $no }
    if (@('done', 'withdrawn') -contains $State.status) { return $no }
    if ($Version -and $Version -ne $State.version) { return $no }
    $sv = [string]$State.version
    $svChannel = if ($sv.Contains('-')) { 'rc' } else { 'stable' }
    $newest = Get-NewestVersion $Tags
    if ($AskedChannel -and $AskedChannel -ne $svChannel) { return $no }
    if ($newest -and (Compare-SemVer $sv $newest) -lt 0) { return $no }
    return [pscustomobject]@{ Resuming = $true; Version = $sv }
}

# Pure. "Nothing new to release": the newest tag of this kind already holds this commit and that release finished.
# Returns 'done' (say so and stop), 'withdrawn' (refuse: merge a fix first) or $null (go on).
function Get-AlreadyReleased([string]$Channel, [string]$NewestTagOfKind, [string]$NewestTagCommit, [string]$Pinned, $State) {
    if (-not $NewestTagOfKind -or $NewestTagCommit -ne $Pinned) { return $null }
    if ($State -and $State.version -eq ($NewestTagOfKind -replace '^v', '')) {
        if ($State.status -eq 'done') { return 'done' }
        if ($State.status -eq 'withdrawn') { return 'withdrawn' }
    }
    return $null
}

# Waits with a growing pause (5 s, 10 s, 15 s ... capped) instead of a fixed one: quick when the thing finishes soon, light when it does not.
function Get-BackoffSeconds([int]$Attempt, [int]$Cap = 30) { return [Math]::Min($Cap, 5 * [Math]::Max(1, $Attempt)) }

# One place to read where the release machinery is: the GitHub lock (who holds it, which step, whether merges should wait), this PC's
# state.json (the release being worked on) and the last audit entries (the same files the releases dashboard reads).
function Get-ReleaseStatus([string]$Repo, [string]$Dir) {
    $remote = $null; $err = $null
    try { $remote = (Read-RemoteLock $Repo).Record } catch { $err = $_.Exception.Message }
    $info = $null; try { $info = Get-Content -Raw -LiteralPath (Join-Path $Dir 'lock-info.json') | ConvertFrom-Json } catch {}
    $audit = Join-Path $Dir 'audit.jsonl'
    $tail = @()
    if (Test-Path -LiteralPath $audit) { $tail = @(Get-Content -LiteralPath $audit -Tail 8 | ForEach-Object { try { $_ | ConvertFrom-Json } catch { $null } } | Where-Object { $_ }) }
    return [pscustomobject]@{ Lock = $remote; LockError = $err; LocalLock = $info; State = (Get-ReleaseState $Dir); AuditTail = $tail; AuditOk = (Test-AuditChain $audit).Ok }
}
function Show-ReleaseStatus([string]$Repo, [string]$Dir, [bool]$AsJson) {
    $s = Get-ReleaseStatus $Repo $Dir
    if ($AsJson) { $s | ConvertTo-Json -Depth 8; return }
    $l = $s.Lock
    if ($s.LockError) { Write-Host "Lock: could not be read ($($s.LockError))" -ForegroundColor Yellow }
    elseif (-not $l) { Write-Host 'Lock: free (nobody has ever released with it).' -ForegroundColor Green }
    elseif ($l.state -eq 'held') { Write-Host (Format-LockBusy $l (Get-Date)) -ForegroundColor Yellow; Write-Host "  pinned commit: $($l.commit)" }
    else { Write-Host "Lock: free. Last: $($l.last.version) $($l.last.outcome) at $($l.last.at) (asked by $($l.last.requester))." -ForegroundColor Green }
    if ($s.State) { Write-Host "This PC: release $($s.State.version): last finished stage '$($s.State.stage)', $($s.State.status)$(if ($s.State.reason) { ': ' + $s.State.reason })  (updated $($s.State.updated))" }
    if ($s.LocalLock) { Write-Host "This PC's lock holder: $($s.LocalLock.requester), pid $($s.LocalLock.pid)" }
    Write-Host "Audit log: $(if ($s.AuditOk) { 'chain unbroken' } else { 'CHAIN BROKEN (run -CheckAudit)' }). Last entries:"
    foreach ($e in $s.AuditTail) { Write-Host ("  {0}  {1}  {2}" -f $e.ts, $e.event, $e.version) }
}

# Where a release got to: { version; stage (last stage that finished); status (running|stopped|done); reason; updated }.
function Get-ReleaseState([string]$Dir) {
    $p = Join-Path $Dir 'state.json'
    if (-not (Test-Path -LiteralPath $p)) { return $null }
    try { return Get-Content -Raw -LiteralPath $p | ConvertFrom-Json } catch { return $null }
}
# Extra: fields kept from one run to the next (key, Proton Pass reference, the feed's release before this one). Never secrets.
function Set-ReleaseState([string]$Dir, [string]$Version, [string]$Stage, [string]$Status, [string]$Reason = '', [hashtable]$Extra = @{}) {
    $s = [ordered]@{ version = $Version; stage = $Stage; status = $Status; reason = $Reason; updated = [DateTime]::UtcNow.ToString('o') }
    foreach ($k in $Extra.Keys) { if ($k -match 'pass(?!ref)|secret|token') { throw "State field '$k' is not allowed." }; $s[$k] = $Extra[$k] }
    $tmp = Join-Path $Dir 'state.json.tmp'
    [System.IO.File]::WriteAllText($tmp, ($s | ConvertTo-Json), [System.Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $tmp -Destination (Join-Path $Dir 'state.json') -Force
}

# The stage after the last one that finished, in Release.ps1's order.
function Get-ResumeStage([string[]]$Order, [string]$LastDone) {
    $i = $Order.IndexOf($LastDone)
    if ($i -lt 0) { return $Order[0] }
    if ($i + 1 -ge $Order.Count) { return $null }
    return $Order[$i + 1]
}
