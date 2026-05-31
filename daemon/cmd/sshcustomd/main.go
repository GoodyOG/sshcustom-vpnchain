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
	"net"
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

// whitelistApp uses our root to exempt the companion app from Doze/battery
// optimization so the OS is less likely to kill its UI process in the
// background (which otherwise causes a brief "Stopped" flash on return).
// Best-effort; errors are ignored.
func whitelistApp() {
	const pkg = "com.sshcustom.vpnchain"
	cmds := []string{
		"dumpsys deviceidle whitelist +" + pkg,
		"cmd appops set " + pkg + " RUN_ANY_IN_BACKGROUND allow",
		"cmd appops set " + pkg + " RUN_IN_BACKGROUND allow",
	}
	for _, c := range cmds {
		_ = exec.Command("/system/bin/sh", "-c", c).Run()
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

	// Exempt the companion app from battery optimization (root, best-effort).
	go whitelistApp()

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
	const (
		baseDelay       = 1 * time.Second
		maxDelay        = 30 * time.Second
		reconnectGiveUp = 15 * time.Minute
	)
	iptables := workDir + "/scripts/ssh.iptables"
	curClient := func() *issh.Client { return clientPtr.Load() }

	// A "session" = the local listeners + iptables. It is brought up once on the
	// first successful connect and KEPT UP across transparent SSH reconnects, so
	// a brief SSH drop no longer flaps routing — apps just stall ~1s with no
	// leak (fail-closed), exactly like a VpnService app whose tun stays up.
	var (
		listenerCancel context.CancelFunc
		sessionMode    string
		iptablesUp     bool
	)
	teardownSession := func() {
		if listenerCancel != nil {
			listenerCancel()
			listenerCancel = nil
		}
		if iptablesUp {
			runScriptTimeout(iptables, 15*time.Second, "disable")
			iptablesUp = false
		}
	}
	defer teardownSession() // explicit stop (ctx cancel) removes routing

	delay := time.Duration(0)
	var downSince time.Time // when the tunnel was lost while a session was up

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
			KeepAliveInterval: 10 * time.Second,
			KeepAliveMax:      3,
		})
		dialCancel()

		if err != nil {
			log.Printf("[tunnel] connect failed: %v", err)
			st.set(func(s *State) {
				s.connected = false
				s.lastError = err.Error()
				s.lastEvent = "connect_failed"
			})
			// Keep the session (iptables + listeners) UP across retries —
			// fail-closed, no traffic leak. But if we've been unable to reconnect
			// for too long, release routing so the device isn't blocked forever.
			if iptablesUp && !downSince.IsZero() && time.Since(downSince) > reconnectGiveUp {
				log.Println("[tunnel] reconnect gave up after timeout — releasing routing until tunnel returns")
				teardownSession()
				sessionMode = ""
			}
			// 302 = carrier blocked the host; use longer backoff to avoid hammering
			if strings.Contains(err.Error(), "302") {
				log.Println("[tunnel] carrier block detected (302) — backing off 60s")
				if delay < 60*time.Second {
					delay = 60 * time.Second
				} else {
					delay = minDuration(delay*2, 5*time.Minute)
				}
			} else {
				delay = nextDelay(delay, baseDelay, maxDelay)
			}
			continue
		}

		// Connected.
		clientPtr.Store(c)
		downSince = time.Time{}
		st.set(func(s *State) {
			s.connected = true
			s.tunnelStart = time.Now()
			s.sshMode = cfg.SSHMode
			s.netMode = cfg.NetworkMode
			s.lastError = ""
			s.lastEvent = "connected"
		})
		log.Printf("[tunnel] connected")
		updateModuleProp("running", cfg.NetworkMode)

		// Bring the session up once, or rebuild it if the network mode changed.
		if listenerCancel == nil || sessionMode != cfg.NetworkMode {
			if listenerCancel != nil {
				listenerCancel()
				listenerCancel = nil
			}
			lctx, lcancel := context.WithCancel(ctx)
			listenerCancel = lcancel
			sessionMode = cfg.NetworkMode
			startListeners(lctx, cfg, curClient)
			time.Sleep(150 * time.Millisecond)
			runScriptTimeout(iptables, 30*time.Second, "enable")
			iptablesUp = true
			go wanIPRefresher(lctx, curClient, st)
		}

		// Wait for THIS client to die (or an explicit shutdown).
		waitDone := make(chan error, 1)
		go func() { waitDone <- c.Wait() }()
		select {
		case <-ctx.Done():
			c.Close()
			clientPtr.Store(nil)
			updateModuleProp("stopped", "")
			return // defer teardownSession removes routing on explicit stop
		case werr := <-waitDone:
			if werr != nil {
				log.Printf("[tunnel] SSH connection lost: %v", werr)
			}
		}

		// Transparent reconnect: keep listeners + iptables UP, clear the client,
		// and re-dial. Listeners refuse connections during the gap (fail-closed).
		log.Println("[tunnel] connection lost — reconnecting (routing kept up)")
		c.Close()
		clientPtr.Store(nil)
		downSince = time.Now()
		st.set(func(s *State) {
			s.connected = false
			s.wanIP = ""
			s.wanCountry = ""
			s.lastEvent = "reconnecting"
		})
		updateModuleProp("reconnecting", "")
		if ctx.Err() != nil {
			return
		}
		delay = baseDelay
	}
}

