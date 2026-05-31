// Package dnsx implements SSHCustom's Android-aware hostname resolver.
//
// # Why a custom resolver
//
// Go's net package, when it falls back from cgo to its pure-Go path, reads
// /etc/resolv.conf or tries [::1]:53 directly. On Android both are dead ends:
//
//   - /etc/resolv.conf is empty or missing on most builds.
//   - [::1]:53 is bound to dnsproxyd, which is a Unix-socket service at
//     /dev/socket/dnsproxyd whose access is whitelisted by uid. Our Go binary
//     runs from /data/adb/sshcustom and is not in the whitelist, so connect()
//     to [::1]:53 returns "connection refused".
//   - Java's InetAddress works because it goes through the proper Android DNS
//     framework, but we cannot call into Java from a static Go binary.
//
// # Strategy
//
// The resolver tries, in order:
//
//  1. A 5-minute in-process cache keyed by (mode, servers, host).
//  2. The configured SSHCustom DNS servers, queried directly via UDP. For
//     "device" mode, we read carrier DNS IPs out of Android system properties
//     (net.dns1, net.rmnet_data0.dns1, etc.) — these are the same IPs the
//     network stack assigns when data connects.
//  3. Shell tools as a last resort: getent ahostsv4, toybox getent, ping.
//     These run as subprocesses and parse IPv4 addresses from output.
//
// Cache eviction is callable from the outside (EvictHost) so the caller can
// flush a hostname after detecting a CDN rate-limit response (HTTP 301/302/503
// during payload probing).
package dnsx

