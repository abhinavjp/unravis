#Requires -Version 7.2
<#
.SYNOPSIS
  After a release is PUBLISHED and its feed updated, proves on a clean Windows machine that real users would get it.
  Runs in .github/workflows/verify-release.yml on a GitHub-hosted runner; also runnable by hand on a throw-away VM.

.DESCRIPTION
  Part A, always (needs only the new tag):
    A1  The public feed (update-feed release, <channel>.json + .sig) names this version.
    A2  The release folder verifies offline with its own setup (signature, expiry, sizes, hashes).
    A3  A folder with a damaged signature, and one with a changed file, are REFUSED by the same check.
  Part B, when -Previous is given (an older published release that already trusts our keys and has the updater):
    B1  Install the previous release silently (user scope, with a Blackbox).
    B2  Its own `unravis.exe --check-update` reaches the real public feed and says the new version is available.
    B3  Upgrade to the new release with the --expect-sha256/--expect-version pin: the app and the managed Blackbox report the new
        version, the service runs and answers.
    B4  The old setup is REFUSED when asked to put the older version back over the new one (no silent downgrade).
    B5  Standalone recorder (a server with no Unravis): install the previous recorder alone, wait for the service to update ITSELF
        from the feed (first check is 1 to 5 minutes after start), then check its version and that it is still running. No one
        triggers it and no one is logged in to approve anything.
  Exit code 0 only when every step held. Each step prints PASS/FAIL. Nothing here publishes anything or needs a secret.

.PARAMETER Tag      The new, published tag (vX.Y.Z or vX.Y.Z-rc.N).
.PARAMETER Previous An older published tag to upgrade from. Leave out for the very first signed release (nothing before it trusts the keys).
.PARAMETER WaitMinutes How long B5 waits for the recorder to update itself (default 12).
#>
param(
    [Parameter(Mandatory)] [string]$Tag,
    [string]$Previous,
    [string]$Repo = 'abhinavjp/unravis',
    [int]$WaitMinutes = 12
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "Tag '$Tag' is not vX.Y.Z or vX.Y.Z-pre.N (lower case)." }
if ($Previous -and $Previous -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "Previous '$Previous' is not a tag." }
$ver = $Tag.TrimStart('v'); $pver = if ($Previous) { $Previous.TrimStart('v') } else { $null }
$channel = if ($Tag.Contains('-')) { 'rc' } else { 'stable' }
$work = Join-Path ([IO.Path]::GetTempPath()) ('unravis-verify-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$failures = [System.Collections.Generic.List[string]]::new()

function Step([string]$id, [scriptblock]$body) {
    try { & $body; Write-Host "PASS $id" -ForegroundColor Green }
    catch { Write-Host "FAIL $id : $($_.Exception.Message)" -ForegroundColor Red; $script:failures.Add("$id : $($_.Exception.Message)") }
}
function Expect([bool]$ok, [string]$msg) { if (-not $ok) { throw $msg } }
function Get-Release([string]$tag, [string]$dir) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    gh release download $tag --repo $Repo --dir $dir --pattern 'unravis-setup-*.exe' --pattern 'unravis-blackbox-*.zip' --pattern 'update-manifest.json' --pattern 'update-manifest.json.sig'
    if ($LASTEXITCODE -ne 0) { throw "could not download $tag" }
}

$newDir = Join-Path $work 'new'
Get-Release $Tag $newDir
$newSetup = (Get-ChildItem $newDir -Filter 'unravis-setup-*.exe' | Select-Object -First 1).FullName
Expect ([bool]$newSetup) 'the release has no setup program'

