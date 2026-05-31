// Package ssh provides SSH transport modes for carrier-bypass tunneling.
package ssh

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	xssh "golang.org/x/crypto/ssh"

	"github.com/GoodyOG/SSHCustom_Magisk/internal/dnsx"
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

// Client wraps an active SSH client connection.
type Client struct {
	sshConn *xssh.Client
	cfg     ConnectConfig
	ctx     context.Context
	cancel  context.CancelFunc
	closed  chan struct{}

	activeConns int32 // number of in-flight proxied connections
}

// AddConn / RemoveConn track in-flight proxied connections for the status UI.
func (c *Client) AddConn()    { atomic.AddInt32(&c.activeConns, 1) }
func (c *Client) RemoveConn() { atomic.AddInt32(&c.activeConns, -1) }

// ActiveConns returns the current number of in-flight proxied connections.
func (c *Client) ActiveConns() int { return int(atomic.LoadInt32(&c.activeConns)) }

// Dial establishes an SSH connection using the configured transport mode.
func Dial(ctx context.Context, cfg ConnectConfig) (*Client, error) {
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
		cfg:     cfg,
		ctx:     cliCtx,
		cancel:  cancel,
		closed:  make(chan struct{}),
	}

	// SSH keepalive sender — keeps the WebSocket/bug-host path alive.
	go c.keepAlive(keepAliveInterval, keepAliveMax)

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
		conn, err := dnsx.New().DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		// Optional payload injection before SSH. The payload is sent verbatim
		// (after token substitution); any HTTP acknowledgement the server returns
		// is then drained so the SSH handshake starts on the raw tunnel.
		if cfg.PayloadEnabled && cfg.Payload != "" {
			p := substitutePayload(cfg.Payload, cfg.Host, cfg.Port)
			if _, err := conn.Write([]byte(p)); err != nil {
				conn.Close()
				return nil, fmt.Errorf("payload write: %w", err)
			}
			log.Printf("[payload] sent %d bytes to %s", len(p), addr)
			// Wait for the real SSH banner, discarding any/all HTTP acks first
			// (HTTP Injector / HTTP Custom behaviour). Far more robust than
			// trusting a single HTTP status.
			out, derr := awaitSSHBanner(ctx, conn)
			if derr != nil {
				conn.Close()
				return nil, derr
			}
			return out, nil
		}
		return conn, nil

	case ModeSNI:
		raw, err := dnsx.New().DialContext(ctx, "tcp", addr)
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
		raw, err := dnsx.New().DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("http proxy dial: %w", err)
		}

		connectReq := buildCONNECT(cfg, addr)
		if _, err := raw.Write([]byte(connectReq)); err != nil {
			raw.Close()
			return nil, fmt.Errorf("proxy CONNECT write: %w", err)
		}
		// Drain the proxy/CDN acknowledgement (CONNECT 200, WebSocket 101, etc.)
		// before starting TLS. Status-agnostic — the TLS/SSH handshake is the
		// real success test.
		raw = drainPayloadResponse(ctx, raw)

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

// payloadDrainTimeout bounds how long we wait for the server's HTTP
// acknowledgement after sending an injection payload.
const payloadDrainTimeout = 12 * time.Second

// drainPayloadResponse consumes any HTTP acknowledgement the server/CDN sends
// after an injected payload — a CONNECT 200, a WebSocket 101 Switching
// Protocols, a GET host-injection response, or several of them — and returns a
// net.Conn that replays any tunnel bytes already read past the headers before
// delegating to the socket. It is deliberately generic and status-agnostic so
// that ANY payload (HTTP Injector / HTTP Custom style) works; the subsequent
// SSH/TLS handshake is the real success test. If the server sends no HTTP
// response at all (a raw prefix payload), nothing is consumed.
//
// This must NOT block waiting for data the client is expected to send next
// (e.g. a TLS ClientHello in proxy mode), so after the first response block we
// only keep consuming blocks that are already buffered.
func drainPayloadResponse(ctx context.Context, conn net.Conn) net.Conn {
	deadline := time.Now().Add(payloadDrainTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return conn
	}
	defer conn.SetReadDeadline(time.Time{})

	br := bufio.NewReader(conn)

	// Decide whether the server actually replied with an HTTP response.
	peek, err := br.Peek(5)
	if err != nil || string(peek) != "HTTP/" {
		// Raw-prefix payload or read timeout/EOF — replay anything buffered.
		return wrapBuffered(conn, br)
	}

	first := true
	for {
		if !first {
			// Only consume an additional response if it already arrived in the
			// same buffer — never block here.
			if br.Buffered() < 5 {
				break
			}
			ahead, _ := br.Peek(5)
			if string(ahead) != "HTTP/" {
				break
			}
		}
		status, herr := readHTTPHeaderBlock(br)
		if status != "" {
			log.Printf("[payload] server response: %s", status)
		}
		if herr != nil {
			break
		}
		first = false
	}

	return wrapBuffered(conn, br)
}

