<#
.SYNOPSIS
  Fills .github/release-notes-template.md in from a tag and the plain git log since the
  previous tag. A person still edits the result in the draft release before publishing
  (installer-and-releases.md section 9).

.DESCRIPTION
  No GitHub API call: "what changed" comes from `git log <previous tag>..<tag>`, one bullet per
  commit subject, oldest first. The first release (no previous tag reachable from this one) gets
  the full log instead and says so in the upgrade notes.

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

function Invoke-Git {
    param([string[]]$GitArgs)
    $out = & git -C $RepoRoot @GitArgs 2>$null
    if ($LASTEXITCODE -ne 0) { return $null }
    return $out
}

# The nearest tag reachable from this tag's parent commit, i.e. the release before this one.
$previousTag = Invoke-Git @('describe', '--tags', '--abbrev=0', "$Tag^") | Select-Object -First 1

if ($previousTag) {
    $range = "$previousTag..$Tag"
    $previousTagText = $previousTag
} else {
    $range = $Tag
    $previousTagText = 'the first release'
}

$commits = Invoke-Git @('log', $range, '--no-merges', '--reverse', '--pretty=format:- %s (%h)')
$whatChanged = if ($commits) { ($commits -join "`n") } else { '- No commits since the previous tag.' }

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

$date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-dd')

$text = Get-Content -LiteralPath $TemplatePath -Raw
$text = $text.Replace('{{VERSION}}', $Version)
$text = $text.Replace('{{DATE}}', $date)
$text = $text.Replace('{{WHAT_CHANGED}}', $whatChanged)
$text = $text.Replace('{{PREVIOUS_TAG}}', $previousTagText)
$text = $text.Replace('{{SIGNING_NOTE}}', $signingNote)

$outDir = Split-Path -Parent $OutFile
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) { New-Item -ItemType Directory -Force -Path $outDir | Out-Null }
$text | Set-Content -Encoding utf8 -LiteralPath $OutFile
Write-Host "Wrote $OutFile (changes since $previousTagText)"
