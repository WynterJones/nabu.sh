#!/bin/sh
# Installs Nabu from a published release. Downloads a prebuilt archive for this
# machine, checks it against the release checksums, and puts `nabu` and `nabud`
# on your PATH. Nothing is compiled, so no Go or Node toolchain is required.
#
#   curl -fsSL https://nabu.sh/install.sh | bash
#
# Environment overrides:
#   NABU_VERSION      release tag to install, default the latest
#   NABU_INSTALL_DIR  where the binaries go, default ~/.local/bin
#   NABU_REPO         owner/name to install from
#
# Pass --from-source to build a local checkout instead.
set -eu

NABU_REPO=${NABU_REPO:-"WynterJones/nabu.sh"}
NABU_VERSION=${NABU_VERSION:-latest}
NABU_USER_HOME=${NABU_USER_HOME:-$(cd && pwd)}
NABU_INSTALL_DIR=${NABU_INSTALL_DIR:-"$NABU_USER_HOME/.local/bin"}
NABU_TEMP_DIR=

cleanup() {
  if [ -n "$NABU_TEMP_DIR" ] && [ -d "$NABU_TEMP_DIR" ]; then
    rm -rf -- "$NABU_TEMP_DIR"
  fi
}
trap cleanup EXIT HUP INT TERM

die() {
  echo "nabu: $1" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but was not found."
}

# ---------------------------------------------------------------- from source

if [ "${1:-}" = "--from-source" ]; then
  NABU_SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || pwd)
  [ -f "$NABU_SCRIPT_DIR/go.mod" ] || die "--from-source must be run from a checkout."
  for tool in go node npm; do
    command -v "$tool" >/dev/null 2>&1 || die "building from source needs $tool."
  done
  mkdir -p "$NABU_INSTALL_DIR"
  (
    cd "$NABU_SCRIPT_DIR/frontend"
    npm ci
    npm run build
  )
  find "$NABU_SCRIPT_DIR/webassets/dist" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  cp -R "$NABU_SCRIPT_DIR/frontend/dist/." "$NABU_SCRIPT_DIR/webassets/dist/"
  (
    cd "$NABU_SCRIPT_DIR"
    go build -trimpath -o "$NABU_INSTALL_DIR/nabu" ./cmd/nabu
    go build -trimpath -o "$NABU_INSTALL_DIR/nabud" ./cmd/nabud
  )
  echo "Built nabu and nabud into $NABU_INSTALL_DIR"
  exit 0
fi

# ------------------------------------------------------------------- platform

need curl
need tar

NABU_OS=$(uname -s)
NABU_ARCH=$(uname -m)

case "$NABU_OS" in
  Darwin) NABU_OS=darwin ;;
  Linux) NABU_OS=linux ;;
  *) die "unsupported operating system: $NABU_OS. macOS and Linux are supported, and Windows through WSL2." ;;
esac

case "$NABU_ARCH" in
  x86_64 | amd64) NABU_ARCH=amd64 ;;
  arm64 | aarch64) NABU_ARCH=arm64 ;;
  *) die "unsupported architecture: $NABU_ARCH" ;;
esac

# Prefer whichever digest tool this platform ships; both print it first.
if command -v shasum >/dev/null 2>&1; then
  digest_of() { shasum -a 256 "$1" | cut -d' ' -f1; }
elif command -v sha256sum >/dev/null 2>&1; then
  digest_of() { sha256sum "$1" | cut -d' ' -f1; }
else
  die "neither shasum nor sha256sum was found, so the download cannot be verified."
fi

# --------------------------------------------------------------------- version

if [ "$NABU_VERSION" = latest ]; then
  NABU_VERSION=$(
    curl -fsSL "https://api.github.com/repos/$NABU_REPO/releases/latest" |
      sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
      head -n 1
  ) || die "could not reach the GitHub releases API."
  [ -n "$NABU_VERSION" ] || die "no published release was found for $NABU_REPO."
fi

# Tags carry a leading v, archive names carry the bare version.
NABU_NUMBER=${NABU_VERSION#v}
NABU_ARCHIVE="nabu_${NABU_NUMBER}_${NABU_OS}_${NABU_ARCH}.tar.gz"
NABU_BASE="https://github.com/$NABU_REPO/releases/download/$NABU_VERSION"

# -------------------------------------------------------------------- download

NABU_TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nabu-install.XXXXXX")

echo "Downloading Nabu $NABU_VERSION for $NABU_OS/$NABU_ARCH"
curl -fsSL -o "$NABU_TEMP_DIR/$NABU_ARCHIVE" "$NABU_BASE/$NABU_ARCHIVE" ||
  die "could not download $NABU_ARCHIVE. Check that $NABU_VERSION has a build for $NABU_OS/$NABU_ARCH."
curl -fsSL -o "$NABU_TEMP_DIR/checksums.txt" "$NABU_BASE/checksums.txt" ||
  die "could not download the checksums for $NABU_VERSION."

NABU_EXPECTED=$(grep " $NABU_ARCHIVE\$" "$NABU_TEMP_DIR/checksums.txt" | cut -d' ' -f1)
[ -n "$NABU_EXPECTED" ] || die "$NABU_ARCHIVE is not listed in the release checksums."

NABU_ACTUAL=$(digest_of "$NABU_TEMP_DIR/$NABU_ARCHIVE")
[ "$NABU_ACTUAL" = "$NABU_EXPECTED" ] || die "checksum mismatch for $NABU_ARCHIVE. Refusing to install."

tar -xzf "$NABU_TEMP_DIR/$NABU_ARCHIVE" -C "$NABU_TEMP_DIR"
[ -f "$NABU_TEMP_DIR/nabu" ] && [ -f "$NABU_TEMP_DIR/nabud" ] ||
  die "the archive did not contain both binaries."

# --------------------------------------------------------------------- install

mkdir -p "$NABU_INSTALL_DIR"

# Writing over a running binary fails with ETXTBSY, but renaming over it is
# fine: the running process keeps the old inode until it exits.
for NABU_BINARY in nabu nabud; do
  chmod 0755 "$NABU_TEMP_DIR/$NABU_BINARY"
  mv -f "$NABU_TEMP_DIR/$NABU_BINARY" "$NABU_INSTALL_DIR/$NABU_BINARY"
done

echo "Installed nabu and nabud in $NABU_INSTALL_DIR"

case ":${PATH}:" in
  *":${NABU_INSTALL_DIR}:"*)
    echo "Run: nabu setup"
    ;;
  *)
    echo
    echo "Add $NABU_INSTALL_DIR to your PATH, then run: nabu setup"
    echo "  echo 'export PATH=\"\$PATH:$NABU_INSTALL_DIR\"' >> ~/.zshrc"
    ;;
esac
