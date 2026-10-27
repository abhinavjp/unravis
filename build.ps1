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
} finally {
    Pop-Location
}
