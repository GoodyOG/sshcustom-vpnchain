// sshcustomd — SSHCustom-VPNChain daemon
// Single static binary; zero CGO; target: GOOS=android GOARCH=arm64.
//
// Usage:
//   sshcustomd run -c /data/adb/sshcustom/settings.ini -w /data/adb/sshcustom [--idle]
//   sshcustomd version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/GoodyOG/SSHCustom_Magisk/internal/api"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/config"
	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/proxy"
)

var version = "2.0.0"

func main() {
	// GC tuning for high-throughput TCP copying on Android
	debug.SetGCPercent(200)
	debug.SetMemoryLimit(192 * 1024 * 1024)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version", "-version", "--version":
		fmt.Printf("sshcustomd v%s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	case "run":
		runCmd()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n  sshcustomd run -c <settings.ini> -w <workdir> [--idle]\n  sshcustomd version\n")
}

// ── State ────────────────────────────────────────────────────────────────────

type State struct {
	mu          sync.RWMutex
	connected   bool
	startedAt   time.Time
	tunnelStart time.Time
	lastError   string
	sshMode     string
	netMode     string
	poolSize    int
	poolAvail   int
	activeConns int32
	memRSS      uint64
	cpuPct      float64
	version     string
}

func (s *State) set(fn func(*State)) {
	s.mu.Lock()
	fn(s)
	s.mu.Unlock()
}

func (s *State) snapshot() api.StatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uptime := int64(0)
	if s.connected && !s.tunnelStart.IsZero() {
		uptime = int64(time.Since(s.tunnelStart).Seconds())
	}
	memMB := float64(s.memRSS) / 1024 / 1024
	return api.StatusSnapshot{
		Connected:        s.connected,
		UptimeSeconds:    uptime,
		SSHMode:          s.sshMode,
		NetworkMode:      s.netMode,
		ChannelPoolSize:  s.poolSize,
		ChannelPoolAvail: s.poolAvail,
		Version:          s.version,
		MemRSSMB:         memMB,
		CPUPercent:       s.cpuPct,
	}
}

// ── runCmd ───────────────────────────────────────────────────────────────────