// nextDelay returns the next backoff delay (immediate → base → ×2 → cap).
func nextDelay(cur, base, max time.Duration) time.Duration {
	if cur <= 0 {
		return base
	}
	return minDuration(cur*2, max)
}

// startListeners brings up the SOCKS5 + mode-specific transparent/tproxy
// listeners. They are bound to the CURRENT SSH client via curClient(), so they
// keep running and pick up the new client across transparent reconnects.
func startListeners(ctx context.Context, cfg *config.Config, curClient func() *issh.Client) {
	socks := &proxy.SOCKS5Server{
		Addr:   fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort),
		Client: curClient,
	}
	go func() {
		if err := socks.ListenAndServe(ctx); err != nil {
			log.Printf("[socks5] %v", err)
		}
	}()

	switch cfg.NetworkMode {
	case "tproxy":
		tp := &proxy.TProxyServer{
			Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.TProxyPort),
			Client:  curClient,
			Timeout: 25 * time.Second,
		}
		go func() {
			if err := tp.ListenAndServe(ctx); err != nil {
				log.Printf("[tproxy] %v", err)
			}
		}()
	default: // redirect
		trans := &proxy.TransparentServer{
			Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.RedirPort),
			Client:  curClient,
			Timeout: 25 * time.Second,
		}
		go func() {
			if err := trans.ListenAndServe(ctx); err != nil {
				log.Printf("[transparent] %v", err)
			}
		}()
	}

	// DNS forwarder: ssh.iptables redirects device UDP:53 to 127.0.0.1:5353,
	// where this listener proxies the query as TCP DNS through the current SSH
	// client to 8.8.8.8:53. Survives reconnects via curClient().
	go func() {
		if err := dnsForwardLoop(ctx, "127.0.0.1:5353", "8.8.8.8:53", curClient); err != nil {
			log.Printf("[dns-forward] %v", err)
		}
	}()
}

// dnsForwardLoop runs a UDP listener that forwards DNS queries through the SSH
// client as TCP DNS (RFC 1035 §4.2.2: 2-byte length prefix + payload).
//
// The listener uses the CURRENT SSH client via curClient(), so it transparently
// picks up the new client after a reconnect. If no client is available (tunnel
// down), the query is silently dropped — better than answering with a bogus
// reply, the resolver will retry.
func dnsForwardLoop(ctx context.Context, listenAddr, upstream string, curClient func() *issh.Client) error {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve udp: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	log.Printf("[dns-forward] listening on %s, upstream=%s (via SSH)", listenAddr, upstream)

	// Close the socket on context cancel so ReadFromUDP returns and we exit.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	defer conn.Close()

	buf := make([]byte, 1500) // largest UDP DNS message in practice
	for {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Read deadline tick — just loop back to check ctx.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return fmt.Errorf("read udp: %w", err)
		}
		if n < 12 { // minimum DNS header
			continue
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go forwardOneDNSQuery(ctx, conn, src, query, upstream, curClient)
	}
}

