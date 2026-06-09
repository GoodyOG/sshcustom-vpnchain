package main

// engine.go — the lean single-connection SSH engine, ported from
// sshcustom-vpnchain. One SSH connection carries every proxied stream via
// on-demand direct-tcpip channels (RFC 4254 multiplexing). Local listeners
// (SOCKS5, transparent REDIRECT, DNS-through-tunnel) and the iptables rules
// are brought up ONCE on first connect and KEPT UP across transparent SSH
// reconnects — a brief SSH drop stalls apps ~1s with no traffic leak
// (fail-closed), exactly like a VpnService whose tun stays up.
//
// This replaces the old multi-connection SSHPool: no pool, no per-connection
// snapshot bookkeeping, no retained buffer pool. That is what keeps idle RAM
// near ~13 MB and working RAM ~30–40 MB.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

// dnsForwardPort is the loopback UDP port the DNS-through-tunnel forwarder
// listens on. iptables redirects device UDP:53 here (skipping uid 0). Matches
// vpnchain's fixed 5353.
const dnsForwardPort = 5353

// dnsUpstream is the DNS server queried (as TCP) through the SSH tunnel.
const dnsUpstream = "8.8.8.8:53"

// tunClient wraps a single authenticated SSH client with an in-flight
// connection counter and a keepalive goroutine that detects a dead carrier
// link (so Wait() returns promptly and the loop reconnects).
type tunClient struct {
	ssh    *xssh.Client
	ctx    context.Context
	cancel context.CancelFunc
	active int32
	dead   atomic.Bool
}

func newTunClient(parent context.Context, sc *xssh.Client, keepaliveSec int) *tunClient {
	ctx, cancel := context.WithCancel(parent)
	c := &tunClient{ssh: sc, ctx: ctx, cancel: cancel}
	go c.keepAlive(keepaliveSec)
	return c
}

// keepAlive sends SSH keepalives and force-closes the connection after a few
// consecutive misses, which makes Wait() return and triggers a reconnect.
// Each keepalive is wrapped in a 5 s timeout goroutine so a congested or
// dead TCP link is detected quickly even when writes are blocked.
func (c *tunClient) keepAlive(sec int) {
	if sec <= 0 {
		sec = 15
	}
	t := time.NewTicker(time.Duration(sec) * time.Second)
	defer t.Stop()
	missed := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			done := make(chan error, 1)
			go func() {
				_, _, err := c.ssh.SendRequest("keepalive@openssh.com", true, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					missed++
				if missed >= 2 {
					c.MarkDead()
					return
				}
			} else {
				missed = 0
			}
			case <-time.After(5 * time.Second):
			// Write blocked for >5 s — link is congested or dead
			missed++
			if missed >= 2 {
				c.MarkDead()
				return
			}
			}
		}
	}
}

// DialTCP opens an on-demand direct-tcpip channel to addr through the SSH
// server. The server resolves hostnames, so addr may be host:port or ip:port.
func (c *tunClient) DialTCP(ctx context.Context, network, addr string) (net.Conn, error) {
	return c.ssh.DialContext(ctx, network, addr)
}

func (c *tunClient) add()        { atomic.AddInt32(&c.active, 1) }
func (c *tunClient) remove()     { atomic.AddInt32(&c.active, -1) }
func (c *tunClient) Active() int   { return int(atomic.LoadInt32(&c.active)) }
func (c *tunClient) IsDead() bool { return c.dead.Load() }
func (c *tunClient) MarkDead() {
	if c.dead.CompareAndSwap(false, true) {
		log.Printf("[tunnel] marking SSH client dead — triggering reconnect")
	}
	c.Close()
}
func (c *tunClient) Close() { c.cancel(); _ = c.ssh.Close() }
func (c *tunClient) Wait() error { return c.ssh.Wait() }

