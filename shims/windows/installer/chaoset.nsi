; Unicode 安装器：本脚本含大量中文，必须以 Unicode 编译。makensis 3.x 默认
; 按 ANSI 读取无 BOM 脚本，中文会整体乱码且产物在非中文 locale 的 Windows 上
; 界面乱码（2026-10-29 打包验证时发现）。
Unicode true
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

!define MUI_FINISHPAGE_TITLE "Chaoset 拼音输入法安装完成"
!define MUI_FINISHPAGE_TEXT "Chaoset 拼音输入法安装完成。$\r$\n$\r$\n1. 打开任意程序，按 Win+Space 切换后打拼音；首次输入会自动启动后台服务（当前用户权限，无需管理员）。$\r$\n2. 安装前已打开的程序不会被安装程序关闭，但它们仍使用旧版 DLL，请关闭并重新打开；开始菜单/搜索等系统宿主在注销或重启后加载新版。$\r$\n3. 若切换后没有候选窗，请先尝试注销并重新登录一次。"
!define MUI_FINISHPAGE_SHOWREADME ""
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
!define MUI_FINISHPAGE_SHOWREADME_TEXT "按 Win+Space 随时切换 Chaoset 拼音输入法"
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

    ; 永不中止原则（2026-10-29 用户要求）：SetOverwrite try 让 File 写失败
    ; 只置错误标志，绝不弹「无法写入」框卡住安装。写不进（文件仍被后台
    ; 程序映射）就写到 .new 并预约重启时改名顶替，安装照常走完，最后统一
    ; 提示用户重启完成更新。
    SetOverwrite try
    ClearErrors
    File "/oname=$INSTDIR\${FILE_NAME}" "${SRC_PATH}\${FILE_NAME}"
    ${If} ${Errors}
        ClearErrors
        File "/oname=$INSTDIR\${FILE_NAME}.new" "${SRC_PATH}\${FILE_NAME}"
        ${If} ${Errors}
            ; 连 .new 都写不进（磁盘/权限问题）：记录警告继续安装，不中断
            DetailPrint "警告：${FILE_NAME} 写入失败，本次未更新该文件"
        ${Else}
            Rename /REBOOTOK "$INSTDIR\${FILE_NAME}.new" "$INSTDIR\${FILE_NAME}"
            SetRebootFlag true
        ${EndIf}
    ${EndIf}
    SetOverwrite on
!macroend

