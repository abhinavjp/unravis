<#
.SYNOPSIS
  Signs programs with Authenticode through SignPath, in place. Used by build.ps1 -SignScript, for STABLE releases only.

.DESCRIPTION
  UNTESTED against a real SignPath account (nobody has one approved yet; see docs/auto-updater/SPEC.md D14 and BL-155). It is
  wired but gated: release.yml passes it to build.ps1 only for a stable tag (no "-" in the tag) when the SIGNPATH secret is set.
  Release candidates are never signed here.

  Needs: the SignPath PowerShell module (installed from the PowerShell Gallery if missing) and these settings, from the
  workflow: SIGNPATH_API_TOKEN (secret), SIGNPATH_ORGANIZATION_ID, SIGNPATH_PROJECT_SLUG, SIGNPATH_POLICY_SLUG (repository
  variables), and optionally SIGNPATH_ARTIFACT_CONFIG_SLUG.

  A file is replaced only after Windows reports its signature as Valid. Any failure stops the build: a signed release must never
  carry an unsigned program under a "signed" notice.

  Known risk: SignPath Foundation may require the GitHub origin-verified submission (their GitHub action) instead of this
  module. If the first real run is refused for that reason, the signing call here is the one place to change; build.ps1 does not care.
#>
param(
    [Parameter(Mandatory)] [string[]]$Path
)
$ErrorActionPreference = 'Stop'

foreach ($name in 'SIGNPATH_API_TOKEN', 'SIGNPATH_ORGANIZATION_ID', 'SIGNPATH_PROJECT_SLUG', 'SIGNPATH_POLICY_SLUG') {
    if (-not [Environment]::GetEnvironmentVariable($name)) { throw "Sign-Binaries: $name is not set, so nothing can be signed." }
}
if (-not (Get-Module -ListAvailable -Name SignPath)) {
    Install-Module -Name SignPath -Scope CurrentUser -Force -Repository PSGallery
}
Import-Module SignPath

foreach ($file in $Path) {
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Sign-Binaries: $file does not exist." }
    $full = (Resolve-Path -LiteralPath $file).Path
    $out = "$full.signed"
    Remove-Item -LiteralPath $out -Force -ErrorAction SilentlyContinue
    $args = @{
        InputArtifactPath  = $full
        OutputArtifactPath = $out
        ApiToken           = $env:SIGNPATH_API_TOKEN
        OrganizationId     = $env:SIGNPATH_ORGANIZATION_ID
        ProjectSlug        = $env:SIGNPATH_PROJECT_SLUG
        SigningPolicySlug  = $env:SIGNPATH_POLICY_SLUG
        WaitForCompletion  = $true
    }
    if ($env:SIGNPATH_ARTIFACT_CONFIG_SLUG) { $args.ArtifactConfigurationSlug = $env:SIGNPATH_ARTIFACT_CONFIG_SLUG }
    Submit-SigningRequest @args
    if (-not (Test-Path -LiteralPath $out -PathType Leaf)) { throw "Sign-Binaries: SignPath returned no file for $file." }
    $sig = Get-AuthenticodeSignature -LiteralPath $out
    if ($sig.Status -ne 'Valid' -or -not $sig.SignerCertificate) { throw "Sign-Binaries: the signed $file does not have a valid signature (status $($sig.Status))." }
    Move-Item -LiteralPath $out -Destination $full -Force
    Write-Host "signed $file ($($sig.SignerCertificate.Subject))"
}
