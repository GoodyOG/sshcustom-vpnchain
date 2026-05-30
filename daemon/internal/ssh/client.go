// Package ssh provides SSH transport modes and channel pool management.
package ssh

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

// TransportMode configures how the TCP connection to the SSH server is established.
type TransportMode string

const (
	ModeDirect       TransportMode = "direct"
	ModeSNI          TransportMode = "sni"
	ModeSNIHTTPProxy TransportMode = "sni_http_proxy"
)

// ConnectConfig holds everything needed to dial the SSH server.
type ConnectConfig struct {
	Host           string
	Port           int
	User           string
	Password       string
	Mode           TransportMode
	SNIHost        string
	HTTPProxyHost  string
	HTTPProxyPort  int
	PayloadEnabled bool
	Payload        string
	ConnectTimeout time.Duration
	KeepAliveInterval time.Duration // how often to send SSH keepalives; 0 = 30s
	KeepAliveMax      int           // max missed keepalives before disconnect; 0 = 3
}

// Client wraps an active SSH client connection plus a channel pool.
type Client struct {
	sshConn *xssh.Client
	poolMu  sync.Mutex
	pool    []net.Conn // pre-warmed direct TCP connections via SSH
	poolSz  int
	cfg     ConnectConfig
	ctx     context.Context
	cancel  context.CancelFunc
	closed  chan struct{}
}

// Dial establishes an SSH connection using the configured transport mode.
func Dial(ctx context.Context, cfg ConnectConfig, poolSize int) (*Client, error) {
	timeout := cfg.ConnectTimeout
	if timeout == 0 {
		timeout = 25 * time.Second
	}

	tcpConn, err := dialTransport(ctx, cfg, timeout)
	if err != nil {
		return nil, fmt.Errorf("transport dial: %w", err)
	}

	keepAliveInterval := cfg.KeepAliveInterval
	if keepAliveInterval == 0 {
		keepAliveInterval = 30 * time.Second
	}
	keepAliveMax := cfg.KeepAliveMax
	if keepAliveMax == 0 {
		keepAliveMax = 3
	}

	sshCfg := &xssh.ClientConfig{
		User:            cfg.User,
		Auth:            []xssh.AuthMethod{xssh.Password(cfg.Password)},
		HostKeyCallback: xssh.InsecureIgnoreHostKey(), //nolint:gosec — carrier bypass context
		Timeout:         timeout,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	sshConn, chans, reqs, err := xssh.NewClientConn(tcpConn, addr, sshCfg)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}

	cliCtx, cancel := context.WithCancel(ctx)
	c := &Client{
		sshConn: xssh.NewClient(sshConn, chans, reqs),
		poolSz:  poolSize,
		cfg:     cfg,
		ctx:     cliCtx,
		cancel:  cancel,
		closed:  make(chan struct{}),
	}

	// SSH keepalive sender
	go c.keepAlive(keepAliveInterval, keepAliveMax)

	// Pre-warm the pool
	if poolSize > 0 {
		go c.fillPool()
	}

	return c, nil
}

// keepAlive sends SSH keepalive requests and closes the connection if the server
// stops responding after keepAliveMax missed responses.
func (c *Client) keepAlive(interval time.Duration, maxMissed int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_, _, err := c.sshConn.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				missed++
				if missed >= maxMissed {
					c.sshConn.Close()
					return
				}
			} else {
				missed = 0
			}
		}
	}
}

// dialTransport creates the raw TCP (or TLS-wrapped) connection to the server.
func dialTransport(ctx context.Context, cfg ConnectConfig, timeout time.Duration) (net.Conn, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	switch cfg.Mode {
	case ModeDirect:
		d := &net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		// Optional payload injection before SSH
		if cfg.PayloadEnabled && cfg.Payload != "" {
			p := substitutePayload(cfg.Payload, cfg.Host, cfg.Port)
			if _, err := conn.Write([]byte(p)); err != nil {
				conn.Close()
				return nil, fmt.Errorf("payload write: %w", err)
			}
		}
		return conn, nil

	case ModeSNI:
		d := &net.Dialer{Timeout: timeout}
		raw, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		sni := cfg.SNIHost
		if sni == "" {
			sni = cfg.Host
		}
		tlsCfg := &tls.Config{ServerName: sni, InsecureSkipVerify: true} //nolint:gosec
		tlsConn := tls.Client(raw, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("TLS handshake: %w", err)
		}
		return tlsConn, nil

	case ModeSNIHTTPProxy:
		proxyAddr := fmt.Sprintf("%s:%d", cfg.HTTPProxyHost, cfg.HTTPProxyPort)
		d := &net.Dialer{Timeout: timeout}
		raw, err := d.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("http proxy dial: %w", err)
		}

		connectReq := buildCONNECT(cfg, addr)
		if _, err := raw.Write([]byte(connectReq)); err != nil {
			raw.Close()
			return nil, fmt.Errorf("proxy CONNECT write: %w", err)
		}

		buf := make([]byte, 4096)
		n, err := raw.Read(buf)
		if err != nil {
			raw.Close()
			return nil, fmt.Errorf("proxy CONNECT response: %w", err)
		}
		resp := string(buf[:n])
		if !strings.Contains(resp, "200") {
			raw.Close()
			return nil, fmt.Errorf("proxy CONNECT rejected: %s", strings.SplitN(resp, "\r\n", 2)[0])
		}

		sni := cfg.SNIHost
		if sni == "" {
			sni = cfg.Host
		}
		tlsCfg := &tls.Config{ServerName: sni, InsecureSkipVerify: true} //nolint:gosec
		tlsConn := tls.Client(raw, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("TLS handshake: %w", err)
		}
		return tlsConn, nil

	default:
		return nil, fmt.Errorf("unknown transport mode: %s", cfg.Mode)
	}
}

