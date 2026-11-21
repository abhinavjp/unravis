@echo off
setlocal
title Unravis - Releases
rem Read-only releases dashboard. Builds it first if needed (Go must be installed), then opens it in the browser.
if not exist "%~dp0dist\unravis-releases.exe" (
  pushd "%~dp0"
  go build -trimpath -o dist\unravis-releases.exe .\cmd\unravis-releases || (echo Build failed. & pause & exit /b 1)
  popd
)
"%~dp0dist\unravis-releases.exe" --open %*
if errorlevel 1 pause
