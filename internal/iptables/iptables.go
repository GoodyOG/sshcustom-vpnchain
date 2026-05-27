// Package iptables installs and removes the SSHCustom transparent-proxy
// chains using mangle-table TPROXY (IPv4 only).
//
// # What this does
//
// We install chains in the mangle table:
//
//   - SSHC_TPROXY_OUT  hooked into mangle OUTPUT, marks packets with
//     fwmark 0x1/0x1 for policy routing.
//   - SSHC_TPROXY_PRE  hooked into mangle PREROUTING, applies the TPROXY
//     target to deliver packets to the daemon's IP_TRANSPARENT listener.
//
// Locally-originated packets flow: OUTPUT (mark) → policy routing →
// loopback → PREROUTING (TPROXY) → daemon socket. Hotspot/forwarded
// traffic enters directly via interface-specific PREROUTING hooks.
//
// # IPv6 handling (v1.3.1+)
//
// IPv6 TCP is NOT tunneled through SSH because the SSH relay server has no
// IPv6 connectivity. Instead, ip6tables filter REJECT rules block all v6
// OUTPUT (except UID 0, loopback, link-local, ICMPv6). This forces apps to
// fall back to IPv4 in <50ms. Legacy v6 mangle chains from v1.2.x/v1.3.0
// are cleaned up automatically on startup.
//
// # The uid-0 RETURN rule
//
// SSHC_TPROXY_OUT has an early "owner uid 0 RETURN" rule. Without it, the
// daemon's own outbound connections (the SSH tunnel itself, DNS lookups,
// etc.) would be marked and redirected through itself, forming a loop.
//
// # Bypass IPs
//
// The daemon passes in a list of resolved SSH endpoint IPs at apply time.
// Each becomes a `-d <ip> RETURN` rule before the catch-all MARK. This
// is critical: without it, the SSH carrier connection itself would hit the
// TPROXY and form a loop.
//
// # Leak protection (v1.1.0)
//
// Three categories of traffic escape TPROXY:
//
//   - IPv6 (when TPROXY v6 rules are active, v6 TCP is tunneled; but v6
//     UDP and non-TCP still leak without additional protection).
//   - UDP in any form, including QUIC (UDP/443). Chrome/YouTube prefer
//     QUIC; without intervention they stall before TCP fallback.
//   - Connections that already exist in conntrack from before our rules.
//
// LeakProtection plugs these:
//
//   - applyIPv6Lockdown: ip6tables filter rules that REJECT non-TCP v6
//     except from UID 0. TPROXY-bound v6 TCP is allowed through.
//   - applyQUICBlock: iptables filter rules that REJECT UDP/443+80.
//   - flushConntrack: drop existing flows after rule install/remove.
//
// # Sysctl hardening
//
// route_localnet=1 is REQUIRED for TPROXY OUTPUT and is therefore set
// unconditionally on every Apply() via applyTPROXYRequiredSysctls(). Without
// it, the policy route `local 0.0.0.0/0 dev lo table 100` silently drops
// marked packets and the listener never receives traffic.
//
// applyDefensiveSysctls sets the optional rp_filter=2 (loose) knob; this is
// gated by the user-facing "Sysctl Hardening" toggle.
//
// restoreSysctls (run during Cleanup) only restores rp_filter. We deliberately
// leave route_localnet=1 in place: resetting it to 0 in v1.3.3 caused TPROXY
// to break on the second apply when the toggle was off, because Apply only
// re-set the value when the toggle was on, leaving an asymmetric apply/cleanup
// cycle that zeroed route_localnet permanently after one rule cycle.
//
// # Lock contention (v1.3.0)
//
// All iptables/ip6tables invocations use -w 5 (wait up to 5 seconds for
// the xtables lock). This prevents failures when other Android system
// components (netd, OpenVPN, etc.) briefly hold the lock.
//
// # Cleanup is idempotent
//
// Apply() always runs Cleanup() first, and Cleanup() ignores errors from
// non-existent chains/rules. Cleanup also removes legacy nat-table chains
// from older SSHCustom versions to ensure clean upgrades.
package iptables

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Config is the subset of daemon config needed to install rules.
type Config struct {
	ChainsPrefix  string
	TCPPort       int
	APIPort       int
	SocksPort     int
	Hotspot       bool
	HotspotIfaces []string

	// Leak protection toggles. All default to false at the package level
	// because the daemon's Config decides the defaults; passing them in
	// explicitly keeps this package config-source-agnostic.
	BlockIPv6Leaks bool // ip6tables filter REJECT all v6 OUTPUT/FORWARD except UID 0
	BlockQUIC      bool // iptables filter REJECT UDP/443 and UDP/80 except UID 0
	FlushConntrack bool // drop existing flows after rules are installed/removed
	SetSysctls     bool // route_localnet=1, rp_filter=2 (loose)
}