// tunnelLoop is the connection manager: connect → bring listeners + iptables
// up once → wait for the client to die → reconnect (keeping routing up).
func tunnelLoop(ctx context.Context, getCfg func() Config, sp Profile, st *State, clientPtr *atomic.Pointer[tunClient], workDir string) {
	const (
		baseDelay = 1 * time.Second
		maxDelay  = 30 * time.Second
	)
	curClient := func() *tunClient { return clientPtr.Load() }

	var (
		listenerCancel context.CancelFunc
		iptablesUp     bool
	)
	teardown := func() {
		if listenerCancel != nil {
			listenerCancel()
			listenerCancel = nil
		}
		if ctx.Err() != nil {
			clientPtr.Store(nil)
			return
		}
		if iptablesUp {
			iptablesScript := filepath.Join(workDir, "ssh.iptables")
			exec.Command("/system/bin/sh", iptablesScript, "disable").Run()
			iptablesUp = false
		}
		clientPtr.Store(nil)
	}
	defer teardown()

	var delay time.Duration
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		cfg := getCfg()
		ri := routeInfo()
		st.set(func() {
			st.NetworkOnline = ri.Online
			st.DefaultRoute = ri.Raw
			st.Interface = ri.Iface
			st.Gateway = ri.Gw
			st.SourceIP = ri.Src
		})
		if !ri.Online {
			st.set(func() {
				st.State = "PAUSED_NO_NETWORK"
				st.Connected = false
				st.SSHAuthenticated = false
				st.TransportReady = false
				st.PoolHealthy = 0
				st.LastEvent = "network offline; reconnect paused"
			})
			delay = 5 * time.Second
			continue
		}

		st.set(func() {
			st.State = "CONNECTING_SSH"
			st.LastEvent = "opening transport and authenticating SSH"
			st.LastError = ""
		})
		log.Printf("[tunnel] connecting %s:%d mode=%s", sp.SSH.Host, sp.SSH.Port, sp.Transport.Mode)

		client, res, err := attemptSSHAuth(ctx, cfg, sp)
		if err != nil {
			st.set(func() {
				st.State = "RETRY_BACKOFF"
				st.Connected = false
				st.SSHAuthenticated = false
				st.TransportReady = false
				st.PoolHealthy = 0
				st.LastError = err.Error()
				st.LastEvent = "SSH auth failed; retrying"
				st.RemoteBanner = res.Banner
				st.HTTPStatuses = res.Statuses
				st.ResolvedDial = res.ResolvedDial
				st.ResolverMethod = res.ResolverMethod
				st.ResolvedIPs = res.ResolvedIPs
			})
			log.Printf("[tunnel] connect failed: %v", err)
			// Keep routing rules up indefinitely while reconnecting — tearing
			// them down would silently expose traffic to the raw carrier.
			// A user who loses signal for >15 min should stay fail-closed, not
			// suddenly have unprotected traffic. Rules are only cleaned on an
			// explicit stop/shutdown.
			delay = nextDelay(delay, baseDelay, maxDelay)
			continue
		}

		delay = baseDelay
		keepaliveSec := secondsDefault(cfg.Performance.KeepAliveSec, 15)
		tc := newTunClient(ctx, client, keepaliveSec)
		clientPtr.Store(tc)
		st.set(func() {
			st.State = "CONNECTED"
			st.Connected = true
			st.SSHAuthenticated = true
			st.TransportReady = true
			st.LastError = ""
			st.LastEvent = "SSH connected; SOCKS5 + transparent TCP + DNS-through-tunnel active"
			st.RemoteBanner = res.Banner
			st.HTTPStatuses = res.Statuses
			st.ResolvedDial = res.ResolvedDial
			st.ResolverMethod = res.ResolverMethod
			st.ResolvedIPs = res.ResolvedIPs
			st.PoolSize = 1
			st.PoolHealthy = 1
			st.PoolReconnecting = 0
			st.PoolStreams = 0
		})
		log.Printf("[tunnel] connected: banner=%q statuses=%v", res.Banner, res.Statuses)

		// Bring listeners + iptables up exactly once. They keep running and
		// pick up the new client (via curClient) across reconnects.
		if listenerCancel == nil {
			lctx, lcancel := context.WithCancel(ctx)
			listenerCancel = lcancel
			startListeners(lctx, cfg, curClient, st)
			time.Sleep(150 * time.Millisecond)
			if cfg.TransparentProxy.Enabled {
				// Write config for shell script
				envPath := filepath.Join(workDir, "run", "iptables.env")
				env := fmt.Sprintf("run_dir=%s\nTCP_PORT=%d\nUDP_PORT=%d\nDNS_PORT=%d\nAPI_PORT=%d\nSOCKS_PORT=%d\nBYPASS_IP=%s\nHOTSPOT=%v\n",
					filepath.Join(workDir, "run"),
					secondsDefault(cfg.TransparentProxy.TCPPort, 10810),
					secondsDefault(cfg.TransparentProxy.UDPPort, 10811),
					5353,
					cfg.API.Port,
					cfg.LocalProxy.SocksPort,
					strings.Join(res.ResolvedIPs, ","),
					cfg.Hotspot.Enabled && cfg.Hotspot.TCP)
				os.WriteFile(envPath, []byte(env), 0644)

				iptablesScript := filepath.Join(workDir, "ssh.iptables")
				cmd := exec.Command("/system/bin/sh", iptablesScript, "enable")
				out, err := cmd.CombinedOutput()
				if err != nil {
					log.Printf("[tunnel] iptables script failed: %v — %s", err, strings.TrimSpace(string(out)))
					st.set(func() {
						st.TransparentApplied = false
						st.LastError = "iptables failed: " + err.Error()
					})
				} else {
					iptablesUp = true
					st.set(func() {
						st.TransparentApplied = true
						st.HotspotRunning = cfg.Hotspot.Enabled && cfg.Hotspot.TCP
					})
				}
			}
		}

		// Wait for this client to die, refreshing live stats meanwhile.
		healthTicker := time.NewTicker(2 * time.Second)
		waitDone := make(chan error, 1)
		go func() { waitDone <- tc.Wait() }()
	wait:
		for {
			select {
			case <-ctx.Done():
				healthTicker.Stop()
				tc.Close()
				clientPtr.Store(nil)
				return
			case <-waitDone:
				break wait
			case <-healthTicker.C:
				ri := routeInfo()
				streams := tc.Active()
				cfgNow := getCfg()
				maxStreams := cfgNow.Performance.MaxStreamsPerSSH
				if maxStreams <= 0 {
					maxStreams = 64
				}
				if streams >= maxStreams*4/5 {
					log.Printf("[tunnel] high stream usage: %d/%d (%.0f%%)", streams, maxStreams, float64(streams)/float64(maxStreams)*100)
				}
				st.set(func() {
					st.PoolStreams = streams
					st.PoolMaxStreams = maxStreams
					st.NetworkOnline = ri.Online
					st.DefaultRoute = ri.Raw
					st.Interface = ri.Iface
					st.Gateway = ri.Gw
					st.SourceIP = ri.Src
				})
			}
		}
		healthTicker.Stop()

		waitErr := <-waitDone
		reason := classifyDisconnect(waitErr)
		tc.Close()
		clientPtr.Store(nil)
		st.set(func() {
			st.State = "RECONNECTING"
			st.Connected = false
			st.SSHAuthenticated = false
			st.TransportReady = false
			st.PoolHealthy = 0
			st.PoolReconnecting = 1
			st.PoolStreams = 0
			st.LastEvent = "SSH connection lost; reconnecting (routing kept up)"
		})
		log.Printf("[tunnel] connection lost (%s) — reconnecting (routing kept up)", reason)
		delay = baseDelay
	}
}

