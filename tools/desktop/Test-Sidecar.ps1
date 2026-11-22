<#
.SYNOPSIS
  Checks the real unravis.exe as the desktop sidecar (BL-301). Windows only.
.DESCRIPTION
  -Check NoPort: builds the exe, starts it with --pipe-only --demo and a temporary data folder, then requires that the process
  (1) is still running, (2) has no listening TCP socket, (3) wrote no session.json, and (4) serves one ui and one ag pipe
  (\\.\pipe\unravis.ui.<32hex>, \\.\pipe\unravis.ag.<32hex>). The process is always stopped at the end.
  -Check Squat: starts the same way, then tries to create a second listener with the name of each running pipe; both attempts must fail
  (first-instance flag plus full instance pool, spec 01 BL-301).   -Check ShutdownClean / KillDuringWrite (BL-303): run the Go tests of the same names on app-created demo cases (the real-case run is a live-PC step).
  Later sidecar items add checks here (OtherUser, TenStarts ...). Not run on Linux.
#>
param([ValidateSet('NoPort', 'Squat', 'ShutdownClean', 'KillDuringWrite')][string]$Check = 'NoPort')
$ErrorActionPreference = 'Stop'
$repo = Resolve-Path (Join-Path $PSScriptRoot '..\..')
if ($Check -in 'ShutdownClean', 'KillDuringWrite') {
    # BL-303. The Go tests build and drive the real exe over its real ui pipe (a copy of the test binary named unravis-desktop.exe is the client).
    $name = if ($Check -eq 'ShutdownClean') { 'TestShutdownCleanRealExe' } else { 'TestKillDuringWriteReopens' }
    Push-Location $repo
    try { go test -count=1 -v -run "^$name$" ./cmd/audit-explorer/...; if ($LASTEXITCODE -ne 0) { Write-Error "$Check failed"; exit 1 } } finally { Pop-Location }
    Write-Host "$Check OK"
    return
}
$work = Join-Path ([IO.Path]::GetTempPath()) ("unravis-sidecar-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$exe = Join-Path $work 'unravis.exe'
Push-Location $repo
try { go build -o $exe ./cmd/audit-explorer; if ($LASTEXITCODE -ne 0) { throw 'go build failed' } } finally { Pop-Location }

$data = Join-Path $work 'data'
New-Item -ItemType Directory -Path $data | Out-Null
$env:AUDIT_CONFIG_DIR = $data
$proc = Start-Process -FilePath $exe -ArgumentList '--pipe-only', '--demo' -PassThru -WindowStyle Hidden
$fail = $null
try {
    $ui = $null; $ag = $null
    for ($i = 0; $i -lt 40 -and (-not ($ui -and $ag)); $i++) {
        Start-Sleep -Milliseconds 250
        if ($proc.HasExited) { throw "unravis.exe exited early with code $($proc.ExitCode)" }
        $pipes = [IO.Directory]::GetFiles('\\.\pipe\') | ForEach-Object { Split-Path $_ -Leaf }
        $ui = $pipes | Where-Object { $_ -match '^unravis\.ui\.[0-9a-f]{32}$' }
        $ag = $pipes | Where-Object { $_ -match '^unravis\.ag\.[0-9a-f]{32}$' }
    }
    if (-not $ui) { throw 'no unravis.ui.<32hex> pipe appeared' }
    if (-not $ag) { throw 'no unravis.ag.<32hex> pipe appeared' }
    $listen = @(Get-NetTCPConnection -OwningProcess $proc.Id -State Listen -ErrorAction SilentlyContinue)
    if ($listen.Count -ne 0) { throw "pipe-only process listens on TCP: $($listen | ForEach-Object LocalPort)" }
    $session = Get-ChildItem -Path $data -Recurse -Filter 'session.json' -ErrorAction SilentlyContinue
    if ($session) { throw "session file written: $($session.FullName)" }
    if ($Check -eq 'Squat') {
        foreach ($leaf in @($ui, $ag)) {
            $squatted = $false
            $srv = $null
            try {
                $srv = New-Object System.IO.Pipes.NamedPipeServerStream($leaf, [System.IO.Pipes.PipeDirection]::InOut, 254, [System.IO.Pipes.PipeTransmissionMode]::Byte, [System.IO.Pipes.PipeOptions]::None)
                $squatted = $true
            } catch { }
            finally { if ($srv) { $srv.Dispose() } }
            if ($squatted) { throw "a second listener could be created on pipe $leaf" }
        }
        Write-Host "Squat OK: a second listener on $ui and on $ag is refused"
        return
    }
    Write-Host "NoPort OK: pid $($proc.Id), pipes $ui and $ag, no TCP listener, no session.json"
} catch { $fail = $_ } finally {
    if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($fail) { Write-Error $fail; exit 1 }
