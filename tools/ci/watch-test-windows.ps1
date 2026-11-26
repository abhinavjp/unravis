# Watches the desktop while Go tests run and records any visible top-level window that a TEST process (an image named *.test.exe)
# opens. A test must never show a real window: it stays on the person's desktop until someone clicks it (the "Unravis test" box,
# 2026-11-26). Each offender is logged, then closed (WM_CLOSE) so it cannot pile up. Started and stopped by tools/ci/prepush.sh; the
# local CI runner can start it the same way around its Windows test step.
#   powershell -NoProfile -File tools/ci/watch-test-windows.ps1 -Log <file> -StopFile <file>
# It runs until -StopFile exists, then exits. -Log gets one line per window: pid|exe|class|title. An empty or missing log = clean.
param(
  [Parameter(Mandatory = $true)][string]$Log,
  [Parameter(Mandatory = $true)][string]$StopFile,
  [int]$PollMs = 150
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
  public static void Close(long h) { PostMessage(new IntPtr(h), 0x0010, IntPtr.Zero, IntPtr.Zero); }
}
'@
$seen = @{}
while (-not (Test-Path -LiteralPath $StopFile)) {
  foreach ($w in [TestWinWatch]::Visible()) {
    $hwnd = $w[0]; $procId = [int]$w[1]
    if ($seen.ContainsKey($hwnd)) { continue }
    $exe = $null
    try { $exe = (Get-Process -Id $procId -ErrorAction Stop).Path } catch { }
    if ($exe -and $exe -match '\.test\.exe$') {
      $seen[$hwnd] = $true
      Add-Content -LiteralPath $Log -Value ("{0}|{1}|{2}|{3}" -f $procId, (Split-Path $exe -Leaf), $w[2], $w[3])
      [TestWinWatch]::Close([long]$hwnd)
    }
  }
  Start-Sleep -Milliseconds $PollMs
}
