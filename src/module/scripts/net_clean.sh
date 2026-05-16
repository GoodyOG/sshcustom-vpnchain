#!/system/bin/sh
#
# Belt-and-suspenders iptables cleanup. The Go daemon does its own cleanup
# in iptables.Cleanup() during shutdown, but if the daemon crashed or was
# killed mid-rule, leftover SSHC_* chains could remain and silently break
# the device's networking. This script is callable from sshcustom.sh stop
# and from customize.sh during a fresh install to guarantee a clean slate.
#
# Removes every SSHC_* chain name we have ever shipped (current and legacy)
# from both the IPv4 and IPv6 nat tables, plus the FORWARD ACCEPT rule that
# hotspot mode adds. Errors are silenced because every -D against a missing
# rule is harmless noise.

RUN_DIR="/data/adb/sshcustom/run"
LOG="$RUN_DIR/net_clean.log"
mkdir -p "$RUN_DIR"

IPT="iptables"
IP6T="ip6tables"
# Every chain SSHCustom-VPNChain has ever installed in any version. Keeping
# legacy names here means a user upgrading from a pre-v2 build still gets
# their orphaned chains removed even if those chains are no longer created.
CHAINS="SSHC_OUTPUT SSHC_PREROUTING SSHC_PROXY SSHC_DNS SSHC_HOTSPOT SSHC_HOTSPOT_DNS"
IFACES="wlan+ swlan+ ap+ rndis+ ncm+ bt-pan+"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG"; }
run() { "$@" >/dev/null 2>&1; }

clean_v4() {
  for C in $CHAINS; do
    run $IPT -t nat -D OUTPUT -p tcp -j "$C"
    run $IPT -t nat -D OUTPUT -j "$C"
    run $IPT -t nat -D PREROUTING -p tcp -j "$C"
    run $IPT -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IPT -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IPT -t nat -D PREROUTING -i "$IF" -j "$C"
    done
  done
  for C in $CHAINS; do
    run $IPT -t nat -F "$C"
    run $IPT -t nat -X "$C"
  done
  run $IPT -D FORWARD -j ACCEPT
}

clean_v6() {
  for C in $CHAINS; do
    run $IP6T -t nat -D OUTPUT -p tcp -j "$C"
    run $IP6T -t nat -D OUTPUT -j "$C"
    run $IP6T -t nat -D PREROUTING -p tcp -j "$C"
    run $IP6T -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IP6T -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IP6T -t nat -D PREROUTING -i "$IF" -j "$C"
    done
  done
  for C in $CHAINS; do
    run $IP6T -t nat -F "$C"
    run $IP6T -t nat -X "$C"
  done
}

log "clean start"
clean_v4
clean_v6
log "clean complete"
exit 0
