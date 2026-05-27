#!/usr/bin/env bash
#
# SSHCustom-VPNChain build:
#   1. Read the canonical version from the VERSION file at the repo root.
#   2. Sync the canonical webroot/index.html into internal/webui/ for go:embed.
#   3. Stamp module.prop with the version.
#   4. Build a host validator and run it against the bundled config.
#   5. Cross-compile the daemon for arm64 (only), statically linked.
#   6. Cross-compile tun2socks for arm64.
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

echo "==> Acquiring tun2socks for ARM64"
# tun2socks restructured upstream: builds are now from repo root (main.go),
# not ./cmd/tun2socks. Newer releases also ship pre-built linux/arm64
# binaries which is faster and dodges the moving-target Go-version
# requirement of `go build` against their unreleased main branch.
TUN2SOCKS_VERSION="${TUN2SOCKS_VERSION:-v2.6.0}"
TUN2SOCKS_URL="https://github.com/xjasonlyu/tun2socks/releases/download/${TUN2SOCKS_VERSION}/tun2socks-linux-arm64.zip"
if [ ! -f "$TUN2SOCKS_BIN" ] || [ "${FORCE_REBUILD_TUN2SOCKS:-}" = "1" ]; then
  TUN2SOCKS_DL="$DIST/tun2socks.zip"
  if curl -fsSL "$TUN2SOCKS_URL" -o "$TUN2SOCKS_DL"; then
    # The release zip ships a single binary named tun2socks-linux-arm64.
    # We unzip into DIST then move into place. unzip is part of the
    # default ubuntu-latest image; if missing, the source-build fallback
    # below picks up the slack.
    if command -v unzip >/dev/null 2>&1 && unzip -p "$TUN2SOCKS_DL" tun2socks-linux-arm64 > "$TUN2SOCKS_BIN" 2>/dev/null && [ -s "$TUN2SOCKS_BIN" ]; then
      chmod +x "$TUN2SOCKS_BIN"
      echo "   downloaded prebuilt tun2socks $TUN2SOCKS_VERSION ($(ls -lh "$TUN2SOCKS_BIN" | awk '{print $5}'))"
    else
      rm -f "$TUN2SOCKS_BIN"
    fi
  fi
  # Fallback: build from source (now from repo root, not ./cmd/tun2socks).
  if [ ! -f "$TUN2SOCKS_BIN" ]; then
    echo "   prebuilt unavailable, building tun2socks from source"
    TUN2SOCKS_SRC="$DIST/tun2socks-src"
    if [ ! -d "$TUN2SOCKS_SRC" ]; then
      git clone --depth 1 --branch "$TUN2SOCKS_VERSION" https://github.com/xjasonlyu/tun2socks.git "$TUN2SOCKS_SRC" 2>/dev/null || \
        git clone --depth 1 https://github.com/xjasonlyu/tun2socks.git "$TUN2SOCKS_SRC"
    fi
    (
      cd "$TUN2SOCKS_SRC"
      # Build from repo root. The cmd/tun2socks subdir was removed in
      # a 2025 restructure; main.go lives at the top level now.
      GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
        -trimpath \
        -buildvcs=false \
        -ldflags="-s -w" \
        -o "$TUN2SOCKS_BIN" \
        .
    )
  fi
fi
if [ -f "$TUN2SOCKS_BIN" ]; then
  echo "   tun2socks ready: $(ls -lh "$TUN2SOCKS_BIN" | awk '{print $5}')"
else
  echo "   WARNING: tun2socks unavailable. VPN Chain feature will be inert."
fi

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
