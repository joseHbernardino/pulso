binary   := "pulso"
buildDir := "./bin"
go       := "go"
goflags  := "-trimpath"

# Inject version info via ldflags
ldflags := "-s -w " + \
    "-X pulso.local/pulso/internal/version.Version=" + `git describe --tags --always --dirty 2>/dev/null || echo dev` + \
    " -X pulso.local/pulso/internal/version.BuildTime=" + `date -u +%Y-%m-%dT%H:%M:%SZ`

# Default: show available recipes
default:
    @just --list

# Build the binary
build:
    CGO_ENABLED=0 {{ go }} build {{ goflags }} -ldflags "{{ ldflags }}" -o {{ buildDir }}/{{ binary }} ./cmd/pulso

# Run tests with race detector
test:
    {{ go }} test -race -count=1 ./...

# Run go vet
vet:
    {{ go }} vet ./...

# Run golangci-lint (skips if not installed)
lint:
    @which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

# Format Go source
fmt:
    gofmt -w .

# Check formatting without modifying files
fmt-check:
    @! gofmt -l . | grep -q . || (echo "Files need formatting:"; gofmt -l .; exit 1)

# Recompute the Nix vendorHash after a dependency change (requires Nix)
vendor-hash:
    scripts/update-vendor-hash.sh

# Remove build artifacts
clean:
    rm -rf {{ buildDir }}

# Build and install to /usr/local/bin
install: build
    cp {{ buildDir }}/{{ binary }} /usr/local/bin/

# Build and run the binary (pass args via `just run -- arg1 arg2`)
[linux]
[macos]
run args="": build
    {{ buildDir }}/{{ binary }} {{ args }}

# Build, test, and vet
all: build test vet
