//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"syscall"
	"unsafe"
)

const soIPTransparent = 19

// listenTransparentTCP creates a TCP listener with IP_TRANSPARENT via
// net.ListenConfig.Control — the same approach used by Box/xray and
// proven on thousands of Android devices.
func listenTransparentTCP(ctx context.Context, addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var setErr error
			c.Control(func(fd uintptr) {
				setErr = setTransparent(int(fd))
				if setErr != nil {
					return
				}
				setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			return setErr
		},
	}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	// Verify IP_TRANSPARENT took effect on this kernel.
	// Some kernels silently ignore it on raw syscall sockets.
	if !isTransparentListener(ln) {
		ln.Close()
		return nil, fmt.Errorf("IP_TRANSPARENT not honored by kernel — REDIRECT fallback required")
	}
	log.Printf("[transparent] IP_TRANSPARENT verified")
	return ln, nil
}

func isTransparentListener(ln net.Listener) bool {
	tcpLn, ok := ln.(*net.TCPListener)
	if !ok {
		return false
	}
	raw, err := tcpLn.SyscallConn()
	if err != nil {
		return false
	}
	var ok2 bool
	raw.Control(func(fd uintptr) {
		val := int32(0)
		sz := uint32(unsafe.Sizeof(val))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			uintptr(syscall.SOL_IP),
			uintptr(soIPTransparent),
			uintptr(unsafe.Pointer(&val)),
			uintptr(unsafe.Pointer(&sz)),
			0,
		)
		ok2 = errno == 0 && val == 1
	})
	return ok2
}

// setTransparent sets IP_TRANSPARENT sockopt on a file descriptor.
func setTransparent(fd int) error {
	return syscall.SetsockoptInt(fd, syscall.SOL_IP, soIPTransparent, 1)
}

// tproxyDst returns the original destination from a TPROXY-accepted TCP
// connection. TPROXY preserves the destination so LocalAddr() holds it.
func tproxyDst(conn *net.TCPConn) (string, error) {
	addr := conn.LocalAddr()
	if addr == nil {
		return "", errors.New("no local address on tproxy socket")
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("unexpected addr type: %T", addr)
	}
	// TPROXY preserves the original destination, which is never loopback.
	// A loopback address means REDIRECT rewrote the destination — reject
	// here so the caller falls through to originalDst (SO_ORIGINAL_DST).
	if tcpAddr.IP.IsLoopback() {
		return "", errors.New("loopback — REDIRECT mode, use SO_ORIGINAL_DST")
	}
	return net.JoinHostPort(tcpAddr.IP.String(), fmt.Sprintf("%d", tcpAddr.Port)), nil
}

// udpTproxyConn wraps a UDP socket with IP_TRANSPARENT + IP_RECVORIGDSTADDR.
type udpTproxyConn struct {
	conn *net.UDPConn
}

// listenTransparentUDP creates a UDP listener with IP_TRANSPARENT +
// IP_RECVORIGDSTADDR for receiving TPROXY-captured UDP packets.
func listenTransparentUDP(ctx context.Context, addr string) (*udpTproxyConn, error) {
	uaddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	var conn *net.UDPConn
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var ctrlErr error
			c.Control(func(fd uintptr) {
				if ctrlErr = setTransparent(int(fd)); ctrlErr != nil {
					return
				}
				if ctrlErr = setRecvOrigDst(int(fd)); ctrlErr != nil {
					return
				}
				if ctrlErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); ctrlErr != nil {
					return
				}
			})
			return ctrlErr
		},
	}
	pc, err := lc.ListenPacket(ctx, "udp", uaddr.String())
	if err != nil {
		return nil, err
	}
	conn = pc.(*net.UDPConn)
	return &udpTproxyConn{conn: conn}, nil
}

// setRecvOrigDst enables IP_RECVORIGDSTADDR (20) on Linux 2.6.29+.
func setRecvOrigDst(fd int) error {
	const ipRecvOrigDstAddr = 20
	return syscall.SetsockoptInt(fd, syscall.SOL_IP, ipRecvOrigDstAddr, 1)
}

func (l *udpTproxyConn) Close() error {
	return l.conn.Close()
}

// recvFrom reads a datagram and extracts original destination from ancillary data.
func (l *udpTproxyConn) recvFrom() ([]byte, *net.UDPAddr, *net.UDPAddr, error) {
	buf := make([]byte, 65535)
	oob := make([]byte, 1024)

	n, oobn, _, src, err := l.conn.ReadMsgUDP(buf, oob)
	if err != nil {
		return nil, nil, nil, err
	}

	dst := parseOrigDstUDP(oob[:oobn])
	if dst == nil {
		return nil, nil, nil, errors.New("no original destination in ancillary data")
	}

	return buf[:n], src, dst, nil
}

// parseOrigDstUDP extracts the original destination IPv4 address from
// IP_RECVORIGDSTADDR ancillary data (sockaddr_in).
func parseOrigDstUDP(oob []byte) *net.UDPAddr {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	for _, msg := range msgs {
		if msg.Header.Level == syscall.SOL_IP && msg.Header.Type == 20 {
			if len(msg.Data) < 8 {
				continue
			}
			port := int(msg.Data[2])<<8 | int(msg.Data[3])
			ip := net.IPv4(msg.Data[4], msg.Data[5], msg.Data[6], msg.Data[7])
			return &net.UDPAddr{IP: ip, Port: port}
		}
	}
	return nil
}