// Default chain prefix when none is configured.
const DefaultPrefix = "SSHC"

// tproxyMark is the fwmark value used for TPROXY policy routing.
// Bit 0 (0x1) is safe on HyperOS: our rule inserts at -I OUTPUT 1 (runs
// first), policy routing matches immediately in OUTPUT before HyperOS's
// POSTROUTING rules that clear marks.
const tproxyMark = "0x1/0x1"

// tproxyTable is the policy-routing table number for TPROXY local delivery.
const tproxyTable = "100"

// Default hotspot interface globs covering most modern Android tether modes.
// wlan+ catches Wi-Fi hotspot, ap+ catches some MediaTek devices, rndis+ is
// USB tethering, ncm+ is USB CDC-NCM tethering, bt-pan+ is Bluetooth PAN.
var DefaultHotspotIfaces = []string{"wlan+", "swlan+", "ap+", "rndis+", "ncm+", "bt-pan+"}

// privateCIDRs is the set of address ranges we always RETURN. Loopback and
// private space must not be tunneled, link-local would never reach the
// internet anyway, and 100.64/10 is the CGNAT range which we exclude so
// the carrier's own infrastructure (NAT64 gateways, etc.) keeps working.
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

// privateCIDRsV6 is the set of IPv6 address ranges we always RETURN in
// TPROXY v6 mode. Link-local, loopback, multicast, and ULA must not be
// tunneled.
var privateCIDRsV6 = []string{
	"::1/128",
	"fe80::/10",
	"fc00::/7",
	"ff00::/8",
	"::ffff:0:0/96",
}

// allLegacyChains lists every nat-table chain name SSHCustom has ever
// created. Cleanup() removes all of them so users upgrading from an older
// module don't end up with orphaned chains.
func allLegacyChains(prefix string) []string {
	return []string{
		prefix + "_OUTPUT",
		prefix + "_PREROUTING",
		prefix + "_PROXY",
		prefix + "_DNS",
		prefix + "_HOTSPOT",
		prefix + "_HOTSPOT_DNS",
	}
}

// allLegacyFilterChains lists every filter-table chain name. These are
// new in v1.1.0 (leak protection); future versions add to this list.
func allLegacyFilterChains(prefix string) []string {
	return []string{
		prefix + "_FILTER_QUIC",
	}
}

// allLegacyV6FilterChains lists every ip6tables filter-table chain.
func allLegacyV6FilterChains(prefix string) []string {
	return []string{
		prefix + "_FILTER_OUTPUT6",
		prefix + "_FILTER_FORWARD6",
	}
}

// allLegacyMangleChains lists every mangle-table chain name (TPROXY mode).
func allLegacyMangleChains(prefix string) []string {
	return []string{
		prefix + "_TPROXY_OUT",
		prefix + "_TPROXY_PRE",
	}
}

// allLegacyV6MangleChains lists every ip6tables mangle-table chain (TPROXY v6).
func allLegacyV6MangleChains(prefix string) []string {
	return []string{
		prefix + "_TPROXY_OUT6",
		prefix + "_TPROXY_PRE6",
	}
}