func runCmd() {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("c", "/data/adb/sshcustom/settings.ini", "path to settings.ini")
	workDir := fs.String("w", "/data/adb/sshcustom", "work directory")
	idle := fs.Bool("idle", false, "start in idle mode (WebUI only, no tunnel)")
	fs.Parse(os.Args[2:])

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Write PID file
	pidFile := cfg.BoxPID
	if pidFile == "" {
		pidFile = *workDir + "/run/sshcustom.pid"
	}
	os.MkdirAll(*workDir+"/run", 0700)
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
	defer os.Remove(pidFile)

	st := &State{
		startedAt: time.Now(),
		netMode:   cfg.NetworkMode,
		sshMode:   cfg.SSHMode,
		version:   version,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sshClient atomic.Pointer[issh.Client]

	// Unix socket API
	sockPath := *workDir + "/run/sshcustomd.sock"
	unixSrv := &api.UnixServer{
		SocketPath: sockPath,
		GetStatus:  st.snapshot,
		HandleControl: func(action string) error {
			return handleControl(action, *workDir, cfg, &sshClient, st)
		},
	}
	go func() {
		if err := unixSrv.ListenAndServe(ctx); err != nil {
			log.Printf("unix-api: %v", err)
		}
	}()

	// HTTP API + WebUI
	mux := buildHTTPMux(cfg, *workDir, st, &sshClient)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", 9190)
	httpSrv := &http.Server{Addr: httpAddr, Handler: mux}
	go func() {
		log.Printf("http: listening on %s", httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		httpSrv.Shutdown(context.Background())
	}()

	// Metrics ticker
	go metricsLoop(ctx, st)

	// Start tunnel unless idle
	if !*idle {
		go tunnelLoop(ctx, cfg, st, &sshClient)
	}

	// Signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	for {
		s := <-sig
		switch s {
		case syscall.SIGHUP:
			log.Println("main: SIGHUP — reloading config")
			newCfg, err := config.Load(*cfgPath)
			if err != nil {
				log.Printf("config reload: %v", err)
			} else {
				*cfg = *newCfg
				log.Println("main: config reloaded")
			}
		default:
			log.Printf("main: signal %v — shutting down", s)
			cancel()
			time.Sleep(300 * time.Millisecond)
			return
		}
	}
}

// ── Tunnel loop ───────────────────────────────────────────────────────────────

func tunnelLoop(ctx context.Context, cfg *config.Config, st *State, clientPtr *atomic.Pointer[issh.Client]) {
	delay := time.Duration(3) * time.Second
	maxDelay := time.Duration(30) * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if cfg.SSHHost == "" || cfg.SSHUser == "" {
			log.Println("tunnel: ssh_host or ssh_user not configured — waiting")
			delay = 10 * time.Second
			continue
		}

		log.Printf("tunnel: connecting to %s:%d mode=%s", cfg.SSHHost, cfg.SSHPort, cfg.SSHMode)
		st.set(func(s *State) { s.lastError = "" })

		c, err := issh.Dial(ctx, issh.ConnectConfig{
			Host:           cfg.SSHHost,
			Port:           cfg.SSHPort,
			User:           cfg.SSHUser,
			Password:       cfg.SSHPassword,
			Mode:           issh.TransportMode(cfg.SSHMode),
			SNIHost:        cfg.SSHSNIHost,
			HTTPProxyHost:  cfg.HTTPProxyHost,
			HTTPProxyPort:  cfg.HTTPProxyPort,
			PayloadEnabled: cfg.PayloadEnabled,
			Payload:        cfg.Payload,
			ConnectTimeout: 25 * time.Second,
			KeepAlive:      10 * time.Second,
		}, cfg.ChannelPoolSize)

		if err != nil {
			log.Printf("tunnel: connect failed: %v", err)
			st.set(func(s *State) {
				s.connected = false
				s.lastError = err.Error()
			})
			delay = min(delay*2, maxDelay)
			continue
		}

		delay = 3 * time.Second
		clientPtr.Store(c)
		st.set(func(s *State) {
			s.connected = true
			s.tunnelStart = time.Now()
			s.sshMode = cfg.SSHMode
			sz, av := c.PoolStats()
			s.poolSize = sz
			s.poolAvail = av
		})
		log.Printf("tunnel: connected (pool_size=%d)", cfg.ChannelPoolSize)

		// Apply iptables if not in idle mode
		go runScript("/data/adb/sshcustom/scripts/ssh.iptables", "enable")

		// Start SOCKS5 listener
		socksCtx, socksCancel := context.WithCancel(ctx)
		socks := &proxy.SOCKS5Server{
			Addr:   fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort),
			Client: c,
		}
		go func() {
			if err := socks.ListenAndServe(socksCtx); err != nil {
				log.Printf("socks5: %v", err)
			}
		}()

		// Start transparent proxy listener
		transCtx, transCancel := context.WithCancel(ctx)
		trans := &proxy.TransparentServer{
			Addr:   fmt.Sprintf("0.0.0.0:%d", cfg.RedirPort),
			Client: c,
		}
		go func() {
			if err := trans.ListenAndServe(transCtx); err != nil {
				log.Printf("transparent: %v", err)
			}
		}()

		// Launch tun2proxy if mode is tun/tun_udpgw
		var tun2proxyCmd *exec.Cmd
		if cfg.NetworkMode == "tun" || cfg.NetworkMode == "tun_udpgw" {
			tun2proxyCmd = startTun2proxy(cfg)
		}

		// Wait for SSH connection to drop
		waitSSHDrop(ctx, c)

		// Teardown
		log.Println("tunnel: connection lost — tearing down")
		socksCancel()
		transCancel()
		if tun2proxyCmd != nil && tun2proxyCmd.Process != nil {
			tun2proxyCmd.Process.Kill()
		}
		c.Close()
		clientPtr.Store(nil)
		st.set(func(s *State) { s.connected = false })
		runScript("/data/adb/sshcustom/scripts/ssh.iptables", "disable")
	}
}

// waitSSHDrop blocks until the SSH connection drops or ctx is cancelled.
func waitSSHDrop(ctx context.Context, c *issh.Client) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Try opening a channel; if it fails the connection is dead
			conn, err := c.DialTCP(ctx, "tcp", "127.0.0.1:1")
			if err != nil {
				return
			}
			conn.Close()
		}
	}
}

