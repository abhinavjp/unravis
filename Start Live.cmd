@echo off
setlocal
title Unravis - Live
"%~dp0dist\unravis.exe" --port 0 %*
if errorlevel 1 (
  echo.
  echo Unravis could not start. See the error above.
  pause
)
