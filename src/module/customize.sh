#!/system/bin/sh
SKIPMOUNT=false
PROPFILE=false
POSTFSDATA=false
LATESTARTSERVICE=true

WORK_DIR="/data/adb/sshcustom-vpnchain"
BIN_DIR="$WORK_DIR/bin"
RUN_DIR="$WORK_DIR/run"
CONFIG_DIR="$WORK_DIR/config"

ui_print "****************************************"
ui_print " SSHCustom-VPNChain v2.0.0"
ui_print " SSH Tunnel + Windscribe VPN Chain"
ui_print "****************************************"

ABI="$(getprop ro.product.cpu.abi 2>/dev/null)"
case "$ABI" in
  arm64-v8a) BIN_SRC="$MODPATH/bin/arm64/sshcustomd" ;;
  armeabi-v7a|armeabi) BIN_SRC="$MODPATH/bin/arm/sshcustomd" ;;
  *)
    ui_print "Unsupported ABI: $ABI (requires arm64-v8a or armeabi-v7a)"
    abort "SSHCustom requires ARM64 or ARMv7 device"
    ;;
esac

# Migrate from old v1.0.1 directory if present
OLD_DIR="/data/adb/sshcustom"
if [ -d "$OLD_DIR" ] && [ ! -d "$WORK_DIR" ]; then
  ui_print "Migrating data from $OLD_DIR to $WORK_DIR..."
  mv "$OLD_DIR" "$WORK_DIR"
fi

mkdir -p "$WORK_DIR" "$BIN_DIR" "$RUN_DIR" "$CONFIG_DIR"

[ -f "$WORK_DIR/config.json" ] && cp -af "$WORK_DIR/config.json" "$WORK_DIR/config.json.bak.$(date +%s)"
[ -f "$WORK_DIR/profiles.json" ] && cp -af "$WORK_DIR/profiles.json" "$WORK_DIR/profiles.json.bak.$(date +%s)"

cp -af "$BIN_SRC" "$BIN_DIR/sshcustomd"
cp -af "$MODPATH/scripts/sshcustom.sh" "$WORK_DIR/sshcustom.sh"
cp -af "$MODPATH/scripts/sshcustom_watchdog.sh" "$WORK_DIR/sshcustom_watchdog.sh"
cp -af "$MODPATH/scripts/net_clean.sh" "$WORK_DIR/net_clean.sh"

# Always refresh module runtime config so new performance/network keys are applied.
cp -af "$MODPATH/config/config.json" "$WORK_DIR/config.json"

# Preserve user profiles on update. First install gets a safe editable sample.
if [ ! -f "$WORK_DIR/profiles.json" ]; then
  cp -af "$MODPATH/config/profiles.json" "$WORK_DIR/profiles.json"
fi

[ -d "$MODPATH/webroot" ] && cp -af "$MODPATH/webroot" "$WORK_DIR/webroot"

# --- VPN Chain setup ---
VPNCHAIN_DIR="$WORK_DIR/vpnchain"
VPNCHAIN_CONFIGS="$VPNCHAIN_DIR/configs"
VPNCHAIN_RUN="$VPNCHAIN_DIR/run"
mkdir -p "$VPNCHAIN_DIR" "$VPNCHAIN_CONFIGS" "$VPNCHAIN_RUN"

cp -af "$MODPATH/vpnchain/vpnchain.sh" "$VPNCHAIN_DIR/vpnchain.sh"

# Only install auth.txt if not already present (don't overwrite user edits)
if [ ! -f "$VPNCHAIN_DIR/auth.txt" ]; then
  cp -af "$MODPATH/vpnchain/auth.txt" "$VPNCHAIN_DIR/auth.txt"
fi

# Install tun2socks and openvpn binaries
if [ -f "$MODPATH/vpnchain/bin/tun2socks" ]; then
  cp -af "$MODPATH/vpnchain/bin/tun2socks" "$BIN_DIR/tun2socks"
fi
if [ -f "$MODPATH/vpnchain/bin/openvpn" ]; then
  cp -af "$MODPATH/vpnchain/bin/openvpn" "$BIN_DIR/openvpn"
fi

chmod 0755 "$BIN_DIR/sshcustomd" "$WORK_DIR/sshcustom.sh" "$WORK_DIR/sshcustom_watchdog.sh" "$WORK_DIR/net_clean.sh"
[ -f "$BIN_DIR/tun2socks" ] && chmod 0755 "$BIN_DIR/tun2socks"
[ -f "$BIN_DIR/openvpn" ] && chmod 0755 "$BIN_DIR/openvpn"
chmod 0755 "$VPNCHAIN_DIR/vpnchain.sh"
chmod 0600 "$VPNCHAIN_DIR/auth.txt"
chmod 0644 "$WORK_DIR/config.json"
chmod 0600 "$WORK_DIR/profiles.json"
chmod 0755 "$WORK_DIR" "$BIN_DIR" "$RUN_DIR" "$VPNCHAIN_DIR" "$VPNCHAIN_CONFIGS" "$VPNCHAIN_RUN"

"$BIN_DIR/sshcustomd" validate -c "$WORK_DIR/config.json" -p "$WORK_DIR/profiles.json" >/dev/null 2>&1 || ui_print "Warning: config/profile validation failed. Open the dashboard and edit your profile."

killall sshcustomd 2>/dev/null || true
"$WORK_DIR/net_clean.sh" >/dev/null 2>&1 || true
rm -f "$RUN_DIR/enabled" "$RUN_DIR/network_paused" "$RUN_DIR/daemon.pid" "$RUN_DIR/watchdog.pid"

ui_print "Installed to: $WORK_DIR"
ui_print "Binary ABI: $ABI"
ui_print "Dashboard: http://127.0.0.1:9190/"
ui_print "VPN Chain: configs in $VPNCHAIN_CONFIGS"
ui_print "Manual start only. Use KSU/Magisk Action after reboot."
