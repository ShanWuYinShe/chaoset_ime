.PHONY: build test vet fmt cover run-cli run-daemon

build:
	go build -o bin/chaosetd ./cmd/chaosetd
	go build -o bin/chaoset-cli ./cmd/chaoset-cli
	go build -o bin/chaoset-import ./cmd/chaoset-import
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

run-cli:
	go run ./cmd/chaoset-cli

run-daemon:
	go run ./cmd/chaosetd
