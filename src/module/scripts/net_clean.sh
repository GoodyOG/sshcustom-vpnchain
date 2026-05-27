#!/system/bin/sh
#
# Belt-and-suspenders iptables cleanup. The Go daemon does its own cleanup
# in iptables.Cleanup() during shutdown, but if the daemon crashed or was
# killed mid-rule, leftover SSHC_* chains could remain and silently break
# the device's networking. This script is callable from sshcustom.sh stop
# and from customize.sh during a fresh install to guarantee a clean slate.
#
# Removes every SSHC_* chain name we have ever shipped (current and legacy)
# from the IPv4 nat + filter tables and the IPv6 nat + filter tables, plus
# the FORWARD ACCEPT rule that hotspot mode adds. Errors are silenced
# because every -D against a missing rule is harmless noise.

RUN_DIR="/data/adb/sshcustom/run"
LOG="$RUN_DIR/net_clean.log"
mkdir -p "$RUN_DIR"

IPT="iptables"
IP6T="ip6tables"
# Every chain SSHCustom-VPNChain has ever installed in the nat table.
# Keeping legacy names here means a user upgrading from a pre-v2 build
# still gets their orphaned chains removed.
NAT_CHAINS="SSHC_OUTPUT SSHC_PREROUTING SSHC_PROXY SSHC_DNS SSHC_HOTSPOT SSHC_HOTSPOT_DNS"
# Filter-table chains were introduced in v1.1.0 (leak protection).
FILTER_CHAINS="SSHC_FILTER_QUIC"
# IPv6 filter-table chains (also v1.1.0).
V6_FILTER_CHAINS="SSHC_FILTER_OUTPUT6 SSHC_FILTER_FORWARD6"
IFACES="wlan+ swlan+ ap+ rndis+ ncm+ bt-pan+"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG"; }
run() { "$@" >/dev/null 2>&1; }

clean_v4_nat() {
  for C in $NAT_CHAINS; do
    run $IPT -t nat -D OUTPUT -p tcp -j "$C"
    run $IPT -t nat -D OUTPUT -j "$C"
    run $IPT -t nat -D PREROUTING -p tcp -j "$C"
    run $IPT -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IPT -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IPT -t nat -D PREROUTING -i "$IF" -j "$C"
    done
  done
  for C in $NAT_CHAINS; do
    run $IPT -t nat -F "$C"
    run $IPT -t nat -X "$C"
  done
  run $IPT -D FORWARD -j ACCEPT
}

clean_v4_filter() {
  for C in $FILTER_CHAINS; do
    run $IPT -t filter -D OUTPUT -p udp -j "$C"
    run $IPT -t filter -D OUTPUT -j "$C"
    run $IPT -t filter -F "$C"
    run $IPT -t filter -X "$C"
  done
}

clean_v6_nat() {
  for C in $NAT_CHAINS; do
    run $IP6T -t nat -D OUTPUT -p tcp -j "$C"
    run $IP6T -t nat -D OUTPUT -j "$C"
    run $IP6T -t nat -D PREROUTING -p tcp -j "$C"
    run $IP6T -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IP6T -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IP6T -t nat -D PREROUTING -i "$IF" -j "$C"
    done
  done
  for C in $NAT_CHAINS; do
    run $IP6T -t nat -F "$C"
    run $IP6T -t nat -X "$C"
  done
}

clean_v6_filter() {
  for C in $V6_FILTER_CHAINS; do
    run $IP6T -t filter -D OUTPUT -j "$C"
    run $IP6T -t filter -D FORWARD -j "$C"
    run $IP6T -t filter -F "$C"
    run $IP6T -t filter -X "$C"
  done
}

# Restore the kernel sysctls the daemon may have tweaked. Defaults are
# Android's standard values; if the user had non-default values these
# get overwritten, but that's a non-issue on stock builds.
restore_sysctls() {
  echo 0 > /proc/sys/net/ipv4/conf/all/route_localnet 2>/dev/null
  echo 0 > /proc/sys/net/ipv4/conf/default/route_localnet 2>/dev/null
  echo 1 > /proc/sys/net/ipv4/conf/all/rp_filter 2>/dev/null
  echo 1 > /proc/sys/net/ipv4/conf/default/rp_filter 2>/dev/null
}

log "clean start"
clean_v4_nat
clean_v4_filter
clean_v6_nat
clean_v6_filter
restore_sysctls
log "clean complete"
exit 0
