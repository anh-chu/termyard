#!/bin/sh
# Termyard installer.
#
#   curl -sSL https://raw.githubusercontent.com/anh-chu/termyard/master/scripts/install.sh | sh
#
# Pin a version:
#   VERSION=v0.12.0 curl -sSL https://raw.githubusercontent.com/anh-chu/termyard/master/scripts/install.sh | sh
#
# Override the install directory:
#   BIN_DIR=~/.local/bin curl -sSL .../install.sh | sh
#
# tmux (required at runtime) is installed automatically. Skip it with:
#   SKIP_TMUX=1 curl -sSL .../install.sh | sh

set -eu

REPO="anh-chu/termyard"
BIN="termyard"

err() { printf 'error: %s\n' "$1" >&2; exit 1; }
info() { printf '%s\n' "$1" >&2; }

# --- requirements ---
command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar  >/dev/null 2>&1 || err "tar is required"

# --- detect platform ---
os=$(uname -s)
case "$os" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *) err "unsupported OS: $os (only linux and darwin have release builds)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac

# --- resolve version ---
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  info "Resolving latest release..."
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)
  [ -n "$VERSION" ] || err "could not determine latest release (has one been published yet?)"
fi
# normalize to a leading 'v'
case "$VERSION" in v*) ;; *) VERSION="v${VERSION}" ;; esac
NUM="${VERSION#v}"

ASSET="${BIN}-${VERSION}-${OS}-${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

# --- download + verify ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
info "Downloading ${ASSET} (${VERSION})..."
curl -fsSL "${BASE}/${ASSET}" -o "${tmp}/${ASSET}" \
  || err "download failed: ${BASE}/${ASSET}"

if curl -fsSL "${BASE}/checksums.txt" -o "${tmp}/checksums.txt" 2>/dev/null; then
  want=$(grep " ${ASSET}\$" "${tmp}/checksums.txt" | awk '{print $1}')
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "${tmp}/${ASSET}" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "${tmp}/${ASSET}" | awk '{print $1}')
    else
      got=""
    fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      err "checksum mismatch for ${ASSET}"
    fi
    [ -n "$got" ] && info "Checksum verified."
  fi
else
  info "checksums.txt not found, skipping verification."
fi

# --- extract ---
tar -xzf "${tmp}/${ASSET}" -C "$tmp"
[ -f "${tmp}/${BIN}" ] || err "binary '${BIN}' not found in archive"
chmod +x "${tmp}/${BIN}"

# --- choose install dir ---
# Default to a user-scoped dir; opt into a system path with BIN_DIR=/usr/local/bin.
if [ -n "${BIN_DIR:-}" ]; then
  DEST="$BIN_DIR"
else
  DEST="${HOME}/.local/bin"
fi
mkdir -p "$DEST" || err "cannot create install dir: $DEST"

if [ -w "$DEST" ]; then
  mv "${tmp}/${BIN}" "${DEST}/${BIN}"
elif command -v sudo >/dev/null 2>&1; then
  info "Elevating with sudo to write ${DEST}..."
  sudo mv "${tmp}/${BIN}" "${DEST}/${BIN}"
else
  err "no write permission for ${DEST} and sudo is unavailable"
fi

info ""
info "Installed ${BIN} ${NUM} to ${DEST}/${BIN}"
case ":${PATH}:" in
  *":${DEST}:"*) ;;
  *) info "Note: ${DEST} is not on your PATH. Add it, e.g.:"
     info "  export PATH=\"${DEST}:\$PATH\"" ;;
esac

# --- tmux dependency (required at runtime) ---
# termyard cannot run without tmux. Install it by default; skip with SKIP_TMUX=1.
if ! command -v tmux >/dev/null 2>&1; then
  # Linux package managers need root. Use sudo only when not already root.
  if [ "$(id -u)" = "0" ]; then
    SUDO=""
  elif command -v sudo >/dev/null 2>&1; then
    SUDO="sudo "
  else
    SUDO=""
  fi

  if [ "$OS" = "darwin" ]; then
    tmux_cmd="brew install tmux"
  elif command -v apt-get >/dev/null 2>&1; then
    tmux_cmd="${SUDO}apt-get install -y tmux"
  elif command -v dnf >/dev/null 2>&1; then
    tmux_cmd="${SUDO}dnf install -y tmux"
  elif command -v yum >/dev/null 2>&1; then
    tmux_cmd="${SUDO}yum install -y tmux"
  elif command -v pacman >/dev/null 2>&1; then
    tmux_cmd="${SUDO}pacman -S --noconfirm tmux"
  elif command -v zypper >/dev/null 2>&1; then
    tmux_cmd="${SUDO}zypper install -y tmux"
  elif command -v apk >/dev/null 2>&1; then
    tmux_cmd="${SUDO}apk add tmux"
  else
    tmux_cmd=""
  fi

  if [ "${SKIP_TMUX:-}" = "1" ]; then
    info ""
    info "Note: tmux is required to run ${BIN} but was not found (SKIP_TMUX=1)."
    [ -n "$tmux_cmd" ] && info "  Install it:  ${tmux_cmd}"
  elif [ "$OS" = "darwin" ] && ! command -v brew >/dev/null 2>&1; then
    err "tmux is required but not installed, and Homebrew was not found. Install tmux (e.g. via Homebrew), then re-run."
  elif [ -n "$tmux_cmd" ]; then
    info "tmux not found; installing (${tmux_cmd})..."
    sh -c "$tmux_cmd" || err "tmux install failed. Install it manually (${tmux_cmd}) and re-run."
    command -v tmux >/dev/null 2>&1 || err "tmux still not found after install. Install it manually and re-run."
    info "tmux installed."
  else
    err "tmux is required but not installed, and no supported package manager was found. Install tmux and re-run."
  fi
fi

info "Run '${BIN} server' to start."
