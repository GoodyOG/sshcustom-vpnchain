#!/usr/bin/env bash
#
# SSHCustom-VPNChain build:
#   1. Read the canonical version from the VERSION file at the repo root.
#   2. Sync the canonical webroot/index.html into internal/webui/ for go:embed.
#   3. Stamp module.prop with the version.
#   4. Build a host validator and run it against the bundled config.
#   5. Cross-compile the daemon for arm64 (only), statically linked.
#   6. Download pre-built tun2socks for arm64.
#   7. Download or build static openvpn for arm64.
#   8. Package the module ZIP.
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

# VPN Chain binary paths
VPNCHAIN_BIN_DIR="$MODULE/vpnchain/bin"
TUN2SOCKS_BIN="$VPNCHAIN_BIN_DIR/tun2socks"
OPENVPN_BIN="$VPNCHAIN_BIN_DIR/openvpn"

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

echo "==> Downloading tun2socks for ARM64"
if [ ! -f "$TUN2SOCKS_BIN" ]; then
  TUN2SOCKS_VER="2.5.2"
  TUN2SOCKS_URL="https://github.com/xjasonlyu/tun2socks/releases/download/v${TUN2SOCKS_VER}/tun2socks-linux-arm64.zip"
  TUN2SOCKS_TMP="$DIST/tun2socks-download.zip"
  echo "   Downloading tun2socks v${TUN2SOCKS_VER} from GitHub releases..."
  curl -fsSL "$TUN2SOCKS_URL" -o "$TUN2SOCKS_TMP"
  unzip -o "$TUN2SOCKS_TMP" -d "$DIST/tun2socks-extract" >/dev/null
  # The zip contains a single binary named tun2socks-linux-arm64
  find "$DIST/tun2socks-extract" -name "tun2socks*" -exec cp {} "$TUN2SOCKS_BIN" \;
  chmod +x "$TUN2SOCKS_BIN"
  rm -rf "$TUN2SOCKS_TMP" "$DIST/tun2socks-extract" "$DIST/tun2socks-src"
fi
echo "   tun2socks: $(ls -lh "$TUN2SOCKS_BIN" | awk '{print $5}')"

echo "==> Acquiring static OpenVPN for ARM64"
# Download pre-built static openvpn for arm64 from a known source.
# If not available, we'll build from source.
if [ ! -f "$OPENVPN_BIN" ] || [ "${FORCE_REBUILD_OPENVPN:-}" = "1" ]; then
  OPENVPN_URL="${OPENVPN_STATIC_URL:-}"
  if [ -n "$OPENVPN_URL" ]; then
    echo "   Downloading from $OPENVPN_URL"
    curl -fsSL "$OPENVPN_URL" -o "$OPENVPN_BIN"
    chmod +x "$OPENVPN_BIN"
  else
    echo "   Building OpenVPN from source for ARM64 (static)..."
    OPENVPN_BUILD="$DIST/openvpn-build"
    mkdir -p "$OPENVPN_BUILD"
    (
      cd "$OPENVPN_BUILD"
      # Download openvpn source
      OPENVPN_VER="${OPENVPN_VERSION:-2.6.12}"
      if [ ! -f "openvpn-${OPENVPN_VER}.tar.gz" ]; then
        curl -fsSL "https://swupdate.openvpn.org/community/releases/openvpn-${OPENVPN_VER}.tar.gz" -o "openvpn-${OPENVPN_VER}.tar.gz"
      fi
      tar xzf "openvpn-${OPENVPN_VER}.tar.gz" 2>/dev/null || true
      cd "openvpn-${OPENVPN_VER}"
      
      # Cross-compile for arm64 with static linking
      export CC="${CROSS_COMPILE:-aarch64-linux-gnu-}gcc"
      export CFLAGS="-static -Os"
      export LDFLAGS="-static"
      
      ./configure \
        --host=aarch64-linux-gnu \
        --enable-static \
        --disable-shared \
        --disable-plugins \
        --disable-debug \
        --disable-plugin-auth-pam \
        --disable-plugin-down-root \
        --with-crypto-library=openssl \
        2>/dev/null || true
      
      make -j"$(nproc)" 2>/dev/null || true
      if [ -f src/openvpn/openvpn ]; then
        cp src/openvpn/openvpn "$OPENVPN_BIN"
        chmod +x "$OPENVPN_BIN"
      fi
    )
  fi
fi

if [ -f "$OPENVPN_BIN" ]; then
  echo "   openvpn binary: $(ls -lh "$OPENVPN_BIN" | awk '{print $5}')"
else
  echo "   WARNING: openvpn binary not available. Set OPENVPN_STATIC_URL env to provide one."
  echo "   The module ZIP will be built without openvpn. Add it manually before flashing."
fi

echo "==> Packaging Magisk module"
python3 "$ROOT/scripts/package_module.py" "$MODULE" "$ZIP_OUT"

echo ""
echo "==> BUILD COMPLETE"
echo "    $ZIP_OUT"
echo ""
