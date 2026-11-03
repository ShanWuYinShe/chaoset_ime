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

echo ==^> Cleaning legacy UILess category (v0.2.16 and earlier)
REM v0.2.17 no longer declares TFCAT_TIPCAP_UIELEMENTENABLED (this shell does not
REM implement ITfUIElementMgr). The NSIS installer deletes the stale key; the
REM portable ZIP upgrade path must do the same, otherwise Start menu / search
REM keep expecting host-side rendering and the candidate window stays hidden
REM (2026-10-31 audit A2).
reg delete "HKLM\Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}" /f /reg:64 >nul 2>&1
reg delete "HKLM\Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}" /f /reg:64 >nul 2>&1
reg delete "HKLM\Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}" /f /reg:32 >nul 2>&1
reg delete "HKLM\Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}" /f /reg:32 >nul 2>&1

echo ==^> Registering components (running programs are not closed)
REM 与 NSIS 安装器同一策略：不枚举/强杀加载旧 DLL 的用户程序与系统宿主。
REM 被占用的 DLL 由安装包复制/重启替换处理；已打开程序继续用旧版，
REM 重新打开后加载新版。只终止本输入法自己的旧 daemon（见下方）。
taskkill /IM %DAEMON% /F >nul 2>&1

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
powershell -NoLogo -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$tip = '0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}'; $l = Get-WinUserLanguageList; $z = $l | Where-Object { $_.LanguageTag -like 'zh*' }; if ($z) { if (-not ($z.InputMethodTips -contains $tip)) { $z.InputMethodTips.Add($tip) } } else { $z = New-WinUserLanguageList zh-CN; $z[0].InputMethodTips.Add($tip); $l += $z[0] }; Set-WinUserLanguageList $l -Force" >nul 2>&1
REM 不重启 ctfmon / 系统宿主；语言列表由下一次正常切换/注销完成刷新。
REM 不授予回环豁免：daemon 改用当前用户命名管道（-pipe），不再开 TCP 端口。

REM 先清掉本输入法自己的旧 daemon；新 daemon 由 TSF 首次输入时以当前用户
REM 身份懒启动（-pipe + -seeddict），避免提升后的管理员与交互用户不一致。
taskkill /IM %DAEMON% /F >nul 2>&1
echo.
echo ==^> Install complete
echo     daemon starts on first use in your user session (no admin needed)
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
powershell -NoLogo -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$l = Get-WinUserLanguageList; foreach ($x in $l) { $x.InputMethodTips.Remove('0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}') }; Set-WinUserLanguageList $l -Force" >nul 2>&1
REM 不强制重启 ctfmon；与安装一致，注销/重启完成宿主侧刷新。
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.Search_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.TextInputHost_cw5n1h2txyewy >nul 2>&1
CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.ShellExperienceHost_cw5n1h2txyewy >nul 2>&1
echo ==^> Removing dictionary copy
if exist "%LOCALAPPDATA%\Chaoset\dict.tsv" del /Q "%LOCALAPPDATA%\Chaoset\dict.tsv"
echo ==^> Uninstalled. Remove the Chaoset keyboard in Settings if it is still listed.
pause
exit /b 0
