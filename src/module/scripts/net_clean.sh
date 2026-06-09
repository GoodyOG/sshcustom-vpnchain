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

IPT="iptables -w 100"
IP6T="ip6tables -w 100"
# Every chain SSHCustom-Magisk has ever installed in any version. Keeping
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
    run $IPT -t nat -D OUTPUT -p udp --dport 53 -j "$C"
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
  for C in $CHAINS SSHC_DROP6; do
    run $IP6T -t filter -D OUTPUT -j "$C"
    run $IP6T -t filter -D FORWARD -j "$C"
    run $IP6T -t nat -D OUTPUT -p tcp -j "$C"
    run $IP6T -t nat -D OUTPUT -j "$C"
    run $IP6T -t nat -D PREROUTING -p tcp -j "$C"
    run $IP6T -t nat -D PREROUTING -j "$C"
    for IF in $IFACES; do
      run $IP6T -t nat -D PREROUTING -i "$IF" -p tcp -j "$C"
      run $IP6T -t nat -D PREROUTING -i "$IF" -j "$C"
    done
  done
  for C in $CHAINS SSHC_DROP6; do
    run $IP6T -t nat -F "$C"
    run $IP6T -t nat -X "$C"
    run $IP6T -t filter -F "$C"
    run $IP6T -t filter -X "$C"
  done
}

clean_quic() {
  # Remove DROP and REJECT variants across all prior builds (loop for stacked dupes).
  for P in 443 80; do
    i=0
    while [ "$i" -lt 4 ]; do
      run $IPT -t filter -D OUTPUT -p udp --dport "$P" -j DROP || break
      i=$((i + 1))
    done
    i=0
    while [ "$i" -lt 4 ]; do
      run $IPT -t filter -D OUTPUT -p udp --dport "$P" -j REJECT --reject-with icmp-port-unreachable || break
      i=$((i + 1))
    done
  done
}

restore_ipv6() {
  run sysctl -w net.ipv6.conf.all.disable_ipv6=0
  run sysctl -w net.ipv6.conf.default.disable_ipv6=0
}

restore_captive_portal() {
  # Re-enable Android's captive-portal detection that the daemon disables
  # while the tunnel is active.
  run settings put global captive_portal_mode 1
  run settings put global captive_portal_detection_enabled 1
  run settings put global captive_portal_use_https 1
  run settings delete global captive_portal_server
  run settings delete global captive_portal_http_url
  run settings delete global captive_portal_https_url
}

clean_udp_tproxy() {
  # Clean up mangle table TPROXY rules for UDP proxy
  run $IPT -t mangle -D PREROUTING -j SSHC_UDP
  run $IPT -t mangle -F SSHC_UDP
  run $IPT -t mangle -X SSHC_UDP
}

clean_tcp_tproxy() {
  # Clean up mangle table TPROXY rules for TCP proxy.
  # Must remove both general and interface-specific hooks to prevent
  # zombie hook accumulation on repeated restarts.
  run $IPT -t mangle -D PREROUTING -p tcp -j SSHC_TCP
  for IF in $IFACES; do
    run $IPT -t mangle -D PREROUTING -i "$IF" -p tcp -j SSHC_TCP
  done
  run $IPT -t mangle -D OUTPUT -p tcp -j SSHC_TCP_OUTPUT
  run $IPT -t mangle -F SSHC_TCP
  run $IPT -t mangle -X SSHC_TCP
  run $IPT -t mangle -F SSHC_TCP_OUTPUT
  run $IPT -t mangle -X SSHC_TCP_OUTPUT
}

clean_tproxy_routing() {
  # Clean policy routing rules (shared between TCP and UDP TPROXY)
  run ip rule del fwmark 0x1/0x1 table 100
  run ip route del local 0.0.0.0/0 dev lo table 100
}

log "clean start"
# Primary: use the iptables script for thorough cleanup
[ -x "$(dirname "$0")/ssh.iptables" ] && sh "$(dirname "$0")/ssh.iptables" disable 2>/dev/null
# Belt-and-suspenders: direct cleanup of any remaining rules
clean_v4
clean_v6
clean_quic
clean_udp_tproxy
clean_tcp_tproxy
clean_tproxy_routing
restore_ipv6
restore_captive_portal
log "clean complete"
exit 0
