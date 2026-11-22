<# Proves the npm release-age cooldown in web/.npmrc (BL-335, spec 02 2.10).
   Finds a version of -Package published less than 3 days ago and checks that `npm install` of that exact
   version in web/ is refused. Needs npm 11.10 or newer and network access. Exit 0 = refused (proved),
   1 = it installed (policy broken), 2 = no young version exists right now (cannot prove; try another -Package). #>
param([string]$Package = 'typescript')
$ErrorActionPreference = 'Stop'
$web = Join-Path $PSScriptRoot '..\..\web'
$times = npm view $Package time --json | ConvertFrom-Json
$cut = (Get-Date).ToUniversalTime().AddDays(-3)
$young = $times.PSObject.Properties | Where-Object { $_.Name -match '^\d+\.\d+\.\d+$' -and ([datetime]$_.Value).ToUniversalTime() -gt $cut } | Select-Object -Last 1
if (-not $young) { Write-Host "No $Package version is younger than 3 days; cannot prove. Try another -Package."; exit 2 }
Write-Host "Trying $Package@$($young.Name) published $($young.Value)"
Push-Location $web
try {
    npm install --no-save --dry-run "$Package@$($young.Name)"
    if ($LASTEXITCODE -eq 0) { Write-Host 'FAIL: a version younger than 3 days was accepted.'; exit 1 }
    Write-Host 'OK: refused by min-release-age.'; exit 0
} finally { Pop-Location }
