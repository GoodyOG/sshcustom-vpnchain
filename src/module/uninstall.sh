#!/system/bin/sh
WORK_DIR="/data/adb/sshcustom"
[ -x "$WORK_DIR/sshcustom.sh" ] && "$WORK_DIR/sshcustom.sh" stop >/dev/null 2>&1
[ -x "$WORK_DIR/net_clean.sh" ] && "$WORK_DIR/net_clean.sh" >/dev/null 2>&1
rm -rf "$WORK_DIR"
exit 0
