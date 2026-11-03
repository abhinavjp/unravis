# Deterministic plan checks. Run from anywhere: powershell -NoProfile -File .forge/investigation-timeline/check-plan.ps1
# Prints problems, then OK or FAIL. Exit 0 on OK, 1 on FAIL.
# ERROR (fails): packet files vs plan.md section 3 rows, six headings, risk/size, header `wave:` equal to section 3,
#   `## Depends on` equal to section 3 ('all other packets' rows: the packet says exactly that and sits after every
#   other packet), deps in earlier waves, phase.md tables without wave/dependency columns and listing exactly the
#   folder's packets. OVERLAP: same-wave write-scope overlaps (run serially). WARN (does not fail): an edge from an
#   earlier-numbered phase to a later one, and wave numbers named in phase.md prose.
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$plan = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $root 'plan.md')
$errs = New-Object System.Collections.Generic.List[string]

# 1. Dependency table from plan.md section 3 (| WP-xx | deps | wave | phase |)
$table = @{}
foreach ($m in [regex]::Matches($plan, '(?m)^\|\s*(WP-[0-9]+[a-z]?)\s*\|\s*([^|]*?)\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|')) {
    $id = $m.Groups[1].Value
    $d = $m.Groups[2].Value.Trim()
    $deps = @()
    if ($d -ne '' -and $d -notmatch '^\W*$' -and $d -ne 'none' -and $d -ne 'all other packets') {
        $deps = @($d -split '\s*,\s*' | ForEach-Object { if ($_ -match '^\d') { "WP-$_" } else { $_ } })
    }
    $table[$id] = [pscustomobject]@{ Deps = $deps; Wave = [int]$m.Groups[3].Value; Phase = $m.Groups[4].Value; All = ($d -eq 'all other packets') }
}
if ($table.Count -eq 0) { $errs.Add('no packet table parsed from plan.md') }

# 2. Packet files
$files = @{}
Get-ChildItem -LiteralPath (Join-Path $root 'phases') -Recurse -Filter 'WP-*.md' -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.Name -match '^(WP-[0-9]+[a-z]?)-') { $files[$Matches[1]] = $_ } else { $errs.Add("bad packet file name: $($_.FullName)") }
}
foreach ($id in $table.Keys) { if (-not $files.ContainsKey($id)) { $errs.Add("$id : packet file missing") } }
foreach ($id in $files.Keys) { if (-not $table.ContainsKey($id)) { $errs.Add("$id : packet file not in plan.md table (split packet? update plan.md)") } }

