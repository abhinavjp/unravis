# Remote Salt access (read-only by default)

A small PowerShell tool in `tools/remote/` that lets Claude (or you) run a short list of read-only Salt commands on the
Salt master over SSH and pull logs back for debugging. It uses the built-in Windows OpenSSH client (`ssh.exe`), so nothing
else is installed. It works in Windows PowerShell 5.1 and PowerShell 7.

## Setup (once)

1. `powershell -File tools\remote\Initialize-RemoteEnv.ps1` creates `%USERPROFILE%\.unravis-remote\.env` from
   `tools/remote/remote.env.example`, makes it readable by your Windows user only, and opens it in Notepad.
2. Type the values yourself. The file lives outside the repo and outside any synced folder (the tool refuses a `.env`
   inside a git repo or OneDrive/Dropbox). Never paste the password anywhere else.
3. Prefer a key (`SSH_KEY_PATH`, OpenSSH format). A PuTTY `.ppk` key must be exported first: PuTTYgen, Conversions,
   Export OpenSSH key. Set either `SSH_KEY_PATH` or `SSH_PASSWORD`, not both.
4. Trust the server once (no silent trust-on-first-use):
   - `Invoke-RemoteSalt.ps1 -Action ShowHostKey` prints the server's key fingerprints. It probes with `ssh` itself, because the Windows `ssh-keyscan` (9.5) cannot negotiate with newer servers (OpenSSH 10).
   - Compare with what you know is right (for example from the server console), put it in `SSH_HOST_KEY_FINGERPRINT`,
     then `Invoke-RemoteSalt.ps1 -Action TrustHost`. It saves the key only if it matches the pinned fingerprint.
5. `-Action Check` tests the connection. `-Action Keys` lists minions. Put the minion id(s) in `SALT_MINION_TARGETS`.

## `.env` fields

| Field | Meaning |
|---|---|
| `SALT_MASTER_HOST` | Master hostname or IP. |
| `SSH_PORT` | Default 22. |
| `SSH_USER` | SSH login. |
| `SSH_PASSWORD` / `SSH_KEY_PATH` | One of the two. The password is passed to ssh only through a child-process environment variable (askpass helper), never on a command line. |
| `SSH_HOST_KEY_FINGERPRINT` | `SHA256:...` of the master's key. Required to trust a host. |
| `SALT_MINION_TARGETS` | Comma list of exact minion ids Salt commands may target. No globs. Empty means no minion commands. |
| `SALT_USE_SUDO` | Default true: runs `sudo -n salt ...`. If sudo needs a password the tool stops and says so; it never feeds the password to sudo. |
| `REMOTE_LOG_PATHS` | Comma list of folders log reads may touch. Empty means no file reads. |
| `REMOTE_MODE` | `readonly` (default) or `write`. |

## Running things

```powershell
$t = 'tools\remote\Invoke-RemoteSalt.ps1'
& $t -Action Check
& $t -Action Salt -Target web01 -Fn test.ping
& $t -Action Salt -Target web01 -Fn grains.item -FnArgs kernel   # one grain per call when started with -File
& $t -Action SaltRun -Fn manage.up
& $t -Action Run -Target web01 -Cmd 'tail -n 50 /var/log/unravis/app.log'   # no quotes or spaces inside paths
& $t -Action LogList -Target web01 -Path 'C:\ProgramData\Unravis' -Os windows
& $t -Action LogTail -Target web01 -Path 'C:\ProgramData\Unravis\app.log' -Lines 200 -Os windows
& $t -Action LogGrep -Target web01 -Path '...' -Pattern 'ERROR'
& $t -Action EventLog -Target web01 -LogName Application -Lines 50
& $t -Action Audit        # what was run, newest last
```

Add `-DryRun` to any action to see exactly what would be sent without connecting. Output longer than `-MaxPrint` is
saved under `~/.unravis-remote/pulled/` (newest 20 kept) and the path is printed.

## Safety rules

- **Read-only by default.** Allowed: `test.ping`, `test.version`, `grains.*`, `status.*`, `service.status`,
  `service.get_all`, `file.file_exists`, `file.stats`, `file.read` (inside `REMOTE_LOG_PATHS`), a short `salt-run`
  list (`manage.*`, `jobs.*`), and `cmd.run` limited to `tail head cat type Get-Content ls dir Get-ChildItem` on
  allowed paths. `-Cmd` has a strict character set (no quotes at all, `\` only inside drive-letter paths) and each
  command has its own allowed flags (`tail`/`head`: `-n`/`-c` plus a number; `Get-Content`: `-Tail`, `-TotalCount`,
  `-Raw`). Follow modes (`-f`, `-Wait`) are refused. Prefer the recipes (`LogTail`, `LogGrep`, `LogList`, `EventLog`).
- Everything else is refused and logged: `state.apply`, other `cmd.*`, installs, file writes/deletes, reboots,
  globs and compound targets.
- Every word is single-quoted for the master's shell, and arguments Salt would parse as YAML (`": "`, `" #"`) are rejected.
- **Write mode** is tiny: `service.start|stop|restart` of one non-protected service, only when `REMOTE_MODE=write`.
  `-Action WriteRequest` prints the exact command and a hash and runs nothing. `-Action WriteApprove -Hash <h>
  -ApprovalNote '<quote of the owner's go in the thread>'` runs it. A pending request is valid 60 minutes, is used once,
  and is checked against its hash. **This gate relies on the operator following the process; it is not a technical lock**
  (anyone who can run the script on this PC with write mode on can approve). Keep `REMOTE_MODE=readonly` unless a change is
  actually needed. Remote logins, `sshd`, `salt-minion` and other access-critical services are protected. Nothing here
  applies Salt states.
- **Audit:** every attempt, run or refused, is a JSON line in `~/.unravis-remote/audit.log` (256 KB cap, one rotation),
  with the password and password/token-looking text redacted. Output is redacted the same way before it is printed or saved.
- The `.env` and `~/.unravis-remote` are private to your Windows user (checked by SID on every run).

## Verified live (2026-11-26, one master with 27 Windows minions)

Password login through askpass (password-only auth), pinned host key, `sudo -n salt`, `salt-key -L`, `test.ping`,
`grains.item`, `service.*`, `salt-run manage.up`, and the `LogList`, `LogTail`, `LogGrep` and `EventLog` recipes on a
Windows minion. Not yet exercised on a real server: write mode and any Linux minion recipes. If sudo on the master
asks for a password, the tool stops and says so; fix it with a sudoers rule for the SSH user, not by sharing the password.