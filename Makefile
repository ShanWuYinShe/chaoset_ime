.PHONY: build test vet fmt run-cli run-daemon

build:
	go build -o bin/chaosetd ./cmd/chaosetd
	go build -o bin/chaoset-cli ./cmd/chaoset-cli
	go build -o bin/chaoset-import ./cmd/chaoset-import

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

run-cli:
	go run ./cmd/chaoset-cli

run-daemon:
	go run ./cmd/chaosetd