// readHTTPHeaderBlock reads a status line plus headers up to the blank CRLF
// line, returning the trimmed status line.
func readHTTPHeaderBlock(br *bufio.Reader) (string, error) {
	status := ""
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			if status == "" {
				status = trimmed
			}
			if trimmed == "" {
				return status, nil // end of header block
			}
		}
		if err != nil {
			return status, err
		}
	}
}

// awaitSSHBanner is used after an injection payload in direct mode. The server
// (or WebSocket/CDN bridge) may reply with one or more HTTP responses
// (101 Switching Protocols, 200, 302, …) which are carrier-fooling noise; we
// discard them all and keep reading until the real SSH identification banner
// ("SSH-…") appears, then hand the socket — with the banner preserved — to the
// SSH handshake. This mirrors HTTP Injector / HTTP Custom behaviour and is far
// more robust than trusting a single HTTP status. If no banner arrives before
// the deadline (or the server closes after a redirect), it returns a clear
// error instead of letting the SSH handshake fail with a cryptic EOF.
func awaitSSHBanner(ctx context.Context, conn net.Conn) (net.Conn, error) {
	deadline := time.Now().Add(payloadDrainTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)
	defer conn.SetReadDeadline(time.Time{})

	br := bufio.NewReader(conn)
	var lastStatus string
	for {
		peek, err := br.Peek(4)
		if err != nil {
			if lastStatus != "" {
				return nil, fmt.Errorf("server rejected upgrade (%s) — no SSH banner", lastStatus)
			}
			return nil, fmt.Errorf("payload response: %w", err)
		}
		switch string(peek) {
		case "SSH-":
			// Real SSH banner — hand off with it preserved.
			return wrapBuffered(conn, br), nil
		case "HTTP":
			status, herr := readHTTPHeaderBlock(br)
			if status != "" {
				lastStatus = status
				log.Printf("[payload] server response: %s", status)
			}
			if herr != nil {
				return nil, fmt.Errorf("server response %q ended early: %w", status, herr)
			}
			// keep reading — the SSH banner should follow
		default:
			// Neither HTTP nor SSH: assume a raw-prefix bridge; hand off as-is.
			return wrapBuffered(conn, br), nil
		}
	}
}

// wrapBuffered returns a net.Conn that first replays any bytes already buffered
// in br (tunnel data read past the HTTP headers) before delegating to conn.
func wrapBuffered(conn net.Conn, br *bufio.Reader) net.Conn {
	n := br.Buffered()
	if n <= 0 {
		return conn
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return conn
	}
	return &prefixConn{Conn: conn, prefix: buf}
}

// prefixConn replays a buffered prefix before reading from the underlying conn.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}



// DialTCP opens a direct SSH tunnel to the given destination.
// Uses sshConn.DialContext which correctly sets up direct-tcpip with RFC 4254 data.
func (c *Client) DialTCP(ctx context.Context, network, addr string) (net.Conn, error) {
	return c.sshConn.DialContext(ctx, network, addr)
}

// Close shuts down the SSH client.
func (c *Client) Close() {
	c.cancel()
	c.sshConn.Close()
}

// Wait blocks until the SSH connection is closed (from either side).
func (c *Client) Wait() error {
	return c.sshConn.Wait()
}