// classifyDisconnect categorises the SSH Wait() error into a short label so
// the reconnect log tells the user WHY the connection dropped:
//   - Dropbear/OpenSSH closed it → a disconnect message from the server
//   - network timeout/reset → a carrier-level interruption
//   - keepalive timeout → our keepalive goroutine closed it
//   - the raw error string for anything else
func classifyDisconnect(err error) string {
	if err == nil {
		return "clean close"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "EOF") || strings.Contains(s, "eof"):
		return "remote closed (SSH server)"
	case strings.Contains(s, "timeout") || strings.Contains(s, "i/o timeout"):
		return "network timeout"
	case strings.Contains(s, "reset") || strings.Contains(s, "broken pipe"):
		return "network reset"
	case strings.Contains(s, "use of closed network connection"):
		return "keepalive timeout (connection dead)"
	case strings.Contains(s, "unexpected packet") || strings.Contains(s, "bad record mac"):
		return "transport corruption"
	default:
		return s
	}
}

func nextDelay(cur, base, max time.Duration) time.Duration {
	if cur <= 0 {
		return base
	}
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

// dialStreamWithRetry opens a direct-tcpip channel through the SSH client with
// up to 2 automatic retries for transient server-side failures. Dropbear and
// some OpenSSH configurations have short internal connect timeouts (3–10 s);
// a single stream timeout does not mean the SSH session is dead — it means the
// server couldn't reach that particular target in its window. Retrying almost
// always succeeds on the next attempt. Fail-closed: if the SSH client itself
// is gone (nil), the call returns immediately without retrying.
func dialStreamWithRetry(ctx context.Context, prefix string, cl *tunClient, target string, curClient func() *tunClient) (net.Conn, error) {
	if cl.IsDead() {
		return nil, fmt.Errorf("ssh client is dead")
	}
	remote, err := cl.DialTCP(ctx, "tcp", target)
	if err == nil {
		return remote, nil
	}
	if isTransportDeath(err) {
		cl.MarkDead()
		return nil, err
	}
	if !isStreamRetryable(err) {
		return nil, err
	}
	ncl := curClient()
	if ncl == nil || ncl.IsDead() {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	remote, err = ncl.DialTCP(ctx, "tcp", target)
	if err == nil {
		log.Printf("[%s] stream retry succeeded for %s", prefix, target)
		return remote, nil
	}
	if isTransportDeath(err) {
		ncl.MarkDead()
	}
	return nil, err
}

// isStreamRetryable returns true for errors that indicate a transient
// server-side connection failure, not a broken SSH session or a permanent
// rejection. These are worth retrying because they often resolve within a
// few hundred milliseconds on the next attempt.
func isStreamRetryable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connect failed") ||
		strings.Contains(s, "Connection refused") ||
		strings.Contains(s, "Connection timed out") ||
		strings.Contains(s, "Temporary failure in name resolution") ||
		strings.Contains(s, "no route to host")
}

// isTransportDeath returns true for errors that indicate the SSH transport
// (underlying TCP/TLS connection) is dead — not just a single stream failure.
// When this happens, the tunClient should be marked dead immediately to trigger
// reconnection without waiting for the keepalive goroutine.
//
// IMPORTANT: We do NOT match on "timeout" or "connection timed out" alone.
// SSH channel rejections from the server (ssh: rejected: connect failed
// (Connection timed out)) contain these substrings but the SSH transport is
// still healthy — only the target was unreachable from the VPS. Those are
// stream-level errors, not transport deaths.
//
// "read tcp" is the unambiguous signal: it only appears in Go net.TCPConn.Read
// failures, which means the underlying TCP connection to the SSH server died.
func isTransportDeath(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Unambiguous transport-level error: Go's TCP read failed on the SSH connection
	if strings.Contains(s, "read tcp") {
		return true
	}
	// SSH protocol errors: the SSH session itself is corrupted or closed
	return strings.Contains(s, "EOF") ||
		strings.Contains(s, "eof") ||
		strings.Contains(s, "reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "unexpected packet") ||
		strings.Contains(s, "bad record mac") ||
		strings.Contains(s, "error decoding message") ||
		strings.Contains(s, "packet too large") ||
		strings.Contains(s, "invalid packet length")
}

// startListeners brings up SOCKS5 + transparent (REDIRECT) + DNS forwarder,
// all bound to the CURRENT SSH client via curClient so they survive reconnects.
func startListeners(ctx context.Context, cfg Config, curClient func() *tunClient, st *State) {
	if cfg.LocalProxy.SocksEnabled {
		go serveSOCKS(ctx, cfg, curClient, st)
	}
	if cfg.TransparentProxy.Enabled {
		go serveTransparent(ctx, cfg, curClient, st)
	}
	// DNS-through-tunnel: iptables redirects device UDP:53 → 127.0.0.1:5353,
	// where we proxy each query as TCP DNS through the SSH tunnel. This is what
	// fixes "no internet" in Chrome/YouTube on bug-host networks.
	go func() {
		if err := dnsForwardLoop(ctx, fmt.Sprintf("127.0.0.1:%d", dnsForwardPort), dnsUpstream, curClient); err != nil {
			log.Printf("[dns-forward] %v", err)
		}
	}()
	if cfg.UDPProxy.Enabled {
		go serveTransparentUDP(ctx, cfg, curClient, st)
	}
}

func serveSOCKS(ctx context.Context, cfg Config, curClient func() *tunClient, st *State) {
	addr := socksAddr(cfg)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[socks5] listen %s: %v", addr, err)
		st.set(func() { st.SocksRunning = false; st.LastError = "SOCKS5 listen failed: " + err.Error() })
		return
	}
	st.set(func() { st.SocksRunning = true; st.SocksAddr = addr })
	log.Printf("[socks5] listening on %s", addr)
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				st.set(func() { st.SocksRunning = false })
				return
			default:
				st.set(func() { st.SocksRunning = false })
				return
			}
		}
		go handleSOCKSClient(ctx, c, cfg, curClient)
	}
}

