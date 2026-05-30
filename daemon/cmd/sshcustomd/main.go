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
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
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
	activeConns  int
	memRSS       uint64
	cpuPct       float64
	upBps        float64
	downBps      float64
	wanIP        string
	wanCountry   string
	version      string
}

func (s *State) set(fn func(*State)) {
	s.mu.Lock()
	fn(s)
	s.mu.Unlock()
}

// wanInfo returns the cached tunnel-side public IP and country.
func (s *State) wanInfo() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wanIP, s.wanCountry
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
		ActiveConns:      s.activeConns,
		Version:          s.version,
		MemRSSMB:         float64(s.memRSS) / 1024 / 1024,
		CPUPercent:       s.cpuPct,
		UpKbps:           s.upBps / 1024,
		DownKbps:         s.downBps / 1024,
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
	go metricsLoop(ctx, st, &sshClient)

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
	const baseDelay = 3 * time.Second
	const maxDelay = 30 * time.Second
	iptables := workDir + "/scripts/ssh.iptables"

	// First attempt is immediate; backoff only applies after a failure.
	delay := time.Duration(0)

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
			// Ensure no stale redirect rules black-hole traffic while we retry.
			runScriptTimeout(iptables, 15*time.Second, "disable")
			if delay == 0 {
				delay = baseDelay
			} else {
				delay = minDuration(delay*2, maxDelay)
			}
			continue
		}

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

		// Bring up the local listeners FIRST so the redirect/tproxy target is
		// already accepting before iptables starts steering traffic to it.
		listenerCtx, listenerCancel := context.WithCancel(ctx)

		socks := &proxy.SOCKS5Server{
			Addr:   fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort),
			Client: c,
		}
		go func() {
			if err := socks.ListenAndServe(listenerCtx); err != nil {
				log.Printf("[socks5] %v", err)
			}
		}()

		switch cfg.NetworkMode {
		case "tproxy":
			tp := &proxy.TProxyServer{
				Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.TProxyPort),
				Client:  c,
				Timeout: 25 * time.Second,
			}
			go func() {
				if err := tp.ListenAndServe(listenerCtx); err != nil {
					log.Printf("[tproxy] %v", err)
				}
			}()
		case "tun", "tun_udpgw":
			// tun2proxy owns the data path; no transparent/tproxy listener.
		default: // redirect
			trans := &proxy.TransparentServer{
				Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.RedirPort),
				Client:  c,
				Timeout: 25 * time.Second,
			}
			go func() {
				if err := trans.ListenAndServe(listenerCtx); err != nil {
					log.Printf("[transparent] %v", err)
				}
			}()
		}

		// For TUN modes, start tun2proxy and wait for the device to exist before
		// applying iptables (the tun routing rules reference the device).
		var tun2proxyCmd *exec.Cmd
		if cfg.NetworkMode == "tun" || cfg.NetworkMode == "tun_udpgw" {
			tun2proxyCmd = startTun2proxy(cfg)
			waitForDevice(cfg.TunDevice, 5*time.Second)
		} else {
			// Give the listener a brief moment to bind before steering traffic.
			time.Sleep(150 * time.Millisecond)
		}

		// Daemon owns iptables: applied only once the tunnel is up and listeners
		// are ready (ssh.service no longer applies them).
		runScriptTimeout(iptables, 30*time.Second, "enable")

		// Resolve the tunnel-side public IP in the background and cache it so the
		// app's WAN card can read it instantly. Refresh every 5 minutes.
		go func(c *issh.Client) {
			for {
				if ip, country := fetchPublicIP(c); ip != "" {
					st.set(func(s *State) { s.wanIP = ip; s.wanCountry = country })
				}
				select {
				case <-listenerCtx.Done():
					return
				case <-time.After(5 * time.Minute):
				}
			}
		}(c)

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
		listenerCancel()
		if tun2proxyCmd != nil && tun2proxyCmd.Process != nil {
			tun2proxyCmd.Process.Kill()
		}
		c.Close()
		clientPtr.Store(nil)
		st.set(func(s *State) {
			s.connected = false
			s.poolSize = 0
			s.poolAvail = 0
			s.wanIP = ""
			s.wanCountry = ""
			s.lastEvent = "disconnected"
		})
		runScriptTimeout(iptables, 15*time.Second, "disable")

		if ctx.Err() != nil {
			return
		}
		// Reconnect attempt is immediate after an unexpected drop.
		delay = 0
	}
}

