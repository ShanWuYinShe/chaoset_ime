.PHONY: build test vet fmt cover race run-cli run-daemon icons lint-workflows package-linux package-windows package-macos package-all

build:
	go build -o bin/chaosetd ./cmd/chaosetd
	go build -o bin/chaoset-cli ./cmd/chaoset-cli
	go build -o bin/chaoset-import ./cmd/chaoset-import
	go build -o bin/chaoset-dictgen ./cmd/chaoset-dictgen
	go build -o bin/chaoset-userdict ./cmd/chaoset-userdict

test:
	go test ./...

vet:
	go vet ./...
	$(MAKE) lint-workflows

# 校验 .github/workflows 的结构完整性。
# 为什么需要它：2026-10-25 一次文本替换误删了两个 job 的头部声明，YAML 语法
# 依然合法（yaml 能解析），但 GitHub 判「workflow file issue」，CI 全线不跑。
# 详细背景与检查项见 tools/check-workflows.py 的文件头注释。
lint-workflows:
	python3 tools/check-workflows.py \
		--expect-jobs test \
		.github/workflows/ci.yml
	python3 tools/check-workflows.py \
		--expect-jobs test,package-macos,package-linux,package-windows,release \
		.github/workflows/release.yml

fmt:
	gofmt -l -w .

cover:
	go test -coverprofile=.workwork/cover.out ./...
	go tool cover -func=.workwork/cover.out | tail -1

race:
	go test -race -count=1 ./...

run-cli:
	go run ./cmd/chaoset-cli

run-daemon:
	go run ./cmd/chaosetd

icons:
	swift shims/macos/tools/make-icon.swift .workwork/tmp_icon --sync-all
	rm -rf .workwork/tmp_icon

package-macos:
	./shims/macos/build.sh
	@# 架构自检：主壳与安装器都必须是 universal（arm64 + x86_64）。
	@# CI 的 macos-latest 是 Apple Silicon，裸 swiftc/go build 只出 arm64，
	@# Intel Mac 用户装上会「无法打开」——这个坑 2026-10-25 实测确认过。
	@for app in shims/macos/build/ChaosetIME.app shims/macos/build/ChaosetSetup.app; do \
		bin="$$app/Contents/MacOS/$$(basename "$$app" .app)"; \
		lipo -info "$$bin" 2>/dev/null | grep -q 'x86_64 arm64\|arm64 x86_64' \
			|| { echo "错误：$$bin 不是 universal 二进制："; lipo -info "$$bin" 2>&1; exit 1; }; \
	done
	mkdir -p dist
	(cd shims/macos/build && zip -r -y ../../../dist/chaoset-macos.zip ChaosetIME.app ChaosetSetup.app)
	@if command -v hdiutil >/dev/null 2>&1; then \
		echo "==> 构建 macOS 单文件磁盘映象 (Chaoset.dmg)..."; \
		rm -rf dist/dmg_staging; \
		mkdir -p dist/dmg_staging; \
		cp -R shims/macos/build/ChaosetSetup.app dist/dmg_staging/; \
		printf "【Chaoset 输入法 macOS 安装说明】\n\n1. 直接双击运行「Chaoset 安装与设置」(ChaosetSetup.app)；\n2. 在窗口中点击「一键安装 / 更新」，程序将自动部署输入法组件并启动后台服务；\n3. 按快捷键【Ctrl + 空格】（或 Fn / 🌐 键）切换至 Chaoset 即可开始打字！\n\n※ 全程图形化自动安装，无需将文件拖入应用程序目录，无需使用终端或运行脚本。\n" > dist/dmg_staging/安装说明.txt; \
		hdiutil create -volname "Chaoset" -srcfolder dist/dmg_staging -ov -format UDZO dist/Chaoset.dmg; \
		rm -rf dist/dmg_staging; \
	fi

# package-linux：显式交叉编译到 linux/amd64。
#
# 必须显式指定 GOOS/GOARCH：此前的写法是裸 `go build`，在 macOS 开发机上打包
# 会把 Darwin Mach-O 二进制塞进 Linux 包（2026-10-25 实测确认），
# 而 CI 因为跑在 Linux runner 上恰好看不出这个问题——本机打包才会踩到。
LINUX_GOENV = GOOS=linux GOARCH=amd64 CGO_ENABLED=0