func handleSOCKSClient(ctx context.Context, c net.Conn, cfg Config, curClient func() *tunClient) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	target, err := socks5Handshake(c)
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	tuneTCPConn(c, cfg, false)
	cl := curClient()
	if cl == nil || cl.IsDead() {
		_ = socks5Reply(c, 0x04) // host unreachable (reconnecting; fail-closed)
		return
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 8*time.Second)
	remote, err := dialStreamWithRetry(dialCtx, "socks5", cl, target, curClient)
	dialCancel()
	if err != nil {
		logTunnelOpenError("[socks5]", target, err)
		if isTransportDeath(err) {
			cl.MarkDead()
		}
		_ = socks5Reply(c, 0x05)
		return
	}
	defer remote.Close()
	cl = curClient()
	if cl == nil {
		_ = socks5Reply(c, 0x04)
		return
	}
	cl.add()
	defer cl.remove()
	_ = socks5Reply(c, 0x00)
	bufSize := cfg.Performance.BufferSize
	if bufSize <= 0 {
		bufSize = 128 * 1024
	}
	pipeBoth(c, remote, bufSize, streamIdleTimeout(cfg))
}

func serveTransparent(ctx context.Context, cfg Config, curClient func() *tunClient, st *State) {
	addr := transparentAddr(cfg)
	// Dual-mode: try TPROXY first (IP_TRANSPARENT), fall back to plain TCP
	// for REDIRECT-based interception (no IP_TRANSPARENT needed).
	ln, err := listenTransparentTCP(ctx, addr)
	mode := "TPROXY"
	if err != nil {
		log.Printf("[transparent] TPROXY listener failed: %v — falling back to REDIRECT mode", err)
		mode = "REDIRECT"
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			log.Printf("[transparent] listen %s: %v", addr, err)
			st.set(func() { st.TransparentRunning = false; st.LastError = "transparent listen failed: " + err.Error() })
			return
		}
	}
	st.set(func() { st.TransparentRunning = true; st.TransparentAddr = addr })
	log.Printf("[transparent] listening on %s (%s mode)", addr, mode)
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			// Transient accept errors (e.g. ECONNABORTED) on TPROXY sockets
			// are normal under load. Log and continue; only exit on context cancel.
			if ctx.Err() != nil {
				st.set(func() { st.TransparentRunning = false })
				return
			}
			log.Printf("[transparent] accept error: %v — continuing", err)
			continue
		}
		go handleTransparentClient(ctx, c, cfg, curClient)
	}
}

