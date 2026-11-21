param(
    [string]$GoPath,
    [switch]$SkipWeb,
    # Skip `go test ./...` for a caller that has already run it (CI does, in an earlier step). Default: tests run.
    [switch]$SkipTests,
    # Stamped into the program (shown by --version and /api/version). Default: git describe, else "dev".
    [string]$Version,
    # A release build: the app must be pinned to the exact unravis-blackbox.exe built here (SEC-FIX-01). The build fails if the
    # pin is empty or -Unpinned is also given. CI passes this.
    [switch]$Release,
    # Local development only: do not pin the app to the helper, so the helper can be rebuilt alone. The log says so loudly.
    [switch]$Unpinned
)

$ErrorActionPreference = 'Stop'
$UtilityRoot = $PSScriptRoot
if ($Release -and $Unpinned) { throw 'A release build must be pinned: use -Release or -Unpinned, not both.' }

# Walk up from the repo (including itself) to find a shared .dev\toolchains\go\bin\go.exe.
$ToolchainRoot = $null
$Probe = $UtilityRoot
while ($Probe) {
    if (Test-Path -LiteralPath (Join-Path $Probe '.dev\toolchains\go\bin\go.exe') -PathType Leaf) {
        $ToolchainRoot = $Probe
        break
    }
    $Probe = Split-Path -Parent $Probe
}

if ($GoPath) {
    if (-not (Test-Path -LiteralPath $GoPath -PathType Leaf)) { throw "Go executable not found: $GoPath" }
    $GoExe = (Resolve-Path -LiteralPath $GoPath).Path
} elseif ($env:AUDIT_EXPLORER_GO) {
    if (-not (Test-Path -LiteralPath $env:AUDIT_EXPLORER_GO -PathType Leaf)) { throw "AUDIT_EXPLORER_GO does not point to a file: $env:AUDIT_EXPLORER_GO" }
    $GoExe = (Resolve-Path -LiteralPath $env:AUDIT_EXPLORER_GO).Path
} else {
    $GoCommand = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($GoCommand) {
        $GoExe = $GoCommand.Source
    } elseif ($env:GOROOT -and (Test-Path -LiteralPath (Join-Path $env:GOROOT 'bin\go.exe') -PathType Leaf)) {
        $GoExe = Join-Path $env:GOROOT 'bin\go.exe'
    } elseif ($ToolchainRoot) {
        $GoExe = Join-Path $ToolchainRoot '.dev\toolchains\go\bin\go.exe'
    } else {
        throw "Go was not found: pass -GoPath, set AUDIT_EXPLORER_GO, put go.exe on PATH, set GOROOT, or provide .dev\toolchains\go\bin\go.exe in the repo or an ancestor directory."
    }
}

if ($ToolchainRoot) {
    $CacheRoot = Join-Path $ToolchainRoot '.dev\toolchains\cache'
} else {
    $CacheRoot = Join-Path $UtilityRoot '.dev\toolchains\cache'
}
$env:GOCACHE = Join-Path $CacheRoot 'build'
$env:GOMODCACHE = Join-Path $CacheRoot 'mod'
$env:GOPATH = Join-Path $CacheRoot 'gopath'
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOPATH | Out-Null

# Version, commit and date for -ldflags. A missing git or a folder that is not a clone gives "dev".
function Get-GitText {
    param([string[]]$GitArgs)
    $git = Get-Command git.exe -ErrorAction SilentlyContinue
    if (-not $git) { return '' }
    try {
        $text = & $git.Source -C $UtilityRoot @GitArgs 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $text) { return '' }
        return ([string]($text | Select-Object -First 1)).Trim()
    } catch { return '' }
}
if (-not $Version) { $Version = Get-GitText @('describe', '--tags', '--always', '--dirty') }
if (-not $Version) { $Version = 'dev' }
if ($Version -notmatch '^[0-9A-Za-z][0-9A-Za-z._+-]*$') { throw "Version may only use letters, digits and . _ + - : $Version" }
$Commit = Get-GitText @('rev-parse', 'HEAD')
$BuildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-dd')
$VersionPackage = 'api-audit-explorer/internal/version'
$LdFlags = "-X $VersionPackage.Version=$Version -X $VersionPackage.Commit=$Commit -X $VersionPackage.Date=$BuildDate"
# The app is a windowless (GUI) program: no console window opens when it is double-clicked.
$GuiLdFlags = "$LdFlags -H=windowsgui"

