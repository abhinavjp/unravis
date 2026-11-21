# Shared helpers for the release scripts (docs/auto-updater/RELEASE-MANAGER.md). Dot-source it:  . ./tools/release/ReleaseLib.ps1
#
# Rules this file keeps:
#  - Nothing here reads, prints or stores a passphrase except Get-SigningPassphrase, which only returns a SecureString.
#  - Every check fails closed: when in doubt it throws, with a message that says what is wrong and what to do.
#  - Under -Yes (the owner's standing approval) nothing here ever waits for typed input. A check that would ask a person
#    either decides by a fixed rule or stops the run.
#  - The pure functions (no git, gh or file system writes) are tested by Test-ReleaseManager.ps1 in CI.

$script:ReleaseTrustedKeysPath = 'internal/update/trusted_keys.txt'
# The code that sees the passphrase or decides what installed copies accept.
$script:ReleaseSigningPaths = @('tools/release/sigtool', 'internal/update', ':!internal/update/trusted_keys.txt')
# Paths whose PRs are named in the release report (they change what is built, signed or trusted).
$script:ReleaseSensitivePaths = @('tools/release', 'internal/update', '.github/workflows', 'go.mod', 'go.sum', 'installer')
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
            $p = (Get-Content -Raw -LiteralPath $pubFile).Trim() -split '\s+'
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
# Input: the lines of "go list -deps ./tools/release/sigtool". Output: the packages that are not allowed.
function Get-ForbiddenSigtoolDeps([string[]]$Lines) {
    $ok = @('api-audit-explorer/internal/update', 'api-audit-explorer/tools/release/sigtool')
    return , @($Lines | Where-Object { $_ } | ForEach-Object { $_.Trim() } | Where-Object {
                $first = ($_ -split '/')[0]
                -not ($ok -contains $_) -and ($first.Contains('.') -or $first -eq 'api-audit-explorer')
            })
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
    foreach ($c in $shas) {
        $n = gh api "repos/$Repo/commits/$c/pulls" --jq '[.[] | select(.merged_at != null and .base.ref == "master") | .number] | first // empty' 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $n) { $missing += $c } else { $prs[$c] = [int]$n }
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
    param([string]$RepoRoot, [string]$Repo, [string]$Baseline, [string]$Commit, [string]$KeyDir, [switch]$Yes, [string]$Context, [string]$AuditPath, [string]$SourceDir)
    if (-not $SourceDir) { $SourceDir = $RepoRoot }
    $r = [ordered]@{ CodeChanged = $false; DiffSha256 = $null; KeysAdded = @(); KeysRemoved = @() }
    $owner = Get-OwnerKeys $KeyDir
    $ask = { param($word, $q) if ((Read-Host "$q Type '$word' to go on") -ne $word) { throw 'Stopped. Nothing was signed or published.' } }
    $newKeys = (git -C $RepoRoot show "${Commit}:$script:ReleaseTrustedKeysPath") -join "`n"
    if ($LASTEXITCODE -ne 0) { throw "Could not read $script:ReleaseTrustedKeysPath at $Commit." }
    if (-not $Baseline) {
        Write-Host 'Trusted public keys in this release:'; $newKeys | Write-Host
        if ($Yes) {
            $c = Compare-KeyLists '' $newKeys $owner
            if ($c.Foreign.Count) { throw "The trusted key list holds keys you have no key file for in ${KeyDir}: $(($c.Foreign | ForEach-Object { "$($_.Role) $($_.Pub)" }) -join '; '). Not signing." }
            Write-Host "-Yes: every trusted key has its key file in $KeyDir." -ForegroundColor Yellow
            $r.KeysAdded = @($c.Added | ForEach-Object { "$($_.Role) $($_.Pub.Substring(0, 16))" })
        } else { & $ask 'keys ok' "These are your keys and nobody else's." }
        return [pscustomobject]$r
    }
    $codeDiff = git -C $RepoRoot diff --stat $Baseline $Commit -- $script:ReleaseSigningPaths
    if ($codeDiff) {
        $r.CodeChanged = $true
        Write-Host "Signing and verifying code changed since ${Baseline}:"; $codeDiff | Write-Host
        $diffText = (git -C $RepoRoot diff $Baseline $Commit -- $script:ReleaseSigningPaths) -join "`n"
        $r.DiffSha256 = Get-TextSha256 $diffText
        if ($Yes) {
            $cmp = gh api "repos/$Repo/compare/master...$Commit" --jq .status 2>$null
            if ($LASTEXITCODE -ne 0 -or ($cmp -ne 'behind' -and $cmp -ne 'identical')) { throw "The signing code at $Context is not part of GitHub's master (compare says '$cmp'). Not signing. Merge it through a pull request first." }
            $pr = Get-CommitPullRequests $RepoRoot $Repo $Baseline $Commit
            if ($pr.Missing.Count) { throw "These commits on master since $Baseline did not come through a merged pull request: $(($pr.Missing | ForEach-Object { $_.Substring(0, 7) }) -join ', '). Not signing." }
            # SourceDir is a clean checkout of Commit (Sign-Release.ps1 passes its throw-away worktree).
            Push-Location $SourceDir; try { $deps = @(go list -deps ./tools/release/sigtool) } finally { Pop-Location }
            if ($LASTEXITCODE -ne 0 -or -not $deps.Count) { throw 'go list could not read the signing tool''s dependencies. Not signing.' }
            $bad = Get-ForbiddenSigtoolDeps $deps
            if ($bad.Count) { throw "The signing tool now depends on code outside the standard library and internal/update: $($bad -join ', '). Not signing." }
            Write-Host "-Yes: the signing code is on master through merged PRs; diff sha256 $($r.DiffSha256)." -ForegroundColor Yellow
        } else { & $ask 'reviewed' 'You have read that diff (git diff) and it is yours.' }
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
            Write-Host '-Yes: every added key has its key file in the key folder; removed keys only narrow trust.' -ForegroundColor Yellow
        } else { & $ask 'keys ok' 'Every added or removed key is one you made or removed on purpose.' }
    }
    if ($AuditPath) { Write-AuditEntry $AuditPath @{ event = 'signing-gates'; context = $Context; baseline = $Baseline; commit = $Commit; code_changed = $r.CodeChanged; diff_sha256 = "$($r.DiffSha256)"; keys_added = $r.KeysAdded; keys_removed = $r.KeysRemoved; mode = $(if ($Yes) { 'auto' } else { 'typed' }) } }
    return [pscustomobject]$r
}

