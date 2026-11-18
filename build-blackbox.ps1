param(
    [string]$Version = 'dev',
    [string]$GoPath,
    [string]$OutDir,
    # Pack the dist\unravis-blackbox.exe that build.ps1 already built, instead of building it again. A release uses this so the
    # zip holds the same bytes the app is pinned to (SEC-FIX-01).
    [switch]$Reuse
)

# Builds the Blackbox recorder and its pack:
#   dist\unravis-blackbox.exe
#   dist\unravis-blackbox-v<Version>-windows-x64.zip  (exe, blackbox.json, README.txt)
# The pack must stay under 8 MB. Nothing is installed or run.
# A standalone build here is NOT the helper that Unravis (unravis.exe) is pinned to: the app refuses to start it elevated
# when the app was pinned by build.ps1. Use -Reuse after build.ps1 to pack the pinned one.

$ErrorActionPreference = 'Stop'
$Root = $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $Root 'dist' }
$MaxZipBytes = 8MB

$ToolchainRoot = $null
$Probe = $Root
while ($Probe) {
    if (Test-Path -LiteralPath (Join-Path $Probe '.dev\toolchains\go\bin\go.exe') -PathType Leaf) { $ToolchainRoot = $Probe; break }
    $Probe = Split-Path -Parent $Probe
}
if ($GoPath) {
    if (-not (Test-Path -LiteralPath $GoPath -PathType Leaf)) { throw "Go executable not found: $GoPath" }
    $GoExe = (Resolve-Path -LiteralPath $GoPath).Path
} elseif ($env:AUDIT_EXPLORER_GO) {
    $GoExe = (Resolve-Path -LiteralPath $env:AUDIT_EXPLORER_GO).Path
} elseif ((Get-Command go.exe -ErrorAction SilentlyContinue)) {
    $GoExe = (Get-Command go.exe).Source
} elseif ($ToolchainRoot) {
    $GoExe = Join-Path $ToolchainRoot '.dev\toolchains\go\bin\go.exe'
} else {
    throw 'Go was not found: pass -GoPath, set AUDIT_EXPLORER_GO, or put go.exe on PATH.'
}
if ($ToolchainRoot) {
    $CacheRoot = Join-Path $ToolchainRoot '.dev\toolchains\cache'
    $env:GOCACHE = Join-Path $CacheRoot 'build'
    $env:GOMODCACHE = Join-Path $CacheRoot 'mod'
    $env:GOPATH = Join-Path $CacheRoot 'gopath'
}
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'

$Clean = $Version.TrimStart('v')
if ($Clean -notmatch '^[0-9A-Za-z][0-9A-Za-z.\-+]*$') { throw "Version has characters that do not belong in a file name: $Version" }

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$Exe = Join-Path $OutDir 'unravis-blackbox.exe'
if ($Reuse) {
    if (-not (Test-Path -LiteralPath $Exe -PathType Leaf)) { throw "-Reuse needs $Exe from build.ps1. Run build.ps1 first, or leave -Reuse out to build it here." }
    Write-Host "Reusing $Exe (built and pinned by build.ps1)."
} else {
    Push-Location $Root
    try {
        & $GoExe build -trimpath -ldflags "-s -w -X main.version=$Clean" -o $Exe ./cmd/unravis-blackbox
        if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
    Write-Warning 'This unravis-blackbox.exe was built on its own, so a pinned unravis.exe will not accept it as its helper. Use build.ps1 (then -Reuse) for a pinned pair.'
}
$ExeSha = (Get-FileHash -LiteralPath $Exe -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Host "unravis-blackbox.exe SHA-256 $ExeSha"

$Stage = Join-Path $OutDir 'blackbox-pack'
if (Test-Path -LiteralPath $Stage) { Remove-Item -LiteralPath $Stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path $Stage | Out-Null
Copy-Item -LiteralPath $Exe -Destination $Stage
Copy-Item -LiteralPath (Join-Path $Root 'cmd\unravis-blackbox\pack\blackbox.json') -Destination $Stage
Copy-Item -LiteralPath (Join-Path $Root 'cmd\unravis-blackbox\pack\README.txt') -Destination $Stage

$Zip = Join-Path $OutDir "unravis-blackbox-v$Clean-windows-x64.zip"
if (Test-Path -LiteralPath $Zip) { Remove-Item -LiteralPath $Zip -Force }
Compress-Archive -Path (Join-Path $Stage '*') -DestinationPath $Zip -CompressionLevel Optimal
Remove-Item -LiteralPath $Stage -Recurse -Force

$Size = (Get-Item -LiteralPath $Zip).Length
'{0}: {1:N2} MB (limit 8 MB)' -f $Zip, ($Size / 1MB)
if ($Size -ge $MaxZipBytes) { throw ('The pack is {0:N2} MB. It must stay under 8 MB.' -f ($Size / 1MB)) }
