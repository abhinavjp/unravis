@echo off
setlocal
rem Unravis Releases: a small read-only desktop window. With Go installed it is rebuilt on every start (quick when nothing changed),
rem so a git pull is picked up. The program has no console; if it cannot start, the message is shown here.
where go >nul 2>nul
if not errorlevel 1 (
  pushd "%~dp0"
  go build -trimpath -ldflags "-H=windowsgui" -o dist\unravis-releases.exe .\cmd\unravis-releases || (echo Build failed. & pause & exit /b 1)
  popd
)
if not exist "%~dp0dist\unravis-releases.exe" (echo Go is not installed and there is no built copy. & pause & exit /b 1)
start "" "%~dp0dist\unravis-releases.exe" %*
