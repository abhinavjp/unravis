<#
.SYNOPSIS
  Clean-machine smoke test for a release (installer-and-releases.md section 11). Runs in the
  "smoke" job of release.yml on fresh GitHub-hosted runners, which are clean every run.

.DESCRIPTION
  Always done:
    1. SHA256SUMS.txt is checked against every asset that was downloaded.
  Done only when the installer asset (unravis-setup-*-windows-x64.exe) is present, i.e. once
  cmd/unravis-setup (P11) exists:
    2. Silent install, user scope, with a Blackbox on 127.0.0.1:7788.
    3. The exe, the Add/Remove Programs entry and the UnravisBlackbox service exist.
    4. The Blackbox status page refuses an unkeyed read and answers a keyed one.
    5. unravis.exe --no-browser starts, logs a listening line, and is stopped again.
    6. Silent uninstall, keeping data; the service and exe are gone afterwards.
  Today cmd/unravis-setup is still on another branch, so steps 2-6 are skipped with a clear
  notice and the job still passes: there is nothing yet to smoke test.

  Not yet covered here (left for when the pieces they need exist): the authenticated
  /api/version check and the Playwright UI smoke (both need the browser-fragment launch token,
  section 3), the upgrade test against the previous published release, and the
  delete-data uninstall variant. Section 11 lists all of them; add them alongside the P11/P13
  work that makes them possible.

.PARAMETER AssetDir
  Folder the downloaded release assets (and SHA256SUMS.txt) live in.

.PARAMETER Tag
  The tag being smoke tested, e.g. v1.0.0. Used only in the log file name and messages.
#>
param(
    [Parameter(Mandatory)] [string]$AssetDir,
    [Parameter(Mandatory)] [string]$Tag
)

$ErrorActionPreference = 'Stop'
$AssetDir = (Resolve-Path -LiteralPath $AssetDir).Path
$LogPath = Join-Path $AssetDir "smoke-$($Tag -replace '[^0-9A-Za-z.\-]', '_').log"
$script:LogLines = [System.Collections.Generic.List[string]]::new()

function Write-Step {
    param([string]$Text)
    Write-Host $Text
    $script:LogLines.Add("$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')  $Text")
}

function Save-Log { $script:LogLines | Set-Content -Encoding utf8 -LiteralPath $LogPath }

function Get-SetupExitMeaning {
    param([int]$Code)
    switch ($Code) {
        1 { 'preflight failed' }
        2 { 'config invalid' }
        3 { 'Windows approval refused' }
        4 { 'rolled back or cancelled' }
        5 { 'finished with something left behind' }
        default { 'unknown' }
    }
}