Section "MainSection" SEC01
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    SetOutPath "$INSTDIR"
    SetOverwrite on

    ; 只终止本输入法自己的旧 daemon（用户程序、输入宿主一律不动）。
    ;
    ; 旧版安装器会枚举所有加载 ChaosetIME*.dll 的进程并 Stop-Process，还会强停
    ; ctfmon/TextInputHost/SearchHost/StartMenu 等系统宿主——用户打开的浏览器、
    ; 编辑器、聊天软件会连带被关闭，未保存内容可能丢失，严重影响正常使用。
    ; 新版安装器不再强杀任何宿主：被占用的 DLL 由 InstallRobustFile 先改名再写
    ; 新文件（或预约重启替换）；已打开的旧程序继续用旧版 DLL，重新打开后加载新
    ; 版，绝不以关闭用户程序为代价追求「立即全面生效」。
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'
    Sleep 300

    ; 安全写入二进制文件与资源（支持热替换）
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "ChaosetIME.dll"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "ChaosetIME32.dll"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaosetd.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaoset-cli.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "chaoset-import.exe"
    !insertmacro InstallRobustFile "..\..\..\dist\chaoset-windows-x64" "dict.tsv"
    ; 图标非关键文件：同样不因占用中止
    SetOverwrite try
    ClearErrors
    File "..\resources\chaoset.ico"
    SetOverwrite on

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

    ; 注册 TSF 类别（键盘、显示属性、沉浸式、托盘；**不含 UILess/UIELEMENTENABLED**）
    ;
    ; 为什么删掉 {49d2f9cf-...}（2026-10-30 修）：该类别声明「本 TIP 以 UILess
    ; 模式提供候选 UI」，TSF 在沉浸式宿主（Windows 11 开始菜单/搜索框）里会
    ; 期望 TIP 通过 ITfUIElementMgr 提交候选元素、由宿主渲染。本壳不实现这些
    ; 接口，候选窗是进程内自绘 GDI 弹窗——声明与实现矛盾，实机表现为开始菜单
    ; 里完全看不到候选窗（记事本正常）。详见 win32.cpp 常量处注释。
    ;
    ; 升级路径：下面先 DeleteRegKey 再写，否则已装旧版的用户注册表里会残留
    ; 该类别，导致修复对他们无效（安装器是直接写注册表的「双重保障」路径，
    ; 不经过 DllRegisterServer）。
    DeleteRegKey HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}"
    DeleteRegKey HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}"
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
    WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
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

        ; 32 位视图同样剔除 UILess 类别（升级残留清理，理由见上方 64 位注释）
        DeleteRegKey HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}"
        DeleteRegKey HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{49d2f9cf-1f5e-11d7-a6d3-00065b84435c}"
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{34745c63-b2f0-4784-8b67-5e12c8701a31}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{046602e4-c36a-4f66-82f7-b44d5fd81ac4}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{13a016df-560b-46cd-947a-4c3af1e0e35d}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Category\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        WriteRegStr HKLM "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\Category\Item\{25504fb4-7bab-4bc1-9c69-cf81890f0ef5}\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}" "" ""
        SetRegView 64
    ${EndIf}

    ; 为当前用户激活语言配置 (HKCU)
    WriteRegDWORD HKCU "Software\Microsoft\CTF\TIPs\{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}\LanguageProfile\0x00000804\{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}" "Enable" 1

    ; 自动添加到用户当前语言列表并无条件强制刷新（免手动配置，即装即用 Win+Space 切换）。
    ; 用 Sysnative 的 64 位 PowerShell：32 位 NSIS 直接调用 powershell.exe 会被
    ; WOW64 重定向，语言列表相关 cmdlet 在 32 位会话下可能不可用/行为不一致。
    ${If} ${RunningX64}
        StrCpy $R1 "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
    ${Else}
        StrCpy $R1 "$WINDIR\System32\WindowsPowerShell\v1.0\powershell.exe"
    ${EndIf}
    nsExec::Exec '"$R1" -NoLogo -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$$tip = \"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\"; $$l = Get-WinUserLanguageList; $$z = $$l | Where-Object { $$_.LanguageTag -like \"zh*\" }; if ($$z) { if (-not ($$z.InputMethodTips -contains $$tip)) { $$z.InputMethodTips.Add($$tip) } } else { $$z = New-WinUserLanguageList zh-CN; $$z[0].InputMethodTips.Add($$tip); $$l += $$z[0] }; Set-WinUserLanguageList $$l -Force"'

    ; 为安装器当前用户刷新可写词库：调用 Go 导入器以 -seed 合并，保留用户
    ; 已导入的词（旧文件由 -backup 备份），不在壳/NSIS 里重复实现合并逻辑。
    ; 其他用户的首次启动仍由 daemon -seeddict 从 $INSTDIR 初始化。
    nsExec::Exec '"$INSTDIR\chaoset-import.exe" -o "$LOCALAPPDATA\Chaoset\dict.tsv" -seed "$INSTDIR\dict.tsv" -backup "$INSTDIR\dict.tsv"'

    ; 不再强杀 ctfmon / 系统宿主，也不在提升后的安装器里启动 daemon：
    ; 管理员账户与交互用户可能不是同一个，强杀会打断用户正在使用的程序，
    ; 用管理员 LOCALAPPDATA 启动/写词库也会让普通用户运行时找不到。
    ; daemon 的自动启动交给下面的 Run 项与 TSF 懒启动。
    ;
    ; Run 用 REG_EXPAND_SZ 保留 %LOCALAPPDATA%，由每个登录用户在 Explorer
    ; 上下文展开；-seeddict 让 daemon 首次运行时从 $INSTDIR 复制种子词库到
    ; 当前用户的 LOCALAPPDATA（不覆盖用户已导入的词库）。
    WriteRegExpandStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "ChaosetDaemon" '"$INSTDIR\chaosetd.exe" -pipe -dict "%LOCALAPPDATA%\Chaoset\dict.tsv" -seeddict "$INSTDIR\dict.tsv" -userdict "%LOCALAPPDATA%\Chaoset\userdict.json" -log "%LOCALAPPDATA%\Chaoset\chaosetd.log"'

    ; 写入系统“已安装的应用/控制面板”标准卸载信息
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayName" "Chaoset 拼音输入法"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayIcon" '"$INSTDIR\chaoset.ico"'
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "Publisher" "ShanWuYinShe"
    ; 版本号由 makensis 的命令行 -DVERSION=… 注入（Makefile 从 git tag 推导）。
    ; 此处曾写死 0.2.12，发版时必然漏改（2026-10-26 修复）。
    !ifndef VERSION
      !define VERSION "0.0.0-dev"
    !endif
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chaoset" "DisplayVersion" "${VERSION}"
    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; 开始菜单快捷方式
    CreateDirectory "$SMPROGRAMS\Chaoset"
    CreateShortcut "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk" "$INSTDIR\uninstall.exe"

    ; 仍有进程（后台驻留应用等）占用 DLL 时走的是 .new + 重启替换预约，
    ; 必须明确告知用户重启完成更新，避免误以为装好了实际还是旧版
    ${If} ${RebootFlag}
        MessageBox MB_OK|MB_ICONINFORMATION "部分文件当前被后台程序占用，已预约重启替换：请重启电脑以完成本次更新（重启前输入法仍可正常使用旧版）。"
    ${EndIf}