// Apply installs the SSHCustom transparent-proxy chains. bypassIPs are the
// resolved SSH endpoint IPs that must be excluded from REDIRECT.
//
// The function tolerates duplicate-rule errors from the cleanup pass but
// returns a real error if any chain creation or rule append actually fails.
func Apply(cfg Config, bypassIPs []string) error {
	prefix := cfg.ChainsPrefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	port := cfg.TCPPort
	if port <= 0 {
		port = 10810
	}

	// Always clean before applying.
	_ = Cleanup(cfg)

	// route_localnet=1 is REQUIRED for TPROXY OUTPUT regardless of the
	// user-facing "Sysctl Hardening" toggle. The toggle only governs the
	// defensive rp_filter knob (applyDefensiveSysctls). v1.3.3 conflated
	// the two, so toggling sysctl_hardening OFF caused TPROXY to break on
	// the second apply: restoreSysctls() unconditionally zeroed
	// route_localnet but applySysctls() was gated by the toggle, leaving
	// an asymmetric add/cleanup cycle that permanently broke the local
	// loopback delivery path until reboot.
	applyTPROXYRequiredSysctls()

	// Sysctl hardening (defensive rp_filter=loose) must come before any
	// rule install when enabled.
	if cfg.SetSysctls {
		applyDefensiveSysctls()
	}

	var err error
	err = applyTPROXY(cfg, prefix, port, bypassIPs)
	if err != nil {
		// Partial apply may have left chains/rules behind. Clean up so
		// the system isn't left in a broken half-installed state.
		_ = Cleanup(cfg)
		return err
	}

	// Leak protection rules go last.
	if cfg.BlockQUIC {
		applyQUICBlock(prefix)
	}
	if cfg.BlockIPv6Leaks {
		applyIPv6Lockdown(prefix, true)
	}

	// Flush conntrack last.
	if cfg.FlushConntrack {
		flushConntrack()
	}

	return nil
}

