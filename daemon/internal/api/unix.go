// Package api provides the Unix domain socket API for the Android app.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// StatusSnapshot is the payload returned for "status" requests.
type StatusSnapshot struct {
	Type              string  `json:"type"`
	Connected         bool    `json:"connected"`
	UptimeSeconds     int64   `json:"uptime_seconds"`
	SSHMode           string  `json:"ssh_mode"`
	NetworkMode       string  `json:"network_mode"`
	BytesSent         int64   `json:"bytes_sent"`
	BytesRecv         int64   `json:"bytes_recv"`
	ChannelPoolSize   int     `json:"channel_pool_size"`
	ChannelPoolAvail  int     `json:"channel_pool_available"`
	ActiveConnections int     `json:"active_connections"`
	Version           string  `json:"version"`
	MemRSSMB          float64 `json:"mem_rss_mb"`
	CPUPercent        float64 `json:"cpu_percent"`
}

// ControlRequest is a request from the app to control the daemon.
type ControlRequest struct {
	Type   string `json:"type"`   // ping | status | reload | start | stop | restart
	Action string `json:"action"` // used for "control" type
}

// ControlResponse is the reply to a ControlRequest.
type ControlResponse struct {
	Type    string      `json:"type"`
	OK      bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// StatusProvider is a callback used by UnixServer to get current state.
type StatusProvider func() StatusSnapshot

// ControlHandler is called when the app sends a control command.
type ControlHandler func(action string) error

// UnixServer listens on a Unix domain socket for the Android app.
type UnixServer struct {
	SocketPath    string
	GetStatus     StatusProvider
	HandleControl ControlHandler
}

// ListenAndServe starts the Unix socket server.
func (u *UnixServer) ListenAndServe(ctx context.Context) error {
	os.Remove(u.SocketPath)
	ln, err := net.Listen("unix", u.SocketPath)
	if err != nil {
		return fmt.Errorf("unix socket listen %s: %w", u.SocketPath, err)
	}
	// World-readable so app process can connect without root
	os.Chmod(u.SocketPath, 0666)
	log.Printf("[unix-api] listening on %s", u.SocketPath)

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
		go u.handleConn(conn)
	}
}

func (u *UnixServer) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req ControlRequest
	if err := dec.Decode(&req); err != nil {
		return
	}

	var resp ControlResponse
	resp.Type = req.Type
	resp.OK = true

	switch req.Type {
	case "ping":
		resp.Data = map[string]string{"pong": "1"}

	case "status":
		snap := u.GetStatus()
		snap.Type = "status"
		resp.Data = snap

	case "reload":
		if u.HandleControl != nil {
			if err := u.HandleControl("reload"); err != nil {
				resp.OK = false
				resp.Error = err.Error()
			}
		}

	case "control":
		if u.HandleControl != nil {
			if err := u.HandleControl(req.Action); err != nil {
				resp.OK = false
				resp.Error = err.Error()
			}
		}

	default:
		resp.OK = false
		resp.Error = fmt.Sprintf("unknown request type: %s", req.Type)
	}

	enc.Encode(resp)
}
