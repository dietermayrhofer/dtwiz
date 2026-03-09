#!/usr/bin/env sh
# install.sh — Download and run dtingest for the current platform.
#
# Usage:
#   ./install.sh --environment <env-url> \
#                --access-token <token> \
#                --platform-token <token> \
#                [dtingest args...]
#
# All three credential flags are optional; omit any that are not required for
# your chosen installation method.  Any extra arguments after the three known
# flags are forwarded verbatim to dtingest (default: "setup").
#
# The script requires either:
#   • the GitHub CLI (gh) — recommended for private repos, or
#   • curl + a GITHUB_TOKEN environment variable with repo read access.

set -e

REPO="dietermayrhofer/dtingest"

# ── Parse known flags ──────────────────────────────────────────────────────────
DT_ENVIRONMENT=""
DT_ACCESS_TOKEN=""
DT_PLATFORM_TOKEN=""
EXTRA_ARGS=""

while [ $# -gt 0 ]; do
    case "$1" in
        --environment|-e)
            DT_ENVIRONMENT="$2"; shift 2 ;;
        --access-token|-a)
            DT_ACCESS_TOKEN="$2"; shift 2 ;;
        --platform-token|-p)
            DT_PLATFORM_TOKEN="$2"; shift 2 ;;
        *)
            EXTRA_ARGS="$EXTRA_ARGS $1"; shift ;;
    esac
done

# Trim leading space
EXTRA_ARGS="${EXTRA_ARGS# }"

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
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

if command -v gh >/dev/null 2>&1; then
    gh release download "$VERSION" \
        --repo "$REPO" \
        --pattern "$ARCHIVE" \
        --dir "$TMPDIR"
else
    curl -fsSL \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        "https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}" \
        -o "${TMPDIR}/${ARCHIVE}"
fi

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"
BINARY="${TMPDIR}/dtingest"

if [ ! -f "$BINARY" ]; then
    echo "Error: dtingest binary not found after extraction." >&2
    exit 1
fi

chmod +x "$BINARY"

# ── Set credentials as environment variables ──────────────────────────────────
[ -n "$DT_ENVIRONMENT" ]    && export DT_ENVIRONMENT
[ -n "$DT_ACCESS_TOKEN" ]   && export DT_ACCESS_TOKEN
[ -n "$DT_PLATFORM_TOKEN" ] && export DT_PLATFORM_TOKEN

# ── Run dtingest ───────────────────────────────────────────────────────────────
if [ -z "$EXTRA_ARGS" ]; then
    exec "$BINARY" setup
else
    # shellcheck disable=SC2086
    exec "$BINARY" $EXTRA_ARGS
fi