// buildCONNECT assembles the HTTP CONNECT request, with optional payload injection.
func buildCONNECT(cfg ConnectConfig, targetAddr string) string {
	if cfg.PayloadEnabled && cfg.Payload != "" {
		return substitutePayload(cfg.Payload, cfg.Host, cfg.Port)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\n", targetAddr)
	fmt.Fprintf(&b, "Host: %s\r\n", targetAddr)
	fmt.Fprintf(&b, "Proxy-Connection: Keep-Alive\r\n")
	fmt.Fprintf(&b, "\r\n")
	return b.String()
}

// substitutePayload replaces template variables in a raw payload string.
func substitutePayload(payload, host string, port int) string {
	p := payload
	p = strings.ReplaceAll(p, "[host]", host)
	p = strings.ReplaceAll(p, "[port]", fmt.Sprintf("%d", port))
	p = strings.ReplaceAll(p, "[crlf]", "\r\n")
	p = strings.ReplaceAll(p, "[cr]", "\r")
	p = strings.ReplaceAll(p, "[lf]", "\n")
	return p
}

// fillPool pre-warms direct SSH-tunnelled TCP connections to a dummy address
// so the goroutines and SSH channel state are ready. Real connections use DialTCP
// which opens fresh channels per destination — the pool here primes the SSH
// multiplexer to avoid first-connection negotiation overhead.
func (c *Client) fillPool() {
	// We warm the pool by opening direct-tcpip channels with a loopback probe.
	// These are cheap to open and test the SSH channel round-trip.
	// We keep them as pre-opened net.Conn and return them from the pool.
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.poolMu.Lock()
		size := len(c.pool)
		c.poolMu.Unlock()

		if size >= c.poolSz {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Open a direct-tcpip channel with proper extra data per RFC 4254 §7.2
		// Destination: 127.0.0.1:7 (discard port — only used to warm channel)
		extraData := encodeDirect("127.0.0.1", 7, "127.0.0.1", 0)
		ch, reqs, err := c.sshConn.OpenChannel("direct-tcpip", extraData)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		go xssh.DiscardRequests(reqs)
		conn := &channelConn{ch}

		c.poolMu.Lock()
		if len(c.pool) < c.poolSz {
			c.pool = append(c.pool, conn)
		} else {
			conn.Close()
		}
		c.poolMu.Unlock()
	}
}

// encodeDirect encodes the extra-data for a direct-tcpip channel per RFC 4254 §7.2.
func encodeDirect(dstHost string, dstPort uint32, srcHost string, srcPort uint32) []byte {
	// string dstHost, uint32 dstPort, string srcHost, uint32 srcPort
	encStr := func(s string) []byte {
		b := make([]byte, 4+len(s))
		binary.BigEndian.PutUint32(b, uint32(len(s)))
		copy(b[4:], s)
		return b
	}
	encU32 := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, v)
		return b
	}
	var out []byte
	out = append(out, encStr(dstHost)...)
	out = append(out, encU32(dstPort)...)
	out = append(out, encStr(srcHost)...)
	out = append(out, encU32(srcPort)...)
	return out
}

// DialTCP opens a direct SSH tunnel to the given destination.
// Uses sshConn.DialContext which correctly sets up direct-tcpip with RFC 4254 data.
func (c *Client) DialTCP(ctx context.Context, network, addr string) (net.Conn, error) {
	return c.sshConn.DialContext(ctx, network, addr)
}

// Close shuts down the SSH client and drains the pool.
func (c *Client) Close() {
	c.cancel()
	c.sshConn.Close()
	c.poolMu.Lock()
	for _, conn := range c.pool {
		conn.Close()
	}
	c.pool = nil
	c.poolMu.Unlock()
}

// PoolStats returns (capacity, current size) of the warm channel pool.
func (c *Client) PoolStats() (int, int) {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()
	return c.poolSz, len(c.pool)
}

// Wait blocks until the SSH connection is closed (from either side).
func (c *Client) Wait() error {
	return c.sshConn.Wait()
}

// channelConn adapts xssh.Channel to net.Conn.
type channelConn struct{ xssh.Channel }

func (c *channelConn) LocalAddr() net.Addr              { return sshAddr("local") }
func (c *channelConn) RemoteAddr() net.Addr             { return sshAddr("remote") }
func (c *channelConn) SetDeadline(t time.Time) error      { return nil }
func (c *channelConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *channelConn) SetWriteDeadline(t time.Time) error { return nil }

// CloseWrite signals EOF to the remote side of the channel.
func (c *channelConn) CloseWrite() error {
	return c.Channel.CloseWrite()
}

type sshAddr string

func (a sshAddr) Network() string { return "ssh" }
func (a sshAddr) String() string  { return string(a) }
