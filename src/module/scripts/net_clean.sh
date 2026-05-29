#!/system/bin/sh
#
# Comprehensive iptables/network cleanup for SSHCustom-VPNChain v4.0.0.
#
# Removes all current and legacy chains/rules from any version (including
# prior TPROXY builds). Safe to run multiple times; errors are silenced
# because deleting non-existent rules is harmless.

WORK_DIR="/data/adb/sshcustom-vpnchain"
RUN_DIR="$WORK_DIR/run"
LOG="$RUN_DIR/net_clean.log"
mkdir -p "$RUN_DIR"

IPT="iptables"
IP6T="ip6tables"

# Current v2 NAT chains
NAT_CHAINS="SSHC_OUTPUT SSHC_PREROUTING"
# Legacy chains from prior versions
LEGACY_NAT_CHAINS="SSHC_PROXY SSHC_DNS SSHC_HOTSPOT SSHC_HOTSPOT_DNS"
# Legacy mangle chains (from TPROXY versions)
LEGACY_MANGLE_CHAINS="SSHC_TPROXY_OUT SSHC_TPROXY_PRE"
# Legacy IPv6 mangle chains
LEGACY_MANGLE6_CHAINS="SSHC_TPROXY_OUT6 SSHC_TPROXY_PRE6"
# Legacy filter chains
LEGACY_FILTER_CHAINS="SSHC_FILTER_QUIC SSHC_FILTER_OUTPUT6 SSHC_FILTER_FORWARD6"

IFACES="wlan+ swlan+ ap+ rndis+ ncm+ bt-pan+"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG"; }
run() { "$@" >/dev/null 2>&1; }

clean_nat_v4() {
  ALL_NAT="$NAT_CHAINS $LEGACY_NAT_CHAINS"
  for C in $ALL_NAT; do
    run $IPT -t nat -D OUTPUT -p tcp -j "$C"
    run $IPT -t nat -D OUTPUT -j "$C"
    run $IPT -t nat -D PREROUTING -p tcp -j "$C"
    run $IPT -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IPT -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IPT -t nat -D PREROUTING -i "$IF" -j "$C"
    done
    run $IPT -t nat -F "$C"
    run $IPT -t nat -X "$C"
  done
  # v3.1.0: Clean DNS REDIRECT rule (UDP/53 -> 10811)
  run $IPT -w 5 -t nat -D OUTPUT -p udp --dport 53 -m owner ! --uid-owner 0 -j REDIRECT --to-ports 10811
}

clean_nat_v6() {
  ALL_NAT="$NAT_CHAINS $LEGACY_NAT_CHAINS"
  for C in $ALL_NAT; do
    run $IP6T -t nat -D OUTPUT -p tcp -j "$C"
    run $IP6T -t nat -D OUTPUT -j "$C"
    run $IP6T -t nat -D PREROUTING -p tcp -j "$C"
    run $IP6T -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IP6T -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IP6T -t nat -D PREROUTING -i "$IF" -j "$C"
    done
    run $IP6T -t nat -F "$C"
    run $IP6T -t nat -X "$C"
  done
}

clean_mangle_v4() {
  for C in $LEGACY_MANGLE_CHAINS; do
    run $IPT -t mangle -D OUTPUT -j "$C"
    run $IPT -t mangle -D PREROUTING -j "$C"
    run $IPT -t mangle -F "$C"
    run $IPT -t mangle -X "$C"
  done
}

clean_mangle_v6() {
  for C in $LEGACY_MANGLE6_CHAINS; do
    run $IP6T -t mangle -D OUTPUT -j "$C"
    run $IP6T -t mangle -D PREROUTING -j "$C"
    run $IP6T -t mangle -F "$C"
    run $IP6T -t mangle -X "$C"
  done
}

clean_filter() {
  for C in $LEGACY_FILTER_CHAINS; do
    run $IPT -t filter -D OUTPUT -j "$C"
    run $IPT -t filter -D FORWARD -j "$C"
    run $IPT -t filter -F "$C"
    run $IPT -t filter -X "$C"
    run $IP6T -t filter -D OUTPUT -j "$C"
    run $IP6T -t filter -D FORWARD -j "$C"
    run $IP6T -t filter -F "$C"
    run $IP6T -t filter -X "$C"
  done
}

clean_quic_block() {
  # Remove standalone QUIC block rule (UDP/443 DROP for non-root)
  run $IPT -t filter -D OUTPUT -p udp --dport 443 -m owner ! --uid-owner 0 -j DROP
}

clean_forward() {
  run $IPT -D FORWARD -j ACCEPT
}

clean_policy_routes() {
  # Remove legacy TPROXY policy routes
  run ip rule del fwmark 0x1/0x1 table 100
  run ip route flush table 100
}

restore_ipv6() {
  echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null
  echo 0 > /proc/sys/net/ipv6/conf/default/disable_ipv6 2>/dev/null
}

flush_conntrack() {
  run conntrack -F
}

log "clean start"
# v4.0.0: Kill tun2proxy and remove TUN device
killall tun2proxy 2>/dev/null
run ip link delete tun_sshc
clean_nat_v4
clean_nat_v6
clean_mangle_v4
clean_mangle_v6
clean_filter
clean_quic_block
clean_forward
clean_policy_routes
restore_ipv6
flush_conntrack
log "clean complete"
exit 0