func handleTransparentClient(ctx context.Context, c net.Conn, cfg Config, curClient func() *tunClient) {
	defer c.Close()
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		log.Printf("[transparent] dropped: not a TCP connection (%T)", c)
		return
	}
	target, err := tproxyDst(tcp)
	if err != nil {
		// TPROXY failed — try REDIRECT (SO_ORIGINAL_DST)
		target, err = originalDst(tcp)
		if err != nil {
			log.Printf("[transparent] dropped: dst failed: %v", err)
			return
		}
	}
	if isLocalOrBlockedTarget(target, cfg) {
		log.Printf("[transparent] dropped: local/blocked target %s", target)
		return
	}
	tuneTCPConn(c, cfg, false)
	cl := curClient()
	if cl == nil || cl.IsDead() {
		if cl == nil {
			log.Printf("[transparent] dropped %s: no SSH client (reconnecting)", target)
		} else {
			log.Printf("[transparent] dropped %s: SSH client dead", target)
		}
		return // reconnecting or dead — drop (fail-closed)
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 8*time.Second)
	remote, err := dialStreamWithRetry(dialCtx, "transparent", cl, target, curClient)
	dialCancel()
	if err != nil {
		logTunnelOpenError("[transparent]", target, err)
		if isTransportDeath(err) {
			cl.MarkDead()
		}
		return
	}
	defer remote.Close()
	cl = curClient()
	if cl == nil {
		return
	}
	cl.add()
	defer cl.remove()
	bufSize := cfg.Performance.BufferSize
	if bufSize <= 0 {
		bufSize = 128 * 1024
	}
	pipeBoth(c, remote, bufSize, streamIdleTimeout(cfg))
}

// dnsForwardLoop runs a UDP listener that proxies DNS queries as TCP DNS
// (RFC 1035 §4.2.2: 2-byte length prefix + payload) through the SSH tunnel.
func dnsForwardLoop(ctx context.Context, listenAddr, upstream string, curClient func() *tunClient) error {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve udp: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	log.Printf("[dns-forward] listening on %s, upstream=%s (via SSH)", listenAddr, upstream)
	go func() { <-ctx.Done(); _ = conn.Close() }()
	defer conn.Close()

	buf := make([]byte, 1500)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return fmt.Errorf("read udp: %w", err)
		}
		if n < 12 {
			continue
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go forwardOneDNSQuery(ctx, conn, src, query, upstream, curClient)
	}
}

