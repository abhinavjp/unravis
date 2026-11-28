# Update Unravis on BG-SRV-UK-01

Add `BG-SRV-UK-01` to `SALT_MINION_TARGETS` in your remote tool settings. For `UpdateStatus`, append `D:\UnravisBlackbox`, `D:\UnravisUpdate`, and `C:\Users\btlsupport1\AppData\Local\Programs\Unravis` to `REMOTE_LOG_PATHS`. Status works in readonly mode and reports the Blackbox manifest version, the app version recorded by Setup, the presence of the staging directory, and app `.old` backups.

```powershell
$tool = '.\tools\remote\Invoke-RemoteSalt.ps1'
& $tool -Action UpdateStatus -Target BG-SRV-UK-01
```

Set `REMOTE_MODE=write` for changes. Use the same release tag for each action. Each request prints a separate single-use hash valid for 60 minutes. Use the hash and the approval message reference after receiving approval for that action.

```powershell
& $tool -Action WriteRequest -Target BG-SRV-UK-01 -Fn update.fetch -Name v0.1.0-rc.68
& $tool -Action WriteApprove -Hash '<hash from request>' -ApprovalNote '<approval message reference>'
```

| Action | Effect |
| --- | --- |
| `update.fetch` | Downloads the two release ZIPs and `SHA256SUMS.txt`, verifies both hashes, and extracts under `D:\UnravisUpdate\<tag>`. Fetch again to replace staging for the same tag. Staging must be writable only by the Salt account, Administrators, or SYSTEM. |
| `update.blackbox` | Runs the verified recorder’s updater. It preserves settings, the read key, and recordings, and keeps the previous installed executable as `unravis-blackbox.exe.old`. |
| `update.app` | Stops the app from the per-user install path and replaces the managed executables and optional `bin` directory supplied by the archive. It preserves `setup-state.json` and writes old and new SHA-256 hashes to `update-<tag>.txt`. The version recorded by Setup can still show the last Setup install. |
| `update.app-start` | Starts the app through a one-shot task if `btlsupport1` has an active interactive session. The app also starts at the user’s next sign-in. |
| `update.rollback` | Restores the app’s latest `.old` backups. For Blackbox, manually restore the installed `unravis-blackbox.exe.old` as the service executable. Request `update.app-start` separately to start the restored app. |
| `update.cleanup` | Removes only the staging directory for the specified tag. |

Run fetch before either component update. After updating, check `UpdateStatus` and the app hash receipt before cleaning up. On a timeout, check the component and its backups before requesting another write.

Note: Windows refuses a command line over 32,767 characters, and Salt re-encodes a PowerShell command to about 2.7 times its length. Each recipe is therefore gzipped and sent as a short loader line (2 to 4.5 KB; under 12 KB after Salt wraps it). A 13 KB uncompressed command failed on the live minion with "The command line is too long." before anything changed. The offline tests fail if a recipe wraps to 30,000 characters or more.
