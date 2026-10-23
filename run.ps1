param(
    [switch]$Demo,
    [int]$Port = 18765,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'
$Executable = Join-Path $PSScriptRoot 'dist\api-audit-explorer.exe'
if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
    throw "API Audit Explorer is not built. Run .\build.ps1 first."
}

$Arguments = @('--port', [string]$Port)
if ($Demo) { $Arguments += '--demo' }
if ($NoBrowser) { $Arguments += '--no-browser' }
& $Executable @Arguments
if ($LASTEXITCODE -ne 0) { throw "API Audit Explorer exited with code $LASTEXITCODE." }
