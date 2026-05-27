// Package iptables installs and removes the SSHCustom transparent-proxy
// chains.
//
// # What this does
//
// We install two chains in the nat table:
//
//   - SSHC_OUTPUT  hooked into nat OUTPUT,  for traffic from this device.
//   - SSHC_PREROUTING  hooked into nat PREROUTING per hotspot interface,
//     for traffic from tethered clients.
//
// Each chain RETURNs traffic destined to private/loopback/link-local CIDRs,
// the daemon's own bypass IPs (resolved SSH endpoint addresses), and the
// daemon's own listener ports. Anything else hits a final
// REDIRECT --to-ports <transparent_tcp_port>, which the kernel rewrites
// in-place; the daemon then reads the original destination via the
// SO_ORIGINAL_DST socket option.
//
// # The uid-0 RETURN rule
//
// SSHC_OUTPUT also has an early "owner uid 0 RETURN" rule. Without it, the
// daemon's own outbound connections (the SSH tunnel itself, DNS lookups,
// etc.) would be redirected through itself and form an infinite loop. Since
// we run from /data/adb/sshcustom under root (Magisk-postFsData environment),
// matching uid 0 reliably bypasses our own traffic.
//
// # Bypass IPs
//
// The daemon passes in a list of resolved SSH endpoint IPs at apply time.
// Each becomes a `-d <ip> RETURN` rule before the catch-all REDIRECT. This
// is critical: without it, the SSH carrier connection itself would hit the
// REDIRECT and form a loop.
//
// # Leak protection (v1.1.0)
//
// REDIRECT in the nat table only catches new TCP/v4 flows. Three categories
// of traffic escape it on real Android devices:
//
//   - IPv6 in any form (we don't run ip6tables nat rules; many kernels
//     don't even support IPv6 nat). Apps that resolve to AAAA records and
//     race-connect via v6 (RFC 6724) bypass the tunnel completely.
//   - UDP in any form, including QUIC (UDP/443). Chrome/YouTube prefer
//     QUIC; without intervention they wait the full retry window before
//     falling back to TCP/443.
//   - Connections that already exist in conntrack from before our nat
//     rules were installed. Once a flow has a conntrack entry, the nat
//     table is bypassed for the rest of its life.
//
// For carrier-restricted users (CGNAT prepaid plans that whitelist a few
// hosts), the leaks above mean the broken-direct-route stalls *every*
// non-tunneled connection by ~8 seconds before the app gives up. The
// LeakProtection block plugs all three:
//
//   - applyIPv6Lockdown: ip6tables filter rules that REJECT outbound v6
//     except from UID 0 (the daemon itself). Apps fall back to v4 instantly.
//   - applyQUICBlock: iptables filter rules that REJECT UDP/443 (and
//     UDP/80) except from UID 0. Chrome falls back to TCP immediately.
//   - flushConntrack: at the end of Apply() and Cleanup(), drop existing
//     flows so old direct-route sockets die and reconnect through the
//     freshly-installed rules.
//
// # Sysctl hardening
//
// applySysctls sets two kernel knobs that make REDIRECT more reliable on
// the OUTPUT path:
//
//   - net.ipv4.conf.all.route_localnet=1 lets the kernel deliver packets
//     whose post-NAT destination is 127.0.0.0/8. Without this, some
//     OUTPUT-REDIRECT packets get silently dropped on stricter kernels.
//   - net.ipv4.conf.all.rp_filter=2 (loose) accepts return packets even
//     when the reverse-path interface differs from the forward path,
//     which happens on every OUTPUT REDIRECT bouncing through lo.
//
// Both are no-ops where the value is already correct; both are reverted
// to the system default in Cleanup().
//
// # Cleanup is idempotent
//
// Apply() always runs Cleanup() first, and Cleanup() ignores errors from
// non-existent chains/rules. The point is that running install -> stop ->
// install -> stop in any order leaves the iptables nat table identical.
// Real-world Android networks reset routes constantly; the daemon must be
// able to tear down and rebuild without leaving leftover rules.
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

	// Mode selects the transparent proxy mechanism:
	//   "redirect" — nat-table REDIRECT (IPv4 only, uses SO_ORIGINAL_DST)
	//   "tproxy"   — mangle-table TPROXY (IPv4 + IPv6, uses IP_TRANSPARENT)
	//   "auto"     — probe for TPROXY support, fall back to REDIRECT
	// The caller (daemon) resolves "auto" before passing Config in.
	Mode string

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

	// Sysctl hardening must come *before* any rule install.
	if cfg.SetSysctls {
		applySysctls()
	}

	var err error
	if cfg.Mode == "tproxy" {
		err = applyTPROXY(cfg, prefix, port, bypassIPs)
	} else {
		err = applyREDIRECT(cfg, prefix, port, bypassIPs)
	}
	if err != nil {
		return err
	}

	// Leak protection rules go last.
	if cfg.BlockQUIC {
		applyQUICBlock(prefix)
	}
	if cfg.BlockIPv6Leaks {
		applyIPv6Lockdown(prefix, cfg.Mode == "tproxy")
	}

	// Flush conntrack last.
	if cfg.FlushConntrack {
		flushConntrack()
	}

	return nil
}

