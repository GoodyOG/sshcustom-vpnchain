// Package ssh provides SSH transport modes and channel pool management.
package ssh

import (
	"bytes"
	"context"
	"crypto/tls"
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
	ModeDirect        TransportMode = "direct"
	ModeSNI           TransportMode = "sni"
	ModeSNIHTTPProxy  TransportMode = "sni_http_proxy"
)

// ConnectConfig holds everything needed to dial the SSH server.
type ConnectConfig struct {
	Host          string
	Port          int
	User          string
	Password      string
	Mode          TransportMode
	SNIHost       string
	HTTPProxyHost string
	HTTPProxyPort int
	PayloadEnabled bool
	Payload       string
	ConnectTimeout time.Duration
	KeepAlive      time.Duration
}

// Client wraps an active SSH client connection plus a channel pool.
type Client struct {
	mu      sync.Mutex
	sshConn *xssh.Client
	pool    chan xssh.Channel // pre-warmed channels
	poolSz  int
	cfg     ConnectConfig
	ctx     context.Context
	cancel  context.CancelFunc
}

// Dial establishes an SSH connection using the configured transport mode.
func Dial(ctx context.Context, cfg ConnectConfig, poolSize int) (*Client, error) {
	tcpConn, err := dialTransport(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("transport dial: %w", err)
	}

	sshCfg := &xssh.ClientConfig{
		User:            cfg.User,
		Auth:            []xssh.AuthMethod{xssh.Password(cfg.Password)},
		HostKeyCallback: xssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         cfg.ConnectTimeout,
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
		pool:    make(chan xssh.Channel, poolSize),
		poolSz:  poolSize,
		cfg:     cfg,
		ctx:     cliCtx,
		cancel:  cancel,
	}

	// Start pool filler
	go c.fillPool()

	return c, nil
}

// dialTransport creates the raw TCP (or TLS-wrapped) connection to the server.
func dialTransport(ctx context.Context, cfg ConnectConfig) (net.Conn, error) {
	timeout := cfg.ConnectTimeout
	if timeout == 0 {
		timeout = 25 * time.Second
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	switch cfg.Mode {
	case ModeDirect:
		d := &net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, "tcp", addr)

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

		// CONNECT tunnel through HTTP proxy
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

		// Now wrap in TLS with SNI
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
		// Substitute variables in the payload template
		p := cfg.Payload
		p = strings.ReplaceAll(p, "[host]", cfg.Host)
		p = strings.ReplaceAll(p, "[port]", fmt.Sprintf("%d", cfg.Port))
		p = strings.ReplaceAll(p, "[crlf]", "\r\n")
		p = strings.ReplaceAll(p, "[cr]", "\r")
		p = strings.ReplaceAll(p, "[lf]", "\n")
		return p
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\n", targetAddr)
	fmt.Fprintf(&b, "Host: %s\r\n", targetAddr)
	fmt.Fprintf(&b, "Proxy-Connection: Keep-Alive\r\n")
	fmt.Fprintf(&b, "\r\n")
	return b.String()
}

// fillPool pre-warms SSH channels and keeps the pool at capacity.
func (c *Client) fillPool() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		if len(c.pool) >= c.poolSz {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		ch, reqs, err := c.sshConn.OpenChannel("direct-tcpip", nil)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		go xssh.DiscardRequests(reqs)
		select {
		case c.pool <- ch:
		case <-c.ctx.Done():
			ch.Close()
			return
		}
	}
}

// OpenChannel returns a pre-warmed SSH channel or opens a new one.
func (c *Client) OpenChannel(channelType string, extraData []byte) (net.Conn, error) {
	select {
	case ch := <-c.pool:
		// replenish pool in background
		go func() {
			if nc, reqs, err := c.sshConn.OpenChannel(channelType, extraData); err == nil {
				go xssh.DiscardRequests(reqs)
				select {
				case c.pool <- nc:
				default:
					nc.Close()
				}
			}
		}()
		return &channelConn{ch}, nil
	default:
		ch, reqs, err := c.sshConn.OpenChannel(channelType, extraData)
		if err != nil {
			return nil, err
		}
		go xssh.DiscardRequests(reqs)
		return &channelConn{ch}, nil
	}
}

// Dial opens a direct-tcpip channel to the given destination.
func (c *Client) DialTCP(ctx context.Context, network, addr string) (net.Conn, error) {
	return c.sshConn.DialContext(ctx, network, addr)
}

// Close shuts down the SSH client and pool.
func (c *Client) Close() {
	c.cancel()
	c.sshConn.Close()
	// drain pool
	for {
		select {
		case ch := <-c.pool:
			ch.Close()
		default:
			return
		}
	}
}

// PoolStats returns (total, available) channel counts.
func (c *Client) PoolStats() (int, int) {
	return c.poolSz, len(c.pool)
}

// channelConn adapts xssh.Channel to net.Conn.
type channelConn struct{ xssh.Channel }

func (c *channelConn) LocalAddr() net.Addr             { return addr("ssh-channel") }
func (c *channelConn) RemoteAddr() net.Addr            { return addr("ssh-channel") }
func (c *channelConn) SetDeadline(t time.Time) error      { return nil }
func (c *channelConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *channelConn) SetWriteDeadline(t time.Time) error { return nil }

type addr string

func (a addr) Network() string { return "ssh" }
func (a addr) String() string  { return string(a) }
