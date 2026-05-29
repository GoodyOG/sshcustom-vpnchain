#!/usr/bin/env bash
#
# SSHCustom-VPNChain build:
#   1. Read the canonical version from the VERSION file at the repo root.
#   2. Sync the canonical webroot/index.html into internal/webui/ for go:embed.
#   3. Stamp module.prop with the version.
#   4. Build a host validator and run it against the bundled config.
#   5. Cross-compile the daemon for arm64 (only), statically linked.
#   6. Check for pre-compiled tun2proxy binary.
#   7. Package the module ZIP.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"

if [ -z "${VERSION:-}" ]; then
  if [ ! -f "$ROOT/VERSION" ]; then
    echo "VERSION file missing at repo root" >&2
    exit 1
  fi
  VERSION="$(cat "$ROOT/VERSION" | tr -d '[:space:]')"
fi
echo "==> Building SSHCustom-VPNChain v${VERSION}"

DIST="$ROOT/dist"
MODULE="$ROOT/src/module"
ARM64_BIN="$MODULE/bin/arm64/sshcustomd"
HOST_BIN="$DIST/sshcustomd-host"
ZIP_OUT="$DIST/SSHCustom-VPNChain-v${VERSION}.zip"
WEBROOT_SRC="$MODULE/webroot/index.html"
WEBROOT_EMBED="$ROOT/internal/webui/index.html"
FAVICON_SRC="$MODULE/webroot/favicon.svg"
FAVICON_EMBED="$ROOT/internal/webui/favicon.svg"
LDFLAGS="-s -w -buildid= -X github.com/GoodyOG/SSHCustom_Magisk/internal/version.Version=${VERSION}"

# VPN Chain binary paths (tun2socks and openvpn are for VPN Chain only)
VPNCHAIN_BIN_DIR="$MODULE/vpnchain/bin"

mkdir -p "$DIST" "$(dirname "$ARM64_BIN")" "$VPNCHAIN_BIN_DIR"
export GOFLAGS="${GOFLAGS:--mod=mod}"

echo "==> Go toolchain"
go version

echo "==> Syncing embedded webroot from $WEBROOT_SRC"
cp "$WEBROOT_SRC" "$WEBROOT_EMBED"
cp "$FAVICON_SRC" "$FAVICON_EMBED"

echo "==> Stamping src/module/module.prop with version=${VERSION}"
sed -i.bak -E "s|^version=.*|version=v${VERSION}|" "$MODULE/module.prop"
sed -i.bak -E "s|^versionCode=.*|versionCode=$(echo "$VERSION" | tr -d '.' | sed 's/^//')00|" "$MODULE/module.prop"
rm -f "$MODULE/module.prop.bak"

echo "==> Running unit tests"
go test ./... >/dev/null

echo "==> Building host validation binary"
CGO_ENABLED=0 go build \
  -trimpath \
  -buildvcs=false \
  -ldflags="$LDFLAGS" \
  -o "$HOST_BIN" \
  ./cmd/sshcustomd/

echo "==> Validating bundled config/profile JSON"
"$HOST_BIN" validate -c "$MODULE/config/config.json" -p "$MODULE/config/profiles.json"

echo "==> Building Android/Linux ARM64 daemon"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath \
  -buildvcs=false \
  -ldflags="$LDFLAGS" \
  -o "$ARM64_BIN" \
  ./cmd/sshcustomd/

echo "==> tun2proxy binary (pre-compiled, static arm64)"
if [ -f "$MODULE/bin/tun2proxy" ]; then
  echo "   tun2proxy found: $(ls -lh "$MODULE/bin/tun2proxy" | awk '{print $5}')"
else
  echo "ERROR: tun2proxy binary not found at $MODULE/bin/tun2proxy" >&2
  echo "Place the pre-compiled arm64 tun2proxy binary there before building." >&2
  exit 1
fi

echo "==> Packaging Magisk module"
python3 "$ROOT/scripts/package_module.py" "$MODULE" "$ZIP_OUT"

echo ""
echo "==> BUILD COMPLETE"
echo "    $ZIP_OUT"
echo ""
