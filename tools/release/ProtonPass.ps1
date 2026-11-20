# Reads one secret from the Proton Pass CLI (pass-cli) for the release scripts. Dot-source it:  . ./tools/release/ProtonPass.ps1
#
# Rules this file keeps:
#  - The secret goes from pass-cli's output straight into a SecureString. It is never written to disk, an environment variable, a
#    command line, a log or the console, and no error message ever contains what pass-cli printed.
#  - It fails closed: a missing or logged-out pass-cli, a locked session, a missing item, an empty answer, an answer that is not
#    exactly one line, or a CLI that takes longer than the timeout all throw. The caller decides what to do (the release scripts
#    fall back to typing the passphrase). It never guesses.
#  - pass-cli gets no input (stdin is closed), so a lock or login prompt cannot hang the release.
#
# Reference: pass://<vault>/<item>/<field>, for example pass://Release/Unravis rc-1 key/password.

function Test-ProtonPassReference([string]$Reference) {
    # One reference of the documented form, no flags or extra words that could be read as options.
    return ($Reference -match '^pass://[^/\s][^/]*/[^/\s][^/]*/[A-Za-z0-9_.][A-Za-z0-9_. -]*$') -and -not $Reference.StartsWith('-')
}

function Get-ProtonPassSecret {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)] [string]$Reference,
        # Tests point these at a fake; real runs leave them out (pass-cli from PATH).
        [string]$CliPath,
        [string[]]$CliArgsPrefix = @(),
        [int]$TimeoutSec = 30
    )
    if (-not (Test-ProtonPassReference $Reference)) {
        throw 'The Proton Pass reference must look like pass://Vault/Item/password.'
    }
    if (-not $CliPath) {
        $cmd = Get-Command 'pass-cli' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $cmd) { throw 'pass-cli is not installed or not on PATH (install: https://protonpass.github.io/pass-cli/get-started/installation/).' }
        $CliPath = $cmd.Source
    }
    $psi = [System.Diagnostics.ProcessStartInfo]::new($CliPath)
    foreach ($a in $CliArgsPrefix) { $psi.ArgumentList.Add($a) }
    foreach ($a in @('item', 'view', $Reference)) { $psi.ArgumentList.Add($a) }
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    # pass-cli writes UTF-8; without this .NET would use the console code page and garble non-ASCII passphrases.
    $psi.StandardOutputEncoding = [System.Text.UTF8Encoding]::new($false)
    $psi.StandardErrorEncoding = [System.Text.UTF8Encoding]::new($false)
    $p = $null
    try {
        try { $p = [System.Diagnostics.Process]::Start($psi) } catch { throw 'pass-cli could not be started.' }
        $p.StandardInput.Close()
        $outTask = $p.StandardOutput.ReadToEndAsync()
        $errTask = $p.StandardError.ReadToEndAsync()
        $clock = [System.Diagnostics.Stopwatch]::StartNew()
        $timedOut = -not $p.WaitForExit($TimeoutSec * 1000)
        if (-not $timedOut) {
            # The process ended; the pipes must close too (a helper that inherited them would otherwise hang us). Same overall limit.
            $left = [Math]::Max(1, $TimeoutSec * 1000 - [int]$clock.ElapsedMilliseconds)
            $timedOut = -not [System.Threading.Tasks.Task]::WaitAll(@($outTask, $errTask), $left)
        }
        if ($timedOut) {
            try { $p.Kill($true) } catch {}
            throw "pass-cli did not answer within $TimeoutSec seconds (a locked or logged-out session can wait for input). Run 'pass-cli login' or 'pass-cli session unlock' in a normal window."
        }
        $p.WaitForExit()
        $raw = $outTask.GetAwaiter().GetResult()
        $null = $errTask.GetAwaiter().GetResult()   # read and dropped: it can name the item
        if ($p.ExitCode -ne 0) { throw "pass-cli failed (exit code $($p.ExitCode)). It is probably logged out, locked, or the item or field was not found. Check with 'pass-cli info'." }
        if ([string]::IsNullOrEmpty($raw)) { throw 'pass-cli returned nothing for that reference.' }
        $value = $raw.TrimEnd("`r", "`n")
        $raw = $null
        if ($value.Length -eq 0) { throw 'pass-cli returned an empty value.' }
        if ($value.Contains("`n") -or $value.Contains("`r")) {
            $value = $null
            throw 'pass-cli returned more than one line, so it is not a bare passphrase. Test the reference with tools/release/Test-ProtonPass.ps1.'
        }
        $secure = [System.Security.SecureString]::new()
        foreach ($c in $value.ToCharArray()) { $secure.AppendChar($c) }
        $value = $null
        $secure.MakeReadOnly()
        return $secure
    } finally {
        if ($p) { $p.Dispose() }
    }
}