// forwardOneDNSQuery proxies a single UDP DNS query as TCP DNS through the SSH
// tunnel and writes the response back to the original UDP client.
func forwardOneDNSQuery(
	ctx context.Context,
	listener *net.UDPConn,
	src *net.UDPAddr,
	query []byte,
	upstream string,
	curClient func() *issh.Client,
) {
	c := curClient()
	if c == nil {
		// Tunnel down — drop. The resolver will retry; better than a bogus reply.
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tcp, err := c.DialTCP(dialCtx, "tcp", upstream)
	if err != nil {
		log.Printf("[dns-forward] dial %s: %v", upstream, err)
		return
	}
	defer tcp.Close()
	tcp.SetDeadline(time.Now().Add(5 * time.Second))

	// TCP DNS framing: 2-byte big-endian length prefix + payload (RFC 1035).
	frame := make([]byte, 2+len(query))
	frame[0] = byte(len(query) >> 8)
	frame[1] = byte(len(query) & 0xff)
	copy(frame[2:], query)
	if _, err := tcp.Write(frame); err != nil {
		return
	}

	var lenHdr [2]byte
	if _, err := io.ReadFull(tcp, lenHdr[:]); err != nil {
		return
	}
	respLen := int(lenHdr[0])<<8 | int(lenHdr[1])
	if respLen <= 0 || respLen > 65535 {
		return
	}
	resp := make([]byte, respLen)
	if _, err := io.ReadFull(tcp, resp); err != nil {
		return
	}

	// Send the DNS payload (without the 2-byte TCP length prefix) back over UDP.
	if _, err := listener.WriteToUDP(resp, src); err != nil {
		// Best-effort; client may have already given up.
		return
	}
}



// wanIPRefresher caches the tunnel-side public IP (refreshes every 5 min),
// using whatever client is current.
func wanIPRefresher(ctx context.Context, curClient func() *issh.Client, st *State) {
	for {
		if c := curClient(); c != nil {
			if ip, country := fetchPublicIP(c); ip != "" {
				st.set(func(s *State) { s.wanIP = ip; s.wanCountry = country })
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
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
	case "start", "restart", "start-idle":
		runScriptTimeout(script, 30*time.Second, action)
	case "stop":
		updateModuleProp("stopped", "")
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

			ac := 0
			if c := clientPtr.Load(); c != nil {
				ac = c.ActiveConns()
			}

			st.set(func(s *State) {
				s.memRSS = rss
				s.cpuPct = cpu
				s.upBps = up
				s.downBps = down
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

// updateModuleProp updates the module.prop description field so KSU/Magisk
// module managers show the current daemon status (running/reconnecting/stopped).
func updateModuleProp(status, networkMode string) {
	const propPath = "/data/adb/modules/sshcustom-vpnchain/module.prop"
	var desc string
	switch status {
	case "running":
		desc = "[ \xf0\x9f\x9f\xa2 ] SSHCustom-VPNChain running | mode=" + networkMode
	case "reconnecting":
		desc = "[ \xf0\x9f\x9f\xa1 ] SSHCustom-VPNChain reconnecting..."
	case "stopped":
		desc = "[ \xf0\x9f\x94\xb4 ] SSHCustom-VPNChain stopped"
	default:
		desc = "[ \xf0\x9f\x92\xa4 ] SSHCustom-VPNChain idle"
	}
	cmd := exec.Command("/system/bin/sh", "-c",
		fmt.Sprintf(`sed -i 's|^description=.*|description=%s|' '%s'`, desc, propPath))
	_ = cmd.Run()
}


