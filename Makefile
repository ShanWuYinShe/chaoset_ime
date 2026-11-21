.PHONY: build build-tools build-settings build-settings-universal build-settings-windows test vet fmt cover race run-cli run-daemon icons lint-workflows check-fmt check-win32-gdi check-uiparity package-linux package-windows package-macos package-all version check-version

# ── 版本号：单一真源 ────────────────────────────────────────────────
#
# 此前版本号散落在 8 处（macOS Info.plist ×2、Linux deb control、
# component XML、两个 PKGBUILD、package.sh 默认值、NSIS、Makefile），
# 全部手工维护且已经不一致——component XML 还停在 0.1.0，而 tag 早已
# v0.2.12（2026-10-26 审查发现）。发版时必然漏改。
#
# 2026-11-06 用户规则：版本号只由环境变量 CHAOS_VERSION_TAG 决定——
# release.yml 顶层 env 在 push tag 触发的 CI 里设成 tag 版本，其他任何环境
# 都不设，version.sh 读不到就生成「构建当天年月日 + commit hash」。
# 以前取「最近一次发布的 tag」，本地/日常 CI 打的包永远显示它，分不清装的是
# 哪个提交；年月日 + hash 才是构建的真实日期与身份。唯一真源是 scripts/version.sh：
#   VERSION      显示用版本（tag 号或 YYYYMMDD-<hash>[-dirty]）
#   VERSION_QUAD 4 段数字版本（NSIS/PE VS_FIXEDFILEINFO 只接受数字）
#   PKG_VERSION  包管理器版本（deb/Arch 必须合法，含 hash）
VERSION_SCRIPT := scripts/version.sh
# 用 sh 显式调用，避免源码 tarball / Windows 检出丢失可执行位时取不到版本。
VERSION ?= $(shell sh $(VERSION_SCRIPT))
VERSION := $(if $(VERSION),$(VERSION),nogit)
VERSION_QUAD := $(shell sh $(VERSION_SCRIPT) --quad)
PKG_VERSION := $(shell sh $(VERSION_SCRIPT) --pkg)
VER_MAJOR := $(word 1,$(subst ., ,$(VERSION_QUAD)))
VER_MINOR := $(word 2,$(subst ., ,$(VERSION_QUAD)))
VER_PATCH := $(word 3,$(subst ., ,$(VERSION_QUAD)))
VER_BUILD := $(word 4,$(subst ., ,$(VERSION_QUAD)))

# 所有 cargo 构建继承版本注入：Rust build.rs 用它们生成 Windows PE 资源
# （图标 + VS_VERSION_INFO）与 CLI 版本输出；单一真源仍是 scripts/version.sh。
export CHAOSET_BUILD_VERSION := $(VERSION)
export CHAOSET_VERSION_QUAD := $(VERSION_QUAD)

version:
	@echo $(VERSION)
	@echo "来源: $(shell sh $(VERSION_SCRIPT) --source)  |  数字版本: $(VERSION_QUAD)  |  包版本: $(PKG_VERSION)" >&2

