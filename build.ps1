param(
    [string]$GoPath,
    [switch]$SkipWeb,
    # Stamped into the program (shown by --version and /api/version). Default: git describe, else "dev".
    [string]$Version
)

$ErrorActionPreference = 'Stop'
$UtilityRoot = $PSScriptRoot

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
    Invoke-Checked $GoExe @('test', './...')
    Invoke-Checked $GoExe @('vet', './...')
    New-Item -ItemType Directory -Force -Path (Join-Path $UtilityRoot 'dist') | Out-Null
    Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis.exe'), './cmd/audit-explorer')
    # Compatibility copy for MCP client configs that still point at dist\api-audit-explorer.exe.
    # Remove this copy and the MCP.md note after 2026-12-31 (one release after the rename).
    Copy-Item -LiteralPath (Join-Path $UtilityRoot 'dist\unravis.exe') -Destination (Join-Path $UtilityRoot 'dist\api-audit-explorer.exe') -Force
    Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-keeper.exe'), './cmd/unravis-keeper')
    Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-blackbox.exe'), './cmd/unravis-blackbox')

    # The one-file installer comes last: it embeds the programs above with a manifest of their SHA-256, so setup refuses a
    # payload that was changed after the build. The payload folder is cleared first and is not committed (see its README.txt).
    # Nothing is signed here; signing is a release step (docs: installer-and-releases.md).
    $PayloadDir = Join-Path $UtilityRoot 'cmd\unravis-setup\payload'
    Get-ChildItem -LiteralPath $PayloadDir -Force | Where-Object { $_.Name -ne 'README.txt' } | Remove-Item -Recurse -Force
    $PayloadFiles = @()
    foreach ($Name in @('unravis.exe', 'unravis-keeper.exe', 'unravis-blackbox.exe')) {
        $Source = Join-Path $UtilityRoot "dist\$Name"
        Copy-Item -LiteralPath $Source -Destination (Join-Path $PayloadDir $Name) -Force
        $PayloadFiles += [ordered]@{
            name   = $Name
            sha256 = (Get-FileHash -LiteralPath $Source -Algorithm SHA256).Hash.ToLowerInvariant()
            size   = (Get-Item -LiteralPath $Source).Length
        }
    }
    $Manifest = [ordered]@{ version = $Version; files = $PayloadFiles } | ConvertTo-Json -Depth 4
    [System.IO.File]::WriteAllText((Join-Path $PayloadDir 'manifest.json'), $Manifest, (New-Object System.Text.UTF8Encoding($false)))
    try {
        Invoke-Checked $GoExe @('build', '-ldflags', $LdFlags, '-o', (Join-Path $UtilityRoot 'dist\unravis-setup.exe'), './cmd/unravis-setup')
    } finally {
        # The programs stay in dist; the copies in the source tree go, so a later `go test` does not carry them.
        Get-ChildItem -LiteralPath $PayloadDir -Force | Where-Object { $_.Name -ne 'README.txt' } | Remove-Item -Recurse -Force
    }
} finally {
    Pop-Location
}
