<!--
  Filled in by tools/release/New-ReleaseNotes.ps1 from a tag, the plain git log since the
  previous tag, and whether SIGNPATH was set for this run. A person edits the result before
  publishing the draft (see installer-and-releases.md section 9, "Release notes").
-->
# Unravis {{VERSION}}

Released {{DATE}}.

## What changed
{{WHAT_CHANGED}}

## Upgrade notes
Upgrading from {{PREVIOUS_TAG}} keeps your cases, saved connections and Blackbox recordings
exactly as they are; only the program files change. If the installer needs to close a running
Unravis, it asks first.

Downgrading to an older version is refused unless you pass `--allow-downgrade`.

## Verify your download
Each file above has a line in `SHA256SUMS.txt`. Check one with:

    Get-FileHash .\<file> -Algorithm SHA256

and compare the result with the matching line.

{{SIGNING_NOTE}}
