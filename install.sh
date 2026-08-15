#!/bin/sh
set -eu

NABU_SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || pwd)
NABU_SOURCE_DIR=$NABU_SCRIPT_DIR
NABU_USER_HOME=${NABU_USER_HOME:-$(cd && pwd)}
NABU_INSTALL_DIR=${NABU_INSTALL_DIR:-"$NABU_USER_HOME/.local/bin"}
NABU_REPO_URL=${NABU_REPO_URL:-"https://github.com/WynterJones/nabu.sh.git"}
NABU_TEMP_DIR=

cleanup() {
  if [ -n "$NABU_TEMP_DIR" ] && [ -d "$NABU_TEMP_DIR" ]; then
    rm -rf -- "$NABU_TEMP_DIR"
  fi
}
trap cleanup EXIT HUP INT TERM

# When invoked through curl | bash, $0 identifies the shell rather than this
# script. Fetch a clean source tree in that case. A local checkout continues to
# build itself without network cloning.
if [ ! -f "$NABU_SOURCE_DIR/go.mod" ] || [ ! -d "$NABU_SOURCE_DIR/frontend" ]; then
  if ! command -v git >/dev/null 2>&1; then
    echo "Nabu needs Git to download the source release." >&2
    exit 1
  fi
  NABU_TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nabu-install.XXXXXX")
  git clone --depth 1 "$NABU_REPO_URL" "$NABU_TEMP_DIR/source"
  NABU_SOURCE_DIR="$NABU_TEMP_DIR/source"
fi

for NABU_TOOL in go node npm git; do
  if ! command -v "$NABU_TOOL" >/dev/null 2>&1; then
    echo "Nabu needs $NABU_TOOL to install from source." >&2
    exit 1
  fi
done

mkdir -p "$NABU_INSTALL_DIR"
(
  cd "$NABU_SOURCE_DIR/frontend"
  npm ci
  npm run build
)
find "$NABU_SOURCE_DIR/webassets/dist" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -R "$NABU_SOURCE_DIR/frontend/dist/." "$NABU_SOURCE_DIR/webassets/dist/"
(
  cd "$NABU_SOURCE_DIR"
  go build -trimpath -o "$NABU_INSTALL_DIR/nabu" ./cmd/nabu
  go build -trimpath -o "$NABU_INSTALL_DIR/nabud" ./cmd/nabud
)

echo "Installed nabu and nabud in $NABU_INSTALL_DIR"
if [ ":${PATH}:" != *":${NABU_INSTALL_DIR}:"* ]; then
  echo "Add $NABU_INSTALL_DIR to PATH, then run: nabu setup"
else
  echo "Run: nabu setup"
fi
