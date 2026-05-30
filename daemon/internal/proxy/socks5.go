// Package proxy provides SOCKS5 and transparent proxy listeners.
package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
)

const copyBuf = 128 * 1024

// SOCKS5Server listens on addr and proxies connections through the SSH client.
type SOCKS5Server struct {
	Addr   string
	Client func() *issh.Client // current SSH client; nil during a reconnect gap
}

// ListenAndServe starts the SOCKS5 server.
func (s *SOCKS5Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("socks5 listen %s: %w", s.Addr, err)
	}
	log.Printf("[socks5] listening on %s", s.Addr)
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
		go s.handle(ctx, conn)
	}
}

func (s *SOCKS5Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// SOCKS5 greeting
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 5 {
		return
	}
	nmethods := int(hdr[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	// No authentication required
	conn.Write([]byte{5, 0})

	// Request header
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != 5 || req[1] != 1 {
		conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}

	var host string
	switch req[3] {
	case 1: // IPv4
		addr := make([]byte, 4)
		io.ReadFull(conn, addr)
		host = net.IP(addr).String()
	case 3: // Domain name
		lenb := make([]byte, 1)
		io.ReadFull(conn, lenb)
		dom := make([]byte, lenb[0])
		io.ReadFull(conn, dom)
		host = string(dom)
	case 4: // IPv6
		addr := make([]byte, 16)
		io.ReadFull(conn, addr)
		host = "[" + net.IP(addr).String() + "]"
	default:
		conn.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}

	portBuf := make([]byte, 2)
	io.ReadFull(conn, portBuf)
	port := binary.BigEndian.Uint16(portBuf)
	target := fmt.Sprintf("%s:%d", host, port)

	conn.SetDeadline(time.Time{}) // reset deadline for data transfer

	cl := s.Client()
	if cl == nil {
		conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0}) // host unreachable (reconnecting)
		return
	}
	remote, err := cl.DialTCP(ctx, "tcp", target)
	if err != nil {
		conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()

	cl.AddConn()
	defer cl.RemoveConn()

	// Success reply
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	// Bidirectional relay — no type assertion, works with any net.Conn
	relay(conn, remote)
}

// relay bidirectionally copies between two net.Conn without type assertions.
// Each side gets its own goroutine; we wait for both to finish.
func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, copyBuf)
		io.CopyBuffer(dst, src, buf)
		// Half-close: signal EOF to the other side if possible.
		// Use interface check — SSH channels implement io.Closer but not CloseWrite.
		type halfCloser interface {
			CloseWrite() error
		}
		if hc, ok := dst.(halfCloser); ok {
			hc.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}
