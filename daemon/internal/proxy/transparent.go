package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"
	"unsafe"

	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
)

// TransparentServer intercepts TCP connections redirected by iptables REDIRECT.
// It reads the original destination via SO_ORIGINAL_DST and forwards through SSH.
type TransparentServer struct {
	Addr    string
	Client  func() *issh.Client // current SSH client; nil during a reconnect gap
	Timeout time.Duration       // per-connection dial timeout; 0 = 25s
}

// ListenAndServe starts the transparent proxy listener.
func (t *TransparentServer) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", t.Addr)
	if err != nil {
		return fmt.Errorf("transparent listen %s: %w", t.Addr, err)
	}
	log.Printf("[transparent] listening on %s", t.Addr)
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

func (t *TransparentServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	dialTimeout := t.Timeout
	if dialTimeout == 0 {
		dialTimeout = 25 * time.Second
	}

	// Read original destination before setting the transfer deadline
	target, err := originalDst(conn)
	if err != nil {
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	cl := t.Client()
	if cl == nil {
		return // tunnel reconnecting — drop this conn (fail-closed, no leak)
	}
	remote, err := cl.DialTCP(dialCtx, "tcp", target)
	if err != nil {
		return
	}
	defer remote.Close()

	cl.AddConn()
	defer cl.RemoveConn()

	// relay uses interface check for half-close — safe for SSH channels
	relay(conn, remote)
}

// originalDst reads the SO_ORIGINAL_DST socket option to get the pre-NAT destination.
func originalDst(conn net.Conn) (string, error) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not a TCPConn")
	}
	rc, err := tc.SyscallConn()
	if err != nil {
		return "", err
	}

	var dst [16]byte
	var cerr error
	err = rc.Control(func(fd uintptr) {
		// SOL_IP=0, SO_ORIGINAL_DST=80
		const SOL_IP = 0
		const SO_ORIGINAL_DST = 80
		sz := uint32(unsafe.Sizeof(dst))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			SOL_IP,
			SO_ORIGINAL_DST,
			uintptr(unsafe.Pointer(&dst)),
			uintptr(unsafe.Pointer(&sz)),
			0,
		)
		if errno != 0 {
			cerr = errno
		}
	})
	if err != nil {
		return "", err
	}
	if cerr != nil {
		return "", cerr
	}

	// sockaddr_in: family(2) + port(2) + addr(4)
	port := binary.BigEndian.Uint16(dst[2:4])
	ip := net.IP(dst[4:8])
	return fmt.Sprintf("%s:%d", ip, port), nil
}
