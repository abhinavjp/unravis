#Requires -Version 7.2
<#
.SYNOPSIS
  Signs a release's update manifest on the owner's own PC, and later publishes the update feed.
  See docs/auto-updater/SPEC.md section 5 and docs/auto-updater/KEYS.md.

.DESCRIPTION
  Needs PowerShell 7.2 or newer, git, go and gh (signed in). The tag must exist locally (git fetch --tags).

  Step 1, before you press Publish on the draft release:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -KeyFile D:\secret\rc-1.key.json
    - checks the local tag is the same commit as on GitHub, and that the manifest was built from that commit;
    - builds the signing tool from a clean copy of that commit (not your working folder);
    - shows what changed in the signing code and in the trusted key list since the last signed release, and makes you
      type "reviewed" / "keys ok" if anything did (a new key in that list would let its owner sign updates for everyone);
    - checks that every downloaded file has a GitHub build attestation from this repository's release workflow at the
      tag's commit (gh attestation verify); no attestation, no signing;
    - re-hashes every downloaded file against update-manifest.json, shows version, dates and names, asks you to type "yes";
    - asks the key passphrase (hidden), signs, then checks the signature against the keys built into the new tag AND the
      keys of the release now on the feed (so installed copies will accept it); uploads update-manifest.json.sig.

  Step 2, after you press Publish:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -Feed
    Refuses a draft. Verifies the published pair, checks it moves the feed forward, then copies it to the permanent
    "update-feed" release as rc.json or stable.json (plus .sig). Until then no installed copy is offered the release.
    Add -FirstRelease only for the very first release of a channel (nothing on the feed yet).

  The passphrase goes to the signing tool on its standard input; never an argument or an environment variable.
  With -ProtonPassRef 'pass://Vault/Item/password' it is read from the Proton Pass CLI instead of typed (see tools/release/ProtonPass.ps1);
  any problem with pass-cli falls back to typing it.
#>
param(
    [Parameter(Mandatory)] [string]$Tag,
    [string]$KeyFile,
    [switch]$Feed,
    [switch]$FirstRelease,
    [string]$Repo = 'abhinavjp/unravis',
    # A Proton Pass reference (pass://Vault/Item/password) the key passphrase is read from. Optional; if it cannot be read the script asks you to type it.
    [string]$ProtonPassRef
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue   # the tool would prefer it over the typed passphrase
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "Tag '$Tag' is not vX.Y.Z or vX.Y.Z-pre.N (lower case)." }
$channel = if ($Tag.Contains('-')) { 'rc' } else { 'stable' }
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-sign-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$src = Join-Path $work 'src'

function Invoke-Checked([string]$what, [scriptblock]$cmd) {
    & $cmd
    if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)." }
}
# Build attestation: every downloaded file must carry a GitHub attestation made by this repository's release workflow, at the tag's
# commit. A stolen token that swapped files in the draft cannot produce one. Fails closed: no attestation, no signing.
function Assert-Attested([string]$dir, [string]$repo, [string]$tag, [string]$commit) {
    $workflow = "$repo/.github/workflows/release.yml"
    foreach ($f in Get-ChildItem -LiteralPath $dir -File | Where-Object { $_.Name -match '^(unravis-.*\.(zip|exe)|update-manifest\.json)$' }) {
        $errFile = Join-Path $dir '.attest-err.txt'   # gh prints progress on stderr; keep it out of the JSON
        $json = gh attestation verify $f.FullName --repo $repo --signer-workflow $workflow --format json 2>$errFile
        if ($LASTEXITCODE -ne 0) { throw "No valid build attestation for $($f.Name) from $workflow : $(Get-Content -Raw -ErrorAction SilentlyContinue $errFile)" }
        Remove-Item -LiteralPath $errFile -Force -ErrorAction SilentlyContinue
        $found = $false
        foreach ($r in ($json | ConvertFrom-Json)) {
            $c = $r.verificationResult.signature.certificate
            if ($c.sourceRepositoryDigest -eq $commit -and $c.sourceRepositoryRef -eq "refs/tags/$tag") { $found = $true }
        }
        if (-not $found) { throw "The attestation for $($f.Name) is not for $tag at commit $commit. Do not sign." }
        Write-Host "  attested: $($f.Name)"
    }
}
function Confirm-Typed([string]$word, [string]$question) {
    if ((Read-Host "$question Type '$word' to go on") -ne $word) { throw 'Stopped. Nothing was signed or published.' }
}

