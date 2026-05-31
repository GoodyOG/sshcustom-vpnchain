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
	"os/exec"
	"strconv"
	"strings"
)

// Config is the subset of daemon config needed to install rules.
type Config struct {
	ChainsPrefix string
	TCPPort      int
	APIPort      int
	SocksPort    int
	Hotspot      bool
	HotspotIfaces []string
}

// Default chain prefix when none is configured.
const DefaultPrefix = "SSHC"

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

// allLegacyChains lists every chain name SSHCustom has ever created in any
// version. Cleanup() must remove all of them so users upgrading from an
// older module don't end up with orphaned chains.
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
	outChain := prefix + "_OUTPUT"
	preChain := prefix + "_PREROUTING"

	var errs []string
	run := func(args ...string) {
		if b, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("iptables %s: %v %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(b))))
		}
	}

	// Always clean before applying. This handles the upgrade case (older
	// chain layouts) and the crash-recovery case (daemon died holding chains).
	_ = Cleanup(cfg)

	for _, ch := range []string{outChain, preChain} {
		run("-t", "nat", "-N", ch)
		run("-t", "nat", "-F", ch)
	}

	addBypasses := func(ch string, isOutput bool) {
		// Match-by-uid only works on the OUTPUT path. nat PREROUTING runs
		// before any uid is associated with a packet, so this rule would be
		// a no-op (and on some kernels, an error) on PREROUTING.
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

	// Hook into the top of OUTPUT. -I 1 means "insert at position 1", which
	// guarantees we run before any other module's rules and before the
	// kernel's default ACCEPT.
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
		// IP forwarding must be on for tethered TCP to traverse PREROUTING.
		// Errors are silently ignored — sysctl can fail if the property is
		// already 1, and we don't want that to fail Apply() overall.
		_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		_ = exec.Command("iptables", "-I", "FORWARD", "-j", "ACCEPT").Run()
	}

	// Filter out errors that genuinely don't matter. "No chain/target/match"
	// and "does a matching rule exist?" come from the cleanup pre-pass when
	// a rule we tried to delete didn't exist. "Chain already exists" comes
	// from -N when the chain is still there from a previous run that crashed.
	// Anything else is a real failure that should stop the daemon.
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

	// Phase 1: detach hooks from OUTPUT/PREROUTING. We run -D against every
	// shape of rule we have ever installed to handle rolling upgrades.
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
	// Phase 2: flush and delete the chains themselves. Must come after
	// phase 1 because iptables refuses to delete a chain still referenced
	// by a hook.
	for _, ch := range chains {
		_ = exec.Command("iptables", "-t", "nat", "-F", ch).Run()
		_ = exec.Command("iptables", "-t", "nat", "-X", ch).Run()
	}
	// FORWARD ACCEPT was added unconditionally for hotspot mode; remove it
	// even when we didn't install it this session, so legacy rules from a
	// previous module version get cleaned up on first run.
	_ = exec.Command("iptables", "-D", "FORWARD", "-j", "ACCEPT").Run()
	return nil
}
