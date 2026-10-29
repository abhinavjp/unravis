<#
.SYNOPSIS
  Fills .github/release-notes-template.md in from a tag and the plain git log since the
  previous tag. A person still edits the result in the draft release before publishing
  (installer-and-releases.md section 9).

.DESCRIPTION
  No GitHub API call: "what changed" comes from `git log <previous tag>..<tag>`, one bullet per
  commit subject, oldest first. The first release (no previous tag reachable from this one) gets
  the full log instead and says so in the upgrade notes.

  Commits whose subject starts with "CI:" are listed apart, under a collapsed "Build and test
  changes" block at the end of the list; everything else is under "What changed". A stable tag
  (no "-" in it) is compared with the previous stable tag, so v1.0.0 lists everything since v0.9.0
  even when release candidates lie in between; a prerelease tag is compared with the nearest tag.

  Works in Windows PowerShell 5.1 and PowerShell 7. A tag with no earlier tag is not an error.

  The notes end with "Not yet verified on a clean PC." unless the environment variable
  CLEAN_VM_VERIFIED is the text "true" (set it once the clean-VM check has been done by hand).

.PARAMETER Tag
  The tag being released, e.g. v1.0.0 or v1.0.0-rc.1.

.PARAMETER Version
  The tag without its leading "v", e.g. 1.0.0.

.PARAMETER TemplatePath
  Path to the template (default .github/release-notes-template.md).

.PARAMETER OutFile
  Where to write the filled-in notes.

.PARAMETER RepoRoot
  Git working tree to read the log from (default: two levels up from this script).

.PARAMETER Signed
  Pass this switch when the run signed the binaries (the SIGNPATH secret was set), so the notes
  point at Get-AuthenticodeSignature instead of the SmartScreen "Run anyway" steps.
#>
param(
    [Parameter(Mandatory)] [string]$Tag,
    [Parameter(Mandatory)] [string]$Version,
    [string]$TemplatePath,
    [Parameter(Mandatory)] [string]$OutFile,
    [string]$RepoRoot,
    [switch]$Signed
)

$ErrorActionPreference = 'Stop'
$Here = $PSScriptRoot
if (-not $RepoRoot) { $RepoRoot = (Resolve-Path -LiteralPath (Join-Path $Here '..\..')).Path }
if (-not $TemplatePath) { $TemplatePath = Join-Path $RepoRoot '.github\release-notes-template.md' }
if (-not (Test-Path -LiteralPath $TemplatePath -PathType Leaf)) { throw "Template not found: $TemplatePath" }

# Runs git and returns its output lines, or $null when git exits non-zero ("no earlier tag" is a normal answer).
# Windows PowerShell 5.1 turns anything a native command writes to stderr into a terminating error while
# $ErrorActionPreference is 'Stop', even with 2>$null, so the preference is relaxed inside this function only.
function Invoke-Git {
    param([string[]]$GitArgs)
    $ErrorActionPreference = 'Continue'
    $out = & git -C $RepoRoot @GitArgs 2>$null
    if ($LASTEXITCODE -ne 0) { return $null }
    return $out
}

# Stable tags (no "-") skip prereleases when looking back; a prerelease tag looks back to the nearest tag of any kind.
$describeArgs = @('describe', '--tags', '--abbrev=0')
if (-not $Tag.Contains('-')) { $describeArgs += @('--exclude', '*-*') }
$describeArgs += "$Tag^"
$previousTag = @(Invoke-Git $describeArgs) | Select-Object -First 1

if ($previousTag) {
    $range = "$previousTag..$Tag"
    $previousTagText = $previousTag
} else {
    $range = $Tag
    $previousTagText = 'the first release'
}

# One bullet per commit, oldest first, split into "CI:" commits and the rest.
$commitLines = @(Invoke-Git @('log', $range, '--no-merges', '--reverse', '--pretty=format:%s%x09%h'))
$mainBullets = [System.Collections.Generic.List[string]]::new()
$ciBullets = [System.Collections.Generic.List[string]]::new()
foreach ($line in $commitLines) {
    if (-not $line) { continue }
    $cut = $line.LastIndexOf("`t")
    if ($cut -lt 0) { continue }
    $bullet = '- {0} ({1})' -f $line.Substring(0, $cut), $line.Substring($cut + 1)
    if ($line -match '^CI:') { $ciBullets.Add($bullet) } else { $mainBullets.Add($bullet) }
}

if ($mainBullets.Count -eq 0 -and $ciBullets.Count -eq 0) {
    $whatChanged = '- No commits since the previous tag.'
} else {
    $parts = [System.Collections.Generic.List[string]]::new()
    if ($mainBullets.Count -gt 0) { $parts.Add($mainBullets -join "`n") }
    else { $parts.Add('- No user-facing changes in this release.') }
    if ($ciBullets.Count -gt 0) {
        # <details> renders collapsed on GitHub; the blank lines let the list render as Markdown inside it.
        $parts.Add("<details>`n<summary>Build and test changes ($($ciBullets.Count))</summary>`n`n$($ciBullets -join "`n")`n`n</details>")
    }
    $whatChanged = $parts -join "`n`n"
}

$signingNote = if ($Signed) {
    @'
This release is signed (SignPath Foundation). Windows will show the
publisher name in the UAC prompt. Check the signature with:

    Get-AuthenticodeSignature .\unravis-setup-{0}-windows-x64.exe
'@ -f $Tag
} else {
    @'
This release is not signed. The first time you run the installer, Windows
SmartScreen may show "Windows protected your PC"; choose "More info", then
"Run anyway". Check the SHA-256 above first.
'@
}
if ($env:CLEAN_VM_VERIFIED -ne 'true') {
    $signingNote = $signingNote.TrimEnd() + "`n`nNot yet verified on a clean PC."
}

$date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-dd')

$text = Get-Content -LiteralPath $TemplatePath -Raw
$text = $text.Replace('{{VERSION}}', $Version)
$text = $text.Replace('{{DATE}}', $date)
$text = $text.Replace('{{WHAT_CHANGED}}', $whatChanged)
$text = $text.Replace('{{PREVIOUS_TAG}}', $previousTagText)
$text = $text.Replace('{{SIGNING_NOTE}}', $signingNote)

# Written without a byte order mark (Set-Content -Encoding utf8 adds one in Windows PowerShell 5.1).
$outPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutFile)
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) { New-Item -ItemType Directory -Force -Path $outDir | Out-Null }
[System.IO.File]::WriteAllText($outPath, $text, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "Wrote $OutFile (changes since $previousTagText)"
