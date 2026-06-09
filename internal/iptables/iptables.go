// Package iptables installs and removes the SSHCustom transparent-proxy
// chains using TPROXY (mangle table) and the leak-prevention side-effects that
// make a TCP/UDP SSH tunnel behave correctly on modern Android ROMs
// (HyperOS 3 / MIUI / OneUI / ColorOS).
//
// # Design notes
//
// Every exec.Command call for iptables uses "-w 100" as its first two
// arguments. On Android 11+ iptables serialises access via /run/xtables.lock;
// without -w, concurrent or rapid-fire calls fail silently and rules are never
// installed. vpnchain's ssh.iptables uses "iptables -w 100" on every call for
// the same reason — this is the single most important correctness requirement.
//
// # v2.7+ TPROXY architecture (mangle table)
//
// TCP and UDP are both intercepted in the mangle table via TPROXY. The
// kernel preserves the original destination address; the daemon reads it
// from the socket's local address (TCP) or IP_RECVORIGDSTADDR (UDP).
//
// Two mangle chains are installed for TCP:
//
//   - SSHC_TCP  hooked into mangle PREROUTING. Catches both device-originated
//     (after OUTPUT MARK re-routing) and hotspot-forwarded TCP traffic.
//     Terminal rule: TPROXY --on-port <port> --tproxy-mark 0x1/0x1.
//
//   - SSHC_TCP_OUTPUT  hooked into mangle OUTPUT. Marks locally-generated TCP
//     traffic with 0x1/0x1 so it re-enters via PREROUTING (TPROXY cannot
//     be used in OUTPUT). Terminal rule: MARK --set-mark 0x1/0x1.
//
// UDP has a separate SSHC_UDP chain in mangle PREROUTING (installed by
// ApplyUDP). Both share the same policy routing (fwmark 1 → table 100).
//
// # DNS-through-tunnel (the real no-internet fix)
//
// We redirect device UDP:53 to our local DNS forwarder (127.0.0.1:5353),
// which proxies each query as TCP DNS through the SSH tunnel to 8.8.8.8.
// This is what makes Android's captive-portal NetworkMonitor probe succeed.
//
// # Leak prevention (always applied with the TPROXY rules)
//
//   - QUIC (UDP/443 and UDP/80): DROPped so browsers fall back to TCP.
//   - IPv6: disabled system-wide (TPROXY is IPv4-only).
//
// # Bypass IPs
//
// The daemon passes in the resolved SSH endpoint IPs at apply time.
// Each becomes a -d <ip> RETURN rule so the SSH carrier connection itself
// is never caught by the TPROXY and looped back.
//
// # uid-0 RETURN rule
//
// SSHC_TCP_OUTPUT skips uid 0 so the daemon's own connections (SSH tunnel,
// DNS lookups) are not redirected through themselves.
//
// # Cleanup is idempotent
//
// Apply() always runs the internal rule cleanup first; Cleanup() ignores
// errors from non-existent chains/rules.
package iptables

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the subset of daemon config needed to install rules.
type Config struct {
	ChainsPrefix   string
	TCPPort        int
	UDPPort        int
	APIPort        int
	SocksPort      int
	DNSForwardPort int // local UDP DNS-through-tunnel forwarder port (0 = disabled)
	Hotspot        bool
	HotspotIfaces  []string
}

// DefaultPrefix is used when ChainsPrefix is empty.
const DefaultPrefix = "SSHC"

// DefaultHotspotIfaces covers Wi-Fi hotspot, MediaTek AP, USB tethering,
// CDC-NCM, and Bluetooth PAN — the tether interfaces on modern Android.
var DefaultHotspotIfaces = []string{"wlan+", "swlan+", "ap+", "rndis+", "ncm+", "bt-pan+"}

var privateCIDRs = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"224.0.0.0/4",
	"240.0.0.0/4",
}

