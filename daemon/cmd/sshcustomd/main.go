// sshcustomd — SSHCustom-VPNChain daemon
// Single static binary, GOOS=android GOARCH=arm64 CGO_ENABLED=0.
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
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA tz database — Android has no usable zoneinfo dir

	"github.com/GoodyOG/SSHCustom_Magisk/internal/api"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/config"
	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/proxy"
)

var version = "2.0.0"

// setLocalTimezone aligns the daemon's log timestamps with the device's local
// time. When launched via nohup from a root service, TZ is unset, so Go's
// time.Local resolves to UTC — making daemon logs appear an hour (or more) off
// from the shell scripts' `date` output in the same log file. We read the
// Android timezone property and load it from the embedded tz database.
func setLocalTimezone() {
	out, err := exec.Command("/system/bin/getprop", "persist.sys.timezone").Output()
	if err != nil {
		return
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return
	}
	if loc, err := time.LoadLocation(name); err == nil {
		time.Local = loc
	}
}

func main() {
	// Align daemon log timestamps with device local time (see setLocalTimezone).
	setLocalTimezone()

	// GC tuning: allow more heap before GC, cap RSS at 192 MB
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

// ── State ─────────────────────────────────────────────────────────────────────

type State struct {
	mu           sync.RWMutex
	connected    bool
	tunnelStart  time.Time
	startedAt    time.Time
	lastError    string
	lastEvent    string
	sshMode      string
	netMode      string
	poolSize     int
	poolAvail    int
	activeConns  int32
	memRSS       uint64
	cpuPct       float64
	version      string
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
	return api.StatusSnapshot{
		Connected:        s.connected,
		UptimeSeconds:    uptime,
		SSHMode:          s.sshMode,
		NetworkMode:      s.netMode,
		ChannelPoolSize:  s.poolSize,
		ChannelPoolAvail: s.poolAvail,
		ActiveConns:      int(atomic.LoadInt32(&s.activeConns)),
		Version:          s.version,
		MemRSSMB:         float64(s.memRSS) / 1024 / 1024,
		CPUPercent:       s.cpuPct,
		LastError:        s.lastError,
	}
}

// ── runCmd ────────────────────────────────────────────────────────────────────

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
	atomicCfg := config.NewAtomic(cfg)

	// Write PID file
	runDir := *workDir + "/run"
	os.MkdirAll(runDir, 0700)
	pidFile := runDir + "/sshcustom.pid"
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

	// Unix socket API for Android app
	sockPath := runDir + "/sshcustomd.sock"
	unixSrv := &api.UnixServer{
		SocketPath: sockPath,
		GetStatus:  st.snapshot,
		HandleControl: func(action string) error {
			return handleControl(action, *workDir)
		},
	}
	go func() {
		if err := unixSrv.ListenAndServe(ctx); err != nil {
			log.Printf("[unix-api] %v", err)
		}
	}()

	// HTTP API + WebUI — with timeouts
	mux := buildHTTPMux(atomicCfg, *workDir, *cfgPath, st, &sshClient)
	httpSrv := &http.Server{
		Addr:         "127.0.0.1:9190",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second, // SSE streams need a longer write timeout
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		log.Printf("[http] listening on 127.0.0.1:9190")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[http] %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutCancel()
		httpSrv.Shutdown(shutCtx)
	}()

	// Metrics ticker
	go metricsLoop(ctx, st)

	// Start tunnel unless idle
	if !*idle {
		go tunnelLoop(ctx, atomicCfg, *cfgPath, *workDir, st, &sshClient)
	}

	// Signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	for {
		s := <-sig
		switch s {
		case syscall.SIGHUP:
			log.Println("[main] SIGHUP — reloading config")
			newCfg, err := config.Load(*cfgPath)
			if err != nil {
				log.Printf("[main] config reload failed: %v", err)
			} else {
				atomicCfg.Store(newCfg)
				log.Println("[main] config reloaded")
			}
		default:
			log.Printf("[main] signal %v — shutting down", s)
			cancel()
			time.Sleep(500 * time.Millisecond)
			return
		}
	}
}