$headings = 'Outcome', 'Write scope', 'Must not change', 'Changes', 'Proof', 'Depends on'
$scopes = @{}
foreach ($id in ($files.Keys | Sort-Object)) {
    $f = $files[$id]
    $txt = Get-Content -Raw -Encoding UTF8 -LiteralPath $f.FullName
    $hs = @([regex]::Matches($txt, '(?m)^## (.+?)\s*$') | ForEach-Object { $_.Groups[1].Value })
    if (($hs -join '|') -ne ($headings -join '|')) { $errs.Add("$id : headings are [$($hs -join ', ')], expected [$($headings -join ', ')]") }
    if ($txt -notmatch '(?m)^risk:\s*(low|medium|high)\b') { $errs.Add("$id : missing 'risk:' line") }
    if ($txt -notmatch '(?m)\bsize:\s*(S|M)\b') { $errs.Add("$id : missing 'size:' value") }
    if ($f.Directory.Name -notmatch '^\d\d-') { $errs.Add("$id : not under phases/NN-slug/") }
    elseif ($table.ContainsKey($id) -and ($f.Directory.Name.Substring(0, 2) -ne $table[$id].Phase.PadLeft(2, '0'))) { $errs.Add("$id : in folder $($f.Directory.Name) but plan.md says phase $($table[$id].Phase)") }
    # Depends on section
    $dm = [regex]::Match($txt, '(?s)## Depends on\s*(.*)$')
    $depTxt = $dm.Groups[1].Value
    $got = @([regex]::Matches($depTxt, 'WP-[0-9]+[a-z]?') | ForEach-Object { $_.Value } | Sort-Object -Unique)
    if ($table.ContainsKey($id) -and -not $table[$id].All) {
        $want = @($table[$id].Deps | Sort-Object -Unique)
        if (($got -join ',') -ne ($want -join ',')) { $errs.Add("$id : Depends on [$($got -join ',')] != plan.md [$($want -join ',')]") }
    }
    # 'all other packets' rows: the packet says exactly that (no id list that can go stale)
    if ($table.ContainsKey($id) -and $table[$id].All -and $depTxt.Trim() -ne 'all other packets') { $errs.Add("$id : plan.md says 'all other packets'; its ## Depends on must be exactly 'all other packets'") }
    # Header wave must equal plan.md section 3
    $wm = [regex]::Match($txt, '(?m)^risk:.*?\bwave:\s*(\d+)')
    if (-not $wm.Success) { $errs.Add("$id : header line has no 'wave: N'") }
    elseif ($table.ContainsKey($id) -and [int]$wm.Groups[1].Value -ne $table[$id].Wave) { $errs.Add("$id : header wave $($wm.Groups[1].Value) != plan.md wave $($table[$id].Wave)") }
    # Write scope paths
    $sm = [regex]::Match($txt, '(?s)## Write scope\s*(.*?)\r?\n## Must not change')
    $paths = @([regex]::Matches($sm.Groups[1].Value, '`([^`]+)`') | ForEach-Object { $_.Groups[1].Value.Replace('\', '/') })
    if ($paths.Count -eq 0) { $errs.Add("$id : Write scope lists no backticked paths") }
    $scopes[$id] = $paths
    if ($txt -match '```(go|ts|js|svelte)\b[\s\S]{600,}?```') { $errs.Add("$id : large pasted code block (plan rule: no pasted code)") }
}

# 3. Cycle check and level check
$warns = New-Object System.Collections.Generic.List[string]
foreach ($id in $table.Keys) {
    foreach ($d in $table[$id].Deps) {
        if (-not $table.ContainsKey($d)) { $errs.Add("$id : unknown dependency $d") }
        elseif ($table[$d].Wave -ge $table[$id].Wave) { $errs.Add("$id (wave $($table[$id].Wave)) depends on $d (wave $($table[$d].Wave)): dependency must be in an earlier wave") }
        elseif ([int]$table[$d].Phase -gt [int]$table[$id].Phase) { $warns.Add("phase order: $id (phase $($table[$id].Phase)) depends on $d (phase $($table[$d].Phase)); phases run in number order, so this edge cannot be met inside phase $($table[$id].Phase)") }
    }
    if ($table[$id].All) {
        foreach ($o in $table.Keys) { if ($o -ne $id -and $table[$o].Wave -ge $table[$id].Wave) { $errs.Add("$id ('all other packets', wave $($table[$id].Wave)) must be after $o (wave $($table[$o].Wave))") } }
    }
}

# 3b. phase.md: packet list with titles only. Waves and dependencies live only in plan.md section 3
#     and the packet files, so a phase.md table with a Wave or Depends-on column fails; the WP rows
#     of phase.md tables must equal the packet files of that folder.
Get-ChildItem -LiteralPath (Join-Path $root 'phases') -Directory | ForEach-Object {
    $pm = Join-Path $_.FullName 'phase.md'
    if (-not (Test-Path -LiteralPath $pm)) { $errs.Add("$($_.Name) : phase.md missing"); return }
    $ln = 0; $listed = @()
    foreach ($line in (Get-Content -Encoding UTF8 -LiteralPath $pm)) {
        $ln++
        if ($line -match '^\s*\|') {
            if ($line -match '(?i)\|\s*(wave|waves|depends on|depends|deps)\s*\|') { $errs.Add("$($_.Name)/phase.md:$ln : table has a wave or dependency column (keep them only in plan.md section 3)") }
            if ($line -match '^\s*\|\s*(WP-[0-9]+[a-z]?)\s*\|') { $listed += $Matches[1] }
        }
        elseif ($line -match '(?i)\bwave\s+\d+|\(w\d+\)|\bw\d+\b') { $warns.Add("$($_.Name)/phase.md:$ln : names a wave number in prose (may go stale; plan.md section 3 is the source)") }
    }
    $dirName = $_.Name
    $have = @($files.Keys | Where-Object { $files[$_].Directory.Name -eq $dirName } | Sort-Object)
    $listed = @($listed | Sort-Object -Unique)
    if (($listed -join ',') -ne ($have -join ',')) { $errs.Add("$dirName/phase.md : packet rows [$($listed -join ',')] != packet files [$($have -join ',')]") }
}

# 4. Same-wave write-scope overlap
$overlaps = New-Object System.Collections.Generic.List[string]
$ids = @($scopes.Keys | Sort-Object)
for ($i = 0; $i -lt $ids.Count; $i++) {
    for ($j = $i + 1; $j -lt $ids.Count; $j++) {
        $a = $ids[$i]; $b = $ids[$j]
        if (-not ($table.ContainsKey($a) -and $table.ContainsKey($b))) { continue }
        if ($table[$a].Wave -ne $table[$b].Wave) { continue }
        $both = @($scopes[$a] | Where-Object { $scopes[$b] -contains $_ })
        if ($both.Count -gt 0) { $overlaps.Add("wave $($table[$a].Wave): $a / $b share $($both -join ', ')") }
    }
}

foreach ($e in $errs) { Write-Output "ERROR $e" }
foreach ($o in $overlaps) { Write-Output "OVERLAP $o" }
foreach ($w in $warns) { Write-Output "WARN $w" }
Write-Output "packets in plan: $($table.Count); packet files: $($files.Count); errors: $($errs.Count); same-wave overlaps: $($overlaps.Count); warnings: $($warns.Count)"
if ($errs.Count -eq 0) { Write-Output 'OK'; exit 0 } else { Write-Output 'FAIL'; exit 1 }
