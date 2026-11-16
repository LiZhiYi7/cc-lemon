@echo off
rem ????????? + ?????
setlocal
cd /d "%~dp0.."
set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
"%PS%" -NoProfile -ExecutionPolicy Bypass -File "%~dp0..\scripts\stop.ps1" -Mode all
if errorlevel 1 pause
endlocal
