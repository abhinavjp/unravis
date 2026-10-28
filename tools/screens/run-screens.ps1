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

  The setup-window screens (p11-*) run against the screenshot build of unravis-setup: this script builds it
  with Go (go build -tags capture) into the temporary folder and starts it with --capture, which fakes every
  change. Go is found the way build.ps1 finds it. Without Go those screens are skipped, and the run says so.
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

# Two runs at once would write to the same pictures and the same results folder.
# The lock belongs to this Windows session and is let go when this script ends, however it ends.
$Lock = New-Object System.Threading.Mutex($false, 'Local\UnravisScreenshotRun')
$HaveLock = $false
try { $HaveLock = $Lock.WaitOne(0) } catch [System.Threading.AbandonedMutexException] { $HaveLock = $true }
if (-not $HaveLock) {
    Write-Host 'Another screenshot run is already going on this computer. Wait for it to finish, then run this again.' -ForegroundColor Red
    exit 4
}

if (-not (Test-Path -LiteralPath $Exe -PathType Leaf)) {
    Write-Host 'dist\unravis.exe is missing. Build first: .\build.ps1' -ForegroundColor Red
    exit 2
}
if (-not (Test-Path -LiteralPath (Join-Path $Here 'node_modules\@playwright\test') -PathType Container)) {
    Write-Host 'The screenshot tools are not installed. In tools\screens run: npm ci, then npx playwright install chromium' -ForegroundColor Red
    exit 2
}

# A run that was killed outright (not stopped with Ctrl+C) cannot clean up after itself.
# Remove what such a run left in the temp folder: only folders this script made, and only after an hour.
$Temp = [System.IO.Path]::GetTempPath()
foreach ($old in @(Get-ChildItem -LiteralPath $Temp -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -match '^unravis-screens-[0-9a-f]{32}$' -and $_.LastWriteTime -lt (Get-Date).AddHours(-1) })) {
    try { Remove-Item -LiteralPath $old.FullName -Recurse -Force -ErrorAction Stop } catch { }
}

$DemoNow = '2026-10-20T09:00:00Z'
$Work = Join-Path ([System.IO.Path]::GetTempPath()) ('unravis-screens-' + [guid]::NewGuid().ToString('N'))
$OutLog = Join-Path $Work 'unravis.out.log'
$ErrLog = Join-Path $Work 'unravis.err.log'
$ConfigDir = Join-Path $Work 'config'
$Process = $null
$SetupProcess = $null
$Saved = @{}
$Names = @('AUDIT_CONFIG_DIR', 'UNRAVIS_DEMO_NOW', 'UNRAVIS_DEMO_CASES', 'UNRAVIS_LAUNCH_URL', 'UNRAVIS_ONLY', 'UNRAVIS_SETUP_URL', 'UNRAVIS_SETUP_KEY', 'CGO_ENABLED', 'GOTOOLCHAIN')
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

    # If this script is killed outright, Windows ends the demo with it: the demo belongs to a job
    # that is set to end when the last handle to it closes, and that handle dies with this process.
    $JobCode = @'
