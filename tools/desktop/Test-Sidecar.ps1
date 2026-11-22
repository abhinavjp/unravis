<#
.SYNOPSIS
  Checks the real unravis.exe as the desktop sidecar (BL-301). Windows only.
.DESCRIPTION
  -Check NoPort: builds the exe, starts it with --pipe-only --demo and a temporary data folder, then requires that the process
  (1) is still running, (2) has no listening TCP socket, (3) wrote no session.json, and (4) serves one ui and one ag pipe
  (\\.\pipe\unravis.ui.<32hex>, \\.\pipe\unravis.ag.<32hex>). The process is always stopped at the end.
  -Check Squat: starts the same way, then tries to create a second listener with the name of each running pipe; both attempts must fail
  (first-instance flag plus full instance pool, spec 01 BL-301). -Check TenStarts, Stale, Acl (BL-302): ten simultaneous `--pipe-server --demo` starts leave exactly one running owner (the others exit 12) and one
  live backend.demo.json; `taskkill /f` on the owner and a new start give a new pid in a live locator; icacls on the locator shows only the
  current user (and SYSTEM), no Everyone/Users, nothing inherited. The locator is <AUDIT_CONFIG_DIR>\run\backend.demo.json here (backend.json
  without --demo). Later sidecar items add checks here (OtherUser, TenStarts ...). Not run on Linux.
#>
param([ValidateSet('NoPort', 'Squat', 'TenStarts', 'Stale', 'Acl')][string]$Check = 'NoPort')
$ErrorActionPreference = 'Stop'
$repo = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$work = Join-Path ([IO.Path]::GetTempPath()) ("unravis-sidecar-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
$exe = Join-Path $work 'unravis.exe'
Push-Location $repo
try { go build -o $exe ./cmd/audit-explorer; if ($LASTEXITCODE -ne 0) { throw 'go build failed' } } finally { Pop-Location }

$data = Join-Path $work 'data'
New-Item -ItemType Directory -Path $data | Out-Null
$env:AUDIT_CONFIG_DIR = $data
$locatorFile = Join-Path $data 'run\backend.demo.json'

# Starts the exe with System.Diagnostics.Process.Start, which keeps the process handle open. With Start-Process -PassThru a copy that exits
# before its handle is opened (the nine losers exit within milliseconds) later reports a null ExitCode and can throw on WaitForExit.
function Start-Sidecar([string[]]$ArgList) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = $ArgList -join ' '
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $p = [System.Diagnostics.Process]::Start($psi)
    $null = $p.Handle
    return $p
}

function Wait-LiveLocator {
    for ($i = 0; $i -lt 120; $i++) {
        if (Test-Path $locatorFile) {
            $rec = Get-Content -Raw $locatorFile | ConvertFrom-Json
            $p = Get-Process -Id $rec.pid -ErrorAction SilentlyContinue
            $pipes = [IO.Directory]::GetFiles('\\.\pipe\') | ForEach-Object { Split-Path $_ -Leaf }
            if ($p -and ($pipes -contains (Split-Path $rec.ui -Leaf)) -and ($pipes -contains (Split-Path $rec.ag -Leaf))) { return $rec }
        }
        Start-Sleep -Milliseconds 250
    }
    throw "no live locator at $locatorFile"
}

if ($Check -in 'TenStarts', 'Stale', 'Acl') {
    $procs = @()
    $fail = $null
    try {
        if ($Check -eq 'TenStarts') {
            1..10 | ForEach-Object { $procs += Start-Sidecar @('--pipe-server', '--demo') }
            $rec = Wait-LiveLocator
            Start-Sleep -Seconds 3
            $running = @($procs | Where-Object { -not $_.HasExited })
            $codes = @($procs | Where-Object { $_.HasExited } | ForEach-Object { [int]$_.ExitCode })
            if ($codes.Count -ne 9) { throw "expected 9 exited copies, found $($codes.Count)" }
            if ($running.Count -ne 1) { throw "expected exactly 1 running owner, found $($running.Count)" }
            if ($running[0].Id -ne $rec.pid) { throw "running pid $($running[0].Id) is not the locator pid $($rec.pid)" }
            if (@($codes | Where-Object { $_ -ne 12 }).Count -ne 0) { throw "non-owner exit codes: $codes (want all 12)" }
            Write-Host "TenStarts OK: one owner pid $($rec.pid), 9 copies exited 12"
        } elseif ($Check -eq 'Stale') {
            $a = Start-Sidecar @('--pipe-server', '--demo')
            $procs += $a
            $r1 = Wait-LiveLocator
            & taskkill /f /pid $a.Id | Out-Null
            if (-not $a.WaitForExit(15000)) { throw "owner pid $($a.Id) did not exit after taskkill" }
            $b = Start-Sidecar @('--pipe-server', '--demo')
            $procs += $b
            $r2 = Wait-LiveLocator
            if ($r2.pid -eq $r1.pid) { throw 'locator pid did not change after the owner was killed' }
            if ($r2.pid -ne $b.Id) { throw "locator pid $($r2.pid) is not the new owner $($b.Id)" }
            Write-Host "Stale OK: pid $($r1.pid) -> $($r2.pid), live"
        } else {
            $a = Start-Sidecar @('--pipe-server', '--demo')
            $procs += $a
            $null = Wait-LiveLocator
            $acl = (& icacls $locatorFile) -join "`n"
            foreach ($bad in 'Everyone', 'BUILTIN\Users', 'Authenticated Users', '(I)') { if ($acl.Contains($bad)) { throw "locator ACL has $bad`n$acl" } }
            if (-not $acl.Contains($env:USERNAME)) { throw "locator ACL does not name $env:USERNAME`n$acl" }
            Write-Host "Acl OK: only the current user (and SYSTEM) on backend.demo.json"
        }
    } catch { $fail = $_ } finally {
        foreach ($q in $procs) { if (-not $q.HasExited) { Stop-Process -Id $q.Id -Force -ErrorAction SilentlyContinue } }
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
    }
    if ($fail) { Write-Error $fail; exit 1 }
    return
}
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