function Get-PeSubsystem([string]$Path) {
    # PE layout: the int32 at 0x3C is the offset of the 'PE' header; the optional header starts 24 bytes in,
    # and its Subsystem field is 68 bytes into that. 2 = Windows GUI, 3 = Windows console.
    $Stream = [System.IO.File]::OpenRead($Path)
    try {
        $Reader = New-Object System.IO.BinaryReader($Stream)
        $Stream.Position = 0x3C
        $PeOffset = $Reader.ReadInt32()
        $Stream.Position = $PeOffset + 24 + 68
        return [int]$Reader.ReadUInt16()
    } finally {
        $Stream.Dispose()
    }
}

function Invoke-Checked {
    param([string]$Executable, [string[]]$CommandArgs)
    & $Executable @CommandArgs
    if ($LASTEXITCODE -ne 0) { throw "Command failed with exit code ${LASTEXITCODE}: $Executable $($CommandArgs -join ' ')" }
}

if (-not $SkipWeb) {
    # `npm ci` deletes web\node_modules before it installs. In a git worktree that folder can be a
    # junction to another checkout's, and the delete would empty the folder that checkout (and every
    # other worktree) uses. So stop before npm runs; never follow the link.
    $NodeModules = Join-Path $UtilityRoot 'web\node_modules'
    $NodeModulesItem = Get-Item -LiteralPath $NodeModules -Force -ErrorAction SilentlyContinue
    if ($NodeModulesItem -and ($NodeModulesItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        throw 'web\node_modules is a junction to another checkout. npm ci would empty it for every checkout that shares it. Run "npm run build" in web, then .\build.ps1 -SkipWeb, or remove the junction yourself first.'
    }
    Push-Location (Join-Path $UtilityRoot 'web')
    try {
        Invoke-Checked 'npm.cmd' @('ci')
        Invoke-Checked 'npm.cmd' @('run', 'check')
        Invoke-Checked 'npm.cmd' @('run', 'build')
    } finally {
        Pop-Location
    }
}

Push-Location $UtilityRoot
try {
    if (-not $SkipTests) { Invoke-Checked $GoExe @('test', './...') }
    Invoke-Checked $GoExe @('vet', './...')
    New-Item -ItemType Directory -Force -Path (Join-Path $UtilityRoot 'dist') | Out-Null

    # SEC-FIX-01: the Blackbox helper is built FIRST and the app is pinned to its SHA-256, so the app only ever starts
    # (elevated) the exact helper it was built with. The same bytes go into the installer payload and, in a release, into
    # the standalone zip (build-blackbox.ps1 -Reuse). The helper itself is never pinned (it would depend on its own hash).
    $HelperExe = Join-Path $UtilityRoot 'dist\unravis-blackbox.exe'
    $HelperVersion = $Version.TrimStart('v')
    $HelperLdFlags = "-s -w $LdFlags -X main.version=$HelperVersion"
    Invoke-Checked $GoExe @('build', '-trimpath', '-ldflags', $HelperLdFlags, '-o', $HelperExe, './cmd/unravis-blackbox')
    $HelperSha = (Get-FileHash -LiteralPath $HelperExe -Algorithm SHA256).Hash.ToLowerInvariant()
    $AppLdFlags = $GuiLdFlags
    if ($Unpinned) {
        Write-Warning 'UNPINNED DEV BUILD: unravis.exe is NOT pinned to unravis-blackbox.exe, so it accepts any helper that is signed or unsigned-but-untampered. Never ship this build. Run without -Unpinned for a pinned build.'
    } else {
        if ($HelperSha -notmatch '^[0-9a-f]{64}$') { throw "The helper hash is not a SHA-256 (got '$HelperSha'); refusing to build the app without a pin." }
        $AppLdFlags = "$GuiLdFlags -X api-audit-explorer/internal/blackbox.PinnedHelperSHA256=$HelperSha"
        Write-Host "Pinning unravis.exe to unravis-blackbox.exe SHA-256 $HelperSha"
    }
    if ($Release -and -not $HelperSha) { throw 'Release build: the helper hash is empty, so the app cannot be pinned.' }
    Invoke-Checked $GoExe @('build', '-ldflags', $AppLdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis.exe'), './cmd/audit-explorer')
    if (-not $Unpinned) {
        # The pin must really be inside the program: the hash text appears in its bytes.
        $AppBytes = [System.IO.File]::ReadAllBytes((Join-Path $UtilityRoot 'dist\unravis.exe'))
        $AppText = [System.Text.Encoding]::GetEncoding(28591).GetString($AppBytes)
        if (-not $AppText.Contains($HelperSha)) { throw 'unravis.exe does not contain the helper pin. The -ldflags -X for PinnedHelperSHA256 did not take effect.' }
    }
    # Compatibility copy for MCP client configs that still point at dist\api-audit-explorer.exe.
    # Remove this copy and the MCP.md note after 2026-12-31 (one release after the rename).
    Copy-Item -LiteralPath (Join-Path $UtilityRoot 'dist\unravis.exe') -Destination (Join-Path $UtilityRoot 'dist\api-audit-explorer.exe') -Force
    Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-keeper.exe'), './cmd/unravis-keeper')
    # The releases window (read-only release status, its own icon). A GUI program like the app, so it opens no console; it ships inside the setup program and updates with it.
    Invoke-Checked $GoExe @('build', '-trimpath', '-ldflags', $GuiLdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-releases.exe'), './cmd/unravis-releases')

    # The one-file installer comes last: it embeds the programs above with a manifest of their SHA-256, so setup refuses a
    # payload that was changed after the build. The payload folder is cleared first and is not committed (see its README.txt).
    # Nothing is signed here; signing is a release step (docs: installer-and-releases.md).
    $PayloadDir = Join-Path $UtilityRoot 'cmd\unravis-setup\payload'
    Get-ChildItem -LiteralPath $PayloadDir -Force | Where-Object { $_.Name -notin @('README.txt', '.gitignore') } | Remove-Item -Recurse -Force
    $PayloadFiles = @()
    foreach ($Name in @('unravis.exe', 'unravis-keeper.exe', 'unravis-blackbox.exe', 'unravis-releases.exe')) {
        $Source = Join-Path $UtilityRoot "dist\$Name"
        Copy-Item -LiteralPath $Source -Destination (Join-Path $PayloadDir $Name) -Force
        $PayloadSha = (Get-FileHash -LiteralPath $Source -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($Name -eq 'unravis-blackbox.exe' -and -not $Unpinned -and $PayloadSha -ne $HelperSha) { throw 'The helper in the payload is not the one the app is pinned to.' }
        $PayloadFiles += [ordered]@{
            name   = $Name
            sha256 = $PayloadSha
            size   = (Get-Item -LiteralPath $Source).Length
        }
    }
    $Manifest = [ordered]@{ version = $Version; files = $PayloadFiles } | ConvertTo-Json -Depth 4
    [System.IO.File]::WriteAllText((Join-Path $PayloadDir 'manifest.json'), $Manifest, (New-Object System.Text.UTF8Encoding($false)))
    try {
        Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-setup.exe'), './cmd/unravis-setup')
        # Unravis Lite: a small console viewer for one Blackbox (docs/auto-updater/SPEC.md). It stays a console program so it can print the one-time link.
        Invoke-Checked $GoExe @('build', '-trimpath', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-lite.exe'), './cmd/unravis-lite')
        # Subsystem check: the app must be GUI (2) so it opens no console; every other program built here stays console (3).
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis.exe')
        if ($Subsystem -ne 2) { throw "unravis.exe: PE subsystem $Subsystem, expected 2" }
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis-releases.exe')
        if ($Subsystem -ne 2) { throw "unravis-releases.exe: PE subsystem $Subsystem, expected 2" }
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis-keeper.exe')
        if ($Subsystem -ne 3) { throw "unravis-keeper.exe: PE subsystem $Subsystem, expected 3" }
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis-blackbox.exe')
        if ($Subsystem -ne 3) { throw "unravis-blackbox.exe: PE subsystem $Subsystem, expected 3" }
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis-setup.exe')
        if ($Subsystem -ne 3) { throw "unravis-setup.exe: PE subsystem $Subsystem, expected 3" }
        $Subsystem = Get-PeSubsystem (Join-Path $UtilityRoot 'dist\unravis-lite.exe')
        if ($Subsystem -ne 3) { throw "unravis-lite.exe: PE subsystem $Subsystem, expected 3" }
    } finally {
        # The programs stay in dist; the copies in the source tree go, so a later `go test` does not carry them.
        Get-ChildItem -LiteralPath $PayloadDir -Force | Where-Object { $_.Name -notin @('README.txt', '.gitignore') } | Remove-Item -Recurse -Force
    }
} finally {
    Pop-Location
}
