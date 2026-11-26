@echo off
rem SSH_ASKPASS helper for Invoke-RemoteSalt.ps1. OpenSSH runs it with the prompt text as %1.
rem It answers only a password prompt, from the child-process environment (UNRAVIS_SSH_PW), and prints nothing else.
rem The value is read inside PowerShell so characters such as & or % in a password are never parsed by cmd.
echo %~1 | findstr /i /c:"password" >nul || exit /b 1
powershell.exe -NoProfile -NonInteractive -Command "[Console]::Out.Write($env:UNRAVIS_SSH_PW)"