# check-version：CI 里断言各处版本号没写死，且版本源不是「最近的 tag」。
check-version:
	@echo "当前版本: $(VERSION)（来源 $(shell sh $(VERSION_SCRIPT) --source)）"
	@# 禁止「最近一次发布的 tag」再成为版本源：只查非注释的可执行行。
	@if grep -rn --include='*.sh' --include='*.yml' 'git describe --tags' shims scripts .github 2>/dev/null \
		| grep -v 'scripts/version.sh' | grep -v '^[^:]*:[0-9]*:[[:space:]]*#' | grep -q .; then \
		echo "错误：仍有可执行代码用 git describe --tags 推导版本（应改用 scripts/version.sh）"; exit 1; \
	fi
	@# 单一真源必须存在：读 CHAOS_VERSION_TAG，读不到则生成 年月日-commit hash。
	@test -f $(VERSION_SCRIPT) || { echo "错误：缺少 $(VERSION_SCRIPT)"; exit 1; }
	@grep -q 'CHAOS_VERSION_TAG' $(VERSION_SCRIPT) || { echo "错误：version.sh 未读取 CHAOS_VERSION_TAG"; exit 1; }
	@grep -q 'rev-parse' $(VERSION_SCRIPT) || { echo "错误：version.sh 未使用 commit hash"; exit 1; }
	@grep -q 'date +%Y%m%d' $(VERSION_SCRIPT) || { echo "错误：version.sh 未生成年月日"; exit 1; }
	@# tag 版本只在 release.yml 顶层 env 注入，且只允许 push tag 触发时。
	@grep -q "github.event_name == 'push'" .github/workflows/release.yml \
		|| { echo "错误：release.yml 未限定只有 push tag 注入 CHAOS_VERSION_TAG"; exit 1; }
	@! grep -rn '^Version: [0-9]' shims/linux/debian/control 2>/dev/null \
		|| { echo "错误：debian/control 的 Version 仍是写死的数字"; exit 1; }
	@! grep -qn '<version>[0-9]' shims/linux/component/chaoset.xml 2>/dev/null \
		|| { echo "错误：component XML 的 version 仍是写死的数字"; exit 1; }
	@! grep -qE '^pkgver=[0-9]' shims/linux/arch/PKGBUILD 2>/dev/null \
		|| { echo "错误：PKGBUILD 的 pkgver 仍是写死的数字"; exit 1; }
	@! grep -qE '^pkgver=[0-9]' shims/linux/arch/PKGBUILD-bin 2>/dev/null \
		|| { echo "错误：PKGBUILD-bin 的 pkgver 仍是写死的数字"; exit 1; }
	@grep -A1 'CFBundleShortVersionString</key>' shims/macos/build.sh \
		| grep -q '<string>\$$VERSION</string>' \
		|| { echo "错误：macOS build.sh 的 CFBundleShortVersionString 未使用 \$$VERSION"; exit 1; }
	@! grep -q '"DisplayVersion" "0\.' shims/windows/installer/chaoset.nsi 2>/dev/null \
		|| { echo "错误：NSIS 的 DisplayVersion 仍是写死的数字"; exit 1; }
	@# Windows 安装器与二进制的版本资源：必须用注入的 $${VERSION}/$${VERSION_QUAD}，
	@# 不能把版本号写死在 NSIS 命令或资源模板里（2026-11-06 补齐安装程序版本信息）。
	@grep -q 'VIProductVersion "\$${VERSION_QUAD}"' shims/windows/installer/chaoset.nsi \
		|| { echo "错误：NSIS 的 VIProductVersion 未使用注入的 VERSION_QUAD"; exit 1; }
	@grep -q 'VIFileVersion "\$${VERSION_QUAD}"' shims/windows/installer/chaoset.nsi \
		|| { echo "错误：NSIS 的 VIFileVersion 未使用注入的 VERSION_QUAD"; exit 1; }
	@grep -q 'VIAddVersionKey.*"ProductVersion".*"\$${VERSION}"' shims/windows/installer/chaoset.nsi \
		|| { echo "错误：NSIS 的 ProductVersion 未使用注入的 VERSION"; exit 1; }
	@grep -q 'VALUE "ProductVersion", "@VERSION@"' shims/windows/resources/chaoset.rc \
		|| { echo "错误：资源模板的 ProductVersion 未使用 @VERSION@ 占位符"; exit 1; }
	@# Makefile 本身必须走 version.sh，不能自己取 tag；三端 build.sh 也一样。
	@grep -q 'VERSION ?= .*VERSION_SCRIPT' Makefile \
		|| { echo "错误：Makefile 的 VERSION 未取自 scripts/version.sh"; exit 1; }
	@# Linux chaoset-setup 现在只是 POSIX 启动器（不显示版本），版本由 Slint 设置中心展示。
	@for s in shims/macos/build.sh shims/windows/build.sh; do \
		grep -q 'version.sh' "$$s" \
			|| { echo "错误：$$s 未使用 scripts/version.sh"; exit 1; }; \
	done
	@# 发行链已切到 Rust CLI（chaosetctl 单二进制 + argv[0] multicall）：构建/打包
	@# 必须用 cargo 构建 chaosetctl，版本经 CHAOSET_BUILD_VERSION / CHAOSET_VERSION_QUAD
	@# 注入（Rust build.rs 读它们生成 Windows PE 图标 + VS_VERSION_INFO）。
	@grep -q 'export CHAOSET_BUILD_VERSION := $$(VERSION)' Makefile \
		|| { echo "错误：Makefile 未 export CHAOSET_BUILD_VERSION（Rust 构建拿不到版本）"; exit 1; }
	@grep -q 'export CHAOSET_VERSION_QUAD := $$(VERSION_QUAD)' Makefile \
		|| { echo "错误：Makefile 未 export CHAOSET_VERSION_QUAD（PE 版本资源拿不到数字版本）"; exit 1; }
	@grep -q -- '--bin chaosetctl' Makefile \
		|| { echo "错误：Makefile 未用 cargo 构建 chaosetctl"; exit 1; }
	@grep -q -- '--bin chaosetctl' shims/windows/package_msvc.sh \
		|| { echo "错误：package_msvc.sh 未用 cargo 构建 chaosetctl.exe"; exit 1; }
	@grep -q 'chaosetctl.exe" import' shims/windows/installer/chaoset.nsi \
		|| { echo "错误：NSIS 词库刷新未改用 chaosetctl.exe import"; exit 1; }
	@# 发行链不得再引用已删除的 Go CLI 命令（Go 只保留构建期词库生成与静态检查）。
	@# 分两段匹配：含 "go build" 的行与含 cmd/<已迁移命令> 的行分开写，
	@# 避免本断言自身被工具或 grep 误判成真实构建命令。
	@if grep -rn 'go build' Makefile shims scripts 2>/dev/null \
		| grep -qE 'cmd/chaoset(ctl|-cli|-import|-userdict|-settingsctl)'; then \
		echo "错误：发行/打包脚本仍在用 Go 构建已迁移到 Rust 的 CLI 命令"; exit 1; \
	fi
	@echo "版本号单一真源检查通过（tag CI 用 tag，其余用 年月日-commit hash；含 Windows 安装器/二进制版本资源与 Rust CLI 发行链）"