// applyTPROXY installs mangle-table TPROXY chains for IPv4 TCP only.
// IPv6 is handled by filter-table REJECT (see applyIPv6Lockdown).
func applyTPROXY(cfg Config, prefix string, port int, bypassIPs []string) error {
	portStr := strconv.Itoa(port)
	outChain4 := prefix + "_TPROXY_OUT"
	preChain4 := prefix + "_TPROXY_PRE"

	var errs []string
	run4 := func(args ...string) {
		full := append([]string{"-w", "5"}, args...)
		if b, err := exec.Command("iptables", full...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("iptables %s: %v %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(b))))
		}
	}

	// --- IPv4 mangle chains ---
	run4("-t", "mangle", "-N", outChain4)
	run4("-t", "mangle", "-F", outChain4)
	run4("-t", "mangle", "-N", preChain4)
	run4("-t", "mangle", "-F", preChain4)

	// OUTPUT chain: bypass UID 0 (daemon), private CIDRs, bypass IPs, daemon ports
	run4("-t", "mangle", "-A", outChain4, "-m", "owner", "--uid-owner", "0", "-j", "RETURN")
	for _, cidr := range privateCIDRs {
		run4("-t", "mangle", "-A", outChain4, "-d", cidr, "-j", "RETURN")
	}
	for _, ip := range bypassIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		run4("-t", "mangle", "-A", outChain4, "-d", ip, "-j", "RETURN")
	}
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort} {
		if p > 0 {
			run4("-t", "mangle", "-A", outChain4, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	// Mark matching packets for policy routing, then TPROXY delivers to our socket
	run4("-t", "mangle", "-A", outChain4, "-p", "tcp", "-j", "MARK", "--set-mark", tproxyMark)
	// PREROUTING chain for hotspot / forwarded traffic
	for _, cidr := range privateCIDRs {
		run4("-t", "mangle", "-A", preChain4, "-d", cidr, "-j", "RETURN")
	}
	for _, ip := range bypassIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		run4("-t", "mangle", "-A", preChain4, "-d", ip, "-j", "RETURN")
	}
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort} {
		if p > 0 {
			run4("-t", "mangle", "-A", preChain4, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	run4("-t", "mangle", "-A", preChain4, "-p", "tcp", "-j", "TPROXY",
		"--on-port", portStr, "--on-ip", "0.0.0.0", "--tproxy-mark", tproxyMark)

	// Hook into builtin chains
	run4("-t", "mangle", "-I", "OUTPUT", "1", "-p", "tcp", "-j", outChain4)
	// PREROUTING hook is REQUIRED for locally-originated traffic: after OUTPUT
	// marks the packet with fwmark 0x1, policy routing sends it to loopback,
	// and the packet re-enters the network stack via PREROUTING. Without this
	// unconditional hook, marked packets fall through PREROUTING untouched and
	// never reach the TPROXY target → the listener gets zero connections.
	// We match on the fwmark so only our already-marked packets enter the chain.
	run4("-t", "mangle", "-I", "PREROUTING", "1", "-p", "tcp", "-m", "mark", "--mark", tproxyMark, "-j", preChain4)
	if cfg.Hotspot {
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			// Hotspot traffic (forwarded from tethered clients) won't have
			// our fwmark, so we match on interface instead.
			run4("-t", "mangle", "-I", "PREROUTING", "1", "-i", iface, "-p", "tcp", "-j", preChain4)
		}
		_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		_ = exec.Command("iptables", "-w", "5", "-I", "FORWARD", "-j", "ACCEPT").Run()
	}

	// --- IPv6: NO mangle TPROXY chains ---
	// The SSH server (bc.game/dropbear) has no IPv6 connectivity, so tunneling
	// v6 TCP through it produces "Network is unreachable" for every connection.
	// Instead, we rely on applyIPv6Lockdown (filter REJECT) to block v6 OUTPUT
	// immediately — apps fall back to IPv4 in <50ms. v6 mangle chains are
	// cleaned up by Cleanup() for users upgrading from v1.2.x/v1.3.0.

	// --- Policy routing for TPROXY (IPv4 only) ---
	// fwmark 0x1/0x1 → table 100 → local 0.0.0.0/0 dev lo
	// Priority 9999 ensures our rule fires after the local table (priority 0)
	// but before any other system rules.
	_ = exec.Command("ip", "rule", "add", "fwmark", tproxyMark, "table", tproxyTable, "prio", "9999").Run()
	_ = exec.Command("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", tproxyTable).Run()

	// Filter non-fatal errors
	var fatal []string
	for _, e := range errs {
		if strings.Contains(e, "No chain/target/match") ||
			strings.Contains(e, "does a matching rule exist") ||
			strings.Contains(e, "Chain already exists") ||
			strings.Contains(e, "File exists") {
			continue
		}
		fatal = append(fatal, e)
	}
	if len(fatal) > 0 {
		return errors.New(strings.Join(fatal, "; "))
	}
	return nil
}

// Cleanup removes every SSHCustom chain and the FORWARD ACCEPT rule. Always
// returns nil; failures here are logged via the caller's discretion but
// don't propagate because cleanup is best-effort.
func Cleanup(cfg Config) error {
	prefix := cfg.ChainsPrefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	chains := allLegacyChains(prefix)
	ifaces := cfg.HotspotIfaces
	if len(ifaces) == 0 {
		ifaces = DefaultHotspotIfaces
	}

	// Phase 1: detach hooks from OUTPUT/PREROUTING (nat table).
	for _, ch := range chains {
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "PREROUTING", "-j", ch).Run()
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run()
			_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-j", ch).Run()
		}
	}
	// Phase 2: flush and delete nat chains.
	for _, ch := range chains {
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-F", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "nat", "-X", ch).Run()
	}
	_ = exec.Command("iptables", "-w", "5", "-D", "FORWARD", "-j", "ACCEPT").Run()

	// Phase 3: clean TPROXY mangle chains (IPv4).
	// Delete hooks in a loop to handle duplicates from failed prior applies.
	for _, ch := range allLegacyMangleChains(prefix) {
		for i := 0; i < 10; i++ {
			if exec.Command("iptables", "-w", "5", "-t", "mangle", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run() != nil {
				break
			}
		}
		for i := 0; i < 10; i++ {
			if exec.Command("iptables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run() != nil {
				break
			}
		}
		for i := 0; i < 10; i++ {
			if exec.Command("iptables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-m", "mark", "--mark", tproxyMark, "-j", ch).Run() != nil {
				break
			}
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			for i := 0; i < 10; i++ {
				if exec.Command("iptables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run() != nil {
					break
				}
			}
		}
		_ = exec.Command("iptables", "-w", "5", "-t", "mangle", "-F", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "mangle", "-X", ch).Run()
	}

	// Phase 4: clean TPROXY mangle chains (IPv6) — legacy from v1.2.x/v1.3.0.
	for _, ch := range allLegacyV6MangleChains(prefix) {
		for i := 0; i < 10; i++ {
			if exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run() != nil {
				break
			}
		}
		for i := 0; i < 10; i++ {
			if exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run() != nil {
				break
			}
		}
		for i := 0; i < 10; i++ {
			if exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-m", "mark", "--mark", tproxyMark, "-j", ch).Run() != nil {
				break
			}
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			for i := 0; i < 10; i++ {
				if exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run() != nil {
					break
				}
			}
		}
		_ = exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-F", ch).Run()
		_ = exec.Command("ip6tables", "-w", "5", "-t", "mangle", "-X", ch).Run()
	}
	_ = exec.Command("ip6tables", "-w", "5", "-D", "FORWARD", "-j", "ACCEPT").Run()

	// Phase 5: remove TPROXY policy routes (best-effort, loop for duplicates).
	for i := 0; i < 5; i++ {
		if exec.Command("ip", "rule", "del", "fwmark", tproxyMark, "table", tproxyTable).Run() != nil {
			break
		}
	}
	// Loop-delete the local-route too: rare, but if a previous apply
	// somehow installed it twice (kernel quirks during rapid reconnect)
	// we want to remove every copy. The flush below is the real safety
	// net, but explicit deletion makes intent clear.
	for i := 0; i < 5; i++ {
		if exec.Command("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", tproxyTable).Run() != nil {
			break
		}
	}
	// Legacy v6 policy routes from v1.2.x/v1.3.0.
	for i := 0; i < 5; i++ {
		if exec.Command("ip", "-6", "rule", "del", "fwmark", tproxyMark, "table", tproxyTable).Run() != nil {
			break
		}
	}
	for i := 0; i < 5; i++ {
		if exec.Command("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", tproxyTable).Run() != nil {
			break
		}
	}
	// Flush table 100 entirely in case any stale routes remain.
	_ = exec.Command("ip", "route", "flush", "table", tproxyTable).Run()
	_ = exec.Command("ip", "-6", "route", "flush", "table", tproxyTable).Run()

	// Leak-protection cleanup.
	cleanupQUICBlock(prefix)
	cleanupIPv6Lockdown(prefix)

	restoreSysctls()

	if cfg.FlushConntrack {
		flushConntrack()
	}
	return nil
}

