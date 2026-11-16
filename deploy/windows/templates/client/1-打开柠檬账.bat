@echo off
rem ============================================================
rem  Lemon Accounting - client launcher (ASCII only)
rem  Edit server.ini to set the server address, then run this.
rem ============================================================
setlocal enabledelayedexpansion
cd /d "%~dp0"

set "LEMON_URL="
if exist "server.ini" (
  for /f "usebackq tokens=1,* delims==" %%a in ("server.ini") do (
    if /i "%%a"=="url" set "LEMON_URL=%%b"
  )
)

if "%LEMON_URL%"=="" (
  echo.
  echo   [ERROR] server.ini not found, or "url" is empty.
  echo.
  echo   Open server.ini with Notepad and set one line, for example:
  echo       url=http://192.168.1.100:8080
  echo.
  pause
  exit /b 1
)

if not exist "%~dp0firefox\firefox.exe" (
  echo.
  echo   [ERROR] The firefox folder is missing.
  echo   Please extract the whole zip again.
  echo.
  pause
  exit /b 1
)

echo.
echo   Opening %LEMON_URL% ...
start "" "%~dp0firefox\firefox.exe" -no-remote -profile "%~dp0profile" "%LEMON_URL%"
exit /b 0
