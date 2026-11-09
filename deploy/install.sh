#!/bin/sh
# Latch relay installer. Usage:
#   curl -sSL https://get.latch.example/install.sh | sh
#   curl -sSL .../install.sh | sh -s -- --version v1.2.3
#
# Downloads the latch-relay binary for this OS/arch, installs it to
# /usr/local/bin, and launches the interactive setup wizard. Re-runnable.
set -eu

REPO="kayaramazan/latch"
BASE_URL="${LATCH_BASE_URL:-https://github.com/${REPO}/releases}"
VERSION="latest"
BIN_DIR="${LATCH_BIN_DIR:-/usr/local/bin}"
RUN_WIZARD=1

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    --no-wizard) RUN_WIZARD=0; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# Elevate for writing to system directories.
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 && SUDO="sudo" || die "run as root or install sudo"
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux) : ;;
  darwin) : ;;
  *) die "unsupported OS: $os (the relay targets Linux servers)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac

asset="latch-relay_${os}_${arch}"
if [ "$VERSION" = "latest" ]; then
  url="${BASE_URL}/latest/download/${asset}"
else
  url="${BASE_URL}/download/${VERSION}/${asset}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "Downloading ${asset} (${VERSION})"
if command -v curl >/dev/null 2>&1; then
  curl -fSL "$url" -o "$tmp/latch-relay" || die "download failed: $url"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$tmp/latch-relay" "$url" || die "download failed: $url"
else
  die "need curl or wget"
fi
chmod +x "$tmp/latch-relay"

say "Installing to ${BIN_DIR}/latch-relay"
$SUDO install -m 0755 "$tmp/latch-relay" "${BIN_DIR}/latch-relay"

# Create the service user and data dir (ignore if they already exist).
if [ "$os" = "linux" ]; then
  $SUDO id latch-relay >/dev/null 2>&1 || $SUDO useradd --system --no-create-home --shell /usr/sbin/nologin latch-relay 2>/dev/null || true
  $SUDO mkdir -p /var/lib/latch-relay /etc/latch-relay
  $SUDO chown latch-relay:latch-relay /var/lib/latch-relay 2>/dev/null || true
fi

say "Installed $(${BIN_DIR}/latch-relay version)"
if [ "$RUN_WIZARD" -eq 1 ]; then
  say "Starting setup wizard"
  exec $SUDO "${BIN_DIR}/latch-relay" install --data-dir /var/lib/latch-relay
else
  say "Run 'sudo latch-relay install' to finish setup."
fi
