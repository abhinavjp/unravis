param(
    [switch]$Demo,
    [int]$Port = 0,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'
$Executable = Join-Path $PSScriptRoot 'dist\unravis.exe'
if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
    throw "Unravis is not built. Run .\build.ps1 first."
}

$Arguments = @('--port', [string]$Port)
if ($Demo) { $Arguments += '--demo' }
if ($NoBrowser) { $Arguments += '--no-browser' }
& $Executable @Arguments
if ($LASTEXITCODE -ne 0) { throw "Unravis exited with code $LASTEXITCODE." }
