// Package dnsx provides an Android-aware DNS resolver that bypasses the broken
// localhost stub resolver ([::1]:53 / 127.0.0.1:53) that Go's pure-Go resolver
// falls back to on Android when CGO is disabled and /etc/resolv.conf is absent.
//
// On Android there is no usable /etc/resolv.conf, so Go's net package tries
// 127.0.0.1:53 and [::1]:53, which have no listener (DNS is served by
// netd/bionic, not a local UDP socket). Worse, when IPv6 is disabled
// system-wide (ipv6=false), [::1] becomes unreachable entirely. This resolver
// instead reads the carrier's IPv4 DNS servers directly from Android system
// properties (getprop) and performs raw UDP DNS queries against them, with a
// small in-process cache and public fallback servers.
package dnsx

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	cacheTTL      = 5 * time.Minute
	queryTimeout  = 5 * time.Second
	serversMaxAge = 30 * time.Second
	// tcpKeepAlive enables SO_KEEPALIVE on tunnel sockets so the carrier/NAT/
	// WebSocket path stays mapped at the TCP layer (OpenSSH sets TCPKeepAlive
	// by default; we did not, which contributed to idle drops).
	tcpKeepAlive = 15 * time.Second
)

// fallbackServers are tried only after carrier DNS. A carrier "bug host"
// (e.g. www.bc.gam) will NOT resolve on these public servers — only the
// carrier's own DNS can answer for it — but they remain useful for real
// hostnames.
var fallbackServers = []string{"8.8.8.8:53", "1.1.1.1:53"}

var (
	defaultResolver *Resolver
	once            sync.Once
)

// Resolver performs Android-aware DNS resolution with caching.
type Resolver struct {
	mu        sync.Mutex
	cache     map[string]cacheEntry
	servers   []string
	serversAt time.Time
}

type cacheEntry struct {
	ips     []net.IP
	expires time.Time
}

// New returns the shared resolver instance. The cache is process-wide so it
// survives across reconnect attempts in the tunnel retry loop.
func New() *Resolver {
	once.Do(func() {
		defaultResolver = &Resolver{cache: make(map[string]cacheEntry)}
	})
	return defaultResolver
}

// DialContext resolves the host portion of addr using Android carrier DNS and
// dials the first reachable resolved IPv4 address. If host is already an IP
// literal it is dialed directly.
func (r *Resolver) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("dnsx: split %q: %w", addr, err)
	}

	// Already an IP literal — no resolution needed.
	if net.ParseIP(host) != nil {
		d := net.Dialer{KeepAlive: tcpKeepAlive}
		return d.DialContext(ctx, network, addr)
	}

	ips, err := r.Lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("dnsx: resolve %q: %w", host, err)
	}

	d := net.Dialer{KeepAlive: tcpKeepAlive}
	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.String(), port)
		conn, derr := d.DialContext(ctx, network, target)
		if derr == nil {
			log.Printf("[dnsx] %s -> %s (dial %s)", host, ip, target)
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = errors.New("no addresses")
	}
	return nil, lastErr
}

// Lookup resolves host to one or more IPv4 addresses using carrier DNS, with
// cache and public fallback.
func (r *Resolver) Lookup(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(host, ".")

	// Cache hit?
	r.mu.Lock()
	if e, ok := r.cache[host]; ok && time.Now().Before(e.expires) {
		ips := e.ips
		r.mu.Unlock()
		return ips, nil
	}
	r.mu.Unlock()

	servers := r.dnsServers()
	var lastErr error
	for _, srv := range servers {
		ips, err := queryA(ctx, srv, host)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ips) > 0 {
			log.Printf("[dnsx] resolved %s via %s -> %v", host, srv, ips)
			r.mu.Lock()
			r.cache[host] = cacheEntry{ips: ips, expires: time.Now().Add(cacheTTL)}
			r.mu.Unlock()
			return ips, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no A records for %s", host)
	}
	return nil, lastErr
}

// dnsServers returns the ordered list of DNS servers to try: carrier DNS first
// (from getprop), then public fallbacks. The result is cached briefly.
func (r *Resolver) dnsServers() []string {
	r.mu.Lock()
	if len(r.servers) > 0 && time.Now().Before(r.serversAt.Add(serversMaxAge)) {
		s := r.servers
		r.mu.Unlock()
		return s
	}
	r.mu.Unlock()

	servers := append(readCarrierDNS(), fallbackServers...)

	// De-duplicate, preserving order.
	seen := make(map[string]bool, len(servers))
	uniq := make([]string, 0, len(servers))
	for _, s := range servers {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}

	r.mu.Lock()
	r.servers = uniq
	r.serversAt = time.Now()
	r.mu.Unlock()
	return uniq
}