# ---- passphrase -------------------------------------------------------------------------------------------------------------

# The key passphrase as a SecureString. From Proton Pass when a reference is given. Under -Yes it never falls back to typing
# (a hidden prompt in a window nobody watches stalls the release); it throws with the reason instead.
function Get-SigningPassphrase([string]$ProtonPassRef, [string]$KeyFile, [switch]$Yes) {
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
    foreach ($hive in 'HKLM:', 'HKCU:') {
        $base = "$hive\SOFTWARE\Policies\Microsoft\Windows\PowerShell"
        foreach ($p in @(@('Transcription', 'EnableTranscripting', 'PowerShell transcription'), @('ModuleLogging', 'EnableModuleLogging', 'PowerShell module logging'))) {
            $v = (Get-ItemProperty -LiteralPath "$base\$($p[0])" -Name $p[1] -ErrorAction SilentlyContinue).($p[1])
            if ($v -eq 1) { $out += "$($p[2]) is on ($hive policy)" }
        }
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
function Enter-ReleaseLock([string]$Dir) {
    $p = Join-Path $Dir 'release.lock'
    try { return [System.IO.File]::Open($p, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None) }
    catch { throw 'Another release is running on this PC (release.lock is held). Wait for it to finish.' }
}

# Where a release got to: { version; stage (last stage that finished); status (running|stopped|done); reason; updated }.
function Get-ReleaseState([string]$Dir) {
    $p = Join-Path $Dir 'state.json'
    if (-not (Test-Path -LiteralPath $p)) { return $null }
    try { return Get-Content -Raw -LiteralPath $p | ConvertFrom-Json } catch { return $null }
}
function Set-ReleaseState([string]$Dir, [string]$Version, [string]$Stage, [string]$Status, [string]$Reason = '') {
    $s = [ordered]@{ version = $Version; stage = $Stage; status = $Status; reason = $Reason; updated = [DateTime]::UtcNow.ToString('o') }
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
