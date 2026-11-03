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
# unravis.exe is a windowless (GUI) program; & would not wait for it.
$quoted = $Arguments | ForEach-Object { '"{0}"' -f $_ }
$p = Start-Process -FilePath $Executable -ArgumentList $quoted -Wait -PassThru
if ($p.ExitCode -ne 0) { throw "Unravis exited with code $($p.ExitCode)." }
