#!/system/bin/sh
# tun_setup.sh - Additional iptables rules that tun2proxy doesn't handle
# Called by sshcustomd after SSH connection is established

case "$1" in
  start)
    # Disable IPv6 (all traffic must be IPv4 through tunnel)
    echo 1 > /proc/sys/net/ipv6/conf/all/disable_ipv6
    echo 1 > /proc/sys/net/ipv6/conf/default/disable_ipv6
    # Allow fwmark 2 packets (tun2proxy bypass + daemon SSH sockets) through QUIC
    iptables -w 5 -t filter -I OUTPUT 1 -p udp --dport 443 -m mark --mark 0x2 -j RETURN
    # Block QUIC for everything else (forces TCP fallback through tunnel)
    iptables -w 5 -t filter -I OUTPUT 2 -p udp --dport 443 -m owner ! --uid-owner 0 -j REJECT --reject-with icmp-port-unreachable
    # Flush conntrack
    conntrack -F 2>/dev/null
    ;;
  stop)
    # Restore IPv6
    echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6
    echo 0 > /proc/sys/net/ipv6/conf/default/disable_ipv6
    # Remove fwmark bypass rule
    iptables -w 5 -t filter -D OUTPUT -p udp --dport 443 -m mark --mark 0x2 -j RETURN 2>/dev/null
    # Remove QUIC block
    iptables -w 5 -t filter -D OUTPUT -p udp --dport 443 -m owner ! --uid-owner 0 -j REJECT --reject-with icmp-port-unreachable 2>/dev/null
    # Flush conntrack
    conntrack -F 2>/dev/null
    ;;
esac
