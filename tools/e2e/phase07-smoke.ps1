# Phase 07 (background run) smoke test for the built GUI-subsystem exe.
#   -Exe <path>   the exe to test (default: dist\unravis.exe in this repo)
#
# Uses a temp AUDIT_CONFIG_DIR (so session.json and logs are private to the run) and --port 0 (the OS picks a free port).
# Facts taken from cmd/audit-explorer/{main,gate,quit}.go:
#   - --version prints the version and exits 0.
#   - --no-browser never reaches the single-instance gate (no mutex, no events), so the single-instance and --quit
#     checks use --background, which does (and mints no launch code, opens no browser).
#   - A second --background copy exits silently with code 0 and starts no server.
#   - --quit exits 0 on success and also 0 with "Unravis is not running." when nothing is running.
#   - The instance names are per user (UnravisApp; demo copies use UnravisDemoApp and --quit cannot reach them).
#     If another Unravis (for example the installed one) already holds the name, the first --background copy here
#     exits silently at once. Then --quit would stop THAT copy, so checks b, c, d, e are reported BLOCKED and not
#     run (b2 records the silent exit). Quit the other Unravis from the tray and run again for the full set.
# Every process and temp dir created here is removed on exit. Exit code: 0 if no check FAILed, 1 otherwise.
param(
  [string]$Exe = ''
)