func forwardOneDNSQuery(ctx context.Context, listener *net.UDPConn, src *net.UDPAddr, query []byte, upstream string, curClient func() *tunClient) {
	c := curClient()
	// If the tunnel is momentarily reconnecting, wait up to 2s before giving
	// up. The resolver will retry in ~5s, but a brief wait avoids dropping
	// queries that arrive right at the reconnect boundary.
	if c == nil {
		for i := 0; i < 4 && c == nil; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			c = curClient()
		}
	}
	if c == nil {
		return // tunnel still down — drop; resolver will retry
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tcp, err := c.DialTCP(dialCtx, "tcp", upstream)
	if err != nil {
		return
	}
	defer tcp.Close()
	_ = tcp.SetDeadline(time.Now().Add(5 * time.Second))

	// RFC 1035 §4.2.2: TCP DNS is a 2-byte length prefix + payload
	frame := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(query)))
	copy(frame[2:], query)
	if _, err := tcp.Write(frame); err != nil {
		return
	}
	var lenHdr [2]byte
	if _, err := io.ReadFull(tcp, lenHdr[:]); err != nil {
		return
	}
	respLen := int(binary.BigEndian.Uint16(lenHdr[:]))
	// Sanity check: DNS messages must be between 12 bytes (header only) and 65535
	if respLen < 12 || respLen > 65535 {
		return
	}
	resp := make([]byte, respLen)
	if _, err := io.ReadFull(tcp, resp); err != nil {
		return
	}
	if _, err := listener.WriteToUDP(resp, src); err != nil {
		// Best-effort: resolver will retry in ~5s
	}
}

// measureLatency opens a TCP connection to target through the SSH tunnel and
// returns the connect time in milliseconds. Used by the Home "latency" card.
func measureLatency(ctx context.Context, cl *tunClient, target string) (int64, error) {
	if cl == nil {
		return 0, fmt.Errorf("tunnel not connected")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := cl.DialTCP(dialCtx, "tcp", target)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start).Milliseconds(), nil
}

func serveTransparentUDP(ctx context.Context, cfg Config, curClient func() *tunClient, st *State) {
	addr := transparentUDPAddr(cfg)
	ln, err := listenTransparentUDP(ctx, addr)
	if err != nil {
		log.Printf("[udp-tproxy] listen %s: %v", addr, err)
		st.set(func() { st.UDPProxyRunning = false; st.LastError = "UDP TPROXY listen failed: " + err.Error() })
		return
	}
	defer ln.Close()
	st.set(func() { st.UDPProxyRunning = true })
	log.Printf("[udp-tproxy] listening on %s", addr)

	udpgwPort := cfg.UDPProxy.UDPGWPort
	if udpgwPort <= 0 {
		udpgwPort = 7300
	}
	tunnel := newUDPGWTunnel(ctx, curClient, udpgwPort)
	defer tunnel.Close()

	flows := make(map[string]chan []byte)
	var flowsMu sync.Mutex

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		data, src, dst, err := ln.recvFrom()
		if err != nil {
			if ctx.Err() != nil {
				st.set(func() { st.UDPProxyRunning = false })
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("[udp-tproxy] recv: %v", err)
			st.set(func() { st.UDPProxyRunning = false })
			return
		}

		flowKey := src.String()

		flowsMu.Lock()
		ch, exists := flows[flowKey]
		if !exists {
			ch = tunnel.ResponseChan(flowKey)
			flows[flowKey] = ch
			go func(key string) {
				select {
				case <-time.After(udpgwFlowTimeout):
				case <-ctx.Done():
				}
				flowsMu.Lock()
				delete(flows, key)
				flowsMu.Unlock()
				tunnel.ReleaseResponse(key)
			}(flowKey)
		}
		flowsMu.Unlock()

		if err := tunnel.Send(dst.IP, dst.Port, data); err != nil {
			log.Printf("[udp-tproxy] send: %v", err)
			continue
		}

		go func(key string, src *net.UDPAddr, dst *net.UDPAddr, ch chan []byte) {
			select {
			case resp := <-ch:
				ln.conn.WriteToUDP(resp, src)
			case <-time.After(30 * time.Second):
			case <-ctx.Done():
			}
		}(flowKey, src, dst, ch)
	}
}