// applyREDIRECT installs the original nat-table REDIRECT chains (IPv4 only).
func applyREDIRECT(cfg Config, prefix string, port int, bypassIPs []string) error {
	outChain := prefix + "_OUTPUT"
	preChain := prefix + "_PREROUTING"

	var errs []string
	run := func(args ...string) {
		if b, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("iptables %s: %v %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(b))))
		}
	}

	for _, ch := range []string{outChain, preChain} {
		run("-t", "nat", "-N", ch)
		run("-t", "nat", "-F", ch)
	}

	addBypasses := func(ch string, isOutput bool) {
		if isOutput {
			run("-t", "nat", "-A", ch, "-m", "owner", "--uid-owner", "0", "-j", "RETURN")
		}
		for _, cidr := range privateCIDRs {
			run("-t", "nat", "-A", ch, "-d", cidr, "-j", "RETURN")
		}
		for _, ip := range bypassIPs {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			run("-t", "nat", "-A", ch, "-d", ip, "-j", "RETURN")
		}
		for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort} {
			if p > 0 {
				run("-t", "nat", "-A", ch, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
			}
		}
		run("-t", "nat", "-A", ch, "-p", "tcp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(port))
	}
	addBypasses(outChain, true)
	addBypasses(preChain, false)

	run("-t", "nat", "-I", "OUTPUT", "1", "-p", "tcp", "-j", outChain)

	if cfg.Hotspot {
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			run("-t", "nat", "-I", "PREROUTING", "1", "-i", iface, "-p", "tcp", "-j", preChain)
		}
		_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		_ = exec.Command("iptables", "-I", "FORWARD", "-j", "ACCEPT").Run()
	}

	var fatal []string
	for _, e := range errs {
		if strings.Contains(e, "No chain/target/match") ||
			strings.Contains(e, "does a matching rule exist") ||
			strings.Contains(e, "Chain already exists") {
			continue
		}
		fatal = append(fatal, e)
	}
	if len(fatal) > 0 {
		return errors.New(strings.Join(fatal, "; "))
	}
	return nil
}

