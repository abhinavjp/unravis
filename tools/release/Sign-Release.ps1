<#
.SYNOPSIS
  Signs a release's update manifest on the owner's own PC, and later publishes the update feed.
  See docs/auto-updater/SPEC.md section 5 and docs/auto-updater/KEYS.md.

.DESCRIPTION
  Step 1, before you press Publish on the draft release:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -KeyFile D:\secret\unravis-rc.key.json -PubFiles D:\secret\unravis-rc.pub
    It downloads the draft's files, re-hashes every one against update-manifest.json (it never signs on trust),
    asks for the key passphrase (typed, hidden), signs, checks the signature again, and uploads update-manifest.json.sig.

  Step 2, after you press Publish:
      ./tools/release/Sign-Release.ps1 -Tag v0.1.0-rc.48 -Feed
    It copies the signed manifest to the permanent "update-feed" release as rc.json or stable.json (plus .sig),
    which is the small file every installed copy checks. Until this step runs, no installed copy is offered the release.

  The private key never goes into the repository, CI or a command line argument. Needs: go, gh (signed in).
#>
param(
    [Parameter(Mandatory)] [string]$Tag,
    [string]$KeyFile,
    [string[]]$PubFiles,
    [switch]$Feed
)
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$') { throw "Tag '$Tag' is not vX.Y.Z or vX.Y.Z-pre.N." }
$channel = if ($Tag.Contains('-')) { 'rc' } else { 'stable' }
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-sign-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    Push-Location $repoRoot
    if (-not $Feed) {
        if (-not $KeyFile -or -not (Test-Path -LiteralPath $KeyFile)) { throw 'Pass -KeyFile (the encrypted private key file).' }
        if (-not $PubFiles) { throw 'Pass -PubFiles (the matching public key file(s)) so the signature can be checked after signing.' }
        gh release download $Tag --dir $work
        if ($LASTEXITCODE -ne 0) { throw 'gh release download failed (is the draft there and are you signed in to gh?).' }
        $manifest = Join-Path $work 'update-manifest.json'
        go run ./tools/release/sigtool check-assets -manifest $manifest -dir $work
        if ($LASTEXITCODE -ne 0) { throw 'The downloaded files do not match the manifest. Nothing was signed.' }
        $secure = Read-Host "Passphrase for $KeyFile" -AsSecureString
        $env:UNRAVIS_KEY_PASSPHRASE = [System.Net.NetworkCredential]::new('', $secure).Password
        try {
            go run ./tools/release/sigtool sign -key $KeyFile -in $manifest -out "$manifest.sig"
            if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
        } finally { Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue }
        go run ./tools/release/sigtool verify -in $manifest -sig "$manifest.sig" -pub ($PubFiles -join ',') -channel $channel
        if ($LASTEXITCODE -ne 0) { throw 'The new signature did not verify. Nothing was uploaded.' }
        gh release upload $Tag "$manifest.sig" --clobber
        if ($LASTEXITCODE -ne 0) { throw 'gh release upload failed.' }
        Write-Host "Signed. Now check the draft and press Publish, then run this script again with -Feed."
    } else {
        # The release must be public now: download from the published tag, not from a draft.
        gh release download $Tag --dir $work --pattern 'update-manifest.json' --pattern 'update-manifest.json.sig'
        if ($LASTEXITCODE -ne 0) { throw 'Could not download the published manifest and signature. Is the release published and signed?' }
        $feedExists = (gh release list --limit 200 --json tagName | ConvertFrom-Json | Where-Object { $_.tagName -eq 'update-feed' })
        if (-not $feedExists) {
            if (-not (git rev-parse --verify --quiet update-feed)) { git tag update-feed; git push origin update-feed }
            gh release create update-feed --prerelease --latest=false --verify-tag --title 'update-feed' --notes 'Signed pointers to the newest release of each channel. Read by the updater. Do not edit by hand.'
            if ($LASTEXITCODE -ne 0) { throw 'Could not create the update-feed release.' }
        }
        Copy-Item (Join-Path $work 'update-manifest.json') (Join-Path $work "$channel.json")
        Copy-Item (Join-Path $work 'update-manifest.json.sig') (Join-Path $work "$channel.json.sig")
        gh release upload update-feed (Join-Path $work "$channel.json") (Join-Path $work "$channel.json.sig") --clobber
        if ($LASTEXITCODE -ne 0) { throw 'Could not update the feed.' }
        Write-Host "Feed updated: $channel now points at $Tag."
    }
} finally {
    Pop-Location
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