// readCarrierDNS parses `getprop` output for any *.dns* properties holding an
// IPv4 address, returning them as "ip:53". IPv6 entries are skipped because
// IPv6 is typically disabled while the tunnel is active.
func readCarrierDNS() []string {
	out, err := exec.Command("/system/bin/getprop").Output()
	if err != nil {
		return nil
	}
	var servers []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		// Property dump format: [net.dns1]: [8.8.8.8]
		lb := strings.IndexByte(line, '[')
		rb := strings.IndexByte(line, ']')
		if lb < 0 || rb <= lb {
			continue
		}
		key := line[lb+1 : rb]
		if !strings.Contains(key, "dns") {
			continue
		}
		rest := line[rb+1:]
		vb := strings.IndexByte(rest, '[')
		ve := strings.IndexByte(rest, ']')
		if vb < 0 || ve <= vb {
			continue
		}
		val := strings.TrimSpace(rest[vb+1 : ve])
		ip := net.ParseIP(val)
		if ip == nil || ip.To4() == nil {
			continue // empty or IPv6
		}
		s := net.JoinHostPort(val, "53")
		if !seen[s] {
			seen[s] = true
			servers = append(servers, s)
		}
	}
	return servers
}

// queryA performs a single A-record query over UDP against server (host:port).
func queryA(ctx context.Context, server, host string) ([]net.IP, error) {
	id := uint16(rand.Uint32())
	msg, err := buildQuery(id, host)
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	deadline := time.Now().Add(queryTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}

	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return parseA(buf[:n], id)
}

// buildQuery builds a DNS query message for the A record of host.
func buildQuery(id uint16, host string) ([]byte, error) {
	b := make([]byte, 0, 32+len(host))
	// Header: ID, flags (RD=1), QDCOUNT=1, others 0
	b = append(b, byte(id>>8), byte(id))
	b = append(b, 0x01, 0x00)
	b = append(b, 0x00, 0x01)
	b = append(b, 0x00, 0x00)
	b = append(b, 0x00, 0x00)
	b = append(b, 0x00, 0x00)
	// QNAME
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			continue
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("label too long: %q", label)
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0x00)       // root label
	b = append(b, 0x00, 0x01) // QTYPE = A
	b = append(b, 0x00, 0x01) // QCLASS = IN
	return b, nil
}

// parseA parses a DNS response and returns its A records.
func parseA(msg []byte, wantID uint16) ([]net.IP, error) {
	if len(msg) < 12 {
		return nil, errors.New("dns: short response")
	}
	if id := uint16(msg[0])<<8 | uint16(msg[1]); id != wantID {
		return nil, errors.New("dns: id mismatch")
	}
	if rcode := msg[3] & 0x0f; rcode != 0 {
		return nil, fmt.Errorf("dns: rcode %d", rcode)
	}
	qd := int(uint16(msg[4])<<8 | uint16(msg[5]))
	an := int(uint16(msg[6])<<8 | uint16(msg[7]))

	off := 12
	var err error
	for i := 0; i < qd; i++ {
		off, err = skipName(msg, off)
		if err != nil {
			return nil, err
		}
		off += 4 // QTYPE + QCLASS
		if off > len(msg) {
			return nil, errors.New("dns: truncated question")
		}
	}

	var ips []net.IP
	for i := 0; i < an; i++ {
		off, err = skipName(msg, off)
		if err != nil {
			return nil, err
		}
		if off+10 > len(msg) {
			break
		}
		typ := uint16(msg[off])<<8 | uint16(msg[off+1])
		rdlen := int(uint16(msg[off+8])<<8 | uint16(msg[off+9]))
		off += 10
		if off+rdlen > len(msg) {
			break
		}
		if typ == 1 && rdlen == 4 { // A record
			ips = append(ips, net.IPv4(msg[off], msg[off+1], msg[off+2], msg[off+3]))
		}
		off += rdlen
	}
	if len(ips) == 0 {
		return nil, errors.New("dns: no A records")
	}
	return ips, nil
}

// skipName advances past a (possibly compressed) DNS name, returning the offset
// just after it.
func skipName(msg []byte, off int) (int, error) {
	for {
		if off >= len(msg) {
			return 0, errors.New("dns: name overflow")
		}
		b := msg[off]
		switch {
		case b == 0:
			return off + 1, nil
		case b&0xc0 == 0xc0:
			// compression pointer — the name ends after the 2-byte pointer
			if off+2 > len(msg) {
				return 0, errors.New("dns: bad pointer")
			}
			return off + 2, nil
		default:
			off += int(b) + 1
		}
	}
}
