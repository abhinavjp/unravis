# Watches the desktop while Go tests run and records any visible top-level window that a TEST process (an image named *.test.exe)
# opens. A test must never show a real window: it stays on the person's desktop until someone clicks it (the "Unravis test" box,
# 2026-11-26). Each offender is logged, then closed (WM_CLOSE) so it cannot pile up. Started and stopped by tools/ci/prepush.sh; the
# local CI runner can start it the same way around its Windows test step.
#   powershell -NoProfile -File tools/ci/watch-test-windows.ps1 -Log <file> -StopFile <file>
# It runs until -StopFile exists, then exits. -Log gets one line per window: pid|exe|class|title. An empty or missing log = clean.
# -RootPid <pid> limits it to test processes started (directly or not) by that process, so another run on the same PC (the local CI
# runner, another thread's gate) is never blamed or touched. Without it, every *.test.exe on the desktop counts (the runner's own use).
param(
  [Parameter(Mandatory = $true)][string]$Log,
  [Parameter(Mandatory = $true)][string]$StopFile,
  [int]$PollMs = 50,
  [int]$RootPid = 0
)
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Text;
using System.Runtime.InteropServices;
public static class TestWinWatch {
  delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc p, IntPtr l);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
  public static List<string[]> Visible() {
    var r = new List<string[]>();
    EnumWindows((h, l) => {
      if (!IsWindowVisible(h)) return true;
      uint pid; GetWindowThreadProcessId(h, out pid);
      var t = new StringBuilder(256); GetWindowText(h, t, 256);
      var c = new StringBuilder(256); GetClassName(h, c, 256);
      r.Add(new[] { h.ToInt64().ToString(), pid.ToString(), c.ToString(), t.ToString() });
      return true;
    }, IntPtr.Zero);
    return r;
  }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct PROCESSENTRY32 { public uint dwSize, cntUsage, th32ProcessID; public IntPtr th32DefaultHeapID; public uint th32ModuleID, cntThreads, th32ParentProcessID; public int pcPriClassBase; public uint dwFlags;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)] public string szExeFile; }
  [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr CreateToolhelp32Snapshot(uint flags, uint pid);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern bool Process32First(IntPtr s, ref PROCESSENTRY32 e);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern bool Process32Next(IntPtr s, ref PROCESSENTRY32 e);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
  // pid -> parent pid, in a few milliseconds (a box may live for only half a second).
  public static Dictionary<int, int> Parents() {
    var d = new Dictionary<int, int>();
    IntPtr snap = CreateToolhelp32Snapshot(2, 0);
    var e = new PROCESSENTRY32(); e.dwSize = (uint)Marshal.SizeOf(typeof(PROCESSENTRY32));
    if (Process32First(snap, ref e)) { do { d[(int)e.th32ProcessID] = (int)e.th32ParentProcessID; } while (Process32Next(snap, ref e)); }
    CloseHandle(snap);
    return d;
  }
  public static void Close(long h) { PostMessage(new IntPtr(h), 0x0010, IntPtr.Zero, IntPtr.Zero); }
}
'@
function Test-UnderRoot([int]$ProcId) {
  if ($RootPid -le 0) { return $true }
  $parent = [TestWinWatch]::Parents()
  $cur = $ProcId
  for ($i = 0; $i -lt 40 -and $parent.ContainsKey($cur); $i++) {
    if ($cur -eq $RootPid) { return $true }
    $cur = $parent[$cur]
  }
  return $cur -eq $RootPid
}
$seen = @{}
if ($RootPid -gt 0 -and -not (Get-Process -Id $RootPid -ErrorAction SilentlyContinue)) { exit 0 }
Set-Content -LiteralPath ($StopFile + '.ready') -Value 'ready' # the gate waits for this before it starts the tests
while (-not (Test-Path -LiteralPath $StopFile)) {
  # The gate that started this is gone (Ctrl-C, a dead shell): stop, so an aborted run never leaves a watcher behind.
  if ($RootPid -gt 0 -and -not (Get-Process -Id $RootPid -ErrorAction SilentlyContinue)) { break }
  $now = [TestWinWatch]::Visible()
  # Windows reuses a window handle after the window is destroyed: forget the ones that are gone.
  $live = @{}; foreach ($w in $now) { $live[$w[0]] = $true }
  foreach ($k in @($seen.Keys)) { if (-not $live.ContainsKey($k)) { $seen.Remove($k) } }
  foreach ($w in $now) {
    $hwnd = $w[0]; $procId = [int]$w[1]
    if ($seen.ContainsKey($hwnd)) { continue }
    $exe = $null
    try { $exe = (Get-Process -Id $procId -ErrorAction Stop).Path } catch { }
    if ($exe -and $exe -match '\.test\.exe$') {
      $seen[$hwnd] = $true
      if (-not (Test-UnderRoot $procId)) { continue }
      Add-Content -LiteralPath $Log -Value ("{0}|{1}|{2}|{3}" -f $procId, (Split-Path $exe -Leaf), $w[2], $w[3])
      [TestWinWatch]::Close([long]$hwnd)
    }
  }
  Start-Sleep -Milliseconds $PollMs
}
Set-Content -LiteralPath ($StopFile + '.done') -Value 'done' # normal end: the gate reads a missing file as a watcher that died