try {
    # Step 1: verify SHA256SUMS.txt against what was actually downloaded.
    Write-Step "Checking SHA256SUMS.txt in $AssetDir"
    $sumsFile = Join-Path $AssetDir 'SHA256SUMS.txt'
    if (-not (Test-Path -LiteralPath $sumsFile -PathType Leaf)) { throw "Missing $sumsFile" }
    $checked = 0
    $listed = [System.Collections.Generic.HashSet[string]]::new()
    foreach ($line in Get-Content -LiteralPath $sumsFile) {
        if ($line.Trim() -eq '') { continue }
        $parts = $line -split '\s{2}', 2
        if ($parts.Count -ne 2) { throw "Malformed SHA256SUMS.txt line: $line" }
        $expected, $name = $parts
        [void]$listed.Add($name)
        $path = Join-Path $AssetDir $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "SHA256SUMS.txt lists '$name' but it was not downloaded" }
        $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected.ToLowerInvariant()) { throw "Checksum mismatch for $name : expected $expected, got $actual" }
        $checked++
    }
    foreach ($f in Get-ChildItem -LiteralPath $AssetDir -File | Where-Object { $_.Name -notlike 'smoke-*.log' -and $_.Name -notin @('SHA256SUMS.txt','ASSET-LIST.txt','RELEASE-NOTES.md') }) {
        if (-not $listed.Contains($f.Name)) { throw "$($f.Name) was downloaded but is not in SHA256SUMS.txt" }
    }
    Write-Step "All $checked asset(s) match SHA256SUMS.txt"

    # Steps 2-6 need the installer, which is not in every checkout yet (cmd/unravis-setup is P11).
    $installer = Get-ChildItem -LiteralPath $AssetDir -Filter 'unravis-setup-*-windows-x64.exe' -File -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $installer) {
        Write-Step 'No installer asset in this release (cmd/unravis-setup is not built yet). Skipping the install-based smoke steps; nothing else to check.'
        Save-Log
        exit 0
    }

    $Work = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-smoke-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $Work | Out-Null
    $keyFile = Join-Path $Work 'readkey.txt'
    $setupConfig = Join-Path $Work 'setup.json'
    $setupLog = Join-Path $Work 'setup.log'

    # Schema is internal/setup/silent.go (SilentConfig): readKeyFile is a top-level key, an absolute path that must not exist yet.
    @{
        scope          = 'user'
        location       = ''
        blackbox       = @{ name = 'smoke'; folder = 'C:\UnravisBlackbox'; keepDays = 7; maxGB = 2; minFreePercent = 10; port = 7788 }
        readKeyFile    = $keyFile
        firewall       = $null
        startAtSignIn  = $false
        openWhenDone   = $false
    } | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 -LiteralPath $setupConfig

    Write-Step "Installing silently with $installer"
    & $installer.FullName --silent --config $setupConfig --log $setupLog
    if ($LASTEXITCODE -ne 0) { throw "Silent install exited with code $LASTEXITCODE ($(Get-SetupExitMeaning $LASTEXITCODE)); see $setupLog" }

    $exePath = Join-Path $env:LOCALAPPDATA 'Programs\Unravis\unravis.exe'
    if (-not (Test-Path -LiteralPath $exePath -PathType Leaf)) { throw "Installed exe not found: $exePath" }
    Write-Step "Found $exePath"

    $uninstallKey = Get-ChildItem -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall' -ErrorAction SilentlyContinue |
        Get-ItemProperty -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq 'Unravis' } | Select-Object -First 1
    if (-not $uninstallKey) { throw 'No Add/Remove Programs entry named Unravis' }
    Write-Step 'Add/Remove Programs entry present'

    $service = Get-Service -Name 'UnravisBlackbox' -ErrorAction SilentlyContinue
    if (-not $service -or $service.Status -ne 'Running') { throw 'UnravisBlackbox service is missing or not running' }
    Write-Step 'UnravisBlackbox service is running'

    $readKey = (Get-Content -LiteralPath $keyFile -Raw).Trim()
    $unkeyed = Invoke-WebRequest -Uri 'http://127.0.0.1:7788/status' -SkipHttpErrorCheck -UseBasicParsing
    if ($unkeyed.StatusCode -eq 200) { throw 'Blackbox status answered without a read key' }
    $keyed = Invoke-WebRequest -Uri 'http://127.0.0.1:7788/status' -Headers @{ 'X-Blackbox-Key' = $readKey } -UseBasicParsing
    if ($keyed.StatusCode -ne 200) { throw "Blackbox status refused the read key (status $($keyed.StatusCode))" }
    Write-Step 'Blackbox status refuses an unkeyed read and answers a keyed one'

    Write-Step 'Starting unravis.exe --no-browser'
    $outLog = Join-Path $Work 'unravis.out.log'
    $errLog = Join-Path $Work 'unravis.err.log'
    $proc = Start-Process -FilePath $exePath -ArgumentList '--no-browser', '--port', '0' -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru -WindowStyle Hidden
    try {
        $deadline = (Get-Date).AddSeconds(20)
        $listening = $false
        while ((Get-Date) -lt $deadline) {
            $text = ''
            foreach ($f in @($outLog, $errLog)) { if (Test-Path -LiteralPath $f) { $text += (Get-Content -LiteralPath $f -Raw -ErrorAction SilentlyContinue) } }
            if ($text -match 'listening at') { $listening = $true; break }
            Start-Sleep -Milliseconds 250
        }
        if (-not $listening) { throw 'unravis.exe did not log a listening line within 20 seconds' }
        Write-Step 'unravis.exe started and is listening'
    } finally {
        if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    }

    Write-Step 'Uninstalling silently, keeping data'
    $uninstallConfig = Join-Path $Work 'uninstall.json'
    @{ uninstall = @{ deleteCases = $false; deleteRecordings = $false } } | ConvertTo-Json -Depth 5 |
        Set-Content -Encoding utf8 -LiteralPath $uninstallConfig
    & $installer.FullName --uninstall --silent --config $uninstallConfig --log (Join-Path $Work 'uninstall.log')
    if ($LASTEXITCODE -ne 0) { throw "Silent uninstall exited with code $LASTEXITCODE ($(Get-SetupExitMeaning $LASTEXITCODE)); see $(Join-Path $Work 'uninstall.log')" }
    if (Test-Path -LiteralPath $exePath -PathType Leaf) { throw 'unravis.exe is still present after uninstall' }
    if (Get-Service -Name 'UnravisBlackbox' -ErrorAction SilentlyContinue) { throw 'UnravisBlackbox service is still registered after uninstall' }
    Write-Step 'Uninstall removed the program and the service'

    Save-Log
    Write-Host "Smoke test passed for $Tag on $($env:COMPUTERNAME)."
    exit 0
} catch {
    Write-Step "FAILED: $($_.Exception.Message)"
    Save-Log
    throw
}
