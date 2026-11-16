@echo off
rem ????????? 0.0.0.0:8080?????????????
setlocal
cd /d "%~dp0.."
set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
"%PS%" -NoProfile -ExecutionPolicy Bypass -File "%~dp0..\scripts\start.ps1" -Mode prod
if errorlevel 1 pause
endlocal
