@echo off
rem ???????? backups\prod\
setlocal
cd /d "%~dp0.."
set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
"%PS%" -NoProfile -ExecutionPolicy Bypass -File "%~dp0..\scripts\backup.ps1" -Mode prod
if errorlevel 1 pause
endlocal
