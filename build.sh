#!/usr/bin/env bash
# build.sh — Local build script for SSHCustom-VPNChain
# Builds daemon for arm64-android, packages Magisk ZIP.
# Run from repo root.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
VERSION="$(cat "$ROOT/VERSION" | tr -d '[:space:]')"
DIST="$ROOT/dist"
DAEMON_DIR="$ROOT/daemon"
MODULE_DIR="$ROOT/module"
ZIP_OUT="$DIST/SSHCustom-VPNChain-v${VERSION}.zip"
LDFLAGS="-s -w -buildid= -X main.version=${VERSION}"

echo "==> Building SSHCustom-VPNChain v${VERSION}"

mkdir -p "$DIST" "$MODULE_DIR/bin"

echo "==> Go toolchain"
go version

echo "==> Stamping module.prop version=${VERSION}"
sed -i.bak -E "s|^version=.*|version=v${VERSION}|" "$MODULE_DIR/module.prop"
sed -i.bak -E "s|^versionCode=.*|versionCode=$(echo "$VERSION" | tr -d '.')00|" "$MODULE_DIR/module.prop"
rm -f "$MODULE_DIR/module.prop.bak"

echo "==> Running daemon tests"
(cd "$DAEMON_DIR" && go test ./... 2>&1) && echo "   tests passed" || echo "   WARN: tests failed"

echo "==> Building sshcustomd for android/arm64"
(
  cd "$DAEMON_DIR"
  GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="$LDFLAGS" \
    -o "$MODULE_DIR/bin/sshcustomd" \
    ./cmd/sshcustomd/
)
echo "   sshcustomd: $(ls -lh "$MODULE_DIR/bin/sshcustomd" | awk '{print $5}')"

echo "==> Checking tun2proxy binary"
if [ -f "$MODULE_DIR/bin/tun2proxy" ]; then
  echo "   tun2proxy: $(ls -lh "$MODULE_DIR/bin/tun2proxy" | awk '{print $5}')"
else
  echo "   WARN: module/bin/tun2proxy not found — CI builds it from source"
  echo "   For local builds, obtain a static arm64 tun2proxy binary and place it at module/bin/tun2proxy"
fi

echo "==> Packaging Magisk ZIP → $ZIP_OUT"
(
  cd "$MODULE_DIR"
  zip -r9 "$ZIP_OUT" . \
    -x "*.DS_Store" -x "__MACOSX/*" -x "bin/.gitkeep" -x "vpnchain/configs/.gitkeep"
)

echo ""
echo "==> BUILD COMPLETE"
echo "    $ZIP_OUT ($(ls -lh "$ZIP_OUT" | awk '{print $5}'))"
echo ""