// applyTPROXY installs mangle-table TPROXY chains for IPv4 and IPv6 TCP.
// This enables transparent proxying of both address families through a
// single IP_TRANSPARENT listener socket.
func applyTPROXY(cfg Config, prefix string, port int, bypassIPs []string) error {
	portStr := strconv.Itoa(port)
	outChain4 := prefix + "_TPROXY_OUT"
	preChain4 := prefix + "_TPROXY_PRE"
	outChain6 := prefix + "_TPROXY_OUT6"
	preChain6 := prefix + "_TPROXY_PRE6"

	var errs []string
	run4 := func(args ...string) {
		if b, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("iptables %s: %v %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(b))))
		}
	}
	run6 := func(args ...string) {
		if b, err := exec.Command("ip6tables", args...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("ip6tables %s: %v %s",
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
	if cfg.Hotspot {
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			run4("-t", "mangle", "-I", "PREROUTING", "1", "-i", iface, "-p", "tcp", "-j", preChain4)
		}
		_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		_ = exec.Command("iptables", "-I", "FORWARD", "-j", "ACCEPT").Run()
	}

	// --- IPv6 mangle chains ---
	run6("-t", "mangle", "-N", outChain6)
	run6("-t", "mangle", "-F", outChain6)
	run6("-t", "mangle", "-N", preChain6)
	run6("-t", "mangle", "-F", preChain6)

	// OUTPUT chain v6: bypass UID 0, private v6 CIDRs, daemon ports
	run6("-t", "mangle", "-A", outChain6, "-m", "owner", "--uid-owner", "0", "-j", "RETURN")
	for _, cidr := range privateCIDRsV6 {
		run6("-t", "mangle", "-A", outChain6, "-d", cidr, "-j", "RETURN")
	}
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort} {
		if p > 0 {
			run6("-t", "mangle", "-A", outChain6, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	run6("-t", "mangle", "-A", outChain6, "-p", "tcp", "-j", "MARK", "--set-mark", tproxyMark)
	// PREROUTING chain v6
	for _, cidr := range privateCIDRsV6 {
		run6("-t", "mangle", "-A", preChain6, "-d", cidr, "-j", "RETURN")
	}
	for _, p := range []int{cfg.APIPort, cfg.SocksPort, cfg.TCPPort} {
		if p > 0 {
			run6("-t", "mangle", "-A", preChain6, "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "RETURN")
		}
	}
	run6("-t", "mangle", "-A", preChain6, "-p", "tcp", "-j", "TPROXY",
		"--on-port", portStr, "--on-ip", "::", "--tproxy-mark", tproxyMark)

	run6("-t", "mangle", "-I", "OUTPUT", "1", "-p", "tcp", "-j", outChain6)
	if cfg.Hotspot {
		ifaces := cfg.HotspotIfaces
		if len(ifaces) == 0 {
			ifaces = DefaultHotspotIfaces
		}
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			run6("-t", "mangle", "-I", "PREROUTING", "1", "-i", iface, "-p", "tcp", "-j", preChain6)
		}
		_ = exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").Run()
		_ = exec.Command("ip6tables", "-I", "FORWARD", "-j", "ACCEPT").Run()
	}

	// --- Policy routing for TPROXY ---
	// IPv4: fwmark 0x1/0x1 → table 100 → local 0.0.0.0/0 dev lo
	_ = exec.Command("ip", "rule", "add", "fwmark", tproxyMark, "table", tproxyTable).Run()
	_ = exec.Command("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", tproxyTable).Run()
	// IPv6: same for v6
	_ = exec.Command("ip", "-6", "rule", "add", "fwmark", tproxyMark, "table", tproxyTable).Run()
	_ = exec.Command("ip", "-6", "route", "add", "local", "::/0", "dev", "lo", "table", tproxyTable).Run()

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
		_ = exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-j", ch).Run()
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run()
			_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-j", ch).Run()
		}
	}
	// Phase 2: flush and delete nat chains.
	for _, ch := range chains {
		_ = exec.Command("iptables", "-t", "nat", "-F", ch).Run()
		_ = exec.Command("iptables", "-t", "nat", "-X", ch).Run()
	}
	_ = exec.Command("iptables", "-D", "FORWARD", "-j", "ACCEPT").Run()

	// Phase 3: clean TPROXY mangle chains (IPv4).
	for _, ch := range allLegacyMangleChains(prefix) {
		_ = exec.Command("iptables", "-t", "mangle", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run()
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			_ = exec.Command("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run()
		}
		_ = exec.Command("iptables", "-t", "mangle", "-F", ch).Run()
		_ = exec.Command("iptables", "-t", "mangle", "-X", ch).Run()
	}

	// Phase 4: clean TPROXY mangle chains (IPv6).
	for _, ch := range allLegacyV6MangleChains(prefix) {
		_ = exec.Command("ip6tables", "-t", "mangle", "-D", "OUTPUT", "-p", "tcp", "-j", ch).Run()
		_ = exec.Command("ip6tables", "-t", "mangle", "-D", "PREROUTING", "-p", "tcp", "-j", ch).Run()
		for _, iface := range ifaces {
			if strings.TrimSpace(iface) == "" {
				continue
			}
			_ = exec.Command("ip6tables", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", ch).Run()
		}
		_ = exec.Command("ip6tables", "-t", "mangle", "-F", ch).Run()
		_ = exec.Command("ip6tables", "-t", "mangle", "-X", ch).Run()
	}
	_ = exec.Command("ip6tables", "-D", "FORWARD", "-j", "ACCEPT").Run()

	// Phase 5: remove TPROXY policy routes (best-effort).
	_ = exec.Command("ip", "rule", "del", "fwmark", tproxyMark, "table", tproxyTable).Run()
	_ = exec.Command("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", tproxyTable).Run()
	_ = exec.Command("ip", "-6", "rule", "del", "fwmark", tproxyMark, "table", tproxyTable).Run()
	_ = exec.Command("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", tproxyTable).Run()
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
		{"iptables", "-t", "filter", "-N", chain},
		{"iptables", "-t", "filter", "-F", chain},
		// Daemon (UID 0) can speak QUIC freely. We only block app traffic.
		{"iptables", "-t", "filter", "-A", chain, "-m", "owner", "--uid-owner", "0", "-j", "RETURN"},
		// Localhost UDP/443 is sometimes used by mDNS/cast helpers; let it through.
		{"iptables", "-t", "filter", "-A", chain, "-d", "127.0.0.0/8", "-j", "RETURN"},
		{"iptables", "-t", "filter", "-A", chain, "-p", "udp", "--dport", "443", "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		{"iptables", "-t", "filter", "-A", chain, "-p", "udp", "--dport", "80", "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		// Hook into top of OUTPUT.
		{"iptables", "-t", "filter", "-I", "OUTPUT", "1", "-p", "udp", "-j", chain},
	}
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

// cleanupQUICBlock removes everything applyQUICBlock installed. Idempotent.
func cleanupQUICBlock(prefix string) {
	for _, ch := range allLegacyFilterChains(prefix) {
		_ = exec.Command("iptables", "-t", "filter", "-D", "OUTPUT", "-p", "udp", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "filter", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("iptables", "-t", "filter", "-F", ch).Run()
		_ = exec.Command("iptables", "-t", "filter", "-X", ch).Run()
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
func applyIPv6Lockdown(prefix string, tproxyActive bool) {
	out := prefix + "_FILTER_OUTPUT6"
	fwd := prefix + "_FILTER_FORWARD6"
	cmds := [][]string{
		{"ip6tables", "-t", "filter", "-N", out},
		{"ip6tables", "-t", "filter", "-F", out},
		{"ip6tables", "-t", "filter", "-N", fwd},
		{"ip6tables", "-t", "filter", "-F", fwd},
		// Daemon (UID 0) keeps full v6. Loopback is exempt for OS housekeeping.
		{"ip6tables", "-t", "filter", "-A", out, "-m", "owner", "--uid-owner", "0", "-j", "RETURN"},
		{"ip6tables", "-t", "filter", "-A", out, "-o", "lo", "-j", "RETURN"},
		{"ip6tables", "-t", "filter", "-A", out, "-d", "::1/128", "-j", "RETURN"},
		// Allow link-local NDP/RA messages so the kernel can keep the v6
		// default route negotiated.
		{"ip6tables", "-t", "filter", "-A", out, "-d", "fe80::/10", "-j", "RETURN"},
		{"ip6tables", "-t", "filter", "-A", out, "-d", "ff00::/8", "-j", "RETURN"},
		{"ip6tables", "-t", "filter", "-A", out, "-p", "icmpv6", "-j", "RETURN"},
	}

	if tproxyActive {
		// When TPROXY is active, v6 TCP that carries our fwmark is being
		// proxied — let it through instead of rejecting it.
		cmds = append(cmds, []string{"ip6tables", "-t", "filter", "-A", out,
			"-p", "tcp", "-m", "mark", "--mark", tproxyMark, "-j", "RETURN"})
	}

	cmds = append(cmds,
		// Catch-all: REJECT anything else.
		[]string{"ip6tables", "-t", "filter", "-A", out, "-j", "REJECT", "--reject-with", "icmp6-adm-prohibited"},
		// Forward chain (hotspot v6 leaks).
		[]string{"ip6tables", "-t", "filter", "-A", fwd, "-j", "REJECT", "--reject-with", "icmp6-adm-prohibited"},
		// Hooks at top of builtin chains.
		[]string{"ip6tables", "-t", "filter", "-I", "OUTPUT", "1", "-j", out},
		[]string{"ip6tables", "-t", "filter", "-I", "FORWARD", "1", "-j", fwd},
	)
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

// cleanupIPv6Lockdown removes everything applyIPv6Lockdown installed.
// Idempotent — safe to call when nothing was installed.
func cleanupIPv6Lockdown(prefix string) {
	for _, ch := range allLegacyV6FilterChains(prefix) {
		_ = exec.Command("ip6tables", "-t", "filter", "-D", "OUTPUT", "-j", ch).Run()
		_ = exec.Command("ip6tables", "-t", "filter", "-D", "FORWARD", "-j", ch).Run()
		_ = exec.Command("ip6tables", "-t", "filter", "-F", ch).Run()
		_ = exec.Command("ip6tables", "-t", "filter", "-X", ch).Run()
	}
}

// applySysctls writes route_localnet=1 and rp_filter=2 to the relevant
// procfs paths. These make REDIRECT-via-loopback more reliable on Android
// stock kernels where the defaults are sometimes too strict.
//
// Best-effort: every write is a single syscall and ignored on failure.
// On many Android variants these paths exist but require SELinux
// permissions that the daemon already has via Magisk's u:r:su:s0 context.
func applySysctls() {
	writeProc("/proc/sys/net/ipv4/conf/all/route_localnet", "1\n")
	writeProc("/proc/sys/net/ipv4/conf/default/route_localnet", "1\n")
	writeProc("/proc/sys/net/ipv4/conf/all/rp_filter", "2\n")
	writeProc("/proc/sys/net/ipv4/conf/default/rp_filter", "2\n")
	// Also apply to every currently-existing per-interface knob. New
	// interfaces (USB-tether plugged in later, second SIM activated) get
	// the value via the "default" entry above.
	if entries, err := os.ReadDir("/proc/sys/net/ipv4/conf"); err == nil {
		for _, e := range entries {
			if e.Name() == "all" || e.Name() == "default" {
				continue
			}
			writeProc("/proc/sys/net/ipv4/conf/"+e.Name()+"/route_localnet", "1\n")
			writeProc("/proc/sys/net/ipv4/conf/"+e.Name()+"/rp_filter", "2\n")
		}
	}
}

// restoreSysctls puts the kernel knobs back to Android's documented
// defaults. We don't snapshot the originals because reading-then-writing
// adds I/O cost on every Apply() and the practical answer is always the
// stock value.
func restoreSysctls() {
	writeProc("/proc/sys/net/ipv4/conf/all/route_localnet", "0\n")
	writeProc("/proc/sys/net/ipv4/conf/default/route_localnet", "0\n")
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
	if err := exec.Command("iptables", "-t", "mangle", "-N", chain).Run(); err != nil {
		return false
	}
	// Attempt a TPROXY rule
	err := exec.Command("iptables", "-t", "mangle", "-A", chain,
		"-p", "tcp", "-j", "TPROXY",
		"--on-port", "1", "--tproxy-mark", "0x1/0x1").Run()
	// Cleanup regardless
	_ = exec.Command("iptables", "-t", "mangle", "-F", chain).Run()
	_ = exec.Command("iptables", "-t", "mangle", "-X", chain).Run()
	return err == nil
}