// startTun2proxy launches the tun2proxy binary.
func startTun2proxy(cfg *config.Config) *exec.Cmd {
	bin := cfg.BinDir + "/tun2proxy"
	args := []string{
		"--device", cfg.TunDevice,
		"--proxy", fmt.Sprintf("socks5://127.0.0.1:%d", cfg.SocksPort),
		"--dns", "over-tcp",
		"--loglevel", "warn",
	}
	if cfg.NetworkMode == "tun_udpgw" && cfg.UDPGWServer != "" {
		args = append(args, "--udpgw-server", cfg.UDPGWServer)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("tun2proxy: failed to start: %v", err)
		return nil
	}
	log.Printf("tun2proxy: started pid=%d", cmd.Process.Pid)
	return cmd
}

// runScript executes a module shell script.
func runScript(path string, args ...string) {
	cmd := exec.Command("/system/bin/sh", append([]string{path}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("script %s %v: %v\n%s", path, args, err, out)
	}
}

// ── Control handler ───────────────────────────────────────────────────────────

func handleControl(action, workDir string, cfg *config.Config, clientPtr *atomic.Pointer[issh.Client], st *State) error {
	switch action {
	case "start":
		runScript("/data/adb/sshcustom/scripts/ssh.service", "start")
	case "stop":
		runScript("/data/adb/sshcustom/scripts/ssh.service", "stop")
	case "restart":
		runScript("/data/adb/sshcustom/scripts/ssh.service", "restart")
	case "reload":
		// handled in signal loop
	}
	return nil
}

// ── HTTP API ─────────────────────────────────────────────────────────────────

func buildHTTPMux(cfg *config.Config, workDir string, st *State, clientPtr *atomic.Pointer[issh.Client]) *http.ServeMux {
	mux := http.NewServeMux()

	envelope := func(w http.ResponseWriter, ok bool, data interface{}, errMsg string) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{"api_version": "v1", "ok": ok}
		if ok {
			resp["data"] = data
		} else {
			resp["error"] = errMsg
		}
		json.NewEncoder(w).Encode(resp)
	}

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		envelope(w, true, map[string]string{"status": "ok", "version": version}, "")
	})

	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		snap := st.snapshot()
		envelope(w, true, map[string]interface{}{
			"runtime": snap,
			"config": map[string]interface{}{
				"network_mode": cfg.NetworkMode,
				"ssh_mode":     cfg.SSHMode,
				"socks_port":   cfg.SocksPort,
			},
		}, "")
	})

	mux.HandleFunc("/api/v1/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var req struct{ Action string `json:"action"` }
		json.NewDecoder(r.Body).Decode(&req)
		go time.AfterFunc(300*time.Millisecond, func() {
			handleControl(req.Action, workDir, cfg, clientPtr, st)
		})
		envelope(w, true, map[string]string{"scheduled": req.Action}, "")
	})

	mux.HandleFunc("/api/v1/autostart", func(w http.ResponseWriter, r *http.Request) {
		marker := workDir + "/run/autostart"
		switch r.Method {
		case http.MethodGet:
			_, err := os.Stat(marker)
			envelope(w, true, map[string]bool{"enabled": err == nil}, "")
		case http.MethodPost, http.MethodPut:
			var req struct{ Enabled bool `json:"enabled"` }
			json.NewDecoder(r.Body).Decode(&req)
			if req.Enabled {
				os.WriteFile(marker, []byte("1"), 0644)
			} else {
				os.Remove(marker)
			}
			envelope(w, true, map[string]bool{"enabled": req.Enabled}, "")
		}
	})

	// Serve embedded WebUI at root
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Prefer on-disk webroot for hot-editing
		webrootIndex := workDir + "/webroot/index.html"
		if _, err := os.Stat(webrootIndex); err == nil {
			http.ServeFile(w, r, webrootIndex)
			return
		}
		http.Error(w, "WebUI not found. Install the companion app.", 404)
	})

	return mux
}

// ── Metrics ──────────────────────────────────────────────────────────────────

func metricsLoop(ctx context.Context, st *State) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rss := readRSS()
			st.set(func(s *State) { s.memRSS = rss })
		}
	}
}

func readRSS() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range splitLines(data) {
		if len(line) > 6 && line[:6] == "VmRSS:" {
			var kb uint64
			fmt.Sscanf(line[6:], "%d", &kb)
			return kb * 1024
		}
	}
	return 0
}

func splitLines(b []byte) []string {
	var lines []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, string(b[start:i]))
			start = i + 1
		}
	}
	return lines
}

// ── relay helper (used by proxy packages via shared copy) ────────────────────

func relayConns(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src io.ReadWriteCloser) {
		buf := make([]byte, 128*1024)
		io.CopyBuffer(dst, src, buf)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
