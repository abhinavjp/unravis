@echo off
setlocal
title API Audit Explorer - Live
"%~dp0dist\api-audit-explorer.exe" --port 0 %*
if errorlevel 1 (
  echo.
  echo API Audit Explorer could not start. See the error above.
  pause
)