try {
    # 1. The tag: same commit here and on GitHub.
    $sha = (git -C $repoRoot rev-parse --verify --quiet "refs/tags/$Tag^{commit}")
    if ($LASTEXITCODE -ne 0 -or -not $sha) { throw "Tag $Tag is not in this clone. Run: git fetch --tags" }
    $remote = (git -C $repoRoot ls-remote origin "refs/tags/$Tag" "refs/tags/$Tag^{}") | ForEach-Object { ($_ -split '\s+')[0] }
    if (-not ($remote -contains $sha)) { throw "Tag $Tag here ($sha) is not the commit GitHub has. Run: git fetch --tags --force, and find out why they differ." }

    # 2. What the feed holds now (the last signed release).
    $files = Join-Path $work 'files'
    New-Item -ItemType Directory -Path $files | Out-Null
    $feedFile = Join-Path $work "current-$channel.json"
    $haveFeed = $false
    $listing = gh release view update-feed --repo $Repo --json assets --jq '.assets[].name' 2>&1
    if ($LASTEXITCODE -eq 0) {
        if (@($listing) -contains "$channel.json") {
            Invoke-Checked 'downloading the current feed file' { gh release download update-feed --repo $Repo --pattern "$channel.json" --output $feedFile }
            $haveFeed = $true
        }
    } elseif (("$listing") -notmatch 'not found') {
        throw "Could not read the update-feed release: $listing"
    }
    if (-not $haveFeed -and $Feed -and -not $FirstRelease) { throw "The feed has no $channel.json. If this really is the first $channel release, add -FirstRelease." }
    $feedArgs = @()
    $prevTag = $null
    if ($haveFeed) {
        $feedArgs = @('-feed', $feedFile)
        $prevTag = 'v' + (Get-Content -Raw $feedFile | ConvertFrom-Json).version
        git -C $repoRoot rev-parse --verify --quiet "refs/tags/$prevTag^{commit}" | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "The feed's current release $prevTag is not in this clone. Run: git fetch --tags" }
    }

    # 3. Build the signing tool from the tag's commit in a throw-away worktree.
    Invoke-Checked 'git worktree add' { git -C $repoRoot worktree add --detach $src $sha | Out-Null }
    $tool = Join-Path $work 'sigtool.exe'
    Push-Location $src
    try { Invoke-Checked 'building the signing tool' { go build -mod=readonly -o $tool ./tools/release/sigtool } } finally { Pop-Location }

    $manifest = Join-Path $files 'update-manifest.json'
    # Keys the installed copies trust today (from the last signed release), so a new release is never signed with a key they would refuse.
    $prevKeys = $null
    if ($prevTag) {
        $prevKeys = Join-Path $work 'prev_trusted_keys.txt'
        git -C $repoRoot show "${prevTag}:internal/update/trusted_keys.txt" > $prevKeys
        if ($LASTEXITCODE -ne 0) { throw "Could not read the trusted keys of $prevTag." }
    }

    if (-not $Feed) {
        if (-not $KeyFile -or -not (Test-Path -LiteralPath $KeyFile)) { throw 'Pass -KeyFile (the encrypted private key file).' }

        # What can see the passphrase and what keys will be trusted: show every change since the last signed release.
        $baseline = if ($prevTag) { $prevTag } else { $null }
        if ($baseline) {
            $codeDiff = git -C $repoRoot diff --stat $baseline $sha -- tools/release/sigtool internal/update ':!internal/update/trusted_keys.txt'
            if ($codeDiff) { Write-Host "Signing and verifying code changed since ${baseline}:"; $codeDiff | Write-Host; Confirm-Typed 'reviewed' 'You have read that diff (git diff) and it is yours.' }
            $keyDiff = git -C $repoRoot diff $baseline $sha -- internal/update/trusted_keys.txt
            if ($keyDiff) { Write-Host 'The list of trusted public keys CHANGED:'; $keyDiff | Write-Host; Confirm-Typed 'keys ok' 'Every added or removed key is one you made or removed on purpose.' }
        } else {
            Write-Host 'Trusted public keys in this release:'; Get-Content (Join-Path $src 'internal/update/trusted_keys.txt') | Write-Host
            Confirm-Typed 'keys ok' 'These are your keys and nobody else''s.'
        }

        Invoke-Checked 'downloading the draft' { gh release download $Tag --repo $Repo --dir $files }
        Invoke-Checked 'checking the files against the manifest' { & $tool check-assets -manifest $manifest -dir $files }
        Invoke-Checked 'checking the manifest against the tag and the feed' { & $tool preflight -manifest $manifest -tag $Tag -commit $sha @feedArgs }
        Write-Host 'Checking that GitHub attests these files were built by the release workflow from the tag:'
        Assert-Attested $files $Repo $Tag $sha
        Confirm-Typed 'yes' 'Sign this release?'
        $secure = $null
        if ($ProtonPassRef) {
            try { $secure = Get-ProtonPassSecret -Reference $ProtonPassRef; Write-Host 'Passphrase read from Proton Pass (not shown).' }
            catch { Write-Host "Proton Pass: $($_.Exception.Message)" -ForegroundColor Yellow; Write-Host 'Falling back to typing the passphrase.' -ForegroundColor Yellow }
        }
        if (-not $secure) { $secure = Read-Host "Passphrase for $KeyFile" -AsSecureString }
        $plain = [System.Net.NetworkCredential]::new('', $secure).Password
        try { $plain | & $tool sign -key $KeyFile -in $manifest -out "$manifest.sig" } finally { $plain = $null; $secure.Dispose() }
        if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
        Invoke-Checked 'verifying with the keys of the new tag' { & $tool verify -in $manifest -sig "$manifest.sig" -trusted -channel $channel -want $channel }
        if ($prevKeys) { Invoke-Checked "verifying with the keys installed copies trust ($prevTag)" { & $tool verify -in $manifest -sig "$manifest.sig" -keys $prevKeys -channel $channel -want $channel } }
        Invoke-Checked 'uploading the signature' { gh release upload $Tag "$manifest.sig" --repo $Repo --clobber }
        Write-Host 'Signed. Check the draft and press Publish, then run this script again with -Feed.'
    } else {
        $isDraft = gh release view $Tag --repo $Repo --json isDraft --jq '.isDraft'
        if ($LASTEXITCODE -ne 0) { throw 'Could not read the release (gh cannot see some drafts; publish it first).' }
        if ($isDraft -ne 'false') { throw "$Tag is still a draft. Publish it first, then run -Feed." }
        Invoke-Checked 'downloading the published release' { gh release download $Tag --repo $Repo --dir $files }
        Invoke-Checked 'checking the files against the manifest' { & $tool check-assets -manifest $manifest -dir $files }
        Invoke-Checked 'verifying the published pair' { & $tool verify -in $manifest -sig "$manifest.sig" -trusted -channel $channel -want $channel }
        if ($prevKeys) { Invoke-Checked "verifying with the keys installed copies trust ($prevTag)" { & $tool verify -in $manifest -sig "$manifest.sig" -keys $prevKeys -channel $channel -want $channel } }
        Invoke-Checked 'checking the manifest against the tag and the feed' { & $tool preflight -manifest $manifest -tag $Tag -commit $sha @feedArgs }
        gh release view update-feed --repo $Repo --json tagName 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Invoke-Checked 'creating the update-feed release' { gh release create update-feed --repo $Repo --target $sha --prerelease --latest=false --title 'update-feed' --notes 'Signed pointers to the newest release of each channel. Read by the updater. Do not edit by hand.' }
        }
        Copy-Item $manifest (Join-Path $files "$channel.json")
        Copy-Item "$manifest.sig" (Join-Path $files "$channel.json.sig")
        # The two uploads are not one atomic step; a client that reads between them fails the signature check and tries again later.
        Invoke-Checked 'updating the feed' { gh release upload update-feed (Join-Path $files "$channel.json") (Join-Path $files "$channel.json.sig") --repo $Repo --clobber }
        Write-Host "Feed updated: $channel now points at $Tag."
    }
} finally {
    if (Test-Path -LiteralPath $src) { git -C $repoRoot worktree remove --force $src 2>&1 | Out-Null }
    git -C $repoRoot worktree prune 2>&1 | Out-Null
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
