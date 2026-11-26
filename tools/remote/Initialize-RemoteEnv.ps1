<#
.SYNOPSIS
  Creates the private settings file for remote Salt access, outside the repo, readable only by you.
.DESCRIPTION
  Makes %USERPROFILE%\.unravis-remote\.env from remote.env.example if it does not exist (never overwrites),
  removes everyone else's access (by SID), checks the result, and opens the file in Notepad so you can type the
  values yourself. Safe to run again: it only re-checks and re-opens.
#>
[CmdletBinding()]
param([switch]$NoOpen)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'RemoteSalt.Lib.ps1')

$dir = Get-RemoteHome
$file = Get-RemoteEnvPath
if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
$loc = Get-LocationProblems $file
if ($loc.Count -gt 0) { throw ($loc -join '; ') }

Set-PrivateAcl $dir
$created = $false
if (-not (Test-Path -LiteralPath $file)) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'remote.env.example') -Destination $file
    $created = $true
}
Set-PrivateAcl $file

$badDir = Get-AclProblems $dir
$badFile = Get-AclProblems $file
if ($badDir.Count + $badFile.Count -gt 0) { throw ('Permissions are not private: ' + (($badDir + $badFile) -join '; ')) }

Write-Host ("Settings file: {0}" -f $file)
Write-Host ("Created: {0}. Private to your Windows user (checked). Nothing here is ever committed or synced." -f $created)
if (-not $NoOpen) { Start-Process notepad.exe -ArgumentList ('"{0}"' -f $file) }
