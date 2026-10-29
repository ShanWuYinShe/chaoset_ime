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

echo ==^> Registering TSF input method
set REGSVR=%WINDIR%\System32\regsvr32.exe
if exist "%WINDIR%\Sysnative\regsvr32.exe" set REGSVR=%WINDIR%\Sysnative\regsvr32.exe
if exist "%DLL%" (
  echo Registering 64-bit component: %DLL%
  "%REGSVR%" /s "%DLL%"
)
if exist "%WINDIR%\SysWOW64\regsvr32.exe" (
  if exist "ChaosetIME32.dll" (
    echo Registering 32-bit component: ChaosetIME32.dll
    "%WINDIR%\SysWOW64\regsvr32.exe" /s "%~dp0ChaosetIME32.dll"
  )
)

echo ==^> Enabling Chaoset in user language list
powershell -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$tip = '0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}'; $l = Get-WinUserLanguageList; $z = $l | Where-Object { $_.LanguageTag -like 'zh*' }; if ($z) { if (-not ($z.InputMethodTips -contains $tip)) { $z.InputMethodTips.Add($tip) } } else { $z = New-WinUserLanguageList zh-CN; $z[0].InputMethodTips.Add($tip); $l += $z[0] }; Set-WinUserLanguageList $l -Force" >nul 2>&1
taskkill /F /IM ctfmon.exe >nul 2>&1
start "" "%WINDIR%\System32\ctfmon.exe"

echo ==^> Granting loopback exemption to Start menu / Search hosts
REM AppContainer hosts (Start menu, search box) deny loopback connections by
REM default; exempt them so they can reach the daemon at 127.0.0.1:7949 and
REM convert pinyin there (2026-10-29: Start menu could only type English).
CheckNetIsolation LoopbackExempt -a -n=Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -a -n=Microsoft.Windows.Search_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -a -n=Microsoft.Windows.TextInputHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -a -n=Microsoft.Windows.ShellExperienceHost_cw5n1h2txyewy >nul 2>&1

echo ==^> Preparing user data directory %LOCALAPPDATA%\Chaoset
if not exist "%LOCALAPPDATA%\Chaoset" mkdir "%LOCALAPPDATA%\Chaoset"

echo ==^> Starting daemon
REM Kill any running instance first to avoid port conflicts on reinstall/upgrade.
taskkill /IM %DAEMON% /F >nul 2>&1
REM Copy dictionary to a fixed location and load it by absolute path, so the
REM daemon does not depend on whatever working directory it happens to start in
REM (same class of bug we hit on Linux).
copy /Y "%DICT%" "%LOCALAPPDATA%\Chaoset\dict.tsv" >nul
start "" "%DAEMON%" -tcp 127.0.0.1:7949 -dict "%LOCALAPPDATA%\Chaoset\dict.tsv" -userdict "%USERDICT%" -log "%LOG%"

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
set REGSVR=%WINDIR%\System32\regsvr32.exe
if exist "%WINDIR%\Sysnative\regsvr32.exe" set REGSVR=%WINDIR%\Sysnative\regsvr32.exe
if exist "%DLL%" "%REGSVR%" /s /u "%DLL%"
if exist "%WINDIR%\SysWOW64\regsvr32.exe" (
  if exist "ChaosetIME32.dll" "%WINDIR%\SysWOW64\regsvr32.exe" /s /u "%~dp0ChaosetIME32.dll"
)
powershell -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$l = Get-WinUserLanguageList; foreach ($x in $l) { $x.InputMethodTips.Remove('0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}') }; Set-WinUserLanguageList $l -Force" >nul 2>&1
taskkill /F /IM ctfmon.exe >nul 2>&1
start "" "%WINDIR%\System32\ctfmon.exe"
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.Search_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.TextInputHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.ShellExperienceHost_cw5n1h2txyewy >nul 2>&1
echo ==^> Removing dictionary copy
if exist "%LOCALAPPDATA%\Chaoset\dict.tsv" del /Q "%LOCALAPPDATA%\Chaoset\dict.tsv"
echo ==^> Uninstalled. Remove the Chaoset keyboard in Settings if it is still listed.
pause
exit /b 0