# 发行链已从 Go 切到 Rust：chaosetctl 是唯一的全能 CLI 二进制
# （argv[0] multicall：chaoset / chaoset-cli / chaoset-import / chaoset-userdict /
# chaoset-settingsctl），daemon 与 dictc 也都是 Rust。本目标不再调用 go build。
# 发行名必须保持：POSIX 用相对软链指向 chaosetctl；Windows/MSYS 没有可靠软链
# （且可执行文件必须是 .exe），直接复制副本。
EXE_SUFFIX := $(if $(filter Windows_NT,$(OS)),.exe,)
BIN_ALIASES := chaoset chaoset-cli chaoset-import chaoset-userdict chaoset-settingsctl
ifeq ($(OS),Windows_NT)
  BIN_ALIAS_CMD = for n in $(BIN_ALIASES); do cp "bin/chaosetctl$(EXE_SUFFIX)" "bin/$$n$(EXE_SUFFIX)"; done
else
  BIN_ALIAS_CMD = for n in $(BIN_ALIASES); do ln -sf chaosetctl "bin/$$n"; done
endif

build:
	@# 发行链已切到 Rust CLI：chaosetctl 单二进制 + argv[0] multicall（软链/副本）。
	cargo build --release --bin chaosetctl --manifest-path crates/Cargo.toml
	# daemon 唯一实现是 Rust（crates/chaosetd）：Go 版已删除。
	cargo build --release --bin chaosetd --manifest-path crates/Cargo.toml
	# 发布用二进制词库编译器（构建期工具，不随包分发）：TSV 是仓库源，打包时编译为 CSDB。
	cargo build --release --bin dictc --manifest-path crates/Cargo.toml
	mkdir -p bin
	cp crates/target/release/chaosetctl$(EXE_SUFFIX) bin/chaosetctl$(EXE_SUFFIX)
	cp crates/target/release/chaosetd$(EXE_SUFFIX) bin/chaosetd$(EXE_SUFFIX)
	cp crates/target/release/dictc$(EXE_SUFFIX) bin/chaoset-dictc$(EXE_SUFFIX)
	$(BIN_ALIAS_CMD)

# 仅存的 Go 构建期工具：内置词库生成器（data/dict_common.tsv 的生成源）。
# 发行链（CLI/daemon/dictc/设置中心）已全部 Rust，本目标以外构建不再需要 Go。
build-tools:
	mkdir -p bin
	go build -o bin/chaoset-dictgen$(EXE_SUFFIX) ./cmd/chaoset-dictgen