// waitForDevice blocks until a network interface named dev appears, or timeout.
func waitForDevice(dev string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/sys/class/net/" + dev); err == nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// startTun2proxy launches the tun2proxy binary (pinned to v0.8.1 in CI).
// We pass --tun (not --setup) and let ssh.iptables own the routing, so the
// daemon's own SSH connection to the server is exempted via owner-match rules.
func startTun2proxy(cfg *config.Config) *exec.Cmd {
	bin := cfg.BinDir + "/tun2proxy"
	args := []string{
		"--tun", cfg.TunDevice,
		"--proxy", fmt.Sprintf("socks5://127.0.0.1:%d", cfg.SocksPort),
		"--dns", "over-tcp",
		"--verbosity", "warn",
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
	log.Printf("[tun2proxy] started pid=%d (tun=%s)", cmd.Process.Pid, cfg.TunDevice)
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

	// Public IP as seen through the tunnel (used by the app's WAN card). Served
	// from the daemon's cache (refreshed on connect) so the app's short HTTP
	// timeout isn't blocked by a live through-tunnel lookup.
	mux.HandleFunc("/api/v1/network/public-ip", func(w http.ResponseWriter, r *http.Request) {
		ip, country := st.wanInfo()
		if ip == "" {
			env(w, false, nil, "resolving")
			return
		}
		env(w, true, map[string]interface{}{
			"tunnel": map[string]string{"ip": ip, "country": country},
		}, "")
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

// fetchPublicIP dials a lightweight IP-echo service through the SSH tunnel and
// returns the tunnel-side public IP and country. Best-effort: returns empty on
// any failure.
func fetchPublicIP(c *issh.Client) (ip string, country string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := c.DialTCP(ctx, "tcp", "ip-api.com:80")
	if err != nil {
		return "", ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))

	// HTTP/1.0 so the server closes the connection after the body (no chunking).
	req := "GET /json/?fields=query,country HTTP/1.0\r\n" +
		"Host: ip-api.com\r\nUser-Agent: sshcustomd\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return "", ""
	}

	raw, err := io.ReadAll(conn)
	if err != nil && len(raw) == 0 {
		return "", ""
	}
	body := string(raw)
	if i := strings.Index(body, "\r\n\r\n"); i >= 0 {
		body = body[i+4:]
	}
	var resp struct {
		Query   string `json:"query"`
		Country string `json:"country"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &resp); err != nil {
		return "", ""
	}
	return resp.Query, resp.Country
}

// clkTck is the kernel USER_HZ (clock ticks per second). 100 on Android/Linux.
const clkTck = 100.0

func metricsLoop(ctx context.Context, st *State, clientPtr *atomic.Pointer[issh.Client]) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	prevCPU := readCPUTicks()
	prevRx, prevTx := readNetBytes()
	prevT := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			dt := now.Sub(prevT).Seconds()

			rss := readRSS()

			// CPU% over the interval (sum of user+system ticks / USER_HZ / dt).
			curCPU := readCPUTicks()
			cpu := 0.0
			if dt > 0 && curCPU >= prevCPU {
				cpu = float64(curCPU-prevCPU) / clkTck / dt * 100.0
			}

			// Net throughput (bytes/sec) across all non-loopback interfaces.
			rx, tx := readNetBytes()
			up, down := 0.0, 0.0
			if dt > 0 {
				if tx >= prevTx {
					up = float64(tx-prevTx) / dt
				}
				if rx >= prevRx {
					down = float64(rx-prevRx) / dt
				}
			}

			prevCPU = curCPU
			prevRx, prevTx = rx, tx
			prevT = now

			ps, pa, ac := 0, 0, 0
			if c := clientPtr.Load(); c != nil {
				ps, pa = c.PoolStats()
				ac = c.ActiveConns()
			}

			st.set(func(s *State) {
				s.memRSS = rss
				s.cpuPct = cpu
				s.upBps = up
				s.downBps = down
				s.poolSize = ps
				s.poolAvail = pa
				s.activeConns = ac
			})
		}
	}
}

// readCPUTicks returns this process's cumulative (utime+stime) in clock ticks.
func readCPUTicks() uint64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	s := string(data)
	// The comm field (2nd) is parenthesised and may contain spaces; parse the
	// fields after the final ')'. utime is field 14, stime field 15 (1-indexed),
	// i.e. indices 11 and 12 in the post-')' slice (which starts at field 3).
	rp := strings.LastIndexByte(s, ')')
	if rp < 0 || rp+1 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[rp+1:])
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return utime + stime
}

// readNetBytes sums received/transmitted bytes across all non-loopback
// interfaces from /proc/net/dev (the daemon is root, so this is readable).
func readNetBytes() (rx uint64, tx uint64) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range splitLines(data) {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:idx])
		if iface == "" || iface == "lo" {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64) // recv bytes
		t, _ := strconv.ParseUint(fields[8], 10, 64) // transmit bytes
		rx += r
		tx += t
	}
	return rx, tx
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


