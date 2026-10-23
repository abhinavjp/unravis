param(
    [string]$GoPath,
    [switch]$SkipWeb
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
    Invoke-Checked $GoExe @('build', '-o', (Join-Path $UtilityRoot 'dist\api-audit-explorer.exe'), './cmd/audit-explorer')
} finally {
    Pop-Location
}
