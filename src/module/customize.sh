#!/system/bin/sh
SKIPMOUNT=false
PROPFILE=false
POSTFSDATA=false
LATESTARTSERVICE=true

WORK_DIR="/data/adb/sshcustom"
BIN_DIR="$WORK_DIR/bin"
RUN_DIR="$WORK_DIR/run"
SCRIPTS_DIR="$WORK_DIR/scripts"

ui_print "****************************************"
ui_print " SSHCustom-VPNChain v5.0.0"
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

mkdir -p "$WORK_DIR" "$BIN_DIR" "$RUN_DIR" "$SCRIPTS_DIR"

# ── Daemon binary ─────────────────────────────────────────────────────────────
cp -af "$BIN_SRC" "$BIN_DIR/sshcustomd"
chmod 0755 "$BIN_DIR/sshcustomd"

# ── Runtime scripts (v2.3.13 engine) ─────────────────────────────────────────
cp -af "$MODPATH/scripts/ssh.service"      "$SCRIPTS_DIR/ssh.service"
cp -af "$MODPATH/scripts/ssh.iptables"     "$SCRIPTS_DIR/ssh.iptables"
cp -af "$MODPATH/scripts/ssh.tool"         "$SCRIPTS_DIR/ssh.tool"
cp -af "$MODPATH/scripts/ovpn.service"     "$SCRIPTS_DIR/ovpn.service"
cp -af "$MODPATH/scripts/vpnchain.iptables" "$SCRIPTS_DIR/vpnchain.iptables"
cp -af "$MODPATH/scripts/net_clean.sh"     "$SCRIPTS_DIR/net_clean.sh"
chmod 0755 "$SCRIPTS_DIR/"*.service "$SCRIPTS_DIR/"*.iptables \
           "$SCRIPTS_DIR/ssh.tool" "$SCRIPTS_DIR/net_clean.sh"

# ── settings.ini — install only if not present (preserve user config) ─────────
if [ ! -f "$WORK_DIR/settings.ini" ]; then
  cp -af "$MODPATH/settings.ini" "$WORK_DIR/settings.ini"
  chmod 0644 "$WORK_DIR/settings.ini"
else
  ui_print "Preserving existing settings.ini"
fi

# ── WebUI (always refresh to get latest UI) ───────────────────────────────────
[ -d "$MODPATH/webroot" ] && cp -af "$MODPATH/webroot" "$WORK_DIR/webroot"

# ── profiles.json — preserve on update ───────────────────────────────────────
if [ ! -f "$WORK_DIR/profiles.json" ]; then
  echo '{"selected_id":"","profiles":[]}' > "$WORK_DIR/profiles.json"
  chmod 0600 "$WORK_DIR/profiles.json"
fi

# ── VPN Chain setup ────────────────────────────────────────────────────────────
VPNCHAIN_DIR="$WORK_DIR/vpnchain"
VPNCHAIN_CONFIGS="$VPNCHAIN_DIR/configs"
VPNCHAIN_RUN="$VPNCHAIN_DIR/run"
mkdir -p "$VPNCHAIN_DIR" "$VPNCHAIN_CONFIGS" "$VPNCHAIN_RUN"

# auth.txt — preserve user credentials on update
if [ ! -f "$VPNCHAIN_DIR/auth.txt" ]; then
  printf 'your_windscribe_username\nyour_windscribe_password\n' > "$VPNCHAIN_DIR/auth.txt"
  chmod 0600 "$VPNCHAIN_DIR/auth.txt"
fi

# tun2socks + openvpn binaries
if [ -f "$MODPATH/vpnchain/bin/tun2socks" ]; then
  cp -af "$MODPATH/vpnchain/bin/tun2socks" "$BIN_DIR/tun2socks"
  chmod 0755 "$BIN_DIR/tun2socks"
fi
if [ -f "$MODPATH/vpnchain/bin/openvpn" ]; then
  cp -af "$MODPATH/vpnchain/bin/openvpn" "$BIN_DIR/openvpn"
  chmod 0755 "$BIN_DIR/openvpn"
fi

# ── Clean up stale state from previous versions ────────────────────────────────
killall sshcustomd 2>/dev/null || true
sh "$SCRIPTS_DIR/net_clean.sh" >/dev/null 2>&1 || true
# Remove v1.0.1 control files (no longer used — v5 uses ssh.service directly)
rm -f "$RUN_DIR/enabled" "$RUN_DIR/network_paused" "$RUN_DIR/daemon.pid" "$RUN_DIR/watchdog.pid"

chmod 0755 "$WORK_DIR" "$BIN_DIR" "$RUN_DIR" "$SCRIPTS_DIR" \
           "$VPNCHAIN_DIR" "$VPNCHAIN_CONFIGS" "$VPNCHAIN_RUN"

ui_print "Installed to: $WORK_DIR"
ui_print "Binary ABI:   $ABI"
ui_print "Dashboard:    http://127.0.0.1:9190/"
ui_print "VPN configs:  $VPNCHAIN_CONFIGS"
ui_print "Reboot then open the module WebUI to configure."
