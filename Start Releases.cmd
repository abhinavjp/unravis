@echo off
setlocal
title Unravis - Releases
rem Read-only releases dashboard. With Go installed it is rebuilt every start (quick when nothing changed), so a git pull is picked up.
where go >nul 2>nul
if not errorlevel 1 (
  pushd "%~dp0"
  go build -trimpath -o dist\unravis-releases.exe .\cmd\unravis-releases || (echo Build failed. & pause & exit /b 1)
  popd
)
if not exist "%~dp0dist\unravis-releases.exe" (echo Go is not installed and there is no built copy. & pause & exit /b 1)
"%~dp0dist\unravis-releases.exe" --open %*
if errorlevel 1 pause
