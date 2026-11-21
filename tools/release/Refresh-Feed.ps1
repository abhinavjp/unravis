#Requires -Version 7.2
<#
.SYNOPSIS
  Re-signs the release now on the update feed with a later expiry, so the feed does not run out (the "freeze" problem).
  See docs/auto-updater/REFRESH.md.

.DESCRIPTION
  A signed manifest expires (180 days by default). If nobody publishes a release before then, installed copies stop seeing updates.
  This keeps the same release (same version, same files, same commit), moves the dates, raises seq by one, signs it, and
  uploads it as rc.json or stable.json. Run it about every 90 days, or before a long quiet time. It installs nothing new:
  machines that already have this version see "latest"; machines on an older version are offered the same release as before.
  The passphrase is read like Sign-Release.ps1 does: Proton Pass with -ProtonPassRef, else typed (hidden).
#>
param(
    [Parameter(Mandatory)] [ValidateSet('rc', 'stable')] [string]$Channel,
    [Parameter(Mandatory)] [string]$KeyFile,
    [ValidateRange(30, 365)] [int]$Days = 180,
    [string]$Repo = 'abhinavjp/unravis',
    [string]$ProtonPassRef,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ProtonPass.ps1')
$PSNativeCommandUseErrorActionPreference = $false
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE -ErrorAction SilentlyContinue
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-refresh-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
function Invoke-Checked([string]$what, [scriptblock]$cmd) { & $cmd; if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)." } }
try {
    # The signing tool is built from a clean checkout that matches GitHub, never from a changed working folder.
    if (git -C $repoRoot status --porcelain) { throw 'The working folder has changes. Use a clean checkout of master.' }
    Invoke-Checked 'fetching master' { git -C $repoRoot fetch --quiet origin master }
    if ((git -C $repoRoot rev-parse HEAD) -ne (git -C $repoRoot rev-parse origin/master)) { throw 'This checkout is not the same as origin/master. Run git pull first.' }
    # What installed copies trust today is the release now on the feed. The signing code and key list are shown against it, like Sign-Release.ps1.
    Invoke-Checked 'reading the feed' { gh release download update-feed --repo $Repo --pattern "$Channel.json*" --dir $work }
    $prevTag = 'v' + (Get-Content -Raw (Join-Path $work "$Channel.json") | ConvertFrom-Json).version
    git -C $repoRoot rev-parse --verify --quiet "refs/tags/$prevTag^{commit}" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "The feed's current release $prevTag is not in this clone. Run: git fetch --tags" }
    $prevKeys = Join-Path $work 'prev_trusted_keys.txt'
    git -C $repoRoot show "${prevTag}:internal/update/trusted_keys.txt" > $prevKeys
    if ($LASTEXITCODE -ne 0) { throw "Could not read the trusted keys of $prevTag." }
    $codeDiff = git -C $repoRoot diff --stat $prevTag HEAD -- tools/release/sigtool internal/update ':!internal/update/trusted_keys.txt'
    if ($codeDiff) { Write-Host "Signing and verifying code changed since ${prevTag}:"; $codeDiff | Write-Host; if ((Read-Host "You have read that diff (git diff) and it is yours. Type 'reviewed' to go on") -ne 'reviewed') { throw 'Stopped. Nothing was signed.' } }
    $keyDiff = git -C $repoRoot diff $prevTag HEAD -- internal/update/trusted_keys.txt
    if ($keyDiff) { Write-Host 'The list of trusted public keys CHANGED:'; $keyDiff | Write-Host; if ((Read-Host "Every added or removed key is one you made or removed on purpose. Type 'keys ok' to go on") -ne 'keys ok') { throw 'Stopped. Nothing was signed.' } }
    $tool = Join-Path $work 'sigtool.exe'
    Push-Location $repoRoot
    try { Invoke-Checked 'building the signing tool' { go build -mod=readonly -o $tool ./tools/release/sigtool } } finally { Pop-Location }

    $old = Join-Path $work "$Channel.json"
    $file = Join-Path $work "$Channel-new.json"
    Invoke-Checked 'checking the feed pair and building the refreshed file' { & $tool refresh -in $old -sig "$old.sig" -keys $prevKeys -days $Days -out $file }
    Write-Host ''; Get-Content -Raw $file | Write-Host
    if (-not $Yes -and (Read-Host "Publish this refreshed $Channel manifest? Type 'yes' to go on") -ne 'yes') { throw 'Stopped. Nothing was signed or published.' }

    $secure = $null
    if ($ProtonPassRef) {
        try { $secure = Get-ProtonPassSecret -Reference $ProtonPassRef; Write-Host 'Passphrase read from Proton Pass (not shown).' }
        catch { Write-Host "Proton Pass: $($_.Exception.Message)" -ForegroundColor Yellow; Write-Host 'Falling back to typing the passphrase.' -ForegroundColor Yellow }
    }
    if (-not $secure) { $secure = Read-Host "Passphrase for $KeyFile" -AsSecureString }
    $plain = [System.Net.NetworkCredential]::new('', $secure).Password
    try { $plain | & $tool sign -key $KeyFile -in $file -out "$file.sig" } finally { $plain = $null; $secure.Dispose() }
    if ($LASTEXITCODE -ne 0) { throw 'Signing failed. Nothing was uploaded.' }
    Invoke-Checked 'verifying with the keys built into this checkout' { & $tool verify -in $file -sig "$file.sig" -trusted -channel $Channel -want $Channel }
    Invoke-Checked "verifying with the keys installed copies trust ($prevTag)" { & $tool verify -in $file -sig "$file.sig" -keys $prevKeys -channel $Channel -want $Channel }
    $out = Join-Path $work 'out'; New-Item -ItemType Directory -Path $out | Out-Null
    Copy-Item $file (Join-Path $out "$Channel.json"); Copy-Item "$file.sig" (Join-Path $out "$Channel.json.sig")
    # The two uploads are not one atomic step; a client that reads between them fails the signature check and tries again later.
    Invoke-Checked 'uploading to the update feed' { gh release upload update-feed (Join-Path $out "$Channel.json") (Join-Path $out "$Channel.json.sig") --repo $Repo --clobber }
    $back = Join-Path $work 'back'; New-Item -ItemType Directory -Path $back | Out-Null
    Invoke-Checked 'reading back the published pair' { gh release download update-feed --repo $Repo --pattern "$Channel.json*" --dir $back }
    Invoke-Checked 'verifying the published pair' { & $tool verify -in (Join-Path $back "$Channel.json") -sig (Join-Path $back "$Channel.json.sig") -keys $prevKeys -channel $Channel -want $Channel }
    Write-Host "Refreshed. The feed is valid again until the new expiry." -ForegroundColor Green
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
