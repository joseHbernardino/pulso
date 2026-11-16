.PHONY: build test lint vet clean install fmt fmt-check

BINARY=pulso
BUILD_DIR=./bin
GO=go
GOFLAGS=-trimpath
LDFLAGS=-ldflags "-s -w -X pulso.local/pulso/internal/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) -X pulso.local/pulso/internal/version.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)"

build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/pulso

test:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed"; exit 1; fi

fmt:
	gofmt -w .

fmt-check:
	@! gofmt -l . | grep -q . || (echo "Files need formatting:"; gofmt -l .; exit 1)

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/

run: build
	$(BUILD_DIR)/$(BINARY) $(ARGS)

all: build test vet
