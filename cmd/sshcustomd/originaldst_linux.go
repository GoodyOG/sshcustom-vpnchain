//go:build linux

package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"unsafe"
)

const soOriginalDst = 80

func originalDst(conn *net.TCPConn) (string, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return "", err
	}
	var out string
	var serr error
	err = raw.Control(func(fd uintptr) {
		var addr syscall.RawSockaddrInet4
		sz := uint32(unsafe.Sizeof(addr))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(syscall.SOL_IP), uintptr(soOriginalDst), uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&sz)), 0)
		if errno != 0 {
			serr = errno
			return
		}
		if addr.Family != syscall.AF_INET {
			serr = fmt.Errorf("unexpected original dst family %d", addr.Family)
			return
		}
		port := int((addr.Port&0xff)<<8 | addr.Port>>8)
		ip := net.IPv4(addr.Addr[0], addr.Addr[1], addr.Addr[2], addr.Addr[3]).String()
		out = net.JoinHostPort(ip, strconv.Itoa(port))
	})
	if err != nil {
		return "", err
	}
	if serr != nil {
		return "", serr
	}
	if out == "" {
		return "", errors.New("empty original dst")
	}
	return out, nil
}
