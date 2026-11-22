<#
.SYNOPSIS
  Checks the real unravis.exe as the desktop sidecar (BL-301). Windows only.
.DESCRIPTION
  -Check NoPort: builds the exe, starts it with --pipe-only --demo and a temporary data folder, then requires that the process
  (1) is still running, (2) has no listening TCP socket, (3) wrote no session.json, and (4) serves one ui and one ag pipe
  (\\.\pipe\unravis.ui.<32hex>, \\.\pipe\unravis.ag.<32hex>). The process is always stopped at the end.
  -Check Squat: starts the same way, then tries to create a second listener with the name of each running pipe; both attempts must fail
  (first-instance flag plus full instance pool, spec 01 BL-301).
  -Check ShutdownClean / KillDuringWrite (BL-303): run the Go tests of the same names (TestShutdownCleanRealExe, TestKillDuringWriteReopens) on the
  demo's app-created cases. They build the exe and drive it over its real ui pipe, so they need a Go toolchain.
  -Check ShutdownClean -Real (BL-303, run it as yourself on your PC): the same proof on a COPY of your real data. It copies the real cases
  folder (%LOCALAPPDATA%\APIAuditExplorer\cases) and the files cases need from %APPDATA%\APIAuditExplorer (hosts.dat, masterkey.dat, erased.json) into
  a temp folder, starts the exe on the copy (no --demo), adds to-do lines to one case from 4 writers, calls shutdown while they are busy, and
  requires exit 0 within 5 s and no -wal/-shm. Then tools/dev/checkdb opens the copy and requires integrity_check ok and every acknowledged
  line in the case. The originals are only read. -CaseId picks the case (default: the first one checkdb can open); a case under a master key
  is refused with 423, so pick one without. The copy is deleted at the end (it is encrypted, but it is your data).
  Later sidecar items add checks here (OtherUser, TenStarts ...). Not run on Linux.
#>
param(
    [ValidateSet('NoPort', 'Squat', 'ShutdownClean', 'KillDuringWrite')][string]$Check = 'NoPort',
    [switch]$Real,
    [string]$CaseId = '',
    [string]$CasesFrom = (Join-Path $env:LOCALAPPDATA 'APIAuditExplorer\cases'),
    [string]$BaseFrom = (Join-Path $env:APPDATA 'APIAuditExplorer')
)
$ErrorActionPreference = 'Stop'
$repo = Resolve-Path (Join-Path $PSScriptRoot '..\..')
if ($Real -and $Check -ne 'ShutdownClean') { throw '-Real only goes with -Check ShutdownClean' }
if ($Real) {
    # BL-303 on a copy of the real data. Nothing here writes to the originals.
    if (-not (Test-Path $CasesFrom)) { throw "no cases folder at $CasesFrom (use -CasesFrom)" }
    $work = Join-Path ([IO.Path]::GetTempPath()) ("unravis-real-" + [guid]::NewGuid().ToString('N'))
    $data = Join-Path $work 'data'
    New-Item -ItemType Directory -Path (Join-Path $data 'cases') | Out-Null
    $fail = $null
    Push-Location $repo
    try {
        $checkdb = Join-Path $work 'checkdb.exe'
        go build -o $checkdb ./tools/dev/checkdb
        if ($LASTEXITCODE -ne 0) { throw 'go build checkdb failed' }
        foreach ($f in 'hosts.dat', 'masterkey.dat', 'erased.json') {
            $src = Join-Path $BaseFrom $f
            if (Test-Path $src) { Copy-Item $src (Join-Path $data $f) }
        }
        Get-ChildItem -LiteralPath $CasesFrom -Force | Where-Object { $CaseId -eq '' -or $_.Name -eq $CaseId -or $_.Name.StartsWith('.') } |
            ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $data 'cases') -Recurse -Force }
        if ($CaseId -eq '') {
            $first = & $checkdb -data $data -list | Where-Object { $_ -match ' ok$' } | Select-Object -First 1
            if (-not $first) { throw 'no case in the copy opens (locked by a master key, or sealed for another user); see: checkdb -data <folder> -list' }
            $CaseId = ($first -split ' ')[0]
        }
        Write-Host "Real case $CaseId (copy in $work)"
        & $checkdb -data $data -case $CaseId -no-wal
        if ($LASTEXITCODE -ne 0) { throw 'the copy is not healthy before the test (checkdb)' }
        $acked = Join-Path $work 'acked.txt'
        $env:UNRAVIS_REAL_DATA = $data; $env:UNRAVIS_REAL_CASE_ID = $CaseId; $env:UNRAVIS_REAL_ACKED = $acked
        go test -count=1 -v -run '^TestShutdownCleanRealCase$' ./cmd/audit-explorer/...
        if ($LASTEXITCODE -ne 0) { throw 'ShutdownClean -Real: the exe did not stop cleanly' }
        & $checkdb -data $data -case $CaseId -no-wal -texts $acked
        if ($LASTEXITCODE -ne 0) { throw 'ShutdownClean -Real: checkdb failed after the stop' }
        Write-Host 'ShutdownClean -Real OK'
    } catch { $fail = $_ } finally {
        Pop-Location
        Remove-Item Env:UNRAVIS_REAL_DATA, Env:UNRAVIS_REAL_CASE_ID, Env:UNRAVIS_REAL_ACKED -ErrorAction SilentlyContinue
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
    }
    if ($fail) { Write-Error $fail; exit 1 }
    return
}
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