using System;
using System.Runtime.InteropServices;
public static class UnravisJob {
    [StructLayout(LayoutKind.Sequential)] struct Basic { public long PerProcessUserTimeLimit; public long PerJobUserTimeLimit; public uint LimitFlags; public UIntPtr MinimumWorkingSetSize; public UIntPtr MaximumWorkingSetSize; public uint ActiveProcessLimit; public UIntPtr Affinity; public uint PriorityClass; public uint SchedulingClass; }
    [StructLayout(LayoutKind.Sequential)] struct Io { public ulong A, B, C, D, E, F; }
    [StructLayout(LayoutKind.Sequential)] struct Extended { public Basic BasicLimit; public Io IoInfo; public UIntPtr ProcessMemoryLimit; public UIntPtr JobMemoryLimit; public UIntPtr PeakProcessMemoryUsed; public UIntPtr PeakJobMemoryUsed; }
    [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr CreateJobObject(IntPtr attributes, string name);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool SetInformationJobObject(IntPtr job, int infoClass, IntPtr info, uint size);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    static IntPtr job = IntPtr.Zero;
    public static bool Bind(IntPtr process) {
        job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) return false;
        Extended info = new Extended();
        info.BasicLimit.LimitFlags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        int size = Marshal.SizeOf(typeof(Extended));
        IntPtr buffer = Marshal.AllocHGlobal(size);
        try {
            Marshal.StructureToPtr(info, buffer, false);
            if (!SetInformationJobObject(job, 9, buffer, (uint)size)) return false;
        } finally { Marshal.FreeHGlobal(buffer); }
        return AssignProcessToJobObject(job, process);
    }
}
'@
    try {
        Add-Type -TypeDefinition $JobCode -ErrorAction Stop
        if (-not [UnravisJob]::Bind($Process.Handle)) { Write-Host 'Note: the demo is not tied to this window; if you kill this window the demo keeps running.' -ForegroundColor Yellow }
    } catch {
        Write-Host 'Note: the demo is not tied to this window; if you kill this window the demo keeps running.' -ForegroundColor Yellow
    }

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

    # The setup window with its fake runs (P11). Built fresh each run so it always matches web\setup and internal\setup.
    $env:UNRAVIS_SETUP_URL = ''
    $env:UNRAVIS_SETUP_KEY = ''
    $Go = $env:AUDIT_EXPLORER_GO
    if (-not $Go) { $GoCommand = Get-Command go.exe -ErrorAction SilentlyContinue; if ($GoCommand) { $Go = $GoCommand.Source } }
    if (-not $Go) {
        $Probe = $RepoRoot
        while ($Probe -and -not $Go) {
            $Candidate = Join-Path $Probe '.dev\toolchains\go\bin\go.exe'
            if (Test-Path -LiteralPath $Candidate -PathType Leaf) { $Go = $Candidate }
            $Probe = Split-Path -Parent $Probe
        }
    }
    if (-not $Go -or -not (Test-Path -LiteralPath $Go -PathType Leaf)) {
        Write-Host 'Note: Go was not found, so the setup-window screens (p11-*) are skipped.' -ForegroundColor Yellow
    } else {
        $SetupExe = Join-Path $Work 'unravis-setup-capture.exe'
        $SetupOut = Join-Path $Work 'setup.out.log'
        $SetupErr = Join-Path $Work 'setup.err.log'
        $env:CGO_ENABLED = '0'
        $env:GOTOOLCHAIN = 'local'
        Push-Location $RepoRoot
        $ErrorActionPreference = 'Continue'
        try {
            & $Go build -tags capture -o $SetupExe ./cmd/unravis-setup
            $BuildCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = 'Stop'
            Pop-Location
        }
        if ($BuildCode -ne 0) {
            Write-Host 'The screenshot build of unravis-setup did not build (see above).' -ForegroundColor Red
            exit 3
        }
        $SetupProcess = Start-Process -FilePath $SetupExe -ArgumentList @('--capture', '--no-browser') `
            -RedirectStandardOutput $SetupOut -RedirectStandardError $SetupErr -PassThru -WindowStyle Hidden
        try { [void][UnravisJob]::Bind($SetupProcess.Handle) } catch { }
        $SetupDeadline = (Get-Date).AddSeconds(30)
        while ((Get-Date) -lt $SetupDeadline -and -not $env:UNRAVIS_SETUP_URL) {
            Start-Sleep -Milliseconds 200
            $Text = ''
            foreach ($f in @($SetupOut, $SetupErr)) {
                if (Test-Path -LiteralPath $f) {
                    $part = Get-Content -LiteralPath $f -Raw -ErrorAction SilentlyContinue
                    if ($part) { $Text += $part }
                }
            }
            if ($Text -match 'listening at (http://127\.0\.0\.1:\d+)/#k=([A-Za-z0-9_-]+)') {
                $env:UNRAVIS_SETUP_URL = $Matches[1]
                $env:UNRAVIS_SETUP_KEY = $Matches[2]
            }
            if ($SetupProcess.HasExited) { break }
        }
        if (-not $env:UNRAVIS_SETUP_URL) {
            Write-Host 'The setup window did not start. Its log says:' -ForegroundColor Red
            foreach ($f in @($SetupOut, $SetupErr)) {
                if (Test-Path -LiteralPath $f) { Get-Content -LiteralPath $f -Tail 15 | ForEach-Object { Write-Host ('  ' + $_) } }
            }
            exit 3
        }
    }

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
    if ($SetupProcess -and -not $SetupProcess.HasExited) {
        try { Stop-Process -Id $SetupProcess.Id -Force -ErrorAction SilentlyContinue } catch { }
        try { $SetupProcess.WaitForExit(5000) | Out-Null } catch { }
    }
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