package-linux:
	rm -rf dist/chaoset-linux-x86_64
	mkdir -p dist/chaoset-linux-x86_64/bin
	mkdir -p dist/chaoset-linux-x86_64/component
	mkdir -p dist/chaoset-linux-x86_64/icons
	mkdir -p dist/chaoset-linux-x86_64/data
	$(LINUX_GOENV) go build -o dist/chaoset-linux-x86_64/bin/chaosetd ./cmd/chaosetd
	$(LINUX_GOENV) go build -o dist/chaoset-linux-x86_64/bin/chaoset-cli ./cmd/chaoset-cli
	$(LINUX_GOENV) go build -o dist/chaoset-linux-x86_64/bin/chaoset-import ./cmd/chaoset-import
	$(LINUX_GOENV) go build -o dist/chaoset-linux-x86_64/bin/chaoset-userdict ./cmd/chaoset-userdict
	# 引擎在编译期写入安装后的词库绝对路径：ibus-daemon 按 component XML 的
	# <exec> 拉起时 cwd 不确定，靠相对路径必然找不到词库（2026-10-25 实测的
	# 发行缺陷）。运行时仍会在该路径不存在时回退到「可执行文件同级 data/」，
	# 因此解包即用的绿色形态也能跑。
	$(LINUX_GOENV) go build -ldflags "-X main.buildTimeDict=/usr/share/chaoset/dict.tsv" \
		-o dist/chaoset-linux-x86_64/bin/chaoset-engine ./shims/linux/ibus-engine/
	cp shims/linux/component/chaoset.xml dist/chaoset-linux-x86_64/component/
	cp -r shims/linux/icons/hicolor dist/chaoset-linux-x86_64/icons/
	# 词库：随包携带常用词库（dict_common.tsv），安装到 /usr/share/chaoset/dict.tsv。
	# 此前只带 558 条的 dict_seed.tsv 而引擎默认要 dict_common.tsv，是装完
	# 「引擎启动即退出」的直接原因。
	cp data/dict_common.tsv dist/chaoset-linux-x86_64/data/dict.tsv
	cp shims/linux/install.sh dist/chaoset-linux-x86_64/
	cp shims/linux/README.md dist/chaoset-linux-x86_64/
	@# 架构自检：包里的二进制必须真是 ELF，不能是开发机的 Mach-O
	@for b in dist/chaoset-linux-x86_64/bin/*; do \
		file "$$b" | grep -q ELF || { echo "错误：$$b 不是 ELF（Linux）可执行文件："; file "$$b"; exit 1; }; \
	done
	tar -czf dist/chaoset-linux-x86_64.tar.gz -C dist chaoset-linux-x86_64
	@if command -v dpkg-deb >/dev/null 2>&1; then \
		echo "==> 构建 Linux Debian/Ubuntu 一键安装包 (chaoset_amd64.deb)..."; \
		rm -rf dist/deb_staging; \
		mkdir -p dist/deb_staging/DEBIAN; \
		mkdir -p dist/deb_staging/usr/bin; \
		mkdir -p dist/deb_staging/usr/share/chaoset; \
		mkdir -p dist/deb_staging/usr/share/ibus/component; \
		mkdir -p dist/deb_staging/usr/share/icons; \
		cp dist/chaoset-linux-x86_64/bin/* dist/deb_staging/usr/bin/; \
		cp dist/chaoset-linux-x86_64/data/dict.tsv dist/deb_staging/usr/share/chaoset/; \
		cp dist/chaoset-linux-x86_64/component/chaoset.xml dist/deb_staging/usr/share/ibus/component/; \
		cp -r dist/chaoset-linux-x86_64/icons/hicolor dist/deb_staging/usr/share/icons/; \
		cp shims/linux/debian/control dist/deb_staging/DEBIAN/control; \
		cp shims/linux/debian/postinst dist/deb_staging/DEBIAN/postinst; \
		cp shims/linux/debian/postrm dist/deb_staging/DEBIAN/postrm; \
		chmod 0755 dist/deb_staging/DEBIAN/postinst dist/deb_staging/DEBIAN/postrm; \
		chmod 0755 dist/deb_staging/usr/bin/*; \
		chmod 0644 dist/deb_staging/usr/share/chaoset/dict.tsv; \
		chmod 0644 dist/deb_staging/usr/share/ibus/component/chaoset.xml; \
		dpkg-deb --build --root-owner-group dist/deb_staging dist/chaoset_amd64.deb; \
		rm -rf dist/deb_staging; \
	fi
	@if command -v zstd >/dev/null 2>&1; then \
		echo "==> 构建 Arch Linux 一键安装包 (chaoset-ime-x86_64.pkg.tar.zst)..."; \
		./shims/linux/arch/package.sh dist/chaoset-linux-x86_64 dist/chaoset-ime-x86_64.pkg.tar.zst 0.2.12; \
	fi
	rm -rf dist/chaoset-linux-x86_64

# package-windows：交叉编译到 windows/amd64（Go）与 mingw-w64（DLL）。
#
# 与 Linux 同理，必须显式指定 GOOS/GOARCH：裸 go build 会把开发机的
# Mach-O/ELF 二进制塞进 Windows 包。包内自带 install.bat，
# 用户右键「以管理员身份运行」即可完成 regsvr32 注册 + daemon 启动，
# 不必再手工敲命令。同时编译 64 位与 32 位两个 TSF DLL，实现合体安装包全面支持。
WIN_GOENV = GOOS=windows GOARCH=amd64 CGO_ENABLED=0

package-windows:
	rm -rf dist/chaoset-windows-x64
	mkdir -p dist/chaoset-windows-x64
	$(WIN_GOENV) go build -ldflags "-H=windowsgui -s -w" -o dist/chaoset-windows-x64/chaosetd.exe ./cmd/chaosetd
	$(WIN_GOENV) go build -ldflags "-s -w" -o dist/chaoset-windows-x64/chaoset-cli.exe ./cmd/chaoset-cli
	$(WIN_GOENV) go build -ldflags "-s -w" -o dist/chaoset-windows-x64/chaoset-import.exe ./cmd/chaoset-import
	# 资源文件编译（将应用图标嵌入 DLL，确保 Windows 语言栏与切换菜单显示专属图标）
	x86_64-w64-mingw32-windres -I shims/windows/resources shims/windows/resources/chaoset.rc -O coff -o dist/chaoset-windows-x64/chaoset_res64.o
	i686-w64-mingw32-windres -I shims/windows/resources shims/windows/resources/chaoset.rc -O coff -o dist/chaoset-windows-x64/chaoset_res32.o
	# 64 位 TSF 前端 DLL
	x86_64-w64-mingw32-g++ -std=c++17 -O2 -Wall -Wextra -shared -static \
		-o dist/chaoset-windows-x64/ChaosetIME.dll \
		shims/windows/src/win32.cpp shims/windows/src/core.cpp dist/chaoset-windows-x64/chaoset_res64.o \
		-lole32 -luuid -lws2_32 -luser32 -lkernel32 -ladvapi32 -lgdi32
	# 32 位 WOW64 前端 DLL（-Wl,--kill-at 去除 stdcall 的 @0 修饰，确保 regsvr32 识别入口）
	i686-w64-mingw32-g++ -std=c++17 -O2 -Wall -Wextra -shared -static -Wl,--kill-at \
		-o dist/chaoset-windows-x64/ChaosetIME32.dll \
		shims/windows/src/win32.cpp shims/windows/src/core.cpp dist/chaoset-windows-x64/chaoset_res32.o \
		-lole32 -luuid -lws2_32 -luser32 -lkernel32 -ladvapi32 -lgdi32
	rm -f dist/chaoset-windows-x64/chaoset_res64.o dist/chaoset-windows-x64/chaoset_res32.o
	cp shims/windows/resources/chaoset.ico dist/chaoset-windows-x64/
	# 词库：随包携带常用词库，文件名与 install.bat / README 里的约定一致（dict.tsv）。
	# 此前放的是 558 条的 dict_seed.tsv 而 README 却让用户传 -dict dict.tsv，
	# 名字对不上——用户照做会直接启动失败。
	cp data/dict_common.tsv dist/chaoset-windows-x64/dict.tsv
	cp shims/windows/install.bat dist/chaoset-windows-x64/
	cp shims/windows/README.md dist/chaoset-windows-x64/
	@# 架构自检：Go 二进制必须是 PE，DLL 必须同时产出 64 位与 32 位且导出标准 COM 入口
	@for b in dist/chaoset-windows-x64/chaosetd.exe dist/chaoset-windows-x64/chaoset-cli.exe dist/chaoset-windows-x64/chaoset-import.exe; do \
		file "$$b" | grep -q 'PE32+' || { echo "错误：$$b 不是 Windows PE 可执行文件："; file "$$b"; exit 1; }; \
	done
	@file dist/chaoset-windows-x64/ChaosetIME.dll | grep -q 'PE32+' || { echo "错误：ChaosetIME.dll 不是 64 位 PE："; file dist/chaoset-windows-x64/ChaosetIME.dll; exit 1; }
	@file dist/chaoset-windows-x64/ChaosetIME32.dll | grep -q 'PE32 ' || { echo "错误：ChaosetIME32.dll 不是 32 位 PE："; file dist/chaoset-windows-x64/ChaosetIME32.dll; exit 1; }
	@x86_64-w64-mingw32-objdump -p dist/chaoset-windows-x64/ChaosetIME.dll \
		| grep -q DllRegisterServer || { echo "错误：ChaosetIME.dll 缺少 DllRegisterServer 导出"; exit 1; }
	@x86_64-w64-mingw32-objdump -p dist/chaoset-windows-x64/ChaosetIME.dll \
		| grep -q DllUnregisterServer || { echo "错误：ChaosetIME.dll 缺少 DllUnregisterServer 导出"; exit 1; }
	@i686-w64-mingw32-objdump -p dist/chaoset-windows-x64/ChaosetIME32.dll \
		| grep -q DllRegisterServer || { echo "错误：ChaosetIME32.dll 缺少 DllRegisterServer 导出"; exit 1; }
	@i686-w64-mingw32-objdump -p dist/chaoset-windows-x64/ChaosetIME32.dll \
		| grep -q DllUnregisterServer || { echo "错误：ChaosetIME32.dll 缺少 DllUnregisterServer 导出"; exit 1; }
	@if command -v makensis >/dev/null 2>&1; then \
		echo "==> 构建 Windows 单文件图形安装包 (ChaosetSetup.exe)..."; \
		(cd shims/windows/installer && makensis chaoset.nsi); \
	fi
	(cd dist && zip -r chaoset-windows-x64.zip chaoset-windows-x64)
	rm -rf dist/chaoset-windows-x64

package-all: package-macos package-linux package-windows
