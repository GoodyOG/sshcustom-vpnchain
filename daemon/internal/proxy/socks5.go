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
	Client *issh.Client
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
	// No authentication
	conn.Write([]byte{5, 0})

	// Request
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
	case 3: // Domain
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

	conn.SetDeadline(time.Time{}) // reset deadline

	remote, err := s.Client.DialTCP(ctx, "tcp", target)
	if err != nil {
		conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()

	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	relay(conn, remote)
}

// relay bidirectionally copies between two conns.
func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, copyBuf)
		io.CopyBuffer(dst, src, buf)
		dst.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}
