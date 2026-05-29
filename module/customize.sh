#!/system/bin/sh
# customize.sh — Magisk/KSU/APatch installation script
SKIPUNZIP=1

ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
ui_print "  SSHCustom-VPNChain v2.0.0"
ui_print "  SSH Transparent Proxy Module"
ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── ABI check ────────────────────────────────────────────────────────────────
ARCH="$(getprop ro.product.cpu.abi)"
case "${ARCH}" in
  arm64-v8a) ui_print "  Arch: arm64 ✓" ;;
  *)
    ui_print "  ERROR: Unsupported architecture: ${ARCH}"
    ui_print "  This module requires arm64-v8a"
    abort "Unsupported architecture"
    ;;
esac

# ── Extract module files ──────────────────────────────────────────────────────
ui_print "  Extracting module files..."
unzip -o "${ZIPFILE}" -d "${MODPATH}" >/dev/null 2>&1

# ── Set permissions ───────────────────────────────────────────────────────────
ui_print "  Setting permissions..."
set_perm_recursive "${MODPATH}/scripts"  root root 0755 0755
set_perm_recursive "${MODPATH}/bin"      root root 0755 0755
set_perm "${MODPATH}/service.sh"         root root 0755
set_perm "${MODPATH}/action.sh"          root root 0755
set_perm "${MODPATH}/customize.sh"       root root 0755
set_perm "${MODPATH}/settings.ini"       root root 0644
set_perm "${MODPATH}/module.prop"        root root 0644

# ── Create data directories ───────────────────────────────────────────────────
WORK_DIR="/data/adb/sshcustom"
ui_print "  Creating data directories at ${WORK_DIR}..."
mkdir -p "${WORK_DIR}/run"
mkdir -p "${WORK_DIR}/run/state"
mkdir -p "${WORK_DIR}/bin"
mkdir -p "${WORK_DIR}/scripts"
mkdir -p "${WORK_DIR}/vpnchain/configs"
mkdir -p "${WORK_DIR}/vpnchain/run"
chmod 700 "${WORK_DIR}"

# ── Copy binaries and scripts ─────────────────────────────────────────────────
ui_print "  Installing binaries and scripts..."
cp -f "${MODPATH}/bin/sshcustomd" "${WORK_DIR}/bin/sshcustomd" 2>/dev/null && \
  chmod 755 "${WORK_DIR}/bin/sshcustomd" || true
cp -f "${MODPATH}/bin/tun2proxy" "${WORK_DIR}/bin/tun2proxy" 2>/dev/null && \
  chmod 755 "${WORK_DIR}/bin/tun2proxy" || true

cp -f "${MODPATH}/scripts/ssh.service"  "${WORK_DIR}/scripts/ssh.service"
cp -f "${MODPATH}/scripts/ssh.iptables" "${WORK_DIR}/scripts/ssh.iptables"
cp -f "${MODPATH}/scripts/ssh.tool"     "${WORK_DIR}/scripts/ssh.tool"
chmod 755 "${WORK_DIR}/scripts/ssh.service"
chmod 755 "${WORK_DIR}/scripts/ssh.iptables"
chmod 755 "${WORK_DIR}/scripts/ssh.tool"

# ── Copy default settings.ini (only if not present — preserve user config) ────
if [ ! -f "${WORK_DIR}/settings.ini" ]; then
  ui_print "  Installing default settings.ini..."
  cp -f "${MODPATH}/settings.ini" "${WORK_DIR}/settings.ini"
  chmod 644 "${WORK_DIR}/settings.ini"
else
  ui_print "  Preserving existing settings.ini"
fi

# ── VPN Chain: copy auth placeholder (only if not present) ───────────────────
if [ ! -f "${WORK_DIR}/vpnchain/auth.txt" ]; then
  cp -f "${MODPATH}/vpnchain/auth.txt" "${WORK_DIR}/vpnchain/auth.txt"
  chmod 600 "${WORK_DIR}/vpnchain/auth.txt"
fi

# ── Migration from old path ───────────────────────────────────────────────────
OLD_DIR="/data/adb/sshcustom-vpnchain"
if [ -d "${OLD_DIR}" ] && [ "${OLD_DIR}" != "${WORK_DIR}" ]; then
  ui_print "  Migrating config from ${OLD_DIR}..."
  [ -f "${OLD_DIR}/settings.ini" ] && ! [ -f "${WORK_DIR}/settings.ini" ] && \
    cp "${OLD_DIR}/settings.ini" "${WORK_DIR}/settings.ini"
  [ -d "${OLD_DIR}/vpnchain/configs" ] && \
    cp -rn "${OLD_DIR}/vpnchain/configs/." "${WORK_DIR}/vpnchain/configs/" 2>/dev/null || true
fi

ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
ui_print "  Installation complete!"
ui_print "  Open the companion app to configure"
ui_print "  or edit: ${WORK_DIR}/settings.ini"
ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