build-settings:
	# 注入显示版本（scripts/version.sh 单一真源）：设置中心标题只显示当前构建版本。
	CHAOSET_BUILD_VERSION='$(VERSION)' cargo build --release --manifest-path shims/settings/Cargo.toml
	mkdir -p bin
	cp shims/settings/target/release/chaoset-settings bin/chaoset-settings

CARGO_WIN_TARGET ?= $(if $(findstring Windows,$(OS)),,--target x86_64-pc-windows-gnu)

# macOS 发行包必须带 universal 设置中心（Intel Mac 也要能打开设置）。
# 本机日常开发用宿主架构的 build-settings 即可；打包/CI 用本目标。
build-settings-universal:
	@if [ "$$(uname -s)" != Darwin ]; then echo "错误：build-settings-universal 仅用于 macOS" >&2; exit 1; fi
	@for t in aarch64-apple-darwin x86_64-apple-darwin; do \
		rustup target add $$t >/dev/null 2>&1 || { echo "错误：rustup target add $$t 失败" >&2; exit 1; }; \
	done
	CHAOSET_BUILD_VERSION='$(VERSION)' cargo build --release --manifest-path shims/settings/Cargo.toml --target aarch64-apple-darwin
	CHAOSET_BUILD_VERSION='$(VERSION)' cargo build --release --manifest-path shims/settings/Cargo.toml --target x86_64-apple-darwin
	mkdir -p bin
	lipo -create shims/settings/target/aarch64-apple-darwin/release/chaoset-settings \
		shims/settings/target/x86_64-apple-darwin/release/chaoset-settings \
		-output bin/chaoset-settings
	@lipo -info bin/chaoset-settings | grep -q 'x86_64 arm64\|arm64 x86_64' || { echo "错误：设置中心不是 universal" >&2; exit 1; }

build-settings-windows:
	@# Git Bash 自带的 /usr/bin/link.exe（GNU link）会遮蔽 MSVC link.exe，
	# cargo 链接 build script 时报「extra operand ... .rcgu.o」；Windows runner 上
	# 必须把 MSVC 工具目录前置（cl.exe 同目录有 link.exe/lib.exe）。
	@if command -v cl.exe >/dev/null 2>&1; then \
		MSVC_BIN_DIR=$$(dirname "$$(command -v cl.exe)"); \
		export PATH="$$MSVC_BIN_DIR:$$PATH"; \
		if command -v cygpath >/dev/null 2>&1; then \
			export CARGO_TARGET_X86_64_PC_WINDOWS_MSVC_LINKER=$$(cygpath -w "$$MSVC_BIN_DIR/link.exe"); \
		fi; \
	fi; \
	CHAOSET_BUILD_VERSION='$(VERSION)' cargo build --release $(CARGO_WIN_TARGET) --manifest-path shims/settings/Cargo.toml

build-rust:
	cargo build --release --manifest-path crates/Cargo.toml

test:
	go test ./...
	cargo test --manifest-path shims/settings/Cargo.toml
	cargo test --manifest-path crates/Cargo.toml

vet:
	go vet ./...
	$(MAKE) check-fmt
	$(MAKE) lint-workflows
	$(MAKE) check-version
	$(MAKE) check-win32-gdi
	$(MAKE) check-uiparity

# Windows 壳 GDI 调用契约：ExtCreatePen 的 plbrush 不能为空指针。
# 2026-10-29 事故：v0.2.14 传 nullptr 导致 gdi32 空指针解引用，
# 候选窗首帧渲染即带走宿主进程；Linux CI 没有 Windows 运行时，
# 只能在源码层把这条契约固定住。详见 tools/checkgdi（Go，跨平台编译，
# make vet 不再依赖 python3）。
# 格式检查接入 make vet：CI 的 gofmt 步骤与本地 make vet 同口径（只查 git 跟踪的 .go，
# 避免 .workwork/ 下 AI 中间文件干扰；检查失败时打印违规文件清单）。
check-fmt:
	@bad=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$bad" ]; then echo "以下 Go 文件未格式化（跑 make fmt）："; echo "$$bad"; exit 1; fi

check-win32-gdi:
	go run ./tools/checkgdi shims/windows/src

