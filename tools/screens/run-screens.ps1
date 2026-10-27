<#
.SYNOPSIS
  Takes the Unravis screenshots against dist\unravis.exe --demo and compares them with the saved ones.

.DESCRIPTION
  Starts the demo with a fixed clock and the capture-only cases, in a temporary folder that
  is deleted afterwards. Then it runs Playwright, which opens every screen in screens.json.

  Default: compare with buildout\screenshots. Any difference fails the run.
  -Update: write new pictures. Only the CI runner is the reference renderer, so do not
           commit pictures made on a developer PC unless the team agreed that PC is the reference.
  -Only:   one screen id, or several separated by commas (for example p1-home,p1-glossary).
#>
param(
    [switch]$Update,
    [string]$Only = '',
    [int]$Port = 0
)

$ErrorActionPreference = 'Stop'
$Here = $PSScriptRoot
$RepoRoot = (Resolve-Path -LiteralPath (Join-Path $Here '..\..')).Path
$Exe = Join-Path $RepoRoot 'dist\unravis.exe'

if (-not (Test-Path -LiteralPath $Exe -PathType Leaf)) {
    Write-Host 'dist\unravis.exe is missing. Build first: .\build.ps1' -ForegroundColor Red
    exit 2
}
if (-not (Test-Path -LiteralPath (Join-Path $Here 'node_modules\@playwright\test') -PathType Container)) {
    Write-Host 'The screenshot tools are not installed. In tools\screens run: npm ci, then npx playwright install chromium' -ForegroundColor Red
    exit 2
}

$DemoNow = '2026-10-20T09:00:00Z'
$Work = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-screens-' + [guid]::NewGuid().ToString('N'))
$OutLog = Join-Path $Work 'unravis.out.log'
$ErrLog = Join-Path $Work 'unravis.err.log'
$ConfigDir = Join-Path $Work 'config'
$Process = $null
$Saved = @{}
$Names = @('AUDIT_CONFIG_DIR', 'UNRAVIS_DEMO_NOW', 'UNRAVIS_DEMO_CASES', 'UNRAVIS_LAUNCH_URL', 'UNRAVIS_ONLY')
foreach ($n in $Names) { $Saved[$n] = [Environment]::GetEnvironmentVariable($n, 'Process') }
$Code = 1

try {
    New-Item -ItemType Directory -Path $ConfigDir -Force | Out-Null
    $env:AUDIT_CONFIG_DIR = $ConfigDir
    $env:UNRAVIS_DEMO_NOW = $DemoNow
    $env:UNRAVIS_DEMO_CASES = '1'
    $env:UNRAVIS_ONLY = $Only

    $Process = Start-Process -FilePath $Exe -ArgumentList @('--demo', '--no-browser', '--port', "$Port") `
        -RedirectStandardOutput $OutLog -RedirectStandardError $ErrLog -PassThru -WindowStyle Hidden

    # Wait for "listening at" and the one-time launch link (both go to the log).
    $Link = $null
    $Deadline = (Get-Date).AddSeconds(45)
    while ((Get-Date) -lt $Deadline -and -not $Link) {
        Start-Sleep -Milliseconds 200
        $Text = ''
        foreach ($f in @($OutLog, $ErrLog)) {
            if (Test-Path -LiteralPath $f) {
                $part = Get-Content -LiteralPath $f -Raw -ErrorAction SilentlyContinue
                if ($part) { $Text += $part }
            }
        }
        if ($Text -match 'listening at' -and $Text -match '(http://127\.0\.0\.1:\d+/launch\?c=[A-Za-z0-9_-]+)') { $Link = $Matches[1] }
        if ($Process.HasExited) { break }
    }
    if (-not $Link) {
        Write-Host 'Unravis did not start. Its log says:' -ForegroundColor Red
        foreach ($f in @($OutLog, $ErrLog)) {
            if (Test-Path -LiteralPath $f) { Get-Content -LiteralPath $f -Tail 15 | ForEach-Object { Write-Host ('  ' + $_) } }
        }
        exit 3
    }
    $env:UNRAVIS_LAUNCH_URL = $Link

    $Args2 = @('playwright', 'test')
    if ($Update) { $Args2 += '--update-snapshots' }
    Push-Location $Here
    # Node prints warnings on stderr. Windows PowerShell 5.1 would turn that into a stop, so only the exit code counts here.
    $ErrorActionPreference = 'Continue'
    try {
        & npx.cmd @Args2
        $Code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = 'Stop'
        Pop-Location
    }
} finally {
    if ($Process -and -not $Process.HasExited) {
        try { Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue } catch { }
        try { $Process.WaitForExit(5000) | Out-Null } catch { }
    }
    foreach ($n in $Names) { [Environment]::SetEnvironmentVariable($n, $Saved[$n], 'Process') }
    for ($i = 0; $i -lt 5 -and (Test-Path -LiteralPath $Work); $i++) {
        try { Remove-Item -LiteralPath $Work -Recurse -Force -ErrorAction Stop } catch { Start-Sleep -Milliseconds 400 }
    }
}
exit $Code
