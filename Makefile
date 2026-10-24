.PHONY: build test vet fmt cover race run-cli run-daemon icons package-linux package-windows package-macos package-all

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
	mkdir -p dist
	(cd shims/macos/build && zip -r -y ../../../dist/chaoset-macos.zip ChaosetIME.app ChaosetSetup.app)

package-linux:
	mkdir -p dist/chaoset-linux-x86_64/bin
	mkdir -p dist/chaoset-linux-x86_64/component
	mkdir -p dist/chaoset-linux-x86_64/icons
	mkdir -p dist/chaoset-linux-x86_64/data
	go build -o dist/chaoset-linux-x86_64/bin/chaosetd ./cmd/chaosetd
	go build -o dist/chaoset-linux-x86_64/bin/chaoset-cli ./cmd/chaoset-cli
	go build -o dist/chaoset-linux-x86_64/bin/chaoset-import ./cmd/chaoset-import
	go build -o dist/chaoset-linux-x86_64/bin/chaoset-userdict ./cmd/chaoset-userdict
	go build -o dist/chaoset-linux-x86_64/bin/chaoset-engine ./shims/linux/ibus-engine/
	cp shims/linux/component/chaoset.xml dist/chaoset-linux-x86_64/component/
	cp -r shims/linux/icons/hicolor dist/chaoset-linux-x86_64/icons/
	cp data/dict_seed.tsv dist/chaoset-linux-x86_64/data/
	cp shims/linux/README.md dist/chaoset-linux-x86_64/
	tar -czf dist/chaoset-linux-x86_64.tar.gz -C dist chaoset-linux-x86_64
	rm -rf dist/chaoset-linux-x86_64

package-windows:
	mkdir -p dist/chaoset-windows-x64
	GOOS=windows GOARCH=amd64 go build -o dist/chaoset-windows-x64/chaosetd.exe ./cmd/chaosetd
	GOOS=windows GOARCH=amd64 go build -o dist/chaoset-windows-x64/chaoset-cli.exe ./cmd/chaoset-cli
	x86_64-w64-mingw32-g++ -std=c++17 -O2 -Wall -Wextra -shared -static \
		-o dist/chaoset-windows-x64/ChaosetIME.dll \
		shims/windows/src/win32.cpp shims/windows/src/core.cpp \
		-lole32 -luuid -lws2_32 -luser32 -lkernel32
	cp shims/windows/resources/chaoset.ico dist/chaoset-windows-x64/
	cp data/dict_seed.tsv dist/chaoset-windows-x64/
	cp shims/windows/README.md dist/chaoset-windows-x64/
	(cd dist && zip -r chaoset-windows-x64.zip chaoset-windows-x64)
	rm -rf dist/chaoset-windows-x64

package-all: package-macos package-linux package-windows