$ErrorActionPreference = 'Stop'
if ($Exe -eq '') { $Exe = Join-Path (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path 'dist\unravis.exe' }
if (-not (Test-Path -LiteralPath $Exe)) { throw "The exe was not found: $Exe" }
$Exe = (Resolve-Path -LiteralPath $Exe).Path

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("unravis-p07-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$cfg = Join-Path $work 'cfg'
New-Item -ItemType Directory -Force -Path $cfg | Out-Null
$sessionFile = Join-Path $cfg 'session.json'

$results = New-Object System.Collections.Generic.List[object]
$started = New-Object System.Collections.Generic.List[System.Diagnostics.Process]
function Add-Result([string]$Id, [bool]$Ok, [string]$Note) {
  $status = if ($Ok) { 'PASS' } else { 'FAIL' }
  $results.Add([pscustomobject]@{ Id = $Id; Status = $status; Note = $Note }) | Out-Null
}
function Add-Blocked([string]$Id, [string]$Note) {
  $results.Add([pscustomobject]@{ Id = $Id; Status = 'BLOCKED'; Note = $Note }) | Out-Null
}

# Runs the exe to completion with redirected output. Returns exit code, elapsed ms, stdout and stderr text; kills it on timeout.
function Invoke-Exe([string[]]$Arguments, [int]$TimeoutSec = 15) {
  $o = Join-Path $work ("out-" + [guid]::NewGuid().ToString('N').Substring(0, 6) + '.txt')
  $e = $o + '.err'
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $p = Start-Process -FilePath $Exe -ArgumentList $Arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $o -RedirectStandardError $e
  $null = $p.Handle # keeps the handle so ExitCode is readable
  $done = $p.WaitForExit($TimeoutSec * 1000)
  $sw.Stop()
  $timedOut = -not $done
  if ($timedOut) { try { $p.Kill() } catch {} ; $code = $null } else { $code = $p.ExitCode }
  $so = if (Test-Path -LiteralPath $o) { (Get-Content -LiteralPath $o -Raw) } else { '' }
  $se = if (Test-Path -LiteralPath $e) { (Get-Content -LiteralPath $e -Raw) } else { '' }
  [pscustomobject]@{ Code = $code; Ms = $sw.ElapsedMilliseconds; TimedOut = $timedOut; Out = [string]$so; Err = [string]$se }
}

function Start-First([string[]]$Arguments) {
  $o = Join-Path $work 'first.out'
  $p = Start-Process -FilePath $Exe -ArgumentList $Arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $o -RedirectStandardError ($o + '.err')
  $null = $p.Handle # keeps the handle so ExitCode is readable
  $started.Add($p) | Out-Null
  return $p
}

function Wait-For([scriptblock]$Cond, [int]$TimeoutSec) {
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  while ($sw.Elapsed.TotalSeconds -lt $TimeoutSec) {
    if (& $Cond) { return $true }
    Start-Sleep -Milliseconds 200
  }
  return [bool](& $Cond)
}

function Get-ChildNames([int]$ParentId) {
  @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$ParentId" -ErrorAction SilentlyContinue | ForEach-Object { $_.Name })
}

try {
  $env:AUDIT_CONFIG_DIR = $cfg
  Remove-Item Env:AUDIT_SESSION_TOKEN -ErrorAction SilentlyContinue

  # a: --version prints and exits 0, nothing hangs.
  $r = Invoke-Exe @('--version') 15
  $ok = (-not $r.TimedOut) -and ($r.Code -eq 0) -and ($r.Out.Trim().Length -gt 0)
  Add-Result 'a --version' $ok ("exit=$($r.Code) ms=$($r.Ms) timedOut=$($r.TimedOut) stdout='" + $r.Out.Trim() + "'")

  # Start the first copy. If another Unravis already holds the per-user instance name, this copy exits silently at once
  # with no session file: then --quit would stop THAT copy, so b, c, d, e are BLOCKED and nothing else is done.
  $first = Start-First @('--background', '--port', '0')
    $up = Wait-For { (Test-Path -LiteralPath $sessionFile) -or $first.HasExited } 20
    $alive = -not $first.HasExited
  if ((-not $alive) -and (-not (Test-Path -LiteralPath $sessionFile))) {
    $why = "BLOCKED: the first --background copy exited at once with no session file (exit=$($first.ExitCode)): another Unravis already holds the per-user instance name, and --quit would stop that copy. Quit it from the tray and re-run."
    Add-Result 'b2 second --background vs existing copy' ($first.ExitCode -eq 0) ("exited silently with code $($first.ExitCode) and started no server")
    foreach ($id in @('b single instance', 'c --quit stops first copy', 'd --quit with nothing running', 'e --background opens no browser')) { Add-Blocked $id $why }
  } else {
    $port = $null
    if ($up -and (Test-Path -LiteralPath $sessionFile)) {
      try { $port = [int]((Get-Content -LiteralPath $sessionFile -Raw | ConvertFrom-Json).port) } catch {}
    }
    $listening = $false
    if ($port) {
      try { $c = New-Object System.Net.Sockets.TcpClient; $c.Connect('127.0.0.1', $port); $listening = $c.Connected; $c.Close() } catch {}
    }
    Start-Sleep -Seconds 2
    $kids = Get-ChildNames $first.Id
    $browserKid = @($kids | Where-Object { $_ -match 'rundll32|chrome|msedge|firefox|iexplore' }).Count -gt 0
    $logDir = Join-Path $cfg 'logs'
    $logText = ''
    if (Test-Path -LiteralPath $logDir) { $logText = (Get-ChildItem -LiteralPath $logDir -File -ErrorAction SilentlyContinue | ForEach-Object { Get-Content -LiteralPath $_.FullName -Raw -ErrorAction SilentlyContinue }) -join "`n" }
    $launchNone = $logText -match 'launch[=":\s]+"?none'
    Add-Result 'e --background opens no browser' ($alive -and $listening -and (-not $browserKid) -and $launchNone) ("alive=$alive listening=$listening browserChild=$browserKid logLaunchNone=$launchNone")

    # b: a second --background copy exits quickly (silently, code 0) and does not start another server.
    $beforeProcs = @(Get-Process -Name ([IO.Path]::GetFileNameWithoutExtension($Exe)) -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Exe }).Count
    $r = Invoke-Exe @('--background', '--port', '0') 10
    $afterProcs = @(Get-Process -Name ([IO.Path]::GetFileNameWithoutExtension($Exe)) -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Exe }).Count
    $portAfter = $null
    try { $portAfter = [int]((Get-Content -LiteralPath $sessionFile -Raw | ConvertFrom-Json).port) } catch {}
    $ok = (-not $r.TimedOut) -and ($r.Code -eq 0) -and ($r.Ms -lt 5000) -and ($afterProcs -eq $beforeProcs) -and ($port -and $portAfter -eq $port) -and (-not $first.HasExited)
    Add-Result 'b single instance' $ok ("second exit=$($r.Code) ms=$($r.Ms) timedOut=$($r.TimedOut) processes $beforeProcs->$afterProcs sameSessionPort=$($port -and $portAfter -eq $port) firstStillRunning=$(-not $first.HasExited)")

    # c: --quit from a second process stops the first cleanly within ~10 s and removes session.json.
    $r = Invoke-Exe @('--quit') 15
    $gone = $first.WaitForExit(10000)
    $sessionGone = -not (Test-Path -LiteralPath $sessionFile)
    $firstCode = if ($gone) { $first.ExitCode } else { $null }
    Add-Result 'c --quit stops first copy' ((-not $r.TimedOut) -and ($r.Code -eq 0) -and $gone -and ($firstCode -eq 0) -and $sessionGone) ("quit exit=$($r.Code) ms=$($r.Ms) firstExited=$gone firstExit=$firstCode sessionRemoved=$sessionGone")

    # d: --quit with nothing running exits 0 (documented: "Unravis is not running." and code 0).
    $r = Invoke-Exe @('--quit') 15
    $says = ($r.Err + $r.Out) -match 'not running'
    Add-Result 'd --quit with nothing running' ((-not $r.TimedOut) -and ($r.Code -eq 0) -and $says) ("exit=$($r.Code) ms=$($r.Ms) timedOut=$($r.TimedOut) says 'not running'=$says")
  }
}
finally {
  foreach ($p in $started) { try { if (-not $p.HasExited) { $p.Kill() } } catch {} }
  Remove-Item Env:AUDIT_CONFIG_DIR -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 300
  try { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction Stop } catch { Write-Warning "temp dir not removed: $work" }
}

$results | ForEach-Object { '{0,-8} {1,-40} {2}' -f $_.Status, $_.Id, $_.Note }
$fail = @($results | Where-Object { $_.Status -eq 'FAIL' }).Count
$pass = @($results | Where-Object { $_.Status -eq 'PASS' }).Count
$blocked = @($results | Where-Object { $_.Status -eq 'BLOCKED' }).Count
"pass=$pass fail=$fail blocked=$blocked"
if ($fail -gt 0) { exit 1 } else { exit 0 }