# 三端 UI 一致性契约（2026-10-31）：设置窗共享文案逐字一致、三端单例、
# 学习数据 GUI 存在且 Linux 不引入引擎 DBus（Go 引擎已移除）、词库导入
# 入口、打包安装一致性（chaoset-setup/hicolor）、ui-parity
# 关键差异行存在。详见 tools/checkuiparity 文件头。
check-uiparity:
	go run ./tools/checkuiparity .

# 校验 .github/workflows 的结构完整性。
# 为什么需要它：2026-10-25 一次文本替换误删了两个 job 的头部声明，YAML 语法
# 依然合法（yaml 能解析），但 GitHub 判「workflow file issue」，CI 全线不跑。
# 详细背景与检查项见 tools/checkworkflows 的文件头注释（Go，跨平台编译）。
lint-workflows:
	go run ./tools/checkworkflows \
		--expect-jobs lint,test,race,linux-e2e,macos-build,windows-build,settings-windows \
		.github/workflows/ci.yml
	go run ./tools/checkworkflows \
		--expect-jobs test,package-macos,package-linux,package-windows,release \
		.github/workflows/release.yml

# 只格式化 git 跟踪的 .go：gofmt -l -w . 会扫到 .workwork/ 下的临时 Go 文件（AI 中间产物）。
fmt:
	gofmt -l -w $(shell git ls-files '*.go')

cover:
	@mkdir -p .workwork
	go test -coverprofile=.workwork/cover.out ./...
	go tool cover -func=.workwork/cover.out | tail -1

race:
	go test -race -count=1 ./...

run-cli:
	@echo "需要先起 daemon（另开终端）：make run-daemon；CLI 缺省连接平台默认 socket"
	cargo run --release --bin chaosetctl --manifest-path crates/Cargo.toml -- cli

run-daemon:
	cargo run --quiet --release --bin chaosetd --manifest-path crates/Cargo.toml

icons:
	python3 scripts/generate_icons.py

