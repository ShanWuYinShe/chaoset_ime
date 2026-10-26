!include "MUI2.nsh"
!include "x64.nsh"

Name "Chaoset 拼音输入法"
OutFile "..\..\..\dist\ChaosetSetup.exe"
InstallDir "$PROGRAMFILES64\Chaoset"
InstallDirRegKey HKLM "Software\Chaoset" "InstallDir"
RequestExecutionLevel admin

; 图标设置
!define MUI_ICON "..\resources\chaoset.ico"
!define MUI_UNICON "..\resources\chaoset.ico"
!define MUI_ABORTWARNING

; 向导页面
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_SHOWREADME ""
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
!define MUI_FINISHPAGE_SHOWREADME_TEXT "按 Win+Space 即可切换至 Chaoset 拼音输入法"
!insertmacro MUI_PAGE_FINISH

; 卸载页面
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

; 简体中文
!insertmacro MUI_LANGUAGE "SimpChinese"

Section "MainSection" SEC01
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    SetOutPath "$INSTDIR"
    SetOverwrite on

    ; 终止可能运行中的旧进程
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'

    ; 复制文件
    File "..\..\..\dist\chaoset-windows-x64\ChaosetIME.dll"
    File "..\..\..\dist\chaoset-windows-x64\chaosetd.exe"
    File "..\..\..\dist\chaoset-windows-x64\chaoset-cli.exe"
    File "..\..\..\dist\chaoset-windows-x64\chaoset-import.exe"
    File "..\..\..\dist\chaoset-windows-x64\dict.tsv"
    File "..\resources\chaoset.ico"

    ; 注册 TSF COM 组件
    ExecWait 'regsvr32.exe /s "$INSTDIR\ChaosetIME.dll"'

    ; 注册 TSF 语言配置与类别（确保 Windows 语言列表中显示为简体中文输入法）
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Description" "Chaoset 拼音输入法"
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconFile" "$INSTDIR\ChaosetIME.dll"
    WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconIndex" 0
    WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

    ; 为当前用户激活语言配置 (HKCU)
    WriteRegDWORD HKCU "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

    ; 准备数据目录并同步词库
    CreateDirectory "$LOCALAPPDATA\Chaoset"
    CopyFiles /SILENT "$INSTDIR\dict.tsv" "$LOCALAPPDATA\Chaoset\dict.tsv"

    ; 写入开机自启动项 (解决重启后守护进程未运行导致输入法打不出字)
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "ChaosetDaemon" '"$INSTDIR\chaosetd.exe" -tcp 127.0.0.1:7949 -dict "$LOCALAPPDATA\Chaoset\dict.tsv"'

    ; 立即启动后台服务
    Exec '"$INSTDIR\chaosetd.exe" -tcp 127.0.0.1:7949 -dict "$LOCALAPPDATA\Chaoset\dict.tsv"'

    ; 写入系统“已安装的应用/控制面板”标准卸载信息
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayName" "Chaoset 拼音输入法"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayIcon" '"$INSTDIR\chaoset.ico"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "Publisher" "ShanWuYinShe"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayVersion" "0.2.4"
    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; 开始菜单快捷方式
    CreateDirectory "$SMPROGRAMS\Chaoset"
    CreateShortcut "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk" "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    ; 终止后台服务
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'

    ; 反注册 TSF COM 组件
    ExecWait 'regsvr32.exe /u /s "$INSTDIR\ChaosetIME.dll"'

    ; 删除注册表启动项与卸载项
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "ChaosetDaemon"
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset"
    DeleteRegKey HKCU "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}"

    ; 删除文件
    Delete "$INSTDIR\ChaosetIME.dll"
    Delete "$INSTDIR\chaosetd.exe"
    Delete "$INSTDIR\chaoset-cli.exe"
    Delete "$INSTDIR\chaoset-import.exe"
    Delete "$INSTDIR\dict.tsv"
    Delete "$INSTDIR\chaoset.ico"
    Delete "$INSTDIR\uninstall.exe"
    RMDir "$INSTDIR"

    ; 删除快捷方式
    Delete "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk"
    RMDir "$SMPROGRAMS\Chaoset"
SectionEnd
