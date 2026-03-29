#!/bin/sh
# Installs the Outgate CLI (og) on macOS and Linux.
# (Replaces the old gw installer.)
# Usage: curl -fsSL https://dev.outgate.ai/download/install.sh | sh

main() {

set -eu

BINARY_NAME="og"
BASE_URL="https://dev.outgate.ai/download"

red="$( (/usr/bin/tput bold 2>/dev/null || :; /usr/bin/tput setaf 1 2>/dev/null || :) 2>&-)"
green="$( (/usr/bin/tput bold 2>/dev/null || :; /usr/bin/tput setaf 2 2>/dev/null || :) 2>&-)"
plain="$( (/usr/bin/tput sgr0 2>/dev/null || :) 2>&-)"

status() { echo ">>> $*" >&2; }
error()  { echo "${red}ERROR:${plain} $*" >&2; exit 1; }

TEMP_DIR=$(mktemp -d)
cleanup() { rm -rf "$TEMP_DIR"; }
trap cleanup EXIT

# -- Platform detection --

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Darwin)  OS="darwin" ;;
    Linux)   OS="linux" ;;
    *)       error "Unsupported OS: $OS. Only macOS and Linux are supported." ;;
esac

case "$ARCH" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)             error "Unsupported architecture: $ARCH" ;;
esac

# -- Version --

if [ -n "${OG_VERSION:-}" ]; then
    VERSION="$OG_VERSION"
else
    VERSION="latest"
fi

# -- Download --

DOWNLOAD_URL="${BASE_URL}/${VERSION}/${BINARY_NAME}-${OS}-${ARCH}"

status "Downloading og for ${OS}/${ARCH}..."

if ! curl --fail --show-error --location --progress-bar \
    -o "${TEMP_DIR}/${BINARY_NAME}" "$DOWNLOAD_URL"; then
    error "Download failed. Check your internet connection and try again."
fi

chmod +x "${TEMP_DIR}/${BINARY_NAME}"

# -- Install --

INSTALL_DIR="/usr/local/bin"

# Try without sudo first, fall back to sudo
if [ -w "$INSTALL_DIR" ]; then
    mv "${TEMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
else
    status "Installing to ${INSTALL_DIR} (may require password)..."
    sudo mv "${TEMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
fi

# -- Verify --

if command -v og >/dev/null 2>&1; then
    INSTALLED_VERSION=$(og --version 2>/dev/null || echo "unknown")
    echo ""
    echo "${green}og installed successfully!${plain} (${INSTALLED_VERSION})"
    echo ""
    echo "Get started:"
    echo "  og login      Sign in to your Outgate account"
    echo "  og status     View account, providers, and usage"
    echo "  og --help     See all available commands"
    echo ""
else
    error "Installation succeeded but 'og' is not in PATH. Add ${INSTALL_DIR} to your PATH."
fi

}

# Wrap in main() so a partial download doesn't execute half the script
main