package-macos:
	VERSION=$(VERSION) ./shims/macos/build.sh
	@# 架构自检：主壳与安装器都必须是 universal（arm64 + x86_64）。
	@# CI 的 macos-latest 是 Apple Silicon，裸 swiftc/go build 只出 arm64，
	@# Intel Mac 用户装上会「无法打开」——这个坑 2026-10-25 实测确认过。
	@for app in shims/macos/build/ChaosetIME.app shims/macos/build/ChaosetSetup.app; do \
		bin="$$app/Contents/MacOS/$$(basename "$$app" .app)"; \
		lipo -info "$$bin" 2>/dev/null | grep -q 'x86_64 arm64\|arm64 x86_64' \
			|| { echo "错误：$$bin 不是 universal 二进制："; lipo -info "$$bin" 2>&1; exit 1; }; \
	done
	@# 随包工具也必须 universal：chaoset-import 曾因裸 go build 只出 arm64，
	@# Intel 用户点「导入词库」直接 Bad CPU type（2026-10-26 修复）。
	@# daemon/导入工具现在随 ChaosetIME.app 安装，安装器内是同一份 embedded app。
	@for b in shims/macos/build/chaosetd shims/macos/build/tis \
	           shims/macos/build/smoke shims/macos/build/chaoset-import \
	                      shims/macos/build/chaosetctl \
	           shims/macos/build/ChaosetIME.app/Contents/Resources/chaosetd \
	           shims/macos/build/ChaosetIME.app/Contents/Resources/chaoset-import \
	                      shims/macos/build/ChaosetIME.app/Contents/Resources/chaosetctl \
	           shims/macos/build/ChaosetSetup.app/Contents/Resources/tis \
	           shims/macos/build/ChaosetSetup.app/Contents/Resources/smoke; do \
		lipo -info "$$b" 2>/dev/null | grep -q 'x86_64 arm64\|arm64 x86_64' \
			|| { echo "错误：$$b 不是 universal："; lipo -info "$$b" 2>&1; exit 1; }; \
	done
	@SETTINGS_APP="shims/macos/build/ChaosetIME.app/Contents/Resources/ChaosetSettings.app"; \
		test -x "$$SETTINGS_APP/Contents/MacOS/chaoset-settings" \
			|| { echo "错误：ChaosetIME.app 缺 ChaosetSettings.app（设置中心 app bundle）" >&2; exit 1; }; \
		test -f "$$SETTINGS_APP/Contents/Info.plist" \
			|| { echo "错误：ChaosetSettings.app 缺 Info.plist" >&2; exit 1; }; \
		test -f "$$SETTINGS_APP/Contents/Resources/ChaosetSettings.icns" \
			|| { echo "错误：ChaosetSettings.app 缺图标" >&2; exit 1; }; \
		lipo -info "$$SETTINGS_APP/Contents/MacOS/chaoset-settings" 2>/dev/null | grep -q 'x86_64 arm64\|arm64 x86_64' \
			|| { echo "错误：ChaosetSettings.app 内设置中心不是 universal" >&2; exit 1; }
	@test -x shims/macos/build/ChaosetIME.app/Contents/Resources/chaosetd \
		|| { echo "错误：ChaosetIME.app 缺 daemon"; exit 1; }
	@test -f shims/macos/build/ChaosetIME.app/Contents/Resources/dict_common.tsv \
		|| { echo "错误：ChaosetIME.app 缺种子词库"; exit 1; }
	@test -f shims/macos/build/ChaosetIME.app/Contents/Resources/dict_common.bin \
		|| { echo "错误：ChaosetIME.app 缺二进制词库 dict_common.bin"; exit 1; }
	@echo "✓ 全部二进制 universal（主壳 + 安装器 + app 内置工具）"
	@# 版本号一致性：两个 app 的 CFBundleShortVersionString 必须等于 $(VERSION)
	@# （tag 发布 = tag 号；本地/日常 CI = 年月日-commit hash）。release 与日常
	@# CI 共用这条断言，避免「发版包版本与 tag 不一致」只能到用户手里才发现。
	@for app in ChaosetIME ChaosetSetup; do \
		got=$$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' \
			"shims/macos/build/$$app.app/Contents/Info.plist"); \
		[ "$$got" = "$(VERSION)" ] \
			|| { echo "错误：$$app 版本 = $$got，期望 $(VERSION)"; exit 1; }; \
	done
	@echo "✓ app 版本与 scripts/version.sh 一致：$(VERSION)"
	mkdir -p dist
	(cd shims/macos/build && zip -r -y ../../../dist/chaoset-macos.zip ChaosetIME.app ChaosetSetup.app)
	@test -f dist/chaoset-macos.zip || { echo "错误：未生成 dist/chaoset-macos.zip"; exit 1; }
	@echo "✓ macOS 安装包：dist/chaoset-macos.zip（不发布 DMG）"

# package-linux：Linux 发行包（tar.gz / deb / Arch）必须在 Linux 上原生构建。
#
# 历史：chaosetctl 曾用 GOOS/GOARCH 交叉编译，在 macOS 开发机上打包会把 Darwin
# Mach-O 塞进 Linux 包（2026-10-25 实测确认），而 CI 跑在 Linux runner 上恰好看
# 不出这个问题。发行链切到 Rust 后 chaosetctl 与 chaosetd 一样由 cargo host 构建
# （不做跨平台交叉链接），因此非 Linux 直接报错，不再产出错误的包。

