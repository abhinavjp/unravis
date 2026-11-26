@echo off
rem SSH_ASKPASS helper for Invoke-RemoteSalt.ps1. OpenSSH runs it with the prompt text as %1.
rem Security: the prompt text can come from the server, so it is NEVER read or echoed here (no %1, no %~1).
rem The caller restricts ssh to plain password authentication, so the only question ever asked is the password.
rem The password comes from the child-process environment (UNRAVIS_SSH_PW) and is read inside PowerShell, so characters
rem such as & or % in it are never parsed by cmd. Full paths: nothing is looked up in the current directory.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -Command "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); [Console]::Out.Write($env:UNRAVIS_SSH_PW)"