// ── Tunnel loop ───────────────────────────────────────────────────────────────

func tunnelLoop(
	ctx context.Context,
	atomicCfg *config.AtomicConfig,
	cfgPath, workDir string,
	st *State,
	clientPtr *atomic.Pointer[issh.Client],
) {
	delay := 3 * time.Second
	maxDelay := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		cfg := atomicCfg.Get()

		if cfg.SSHHost == "" || cfg.SSHUser == "" {
			log.Println("[tunnel] ssh_host/ssh_user not configured — waiting")
			delay = 10 * time.Second
			continue
		}

		log.Printf("[tunnel] connecting to %s:%d mode=%s", cfg.SSHHost, cfg.SSHPort, cfg.SSHMode)
		st.set(func(s *State) { s.lastError = ""; s.lastEvent = "connecting" })

		dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
		c, err := issh.Dial(dialCtx, issh.ConnectConfig{
			Host:              cfg.SSHHost,
			Port:              cfg.SSHPort,
			User:              cfg.SSHUser,
			Password:          cfg.SSHPassword,
			Mode:              issh.TransportMode(cfg.SSHMode),
			SNIHost:           cfg.SSHSNIHost,
			HTTPProxyHost:     cfg.HTTPProxyHost,
			HTTPProxyPort:     cfg.HTTPProxyPort,
			PayloadEnabled:    cfg.PayloadEnabled,
			Payload:           cfg.Payload,
			ConnectTimeout:    25 * time.Second,
			KeepAliveInterval: 30 * time.Second,
			KeepAliveMax:      3,
		}, cfg.ChannelPoolSize)
		dialCancel()

		if err != nil {
			log.Printf("[tunnel] connect failed: %v", err)
			st.set(func(s *State) {
				s.connected = false
				s.lastError = err.Error()
				s.lastEvent = "connect_failed"
			})
			delay = minDuration(delay*2, maxDelay)
			continue
		}

		delay = 3 * time.Second
		clientPtr.Store(c)
		sz, av := c.PoolStats()
		st.set(func(s *State) {
			s.connected = true
			s.tunnelStart = time.Now()
			s.sshMode = cfg.SSHMode
			s.netMode = cfg.NetworkMode
			s.poolSize = sz
			s.poolAvail = av
			s.lastEvent = "connected"
		})
		log.Printf("[tunnel] connected pool_size=%d", cfg.ChannelPoolSize)

		// Apply iptables with timeout
		runScriptTimeout(workDir+"/scripts/ssh.iptables", 30*time.Second, "enable")

		// SOCKS5 listener
		socksCtx, socksCancel := context.WithCancel(ctx)
		socks := &proxy.SOCKS5Server{
			Addr:   fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort),
			Client: c,
		}
		go func() {
			if err := socks.ListenAndServe(socksCtx); err != nil {
				log.Printf("[socks5] %v", err)
			}
		}()

		// Transparent proxy listener
		transCtx, transCancel := context.WithCancel(ctx)
		trans := &proxy.TransparentServer{
			Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.RedirPort),
			Client:  c,
			Timeout: 25 * time.Second,
		}
		go func() {
			if err := trans.ListenAndServe(transCtx); err != nil {
				log.Printf("[transparent] %v", err)
			}
		}()

		// Launch tun2proxy if mode requires it
		var tun2proxyCmd *exec.Cmd
		if cfg.NetworkMode == "tun" || cfg.NetworkMode == "tun_udpgw" {
			tun2proxyCmd = startTun2proxy(cfg)
		}

		// Wait for SSH to die using the proper Wait() method
		waitDone := make(chan error, 1)
		go func() { waitDone <- c.Wait() }()

		select {
		case <-ctx.Done():
		case err := <-waitDone:
			if err != nil {
				log.Printf("[tunnel] SSH connection lost: %v", err)
			}
		}

		// Teardown
		log.Println("[tunnel] tearing down")
		socksCancel()
		transCancel()
		if tun2proxyCmd != nil && tun2proxyCmd.Process != nil {
			tun2proxyCmd.Process.Kill()
		}
		c.Close()
		clientPtr.Store(nil)
		st.set(func(s *State) {
			s.connected = false
			s.poolSize = 0
			s.poolAvail = 0
			s.lastEvent = "disconnected"
		})
		runScriptTimeout(workDir+"/scripts/ssh.iptables", 15*time.Second, "disable")

		if ctx.Err() != nil {
			return
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
		log.Printf("[tun2proxy] failed to start: %v", err)
		return nil
	}
	log.Printf("[tun2proxy] started pid=%d", cmd.Process.Pid)
	return cmd
}