Step 'A1 public feed names this version' {
    $base = "https://github.com/$Repo/releases/download/update-feed"
    $feed = Invoke-RestMethod -Uri "$base/$channel.json" -UseBasicParsing
    $null = Invoke-WebRequest -Uri "$base/$channel.json.sig" -UseBasicParsing   # the signature file must be reachable too
    Expect ($feed.version -eq $ver) "feed says '$($feed.version)', expected '$ver'"
}
Step 'A2 release verifies offline (signature, expiry, hashes)' {
    & $newSetup --verify $newDir | Out-Host
    Expect ($LASTEXITCODE -eq 0) "--verify exit $LASTEXITCODE"
}
Step 'A3a damaged signature is refused' {
    $d = Join-Path $work 'bad-sig'; Copy-Item $newDir $d -Recurse
    $sig = Join-Path $d 'update-manifest.json.sig'
    # The file is "unravis-sig-v1 <keyid> <base64>". Change one base64 letter in the middle so the text still decodes and the signature check itself fails.
    $t = [IO.File]::ReadAllText($sig); $parts = $t.TrimEnd() -split ' '
    $b64 = $parts[-1].ToCharArray(); $m = [int]($b64.Length / 2); $b64[$m] = if ($b64[$m] -eq 'A') { 'B' } else { 'A' }
    $parts[-1] = -join $b64; [IO.File]::WriteAllText($sig, (($parts -join ' ') + "`n"))
    & $newSetup --verify $d *> $null
    Expect ($LASTEXITCODE -ne 0) 'a damaged signature was accepted'
}
Step 'A3b changed file is refused' {
    $d = Join-Path $work 'bad-file'; Copy-Item $newDir $d -Recurse
    $z = Get-ChildItem $d -Filter 'unravis-blackbox-*.zip' | Select-Object -First 1
    if ($z) { $fs = [IO.File]::Open($z.FullName, 'Open', 'ReadWrite'); $fs.Seek(-1, 'End') | Out-Null; $last = $fs.ReadByte(); $fs.Seek(-1, 'End') | Out-Null; $fs.WriteByte($last -bxor 0x01); $fs.Close() }
    Expect ([bool]$z) 'no blackbox zip in the release to damage'
    & $newSetup --verify $d *> $null
    Expect ($LASTEXITCODE -ne 0) 'a changed file was accepted'
}

