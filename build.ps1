param(
    [string]$GoPath,
    [switch]$SkipWeb
)

$ErrorActionPreference = 'Stop'
$UtilityRoot = $PSScriptRoot
$WorkspaceRoot = (Resolve-Path (Join-Path $UtilityRoot '..\..')).Path

if ($GoPath) {
    if (-not (Test-Path -LiteralPath $GoPath -PathType Leaf)) { throw "Go executable not found: $GoPath" }
    $GoExe = (Resolve-Path -LiteralPath $GoPath).Path
} else {
    $GoCommand = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($GoCommand) {
        $GoExe = $GoCommand.Source
    } else {
        $GoExe = Join-Path $WorkspaceRoot '.dev\toolchains\go\bin\go.exe'
        if (-not (Test-Path -LiteralPath $GoExe -PathType Leaf)) {
            throw "Go was not found on PATH or at the workspace toolchain path: $GoExe"
        }
    }
}

$CacheRoot = Join-Path $WorkspaceRoot '.dev\toolchains\cache'
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