// applyQUICBlock installs a small filter-table chain that REJECTs UDP/443
// and UDP/80 except from UID 0 (the daemon).
//
// Why both 443 and 80: QUIC most commonly uses 443 but HTTP/3 over 80 is
// also defined and some CDNs use it. Together they cover Chrome's full
// fallback ladder, forcing TCP within ~50ms instead of the usual 8s timeout.
func applyQUICBlock(prefix string) {
	chain := prefix + "_FILTER_QUIC"
	cmds := [][]string{
		{"iptables", "-w", "5", "-t", "filter", "-N", chain},
		{"iptables", "-w", "5", "-t", "filter", "-F", chain},
		// Daemon (UID 0) can speak QUIC freely. We only block app traffic.
		{"iptables", "-w", "5", "-t", "filter", "-A", chain, "-m", "owner", "--uid-owner", "0", "-j", "RETURN"},
		// Localhost UDP/443 is sometimes used by mDNS/cast helpers; let it through.
		{"iptables", "-w", "5", "-t", "filter", "-A", chain, "-d", "127.0.0.0/8", "-j", "RETURN"},
		{"iptables", "-w", "5", "-t", "filter", "-A", chain, "-p", "udp", "--dport", "443", "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		{"iptables", "-w", "5", "-t", "filter", "-A", chain, "-p", "udp", "--dport", "80", "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		// Hook into top of OUTPUT.
		{"iptables", "-w", "5", "-t", "filter", "-I", "OUTPUT", "1", "-p", "udp", "-j", chain},
	}
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

// cleanupQUICBlock removes everything applyQUICBlock installed. Idempotent.
func cleanupQUICBlock(prefix string) {
	for _, ch := range allLegacyFilterChains(prefix) {
		_ = exec.Command("iptables", "-w", "5", "-t", "filter", "-D", "OUTPUT", "-p", "udp", "-j", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "filter", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "filter", "-F", ch).Run()
		_ = exec.Command("iptables", "-w", "5", "-t", "filter", "-X", ch).Run()
	}
}

// applyIPv6Lockdown installs ip6tables filter rules that REJECT all
// outbound and forwarded IPv6 traffic except from UID 0.
//
// Why filter-table REJECT and not nat: most Android kernels (including
// HyperOS / Poco F6) compile out CONFIG_IP6_NF_NAT, so v6 nat is not
// available. Filter-table REJECT is universal: it works with just
// CONFIG_IP6_NF_FILTER, which every Android kernel since 4.4 has.
//
// The REJECT (ICMPv6 admin-prohibited) is preferable to DROP because it
// signals the userspace TCP stack to fail-fast instead of waiting for the
// retransmission timer. Apps fall back to v4 in milliseconds.
//
// v1.3.1: We no longer allow marked v6 TCP through. The SSH server has no
// IPv6 connectivity, so tunneling v6 just produces "Network is unreachable"
// floods. All v6 gets rejected immediately → fast fallback to IPv4.
func applyIPv6Lockdown(prefix string, _ bool) {
	out := prefix + "_FILTER_OUTPUT6"
	fwd := prefix + "_FILTER_FORWARD6"
	cmds := [][]string{
		{"ip6tables", "-w", "5", "-t", "filter", "-N", out},
		{"ip6tables", "-w", "5", "-t", "filter", "-F", out},
		{"ip6tables", "-w", "5", "-t", "filter", "-N", fwd},
		{"ip6tables", "-w", "5", "-t", "filter", "-F", fwd},
		// Daemon (UID 0) keeps full v6. Loopback is exempt for OS housekeeping.
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-m", "owner", "--uid-owner", "0", "-j", "RETURN"},
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-o", "lo", "-j", "RETURN"},
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-d", "::1/128", "-j", "RETURN"},
		// Allow link-local NDP/RA messages so the kernel can keep the v6
		// default route negotiated.
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-d", "fe80::/10", "-j", "RETURN"},
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-d", "ff00::/8", "-j", "RETURN"},
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-p", "icmpv6", "-j", "RETURN"},
		// Catch-all: REJECT anything else. Apps fall back to IPv4 in <50ms.
		{"ip6tables", "-w", "5", "-t", "filter", "-A", out, "-j", "REJECT", "--reject-with", "icmp6-adm-prohibited"},
		// Forward chain (hotspot v6 leaks).
		{"ip6tables", "-w", "5", "-t", "filter", "-A", fwd, "-j", "REJECT", "--reject-with", "icmp6-adm-prohibited"},
		// Hooks at top of builtin chains.
		{"ip6tables", "-w", "5", "-t", "filter", "-I", "OUTPUT", "1", "-j", out},
		{"ip6tables", "-w", "5", "-t", "filter", "-I", "FORWARD", "1", "-j", fwd},
	}
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

// cleanupIPv6Lockdown removes everything applyIPv6Lockdown installed.
// Idempotent — safe to call when nothing was installed.
func cleanupIPv6Lockdown(prefix string) {
	for _, ch := range allLegacyV6FilterChains(prefix) {
		_ = exec.Command("ip6tables", "-w", "5", "-t", "filter", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("ip6tables", "-w", "5", "-t", "filter", "-D", "FORWARD", "-j", ch).Run()
		_ = exec.Command("ip6tables", "-w", "5", "-t", "filter", "-F", ch).Run()
		_ = exec.Command("ip6tables", "-w", "5", "-t", "filter", "-X", ch).Run()
	}
}

// applyTPROXYRequiredSysctls writes the sysctl knobs that TPROXY itself
// REQUIRES to function: route_localnet=1 on every IPv4 conf path. Without
// it, the policy route `local 0.0.0.0/0 dev lo table 100` silently drops
// marked packets (kernel treats the source as a martian) and the listener
// never receives traffic.
//
// This is called unconditionally from Apply(), independent of the
// user-facing "Sysctl Hardening" toggle. The toggle only governs the
// defensive rp_filter knob (applyDefensiveSysctls). Conflating the two in
// v1.3.3 meant a user with sysctl_hardening=false saw TPROXY break on the
// second apply: Cleanup() reset route_localnet=0 unconditionally, but
// Apply() only set it back to 1 when the toggle was on.
func applyTPROXYRequiredSysctls() {
	writeProc("/proc/sys/net/ipv4/conf/all/route_localnet", "1\n")
	writeProc("/proc/sys/net/ipv4/conf/default/route_localnet", "1\n")
	// lo specifically — the TPROXY loopback hop happens here.
	writeProc("/proc/sys/net/ipv4/conf/lo/route_localnet", "1\n")
	if entries, err := os.ReadDir("/proc/sys/net/ipv4/conf"); err == nil {
		for _, e := range entries {
			if e.Name() == "all" || e.Name() == "default" || e.Name() == "lo" {
				continue
			}
			writeProc("/proc/sys/net/ipv4/conf/"+e.Name()+"/route_localnet", "1\n")
		}
	}
}

// applyDefensiveSysctls writes the optional rp_filter=2 (loose) knob,
// gated by the user-facing "Sysctl Hardening" toggle. Loose rp_filter
// accepts return packets on lo even when the routing table would normally
// reject them, which helps on stricter Android kernels.
func applyDefensiveSysctls() {
	writeProc("/proc/sys/net/ipv4/conf/all/rp_filter", "2\n")
	writeProc("/proc/sys/net/ipv4/conf/default/rp_filter", "2\n")
	if entries, err := os.ReadDir("/proc/sys/net/ipv4/conf"); err == nil {
		for _, e := range entries {
			if e.Name() == "all" || e.Name() == "default" {
				continue
			}
			writeProc("/proc/sys/net/ipv4/conf/"+e.Name()+"/rp_filter", "2\n")
		}
	}
}

// restoreSysctls puts rp_filter back to Android's documented default. We
// deliberately do NOT touch route_localnet here:
//
//   - TPROXY requires route_localnet=1. Resetting it to 0 mid-cycle (e.g.
//     between Cleanup and the next Apply, or during a reconnect race)
//     silently breaks the loopback delivery path.
//   - route_localnet=1 has no security implications outside of routing
//     packets bound to 127.0.0.0/8; on Android, only privileged callers
//     can craft such packets anyway.
//   - The asymmetric apply/cleanup of route_localnet was the v1.3.3
//     reconnect bug: Cleanup zeroed it unconditionally while Apply only
//     restored it when the toggle was on, leaving TPROXY broken until
//     reboot when the toggle was off.
//
// We don't snapshot original values because reading-then-writing adds I/O
// cost on every Apply() and the practical answer is always the stock value.
func restoreSysctls() {
	writeProc("/proc/sys/net/ipv4/conf/all/rp_filter", "1\n")
	writeProc("/proc/sys/net/ipv4/conf/default/rp_filter", "1\n")
}

// writeProc writes value to path with permissions 0644. Returns silently
// on any failure (e.g. SELinux denial, path missing on a stripped kernel).
func writeProc(path, value string) {
	_ = os.WriteFile(path, []byte(value), 0644)
}

// flushConntrack drops all existing connection-tracking entries. This is
// what makes the leak-protection rules retroactive: without a flush, any
// flow established before Apply() ran continues using its cached route
// for the rest of its life.
//
// We try multiple mechanisms because Android kernels and userland tooling
// vary. In order of preference:
//
//  1. /proc/sys/net/netfilter/nf_conntrack_flush — modern kernels, write
//     "1" to evict every flow.
//  2. `conntrack -F` — present on devices with conntrack-tools installed.
//  3. `ip route flush cache` for v4 and v6 — flushes the routing cache,
//     which on its own doesn't drop conntrack entries but knocks loose
//     dst-cache entries that pin reused 5-tuples. Combined with the above
//     this gets us most of the way there.
//
// If none of these work, the user can toggle airplane mode for 5s, which
// drops every rmnet session and achieves the same effect manually.
func flushConntrack() {
	writeProc("/proc/sys/net/netfilter/nf_conntrack_flush", "1\n")
	_ = exec.Command("conntrack", "-F").Run()
	_ = exec.Command("ip", "-4", "route", "flush", "cache").Run()
	_ = exec.Command("ip", "-6", "route", "flush", "cache").Run()
}

// ProbeTPROXY tests whether the kernel supports the TPROXY iptables target
// by attempting to create a temporary mangle chain with a TPROXY rule. If
// the rule insertion succeeds the kernel has CONFIG_NETFILTER_XT_TARGET_TPROXY.
// The test chain is always cleaned up regardless of result.
func ProbeTPROXY() bool {
	chain := "_SSHC_PROBE_TPROXY"
	// Create temporary chain
	if err := exec.Command("iptables", "-w", "5", "-t", "mangle", "-N", chain).Run(); err != nil {
		return false
	}
	// Attempt a TPROXY rule
	err := exec.Command("iptables", "-w", "5", "-t", "mangle", "-A", chain,
		"-p", "tcp", "-j", "TPROXY",
		"--on-port", "1", "--tproxy-mark", "0x1/0x1").Run()
	// Cleanup regardless
	_ = exec.Command("iptables", "-w", "5", "-t", "mangle", "-F", chain).Run()
	_ = exec.Command("iptables", "-w", "5", "-t", "mangle", "-X", chain).Run()
	return err == nil
}
