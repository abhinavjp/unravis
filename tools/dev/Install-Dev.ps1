<#
.SYNOPSIS
  One command to install or update Unravis (and its Blackbox recorder) on this PC. One Windows admin prompt, no pasted commands.

.DESCRIPTION
  Works in Windows PowerShell 5.1 and PowerShell 7. Start it as a normal user (NOT as administrator): the setup program asks for
  administrator approval itself, once, when it needs it.

  Where the setup program comes from:
    -FromBuild           build this checkout now (build.ps1 -Release, pinned), then use dist\unravis-setup.exe   [default]
    -FromRelease <tag>   download that GitHub release (gh must be signed in), check it with "unravis-setup.exe --verify"
                         (signature, expiry, sizes, hashes) and use its setup program.
  On an upgrade the setup program is started with --expect-sha256 and --expect-version, so it refuses to run if the file changed
  or is a different version (setup accepts these two flags for --upgrade only).

  What it does, in order:
    1. gets the setup program (build or download + verify)
    2. looks for an existing install (setup-state.json in Program Files or in your user folder)
    3. backs up the settings and cases it can find (see -NoBackup) to %LOCALAPPDATA%\Unravis\dev-backups\<time>
    4. asks a running Unravis to quit (setup never closes it in silent mode)
    5. runs unravis-setup.exe --silent: --upgrade when an install exists, a fresh install otherwise.
       This is where Windows asks for administrator approval, once. Setup replaces Unravis AND the Blackbox recorder,
       keeps all settings and recordings, and puts everything back if a step fails.
    6. checks the result: program version, Blackbox program version, Blackbox service state and status answer.
    7. prints a short result and writes a log (path printed). The Blackbox read key is never printed.

  First install with a Blackbox:  -WithBlackbox  (name, recordings folder and port have defaults; the read key goes to -ReadKeyFile).

.EXAMPLE
  .\tools\dev\Install-Dev.ps1                        # build this checkout and install or upgrade it
  .\tools\dev\Install-Dev.ps1 -FromRelease v0.1.0-rc.49
  .\tools\dev\Install-Dev.ps1 -DryRun                # build/download and verify, show what would happen, change nothing
#>
[CmdletBinding()]
param(
    [switch]$FromBuild,
    [string]$FromRelease,
    [string]$Repo = 'abhinavjp/unravis',
    [switch]$SkipTests,
    [switch]$SkipWeb,
    [switch]$NoBackup,
    [switch]$DryRun,
    # First install only
    [ValidateSet('machine', 'user')] [string]$Scope = 'machine',
    [switch]$WithBlackbox,
    [string]$BlackboxName = $env:COMPUTERNAME,
    [string]$BlackboxFolder,
    [int]$BlackboxPort = 7788,
    [string]$ReadKeyFile = (Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'unravis-blackbox-readkey.txt')
)

