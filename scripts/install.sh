#!/usr/bin/env sh
# install.sh — Download and install dtingest for the current platform.
#
# Usage:
#   ./install.sh [--install-dir <dir>]
#
# By default the binary is installed to /usr/local/bin (requires sudo) or
# ~/bin if /usr/local/bin is not writable.  Pass --install-dir to override.
#
# The script requires either:
#   • the GitHub CLI (gh) — recommended for private repos, or
#   • curl + a GITHUB_TOKEN environment variable with repo read access.

set -e

REPO="dietermayrhofer/dtingest"

# ── Parse known flags ──────────────────────────────────────────────────────────
INSTALL_DIR=""

while [ $# -gt 0 ]; do
    case "$1" in
        --install-dir)
            INSTALL_DIR="$2"; shift 2 ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 1 ;;
    esac
done

# ── Detect OS ─────────────────────────────────────────────────────────────────
OS_RAW="$(uname -s)"
case "$OS_RAW" in
    Darwin) OS="darwin" ;;
    Linux)  OS="linux"  ;;
    *)
        echo "Unsupported OS: $OS_RAW" >&2
        echo "Use install.ps1 on Windows." >&2
        exit 1 ;;
esac

# ── Detect architecture ───────────────────────────────────────────────────────
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
    x86_64)         ARCH="amd64" ;;
    arm64|aarch64)  ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH_RAW" >&2
        exit 1 ;;
esac

echo "Detected platform: ${OS}/${ARCH}"

# ── Resolve latest release version ────────────────────────────────────────────
if command -v gh >/dev/null 2>&1; then
    VERSION="$(gh release view --repo "$REPO" --json tagName -q '.tagName' 2>/dev/null)"
elif [ -n "$GITHUB_TOKEN" ]; then
    VERSION="$(curl -fsSL \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "Accept: application/vnd.github+json" \
        "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
else
    echo "Error: neither 'gh' CLI nor GITHUB_TOKEN is available." >&2
    echo "  Install the GitHub CLI (https://cli.github.com/) or set GITHUB_TOKEN." >&2
    exit 1
fi

if [ -z "$VERSION" ]; then
    echo "Error: could not determine the latest dtingest version." >&2
    exit 1
fi

echo "Downloading dtingest ${VERSION}..."

# ── Download and extract ───────────────────────────────────────────────────────
ARCHIVE="dtingest_${VERSION#v}_${OS}_${ARCH}.tar.gz"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

if command -v gh >/dev/null 2>&1; then
    gh release download "$VERSION" \
        --repo "$REPO" \
        --pattern "$ARCHIVE" \
        --dir "$WORK_DIR"
else
    curl -fsSL \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        "https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}" \
        -o "${WORK_DIR}/${ARCHIVE}"
fi

tar -xzf "${WORK_DIR}/${ARCHIVE}" -C "$WORK_DIR"

if [ ! -f "${WORK_DIR}/dtingest" ]; then
    echo "Error: dtingest binary not found after extraction." >&2
    exit 1
fi

chmod +x "${WORK_DIR}/dtingest"

# ── Determine install directory ────────────────────────────────────────────────
if [ -z "$INSTALL_DIR" ]; then
    if [ -w "/usr/local/bin" ]; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="$HOME/bin"
        mkdir -p "$INSTALL_DIR"
    fi
fi

# ── Install binary ─────────────────────────────────────────────────────────────
DEST="${INSTALL_DIR}/dtingest"
if [ -w "$INSTALL_DIR" ]; then
    mv "${WORK_DIR}/dtingest" "$DEST"
else
    echo "Installing to ${INSTALL_DIR} requires elevated privileges..."
    sudo mv "${WORK_DIR}/dtingest" "$DEST"
fi

echo ""
echo "dtingest ${VERSION} installed to ${DEST}"

# ── Add to PATH in shell profile if needed ─────────────────────────────────────
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*)
        # Already in the current session's PATH — nothing to do.
        ;;
    *)
        # Detect shell profile file
        PROFILE_FILE=""
        case "${SHELL}" in
            */zsh)
                PROFILE_FILE="${HOME}/.zshrc" ;;
            */bash)
                if [ "$(uname -s)" = "Darwin" ]; then
                    PROFILE_FILE="${HOME}/.bash_profile"
                else
                    PROFILE_FILE="${HOME}/.bashrc"
                fi ;;
            *)
                PROFILE_FILE="${HOME}/.profile" ;;
        esac

        EXPORT_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""

        if [ -n "$PROFILE_FILE" ]; then
            # Only append if the line isn't already present
            if ! grep -qF "${INSTALL_DIR}" "${PROFILE_FILE}" 2>/dev/null; then
                printf '\n# Added by dtingest installer\n%s\n' "$EXPORT_LINE" >> "$PROFILE_FILE"
                echo ""
                echo "  Added ${INSTALL_DIR} to PATH in ${PROFILE_FILE}"
                echo "  Run: source ${PROFILE_FILE}  (or open a new terminal)"
            fi
        else
            echo ""
            echo "  NOTE: ${INSTALL_DIR} is not in your PATH."
            echo "  Add it with: ${EXPORT_LINE}"
        fi
        ;;
esac
