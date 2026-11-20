<#
.SYNOPSIS
  Signs a release's update manifest on the owner's own PC, and later publishes the update feed.
  See docs/auto-updater/SPEC.md section 5 and docs/auto-updater/KEYS.md.

.DESCRIPTION
  Step 1, before you press Publish on the draft release:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -KeyFile D:\secret\rc-1.key.json
    Builds the signing tool from a clean copy of the TAG (not your working folder), downloads the draft's files,
    re-hashes every one against update-manifest.json, shows you the version, dates and file names and asks you to type
    "yes", asks the key passphrase (hidden), signs, checks the new signature against the keys built into that tag,
    and uploads update-manifest.json.sig.

  Step 2, after you press Publish:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -Feed
    Refuses if the release is still a draft. Verifies the published pair, checks it moves the feed forward, then copies it to
    the permanent "update-feed" release as rc.json or stable.json (plus .sig). Until this step runs, no installed copy is offered the release.

  The passphrase goes to the signing tool on its standard input. It is never an argument or an environment variable.
  Needs: git, go, gh (signed in). The tag must exist locally (git fetch --tags).
#>
param(
    [Parameter(Mandatory)] [string]$Tag,
    [string]$KeyFile,
    [switch]$Feed,
    [string]$Repo = 'abhinavjp/unravis'
)
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$') { throw "Tag '$Tag' is not vX.Y.Z or vX.Y.Z-pre.N." }
$channel = if ($Tag.Contains('-')) { 'rc' } else { 'stable' }
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-sign-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null

function Invoke-Checked([string]$what, [scriptblock]$cmd) {
    & $cmd
    if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)." }
}

try {
    # Build the tool from the tag in a throw-away worktree, so uncommitted or other-branch code never sees the passphrase.
    git -C $repoRoot rev-parse --verify --quiet "refs/tags/$Tag^{commit}" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Tag $Tag is not in this clone. Run: git fetch --tags" }
    $src = Join-Path $work 'src'
    Invoke-Checked 'git worktree add' { git -C $repoRoot worktree add --detach $src $Tag | Out-Null }
    $tool = Join-Path $work 'sigtool.exe'
    Push-Location $src
    try { Invoke-Checked 'building the signing tool' { go build -mod=readonly -o $tool ./tools/release/sigtool } } finally { Pop-Location }

    $files = Join-Path $work 'files'
    New-Item -ItemType Directory -Path $files | Out-Null
    $manifest = Join-Path $files 'update-manifest.json'
    $feedFile = Join-Path $work "current-$channel.json"
    $haveFeed = $false
    gh release download update-feed --repo $Repo --pattern "$channel.json" --output $feedFile 2>$null
    if ($LASTEXITCODE -eq 0) { $haveFeed = $true }
    $feedArgs = if ($haveFeed) { @('-feed', $feedFile) } else { @() }

    if (-not $Feed) {
        if (-not $KeyFile -or -not (Test-Path -LiteralPath $KeyFile)) { throw 'Pass -KeyFile (the encrypted private key file).' }
        Invoke-Checked 'downloading the draft' { gh release download $Tag --repo $Repo --dir $files }
        Invoke-Checked 'checking the files against the manifest' { & $tool check-assets -manifest $manifest -dir $files }
        Invoke-Checked 'checking the manifest against the tag and the feed' { & $tool preflight -manifest $manifest -tag $Tag @feedArgs }
        if ((Read-Host 'Sign this? Type yes') -ne 'yes') { throw 'Not signed.' }
        $secure = Read-Host "Passphrase for $KeyFile" -AsSecureString
        $plain = [System.Net.NetworkCredential]::new('', $secure).Password
        try { $plain | & $tool sign -key $KeyFile -in $manifest -out "$manifest.sig" } finally { $plain = $null }
        if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
        Invoke-Checked 'verifying the new signature' { & $tool verify -in $manifest -sig "$manifest.sig" -trusted -channel $channel -want $channel }
        Invoke-Checked 'uploading the signature' { gh release upload $Tag "$manifest.sig" --repo $Repo --clobber }
        Write-Host 'Signed. Check the draft and press Publish, then run this script again with -Feed.'
    } else {
        $isDraft = gh release view $Tag --repo $Repo --json isDraft --jq '.isDraft'
        if ($LASTEXITCODE -ne 0) { throw 'Could not read the release (drafts are hard for gh to find; if it is a draft, publish it first).' }
        if ($isDraft -ne 'false') { throw "$Tag is still a draft. Publish it first, then run -Feed." }
        Invoke-Checked 'downloading the published manifest' { gh release download $Tag --repo $Repo --dir $files --pattern 'update-manifest.json' --pattern 'update-manifest.json.sig' }
        Invoke-Checked 'verifying the published pair' { & $tool verify -in $manifest -sig "$manifest.sig" -trusted -channel $channel -want $channel }
        Invoke-Checked 'checking the manifest against the tag and the feed' { & $tool preflight -manifest $manifest -tag $Tag @feedArgs }
        gh release view update-feed --repo $Repo --json tagName 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) {
            $head = (git -C $repoRoot rev-parse "refs/tags/$Tag^{commit}")
            Invoke-Checked 'creating the update-feed release' { gh release create update-feed --repo $Repo --target $head --prerelease --latest=false --title 'update-feed' --notes 'Signed pointers to the newest release of each channel. Read by the updater. Do not edit by hand.' }
        }
        Copy-Item $manifest (Join-Path $files "$channel.json")
        Copy-Item "$manifest.sig" (Join-Path $files "$channel.json.sig")
        # The two uploads are not one atomic step; a client that reads between them fails the signature check and tries again later.
        Invoke-Checked 'updating the feed' { gh release upload update-feed (Join-Path $files "$channel.json") (Join-Path $files "$channel.json.sig") --repo $Repo --clobber }
        Write-Host "Feed updated: $channel now points at $Tag."
    }
} finally {
    git -C $repoRoot worktree remove --force (Join-Path $work 'src') 2>$null
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