package-linux:
	rm -rf dist/chaoset-linux-x86_64
	@test "$$(uname -s)" = Linux || { echo "错误：Linux 发行包必须在 Linux 上构建（chaosetctl/chaosetd 均为 cargo host 构建，不做跨平台交叉链接）" >&2; exit 1; }
	mkdir -p dist/chaoset-linux-x86_64/bin
	mkdir -p dist/chaoset-linux-x86_64/component
	mkdir -p dist/chaoset-linux-x86_64/icons
	mkdir -p dist/chaoset-linux-x86_64/data
	# 全能 CLI 是 Rust 单二进制（crates/chaosetctl，argv[0] multicall）；
	# 发行名 chaoset/chaoset-cli/chaoset-import/chaoset-userdict 由下面的软链提供。
	cargo build --release --bin chaosetctl --manifest-path crates/Cargo.toml
	cp crates/target/release/chaosetctl dist/chaoset-linux-x86_64/bin/chaosetctl
	strip dist/chaoset-linux-x86_64/bin/chaosetctl 2>/dev/null || true
	# daemon 由 Rust 实现（crates/chaosetd，已接替发行包）：交叉链接不可靠，
	# Linux 包必须在 Linux 上构建（release.yml 的 package-linux 用 ubuntu runner）。
	cargo build --release --bin chaosetd --manifest-path crates/Cargo.toml
	cp crates/target/release/chaosetd dist/chaoset-linux-x86_64/bin/chaosetd
	strip dist/chaoset-linux-x86_64/bin/chaosetd 2>/dev/null || true
	# 使用符号链接复用单一全能控制工具 chaosetctl (Multicall)，消除冗余二进制
	(cd dist/chaoset-linux-x86_64/bin && ln -sf chaosetctl chaoset && ln -sf chaosetctl chaoset-cli && ln -sf chaosetctl chaoset-import && ln -sf chaosetctl chaoset-userdict)
	# 设置窗只保留 Slint 统一设置中心：缺少它用户点设置将无窗口可用，打包必须失败。
	@test -x shims/settings/target/release/chaoset-settings || { echo "错误：缺少统一设置中心 chaoset-settings（先 make build-settings）" >&2; exit 1; }
	cp shims/settings/target/release/chaoset-settings dist/chaoset-linux-x86_64/bin/chaoset-settings
	# 引擎是平台原生壳（C++/libibus），只做 IBus UI 与按键路由；拼音/联想
	# 由 Rust chaosetd 通过 Unix socket 提供。必须在 Linux 上构建（CI/容器）。
	sh shims/linux/engine/build.sh dist/chaoset-linux-x86_64/bin/chaoset-engine
	sed "s/@VERSION@/$(VERSION)/g" shims/linux/component/chaoset.xml > dist/chaoset-linux-x86_64/component/chaoset.xml
	cp -r shims/linux/icons/hicolor dist/chaoset-linux-x86_64/icons/
	cp shims/linux/chaoset-settings.desktop dist/chaoset-linux-x86_64/
	# 词库：随包携带常用词库（dict_common.tsv），安装到 /usr/share/chaoset/dict.tsv。
	# 此前只带 558 条的 dict_seed.tsv 而引擎默认要 dict_common.tsv，是装完
	# 「引擎启动即退出」的直接原因。
	cp data/dict_common.tsv dist/chaoset-linux-x86_64/data/dict.tsv
	# 发布用二进制词库：daemon 启动直接解析 CSDB（比 TSV 快 1.4–1.7x）；用户导入词库后
	# daemon 会按 mtime 自动重建同目录 .bin 缓存，launch 参数仍指向 dict.tsv。
	cargo build --release --bin dictc --manifest-path crates/Cargo.toml
	crates/target/release/dictc data/dict_common.tsv dist/chaoset-linux-x86_64/data/dict.bin
	# 设置窗口：ibus-setup 的 <setup> 入口（三端一致的设置窗口，2026-10-29；
	# 2026-11-01 起为原生 GTK3 C，不再依赖 Python/PyGObject）。
	install -d dist/chaoset-linux-x86_64/setup
	VERSION=$(VERSION) sh shims/linux/setup/build.sh dist/chaoset-linux-x86_64/setup/chaoset-setup
	cp shims/linux/install.sh dist/chaoset-linux-x86_64/
	cp shims/linux/README.md dist/chaoset-linux-x86_64/
	cp LICENSE dist/chaoset-linux-x86_64/LICENSE
	@# 架构自检：包里的二进制必须真是 ELF，不能是开发机的 Mach-O
	@for b in dist/chaoset-linux-x86_64/bin/*; do \
		file -L "$$b" | grep -q ELF || { echo "错误：$$b 不是 ELF（Linux）可执行文件："; file -L "$$b"; exit 1; }; \
	done
	tar -czf dist/chaoset-linux-x86_64.tar.gz -C dist chaoset-linux-x86_64
	@# deb control 语法不支持 # 注释，模板里的维护说明必须在生成时剔除，
	@# 否则 dpkg-deb 报 "field name '#' must be followed by colon"。
	@if command -v dpkg-deb >/dev/null 2>&1; then \
		echo "==> 构建 Linux Debian/Ubuntu 一键安装包 (chaoset_amd64.deb)..."; \
		rm -rf dist/deb_staging; \
		mkdir -p dist/deb_staging/DEBIAN; \
		mkdir -p dist/deb_staging/usr/bin; \
		mkdir -p dist/deb_staging/usr/share/chaoset; \
		mkdir -p dist/deb_staging/usr/share/ibus/component; \
		mkdir -p dist/deb_staging/usr/share/icons; \
		mkdir -p dist/deb_staging/usr/share/applications; \
		chmod 0755 dist/deb_staging dist/deb_staging/DEBIAN; \
		cp -d dist/chaoset-linux-x86_64/bin/* dist/deb_staging/usr/bin/; \
		cp dist/chaoset-linux-x86_64/data/dict.tsv dist/deb_staging/usr/share/chaoset/; \
		cp dist/chaoset-linux-x86_64/data/dict.bin dist/deb_staging/usr/share/chaoset/; \
		install -Dm755 dist/chaoset-linux-x86_64/setup/chaoset-setup dist/deb_staging/usr/share/chaoset/chaoset-setup; \
		sed "s/@VERSION@/$(VERSION)/g" dist/chaoset-linux-x86_64/component/chaoset.xml > dist/deb_staging/usr/share/ibus/component/chaoset.xml; \
		cp -r dist/chaoset-linux-x86_64/icons/hicolor dist/deb_staging/usr/share/icons/; \
		install -Dm644 dist/chaoset-linux-x86_64/chaoset-settings.desktop dist/deb_staging/usr/share/applications/chaoset-settings.desktop; \
		sed -e '/^#/d' -e "s/@VERSION@/$(PKG_VERSION)/g" shims/linux/debian/control > dist/deb_staging/DEBIAN/control; \
		mkdir -p dist/deb_staging/usr/share/doc/chaoset-ime; \
		cp LICENSE dist/deb_staging/usr/share/doc/chaoset-ime/copyright; \
		cp shims/linux/debian/postinst dist/deb_staging/DEBIAN/postinst; \
		cp shims/linux/debian/postrm dist/deb_staging/DEBIAN/postrm; \
		chmod 0755 dist/deb_staging/DEBIAN/postinst dist/deb_staging/DEBIAN/postrm; \
		chmod 0755 dist/deb_staging/usr/bin/*; \
		chmod 0644 dist/deb_staging/usr/share/chaoset/dict.tsv; \
		chmod 0644 dist/deb_staging/usr/share/ibus/component/chaoset.xml; \
		dpkg-deb --build --root-owner-group dist/deb_staging dist/chaoset_amd64.deb || exit 1; \
		rm -rf dist/deb_staging; \
	fi
	@if command -v zstd >/dev/null 2>&1; then \
		echo "==> 构建 Arch Linux 一键安装包 (chaoset-ime-x86_64.pkg.tar.zst)..."; \
		./shims/linux/arch/package.sh dist/chaoset-linux-x86_64 dist/chaoset-ime-x86_64.pkg.tar.zst $(PKG_VERSION); \
	fi
	rm -rf dist/chaoset-linux-x86_64

# package-windows：Windows 原生 MSVC + NSIS 打包，唯一权威实现是
# shims/windows/package_msvc.sh（CI 的 windows-build job 直接跑同一脚本）。
#
# 历史：这里曾用 mingw-w64 交叉编译 TSF DLL / UI 宿主，并用 Go 交叉编译
# chaosetctl.exe。发行链切到 Rust CLI 后 chaosetctl.exe 由 cargo 构建
# （PE 图标与 VS_VERSION_INFO 由 crates/chaosetctl/build.rs 注入），Windows 包
# 必须在 Windows 上用 MSVC + NSIS 原生构建——mingw 交叉链与 MSVC 产物不是同一
# 套实现，保留两份只会让「本地能打、CI 打不出」的漂移长期存在。
package-windows:
	@case "$$(uname -s)" in \
		MINGW*|MSYS*|CYGWIN*) \
			echo "==> Windows 发行包：委派 shims/windows/package_msvc.sh（MSVC + NSIS）"; \
			bash shims/windows/package_msvc.sh ;; \
		*) \
			echo "错误：Windows 发行包必须在 Windows（Git Bash/MSYS）上构建。" >&2; \
			echo "      用 MSVC + NSIS 原生构建：bash shims/windows/package_msvc.sh" >&2; \
			echo "      （Linux/macOS 不再做 mingw 交叉打包，chaosetctl.exe 由 cargo 生成）" >&2; \
			exit 1 ;; \
	esac

package-all: package-macos package-linux package-windows
