#!/usr/bin/env bash
set -euo pipefail

BINARY="pulso"
REPO="${PULSO_RELEASE_REPOSITORY:-}"
if [[ ! "$REPO" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]]; then
    echo "Set PULSO_RELEASE_REPOSITORY to your GitHub owner/repository before installing Pulso."
    exit 1
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
    linux|darwin) ;;
    *) echo "Error: unsupported OS: $OS"; exit 1 ;;
esac
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Error: unsupported architecture: $ARCH"; exit 1 ;;
esac

INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -nE 's/.*"tag_name": *"([^"]+)".*/\1/p' | head -1)
fi
if [[ ! "$VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "Error: invalid or unavailable release version"
    exit 1
fi

TASK_TMP="$(mktemp -d)"
trap 'rm -rf "$TASK_TMP"' EXIT
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
if ! curl -fsSL "${BASE_URL}/${ARCHIVE}" -o "${TASK_TMP}/package.tar.gz"; then
    ARCHIVE="${BINARY}_${OS}_${ARCH}.tar.gz"
    curl -fsSL "${BASE_URL}/${ARCHIVE}" -o "${TASK_TMP}/package.tar.gz"
fi
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TASK_TMP}/checksums.txt"
EXPECTED="$(awk -v name="$ARCHIVE" '$2 == name { print $1; exit }' "${TASK_TMP}/checksums.txt")"
if [[ ! "$EXPECTED" =~ ^[a-fA-F0-9]{64}$ ]]; then
    echo "Error: release archive has no valid SHA-256 checksum"
    exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "${TASK_TMP}/package.tar.gz" | awk '{ print $1 }')"
else
    ACTUAL="$(shasum -a 256 "${TASK_TMP}/package.tar.gz" | awk '{ print $1 }')"
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Error: checksum mismatch; installation cancelled"
    exit 1
fi

tar -xzf "${TASK_TMP}/package.tar.gz" -C "$TASK_TMP"
EXTRACTED=""
for candidate in "${TASK_TMP}/${BINARY}" "${TASK_TMP}/"*/"${BINARY}"; do
    if [ -f "$candidate" ]; then EXTRACTED="$candidate"; break; fi
done
if [ -z "$EXTRACTED" ]; then
    echo "Error: Pulso executable not found in the archive"
    exit 1
fi
mkdir -p "$INSTALL_DIR"
chmod +x "$EXTRACTED"
mv "$EXTRACTED" "${INSTALL_DIR}/${BINARY}"
echo "Installed Pulso ${VERSION} to ${INSTALL_DIR}/${BINARY}"
echo "Ensure ${INSTALL_DIR} is on your PATH."
