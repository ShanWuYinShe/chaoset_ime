@echo off
REM chaoset Windows installer (TSF input method + daemon)
REM
REM Usage: right-click "Run as administrator" on this file
REM        Uninstall: install.bat uninstall
REM
REM TSF input methods must be registered via COM (regsvr32), hence admin rights.
REM The daemon uses the local loopback TCP form; shell <-> daemon talk on 127.0.0.1:7949.
setlocal enabledelayedexpansion
cd /d "%~dp0"

set DLL=ChaosetIME.dll
set DAEMON=chaosetd.exe
set DICT=dict.tsv
set USERDICT=%LOCALAPPDATA%\Chaoset\userdict.json
set LOG=%LOCALAPPDATA%\Chaoset\chaosetd.log

net session >nul 2>&1
if errorlevel 1 (
  echo ERROR: administrator rights required.
  echo Right-click this file and choose "Run as administrator".
  pause
  exit /b 1
)

if /i "%1"=="uninstall" goto uninstall

echo ==^> Checking required files
if not exist "%DLL%"    ( echo ERROR: %DLL% missing & pause & exit /b 1 )
if not exist "%DAEMON%" ( echo ERROR: %DAEMON% missing & pause & exit /b 1 )
if not exist "%DICT%"   ( echo ERROR: dictionary %DICT% missing, package incomplete & pause & exit /b 1 )

echo ==^> Registering TSF input method: regsvr32 %DLL%
regsvr32 /s "%DLL%"
if errorlevel 1 (
  echo ERROR: regsvr32 failed. Make sure %DLL% is in the same folder and you are admin.
  pause
  exit /b 1
)

echo ==^> Preparing user data directory %LOCALAPPDATA%\Chaoset
if not exist "%LOCALAPPDATA%\Chaoset" mkdir "%LOCALAPPDATA%\Chaoset"

echo ==^> Starting daemon
REM Kill any running instance first to avoid port conflicts on reinstall/upgrade.
taskkill /IM %DAEMON% /F >nul 2>&1
REM Copy dictionary to a fixed location and load it by absolute path, so the
REM daemon does not depend on whatever working directory it happens to start in
REM (same class of bug we hit on Linux).
copy /Y "%DICT%" "%LOCALAPPDATA%\Chaoset\dict.tsv" >nul
start "" /B "%DAEMON%" -tcp 127.0.0.1:7949 -dict "%LOCALAPPDATA%\Chaoset\dict.tsv" -userdict "%USERDICT%" >> "%LOG%" 2>&1

echo.
echo ==^> Install complete
echo     1. Open Settings ^> Time ^& language ^> Language ^& region
echo     2. Under Chinese (Simplified) click "Add a keyboard" and pick Chaoset
echo     3. Switch with Win+Space to start typing
echo.
echo     daemon log: %LOG%
echo     Uninstall: run this file as administrator with parameter "uninstall"
echo.
echo If Chaoset does not show up in the list, sign out and sign in again.
pause
exit /b 0

:uninstall
echo ==^> Stopping daemon
taskkill /IM %DAEMON% /F >nul 2>&1
echo ==^> Unregistering TSF input method
regsvr32 /s /u "%DLL%"
echo ==^> Removing dictionary copy
if exist "%LOCALAPPDATA%\Chaoset\dict.tsv" del /Q "%LOCALAPPDATA%\Chaoset\dict.tsv"
echo ==^> Uninstalled. Remove the Chaoset keyboard in Settings if it is still listed.
pause
exit /b 0