import (
	"context"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// cacheEntry holds the resolved IPs and the resolver method used to obtain
// them. The method is included in the cache so callers can distinguish
// between e.g. "we got these from the carrier DNS" vs "we got them from a
// shell ping fallback" without re-resolving.
type cacheEntry struct {
	ips     []string
	method  string
	expires time.Time
}

var hostCache struct {
	sync.Mutex
	entries map[string]cacheEntry
}

// androidPropCache stores the carrier DNS server IPs read from getprop.
// getprop is fast (~1ms) but we still avoid calling it dozens of times.
// 2-minute TTL is plenty: data changes rarely change carrier DNS within
// a single app session.
var androidPropCache struct {
	sync.Mutex
	servers []string
	expires time.Time
}

// Mode is the DNS resolution preset. The same set of values flows from
// config.json straight through to here without translation.
type Mode string

const (
	ModeDevice     Mode = "device"
	ModeGoogle     Mode = "google"
	ModeCloudflare Mode = "cloudflare"
	ModeCustom     Mode = "custom"
)

// Config is the subset of daemon config that controls resolution.
type Config struct {
	Mode    Mode
	Servers []string // ip:port list when Mode==Custom
}

// ResolveHost returns IPv4 addresses for host, or (nil, "dns_failed") on
// total failure. The returned method string is descriptive and shows up in
// the dashboard so users can see which path was used for a given lookup.
func ResolveHost(ctx context.Context, cfg Config, host string) ([]string, string) {
	host = strings.ToLower(strings.TrimSpace(host))
	cacheKey := cacheKey(cfg, host)

	hostCache.Lock()
	if hostCache.entries == nil {
		hostCache.entries = make(map[string]cacheEntry)
	}
	if e, ok := hostCache.entries[cacheKey]; ok && time.Now().Before(e.expires) {
		hostCache.Unlock()
		log.Printf("dnsx: resolved %s from cache mode=%s -> %v", host, cfg.Mode, e.ips)
		return e.ips, "dns_cached_" + e.method
	}
	hostCache.Unlock()

	servers, method := configuredServers(ctx, cfg)
	if len(servers) > 0 {
		if ips := resolveViaDirectDNS(ctx, host, servers); len(ips) > 0 {
			cache(cacheKey, ips, method)
			return ips, method
		}
	}

	if ips := shellResolve(ctx, host); len(ips) > 0 {
		cache(cacheKey, ips, "android_shell_dns")
		return ips, "android_shell_dns"
	}

	return nil, "dns_failed"
}

// EvictHost removes any cache entries for host across all modes/server sets.
// Callers fire this after observing a CDN rate-limit response so the next
// reconnect attempt re-resolves and (likely) lands on a different IP.
func EvictHost(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	hostCache.Lock()
	for key := range hostCache.entries {
		// Cache keys end with "|<host>"; match the suffix to evict every
		// (mode, servers) variation for this hostname.
		if strings.HasSuffix(key, "|"+host) {
			delete(hostCache.entries, key)
		}
	}
	hostCache.Unlock()
	log.Printf("dnsx: cache evicted for %s", host)
}

// NormalizeServers cleans a config-provided server list: strips whitespace,
// adds a default port 53 if absent, deduplicates, and rejects entries that
// don't parse as IP addresses.
func NormalizeServers(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53")
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func cacheKey(cfg Config, host string) string {
	servers := strings.Join(NormalizeServers(cfg.Servers), ",")
	return string(cfg.Mode) + "|" + servers + "|" + host
}

func cache(key string, ips []string, method string) {
	hostCache.Lock()
	if hostCache.entries == nil {
		hostCache.entries = make(map[string]cacheEntry)
	}
	hostCache.entries[key] = cacheEntry{
		ips:     ips,
		method:  method,
		expires: time.Now().Add(5 * time.Minute),
	}
	hostCache.Unlock()
}

// configuredServers returns the DNS server list to query and a descriptive
// method name. For "device" mode it consults Android system properties.
func configuredServers(ctx context.Context, cfg Config) ([]string, string) {
	switch cfg.Mode {
	case ModeGoogle:
		return NormalizeServers([]string{"8.8.8.8", "8.8.4.4"}), "google_dns"
	case ModeCloudflare:
		return NormalizeServers([]string{"1.1.1.1", "1.0.0.1"}), "cloudflare_dns"
	case ModeCustom:
		return NormalizeServers(cfg.Servers), "custom_dns"
	default:
		return AndroidCarrierDNS(ctx), "android_real_dns"
	}
}

// AndroidCarrierDNS reads the actual carrier DNS server IPs from Android
// system properties. These are the IPs the network stack assigns when data
// connects, and they bypass dnsproxyd entirely.
//
// We try a long list of known property names because Android stores DNS
// under different keys depending on version, carrier, and modem driver:
// net.dns* for legacy stack, net.rmnet_data*.dns* for modern radios,
// dhcp.wlan0.dns* for Wi-Fi.
func AndroidCarrierDNS(ctx context.Context) []string {
	androidPropCache.Lock()
	if time.Now().Before(androidPropCache.expires) && len(androidPropCache.servers) > 0 {
		s := androidPropCache.servers
		androidPropCache.Unlock()
		return s
	}
	androidPropCache.Unlock()

	props := []string{
		"net.dns1", "net.dns2", "net.dns3", "net.dns4",
		"net.rmnet0.dns1", "net.rmnet0.dns2",
		"net.rmnet_data0.dns1", "net.rmnet_data0.dns2",
		"net.rmnet_data1.dns1", "net.rmnet_data1.dns2",
		"net.rmnet_data2.dns1", "net.rmnet_data2.dns2",
		"dhcp.rmnet_data0.dns1", "dhcp.rmnet_data0.dns2",
		"dhcp.wlan0.dns1", "dhcp.wlan0.dns2",
	}
	var servers []string
	seen := map[string]bool{}
	for _, prop := range props {
		cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		out, err := exec.CommandContext(cctx, "getprop", prop).Output()
		cancel()
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(string(out))
		if ip == "" || ip == "0.0.0.0" {
			continue
		}
		if net.ParseIP(ip) == nil {
			continue
		}
		addr := net.JoinHostPort(ip, "53")
		if !seen[addr] {
			seen[addr] = true
			servers = append(servers, addr)
		}
		// Three real DNS servers is plenty; stop scanning so we don't waste
		// time on every getprop key when carrier already gave us enough.
		if len(servers) >= 3 {
			break
		}
	}
	if len(servers) > 0 {
		log.Printf("dnsx: android carrier DNS servers: %v", servers)
		androidPropCache.Lock()
		androidPropCache.servers = servers
		androidPropCache.expires = time.Now().Add(2 * time.Minute)
		androidPropCache.Unlock()
	}
	return servers
}

// resolveViaDirectDNS sends an A query directly to each server via UDP.
// This bypasses Android's dnsproxyd entirely. We use Go's pure-Go resolver
// path because we control the Dial function and feed it the right address.
func resolveViaDirectDNS(ctx context.Context, host string, servers []string) []string {
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var lastErr error
			for _, srv := range servers {
				d := &net.Dialer{Timeout: 3 * time.Second}
				c, err := d.DialContext(ctx, "udp", srv)
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	addrs, err := res.LookupHost(ctx, host)
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			ips = appendUnique(ips, ip.String())
		}
	}
	return ips
}

// shellResolve falls back to subprocess tools. Order matters:
//   - getent ahostsv4: fastest when available (Bionic libc 9+).
//   - toybox getent: AOSP toybox shipped with most modern devices.
//   - ping -c 1: brute-force IPv4 extraction from ping output. Almost
//     always works because ping resolves through whatever DNS the kernel
//     network stack uses, which is the same path ICMP would take.
func shellResolve(ctx context.Context, host string) []string {
	cmds := [][]string{
		{"getent", "ahostsv4", host},
		{"toybox", "getent", "ahostsv4", host},
		{"ping", "-c", "1", "-W", "2", host},
	}
	for _, c := range cmds {
		cctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
		out, err := exec.CommandContext(cctx, c[0], c[1:]...).CombinedOutput()
		cancel()
		if err != nil && len(out) == 0 {
			continue
		}
		ips := ExtractIPv4s(string(out))
		if len(ips) > 0 {
			log.Printf("dnsx: resolved %s via %s -> %v", host, strings.Join(c, " "), ips)
			return ips
		}
	}
	return nil
}

// ExtractIPv4s pulls every dotted-quad IPv4 out of arbitrary text. Useful
// for parsing ping output, getent output, and any other shell tool's
// stdout. Exported because the daemon's transport-probe code uses the
// same logic for HTTP response inspection.
func ExtractIPv4s(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '.' || (r >= '0' && r <= '9'))
	})
	var out []string
	for _, f := range fields {
		ip := net.ParseIP(f)
		if ip != nil && ip.To4() != nil {
			out = appendUnique(out, ip.String())
		}
	}
	return out
}

// SanitizeIPv4List trims, validates, and deduplicates an IPv4 address list.
// Used by the daemon to clean up bypass IP lists before installing iptables
// rules where invalid entries would either be no-ops or fail the chain.
func SanitizeIPv4List(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			continue
		}
		out = appendUnique(out, ip.String())
	}
	return out
}

// RotateIPs returns the input rotated by a time-based offset. Used for
// load-spreading across CDN A records: every reconnect attempt starts at
// a different position so two devices behind the same NAT don't both hit
// the same edge.
func RotateIPs(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	out := append([]string(nil), in...)
	off := int(time.Now().UnixNano() % int64(len(out)))
	return append(out[off:], out[:off]...)
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
