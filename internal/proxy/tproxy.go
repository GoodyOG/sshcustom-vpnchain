package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"

	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
)

// ipTransparent is IP_TRANSPARENT (Linux). Setting it on the listening socket
// lets the kernel deliver TPROXY-marked packets to us while preserving the
// original destination as the accepted socket's local address.
const ipTransparent = 19

// TProxyServer intercepts TCP connections delivered by iptables TPROXY (mangle
// table + policy routing). Unlike REDIRECT (which rewrites the destination and
// requires SO_ORIGINAL_DST), TPROXY preserves the original destination, so the
// accepted connection's LocalAddr() is the real target.
type TProxyServer struct {
	Addr    string
	Client  func() *issh.Client // current SSH client; nil during a reconnect gap
	Timeout time.Duration       // per-connection dial timeout; 0 = 25s
}

// ListenAndServe binds with IP_TRANSPARENT and serves until ctx is cancelled.
func (t *TProxyServer) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipTransparent, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	ln, err := lc.Listen(ctx, "tcp", t.Addr)
	if err != nil {
		return fmt.Errorf("tproxy listen %s: %w", t.Addr, err)
	}
	log.Printf("[tproxy] listening on %s", t.Addr)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go t.handle(ctx, conn)
	}
}

func (t *TProxyServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	dialTimeout := t.Timeout
	if dialTimeout == 0 {
		dialTimeout = 25 * time.Second
	}

	// With TPROXY the accepted socket's local address IS the original destination.
	target := conn.LocalAddr().String()

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	cl := t.Client()
	if cl == nil {
		return // tunnel reconnecting — drop this conn (fail-closed)
	}
	remote, err := cl.DialTCP(dialCtx, "tcp", target)
	if err != nil {
		return
	}
	defer remote.Close()

	cl.AddConn()
	defer cl.RemoveConn()

	relay(conn, remote)
}
