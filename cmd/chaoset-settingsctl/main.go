// chaoset-settingsctl 是三端设置项的 Go 单一真源命令行接口（方案 A）。
//
// # 用途
//
// 设置 UI 只有 Slint 统一设置中心（shims/settings，经随包 chaosetctl 调用本
// 命令）；设置项的 schema/默认值/校验/持久化唯一真源在
// Go internal/settings，本命令是它给 shim 的**冷路径**入口（设置窗读取/
// 写入、诊断脚本）。输入热路径（Windows TSF 回调、macOS IMK、Linux ibus）
// 禁止调用本命令，仍读各自本机缓存/原生存储，保证按键延迟不受影响。
//
// # 命令与输出契约
//
//	chaoset-settingsctl get
//	    单行紧凑 JSON（字段顺序固定），写 stdout，末尾换行：
//	    {"associate":true,"fuzzy":false,"shuangpin":"","mode_scope":"system","ui_english":false,"theme":"system"}
//
//	chaoset-settingsctl set associate on|off
//	    写联想开关；成功无输出。
//
//	chaoset-settingsctl set fuzzy on|off
//	    写模糊音开关（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）；成功无输出。
//
//	chaoset-settingsctl set shuangpin full|zrm|mspy|flypy
//	    写双拼方案（full=全拼，落存储为空串）；成功无输出。
//
//	chaoset-settingsctl set mode-scope system|process
//	    写中英状态作用域；成功无输出。旧三档值 window 也接受并归一化
//	    （window→process），但新写入只落 system|process。
//
//	chaoset-settingsctl set theme system|light|dark
//	    写外观主题：system 跟随系统（默认）/ light 浅色 / dark 深色。
//
//	chaoset-settingsctl set ui-english on|off
//	    写 Windows UI 宿主持久化的中英状态镜像（内部用）。
//	    macOS/Linux 壳没有对应存储：报错退出 1（不支持，不是参数错误）。
//
// 取值大小写不敏感（on/ON、SYSTEM 均可），前后空白忽略；mode-scope 的旧三档
// 值 window 也接受并归一化（读取存储时同样归一化）。
//
// # 退出码
//
//	0  成功
//	1  运行错误（存储不可用、读取失败、当前平台不支持）
//	2  参数非法（未知子命令、缺少参数、枚举值非法）
//
// 约定：错误只写 stderr；stdout 只放 get 的 JSON。help/-h/--help 输出
// 用法到 stdout 并退出 0；无子命令按参数非法处理（退出 2）。
//
// # 存储映射（与现有三端壳逐字兼容；细节见 internal/settings 包注释）
//
//	Windows  HKCU\Software\Chaoset
//	         Associate REG_DWORD 缺省 1
//	         Fuzzy     REG_DWORD 缺省 0
//	         Shuangpin REG_SZ    缺省 ""（""|zrm|mspy|flypy）
//	         ModeScope REG_SZ    缺省 "system"（只写 system|process；
//	                             旧三档 window→process）
//	         UiEnglish REG_DWORD 缺省 0
//	         Theme     REG_SZ    缺省 "system"（system|light|dark）
//	macOS    UserDefaults 域 ime.chaoset.inputmethod.ChaosetIME（输入法
//	         bundle id；Slint 设置中心写同一域）
//	         AssociateEnabled Bool   缺省 true
//	         ChaosetFuzzy     Bool   缺省 false
//	         ChaosetShuangpin String 缺省 ""（""|zrm|mspy|flypy）
//	         ChaosetModeScope String 缺省 "system"（旧值读取时归一化）
//	         ChaosetTheme    String 缺省 "system"（system|light|dark）
//	         ui_english 不支持（macOS 壳不持久化中英状态）
//	Linux    $XDG_CONFIG_HOME/chaoset/config.json（回退
//	         $HOME/.config/chaoset/config.json）
//	         "associate"  bool   缺省 true
//	         "fuzzy"      bool   缺省 false
//	         "shuangpin"  string 缺省 ""（""|zrm|mspy|flypy）
//	         "mode_scope" string 缺省 "system"（旧值读取时归一化）
//	         "theme"      string 缺省 "system"（system|light|dark）
//	         其它键（traditional 等）原样保留
//	         ui_english 不支持（Linux 壳不持久化中英状态）
package main

import (
	"io"
	"os"

	"chaoset.ime/internal/tools/settingsctl"
)

const usageText = settingsctl.UsageText

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 是可单测的入口：返回退出码，不直接 os.Exit。
func run(args []string, stdout, stderr io.Writer) int {
	return settingsctl.Run(args, stdout, stderr)
}
