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

; 核心宏：安全替换可能被运行中进程占用的二进制文件（DLL/EXE）
; 原理：在 Windows NTFS 文件系统中，正在被进程内存映射打开的 DLL 虽不能被覆写或删除，
; 但允许被重命名！将正在使用的旧 DLL 移开至 .old，原文件路径立即被腾空释放，
; 随后写入新版 DLL，实现零弹窗热替换与平滑无缝升级。
!macro InstallRobustFile SRC_PATH FILE_NAME
    Delete "$INSTDIR\${FILE_NAME}.old"
    Delete "$INSTDIR\${FILE_NAME}.old2"

    ${If} ${FileExists} "$INSTDIR\${FILE_NAME}"
        ClearErrors
        Delete "$INSTDIR\${FILE_NAME}"
        ${If} ${Errors}
            ClearErrors
            Rename "$INSTDIR\${FILE_NAME}" "$INSTDIR\${FILE_NAME}.old"
            ${If} ${Errors}
                ClearErrors
                Rename "$INSTDIR\${FILE_NAME}" "$INSTDIR\${FILE_NAME}.old2"
                Delete /REBOOTOK "$INSTDIR\${FILE_NAME}.old2"
            ${Else}
                Delete /REBOOTOK "$INSTDIR\${FILE_NAME}.old"
            ${EndIf}
        ${EndIf}
    ${EndIf}

    ClearErrors
    File "/oname=$INSTDIR\${FILE_NAME}" "${SRC_PATH}\${FILE_NAME}"
    ${If} ${Errors}
        ; 极端保护：若仍有只读或锁定限制，写为 .new 并预约重启替换
        File "/oname=$INSTDIR\${FILE_NAME}.new" "${SRC_PATH}\${FILE_NAME}"
        Rename /REBOOTOK "$INSTDIR\${FILE_NAME}.new" "$INSTDIR\${FILE_NAME}"
        SetRebootFlag true
    ${EndIf}
!macroend