SectionEnd

Section "Uninstall"
    ${If} ${RunningX64}
        SetRegView 64
    ${EndIf}

    ; 终止后台服务并等待句柄完全释放
    nsExec::Exec 'taskkill /IM chaosetd.exe /F'
    Sleep 300

    ; 从用户语言列表中移除输入法（同样用 64 位 PowerShell，避免 WOW64 重定向）
    ${If} ${RunningX64}
        StrCpy $R1 "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
    ${Else}
        StrCpy $R1 "$WINDIR\System32\WindowsPowerShell\v1.0\powershell.exe"
    ${EndIf}
    nsExec::Exec '"$R1" -NoLogo -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$$l = Get-WinUserLanguageList; foreach ($$x in $$l) { $$x.InputMethodTips.Remove(\"0804:{8a5c7b31-4e2d-4c6a-9f41-77d0b3e5c1a8}{5a1b3c7d-8e9f-4a1b-a2c3-d4e5f6071829}\") }; Set-WinUserLanguageList $$l -Force"'
    ; 不强制重启 ctfmon / 系统宿主：卸载同样不打断用户正在使用的程序；
    ; 输入法从语言列表移除后由下一次注销/重启完成宿主侧刷新。
    ; 清理旧版本曾授予系统输入宿主的回环豁免（新版本已改用命名管道）
    nsExec::Exec 'CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy'
    nsExec::Exec 'CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.Search_cw5n1h2txyewy'
    nsExec::Exec 'CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.TextInputHost_cw5n1h2txyewy'
    nsExec::Exec 'CheckNetIsolation LoopbackExempt -d -n=Microsoft.Windows.ShellExperienceHost_cw5n1h2txyewy'

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

    ; 清理用户数据目录（词库副本 + 用户学习数据 + 日志）。
    ; 2026-10-26 修复：此前卸载完全不碰 %LOCALAPPDATA%\Chaoset，用户重装后
    ; 会读到上一份 userdict.json；同时按 NSIS 惯例，卸载应提供「是否保留
    ; 个人数据」的选择，这里默认删除（输入法学习数据无长期价值）。
    MessageBox MB_YESNO|MB_ICONQUESTION "是否同时删除 Chaoset 的个人数据（学习词库与日志）？$\r$\n选择「否」将保留在 $LOCALAPPDATA\Chaoset，重装后可继续使用。" IDNO skipUserData
      Delete "$LOCALAPPDATA\Chaoset\dict.tsv"
      Delete "$LOCALAPPDATA\Chaoset\userdict.json"
      Delete "$LOCALAPPDATA\Chaoset\chaosetd.log"
      Delete "$LOCALAPPDATA\Chaoset\chaoset-userdict.json"
      RMDir "$LOCALAPPDATA\Chaoset"
    skipUserData:

    ; 删除快捷方式
    Delete "$SMPROGRAMS\Chaoset\卸载 Chaoset 输入法.lnk"
    RMDir "$SMPROGRAMS\Chaoset"
SectionEnd

Function .onInstFailed
    MessageBox MB_OK|MB_ICONEXCLAMATION "安装遇到异常中断，已自动中止退出。您可以重新运行安装程序。"
FunctionEnd