$ErrorActionPreference = 'Stop'
if ($FromBuild -and $FromRelease) { throw 'Use -FromBuild or -FromRelease, not both.' }
if ($FromRelease -and $FromRelease -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { throw "Release '$FromRelease' is not vX.Y.Z or vX.Y.Z-pre.N." }

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$base = Join-Path $env:LOCALAPPDATA 'Unravis'
$logDir = Join-Path $base 'dev-install-logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$log = Join-Path $logDir "install-dev-$stamp.log"
Start-Transcript -Path $log | Out-Null

function Say([string]$m) { Write-Host $m }
# Where an Unravis install may be: the folder Windows records for it (as setup itself looks), then the two default folders.
function Get-InstallDirs {
    $dirs = New-Object System.Collections.Generic.List[string]
    foreach ($k in @('HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Unravis', 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Unravis')) {
        $loc = (Get-ItemProperty -LiteralPath $k -ErrorAction SilentlyContinue).InstallLocation
        if ($loc) { $dirs.Add($loc.TrimEnd('\')) }
    }
    $dirs.Add((Join-Path $env:ProgramW6432 'Unravis')); $dirs.Add((Join-Path $env:LOCALAPPDATA 'Programs\Unravis'))
    $dirs | Select-Object -Unique
}
function Fail([string]$m) { Write-Host "FAILED: $m" -ForegroundColor Red; Say "Log: $log"; try { Stop-Transcript | Out-Null } catch {}; exit 1 }

try {
    # Starting elevated would make the browser, the build and the files all elevated. Setup asks for approval itself.
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    if ((New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail 'Start this as a normal user, not as administrator. Setup asks for administrator approval itself, once.'
    }

    # 1. the setup program ---------------------------------------------------------------------------------------------------
    $work = Join-Path ([IO.Path]::GetTempPath()) "unravis-dev-$stamp"
    $setupExe = $null
    if ($FromRelease) {
        Say "1/6 Downloading release $FromRelease"
        if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Fail 'gh (GitHub CLI) is needed for -FromRelease and must be signed in.' }
        New-Item -ItemType Directory -Force -Path $work | Out-Null
        & gh release download $FromRelease --repo $Repo --dir $work --pattern 'unravis-setup-*.exe' --pattern 'update-manifest.json' --pattern 'update-manifest.json.sig'
        if ($LASTEXITCODE -ne 0) { Fail "Could not download $FromRelease (is it published and are you signed in to gh?)." }
        $setupExe = (Get-ChildItem -LiteralPath $work -Filter 'unravis-setup-*.exe' | Select-Object -First 1).FullName
        if (-not $setupExe) { Fail 'The release has no unravis-setup-*.exe.' }
        # The chosen file must be the one the manifest lists (an extra unlisted file is never run).
        $mf = Get-Content -Raw -LiteralPath (Join-Path $work 'update-manifest.json') | ConvertFrom-Json
        $listed = $mf.assets.setup
        if (-not $listed -or $listed.name -ne (Split-Path -Leaf $setupExe) -or $listed.sha256 -ne (Get-FileHash -LiteralPath $setupExe -Algorithm SHA256).Hash.ToLowerInvariant()) {
            Fail 'The downloaded setup program is not the file the manifest lists (name or SHA-256 differs).'
        }
        # Prefer an already installed setup (a program you trusted earlier) to check the folder; a fresh PC uses the downloaded one.
        $checker = $setupExe
        foreach ($d in @((Join-Path $env:ProgramW6432 'Unravis'), (Join-Path $env:LOCALAPPDATA 'Programs\Unravis'))) {
            $c = Join-Path $d 'unravis-setup.exe'
            if (Test-Path -LiteralPath $c) { $checker = $c; break }
        }
        if ($checker -eq $setupExe) { Say '    Note: no earlier install to check with, so the downloaded setup checks its own release (its built-in keys decide).' }
        Say '    Checking signature, expiry and hashes (unravis-setup --verify)'
        & $checker --verify $work
        if ($LASTEXITCODE -ne 0) { Fail "The release folder did not verify (exit $LASTEXITCODE). Nothing was installed. There is no way to skip this check." }
    } else {
        Say '1/6 Building this checkout (pinned release build)'
        # A hashtable, not an array: splatting an array passes "-Release" as a positional value (it became build.ps1's -GoPath).
        $buildArgs = @{ Release = $true }
        if ($SkipTests) { $buildArgs.SkipTests = $true }
        if ($SkipWeb) { $buildArgs.SkipWeb = $true }
        $setupExe = Join-Path $repoRoot 'dist\unravis-setup.exe'
        Remove-Item -LiteralPath $setupExe -Force -ErrorAction SilentlyContinue   # never install a leftover from an earlier build
        Push-Location $repoRoot
        try { & (Join-Path $repoRoot 'build.ps1') @buildArgs } finally { Pop-Location }
        if (-not (Test-Path -LiteralPath $setupExe)) { Fail "The build did not make $setupExe." }
    }
    $sha = (Get-FileHash -LiteralPath $setupExe -Algorithm SHA256).Hash.ToLowerInvariant()
    $verOut = (& $setupExe --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $verOut) { Fail 'The setup program did not report a version.' }
    $newVersion = ($verOut -split '\s+' | Where-Object { $_ -match '^v?[0-9]+\.[0-9]+\.[0-9]+' } | Select-Object -First 1)
    if ($newVersion) { $newVersion = $newVersion.TrimStart('v') } else { $newVersion = $null }
    Say "    Setup: $setupExe"
    Say "    Version: $verOut"
    Say "    SHA-256: $sha"

    # 2. what is installed ---------------------------------------------------------------------------------------------------
    Say '2/6 Looking for an existing install'
    $state = $null; $stateDir = $null
    foreach ($d in (Get-InstallDirs)) {
        $f = Join-Path $d 'setup-state.json'
        if (Test-Path -LiteralPath $f) {
            try { $state = Get-Content -Raw -LiteralPath $f | ConvertFrom-Json; $stateDir = $d; break }
            catch { Fail "Found $f but cannot read it: $($_.Exception.Message). Run the setup window once (unravis-setup.exe) to repair." }
        }
    }
    $bbSvc = Get-Service -Name 'UnravisBlackbox' -ErrorAction SilentlyContinue
    if ($state) {
        Say "    Installed: Unravis $($state.version) ($($state.scope)) in $stateDir; Blackbox: $(if ($state.blackbox) { 'yes' } else { 'no' })"
        $op = 'upgrade'
        if ($WithBlackbox -and -not $state.blackbox) { Fail 'Unravis is installed without a Blackbox. Adding one is done in the setup window (Repair), not here. Nothing was changed.' }
    } else {
        if ($bbSvc) { Fail 'A Blackbox service (UnravisBlackbox) exists but no Unravis install owns it. This script will not guess. Install via the setup window first (it explains what it will do), or remove that Blackbox. Nothing was changed.' }
        Say '    No install found: this will be a fresh install.'
        $op = 'install'
    }

    # 3. backup --------------------------------------------------------------------------------------------------------------
    if (-not $NoBackup -and $state) {
        $bk = Join-Path $base "dev-backups\$stamp"
        $src = @()
        if ($state.sharedRoot) { $src += $state.sharedRoot }
        # A user-scope install keeps its cases and settings under these two folders (internal/datadir).
        foreach ($f in @((Join-Path $env:APPDATA 'APIAuditExplorer'), (Join-Path $env:LOCALAPPDATA 'APIAuditExplorer'))) { if (Test-Path -LiteralPath $f) { $src += $f } }
        $skipped = 0; $copied = 0
        Say "3/6 Backup of settings and cases to $bk"
        if (-not $DryRun) {
            New-Item -ItemType Directory -Force -Path $bk | Out-Null
            Copy-Item -LiteralPath (Join-Path $stateDir 'setup-state.json') -Destination $bk -Force
            foreach ($s in $src) {
                if (-not (Test-Path -LiteralPath $s)) { continue }
                # Recordings can be huge: copy small files only (json, db, sealed), skip anything over 50 MB.
                $dest = Join-Path $bk (($s -replace '[:\\/]+', '_').Trim('_'))
                New-Item -ItemType Directory -Force -Path $dest | Out-Null
                Get-ChildItem -LiteralPath $s -Recurse -File -ErrorAction SilentlyContinue | Where-Object { $_.Length -le 50MB } | ForEach-Object {
                    $rel = $_.FullName.Substring($s.Length).TrimStart('\')
                    $to = Join-Path $dest $rel
                    try {
                        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $to) | Out-Null
                        Copy-Item -LiteralPath $_.FullName -Destination $to -Force; $copied++
                    } catch { $skipped++; Say "    skipped (in use, no access or path too long): $rel" }
                }
            }
            Say "    Copied $copied file(s); skipped $skipped. Files over 50 MB (recordings) are not copied; setup never touches recordings."
            if ($skipped -gt 0) { Say '    Some files could not be copied (see above). Setup keeps all data and undoes itself on a failure; continue only if that is acceptable.' }
        }
    } else { Say '3/6 Backup skipped' }

    if ($DryRun) {
        Say "DRY RUN: would now ask Unravis to quit and run: unravis-setup.exe --silent --$op ..."
        Say "Log: $log"; Stop-Transcript | Out-Null; exit 0
    }

    # 4. ask a running Unravis to quit --------------------------------------------------------------------------------------
    Say '4/6 Asking a running Unravis to quit'
    $appExe = if ($stateDir) { Join-Path $stateDir 'unravis.exe' } else { $null }
    if ($appExe -and (Test-Path -LiteralPath $appExe)) {
        $eap = $ErrorActionPreference; $ErrorActionPreference = 'Continue'   # Windows PowerShell 5.1 turns native stderr into errors under Stop
        & $appExe --quit 2>$null | Out-Null   # 0 = quit sent, or it was not running
        $ErrorActionPreference = $eap
        $deadline = (Get-Date).AddSeconds(20)
        while ((Get-Date) -lt $deadline -and (Get-Process -Name 'unravis' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $appExe })) { Start-Sleep -Milliseconds 500 }
        if (Get-Process -Name 'unravis' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $appExe }) { Fail 'Unravis is still running. Choose Quit Unravis from the tray icon and run this again. Nothing was changed.' }
    }

    # 5. setup ---------------------------------------------------------------------------------------------------------------
    Say '5/6 Running setup. Windows will ask for administrator approval once; please approve it.'
    $cfgFile = Join-Path $work 'setup.json'
    New-Item -ItemType Directory -Force -Path $work | Out-Null
    if ($op -eq 'upgrade') {
        '{}' | Set-Content -LiteralPath $cfgFile -Encoding UTF8
    } else {
        $cfg = [ordered]@{ scope = $Scope; startAtSignIn = $true }
        if ($WithBlackbox) {
            if (Test-Path -LiteralPath $ReadKeyFile) { Fail "$ReadKeyFile already exists; setup never replaces a file. Pass -ReadKeyFile with a new path." }
            if (-not $BlackboxFolder) { $BlackboxFolder = Join-Path $env:SystemDrive 'UnravisBlackbox' }
            $cfg.blackbox = [ordered]@{ name = $BlackboxName; folder = $BlackboxFolder; keepDays = 7; maxGB = 2; minFreePercent = 10; port = $BlackboxPort }
            $cfg.readKeyFile = $ReadKeyFile
        }
        ($cfg | ConvertTo-Json -Depth 5) | Set-Content -LiteralPath $cfgFile -Encoding UTF8
    }
    $setupLog = Join-Path $logDir "setup-$stamp.log"
    $a = @('--silent', '--config', $cfgFile, '--log', $setupLog)
    if ($op -eq 'upgrade') {
        $a += '--upgrade'
        # Setup accepts these two only for --upgrade, and only together, with an exact semantic version (1.2.3 or 1.2.3-rc.4).
        if ($newVersion -match '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z.]+)?$') { $a += @('--expect-sha256', $sha, '--expect-version', $newVersion) }
        else { Say "    Note: version '$newVersion' is not a plain release version, so the hash/version pin is not passed to setup." }
    }
    & $setupExe @a
    $code = $LASTEXITCODE
    $meaning = switch ($code) {
        0 { 'Done.' } 1 { 'A check failed. Nothing was changed.' } 2 { 'The command line or setup.json is wrong. Nothing was changed.' }
        3 { 'The Windows approval was refused. Nothing was changed.' } 4 { 'A step failed and everything was put back.' }
        default { 'Anything else, including items that could not be put back. Read the setup log.' }
    }
    Say "    Setup exit code $code : $meaning"
    if ($code -ne 0) { Fail "Setup did not finish. $meaning Setup log: $setupLog" }

    # 6. check the result ---------------------------------------------------------------------------------------------------
    Say '6/6 Checking the result'
    $st = $null
    foreach ($d in (Get-InstallDirs)) {
        $f = Join-Path $d 'setup-state.json'
        if (Test-Path -LiteralPath $f) { $st = Get-Content -Raw -LiteralPath $f | ConvertFrom-Json; $stateDir = $d; break }
    }
    $problems = @()
    if (-not $st) { $problems += 'setup-state.json is missing after setup.' }
    else {
        $installed = (& (Join-Path $stateDir 'unravis.exe') --version | Out-String).Trim()
        Say "    Unravis program: $installed"
        if ($newVersion -and ([string]$st.version).TrimStart('v') -ne $newVersion) { $problems += "Recorded version is $($st.version), expected $newVersion." }
        if ($st.blackbox) {
            $svc = $null
            $until = (Get-Date).AddSeconds(30)   # setup returns before the service has finished starting
            do { $svc = Get-CimInstance Win32_Service -Filter "Name='UnravisBlackbox'" -ErrorAction SilentlyContinue; if ($svc -and $svc.State -eq 'Running') { break }; Start-Sleep -Seconds 2 } while ((Get-Date) -lt $until)
            if (-not $svc) { $problems += 'The Blackbox service is not installed.' }
            else {
                $exe = ($svc.PathName -replace '^"([^"]+)".*$', '$1')
                $bbVer = (& $exe version | Out-String).Trim()
                Say "    Blackbox program: $bbVer"
                Say "    Blackbox service: $($svc.State)"
                if ($svc.State -ne 'Running') { $problems += "Blackbox service is $($svc.State), expected Running." }
                if ($newVersion -and $bbVer -notmatch [regex]::Escape($newVersion)) { $problems += "Blackbox program reports '$bbVer', expected $newVersion." }
                $port = if ($st.blackbox.port) { $st.blackbox.port } else { 7788 }
                $r = $null; $err = $null
                $until = (Get-Date).AddSeconds(30)
                do {
                    try { $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/" -TimeoutSec 5; $err = $null; break } catch { $err = $_.Exception.Message; Start-Sleep -Seconds 2 }
                } while ((Get-Date) -lt $until)
                if ($err) { $problems += "The Blackbox did not answer on 127.0.0.1:$port ($err)." }
                else { Say "    Blackbox answers: $(($r.Content -split "`n")[0].Trim())" }
            }
        }
    }
    if ($problems.Count -gt 0) { $problems | ForEach-Object { Write-Host "    PROBLEM: $_" -ForegroundColor Yellow }; Fail 'Setup finished but the check found problems (above).' }
    Write-Host 'OK: everything is installed and running.' -ForegroundColor Green
    if ($op -eq 'install' -and $WithBlackbox) { Say "The Blackbox read key was written to $ReadKeyFile (readable by you only). Keep it safe." }
    Say "Log: $log"
    Say "Setup log: $setupLog"
    Stop-Transcript | Out-Null
    exit 0
} catch {
    Write-Host "FAILED: $($_.Exception.Message)" -ForegroundColor Red
    Say "Log: $log"
    try { Stop-Transcript | Out-Null } catch {}
    exit 1
}