Section "MainSection" SEC01
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    SetOutPath "$INSTDIR"
    SetOverwrite on

    ; 终止可能运行中的旧守护进程并等待系统完全释放文件句柄
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'
    Sleep 300

    ; 安全写入二进制文件与资源（支持热替换）
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "ChaosetIME.dll"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "ChaosetIME32.dll"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaosetd.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaoset-cli.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaoset-import.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "dict.tsv"
    File "..\resources\chaoset.ico"

    ; 注册 TSF COM 组件（同时支持 64 位原生程序与 32 位 WOW64 程序）
    ${If} ${RunningX64}
        ; 1. 注册 64 位原生组件 (供 64 位软件如 Edge、Notepad、资源管理器等加载)
        ${DisableX64FSRedirection}
        ExecWait 'regsvr32.exe /s "$INSTDIR\ChaosetIME.dll"'
        ${EnableX64FSRedirection}

        ; 2. 注册 32 位 WOW64 组件 (供 32 位软件如 Steam、游戏、32位客户端等加载)
        ${If} ${FileExists} "$WINDIR\SysWOW64\regsvr32.exe"
            ExecWait '"$WINDIR\SysWOW64\regsvr32.exe" /s "$INSTDIR\ChaosetIME32.dll"'
        ${EndIf}
    ${Else}
        ; 纯 32 位系统
        ExecWait 'regsvr32.exe /s "$INSTDIR\ChaosetIME32.dll"'
    ${EndIf}

    ; 注册 TSF 语言配置与类别（确保 Windows 语言列表中显示为简体中文输入法）
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Description" "Chaoset 拼音输入法"
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconFile" "$INSTDIR\ChaosetIME.dll"
    WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconIndex" 0
    WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

    ; 注册 TSF 类别（键盘、显示属性、沉浸式、托盘与无界面支持，确保现代设置界面与任务栏可见）
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""

    ; 32 位 WOW64 类别与配置注册 (WOW6432Node)
    ${If} ${RunningX64}
        SetRegView 32
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Description" "Chaoset 拼音输入法"
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconFile" "$INSTDIR\ChaosetIME32.dll"
        WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "IconIndex" 0
        WriteRegDWORD HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        SetRegView 64
    ${EndIf}

    ; 为当前用户激活语言配置 (HKCU)
    WriteRegDWORD HKCU "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

    ; 自动添加到用户当前语言列表（免手动配置，即装即用 Win+Space 切换）
    nsExec::Exec 'powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$l = Get-WinUserLanguageList; $z = $l | Where-Object { $_.LanguageTag -like \"zh*\" }; if ($z) { if (-not ($z.InputMethodTips -contains \"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\")) { $z.InputMethodTips.Add(\"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\"); Set-WinUserLanguageList $l -Force } } else { $z = New-WinUserLanguageList zh-CN; $z[0].InputMethodTips.Add(\"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\"); $l += $z[0]; Set-WinUserLanguageList $l -Force }"'

    ; 准备数据目录并同步词库
    CreateDirectory "$LOCALAPPDATA\Chaoset"
    CopyFiles /SILENT "$INSTDIR\dict.tsv" "$LOCALAPPDATA\Chaoset\dict.tsv"

    ; 写入开机自启动项 (解决重启后守护进程未运行导致输入法打不出字)
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "ChaosetDaemon" '"$INSTDIR\chaosetd.exe" -tcp 127.0.0.1:7949 -dict "$LOCALAPPDATA\Chaoset\dict.tsv" -log "$LOCALAPPDATA\Chaoset\chaosetd.log"'

    ; 立即启动后台服务（GUI 无控制台黑框直接运行，日志静默落盘）
    Exec '"$INSTDIR\chaosetd.exe" -tcp 127.0.0.1:7949 -dict "$LOCALAPPDATA\Chaoset\dict.tsv" -log "$LOCALAPPDATA\Chaoset\chaosetd.log"'

    ; 写入系统“已安装的应用/控制面板”标准卸载信息
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayName" "Chaoset 拼音输入法"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayIcon" '"$INSTDIR\chaoset.ico"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "Publisher" "ShanWuYinShe"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayVersion" "0.2.11"
    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; 开始菜单快捷方式
    CreateDirectory "$SMPROGRAMS\Chaoset"
    CreateShortcut "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk" "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    ; 终止后台服务并等待句柄完全释放
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'
    Sleep 300

    ; 从用户语言列表中移除输入法
    nsExec::Exec 'powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$l = Get-WinUserLanguageList; foreach ($x in $l) { $x.InputMethodTips.Remove(\"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\") }; Set-WinUserLanguageList $l -Force"'

    ; 反注册 TSF COM 组件
    ${If} ${RunningX64}
        ${DisableX64FSRedirection}
        ExecWait 'regsvr32.exe /u /s "$INSTDIR\ChaosetIME.dll"'
        ${EnableX64FSRedirection}

        ${If} ${FileExists} "$WINDIR\SysWOW64\regsvr32.exe"
            ExecWait '"$WINDIR\SysWOW64\regsvr32.exe" /u /s "$INSTDIR\ChaosetIME32.dll"'
        ${EndIf}

        SetRegView 32
        DeleteRegKey HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}"
        SetRegView 64
    ${Else}
        ExecWait 'regsvr32.exe /u /s "$INSTDIR\ChaosetIME32.dll"'
    ${EndIf}

    ; 删除注册表启动项与卸载项
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "ChaosetDaemon"
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset"
    DeleteRegKey HKLM "Software\Chaoset"
    DeleteRegKey HKCU "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}"

    ; 安全删除文件（若 DLL 仍被活跃软件映射占用，预约重启清除，绝不报错阻断）
    Delete "$INSTDIR\chaosetd.exe"
    Delete "$INSTDIR\chaoset-cli.exe"
    Delete "$INSTDIR\chaoset-import.exe"
    Delete "$INSTDIR\dict.tsv"
    Delete "$INSTDIR\chaoset.ico"
    Delete "$INSTDIR\uninstall.exe"

    Delete /REBOOTOK "$INSTDIR\ChaosetIME.dll"
    Delete /REBOOTOK "$INSTDIR\ChaosetIME32.dll"
    Delete /REBOOTOK "$INSTDIR\ChaosetIME.dll.old"
    Delete /REBOOTOK "$INSTDIR\ChaosetIME32.dll.old"
    Delete /REBOOTOK "$INSTDIR\ChaosetIME.dll.old2"
    Delete /REBOOTOK "$INSTDIR\ChaosetIME32.dll.old2"
    RMDir /REBOOTOK "$INSTDIR"

    ; 删除快捷方式
    Delete "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk"
    RMDir "$SMPROGRAMS\Chaoset"
SectionEnd

Function .onInstFailed
    MessageBox MB_OK|MB_ICONEXCLAMATION "安装遇到异常中断，已自动中止退出。您可以重新运行安装程序。"
FunctionEnd