// runScriptTimeout executes a module shell script with a timeout.
func runScriptTimeout(path string, timeout time.Duration, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/system/bin/sh", append([]string{path}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[script] %s %v: %v\n%s", path, args, err, out)
	}
}

// handleControl dispatches control actions to the module script.
func handleControl(action, workDir string) error {
	script := workDir + "/scripts/ssh.service"
	switch action {
	case "start", "stop", "restart", "start-idle":
		runScriptTimeout(script, 30*time.Second, action)
	case "reload":
		// SIGHUP to self handled in signal loop
	}
	return nil
}

// ── HTTP API ──────────────────────────────────────────────────────────────────

func buildHTTPMux(
	atomicCfg *config.AtomicConfig,
	workDir, cfgPath string,
	st *State,
	clientPtr *atomic.Pointer[issh.Client],
) *http.ServeMux {
	mux := http.NewServeMux()

	env := func(w http.ResponseWriter, ok bool, data interface{}, errMsg string) {
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
		env(w, true, map[string]string{"status": "ok", "version": version}, "")
	})

	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		cfg := atomicCfg.Get()
		snap := st.snapshot()
		env(w, true, map[string]interface{}{
			"runtime": snap,
			"config": map[string]interface{}{
				"network_mode": cfg.NetworkMode,
				"ssh_mode":     cfg.SSHMode,
				"socks_port":   cfg.SocksPort,
				"redir_port":   cfg.RedirPort,
				"tproxy_port":  cfg.TProxyPort,
				"quic":         cfg.QUIC,
				"channel_pool": cfg.ChannelPool,
			},
		}, "")
	})

	mux.HandleFunc("/api/v1/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		// Schedule 300ms later so HTTP response returns before daemon restarts
		go func() {
			time.Sleep(300 * time.Millisecond)
			handleControl(req.Action, workDir)
		}()
		env(w, true, map[string]string{"scheduled": req.Action}, "")
	})

	mux.HandleFunc("/api/v1/autostart", func(w http.ResponseWriter, r *http.Request) {
		marker := workDir + "/run/autostart"
		switch r.Method {
		case http.MethodGet:
			_, err := os.Stat(marker)
			env(w, true, map[string]bool{"enabled": err == nil}, "")
		case http.MethodPost, http.MethodPut:
			var req struct {
				Enabled bool `json:"enabled"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Enabled {
				os.WriteFile(marker, []byte("1"), 0644)
			} else {
				os.Remove(marker)
			}
			env(w, true, map[string]bool{"enabled": req.Enabled}, "")
		default:
			w.WriteHeader(405)
		}
	})

	// Config read endpoint
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := atomicCfg.Get()
		env(w, true, cfg, "")
	})

	// Serve on-disk WebUI at root (no embedded — companion app is the primary UI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		webrootIndex := workDir + "/webroot/index.html"
		if _, err := os.Stat(webrootIndex); err == nil {
			http.ServeFile(w, r, webrootIndex)
			return
		}
		http.Error(w, "WebUI not installed", 404)
	})

	return mux
}

// ── Metrics ───────────────────────────────────────────────────────────────────

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

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}