// allLegacyChains lists every chain name ever created across all SSHCustom
// versions so Cleanup() removes orphans from older installs too. Includes
// legacy nat chains (pre-v2.7) and current mangle chains.
func allLegacyChains(prefix string) []string {
	return []string{
		prefix + "_OUTPUT",
		prefix + "_PREROUTING",
		prefix + "_PROXY",
		prefix + "_DNS",
		prefix + "_HOTSPOT",
		prefix + "_HOTSPOT_DNS",
		prefix + "_TCP",
		prefix + "_TCP_OUTPUT",
	}
}

// ipt runs a single iptables command with the -w 100 lock-wait flag.
// -w 100 tells iptables to wait up to 100 seconds to acquire the xtables lock.
// Without it, concurrent calls on Android 11+ silently fail and rules are
// never installed — the single most important correctness detail in this file.
func ipt(args ...string) *exec.Cmd {
	return exec.Command("iptables", append([]string{"-w", "100"}, args...)...)
}

func ip6t(args ...string) *exec.Cmd {
	return exec.Command("ip6tables", append([]string{"-w", "100"}, args...)...)
}

// Apply installs TPROXY chains in the mangle table for TCP, DNS-through-tunnel,
// QUIC block, IPv6 disable, TCP tuning, and captive-portal bypass. Bypass IPs
// are the resolved SSH endpoint IPs that must not be caught by TPROXY.
//
// v2.7+ uses mangle/TPROXY for TCP (was nat/REDIRECT). UDP TPROXY is handled
// separately by ApplyUDP() — both share the same policy routing (fwmark 1).
func Apply(cfg Config, bypassIPs []string) error {
	prefix := cfg.ChainsPrefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	port := cfg.TCPPort
	if port <= 0 {
		port = 10810
	}
	tcpChain := prefix + "_TCP"
	tcpOutChain := prefix + "_TCP_OUTPUT"

	var errs []string
	run := func(args ...string) {
		if b, err := ipt(args...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("iptables %s: %v %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(b))))
		}
	}

	// Pre-pass: tear down any existing rules from a prior run or a crash.
	// Removes both old nat chains (pre-v2.7) and current mangle chains.
	cleanupRules(cfg)

	// ----- Policy routing (shared with UDP TPROXY) -----
	setupPolicyRouting()

	for _, ch := range []string{tcpChain, tcpOutChain} {
		run("-t", "mangle", "-N", ch)
		run("-t", "mangle", "-F", ch)
	}

	// ----- SSHC_TCP (mangle PREROUTING) — catches all incoming TCP -----
	// Bypass: private/loopback/multicast CIDRs
	for _, cidr := range privateCIDRs {
		run("-t", "mangle", "-A", tcpChain, "-d", cidr, "-j", "RETURN")
	}
	// Bypass: SSH endpoint IPs (our carrier connection)
	for _, ip := range bypassIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		run("-t", "mangle", "-A", tcpChain, "-d", ip, "-j", "RETURN")
	}
	// Bypass: daemon's own listener ports
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort, cfg.DNSForwardPort} {
		if p > 0 {
			run("-t", "mangle", "-A", tcpChain, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	// Terminal: TPROXY to our TCP listener
	run("-t", "mangle", "-A", tcpChain, "-p", "tcp", "-j", "TPROXY",
		"--on-port", strconv.Itoa(port), "--tproxy-mark", "0x1/0x1")

	// Hook into mangle PREROUTING at position 1
	run("-t", "mangle", "-I", "PREROUTING", "1", "-p", "tcp", "-j", tcpChain)

	// Hotspot: per-interface hooks into PREROUTING for tethered clients
	if cfg.Hotspot {
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			run("-t", "mangle", "-I", "PREROUTING", "1", "-i", iface, "-p", "tcp", "-j", tcpChain)
		}
		if err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "sshcustom: warning: failed to enable ip_forward: %v\n", err)
		}
		if err := ipt("-I", "FORWARD", "-j", "ACCEPT").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "sshcustom: warning: failed to add FORWARD rule: %v\n", err)
		}
	}

	// ----- SSHC_TCP_OUTPUT (mangle OUTPUT) — marks device TCP for re-routing -----
	// TPROXY cannot be used in OUTPUT; we MARK outgoing TCP packets with 0x1/0x1
	// so they get re-routed via the policy routing table (table 100) through lo,
	// then re-enter PREROUTING where SSHC_TCP catches them.
	// Bypass: uid 0 (daemon's own traffic)
	run("-t", "mangle", "-A", tcpOutChain, "-m", "owner", "--uid-owner", "0", "-j", "RETURN")
	// Bypass: private/loopback CIDRs
	for _, cidr := range privateCIDRs {
		run("-t", "mangle", "-A", tcpOutChain, "-d", cidr, "-j", "RETURN")
	}
	// Bypass: SSH endpoint IPs
	for _, ip := range bypassIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		run("-t", "mangle", "-A", tcpOutChain, "-d", ip, "-j", "RETURN")
	}
	// Bypass: daemon listener ports
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort, cfg.DNSForwardPort} {
		if p > 0 {
			run("-t", "mangle", "-A", tcpOutChain, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	// Terminal: MARK for re-routing through PREROUTING
	run("-t", "mangle", "-A", tcpOutChain, "-p", "tcp", "-j", "MARK", "--set-mark", "0x1/0x1")

	// Hook into mangle OUTPUT at position 1
	run("-t", "mangle", "-I", "OUTPUT", "1", "-p", "tcp", "-j", tcpOutChain)

	// ----- Leak prevention + captive-portal (best-effort) -----
	setupDNSForward(prefix, cfg.DNSForwardPort)
	blockQUIC()
	disableIPv6()
	tuneTCP()
	disableCaptivePortal()

	var fatal []string
	for _, e := range errs {
		if strings.Contains(e, "No chain/target/match") ||
			strings.Contains(e, "does a matching rule exist") ||
			strings.Contains(e, "Chain already exists") {
			continue
		}
		fmt.Printf("[iptables] warn: %s\n", e)
		fatal = append(fatal, e)
	}
	if len(fatal) > 0 {
		return errors.New(strings.Join(fatal, "; "))
	}
	return nil
}

// Cleanup removes all SSHC chains (nat, mangle, filter), re-enables IPv6,
// and restores captive-portal.
func Cleanup(cfg Config) error {
	cleanupRules(cfg)
	// Clean UDP TPROXY mangle chain too
	_ = cleanupUDPChain(cfg)
	enableIPv6()
	restoreCaptivePortal()
	return nil
}

// cleanupRules tears down all SSHC iptables rules in both nat (legacy pre-v2.7)
// and mangle (v2.7+ TPROXY) tables. Does NOT touch IPv6 sysctls or captive-portal
// settings — Apply() calls this as a pre-pass and must not flap those.
func cleanupRules(cfg Config) {
	prefix := cfg.ChainsPrefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	chains := allLegacyChains(prefix)

	// Phase 0: Clean old nat table hooks (pre-v2.7 REDIRECT chains)
	for _, ch := range chains {
		_ = ipt("-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run()
		_ = ipt("-t", "nat", "-D", "OUTPUT", "-j", ch).Run()
		_ = ipt("-t", "nat", "-D", "OUTPUT", "-p", "udp", "--dport", "53", "-j", ch).Run()
		_ = ipt("-t", "nat", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run()
		_ = ipt("-t", "nat", "-D", "PREROUTING", "-j", ch).Run()
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			_ = ipt("-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run()
			_ = ipt("-t", "nat", "-D", "PREROUTING", "-i", iface, "-j", ch).Run()
		}
	}

	// Phase 1: Detach mangle table hooks (v2.7+ TPROXY chains).
	// Must remove both general and interface-specific hooks.
	// Without interface cleanup, hotspot hooks accumulate on restart
	// (25+ zombie hooks observed) and push the general hook down,
	// causing locally-generated traffic to never reach TPROXY.
	_ = ipt("-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-j", prefix+"_TCP").Run()
	ifaces := cfg.HotspotIfaces
	if len(ifaces) == 0 {
		ifaces = DefaultHotspotIfaces
	}
	for _, iface := range ifaces {
		if strings.TrimSpace(iface) == "" {
			continue
		}
		_ = ipt("-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", prefix+"_TCP").Run()
	}
	_ = ipt("-t", "mangle", "-D", "OUTPUT", "-p", "tcp", "-j", prefix+"_TCP_OUTPUT").Run()

	// Phase 2: Flush and delete ALL chains in ALL tables
	for _, ch := range chains {
		// nat table (legacy)
		_ = ipt("-t", "nat", "-F", ch).Run()
		_ = ipt("-t", "nat", "-X", ch).Run()
		// mangle table (v2.7+)
		_ = ipt("-t", "mangle", "-F", ch).Run()
		_ = ipt("-t", "mangle", "-X", ch).Run()
	}

	_ = ipt("-D", "FORWARD", "-j", "ACCEPT").Run()
	unblockQUIC()
}

// setupPolicyRouting creates the fwmark-based routing table used by both TCP
// and UDP TPROXY. Idempotent — errors from existing rules are suppressed.
func setupPolicyRouting() {
	exec.Command("/system/bin/sh", "-c",
		"ip rule add fwmark 0x1/0x1 table 100 pref 100 2>/dev/null; "+
			"ip route add local 0.0.0.0/0 dev lo table 100 2>/dev/null").Run()

	// route_localnet=1 is REQUIRED. Write directly to proc to avoid
	// shell/sysctl availability issues on stripped Android ROMs.
	os.WriteFile("/proc/sys/net/ipv4/conf/lo/route_localnet", []byte("1\n"), 0644)
	os.WriteFile("/proc/sys/net/ipv4/conf/all/route_localnet", []byte("1\n"), 0644)
}

// setupDNSForward redirects device UDP:53 to 127.0.0.1:<port> (our DNS
// forwarder) so every DNS query rides the SSH tunnel as TCP DNS to 8.8.8.8.
//
// Why this is essential: Android's NetworkMonitor fires a captive-portal probe
// even when captive_portal_mode=0 on HyperOS 3 / Android 16. The probe needs
// to resolve its target and reach a 204 endpoint. With the DNS tunnel active,
// the probe resolves through the tunnel (uid 0 bypasses the DNS redirect so
// the daemon's own dnsx still uses carrier DNS), reaches the 204 endpoint
// through the tunnel, and Android marks the network validated — no "no
// internet" tag without any data toggle. This is exactly vpnchain's design.
//
// uid 0 is excluded so the daemon's own DNS lookups go direct to carrier DNS.
//
// We always delete-then-insert (not check-then-insert) to avoid stacking
// duplicate DNAT rules if the daemon restarts or Apply() is called twice.
func setupDNSForward(prefix string, port int) {
	if port <= 0 {
		return
	}
	chain := prefix + "_DNS"
	// DNAT to loopback for locally-generated packets requires route_localnet=1.
	shRun("sysctl -w net.ipv4.conf.all.route_localnet=1 2>/dev/null || echo 1 > /proc/sys/net/ipv4/conf/all/route_localnet")
	
	// Check if chain already exists before creating (avoids error spam in logs)
	if ipt("-t", "nat", "-L", chain, "-n").Run() != nil {
		_ = ipt("-t", "nat", "-N", chain).Run()
	}
	_ = ipt("-t", "nat", "-F", chain).Run()
	
	// uid 0 (daemon + root shell) goes direct; everyone else is redirected.
	_ = ipt("-t", "nat", "-A", chain, "-m", "owner", "--uid-owner", "0", "-j", "RETURN").Run()
	_ = ipt("-t", "nat", "-A", chain, "-p", "udp", "--dport", "53",
		"-j", "DNAT", "--to-destination", fmt.Sprintf("127.0.0.1:%d", port)).Run()
	// Delete-then-insert: avoids stacking duplicate rules if Apply() is
	// called more than once (daemon restart, watchdog, etc.).
	_ = ipt("-t", "nat", "-D", "OUTPUT", "-p", "udp", "--dport", "53", "-j", chain).Run()
	_ = ipt("-t", "nat", "-I", "OUTPUT", "1", "-p", "udp", "--dport", "53", "-j", chain).Run()
}

// blockQUIC drops outbound UDP/443 and UDP/80. REDIRECT only catches TCP, so
// without this QUIC traffic escapes the tunnel entirely. Try REJECT first so 
// browsers fall back to TCP immediately. If REJECT is unsupported, use DROP.
func blockQUIC() {
	for _, port := range []string{"443", "80"} {
		// Use REJECT first because it falls back to TCP instantaneously without stalling apps like YouTube or Chrome.
		// ipt_REJECT is not available on some Android kernels, so we gracefully fallback to DROP.
		if ipt("-t", "filter", "-C", "OUTPUT", "-p", "udp", "--dport", port, "-j", "REJECT", "--reject-with", "icmp-port-unreachable").Run() != nil {
			if ipt("-t", "filter", "-A", "OUTPUT", "-p", "udp", "--dport", port, "-j", "REJECT", "--reject-with", "icmp-port-unreachable").Run() != nil {
				// Fallback to DROP
				if ipt("-t", "filter", "-C", "OUTPUT", "-p", "udp", "--dport", port, "-j", "DROP").Run() != nil {
					_ = ipt("-t", "filter", "-A", "OUTPUT", "-p", "udp", "--dport", port, "-j", "DROP").Run()
				}
			}
		}
	}
}

func unblockQUIC() {
	// Remove all variants (DROP and any REJECT from prior builds).
	for _, port := range []string{"443", "80"} {
		for i := 0; i < 4; i++ {
			if ipt("-t", "filter", "-D", "OUTPUT", "-p", "udp", "--dport", port, "-j", "DROP").Run() != nil {
				break
			}
		}
		for i := 0; i < 4; i++ {
			if ipt("-t", "filter", "-D", "OUTPUT", "-p", "udp", "--dport", port, "-j", "REJECT", "--reject-with", "icmp-port-unreachable").Run() != nil {
				break
			}
		}
	}
}

// shellPATH mirrors the PATH used by vpnchain's shell scripts so that
// settings, ndc, sysctl, and ip all resolve correctly when called from the
// daemon (which is launched with a minimal nohup environment).
const shellPATH = "/system/bin:/system/xbin:/vendor/bin:/data/adb/magisk:/data/adb/ksu/bin:/data/adb/ap/bin"

// shRun executes a shell command with a full Android PATH. Best-effort.
func shRun(cmdline string) {
	_ = exec.Command("/system/bin/sh", "-c", "export PATH="+shellPATH+":$PATH; "+cmdline).Run()
}

// disableIPv6 turns off IPv6 system-wide so no traffic leaks past the
// IPv4-only REDIRECT path. Also enables ip_forward (needed for hotspot).
// Since Android netd often overrides sysctl, we aggressively block IPv6
// through ip6tables to forcefully fail connections and trigger IPv4 fallback.
func disableIPv6() {
	shRun(`sysctl -w net.ipv4.ip_forward=1 2>/dev/null || echo 1 > /proc/sys/net/ipv4/ip_forward
sysctl -w net.ipv6.conf.all.disable_ipv6=1 2>/dev/null || echo 1 > /proc/sys/net/ipv6/conf/all/disable_ipv6
sysctl -w net.ipv6.conf.default.disable_ipv6=1 2>/dev/null || echo 1 > /proc/sys/net/ipv6/conf/default/disable_ipv6`)

	chain := "SSHC_DROP6"
	if ip6t("-t", "filter", "-L", chain, "-n").Run() != nil {
		_ = ip6t("-t", "filter", "-N", chain).Run()
	}
	_ = ip6t("-t", "filter", "-F", chain).Run()
	
	// Exempt loopback to prevent breaking local daemon communication
	_ = ip6t("-t", "filter", "-A", chain, "-o", "lo", "-j", "RETURN").Run()
	
	// REJECT forces immediate TCP fallback in browsers instead of stalling
	if ip6t("-t", "filter", "-A", chain, "-j", "REJECT", "--reject-with", "icmp6-adm-prohibited").Run() != nil {
		_ = ip6t("-t", "filter", "-A", chain, "-j", "DROP").Run()
	}

	_ = ip6t("-t", "filter", "-D", "OUTPUT", "-j", chain).Run()
	_ = ip6t("-t", "filter", "-I", "OUTPUT", "1", "-j", chain).Run()
	
	_ = ip6t("-t", "filter", "-D", "FORWARD", "-j", chain).Run()
	_ = ip6t("-t", "filter", "-I", "FORWARD", "1", "-j", chain).Run()
}

func enableIPv6() {
	shRun(`sysctl -w net.ipv6.conf.all.disable_ipv6=0 2>/dev/null || echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6
sysctl -w net.ipv6.conf.default.disable_ipv6=0 2>/dev/null || echo 0 > /proc/sys/net/ipv6/conf/default/disable_ipv6`)

	chain := "SSHC_DROP6"
	_ = ip6t("-t", "filter", "-D", "OUTPUT", "-j", chain).Run()
	_ = ip6t("-t", "filter", "-D", "FORWARD", "-j", chain).Run()
	_ = ip6t("-t", "filter", "-F", chain).Run()
	_ = ip6t("-t", "filter", "-X", chain).Run()
}

// disableCaptivePortal points Android's network validation probe at the
// daemon's own HTTP server at 127.0.0.1:9190. The daemon returns 204 for
// /generate_204 and /captive/generate_204. No DNS, no SSH tunnel — works on
// both bug-host and zero-bug-host networks because 127.0.0.0/8 and port 9190
// are already exempt from the iptables REDIRECT rules.
func disableCaptivePortal() {
	// Use the daemon's own HTTP server at 127.0.0.1:9190 as the captive portal
	// target. The daemon returns 204 for /generate_204 and /captive/generate_204.
	// No DNS, no SSH tunnel required — works on both bug-host and zero-bug-host
	// networks. Port 9190 and 127.0.0.0/8 are already exempt from iptables REDIRECT.
	shRun(`settings put global captive_portal_mode 0
settings put global captive_portal_use_https 0
settings put global captive_portal_server 127.0.0.1
settings put global captive_portal_http_url "http://127.0.0.1:9190/generate_204"
settings delete global captive_portal_https_url 2>/dev/null || true
ndc resolver clearnetdns 2>/dev/null || true`)
	kickRevalidation()
}

func restoreCaptivePortal() {
	resetRevalidation()
	shRun(`settings put global captive_portal_mode 1
settings put global captive_portal_use_https 1
settings delete global captive_portal_server 2>/dev/null || true
settings delete global captive_portal_http_url 2>/dev/null || true
settings delete global captive_portal_https_url 2>/dev/null || true`)
}

var (
	revalidateMu   sync.Mutex
	revalidateDone bool
)

// kickRevalidation fires "cmd connectivity reevaluate <netId>" once per
// tunnel session to clear any stale "no internet" verdict that Android
// cached before the DNS tunnel and captive-portal settings took effect.
// Non-disruptive: does NOT toggle mobile data or drop the SSH carrier.
// Runs at most once per session; re-arms on Cleanup().
//
// Updated strategy: Use Android's built-in reevaluate command which triggers
// a fresh NetworkMonitor probe WITHOUT toggling radio or disconnecting. This
// is much cleaner than the old data toggle approach and doesn't cause the
// brief connectivity loss that users find annoying.
func kickRevalidation() {
	revalidateMu.Lock()
	already := revalidateDone
	revalidateDone = true
	revalidateMu.Unlock()
	if already {
		return
	}
	go func() {
		// 5s delay: let iptables rules, settings, and DNS forwarder fully stabilize
		// so the probe has the best chance of succeeding on first attempt.
		time.Sleep(10 * time.Second)
		
		// Try multiple methods in order of preference:
		// 1. Specific network ID (most reliable)
		// 2. All networks (fallback for older Android)
		// 3. Broadcast intent (last resort)
		shRun(`NETID=$(dumpsys connectivity 2>/dev/null | grep -oE "network=[0-9]+" | tail -1 | cut -d= -f2)
if [ -n "$NETID" ]; then
  cmd connectivity reevaluate "$NETID" 2>/dev/null || \
  cmd connectivity reevaluate 2>/dev/null || \
  am broadcast -a android.net.conn.CONNECTIVITY_CHANGE 2>/dev/null
else
  cmd connectivity reevaluate 2>/dev/null || \
  am broadcast -a android.net.conn.CONNECTIVITY_CHANGE 2>/dev/null
fi`)
	}()
}

func resetRevalidation() {
	revalidateMu.Lock()
	revalidateDone = false
	revalidateMu.Unlock()
}

// tuneTCP raises kernel TCP socket-buffer ceilings so the single tunnel socket
// can reach full throughput on high-latency mobile links (measured 382 KB/s →
// 3.3 MB/s on a Poco F6 / 5G / 153ms RTT). Called from Apply() so the buffers
// are set before any proxied connection opens its first socket. Note: the SSH
// carrier socket itself was already created before Apply() runs, but new
// sockets (proxied app connections) benefit immediately. For the carrier socket
// itself, see the speed_boost call in sshcustom.sh which runs BEFORE the daemon
// starts. The larger ceilings are harmless (the kernel auto-tunes within them).
func tuneTCP() {
	shRun(`sysctl -w net.core.rmem_max=67108864 2>/dev/null || echo 67108864 > /proc/sys/net/core/rmem_max
sysctl -w net.core.wmem_max=67108864 2>/dev/null || echo 67108864 > /proc/sys/net/core/wmem_max
sysctl -w net.ipv4.tcp_rmem="4096 87380 67108864" 2>/dev/null || echo "4096 87380 67108864" > /proc/sys/net/ipv4/tcp_rmem
sysctl -w net.ipv4.tcp_wmem="4096 65536 67108864" 2>/dev/null || echo "4096 65536 67108864" > /proc/sys/net/ipv4/tcp_wmem`)
}

// ApplyUDP installs UDP TPROXY rules in the mangle table for capturing
// UDP traffic via TPROXY into the daemon's local listener. Shares the same
// policy routing (fwmark 1, table 100) with TCP TPROXY.
func ApplyUDP(cfg Config) error {
	port := cfg.UDPPort
	if port <= 0 {
		port = 10811
	}

	// Policy routing is shared with TCP — idempotent call
	setupPolicyRouting()

	ipt("-t", "mangle", "-N", "SSHC_UDP", "2>/dev/null").Run()
	ipt("-t", "mangle", "-F", "SSHC_UDP", "2>/dev/null").Run()
	ipt("-t", "mangle", "-A", "SSHC_UDP", "-p", "udp", "--dport", "53", "-j", "RETURN").Run()
	ipt("-t", "mangle", "-A", "SSHC_UDP", "-p", "udp", "--dport", strconv.Itoa(port), "-j", "RETURN").Run()
	ipt("-t", "mangle", "-A", "SSHC_UDP", "-p", "udp", "-j", "TPROXY", "--on-port", strconv.Itoa(port), "--tproxy-mark", "0x1/0x1").Run()
	ipt("-t", "mangle", "-I", "PREROUTING", "1", "-j", "SSHC_UDP").Run()
	return nil
}

// CleanupUDP removes the UDP TPROXY mangle chain. Does NOT remove shared
// policy routing (that is handled by Cleanup).
func CleanupUDP(cfg Config) error {
	return cleanupUDPChain(cfg)
}

func cleanupUDPChain(cfg Config) error {
	ipt("-t", "mangle", "-D", "PREROUTING", "-j", "SSHC_UDP", "2>/dev/null").Run()
	ipt("-t", "mangle", "-F", "SSHC_UDP", "2>/dev/null").Run()
	ipt("-t", "mangle", "-X", "SSHC_UDP", "2>/dev/null").Run()
	return nil
}
