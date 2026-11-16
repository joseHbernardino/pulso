#!/usr/bin/env bash
set -euo pipefail

# Build script for Pulso.
# Usage: ./scripts/build.sh [version]
#   version: tag or commit-ish to embed (default: git describe)

BINARY="pulso"
BUILD_DIR="./bin"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LDFLAGS="-s -w -X pulso.local/pulso/internal/version.Version=${VERSION} -X pulso.local/pulso/internal/version.BuildTime=${BUILD_TIME}"

echo "Building ${BINARY} ${VERSION}..."

mkdir -p "${BUILD_DIR}"

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

echo "  OS:      ${GOOS}"
echo "  Arch:    ${GOARCH}"
echo "  Version: ${VERSION}"
echo "  Time:    ${BUILD_TIME}"

CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "${LDFLAGS}" \
    -o "${BUILD_DIR}/${BINARY}" \
    ./cmd/pulso

echo "Built: ${BUILD_DIR}/${BINARY}"
"${BUILD_DIR}/${BINARY}" version