if ($Previous) {
    $oldDir = Join-Path $work 'old'
    Get-Release $Previous $oldDir
    $oldSetup = (Get-ChildItem $oldDir -Filter 'unravis-setup-*.exe' | Select-Object -First 1).FullName
    $prog = Join-Path $env:LOCALAPPDATA 'Programs\Unravis'
    $appExe = Join-Path $prog 'unravis.exe'
    $keyFile = Join-Path $work 'readkey.txt'
    $cfg = Join-Path $work 'install.json'
    @{ scope = 'user'; startAtSignIn = $false
       blackbox = @{ name = 'verify'; folder = 'C:\UnravisBlackbox'; keepDays = 7; maxGB = 2; minFreePercent = 10; port = 7788 }
       readKeyFile = $keyFile } | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 -LiteralPath $cfg
    function Get-BbExe { $s = Get-CimInstance Win32_Service -Filter "Name='UnravisBlackbox'"; if (-not $s) { throw 'no UnravisBlackbox service' }; $s.PathName -replace '^"([^"]+)".*$', '$1' }
    function Get-BbVersion { ((& (Get-BbExe) version 2>&1 | Out-String).Trim() -split '\s+')[-1].TrimStart('v') }

    Step 'B1 previous release installs silently' {
        & $oldSetup --silent --config $cfg --log (Join-Path $work 'old-install.log') | Out-Host
        Expect ($LASTEXITCODE -eq 0) "install exit $LASTEXITCODE"
        Expect ((Get-Service UnravisBlackbox).Status -eq 'Running') 'service not running'
    }
    Step 'B2 the old app sees the new release on the real feed' {
        $o = (& $appExe --check-update | Out-String).Trim(); $code = $LASTEXITCODE
        Write-Host $o
        Expect ($code -eq 0) "--check-update exit $code"
        $j = $o | ConvertFrom-Json
        Expect ($j.status -eq 'available' -and $j.latest -eq $ver) "status '$($j.status)', latest '$($j.latest)', expected available $ver"
    }
    Step 'B3 upgrade with the hash and version pin' {
        $sha = (Get-FileHash $newSetup -Algorithm SHA256).Hash.ToLowerInvariant()
        '{}' | Set-Content -Encoding utf8 -LiteralPath (Join-Path $work 'up.json')
        & $newSetup --silent --upgrade --config (Join-Path $work 'up.json') --expect-sha256 $sha --expect-version $ver --log (Join-Path $work 'upgrade.log') | Out-Host
        Expect ($LASTEXITCODE -eq 0) "upgrade exit $LASTEXITCODE"
        Expect (((& $appExe --version | Out-String)) -match ('v' + [regex]::Escape($ver) + '(\s|$)')) 'app version is not the new one'
        Expect ((Get-BbVersion) -eq $ver) "recorder reports '$(Get-BbVersion)'"
        Expect ((Get-Service UnravisBlackbox).Status -eq 'Running') 'service not running after upgrade'
        $r = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:7788/' -TimeoutSec 15
        Expect ($r.StatusCode -eq 200) 'recorder did not answer'
    }
    Step 'B4 old setup cannot put the old version back (no downgrade)' {
        '{}' | Set-Content -Encoding utf8 -LiteralPath (Join-Path $work 'down.json')
        & $oldSetup --silent --upgrade --config (Join-Path $work 'down.json') --log (Join-Path $work 'downgrade.log') *> $null
        Expect ($LASTEXITCODE -ne 0) 'a downgrade was accepted'
        Expect ((Get-BbVersion) -eq $ver) 'the recorder version changed'
    }
    Step 'B5 standalone recorder updates itself with nobody there' {
        # Remove the Unravis install (keeps data); then install ONLY the previous recorder, with no managed marker.
        $un = Join-Path $work 'un.json'
        @{ uninstall = @{ deleteCases = $false; deleteRecordings = $false } } | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 -LiteralPath $un
        & $newSetup --uninstall --silent --config $un --log (Join-Path $work 'uninstall.log') | Out-Host
        Expect ($LASTEXITCODE -eq 0) "uninstall exit $LASTEXITCODE"
        $zip = Get-ChildItem $oldDir -Filter 'unravis-blackbox-*.zip' | Select-Object -First 1
        Expect ([bool]$zip) 'previous release has no recorder zip'
        # Setup's uninstall can leave this marker behind, and with it a recorder never updates itself (it is "managed by Unravis").
        Remove-Item -LiteralPath (Join-Path $env:ProgramFiles 'Unravis Blackbox\managed-by-unravis') -Force -ErrorAction SilentlyContinue
        $bbDir = Join-Path $work 'bb-old'; Expand-Archive -LiteralPath $zip.FullName -DestinationPath $bbDir
        $bbExe = (Get-ChildItem $bbDir -Recurse -Filter 'unravis-blackbox.exe' | Select-Object -First 1).FullName
        & $bbExe install --name verify --folder 'C:\UnravisBlackbox' --port 7788 *> (Join-Path $work 'bb-install.txt')   # read key goes to this file; never printed
        Expect ($LASTEXITCODE -eq 0) "recorder install exit $LASTEXITCODE"
        Expect ((Get-BbVersion) -eq $pver) "installed recorder is '$(Get-BbVersion)', expected $pver"
        $deadline = (Get-Date).AddMinutes($WaitMinutes)
        while ((Get-Date) -lt $deadline) { $cur = try { Get-BbVersion } catch { '' }; if ($cur -eq $ver) { break }; Start-Sleep -Seconds 20 }
        Expect ((Get-BbVersion) -eq $ver) "after $WaitMinutes minutes the recorder is still '$(Get-BbVersion)'"
        Start-Sleep -Seconds 10
        Expect ((Get-Service UnravisBlackbox).Status -eq 'Running') 'service not running after its own update'
    }
}

Remove-Item -LiteralPath (Join-Path $work 'bb-install.txt'), (Join-Path $work 'readkey.txt') -Force -ErrorAction SilentlyContinue   # both hold a read key
if ($failures.Count -gt 0) {
    Write-Host "`n$($failures.Count) step(s) FAILED:" -ForegroundColor Red
    $failures | ForEach-Object { Write-Host "  $_" -ForegroundColor Red }
    exit 1
}
Write-Host "`nAll steps passed for $Tag$(if ($Previous) { " (upgraded from $Previous)" } else { ' (first release: part A only)' })." -ForegroundColor Green
exit 0
