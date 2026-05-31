//go:build !linux

package main

import (
	"errors"
	"net"
)

func originalDst(conn *net.TCPConn) (string, error) {
	return "", errors.New("transparent original-dst is only supported on linux")
}
