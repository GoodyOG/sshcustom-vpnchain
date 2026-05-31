// sshcustomd — SSHCustom-VPNChain daemon
// Single static binary, GOOS=android GOARCH=arm64 CGO_ENABLED=0.
//
// Usage:
//   sshcustomd run -c /data/adb/sshcustom/settings.ini -w /data/adb/sshcustom [--idle]
//   sshcustomd version
package main

import (
	"bufio"
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
	"github.com/GoodyOG/SSHCustom_Magisk/internal/proxy"
	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/webui"
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
	poolSize     int
	poolHealthy  int
	standby      bool

	// VPN chain state
	vpnChainState    string
	vpnChainExitIP   string
	vpnChainLocation string

	// Latency measurements
	latencyGoogle     int
	latencyCloudflare int
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
		Connected:           s.connected,
		UptimeSeconds:       uptime,
		SSHMode:             s.sshMode,
		NetworkMode:         s.netMode,
		ActiveConns:         s.activeConns,
		Version:             s.version,
		MemRSSMB:            float64(s.memRSS) / 1024 / 1024,
		CPUPercent:          s.cpuPct,
		UpKbps:              s.upBps / 1024,
		DownKbps:            s.downBps / 1024,
		LastError:           s.lastError,
		ChannelPoolSize:     s.poolSize,
		ChannelPoolAvail:    s.poolHealthy,
		Standby:             s.standby,
		PoolSize:            s.poolSize,
		PoolHealthy:         s.poolHealthy,
		VpnChainState:       s.vpnChainState,
		VpnChainExitIP:      s.vpnChainExitIP,
		VpnChainLocation:    s.vpnChainLocation,
		LatencyGoogleMs:     s.latencyGoogle,
		LatencyCloudflareMs: s.latencyCloudflare,
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
		Handler:      corsMiddleware(mux),
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

	// Pool replaces the single client. curClient() calls pool.Pick() so proxy
	// servers and DNS forwarder get round-robin selection among alive connections.
	var pool *issh.Pool
	curClient := func() *issh.Client {
		if pool == nil {
			return nil
		}
		return pool.Pick()
	}

	// A "session" = the local listeners + iptables. It is brought up once on the
	// first successful connect and KEPT UP across transparent SSH reconnects, so
	// a brief SSH drop no longer flaps routing -- apps just stall ~1s with no
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
	noRouteCount := 0       // consecutive checks with no default route

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		cfg := atomicCfg.Get()
		if cfg.SSHHost == "" || cfg.SSHUser == "" {
			log.Println("[tunnel] ssh_host/ssh_user not configured -- waiting")
			delay = 10 * time.Second
			continue
		}

		// Network standby: check for a default route before dialing.
		if !hasDefaultRoute() {
			noRouteCount++
			if noRouteCount >= 2 {
				log.Println("[tunnel] no default route -- entering standby")
				teardownSession()
				if pool != nil {
					pool.CloseAll()
					pool = nil
				}
				clientPtr.Store(nil)
				st.set(func(s *State) {
					s.standby = true
					s.connected = false
					s.lastEvent = "standby"
				})
				updateModuleProp("standby", "")
				// Poll every 10s until route returns
				for {
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Second):
					}
					if hasDefaultRoute() {
						log.Println("[tunnel] default route restored -- resuming")
						st.set(func(s *State) { s.standby = false; s.lastEvent = "resuming" })
						noRouteCount = 0
						break
					}
				}
				delay = 0
				continue
			}
			delay = 20 * time.Second
			continue
		}
		noRouteCount = 0

		log.Printf("[tunnel] connecting to %s:%d mode=%s", cfg.SSHHost, cfg.SSHPort, cfg.SSHMode)
		st.set(func(s *State) { s.lastError = ""; s.lastEvent = "connecting" })

		poolSize := cfg.ChannelPoolSize
		if poolSize < 1 || !cfg.ChannelPool {
			poolSize = 1
		}
		pool = issh.NewPool(poolSize)

		dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
		err := pool.Dial(dialCtx, issh.ConnectConfig{
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
			pool = nil
			// Keep the session (iptables + listeners) UP across retries --
			// fail-closed, no traffic leak. But if we've been unable to reconnect
			// for too long, release routing so the device isn't blocked forever.
			if iptablesUp && !downSince.IsZero() && time.Since(downSince) > reconnectGiveUp {
				log.Println("[tunnel] reconnect gave up after timeout -- releasing routing until tunnel returns")
				teardownSession()
				sessionMode = ""
			}
			// 302 = carrier blocked the host; use longer backoff to avoid hammering
			if strings.Contains(err.Error(), "302") {
				log.Println("[tunnel] carrier block detected (302) -- backing off 60s")
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

		// Connected. Store one client for backward compat (metrics uses it).
		c := pool.Pick()
		clientPtr.Store(c)
		downSince = time.Time{}
		st.set(func(s *State) {
			s.connected = true
			s.tunnelStart = time.Now()
			s.sshMode = cfg.SSHMode
			s.netMode = cfg.NetworkMode
			s.lastError = ""
			s.lastEvent = "connected"
			s.poolSize = pool.Size()
			s.poolHealthy = pool.Healthy()
		})
		log.Printf("[tunnel] connected (pool size=%d healthy=%d)", pool.Size(), pool.Healthy())
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
			pool.CloseAll()
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
		log.Println("[tunnel] connection lost -- reconnecting (routing kept up)")
		pool.CloseAll()
		pool = nil
		clientPtr.Store(nil)
		downSince = time.Now()
		st.set(func(s *State) {
			s.connected = false
			s.wanIP = ""
			s.wanCountry = ""
			s.lastEvent = "reconnecting"
			s.poolHealthy = 0
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

// startListeners brings up the SOCKS5 + transparent redirect listener.
// They are bound to the CURRENT SSH client via curClient(), so they
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



// hasDefaultRoute checks if the device currently has a default route by running
// `ip route get 1.1.1.1`. Returns true if the command succeeds (route exists).
func hasDefaultRoute() bool {
	cmd := exec.Command("ip", "route", "get", "1.1.1.1")
	return cmd.Run() == nil
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

// corsMiddleware wraps an http.Handler adding CORS headers to every response.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Profile represents a saved SSH connection profile (v1.0.1 nested model).
type Profile struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Selected  bool           `json:"selected,omitempty"`
	SSH       ProfileSSH     `json:"ssh"`
	Transport ProfileTransport `json:"transport"`
}

type ProfileSSH struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	AuthType string `json:"auth_type,omitempty"`
}

type ProfileTransport struct {
	Mode      string           `json:"mode"`
	HTTPProxy ProfileHTTPProxy `json:"http_proxy,omitempty"`
	TLS       ProfileTLS       `json:"tls,omitempty"`
	Payload   ProfilePayload   `json:"payload,omitempty"`
}

type ProfileHTTPProxy struct {
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

type ProfileTLS struct {
	ServerName string `json:"server_name,omitempty"`
}

type ProfilePayload struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Template string `json:"template,omitempty"`
}

type ProfilesFile struct {
	SelectedID string    `json:"selected_id"`
	Profiles   []Profile `json:"profiles"`
}

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

	// ── GET /api/v1/status (v1.0.1 contract) ──────────────────────────────────

	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		cfg := atomicCfg.Get()
		st.mu.RLock()
		connected := st.connected
		standbyVal := st.standby
		lastError := st.lastError
		lastEvent := st.lastEvent
		tunnelUptime := int64(0)
		if st.connected && !st.tunnelStart.IsZero() {
			tunnelUptime = int64(time.Since(st.tunnelStart).Seconds())
		}
		totalUptime := int64(time.Since(st.startedAt).Seconds())
		poolSz := st.poolSize
		poolH := st.poolHealthy
		activeC := st.activeConns
		cpuP := st.cpuPct
		memR := st.memRSS
		sshM := st.sshMode
		st.mu.RUnlock()

		state := "STOPPED"
		if connected {
			state = "CONNECTED"
		} else if standbyVal {
			state = "PAUSED_NO_NETWORK"
		} else if lastEvent == "connecting" || lastEvent == "reconnecting" || lastEvent == "connect_failed" || lastEvent == "resuming" {
			state = "CONNECTING"
		}

		running := connected || lastEvent == "reconnecting" || standbyVal || lastEvent == "connecting"
		sourceIP := getSourceIP()
		sysMemPct := getSystemMemPct()
		memMB := int(memR / 1024 / 1024)

		dnsMode := cfg.DNSMode
		if dnsMode == "" {
			dnsMode = "device"
		}
		quicVal := cfg.QUIC
		if quicVal == "" {
			quicVal = "disable"
		}
		dnsServers := []string{}
		if cfg.DNSServers != "" {
			for _, s := range strings.Split(cfg.DNSServers, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					dnsServers = append(dnsServers, s)
				}
			}
		}

		runtimeData := map[string]interface{}{
			"connected": connected, "ssh_authenticated": connected, "running": running,
			"state": state, "last_error": lastError, "last_event": lastEvent, "version": version,
			"tunnel_uptime_seconds": tunnelUptime, "uptime_seconds": totalUptime,
			"pool_size": poolSz, "pool_healthy": poolH, "pool_streams": activeC,
			"pool_max_streams": poolSz * 8, "cpu_percent": cpuP,
			"memory_rss_bytes": memR, "memory_rss_mb": memMB,
			"system_mem_used_percent": sysMemPct, "source_ip": sourceIP,
			"interface": "", "gateway": "", "network_online": sourceIP != "",
			"selected_mode": sshM, "transport_chain": sshM, "payload_enabled": cfg.PayloadEnabled,
			"resolver_method": "android_dnsx", "resolved_ips": []string{}, "dns_mode": dnsMode,
			"socks_running": connected, "socks_addr": fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort),
			"transparent_running": connected, "transparent_addr": fmt.Sprintf("0.0.0.0:%d", cfg.RedirPort),
			"hotspot_enabled": cfg.HotspotSharing,
		}

		configData := map[string]interface{}{
			"dns": map[string]interface{}{"mode": dnsMode, "servers": dnsServers},
			"hotspot": map[string]interface{}{"enabled": cfg.HotspotSharing, "tcp": cfg.HotspotSharing},
			"quic": quicVal, "ipv6": cfg.IPv6, "channel_pool_size": cfg.ChannelPoolSize,
			"network_mode": "redirect", "socks_port": cfg.SocksPort, "redir_port": cfg.RedirPort,
		}

		paths := map[string]string{
			"work_dir": workDir, "config_path": workDir + "/settings.ini",
			"run_dir": workDir + "/run", "webroot": workDir + "/webroot",
		}

		env(w, true, map[string]interface{}{"runtime": runtimeData, "config": configData, "paths": paths}, "")
	})

	// ── GET /api/v1/diagnostics ───────────────────────────────────────────────

	mux.HandleFunc("/api/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		ps := st.poolSize
		ph := st.poolHealthy
		ac := st.activeConns
		st.mu.RUnlock()
		srcIP := getSourceIP()
		env(w, true, map[string]interface{}{
			"pool": map[string]interface{}{
				"healthy": ph, "size": ps, "streams": ac, "max_streams": ps * 8,
				"capacity_hint": fmt.Sprintf("%d/%d healthy, %d active streams", ph, ps, ac),
			},
			"source_ip": srcIP,
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

	// ── POST /api/v1/config (flexible body) ───────────────────────────────────

	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := atomicCfg.Get()
			env(w, true, cfg, "")
		case http.MethodPost:
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				env(w, false, nil, "invalid JSON body")
				return
			}
			patch := map[string]string{}
			doRestart := false

			if raw, ok := body["dns"]; ok {
				var dns struct {
					Mode    string   `json:"mode"`
					Servers []string `json:"servers"`
				}
				json.Unmarshal(raw, &dns)
				if dns.Mode != "" {
					patch["dns_mode"] = dns.Mode
				}
				if len(dns.Servers) > 0 {
					patch["dns_servers"] = strings.Join(dns.Servers, ",")
				}
			}
			if raw, ok := body["hotspot"]; ok {
				var hs struct {
					Enabled bool `json:"enabled"`
					TCP     bool `json:"tcp"`
				}
				json.Unmarshal(raw, &hs)
				patch["hotspot_sharing"] = strconv.FormatBool(hs.Enabled || hs.TCP)
			}
			if raw, ok := body["quic"]; ok {
				var q string
				if json.Unmarshal(raw, &q) == nil && q != "" {
					patch["quic"] = q
				}
			}
			if raw, ok := body["ipv6"]; ok {
				var bVal bool
				if json.Unmarshal(raw, &bVal) == nil {
					patch["ipv6"] = strconv.FormatBool(bVal)
				} else {
					var sVal string
					if json.Unmarshal(raw, &sVal) == nil {
						patch["ipv6"] = sVal
					}
				}
			}
			if raw, ok := body["channel_pool_size"]; ok {
				var n int
				if json.Unmarshal(raw, &n) == nil && n >= 1 && n <= 8 {
					patch["channel_pool_size"] = strconv.Itoa(n)
				}
			}
			if raw, ok := body["restart"]; ok {
				var rb bool
				json.Unmarshal(raw, &rb)
				doRestart = rb
			}

			if len(patch) > 0 {
				if err := patchSettingsINI(cfgPath, patch); err != nil {
					env(w, false, nil, err.Error())
					return
				}
			}
			syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
			if doRestart {
				go func() {
					time.Sleep(300 * time.Millisecond)
					handleControl("restart", workDir)
				}()
			}
			env(w, true, map[string]string{"status": "updated"}, "")
		default:
			w.WriteHeader(405)
		}
	})

	// ── GET /api/v1/network/public-ip ─────────────────────────────────────────

	mux.HandleFunc("/api/v1/network/public-ip", func(w http.ResponseWriter, r *http.Request) {
		ip, country := st.wanInfo()
		tunnelData := map[string]interface{}{
			"ok": ip != "", "ip": ip, "country": country,
			"city": "", "region": "", "isp": "", "asn": "", "cached": true,
		}
		env(w, true, map[string]interface{}{"tunnel": tunnelData}, "")
	})

	// Latency ping endpoint
	mux.HandleFunc("/api/v1/network/ping", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		c := clientPtr.Load()
		if c == nil {
			env(w, false, nil, "no active tunnel connection")
			return
		}

		measureTCP := func(addr string) int {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel2()
			start := time.Now()
			conn, err := c.DialTCP(ctx2, "tcp", addr)
			if err != nil {
				return -1
			}
			elapsed := int(time.Since(start).Milliseconds())
			conn.Close()
			return elapsed
		}

		googleMs := measureTCP("google.com:443")
		cloudflareMs := measureTCP("1.1.1.1:443")

		st.set(func(s *State) {
			s.latencyGoogle = googleMs
			s.latencyCloudflare = cloudflareMs
		})

		env(w, true, map[string]int{
			"google_ms":     googleMs,
			"cloudflare_ms": cloudflareMs,
		}, "")
	})

	// ── VPN Chain (shell out to vpnchain.sh) ──────────────────────────────────

	vpnchainScript := workDir + "/vpnchain/vpnchain.sh"

	vpnchainRun := func(args ...string) (string, error) {
		cmdArgs := append([]string{vpnchainScript}, args...)
		cmd := exec.Command("/system/bin/sh", cmdArgs...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	mux.HandleFunc("/api/v1/vpnchain/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			Location string `json:"location"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			env(w, false, nil, "invalid JSON body")
			return
		}
		if req.Location == "" {
			env(w, false, nil, "location is required")
			return
		}
		out, err := vpnchainRun("start", req.Location)
		if err != nil {
			env(w, false, nil, fmt.Sprintf("start failed: %s", out))
			return
		}
		env(w, true, map[string]string{"status": "started", "output": out}, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		out, err := vpnchainRun("stop")
		if err != nil {
			env(w, false, nil, fmt.Sprintf("stop failed: %s", out))
			return
		}
		env(w, true, map[string]string{"status": "stopped", "output": out}, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/switch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			Location string `json:"location"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			env(w, false, nil, "invalid JSON body")
			return
		}
		if req.Location == "" {
			env(w, false, nil, "location is required")
			return
		}
		out, err := vpnchainRun("switch", req.Location)
		if err != nil {
			env(w, false, nil, fmt.Sprintf("switch failed: %s", out))
			return
		}
		env(w, true, map[string]string{"status": "switched", "output": out}, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/status", func(w http.ResponseWriter, r *http.Request) {
		out, err := vpnchainRun("status")
		if err != nil {
			env(w, true, map[string]interface{}{"running": false, "location": "", "ip": ""}, "")
			return
		}
		var statusData map[string]interface{}
		if json.Unmarshal([]byte(out), &statusData) != nil {
			env(w, true, map[string]interface{}{"running": false, "location": "", "ip": ""}, "")
			return
		}
		env(w, true, statusData, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/locations", func(w http.ResponseWriter, r *http.Request) {
		out, _ := vpnchainRun("locations")
		var locations []string
		if out != "" {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					locations = append(locations, line)
				}
			}
		}
		if locations == nil {
			locations = []string{}
		}
		env(w, true, locations, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/log", func(w http.ResponseWriter, r *http.Request) {
		logPath := workDir + "/vpnchain/run/vpnchain.log"
		lines := tailFile(logPath, 400)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))
	})

	// ── Logs (plain text) ─────────────────────────────────────────────────────

	logTypeMap := map[string]string{
		"core":    "run/sshcustom.log",
		"control": "run/boot.log",
		"action":  "run/tool.log",
	}

	mux.HandleFunc("/api/v1/logs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/logs/")
		parts := strings.SplitN(path, "/", 2)
		logType := parts[0]
		isClear := len(parts) == 2 && parts[1] == "clear"

		relPath, ok := logTypeMap[logType]
		if !ok {
			env(w, false, nil, "unknown log type: "+logType)
			return
		}
		logPath := workDir + "/" + relPath

		if isClear {
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			os.Truncate(logPath, 0)
			env(w, true, nil, "")
			return
		}

		// Return plain text tail
		lines := tailFile(logPath, 400)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))
	})

	// ── Profiles (v1.0.1 nested model) ────────────────────────────────────────

	profilesPath := workDir + "/profiles.json"

	mux.HandleFunc("/api/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			pf := loadProfilesFile(profilesPath)
			env(w, true, map[string]interface{}{
				"selected_id": pf.SelectedID,
				"profiles":    pf.Profiles,
			}, "")
			return
		}
		w.WriteHeader(405)
	})

	mux.HandleFunc("/api/v1/profile/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			ID        string           `json:"id"`
			Name      string           `json:"name"`
			Select    bool             `json:"select"`
			Restart   bool             `json:"restart"`
			SSH       ProfileSSH       `json:"ssh"`
			Transport ProfileTransport `json:"transport"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			env(w, false, nil, "invalid JSON body")
			return
		}
		pf := loadProfilesFile(profilesPath)
		if req.ID == "" {
			req.ID = generateID()
		}
		p := Profile{ID: req.ID, Name: req.Name, SSH: req.SSH, Transport: req.Transport}
		found := false
		for i := range pf.Profiles {
			if pf.Profiles[i].ID == req.ID {
				pf.Profiles[i] = p
				found = true
				break
			}
		}
		if !found {
			pf.Profiles = append(pf.Profiles, p)
		}
		if req.Select {
			pf.SelectedID = req.ID
			applyProfileToSettings(cfgPath, &p)
			syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
		}
		saveProfilesFile(profilesPath, pf)
		doRestart := req.Restart
		if doRestart {
			go func() {
				time.Sleep(300 * time.Millisecond)
				handleControl("restart", workDir)
			}()
		}
		env(w, true, map[string]interface{}{"selected_id": pf.SelectedID, "restart": doRestart}, "")
	})

	mux.HandleFunc("/api/v1/profile/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			SelectedID string `json:"selected_id"`
			Restart    bool   `json:"restart"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			env(w, false, nil, "invalid JSON body")
			return
		}
		pf := loadProfilesFile(profilesPath)
		pf.SelectedID = req.SelectedID
		var target *Profile
		for i := range pf.Profiles {
			if pf.Profiles[i].ID == req.SelectedID {
				target = &pf.Profiles[i]
				break
			}
		}
		if target != nil {
			applyProfileToSettings(cfgPath, target)
			syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
		}
		saveProfilesFile(profilesPath, pf)
		if req.Restart {
			go func() {
				time.Sleep(300 * time.Millisecond)
				handleControl("restart", workDir)
			}()
		}
		env(w, true, map[string]interface{}{"selected_id": pf.SelectedID, "restart": req.Restart}, "")
	})

	mux.HandleFunc("/api/v1/profile/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			env(w, false, nil, "invalid JSON body")
			return
		}
		pf := loadProfilesFile(profilesPath)
		var filtered []Profile
		for _, p := range pf.Profiles {
			if p.ID != req.ID {
				filtered = append(filtered, p)
			}
		}
		pf.Profiles = filtered
		if pf.SelectedID == req.ID {
			pf.SelectedID = ""
		}
		saveProfilesFile(profilesPath, pf)
		env(w, true, map[string]interface{}{"deleted": req.ID}, "")
	})

	// ── WebUI static file serving ─────────────────────────────────────────────

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		webrootDir := workDir + "/webroot/"
		// Serve static files from webroot directory
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			filePath := webrootDir + strings.TrimPrefix(r.URL.Path, "/")
			if _, err := os.Stat(filePath); err == nil {
				http.ServeFile(w, r, filePath)
				return
			}
		}
		// For "/" or "/index.html", serve webroot/index.html if it exists
		webrootIndex := webrootDir + "index.html"
		if _, err := os.Stat(webrootIndex); err == nil {
			http.ServeFile(w, r, webrootIndex)
			return
		}
		// Fallback to embedded index
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(webui.IndexHTML)
	})

	return mux
}

// tailFile reads the last N lines from a file.
func tailFile(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// patchSettingsINI reads settings.ini, updates matching key= lines, writes back.
func patchSettingsINI(path string, patch map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	used := make(map[string]bool)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eqIdx := strings.IndexByte(trimmed, '=')
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eqIdx])
		if val, ok := patch[key]; ok {
			lines[i] = key + "=" + val
			used[key] = true
		}
	}

	// Append any keys that weren't found
	for k, v := range patch {
		if !used[k] {
			lines = append(lines, k+"="+v)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// loadProfilesFile reads the profiles JSON file (v1.0.1 format).
func loadProfilesFile(path string) ProfilesFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProfilesFile{Profiles: []Profile{}}
	}
	var pf ProfilesFile
	if err := json.Unmarshal(data, &pf); err != nil {
		// Try legacy array format
		var profiles []Profile
		if json.Unmarshal(data, &profiles) == nil {
			return ProfilesFile{Profiles: profiles}
		}
		return ProfilesFile{Profiles: []Profile{}}
	}
	if pf.Profiles == nil {
		pf.Profiles = []Profile{}
	}
	return pf
}

// saveProfilesFile writes the profiles JSON file.
func saveProfilesFile(path string, pf ProfilesFile) {
	if pf.Profiles == nil {
		pf.Profiles = []Profile{}
	}
	data, _ := json.MarshalIndent(pf, "", "  ")
	os.WriteFile(path, data, 0644)
}

// applyProfileToSettings writes profile fields to settings.ini.
func applyProfileToSettings(cfgPath string, p *Profile) {
	// Map transport mode
	sshMode := p.Transport.Mode
	switch sshMode {
	case "direct":
		sshMode = "direct"
	case "tls_sni":
		sshMode = "sni"
	case "http_proxy":
		sshMode = "sni_http_proxy"
	}
	patch := map[string]string{
		"ssh_host":        p.SSH.Host,
		"ssh_port":        strconv.Itoa(p.SSH.Port),
		"ssh_user":        p.SSH.Username,
		"ssh_password":    p.SSH.Password,
		"ssh_mode":        sshMode,
		"ssh_sni_host":    p.Transport.TLS.ServerName,
		"http_proxy_host": p.Transport.HTTPProxy.Host,
		"http_proxy_port": strconv.Itoa(p.Transport.HTTPProxy.Port),
		"payload_enabled": strconv.FormatBool(p.Transport.Payload.Enabled),
		"payload":         p.Transport.Payload.Template,
	}
	patchSettingsINI(cfgPath, patch)
}

// generateID produces a simple unique ID based on timestamp.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// getSourceIP returns the local source IP by dialing UDP to 8.8.8.8:80.
func getSourceIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// getSystemMemPct reads /proc/meminfo and returns (Total-Available)/Total*100.
func getSystemMemPct() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, avail uint64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(strings.TrimPrefix(line, "MemTotal:"), "%d", &total)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(strings.TrimPrefix(line, "MemAvailable:"), "%d", &avail)
		}
	}
	if total == 0 {
		return 0
	}
	return float64(total-avail) / float64(total) * 100.0
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
	case "standby":
		desc = "[ \xf0\x9f\x92\xa4 ] SSHCustom-VPNChain standby (no route)"
	default:
		desc = "[ \xf0\x9f\x92\xa4 ] SSHCustom-VPNChain idle"
	}
	cmd := exec.Command("/system/bin/sh", "-c",
		fmt.Sprintf(`sed -i 's|^description=.*|description=%s|' '%s'`, desc, propPath))
	_ = cmd.Run()
}


