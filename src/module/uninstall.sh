#!/system/bin/sh
WORK_DIR="/data/adb/sshcustom-vpnchain"
[ -x "$WORK_DIR/sshcustom.sh" ] && "$WORK_DIR/sshcustom.sh" stop >/dev/null 2>&1
[ -x "$WORK_DIR/net_clean.sh" ] && "$WORK_DIR/net_clean.sh" >/dev/null 2>&1
# Restore IPv6 (may have been disabled by leak protection)
echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null
echo 0 > /proc/sys/net/ipv6/conf/default/disable_ipv6 2>/dev/null
rm -rf "$WORK_DIR"
exit 0
