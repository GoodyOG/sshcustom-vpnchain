#!/system/bin/sh
# customize.sh — Magisk/KSU/APatch installation script
SKIPUNZIP=1

ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
ui_print "  SSHCustom-VPNChain v2.0.0"
ui_print "  SSH Transparent Proxy Module"
ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── ABI check ─────────────────────────────────────────────────────────────────
ARCH="$(getprop ro.product.cpu.abi)"
case "${ARCH}" in
  arm64-v8a) ui_print "  Arch: arm64 ✓" ;;
  *)
    ui_print "  ERROR: Unsupported architecture: ${ARCH}"
    ui_print "  This module requires arm64-v8a"
    abort "Unsupported architecture: ${ARCH}"
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
ui_print "  Creating data directories..."
mkdir -p "${WORK_DIR}/run/state"
mkdir -p "${WORK_DIR}/bin"
mkdir -p "${WORK_DIR}/scripts"
mkdir -p "${WORK_DIR}/vpnchain/configs"
mkdir -p "${WORK_DIR}/vpnchain/run"
chmod 700 "${WORK_DIR}"

# ── Install binaries ──────────────────────────────────────────────────────────
ui_print "  Installing binaries..."

_install_bin() {
  local src="${MODPATH}/bin/$1" dst="${WORK_DIR}/bin/$1"
  if [ -f "${src}" ]; then
    cp -f "${src}" "${dst}" || { ui_print "  ERROR: failed to copy $1"; return 1; }
    chmod 755 "${dst}"
    ui_print "  Installed: $1 ($(ls -lh "${dst}" | awk '{print $5}'))"
  else
    ui_print "  WARN: $1 not found in ZIP — skipping"
  fi
}

_install_bin sshcustomd
_install_bin tun2proxy
_install_bin openvpn

# ── Install scripts ───────────────────────────────────────────────────────────
ui_print "  Installing scripts..."

_install_script() {
  local src="${MODPATH}/scripts/$1" dst="${WORK_DIR}/scripts/$1"
  if [ -f "${src}" ]; then
    cp -f "${src}" "${dst}" || { ui_print "  ERROR: failed to copy script $1"; return 1; }
    chmod 755 "${dst}"
  else
    ui_print "  ERROR: script $1 missing from ZIP"
    return 1
  fi
}

_install_script ssh.service  || abort "Failed to install ssh.service"
_install_script ssh.iptables || abort "Failed to install ssh.iptables"
_install_script ssh.tool     || abort "Failed to install ssh.tool"
_install_script ovpn.service || ui_print "  WARN: ovpn.service not installed (VPN Chain)"
_install_script vpnchain.iptables || ui_print "  WARN: vpnchain.iptables not installed"

# ── Install settings.ini (preserve existing user config) ─────────────────────
if [ ! -f "${WORK_DIR}/settings.ini" ]; then
  ui_print "  Installing default settings.ini..."
  cp -f "${MODPATH}/settings.ini" "${WORK_DIR}/settings.ini" || \
    abort "Failed to install settings.ini"
  chmod 644 "${WORK_DIR}/settings.ini"
else
  ui_print "  Preserving existing settings.ini"
  # Update settings.ini with any new keys while preserving user values
  # (Future: diff-merge new keys; for now just inform the user)
  ui_print "  NOTE: new options may be available — see module docs"
fi

# ── VPN Chain: auth template (create if missing or still template) ────────────
if [ ! -f "${WORK_DIR}/vpnchain/auth.txt" ] || grep -q "^username$" "${WORK_DIR}/vpnchain/auth.txt" 2>/dev/null; then
  printf 'username\npassword\n' > "${WORK_DIR}/vpnchain/auth.txt"
  chmod 600 "${WORK_DIR}/vpnchain/auth.txt"
  ui_print "  VPN Chain: edit vpnchain/auth.txt with your OpenVPN credentials"
fi

# ── Migration from old path ───────────────────────────────────────────────────
OLD_DIR="/data/adb/sshcustom-vpnchain"
if [ -d "${OLD_DIR}" ] && [ "${OLD_DIR}" != "${WORK_DIR}" ]; then
  ui_print "  Migrating config from old path..."
  [ -f "${OLD_DIR}/settings.ini" ] && [ ! -f "${WORK_DIR}/settings.ini" ] && \
    cp "${OLD_DIR}/settings.ini" "${WORK_DIR}/settings.ini"
  [ -d "${OLD_DIR}/vpnchain/configs" ] && \
    cp -rn "${OLD_DIR}/vpnchain/configs/." "${WORK_DIR}/vpnchain/configs/" 2>/dev/null || true
fi

# ── Verify critical files installed ──────────────────────────────────────────
_verify() {
  local f="$1"
  if [ ! -f "${f}" ]; then
    ui_print "  ERROR: verification failed — missing: ${f}"
    abort "Installation incomplete: ${f}"
  fi
}

_verify "${WORK_DIR}/bin/sshcustomd"
_verify "${WORK_DIR}/scripts/ssh.service"
_verify "${WORK_DIR}/scripts/ssh.iptables"
_verify "${WORK_DIR}/scripts/ssh.tool"
_verify "${WORK_DIR}/settings.ini"

ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
ui_print "  Installation complete!"
ui_print "  Configure: ${WORK_DIR}/settings.ini"
ui_print "  Or open the companion app"
ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
