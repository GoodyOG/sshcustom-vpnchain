package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sync/atomic"

	"github.com/GoodyOG/SSHCustom_Magisk/internal/apiv1"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/dnsx"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/metrics"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/version"
	"github.com/GoodyOG/SSHCustom_Magisk/internal/webui"
	xssh "golang.org/x/crypto/ssh"
)

// Version is set by -ldflags from the repository-root VERSION file at build
// time. The package alias keeps the existing call sites working.
var Version = version.Version



const defaultCopyBufferSize = 128 * 1024
const maxCopyBufferSize = 256 * 1024

type Config struct {
	Module struct {
		Name        string `json:"name"`
		WorkDir     string `json:"work_dir"`
		LogDir      string `json:"log_dir"`
		ManualStart bool   `json:"manual_start"`
	} `json:"module"`
	API struct {
		Enabled bool   `json:"enabled"`
		Host    string `json:"host"`
		Port    int    `json:"port"`
	} `json:"api"`
	DNS struct {
		Enabled    bool     `json:"enabled"`
		Mode       string   `json:"mode"`
		Servers    []string `json:"servers"`
		TimeoutSec int      `json:"timeout_seconds"`
	} `json:"dns"`
	Transport struct {
		SupportedModes []string `json:"supported_modes"`
	} `json:"transport"`
	LocalProxy struct {
		SocksEnabled bool   `json:"socks_enabled"`
		SocksHost    string `json:"socks_host"`
		SocksPort    int    `json:"socks_port"`
	} `json:"local_proxy"`
	TransparentProxy struct {
		Enabled      bool   `json:"enabled"`
		TCPPort      int    `json:"tcp_port"`
		UDPPort      int    `json:"udp_port"`
		UDPGWPort    int    `json:"udpgw_port"`
		ChainsPrefix string `json:"chains_prefix"`
	} `json:"transparent_proxy"`
	UDPProxy struct {
		Enabled    bool `json:"enabled"`
		UDPGWPort  int  `json:"udpgw_port"`
	} `json:"udp_proxy"`
	Hotspot struct {
		Enabled    bool     `json:"enabled"`
		TCP        bool     `json:"tcp"`
		DNS        bool     `json:"dns"`
		Interfaces []string `json:"interfaces"`
	} `json:"hotspot"`
	Performance struct {
		BufferSize              int  `json:"buffer_size"`
		ConnectTimeoutSec       int  `json:"connect_timeout_seconds"`
		KeepAliveSec            int  `json:"keepalive_seconds"`
		SSHPoolSize             int  `json:"ssh_pool_size"`
		MaxStreamsPerSSH        int  `json:"max_streams_per_ssh"`
		StreamIdleTimeoutSec    int  `json:"stream_idle_timeout_seconds"`
		StreamAcquireTimeoutSec int  `json:"stream_acquire_timeout_seconds"`
		VerboseTransparentLogs  bool `json:"verbose_transparent_logs"`
		RetryInitialDelaySec    int  `json:"retry_initial_delay_seconds"`
		RetryMaxDelaySec        int  `json:"retry_max_delay_seconds"`
		ReadProbeTimeoutSec     int  `json:"read_probe_timeout_seconds"`
	} `json:"performance"`
}

type ProfilesFile struct {
	SelectedID string    `json:"selected_id"`
	Profiles   []Profile `json:"profiles"`
}

type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Selected  bool   `json:"selected,omitempty"`
	SSH       SSH    `json:"ssh"`
	Transport TConf  `json:"transport"`
}

type SSH struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	AuthType    string   `json:"auth_type"`
	FallbackIPs []string `json:"fallback_ips,omitempty"`
}

type TConf struct {
	Mode      string        `json:"mode"`
	Chain     []string      `json:"chain"`
	HTTPProxy *HTTPProxyCfg `json:"http_proxy,omitempty"`
	TLS       *TLSCfg       `json:"tls,omitempty"`
	Payload   PayloadCfg    `json:"payload"`
}

type HTTPProxyCfg struct {
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	ConnectMethod string   `json:"connect_method"`
	FallbackIPs   []string `json:"fallback_ips,omitempty"`
}

type TLSCfg struct {
	Enabled            bool     `json:"enabled"`
	ServerName         string   `json:"server_name"`
	InsecureSkipVerify bool     `json:"insecure_skip_verify"`
	ALPN               []string `json:"alpn"`
}

type PayloadCfg struct {
	Enabled       bool   `json:"enabled"`
	Template      string `json:"template"`
	SendTiming    string `json:"send_timing"`
	ReadResponse  bool   `json:"read_response"`
	AllowStatuses []int  `json:"allow_http_status"`
}

type State struct {
	mu                  sync.RWMutex
	StartedAt           time.Time `json:"started_at"`
	TunnelStartedAt     time.Time `json:"tunnel_started_at"`
	State               string    `json:"state"`
	Running             bool      `json:"running"`
	Connected           bool      `json:"connected"`
	SSHAuthenticated    bool      `json:"ssh_authenticated"`
	TransportReady      bool      `json:"transport_ready"`
	Phase               string    `json:"phase"`
	Version             string    `json:"version"`
	GOOS                string    `json:"goos"`
	GOARCH              string    `json:"goarch"`
	WorkDir             string    `json:"work_dir"`
	ConfigPath          string    `json:"config_path"`
	ProfilesPath        string    `json:"profiles_path"`
	SelectedProfile     string    `json:"selected_profile"`
	SelectedMode        string    `json:"selected_mode"`
	TransportChain      string    `json:"transport_chain"`
	PayloadEnabled      bool      `json:"payload_enabled"`
	LastError           string    `json:"last_error"`
	LastEvent           string    `json:"last_event"`
	Attempt             int       `json:"attempt"`
	NetworkOnline       bool      `json:"network_online"`
	DefaultRoute        string    `json:"default_route"`
	Interface           string    `json:"interface"`
	Gateway             string    `json:"gateway"`
	SourceIP            string    `json:"source_ip"`
	HotspotEnabled      bool      `json:"hotspot_enabled"`
	SocksEnabled        bool      `json:"socks_enabled"`
	SocksAddr           string    `json:"socks_addr"`
	SocksRunning        bool      `json:"socks_running"`
	TransparentEnabled  bool      `json:"transparent_enabled"`
	TransparentAddr     string    `json:"transparent_addr"`
	TransparentRunning  bool      `json:"transparent_running"`
	TransparentApplied  bool      `json:"transparent_applied"`
	UDPProxyEnabled     bool      `json:"udp_proxy_enabled"`
	UDPProxyRunning     bool      `json:"udp_proxy_running"`
	UDPGWPort           int       `json:"udpgw_port"`
	HotspotRunning      bool      `json:"hotspot_running"`
	CPUPercent          float64   `json:"cpu_percent"`
	MemoryRSSBytes      uint64    `json:"memory_rss_bytes"`
	MemoryRSSMB         float64   `json:"memory_rss_mb"`
	SystemMemTotalBytes uint64    `json:"system_mem_total_bytes"`
	SystemMemAvailBytes uint64    `json:"system_mem_available_bytes"`
	SystemMemUsedPct    float64   `json:"system_mem_used_percent"`
	Goroutines          int       `json:"goroutines"`
	RemoteBanner        string    `json:"remote_banner"`
	HTTPStatuses        []int     `json:"http_statuses"`
	APIAddr             string    `json:"api_addr"`
	ResolvedDial        string    `json:"resolved_dial"`
	ResolverMethod      string    `json:"resolver_method"`
	ResolvedIPs         []string  `json:"resolved_ips"`
	DNSMode             string    `json:"dns_mode"`
	DNSServers          []string  `json:"dns_servers"`
	PoolSize            int       `json:"pool_size"`
	PoolHealthy         int       `json:"pool_healthy"`
	PoolReconnecting    int       `json:"pool_reconnecting"`
	PoolStreams         int       `json:"pool_streams"`
	PoolMaxStreams      int       `json:"pool_max_streams"`
	PoolLastError       string    `json:"pool_last_error"`
	Note                string    `json:"note"`

	// SSE broadcast plumbing. Subscribers are kept on a slice guarded by
	// subsMu; broadcast() notifies all of them with a non-blocking send so a
	// slow client never stalls a state mutation.
	subsMu sync.Mutex
	subs   []chan struct{}
}

func (s *State) set(fn func()) {
	s.mu.Lock()
	fn()
	s.mu.Unlock()
	s.broadcast()
}

// Subscribe registers a listener that is notified whenever state changes. The
// returned channel buffers a single pending notification. Callers must invoke
// the returned cleanup func when done. Notifications coalesce: missing one is
// always safe because consumers re-read the full snapshot anyway.
func (s *State) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.subsMu.Lock()
	s.subs = append(s.subs, ch)
	s.subsMu.Unlock()
	cleanup := func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				break
			}
		}
	}
	return ch, cleanup
}

func (s *State) broadcast() {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
			// Subscriber already has a pending notification; coalesce.
		}
	}
}

func (s *State) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uptime := int64(0)
	if !s.StartedAt.IsZero() {
		uptime = int64(time.Since(s.StartedAt).Seconds())
	}
	tunnelUptime := int64(0)
	if !s.TunnelStartedAt.IsZero() && s.Running {
		tunnelUptime = int64(time.Since(s.TunnelStartedAt).Seconds())
	}
	return map[string]any{
		"started_at":              s.StartedAt.Format(time.RFC3339),
		"uptime_seconds":          uptime,
		"tunnel_uptime_seconds":   tunnelUptime,
		"state":                   s.State,
		"running":                 s.Running,
		"connected":               s.Connected,
		"ssh_authenticated":       s.SSHAuthenticated,
		"transport_ready":         s.TransportReady,
		"phase":                   s.Phase,
		"version":                 s.Version,
		"goos":                    s.GOOS,
		"goarch":                  s.GOARCH,
		"work_dir":                s.WorkDir,
		"config_path":             s.ConfigPath,
		"profiles_path":           s.ProfilesPath,
		"selected_profile":        s.SelectedProfile,
		"selected_mode":           s.SelectedMode,
		"transport_chain":         s.TransportChain,
		"payload_enabled":         s.PayloadEnabled,
		"last_error":              s.LastError,
		"last_event":              s.LastEvent,
		"attempt":                 s.Attempt,
		"network_online":          s.NetworkOnline,
		"default_route":           s.DefaultRoute,
		"interface":               s.Interface,
		"gateway":                 s.Gateway,
		"source_ip":               s.SourceIP,
		"hotspot_enabled":         s.HotspotEnabled,
		"api_addr":                s.APIAddr,
		"resolved_dial":           s.ResolvedDial,
		"resolver_method":         s.ResolverMethod,
		"resolved_ips":            s.ResolvedIPs,
		"dns_mode":                s.DNSMode,
		"dns_servers":             s.DNSServers,
		"pool_size":               s.PoolSize,
		"pool_healthy":            s.PoolHealthy,
		"pool_reconnecting":       s.PoolReconnecting,
		"pool_streams":            s.PoolStreams,
		"pool_max_streams":        s.PoolMaxStreams,
		"pool_last_error":         s.PoolLastError,
		"socks_enabled":           s.SocksEnabled,
		"socks_addr":              s.SocksAddr,
		"socks_running":           s.SocksRunning,
		"transparent_enabled":     s.TransparentEnabled,
		"transparent_addr":        s.TransparentAddr,
		"transparent_running":     s.TransparentRunning,
		"transparent_applied":     s.TransparentApplied,
		"udp_proxy_enabled":       s.UDPProxyEnabled,
		"udp_proxy_running":       s.UDPProxyRunning,
		"udpgw_port":              s.UDPGWPort,
		"hotspot_running":         s.HotspotRunning,
		"cpu_percent":             s.CPUPercent,
		"memory_rss_bytes":        s.MemoryRSSBytes,
		"memory_rss_mb":           s.MemoryRSSMB,
		"system_mem_used_percent": s.SystemMemUsedPct,
		"remote_banner":           s.RemoteBanner,
		"http_statuses":           s.HTTPStatuses,
		"note":                    s.Note,
	}
}

type ProbeResult struct {
	Banner         string
	Statuses       []int
	Preview        string
	ResolvedDial   string
	ResolverMethod string
	ResolvedIPs    []string
}

type RouteInfo struct {
	Online bool
	Raw    string
	Iface  string
	Gw     string
	Src    string
}

func readJSON[T any](path string, out *T) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	// Forward-compatible JSON: allow harmless extra keys in config/profile files.
	// Required fields are checked by validate()/runtime logic instead.
	return dec.Decode(out)
}

func ensureDir(path string) error { return os.MkdirAll(path, 0755) }

func setupLogger(logPath string) (*os.File, error) {
	if err := ensureDir(filepath.Dir(logPath)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	return f, nil
}

func selectedProfile(pf ProfilesFile) *Profile {
	if pf.SelectedID != "" {
		for i := range pf.Profiles {
			if pf.Profiles[i].ID == pf.SelectedID {
				return &pf.Profiles[i]
			}
		}
	}
	for i := range pf.Profiles {
		if pf.Profiles[i].Selected {
			return &pf.Profiles[i]
		}
	}
	if len(pf.Profiles) > 0 {
		return &pf.Profiles[0]
	}
	return nil
}

func main() {
	// Keep RSS lower on Android during many transparent TCP streams.
	// Higher GC threshold = fewer pauses during sustained download bursts.
	debug.SetGCPercent(200)
	// Soft memory limit: let the runtime use up to 192MB RSS before it starts
	// aggressively reclaiming. Prevents OOM kills on 2–3GB Android devices
	// while still leaving headroom. Set via GOMEMLIMIT env to override.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(192 * 1024 * 1024)
	}
	// Use all CPUs — download workloads benefit from parallel I/O goroutines.
	maxProcs := runtime.NumCPU()
	if maxProcs > 8 {
		maxProcs = 8
	}
	if maxProcs < 1 {
		maxProcs = 1
	}
	runtime.GOMAXPROCS(maxProcs)
	if len(os.Args) < 2 {
		fmt.Println("sshcustomd " + Version)
		fmt.Println("usage: sshcustomd run -c config.json -p profiles.json -w /data/adb/sshcustom")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(Version)
	case "run":
		run(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	case "probe":
		probeCLI(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func validate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	cfgPath := fs.String("c", "config.json", "config path")
	profPath := fs.String("p", "profiles.json", "profiles path")
	_ = fs.Parse(args)
	var cfg Config
	var pf ProfilesFile
	if err := readJSON(*cfgPath, &cfg); err != nil {
		fatal(err)
	}
	normalizeConfig(&cfg)
	if err := readJSON(*profPath, &pf); err != nil {
		fatal(err)
	}
	sp := selectedProfile(pf)
	if sp == nil {
		fatal(errors.New("no profile found"))
	}
	if sp.SSH.Host == "" || sp.SSH.Port <= 0 {
		fatal(errors.New("selected profile has invalid ssh host/port"))
	}
	if len(sp.Transport.Chain) == 0 {
		fatal(errors.New("selected profile has empty transport chain"))
	}
	fmt.Println("config OK")
}

func probeCLI(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	cfgPath := fs.String("c", "config.json", "config path")
	profPath := fs.String("p", "profiles.json", "profiles path")
	_ = fs.Parse(args)
	var cfg Config
	var pf ProfilesFile
	if err := readJSON(*cfgPath, &cfg); err != nil {
		fatal(err)
	}
	normalizeConfig(&cfg)
	if err := readJSON(*profPath, &pf); err != nil {
		fatal(err)
	}
	sp := selectedProfile(pf)
	if sp == nil {
		fatal(errors.New("no selected profile"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	r, err := attemptTransport(ctx, cfg, *sp)
	if err != nil {
		fatal(err)
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(1) }

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("c", "/data/adb/sshcustom/config.json", "config path")
	profPath := fs.String("p", "/data/adb/sshcustom/profiles.json", "profiles path")
	workDir := fs.String("w", "/data/adb/sshcustom", "work dir")
	idleMode := fs.Bool("idle", false, "start in idle mode (WebUI only, no tunnel)")
	_ = fs.Parse(args)

	runDir := filepath.Join(*workDir, "run")
	_ = ensureDir(runDir)
	logFile, err := setupLogger(filepath.Join(runDir, "core.log"))
	if err != nil {
		fatal(err)
	}
	defer logFile.Close()

	var cfg Config
	var pf ProfilesFile
	if err := readJSON(*cfgPath, &cfg); err != nil {
		log.Fatal(err)
	}
	normalizeConfig(&cfg)
	if err := readJSON(*profPath, &pf); err != nil {
		log.Fatal(err)
	}
	sp := selectedProfile(pf)
	if sp == nil && !*idleMode {
		log.Fatal("no selected profile")
	}

	ri := routeInfo()
	initialState := "STARTING"
	initialRunning := true
	if *idleMode {
		initialState = "IDLE"
		initialRunning = false
	}

	// Safe defaults for when sp is nil (idle mode with no profiles yet).
	spName, spMode, spChain, spPayload := "", "", []string{}, false
	if sp != nil {
		spName = sp.Name
		spMode = sp.Transport.Mode
		spChain = sp.Transport.Chain
		spPayload = sp.Transport.Payload.Enabled
	}

	state := &State{
		StartedAt:          time.Now(),
		State:              initialState,
		Running:            initialRunning,
		Connected:          false,
		SSHAuthenticated:   false,
		TransportReady:     false,
		Version:            Version,
		GOOS:               runtime.GOOS,
		GOARCH:             runtime.GOARCH,
		WorkDir:            *workDir,
		ConfigPath:         *cfgPath,
		ProfilesPath:       *profPath,
		SelectedProfile:    spName,
		SelectedMode:       spMode,
		TransportChain:     strings.Join(spChain, " -> "),
		PayloadEnabled:     spPayload,
		NetworkOnline:      ri.Online,
		DefaultRoute:       ri.Raw,
		Interface:          ri.Iface,
		Gateway:            ri.Gw,
		SourceIP:           ri.Src,
		HotspotEnabled:     cfg.Hotspot.Enabled,
		SocksEnabled:       cfg.LocalProxy.SocksEnabled,
		SocksAddr:          socksAddr(cfg),
		TransparentEnabled: cfg.TransparentProxy.Enabled,
		TransparentAddr:    transparentAddr(cfg),
		UDPProxyEnabled:    cfg.UDPProxy.Enabled,
		UDPGWPort:          cfg.UDPProxy.UDPGWPort,
		DNSMode:            cfg.DNS.Mode,
		DNSServers:         append([]string(nil), cfg.DNS.Servers...),
		Note:               "v3.0.0: VPN Chain — Windscribe OpenVPN over SSH SOCKS5. TPROXY TCP+UDP, fast reconnect, KSU fix.",
	}

	log.Printf("SSHCustom daemon %s starting (idle=%v)", Version, *idleMode)
	if sp != nil {
		log.Printf("profile=%q mode=%s payload=%v ssh=%s:%d user=%s", sp.Name, sp.Transport.Mode, sp.Transport.Payload.Enabled, sp.SSH.Host, sp.SSH.Port, sp.SSH.Username)
		log.Printf("transport chain=%s", strings.Join(sp.Transport.Chain, " -> "))
	}
	log.Printf("dns mode=%s servers=%v", cfg.DNS.Mode, cfg.DNS.Servers)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var profileMu sync.Mutex
	var configMu sync.RWMutex
	getConfig := func() Config {
		configMu.RLock()
		defer configMu.RUnlock()
		return cfg
	}
	saveRuntimeConfig := func(next Config) error {
		normalizeConfig(&next)
		if err := saveConfig(*cfgPath, next); err != nil {
			return err
		}
		configMu.Lock()
		cfg = next
		configMu.Unlock()
		state.set(func() {
			state.HotspotEnabled = next.Hotspot.Enabled
			state.SocksEnabled = next.LocalProxy.SocksEnabled
			state.SocksAddr = socksAddr(next)
			state.TransparentEnabled = next.TransparentProxy.Enabled
			state.TransparentAddr = transparentAddr(next)
			state.UDPProxyEnabled = next.UDPProxy.Enabled
			state.UDPGWPort = next.UDPProxy.UDPGWPort
			state.DNSMode = next.DNS.Mode
			state.DNSServers = append([]string(nil), next.DNS.Servers...)
			state.LastEvent = "configuration updated; restart may be required"
		})
		return nil
	}

	// Forward-declare tunnel lifecycle closures (assigned after HTTP server starts)
	var startTunnel func()
	var stopTunnel func()

	// The single active SSH client (lean engine). nil while idle/reconnecting.
	// Populated by tunnelLoop; read by the latency / public-ip endpoints.
	var sshClient atomic.Pointer[tunClient]

	mux := http.NewServeMux()
	// Only the v1 API surface is registered. The legacy /api/{status,
	// profiles, profile/*, control, config, health, logs/*} endpoints from
	// pre-2.0 module versions are gone — everything routes through the
	// /api/v1/* envelope shape now. Any third-party scripts that still hit
	// the old paths will get 404 and need updating.
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeV1OK(w, apiv1.HealthResponse{Status: "ok", Version: Version})
	})
	// Latency probe: TCP connect time to a target (default Google) THROUGH the
	// SSH tunnel. Powers the Home "latency" card.
	mux.HandleFunc("/api/v1/latency", func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSpace(r.URL.Query().Get("target"))
		if target == "" {
			target = "www.google.com:443"
		}
		cl := sshClient.Load()
		if cl == nil {
			writeV1Error(w, http.StatusServiceUnavailable, errors.New("tunnel not connected"))
			return
		}
		ms, err := measureLatency(r.Context(), cl, target)
		if err != nil {
			writeV1Error(w, http.StatusBadGateway, err)
			return
		}
		writeV1OK(w, map[string]any{"target": target, "latency_ms": ms})
	})
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		current := getConfig()
		writeV1OK(w, map[string]any{
			"runtime":      state.Snapshot(),
			"config":       configSummary(current),
			"capabilities": apiCapabilities(current),
			"paths": map[string]string{
				"work_dir":      *workDir,
				"config_path":   *cfgPath,
				"profiles_path": *profPath,
				"run_dir":       runDir,
				"webroot":       filepath.Join(*workDir, "webroot"),
			},
		})
	})
	mux.HandleFunc("/api/v1/network/public-ip", func(w http.ResponseWriter, r *http.Request) {
		refresh := r.URL.Query().Get("refresh") == "1"
		ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
		defer cancel()
		writeV1OK(w, lookupPublicIPs(ctx, getConfig(), refresh))
	})
	mux.HandleFunc("/api/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		writeV1OK(w, diagnosticsSnapshot(state, getConfig(), runDir))
	})
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeV1OK(w, getConfig())
		case http.MethodPost, http.MethodPatch:
			var req apiv1.ConfigPatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeV1Error(w, http.StatusBadRequest, err)
				return
			}
			next, changed, restartRequired, err := applyConfigPatch(getConfig(), req)
			if err != nil {
				writeV1Error(w, http.StatusBadRequest, err)
				return
			}
			if err := saveRuntimeConfig(next); err != nil {
				writeV1Error(w, http.StatusInternalServerError, err)
				return
			}
			restartPending := false
			if req.Restart && restartRequired {
				go func() {
					stopTunnel()
					time.Sleep(500 * time.Millisecond)
					startTunnel()
				}()
				restartPending = true
			}
			writeV1OK(w, apiv1.ConfigUpdateResponse{
				Config:         next,
				Restart:        req.Restart,
				RestartPending: restartPending,
				Changed:        changed,
			})
		default:
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("GET, POST, or PATCH required"))
		}
	})
	mux.HandleFunc("/api/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		profileMu.Lock()
		defer profileMu.Unlock()
		latest, err := loadProfiles(*profPath)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		pf = latest
		writeV1OK(w, latest)
	})
	mux.HandleFunc("/api/v1/profile/current", func(w http.ResponseWriter, r *http.Request) {
		profileMu.Lock()
		defer profileMu.Unlock()
		latest, err := loadProfiles(*profPath)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		sp := selectedProfile(latest)
		if sp == nil {
			writeV1Error(w, http.StatusNotFound, errors.New("no selected profile"))
			return
		}
		writeV1OK(w, sp)
	})
	mux.HandleFunc("/api/v1/profile/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req struct {
			SelectedID string `json:"selected_id"`
			Restart    bool   `json:"restart"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		profileMu.Lock()
		latest, err := loadProfiles(*profPath)
		if err == nil {
			err = selectProfileByID(&latest, req.SelectedID)
		}
		if err == nil {
			err = saveProfiles(*profPath, latest)
		}
		if err == nil {
			pf = latest
		}
		profileMu.Unlock()
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Restart {
			go func() {
				stopTunnel()
				time.Sleep(500 * time.Millisecond)
				startTunnel()
			}()
		}
		writeV1OK(w, map[string]any{"selected_id": req.SelectedID, "restart": req.Restart})
	})
	mux.HandleFunc("/api/v1/profile/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		if req.ID == "" {
			writeV1Error(w, http.StatusBadRequest, errors.New("id is required"))
			return
		}
		profileMu.Lock()
		latest, err := loadProfiles(*profPath)
		if err == nil {
			found := false
			filtered := make([]Profile, 0, len(latest.Profiles))
			for _, p := range latest.Profiles {
				if p.ID == req.ID {
					found = true
					continue
				}
				filtered = append(filtered, p)
			}
			if !found {
				err = errors.New("profile not found")
			} else {
				latest.Profiles = filtered
				if latest.SelectedID == req.ID {
					latest.SelectedID = ""
				}
				err = saveProfiles(*profPath, latest)
				if err == nil {
					pf = latest
				}
			}
		}
		profileMu.Unlock()
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		writeV1OK(w, map[string]any{"deleted": req.ID})
	})
	mux.HandleFunc("/api/v1/profile/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req SaveProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		profileMu.Lock()
		latest, err := loadProfiles(*profPath)
		if err == nil {
			err = upsertProfile(&latest, req)
		}
		if err == nil {
			err = saveProfiles(*profPath, latest)
		}
		if err == nil {
			pf = latest
		}
		profileMu.Unlock()
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Restart {
			go func() {
				stopTunnel()
				time.Sleep(500 * time.Millisecond)
				startTunnel()
			}()
		}
		writeV1OK(w, map[string]any{"selected_id": req.ID, "restart": req.Restart})
	})
	mux.HandleFunc("/api/v1/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		switch strings.TrimSpace(strings.ToLower(req.Action)) {
		case "start":
			go startTunnel()
			writeV1OK(w, map[string]any{"action": "start"})
		case "stop":
			go stopTunnel()
			writeV1OK(w, map[string]any{"action": "stop"})
		case "restart":
			go func() {
				stopTunnel()
				time.Sleep(500 * time.Millisecond)
				startTunnel()
			}()
			writeV1OK(w, map[string]any{"action": "restart"})
		default:
			writeV1Error(w, http.StatusBadRequest, fmt.Errorf("unsupported action: %s", req.Action))
		}
	})
	mux.HandleFunc("/api/v1/logs/core", func(w http.ResponseWriter, r *http.Request) { serveLog(w, filepath.Join(runDir, "core.log")) })
	mux.HandleFunc("/api/v1/logs/control", func(w http.ResponseWriter, r *http.Request) { serveLog(w, filepath.Join(runDir, "control.log")) })
	mux.HandleFunc("/api/v1/logs/action", func(w http.ResponseWriter, r *http.Request) { serveLog(w, filepath.Join(runDir, "action.log")) })
	// Clear endpoint truncates the named log on disk. POST is required so
	// accidental browser pre-fetch can't wipe logs. Truncating core.log while
	// the daemon holds it open is safe on Linux: log.SetOutput keeps writing
	// at the (now zero) end-of-file. We seek back to 0 to avoid sparse files.
	clearLog := func(w http.ResponseWriter, r *http.Request, name string) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		path := filepath.Join(runDir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		_ = f.Close()
		log.Printf("log cleared via API: %s", name)
		writeV1OK(w, map[string]any{"cleared": name})
	}
	mux.HandleFunc("/api/v1/logs/core/clear", func(w http.ResponseWriter, r *http.Request) { clearLog(w, r, "core.log") })
	mux.HandleFunc("/api/v1/logs/control/clear", func(w http.ResponseWriter, r *http.Request) { clearLog(w, r, "control.log") })
	mux.HandleFunc("/api/v1/logs/action/clear", func(w http.ResponseWriter, r *http.Request) { clearLog(w, r, "action.log") })
	// Server-Sent Events stream of state changes. The client opens a long
	// connection; we send the current snapshot immediately, then push a fresh
	// snapshot every time State.broadcast() fires (i.e. on every state.set).
	// A 25 s heartbeat keeps idle proxies/Android battery savers from killing
	// the connection. Falls back gracefully — clients that can't keep an
	// EventSource open (e.g. behind a buggy proxy) just keep polling /status.
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeV1Error(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx-style buffering
		w.WriteHeader(http.StatusOK)

		send := func(event string, payload any) bool {
			data, err := json.Marshal(payload)
			if err != nil {
				return false
			}
			if event != "" {
				if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
					return false
				}
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}

		statusPayload := func() map[string]any {
			current := getConfig()
			return map[string]any{
				"runtime":      state.Snapshot(),
				"config":       configSummary(current),
				"capabilities": apiCapabilities(current),
				"paths": map[string]string{
					"work_dir":      *workDir,
					"config_path":   *cfgPath,
					"profiles_path": *profPath,
					"run_dir":       runDir,
					"webroot":       filepath.Join(*workDir, "webroot"),
				},
			}
		}

		// Initial snapshot so the dashboard renders without waiting for the
		// next mutation.
		if !send("status", statusPayload()) {
			return
		}

		ch, cleanup := state.Subscribe()
		defer cleanup()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if !send("status", statusPayload()) {
					return
				}
			case <-heartbeat.C:
				// Comment line keeps the connection warm without
				// triggering an EventSource onmessage handler.
				if _, err := w.Write([]byte(": ping\n\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})

	// --- VPN Chain API endpoints ---
	vpnchainScript := filepath.Join(*workDir, "vpnchain", "vpnchain.sh")
	vpnchainRun := func(args ...string) (string, error) {
		cmd := exec.Command("/system/bin/sh", append([]string{vpnchainScript}, args...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = strings.TrimSpace(stdout.String())
			}
			if errMsg == "" {
				errMsg = err.Error()
			}
			return "", errors.New(errMsg)
		}
		return strings.TrimSpace(stdout.String()), nil
	}

	mux.HandleFunc("/api/v1/vpnchain/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req struct{ Location string `json:"location"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Location == "" {
			writeV1Error(w, http.StatusBadRequest, errors.New("location is required"))
			return
		}
		out, err := vpnchainRun("start", req.Location)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		writeV1OK(w, map[string]any{"result": out})
	})

	mux.HandleFunc("/api/v1/vpnchain/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		out, err := vpnchainRun("stop")
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		writeV1OK(w, map[string]any{"result": out})
	})

	mux.HandleFunc("/api/v1/vpnchain/switch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("POST required"))
			return
		}
		var req struct{ Location string `json:"location"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Location == "" {
			writeV1Error(w, http.StatusBadRequest, errors.New("location is required"))
			return
		}
		out, err := vpnchainRun("switch", req.Location)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, err)
			return
		}
		writeV1OK(w, map[string]any{"result": out})
	})

	mux.HandleFunc("/api/v1/vpnchain/status", func(w http.ResponseWriter, r *http.Request) {
		out, err := vpnchainRun("status")
		if err != nil {
			writeV1OK(w, map[string]any{"running": false, "location": "", "ip": ""})
			return
		}
		var status map[string]any
		if err := json.Unmarshal([]byte(out), &status); err != nil {
			writeV1OK(w, map[string]any{"running": false, "location": "", "ip": ""})
			return
		}
		writeV1OK(w, status)
	})

	mux.HandleFunc("/api/v1/vpnchain/locations", func(w http.ResponseWriter, r *http.Request) {
		out, err := vpnchainRun("locations")
		if err != nil {
			writeV1OK(w, []string{})
			return
		}
		locations := []string{}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				locations = append(locations, line)
			}
		}
		writeV1OK(w, locations)
	})

	mux.HandleFunc("/api/v1/vpnchain/log", func(w http.ResponseWriter, r *http.Request) {
		serveLog(w, filepath.Join(*workDir, "vpnchain", "run", "vpnchain.log"))
	})

	// Autostart marker. service.sh reads /data/adb/sshcustom/run/autostart at
	// boot; the daemon owns the toggle so the WebUI Settings tab can flip it
	// without users editing files. Body: {"enabled": true|false}.
	mux.HandleFunc("/api/v1/autostart", func(w http.ResponseWriter, r *http.Request) {
		marker := filepath.Join(runDir, "autostart")
		switch r.Method {
		case http.MethodGet:
			_, err := os.Stat(marker)
			writeV1OK(w, map[string]any{"enabled": err == nil})
		case http.MethodPost, http.MethodPut:
			var req struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeV1Error(w, http.StatusBadRequest, err)
				return
			}
			if req.Enabled {
				if err := os.WriteFile(marker, []byte("1\n"), 0644); err != nil {
					writeV1Error(w, http.StatusInternalServerError, err)
					return
				}
			} else {
				if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
					writeV1Error(w, http.StatusInternalServerError, err)
					return
				}
			}
			writeV1OK(w, map[string]any{"enabled": req.Enabled})
		default:
			writeV1Error(w, http.StatusMethodNotAllowed, errors.New("GET, POST, or PUT required"))
		}
	})
	// Captive portal: return HTTP 204 so Android's NetworkMonitor validates
	// the network without needing DNS or SSH tunnel access. Used on zero-bug
	// hosts where the SSH server (Dropbear) can't forward DNS queries.
	// 127.0.0.1:9190 is already exempt from iptables REDIRECT via the
	// 127.0.0.0/8 private CIDR bypass rule.
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/captive/generate_204", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// Dashboard. webui.Handler prefers <work_dir>/webroot/index.html when
	// present and falls back to the embedded HTML otherwise. This means a
	// fresh install always has a working UI even if the file copy step
	// failed, and developers can hot-edit on disk during testing.
	mux.Handle("/", webui.Handler(*workDir))

	addr := net.JoinHostPort(cfg.API.Host, strconv.Itoa(cfg.API.Port))
	srv := &http.Server{
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	srv.SetKeepAlivesEnabled(false) // dashboard polls are short-lived; no need for persistent conns
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("API listen failed on http://%s: %v", addr, err)
		state.set(func() {
			state.LastError = fmt.Sprintf("API listen failed on %s: %v", addr, err)
			state.LastEvent = "API unavailable; likely port conflict"
		})
	} else {
		go func() {
			log.Printf("API listening on http://%s", addr)
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("API error: %v", err)
			}
		}()
	}

	go startMetricsSampler(ctx, state)

	// --- Tunnel lifecycle management ---
	// The daemon stays alive always; tunnel start/stop/restart is controlled
	// via the API or autostart. module.prop is updated to reflect tunnel state.
	modulePropPath := "/data/adb/modules/sshcustom_vpnchain/module.prop"
	updateModuleProp := func(status string) {
		var desc string
		switch status {
		case "running":
			desc = "description=[ON] SSHCustom-Magisk - running"
		case "standby":
			desc = "description=[--] SSHCustom-Magisk - standby"
		default:
			desc = "description=[OFF] SSHCustom-Magisk - disconnected"
		}
		data, err := os.ReadFile(modulePropPath)
		if err != nil {
			return
		}
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "description=") {
				lines[i] = desc
				found = true
				break
			}
		}
		if !found {
			return
		}
		out := strings.Join(lines, "\n")
		tmp := modulePropPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0644); err != nil {
			return
		}
		if err := os.Rename(tmp, modulePropPath); err != nil {
			os.Remove(tmp)
			return
		}
	}

	var tunnelCancel context.CancelFunc
	var tunnelRunning atomic.Bool
	var explicitStop atomic.Bool
	var restartBackoff time.Duration
	var tunnelDone chan struct{} // closed when the tunnelLoop goroutine exits

	startTunnel = func() {
		if tunnelRunning.Load() {
			return
		}
		netClean := filepath.Join(*workDir, "net_clean.sh")
		if _, err := os.Stat(netClean); err == nil {
			exec.Command("/system/bin/sh", netClean).Run()
		}
		// Re-read profiles in case they changed
		profileMu.Lock()
		latest, err := loadProfiles(*profPath)
		if err == nil {
			pf = latest
		}
		profileMu.Unlock()
		sp = selectedProfile(pf)
		if sp == nil {
			state.set(func() {
				state.LastError = "no selected profile"
				state.LastEvent = "cannot start tunnel: no profile selected"
			})
			return
		}
		tunnelRunning.Store(true)
		explicitStop.Store(false)
		restartBackoff = 0
		done := make(chan struct{})
		tunnelDone = done
		var tunnelCtx context.Context
		tunnelCtx, tunnelCancel = context.WithCancel(ctx)
		state.set(func() {
			state.Running = true
			state.State = "STARTING"
			state.TunnelStartedAt = time.Now()
			state.SelectedProfile = sp.Name
			state.SelectedMode = sp.Transport.Mode
			state.TransportChain = strings.Join(sp.Transport.Chain, " -> ")
			state.PayloadEnabled = sp.Transport.Payload.Enabled
			state.LastEvent = "tunnel starting"
			state.LastError = ""
		})
		log.Printf("starting tunnel with profile %q (mode=%s)", sp.Name, sp.Transport.Mode)
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					log.Printf("tunnel panic recovered: %v", r)
				}
			}()
			tunnelLoop(tunnelCtx, getConfig, *sp, state, &sshClient, *workDir)

		if explicitStop.Load() || ctx.Err() != nil {
			tunnelRunning.Store(false)
			state.set(func() {
				state.Running = false
				state.Connected = false
				state.SSHAuthenticated = false
				state.TransportReady = false
				state.TunnelStartedAt = time.Time{}
				if state.State != "IDLE" {
					state.State = "IDLE"
					state.LastEvent = "tunnel stopped"
				}
			})
			return
		}

		tunnelRunning.Store(false)
		if restartBackoff <= 0 {
			restartBackoff = 2 * time.Second
		}
		delay := restartBackoff
		restartBackoff *= 2
		if restartBackoff > 60*time.Second {
			restartBackoff = 60 * time.Second
		}
		log.Printf("tunnel exited unexpectedly; auto-restarting in %v...", delay)
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		if ctx.Err() == nil && !explicitStop.Load() {
			startTunnel()
		}
	}()
	}

	stopTunnel = func() {
		if !tunnelRunning.Load() {
			return
		}
		explicitStop.Store(true)
		if tunnelCancel != nil {
			tunnelCancel()
		}
		sshClient.Store(nil)
		// Wait for the tunnelLoop goroutine to fully exit before running
		// net_clean.sh. Without this, tunnelLoop's deferred teardown() may
		// reinstall iptables rules after net_clean.sh removes them.
		if tunnelDone != nil {
			select {
			case <-tunnelDone:
			case <-time.After(5 * time.Second):
				log.Printf("[stop] tunnel goroutine did not exit within 5s — proceeding with cleanup anyway")
			}
		}
		// Clean iptables
		netClean := filepath.Join(*workDir, "net_clean.sh")
		if _, err := os.Stat(netClean); err == nil {
			cmd := exec.Command("/system/bin/sh", netClean)
			if err := cmd.Run(); err != nil {
				log.Printf("[stop] net_clean.sh failed: %v", err)
			}
		}
		state.set(func() {
			state.Running = false
			state.Connected = false
			state.SSHAuthenticated = false
			state.TransportReady = false
			state.State = "IDLE"
			state.LastEvent = "tunnel stopped and rules cleaned"
			state.LastError = ""
			state.TunnelStartedAt = time.Time{}
			state.PoolSize = 0
			state.PoolHealthy = 0
			state.PoolReconnecting = 0
			state.PoolStreams = 0
		})
		log.Printf("tunnel stopped")
	}

	// Monitor state changes to sync module.prop with tunnel state.
	// Rate-limited: max 1 write per 3s to avoid thrashing module.prop
	// during startup/stop transitions. Daemon is the sole writer — the shell
	// script no longer touches module.prop.
	go func() {
		ch, cleanup := state.Subscribe()
		defer cleanup()
		lastStatus := ""
		var lastWrite time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				state.mu.RLock()
				var status string
				if state.Connected && state.SSHAuthenticated {
					status = "running"
				} else if state.Running && state.State == "PAUSED_NO_NETWORK" {
					status = "standby"
				} else if state.Running {
					status = "running"
				} else {
					status = "disconnected"
				}
				state.mu.RUnlock()
				if status != lastStatus && time.Since(lastWrite) > 3*time.Second {
					lastStatus = status
					lastWrite = time.Now()
					updateModuleProp(status)
				}
			}
		}
	}()

	if !*idleMode {
		// Normal mode: start tunnel immediately
		startTunnel()
	} else {
		log.Printf("daemon started in idle mode (WebUI only); tunnel not started")
	}

	<-ctx.Done()
	log.Printf("shutdown signal received")
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = srv.Shutdown(c)
	cancel()
	state.set(func() {
		state.State = "STOPPED"
		state.Running = false
		state.Connected = false
		state.TransportReady = false
	})
	log.Printf("SSHCustom daemon stopped")
}



func shortBannerLog(message string) string {
	m := strings.TrimSpace(message)
	if m == "" {
		return ""
	}
	lower := strings.ToLower(m)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<body") {
		return fmt.Sprintf("http/html banner suppressed (%d bytes)", len(m))
	}
	if len(m) > 160 {
		m = m[:160] + "..."
	}
	return m
}

// packetErrCount throttles "packet too large" log spam.
// These errors happen many times/second under load and fill the log.
var packetErrCount struct {
	sync.Mutex
	count int
	last  time.Time
}

func logTunnelOpenError(prefix, target string, err error) {
	if err == nil {
		return
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "bad record mac") || strings.Contains(s, "error decoding message") || strings.Contains(s, "use of closed network connection") || strings.Contains(s, "eof") {
		log.Printf("%s failed target=%s: transport reset; reconnecting SSH session", prefix, target)
		return
	}
	if strings.Contains(s, "connection timed out") || strings.Contains(s, "i/o timeout") {
		packetErrCount.Lock()
		packetErrCount.count++
		if time.Since(packetErrCount.last) > 10*time.Second {
			log.Printf("%s ssh connection timed out (x%d streams in last 10s): transport dead; will reconnect", prefix, packetErrCount.count)
			packetErrCount.count = 0
			packetErrCount.last = time.Now()
		}
		packetErrCount.Unlock()
		return
	}
	// Suppress packet-too-large spam — log once per 10s with count
	if strings.Contains(s, "packet too large") || strings.Contains(s, "invalid packet length") {
		packetErrCount.Lock()
		packetErrCount.count++
		if time.Since(packetErrCount.last) > 10*time.Second {
			log.Printf("%s packet size error (x%d in last 10s): Dropbear sent oversized packet; will fix on next pool connect", prefix, packetErrCount.count)
			packetErrCount.count = 0
			packetErrCount.last = time.Now()
		}
		packetErrCount.Unlock()
		return
	}
	log.Printf("%s failed target=%s: %v", prefix, target, err)
}

type prefixConn struct {
	net.Conn
	prefix *bytes.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if c.prefix != nil && c.prefix.Len() > 0 {
		return c.prefix.Read(p)
	}
	return c.Conn.Read(p)
}

func attemptSSHAuth(ctx context.Context, cfg Config, p Profile) (*xssh.Client, ProbeResult, error) {
	conn, res, err := openPreparedSSHConn(ctx, cfg, p)
	if err != nil {
		return nil, res, err
	}
	sshPassword := p.SSH.Password
	if sshPassword == "" {
		// Legacy SSHGo-style profiles sometimes use identical user/pass or lose the
		// password field during import. Falling back to username avoids blank auth.
		sshPassword = p.SSH.Username
	}
	sshCfg := &xssh.ClientConfig{
		User: p.SSH.Username,
		Auth: []xssh.AuthMethod{
			xssh.Password(sshPassword),
			xssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = sshPassword
				}
				return answers, nil
			}),
		},
		HostKeyCallback: xssh.InsecureIgnoreHostKey(),
		Timeout:         time.Duration(secondsDefault(cfg.Performance.ConnectTimeoutSec, 20)) * time.Second,
		BannerCallback: func(message string) error {
			msg := shortBannerLog(message)
			if msg != "" {
				log.Printf("server message: %s", msg)
			}
			return nil
		},
		Config: xssh.Config{
			KeyExchanges: []string{"curve25519-sha256", "curve25519-sha256@libssh.org", "ecdh-sha2-nistp256", "diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1", "diffie-hellman-group-exchange-sha256"},
			Ciphers:      []string{"chacha20-poly1305@openssh.com", "aes128-ctr", "aes192-ctr", "aes256-ctr"},
			MACs:         []string{"hmac-sha1", "hmac-sha2-256"},
		},
	}
	addr := net.JoinHostPort(p.SSH.Host, strconv.Itoa(p.SSH.Port))
	sshConn, chans, reqs, err := xssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		conn.Close()
		return nil, res, fmt.Errorf("ssh auth/handshake failed: %w", err)
	}
	return xssh.NewClient(sshConn, chans, reqs), res, nil
}

func openPreparedSSHConn(ctx context.Context, cfg Config, p Profile) (net.Conn, ProbeResult, error) {
	timeout := time.Duration(secondsDefault(cfg.Performance.ConnectTimeoutSec, 20)) * time.Second
	readTimeout := time.Duration(secondsDefault(cfg.Performance.ReadProbeTimeoutSec, 6)) * time.Second
	dialHost, dialPort, err := dialEndpoint(p)
	if err != nil {
		return nil, ProbeResult{}, err
	}
	base, resolved, method, resolvedIPs, err := dialTCPResolved(ctx, cfg, dialHost, dialPort, dialFallbackIPs(p, dialHost))
	res := ProbeResult{ResolvedDial: resolved, ResolverMethod: method, ResolvedIPs: resolvedIPs}
	if err != nil {
		return nil, res, fmt.Errorf("tcp dial failed: %w", err)
	}
	conn := base
	if usesHTTPProxy(p) && strings.EqualFold(p.Transport.HTTPProxy.ConnectMethod, "connect") {
		if err := httpCONNECT(conn, p, readTimeout); err != nil {
			conn.Close()
			return nil, ProbeResult{}, err
		}
	}
	if usesTLS(p) {
		tlsCfg := p.Transport.TLS
		if tlsCfg == nil {
			conn.Close()
			return nil, ProbeResult{}, errors.New("tls mode selected but tls config is missing")
		}
		serverName := tlsCfg.ServerName
		if serverName == "" {
			serverName = p.SSH.Host
		}
		log.Printf("tls handshake server_name=%s insecure_skip_verify=%v", serverName, tlsCfg.InsecureSkipVerify)
		tc := tls.Client(conn, &tls.Config{ServerName: serverName, InsecureSkipVerify: tlsCfg.InsecureSkipVerify, NextProtos: tlsCfg.ALPN, MinVersion: tls.VersionTLS10})
		hctx, cancel := context.WithTimeout(ctx, timeout)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			conn.Close()
			return nil, ProbeResult{}, fmt.Errorf("tls handshake failed: %w", err)
		}
		cs := tc.ConnectionState()
		log.Printf("tls handshake complete version=%s cipher=%s peer_certs=%d", tlsVersionName(cs.Version), tls.CipherSuiteName(cs.CipherSuite), len(cs.PeerCertificates))
		conn = tc
	}
	var all []byte
	if p.Transport.Payload.Enabled {
		payload := renderPayload(p.Transport.Payload.Template, p)
		log.Printf("sending payload timing=%s bytes=%d", p.Transport.Payload.SendTiming, len(payload))
		if _, err := io.WriteString(conn, payload); err != nil {
			conn.Close()
			return nil, ProbeResult{}, fmt.Errorf("payload write failed: %w", err)
		}
		if p.Transport.Payload.ReadResponse {
			buf, err := readProbe(conn, readTimeout)
			if err != nil {
				conn.Close()
				return nil, ProbeResult{}, fmt.Errorf("payload response read failed: %w", err)
			}
			all = append(all, buf...)
		}
	}
	statuses := extractHTTPStatuses(string(all))
	if len(statuses) > 0 && !allowedStatuses(statuses, p.Transport.Payload.AllowStatuses) {
		conn.Close()
		return nil, ProbeResult{Statuses: statuses, Preview: preview(all)}, fmt.Errorf("http status not allowed: %v", statuses)
	}
	banner := extractSSHBanner(string(all))
	idx := bytes.Index(all, []byte("SSH-"))
	if p.Transport.Payload.Enabled && p.Transport.Payload.ReadResponse {
		if banner != "" && idx >= 0 {
			conn = &prefixConn{Conn: conn, prefix: bytes.NewReader(all[idx:])}
		} else if len(statuses) > 0 {
			// VPN-style SSH+Payload+SNI often returns only the HTTP response first.
			// Keep the live socket open and let SSH read the banner after the response.
			log.Printf("payload response statuses=%v; waiting for SSH banner", statuses)
			// 302/301/503 = CDN rate-limiting or redirecting this IP.
			// Evict DNS cache so the next reconnect tries the alternate Cloudflare IP.
			for _, s := range statuses {
				if s == 302 || s == 301 || s == 503 {
					evictDNSCache(p.SSH.Host)
					if p.Transport.HTTPProxy != nil {
						evictDNSCache(p.Transport.HTTPProxy.Host)
					}
					log.Printf("payload got %d from CDN — DNS cache evicted, next retry uses alternate IP", s)
					break
				}
			}
		} else {
			conn.Close()
			return nil, ProbeResult{Statuses: statuses, Preview: preview(all)}, fmt.Errorf("transport did not expose SSH banner; preview=%q", preview(all))
		}
	}
	res.Banner = banner
	res.Statuses = statuses
	res.Preview = preview(all)
	return conn, res, nil
}

func attemptTransport(ctx context.Context, cfg Config, p Profile) (ProbeResult, error) {
	timeout := time.Duration(secondsDefault(cfg.Performance.ConnectTimeoutSec, 20)) * time.Second
	readTimeout := time.Duration(secondsDefault(cfg.Performance.ReadProbeTimeoutSec, 6)) * time.Second

	dialHost, dialPort, err := dialEndpoint(p)
	if err != nil {
		return ProbeResult{}, err
	}
	c, resolved, method, resolvedIPs, err := dialTCPResolved(ctx, cfg, dialHost, dialPort, dialFallbackIPs(p, dialHost))
	res := ProbeResult{ResolvedDial: resolved, ResolverMethod: method, ResolvedIPs: resolvedIPs}
	if err != nil {
		return res, fmt.Errorf("tcp dial failed: %w", err)
	}
	defer c.Close()

	conn := c
	if usesHTTPProxy(p) && strings.EqualFold(p.Transport.HTTPProxy.ConnectMethod, "connect") {
		if err := httpCONNECT(conn, p, readTimeout); err != nil {
			return ProbeResult{}, err
		}
	}

	if usesTLS(p) {
		tlsCfg := p.Transport.TLS
		if tlsCfg == nil {
			return ProbeResult{}, errors.New("tls mode selected but tls config is missing")
		}
		serverName := tlsCfg.ServerName
		if serverName == "" {
			serverName = p.SSH.Host
		}
		log.Printf("tls handshake server_name=%s insecure_skip_verify=%v", serverName, tlsCfg.InsecureSkipVerify)
		tc := tls.Client(conn, &tls.Config{ServerName: serverName, InsecureSkipVerify: tlsCfg.InsecureSkipVerify, NextProtos: tlsCfg.ALPN, MinVersion: tls.VersionTLS10})
		hctx, cancel := context.WithTimeout(ctx, timeout)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			return ProbeResult{}, fmt.Errorf("tls handshake failed: %w", err)
		}
		cs := tc.ConnectionState()
		log.Printf("tls handshake complete version=%s cipher=%s peer_certs=%d", tlsVersionName(cs.Version), tls.CipherSuiteName(cs.CipherSuite), len(cs.PeerCertificates))
		conn = tc
	}

	var all []byte
	if p.Transport.Payload.Enabled {
		payload := renderPayload(p.Transport.Payload.Template, p)
		log.Printf("sending payload timing=%s bytes=%d", p.Transport.Payload.SendTiming, len(payload))
		if _, err := io.WriteString(conn, payload); err != nil {
			return ProbeResult{}, fmt.Errorf("payload write failed: %w", err)
		}
		if p.Transport.Payload.ReadResponse {
			buf, err := readProbe(conn, readTimeout)
			if err != nil {
				return ProbeResult{}, fmt.Errorf("payload response read failed: %w", err)
			}
			all = append(all, buf...)
		}
	} else {
		buf, err := readProbe(conn, readTimeout)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("ssh banner read failed: %w", err)
		}
		all = append(all, buf...)
	}

	statuses := extractHTTPStatuses(string(all))
	if len(statuses) > 0 && !allowedStatuses(statuses, p.Transport.Payload.AllowStatuses) {
		return ProbeResult{Statuses: statuses, Preview: preview(all)}, fmt.Errorf("http status not allowed: %v", statuses)
	}
	banner := extractSSHBanner(string(all))
	if banner == "" && len(statuses) == 0 {
		return ProbeResult{Statuses: statuses, Preview: preview(all)}, fmt.Errorf("transport did not expose SSH banner; preview=%q", preview(all))
	}
	res.Banner = banner
	res.Statuses = statuses
	res.Preview = preview(all)
	return res, nil
}

func dialEndpoint(p Profile) (string, int, error) {
	host := p.SSH.Host
	port := p.SSH.Port
	if usesHTTPProxy(p) {
		if p.Transport.HTTPProxy == nil {
			return "", 0, errors.New("http_proxy mode selected but http_proxy config is missing")
		}
		host = p.Transport.HTTPProxy.Host
		port = p.Transport.HTTPProxy.Port
	}
	if host == "" || port <= 0 {
		return "", 0, fmt.Errorf("invalid dial endpoint %q:%d", host, port)
	}
	return host, port, nil
}

func tuneTCPConn(c net.Conn, cfg Config, serverSide bool) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		ka := time.Duration(secondsDefault(cfg.Performance.KeepAliveSec, 60)) * time.Second
		_ = tc.SetKeepAlivePeriod(ka)

		if serverSide {
			// Carrier socket: set Linux TCP keepalive parameters so a
			// dead link is detected quickly even under write congestion.
			// TCP_USER_TIMEOUT (30): kernel resets connection if any
			// transmitted data remains unACKed for this long.
			// TCP_KEEPIDLE (5):  idle before first keepalive probe.
			// TCP_KEEPINTVL (3): interval between subsequent probes.
			// TCP_KEEPCNT  (2):  number of unanswered probes before dead.
			// Total dead detection: 5 + 3 + 3 = ~11s
			setTCPTimeouts(tc, 30, 5, 3, 2)
		}

		// Deliberately NOT setting large SO_RCVBUF/SO_SNDBUF here. The old
		// build forced 4 MB (server side) / 1 MB (client side) per socket,
		// which — multiplied across a 4-connection pool and every accepted
		// app connection — was the dominant cause of high working-set RAM.
		// Linux autotuning sizes these dynamically and saturates a mobile
		// uplink fine with a single SSH connection. (vpnchain does the same.)
		_ = serverSide
	}
}

func dialTCPResolved(ctx context.Context, cfg Config, host string, port int, fallbackIPs []string) (net.Conn, string, string, []string, error) {
	portStr := strconv.Itoa(port)

	// Literal IP — no DNS needed at all
	if ip := net.ParseIP(host); ip != nil {
		addr := net.JoinHostPort(host, portStr)
		log.Printf("dial tcp %s method=literal_ip", addr)
		d := baseDialer(cfg)
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			tuneTCPConn(c, cfg, true)
		}
		return c, addr, "literal_ip", []string{ip.String()}, err
	}

	if cfg.DNS.Mode != "device" {
		ips, method := dnsx.ResolveHost(ctx, dnsx.Config{Mode: "device", Servers: nil}, host)
		if len(ips) > 0 {
			var lastErr error
			for _, ip := range rotateIPs(ips) {
				ipAddr := net.JoinHostPort(ip, portStr)
				log.Printf("dial tcp %s host=%s method=%s", ipAddr, host, method)
				d := baseDialer(cfg)
				c, err := d.DialContext(ctx, "tcp", ipAddr)
				if err == nil {
					tuneTCPConn(c, cfg, true)
					return c, ipAddr, method, ips, nil
				}
				lastErr = err
				log.Printf("dial failed %s host=%s: %v", ipAddr, host, err)
			}
			log.Printf("configured DNS mode=%s resolved host=%s but all dials failed: %v", cfg.DNS.Mode, host, lastErr)
		} else {
			log.Printf("configured DNS mode=%s failed for host=%s; falling back to device resolver path", cfg.DNS.Mode, host)
		}
	}

	// Try system DNS first
	addr := net.JoinHostPort(host, portStr)
	log.Printf("dial tcp %s method=device_system_dns", addr)
	d := baseDialer(cfg)
	if d.Timeout <= 0 || d.Timeout > 10*time.Second {
		d.Timeout = 10 * time.Second
	}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err == nil {
		tuneTCPConn(c, cfg, true)
		return c, addr, "device_system_dns", nil, nil
	}
	log.Printf("device/system hostname dial failed for %s: %v", addr, err)

	// Shell DNS with in-memory cache (5 min TTL) — avoids repeated ping subprocesses
	ips, method := dnsx.ResolveHost(ctx, dnsx.Config{Mode: "device", Servers: nil}, host)
	if len(ips) == 0 {
		// Last resort: use profile fallback IPs if configured and all DNS failed
		if len(fallbackIPs) > 0 {
			for _, ip := range rotateIPs(fallbackIPs) {
				ipAddr := net.JoinHostPort(ip, portStr)
				log.Printf("all DNS failed; trying profile fallback ip %s host=%s", ipAddr, host)
				d := baseDialer(cfg)
				if c, cerr := d.DialContext(ctx, "tcp", ipAddr); cerr == nil {
					tuneTCPConn(c, cfg, true)
					return c, ipAddr, "profile_fallback_ip_last_resort", fallbackIPs, nil
				}
			}
		}
		return nil, addr, "device_system_dns_failed", nil, err
	}
	ips = rotateIPs(ips)
	var lastErr error
	for _, ip := range ips {
		ipAddr := net.JoinHostPort(ip, portStr)
		log.Printf("dial tcp %s host=%s method=%s", ipAddr, host, method)
		d2 := baseDialer(cfg)
		c, err := d2.DialContext(ctx, "tcp", ipAddr)
		if err == nil {
			tuneTCPConn(c, cfg, true)
			return c, ipAddr, method, ips, nil
		}
		lastErr = err
		log.Printf("dial failed %s host=%s: %v", ipAddr, host, err)
	}
	if lastErr == nil {
		lastErr = err
	}
	return nil, net.JoinHostPort(ips[0], portStr), method, ips, lastErr
}



func evictDNSCache(host string) { dnsx.EvictHost(host) }

func dialFallbackIPs(p Profile, host string) []string {
	var out []string
	if strings.EqualFold(host, p.SSH.Host) {
		out = append(out, p.SSH.FallbackIPs...)
	}
	if p.Transport.HTTPProxy != nil && strings.EqualFold(host, p.Transport.HTTPProxy.Host) {
		out = append(out, p.Transport.HTTPProxy.FallbackIPs...)
		out = append(out, p.SSH.FallbackIPs...)
	}
	return dnsx.SanitizeIPv4List(out)
}


func rotateIPs(in []string) []string        { return dnsx.RotateIPs(in) }
func extractIPv4s(s string) []string        { return dnsx.ExtractIPv4s(s) }

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func httpCONNECT(conn net.Conn, p Profile, timeout time.Duration) error {
	target := net.JoinHostPort(p.SSH.Host, strconv.Itoa(p.SSH.Port))
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: Keep-Alive\r\n\r\n", target, target)
	log.Printf("sending HTTP CONNECT target=%s", target)
	if _, err := io.WriteString(conn, req); err != nil {
		return fmt.Errorf("http CONNECT write failed: %w", err)
	}
	data, err := readSome(conn, timeout, 4096)
	if err != nil {
		return fmt.Errorf("http CONNECT response read failed: %w", err)
	}
	statuses := extractHTTPStatuses(string(data))
	if len(statuses) == 0 {
		return fmt.Errorf("http CONNECT response had no status: %q", preview(data))
	}
	if statuses[0] < 200 || statuses[0] > 299 {
		return fmt.Errorf("http CONNECT failed with status %d: %q", statuses[0], preview(data))
	}
	log.Printf("HTTP CONNECT accepted status=%d", statuses[0])
	return nil
}

func readProbe(conn net.Conn, timeout time.Duration) ([]byte, error) {
	maxBytes := 64 * 1024
	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)
	defer conn.SetReadDeadline(time.Time{})
	var out bytes.Buffer
	tmp := make([]byte, 2048)
	for out.Len() < maxBytes {
		n, err := conn.Read(tmp)
		if n > 0 {
			out.Write(tmp[:n])
			s := out.String()
			if strings.Contains(s, "SSH-") {
				return out.Bytes(), nil
			}
			// Some endpoints send multiple HTTP responses first; keep reading until timeout or SSH banner.
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			if errors.Is(err, io.EOF) {
				break
			}
			return out.Bytes(), err
		}
	}
	if out.Len() == 0 {
		return nil, errors.New("no bytes received before timeout")
	}
	return out.Bytes(), nil
}

func readSome(conn net.Conn, timeout time.Duration, max int) ([]byte, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, max)
	n, err := conn.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	if err != nil {
		return nil, err
	}
	return nil, errors.New("empty response")
}

func socksAddr(cfg Config) string {
	host := cfg.LocalProxy.SocksHost
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.LocalProxy.SocksPort
	if port <= 0 {
		port = 1080
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func socks5Handshake(c net.Conn) (string, error) {
	buf := make([]byte, 262)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", fmt.Errorf("unsupported socks version %d", buf[0])
	}
	nmethods := int(buf[1])
	if nmethods < 1 || nmethods > 255 {
		return "", errors.New("invalid methods length")
	}
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return "", err
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", fmt.Errorf("bad request socks version %d", buf[0])
	}
	if buf[1] != 0x01 {
		_ = socks5Reply(c, 0x07)
		return "", fmt.Errorf("unsupported command %d", buf[1])
	}
	atyp := buf[3]
	var host string
	switch atyp {
	case 0x01:
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return "", err
		}
		host = net.IPv4(buf[0], buf[1], buf[2], buf[3]).String()
	case 0x03:
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return "", err
		}
		l := int(buf[0])
		if l <= 0 {
			return "", errors.New("empty domain")
		}
		if _, err := io.ReadFull(c, buf[:l]); err != nil {
			return "", err
		}
		host = string(buf[:l])
	case 0x04:
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return "", err
		}
		host = net.IP(buf[:16]).String()
	default:
		_ = socks5Reply(c, 0x08)
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	port := int(buf[0])<<8 | int(buf[1])
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func socks5Reply(c net.Conn, rep byte) error {
	_, err := c.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func pipeBoth(a net.Conn, b net.Conn, bufSize int, idleTimeout time.Duration) {
	bufSize = normalizedCopyBufferSize(bufSize)
	if idleTimeout <= 0 {
		idleTimeout = 120 * time.Second
	}
	done := make(chan struct{}, 2)
	activity := make(chan struct{}, 2)
	closeBoth := func() {
		_ = a.SetDeadline(time.Now())
		_ = b.SetDeadline(time.Now())
		_ = a.Close()
		_ = b.Close()
	}
	pump := func(dst net.Conn, src net.Conn) {
		defer func() { done <- struct{}{} }()
		// One buffer per direction, allocated for this connection's lifetime
		// and freed (GC'd) when the connection closes. No shared sync.Pool —
		// that pool retained large buffers at the high-water mark and was the
		// main reason working-set RAM climbed under load. vpnchain uses the
		// same per-connection 128 KB buffer model.
		buf := make([]byte, bufSize)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				select {
				case activity <- struct{}{}:
				default:
				}
				if _, werr := writeAll(dst, buf[:n]); werr != nil {
					return
				}

			}
			if err != nil {
				if tc, ok := dst.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				} else {
					_ = dst.SetDeadline(time.Now())
				}
				return
			}
		}
	}
	go pump(b, a)
	go pump(a, b)
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	for {
		select {
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)
		case <-done:
			grace := time.NewTimer(400 * time.Millisecond)
			select {
			case <-done:
			case <-grace.C:
			}
			if !grace.Stop() {
				select {
				case <-grace.C:
				default:
				}
			}
			closeBoth()
			return
		case <-timer.C:
			closeBoth()
			return
		}
	}
}

func normalizedCopyBufferSize(n int) int {
	if n <= 0 {
		n = defaultCopyBufferSize
	}
	if n < 32*1024 {
		n = 32 * 1024
	}
	if n > maxCopyBufferSize {
		n = maxCopyBufferSize
	}
	q := 32 * 1024
	return ((n + q - 1) / q) * q
}

func writeAll(w net.Conn, b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			total += n
			b = b[n:]
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func streamIdleTimeout(cfg Config) time.Duration {
	sec := secondsDefault(cfg.Performance.StreamIdleTimeoutSec, 150)
	if sec < 30 {
		sec = 30
	}
	if sec > 3600 {
		sec = 3600
	}
	return time.Duration(sec) * time.Second
}

func transparentAddr(cfg Config) string {
	port := cfg.TransparentProxy.TCPPort
	if port <= 0 {
		port = 10810
	}
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
}

func transparentUDPAddr(cfg Config) string {
	port := cfg.TransparentProxy.UDPPort
	if port <= 0 {
		port = 10811
	}
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
}

func isLocalOrBlockedTarget(target string, cfg Config) bool {
	h, p, err := net.SplitHostPort(target)
	if err != nil {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	port, _ := strconv.Atoi(p)
	if port == cfg.API.Port || port == cfg.LocalProxy.SocksPort {
		return true
	}
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast()
}

type SaveProfileRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Select    bool   `json:"select"`
	Restart   bool   `json:"restart"`
	SSH       SSH    `json:"ssh"`
	Transport struct {
		Mode      string        `json:"mode"`
		HTTPProxy *HTTPProxyCfg `json:"http_proxy,omitempty"`
		TLS       *TLSCfg       `json:"tls,omitempty"`
		Payload   PayloadCfg    `json:"payload"`
	} `json:"transport"`
}

func loadProfiles(path string) (ProfilesFile, error) {
	var pf ProfilesFile
	err := readJSON(path, &pf)
	return pf, err
}

func saveConfig(path string, cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeConfig(cfg *Config) {
	// DNS mode selection was removed from the WebUI. The resolver is always
	// device/system DNS with shell fallback (handled by the dnsx package).
	// Hardwire it here so a stale config.json that still says google/cloudflare/
	// custom can't change runtime behaviour.
	cfg.DNS.Mode = "device"
	cfg.DNS.Enabled = false
	cfg.DNS.Servers = nil
	if cfg.DNS.TimeoutSec <= 0 {
		cfg.DNS.TimeoutSec = 4
	}
	if len(cfg.Hotspot.Interfaces) == 0 {
		cfg.Hotspot.Interfaces = []string{"wlan+", "swlan+", "ap+", "rndis+", "ncm+", "bt-pan+"}
	}
}



func applyConfigPatch(cfg Config, req apiv1.ConfigPatchRequest) (Config, []string, bool, error) {
	next := cfg
	var changed []string
	restartRequired := false

	if req.Hotspot != nil {
		before := next.Hotspot
		if req.Hotspot.Enabled != nil {
			next.Hotspot.Enabled = *req.Hotspot.Enabled
		}
		if req.Hotspot.TCP != nil {
			next.Hotspot.TCP = *req.Hotspot.TCP
		}
		if req.Hotspot.DNS != nil {
			next.Hotspot.DNS = *req.Hotspot.DNS
		}
		if len(req.Hotspot.Interfaces) > 0 {
			next.Hotspot.Interfaces = req.Hotspot.Interfaces
		}
		normalizeConfig(&next)
		if before.Enabled != next.Hotspot.Enabled || before.TCP != next.Hotspot.TCP || before.DNS != next.Hotspot.DNS || strings.Join(before.Interfaces, ",") != strings.Join(next.Hotspot.Interfaces, ",") {
			changed = append(changed, "hotspot")
			restartRequired = true
		}
	}
	return next, changed, restartRequired, nil
}

func saveProfiles(path string, pf ProfilesFile) error {
	b, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func selectProfileByID(pf *ProfilesFile, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("selected_id is required")
	}
	found := false
	for i := range pf.Profiles {
		pf.Profiles[i].Selected = pf.Profiles[i].ID == id
		if pf.Profiles[i].Selected {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("profile not found: %s", id)
	}
	pf.SelectedID = id
	return nil
}

func upsertProfile(pf *ProfilesFile, req SaveProfileRequest) error {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "SSHCustom Profile"
	}
	creatingNew := req.ID == ""
	if req.ID == "" {
		req.ID = uniqueProfileID(pf, slugify(req.Name))
	}
	if req.ID == "" {
		req.ID = uniqueProfileID(pf, fmt.Sprintf("profile-%d", time.Now().Unix()))
	}
	if strings.TrimSpace(req.SSH.Host) == "" {
		return errors.New("SSH host is required")
	}
	if req.SSH.Port <= 0 || req.SSH.Port > 65535 {
		return errors.New("SSH port must be 1-65535")
	}
	if strings.TrimSpace(req.SSH.Username) == "" {
		return errors.New("SSH username is required")
	}
	mode := normalizeMode(req.Transport.Mode)
	if mode == "" {
		return errors.New("invalid transport mode")
	}
	idx := -1
	for i := range pf.Profiles {
		if pf.Profiles[i].ID == req.ID {
			idx = i
			break
		}
	}
	if creatingNew {
		idx = -1
	}
	oldPassword := ""
	if idx >= 0 {
		oldPassword = pf.Profiles[idx].SSH.Password
	}
	if req.SSH.Password == "" {
		req.SSH.Password = oldPassword
	}
	if req.SSH.AuthType == "" {
		req.SSH.AuthType = "password"
	}
	profile := Profile{ID: req.ID, Name: req.Name, SSH: req.SSH}
	profile.Transport = buildTransport(mode, req.Transport.Payload, req.Transport.HTTPProxy, req.Transport.TLS, profile.SSH)
	if idx >= 0 {
		pf.Profiles[idx] = profile
	} else {
		pf.Profiles = append(pf.Profiles, profile)
	}
	if req.Select || pf.SelectedID == "" || pf.SelectedID == req.ID {
		_ = selectProfileByID(pf, req.ID)
	}
	return nil
}

func normalizeMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "direct", "ssh", "ssh_payload", "ssh+pl":
		return "direct"
	case "http_proxy", "ssh_http_proxy", "ssh+http", "ssh+http_proxy", "ssh+pl+http":
		return "http_proxy"
	case "tls_sni", "sni", "ssh+sni", "ssh+pl+sni":
		return "tls_sni"
	case "http_proxy_tls_sni", "http_tls", "ssh+http+sni", "ssh+pl+http+sni":
		return "http_proxy_tls_sni"
	default:
		return ""
	}
}

func buildTransport(mode string, payload PayloadCfg, hp *HTTPProxyCfg, tlsCfg *TLSCfg, ssh SSH) TConf {
	if payload.SendTiming == "" {
		switch mode {
		case "http_proxy":
			payload.SendTiming = "after_proxy_socket_before_ssh"
		case "tls_sni", "http_proxy_tls_sni":
			payload.SendTiming = "after_tls_before_ssh"
		default:
			payload.SendTiming = "before_ssh"
		}
	}
	if len(payload.AllowStatuses) == 0 {
		payload.AllowStatuses = []int{101, 200, 204, 302}
	}
	chain := []string{"tcp"}
	var outHP *HTTPProxyCfg
	var outTLS *TLSCfg
	if mode == "http_proxy" || mode == "http_proxy_tls_sni" {
		proxy := HTTPProxyCfg{Host: ssh.Host, Port: ssh.Port, ConnectMethod: "socket"}
		if hp != nil {
			proxy = *hp
		}
		if proxy.Host == "" {
			proxy.Host = ssh.Host
		}
		if proxy.Port <= 0 {
			proxy.Port = ssh.Port
		}
		if proxy.ConnectMethod == "" {
			proxy.ConnectMethod = "socket"
		}
		// SNI FIX: TLS handshake on port 80 always times out because port 80
		// speaks plain HTTP, not TLS. If TLS is in the chain and we ended up
		// with port 80, auto-upgrade to 443.
		if mode == "http_proxy_tls_sni" && proxy.Port == 80 {
			proxy.Port = 443
		}
		outHP = &proxy
		chain = append(chain, "http_proxy")
	}
	if mode == "tls_sni" || mode == "http_proxy_tls_sni" {
		tc := TLSCfg{Enabled: true, ServerName: ssh.Host, InsecureSkipVerify: true, ALPN: []string{"http/1.1"}}
		if tlsCfg != nil {
			tc = *tlsCfg
		}
		if tc.ServerName == "" {
			tc.ServerName = ssh.Host
		}
		if len(tc.ALPN) == 0 {
			tc.ALPN = []string{"http/1.1"}
		}
		tc.Enabled = true
		outTLS = &tc
		chain = append(chain, "tls")
	}
	if payload.Enabled {
		chain = append(chain, "payload")
	}
	chain = append(chain, "ssh")
	return TConf{Mode: mode, Chain: chain, HTTPProxy: outHP, TLS: outTLS, Payload: payload}
}

func uniqueProfileID(pf *ProfilesFile, base string) string {
	base = strings.Trim(slugify(base), "-")
	if base == "" {
		base = "profile"
	}
	used := map[string]bool{}
	for _, p := range pf.Profiles {
		used[p.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}



func routeSignature(ri RouteInfo) string {
	if !ri.Online {
		return "offline"
	}
	// Use ONLY the interface name to detect genuine network changes.
	// Source IP changes constantly on mobile CGNAT networks — including it
	// causes false-positive reconnects every few minutes.
	// Gateway can flip during tower handoffs without losing connectivity.
	// The interface name (rmnet_data0, wlan0, etc.) is the reliable signal.
	if iface := strings.TrimSpace(ri.Iface); iface != "" {
		return iface
	}
	// Fallback: just "online" — better than nothing, still beats false reconnects
	return "online"
}

// startMetricsSampler periodically writes a metrics snapshot into State so
// the dashboard always has fresh CPU/memory/goroutine numbers.
//
// 20-second cadence: gives enough resolution to spot a stuck pool or memory
// leak without burning CPU on /proc reads. The first sample lands in State
// immediately so the dashboard shows real numbers as soon as it loads.
func startMetricsSampler(ctx context.Context, st *State) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	sampler := &metrics.Sampler{}
	applySample(st, sampler.Sample())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			applySample(st, sampler.Sample())
		}
	}
}

// applySample copies the metrics package's snapshot into the daemon's
// State struct under the state lock. The State fields existed before the
// metrics package split; we keep them as the public surface to avoid
// rewriting every dashboard call site.
func applySample(st *State, snap metrics.Snapshot) {
	st.set(func() {
		st.CPUPercent = snap.CPUPercent
		st.MemoryRSSBytes = snap.MemoryRSSBytes
		st.MemoryRSSMB = snap.MemoryRSSMB
		st.SystemMemTotalBytes = snap.SystemMemTotal
		st.SystemMemAvailBytes = snap.SystemMemAvail
		st.SystemMemUsedPct = snap.SystemMemUsedPct
		st.Goroutines = snap.Goroutines
	})
}

func renderPayload(t string, p Profile) string {
	repl := map[string]string{
		"[crlf]":       "\r\n",
		"[lf]":         "\n",
		"[host]":       p.SSH.Host,
		"[port]":       strconv.Itoa(p.SSH.Port),
		"[ssh_host]":   p.SSH.Host,
		"[ssh_port]":   strconv.Itoa(p.SSH.Port),
		"[sni]":        "",
		"[proxy_host]": "",
		"[proxy_port]": "",
	}
	if p.Transport.TLS != nil {
		repl["[sni]"] = p.Transport.TLS.ServerName
	}
	if p.Transport.HTTPProxy != nil {
		repl["[proxy_host]"] = p.Transport.HTTPProxy.Host
		repl["[proxy_port]"] = strconv.Itoa(p.Transport.HTTPProxy.Port)
	}
	out := t
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

func extractHTTPStatuses(s string) []int {
	var out []int
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "HTTP/") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func extractSSHBanner(s string) string {
	idx := strings.Index(s, "SSH-")
	if idx < 0 {
		return ""
	}
	tail := s[idx:]
	for _, sep := range []string{"\r\n", "\n"} {
		if j := strings.Index(tail, sep); j >= 0 {
			return strings.TrimSpace(tail[:j])
		}
	}
	if len(tail) > 120 {
		tail = tail[:120]
	}
	return strings.TrimSpace(tail)
}

func allowedStatuses(got []int, allowed []int) bool {
	if len(got) == 0 || len(allowed) == 0 {
		return true
	}
	set := map[int]bool{}
	for _, n := range allowed {
		set[n] = true
	}
	for _, n := range got {
		if !set[n] {
			return false
		}
	}
	return true
}

func preview(b []byte) string {
	s := string(b)
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}

func usesHTTPProxy(p Profile) bool {
	return p.Transport.Mode == "http_proxy" || p.Transport.Mode == "http_proxy_tls_sni" || contains(p.Transport.Chain, "http_proxy")
}
func usesTLS(p Profile) bool {
	return p.Transport.Mode == "tls_sni" || p.Transport.Mode == "http_proxy_tls_sni" || contains(p.Transport.Chain, "tls")
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func secondsDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func baseDialer(cfg Config) net.Dialer {
	timeout := time.Duration(secondsDefault(cfg.Performance.ConnectTimeoutSec, 20)) * time.Second
	keepAlive := time.Duration(secondsDefault(cfg.Performance.KeepAliveSec, 30)) * time.Second
	return net.Dialer{Timeout: timeout, KeepAlive: keepAlive}
}



func routeInfo() RouteInfo {
	// Pure-Go route detection: UDP connect triggers kernel route lookup
	// without actually sending any packets. Zero subprocess overhead.
	// Falls back gracefully if the kernel rejects the connect.
	conn, err := net.DialTimeout("udp", "1.1.1.1:53", 1*time.Second)
	if err != nil {
		return RouteInfo{Online: false, Raw: ""}
	}
	defer conn.Close()
	laddr := conn.LocalAddr().String()
	src, _, _ := net.SplitHostPort(laddr)
	// Get interface name from local address
	iface := ""
	if src != "" {
		ifaceList, ierr := net.Interfaces()
		if ierr == nil {
			for _, ifc := range ifaceList {
				addrs, _ := ifc.Addrs()
				for _, a := range addrs {
					if strings.HasPrefix(a.String(), src+"/") || strings.HasPrefix(a.String(), src) {
						iface = ifc.Name
						break
					}
				}
				if iface != "" {
					break
				}
			}
		}
	}
	return RouteInfo{Online: true, Raw: laddr, Src: src, Iface: iface}
}



func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLSv1.0"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS13:
		return "TLSv1.3"
	default:
		return fmt.Sprintf("0x%x", v)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serveLog(w http.ResponseWriter, path string) {
	stat, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// If log file is larger than 64KB, read only the last 64KB
	// This prevents OOM on Android when logs grow unbounded
	const maxLogRead = 65536
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	
	var b []byte
	if stat.Size() > maxLogRead {
		offset := stat.Size() - maxLogRead
		_, err = f.Seek(offset, io.SeekStart)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b = make([]byte, maxLogRead)
		_, err = io.ReadFull(f, b)
		if err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		b, err = io.ReadAll(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeV1OK(w http.ResponseWriter, data any) {
	writeJSON(w, apiv1.Envelope{APIVersion: apiv1.Version, OK: true, Data: data})
}

func writeV1Error(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	writeJSON(w, apiv1.Envelope{APIVersion: apiv1.Version, OK: false, Error: err.Error()})
}

func configSummary(cfg Config) map[string]any {
	return map[string]any{
		"api": map[string]any{
			"host": cfg.API.Host,
			"port": cfg.API.Port,
		},
		"dns": map[string]any{"mode": cfg.DNS.Mode},
		"hotspot": map[string]any{
			"enabled":    cfg.Hotspot.Enabled,
			"tcp":        cfg.Hotspot.TCP,
			"dns":        cfg.Hotspot.DNS,
			"interfaces": cfg.Hotspot.Interfaces,
		},
		"local_proxy": map[string]any{
			"socks_enabled": cfg.LocalProxy.SocksEnabled,
			"socks_host":    cfg.LocalProxy.SocksHost,
			"socks_port":    cfg.LocalProxy.SocksPort,
		},
		"transparent_proxy": map[string]any{
			"enabled":       cfg.TransparentProxy.Enabled,
			"tcp_port":      cfg.TransparentProxy.TCPPort,
			"chains_prefix": cfg.TransparentProxy.ChainsPrefix,
			"ipv4_only":     true,
		},
	}
}

func apiCapabilities(cfg Config) []apiv1.Capability {
	return []apiv1.Capability{
		{Name: "manual_start", Enabled: cfg.Module.ManualStart, Description: "runtime starts from Magisk/KernelSU action"},
		{Name: "ipv4_transparent_tcp", Enabled: cfg.TransparentProxy.Enabled, Description: "iptables IPv4 TCP redirect"},
		{Name: "hotspot_tcp_share", Enabled: cfg.Hotspot.Enabled && cfg.Hotspot.TCP, Description: "TCP sharing for tethered clients"},
		{Name: "socks5", Enabled: cfg.LocalProxy.SocksEnabled, Description: "local SOCKS5 listener"},
		{Name: "quic_block", Enabled: cfg.TransparentProxy.Enabled, Description: "drops UDP 443/80 so QUIC apps fall back to TCP through the tunnel"},
		{Name: "ipv6_disable", Enabled: cfg.TransparentProxy.Enabled, Description: "disables IPv6 while active to prevent leaks (IPv4-only redirect)"},
		{Name: "captive_portal_bypass", Enabled: cfg.TransparentProxy.Enabled, Description: "disables Android captive-portal detection so apps trust the tunneled network"},
		{Name: "per_app_routing", Enabled: false, Description: "planned future feature"},
		{Name: "ipv6", Enabled: false, Description: "intentionally disabled while the tunnel is active"},
	}
}

func diagnosticsSnapshot(st *State, cfg Config, runDir string) apiv1.DiagnosticsResponse {
	snap := st.Snapshot()
	return apiv1.DiagnosticsResponse{
		Runtime: snap,
		Config:  configSummary(cfg),
		Pool: map[string]any{
			"size":          snap["pool_size"],
			"healthy":       snap["pool_healthy"],
			"reconnecting":  snap["pool_reconnecting"],
			"streams":       snap["pool_streams"],
			"max_streams":   snap["pool_max_streams"],
			"last_error":    snap["pool_last_error"],
			"capacity_hint": fmt.Sprintf("%v/%v healthy, %v active streams", snap["pool_healthy"], snap["pool_size"], snap["pool_streams"]),
		},
		Route: map[string]any{
			"online":        snap["network_online"],
			"interface":     snap["interface"],
			"gateway":       snap["gateway"],
			"source_ip":     snap["source_ip"],
			"default_route": snap["default_route"],
		},
		Performance: map[string]any{
			"cpu_percent":                snap["cpu_percent"],
			"memory_rss_bytes":           snap["memory_rss_bytes"],
			"memory_rss_mb":              snap["memory_rss_mb"],
			"system_mem_used_percent":    snap["system_mem_used_percent"],
			"goroutines":                 snap["goroutines"],
			"copy_buffer_size":           cfg.Performance.BufferSize,
			"connect_timeout_seconds":    secondsDefault(cfg.Performance.ConnectTimeoutSec, 20),
			"keepalive_seconds":          secondsDefault(cfg.Performance.KeepAliveSec, 30),
			"stream_idle_seconds":        secondsDefault(cfg.Performance.StreamIdleTimeoutSec, 600),
			"stream_acquire_seconds":     secondsDefault(cfg.Performance.StreamAcquireTimeoutSec, 15),
			"max_streams_per_ssh_config": cfg.Performance.MaxStreamsPerSSH,
		},
		Logs: map[string]any{
			"core":      logFileInfo(filepath.Join(runDir, "core.log")),
			"control":   logFileInfo(filepath.Join(runDir, "control.log")),
			"action":    logFileInfo(filepath.Join(runDir, "action.log")),
			"watchdog":  logFileInfo(filepath.Join(runDir, "watchdog.log")),
			"net_clean": logFileInfo(filepath.Join(runDir, "net_clean.log")),
		},
	}
}

func logFileInfo(path string) map[string]any {
	info, err := os.Stat(path)
	if err != nil {
		return map[string]any{"path": path, "exists": false}
	}
	return map[string]any{
		"path":     path,
		"exists":   true,
		"size":     info.Size(),
		"modified": info.ModTime().Format(time.RFC3339),
	}
}

const publicIPProvider = "ip-api.com"
const publicIPPath = "/json/?fields=status,message,query,country,regionName,city,isp,org,as,asname,timezone"

var publicIPCache struct {
	sync.Mutex
	resp    apiv1.PublicIPResponse
	expires time.Time
}

type ipAPIResponse struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	Query      string `json:"query"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
	ISP        string `json:"isp"`
	Org        string `json:"org"`
	AS         string `json:"as"`
	ASName     string `json:"asname"`
	Timezone   string `json:"timezone"`
}

func lookupPublicIPs(ctx context.Context, cfg Config, refresh bool) apiv1.PublicIPResponse {
	if !refresh {
		publicIPCache.Lock()
		if time.Now().Before(publicIPCache.expires) {
			cached := publicIPCache.resp
			if cached.Device != nil {
				cached.Device.Cached = true
			}
			if cached.Tunnel != nil {
				cached.Tunnel.Cached = true
			}
			publicIPCache.Unlock()
			return cached
		}
		publicIPCache.Unlock()
	}

	resp := apiv1.PublicIPResponse{
		CheckedAt: time.Now().Format(time.RFC3339),
		Provider:  publicIPProvider,
	}
	// Only the tunnel side hits an external service. The device-side public IP
	// lookup was removed in v2: on Android the Go HTTP client fails with
	// "[::1]:53 connection refused" because dnsproxyd is restricted to system
	// uids, and a public IP for the carrier-side leg isn't useful here. The
	// dashboard now reads the local source IP directly from runtime state.
	resp.Tunnel = lookupPublicIPTunnel(ctx, cfg)

	publicIPCache.Lock()
	publicIPCache.resp = resp
	publicIPCache.expires = time.Now().Add(60 * time.Second)
	publicIPCache.Unlock()
	return resp
}

func lookupPublicIPTunnel(ctx context.Context, cfg Config) *apiv1.PublicIPDetails {
	start := time.Now()
	out := &apiv1.PublicIPDetails{Path: "tunnel"}
	if !cfg.LocalProxy.SocksEnabled {
		out.Error = "SOCKS5 is disabled"
		return out
	}
	body, err := fetchHTTPViaLocalSOCKS(ctx, cfg, publicIPProvider, 80, publicIPPath)
	if err != nil {
		out.Error = err.Error()
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	return parsePublicIPDetails("tunnel", body, time.Since(start))
}

func parsePublicIPDetails(path string, body []byte, latency time.Duration) *apiv1.PublicIPDetails {
	out := &apiv1.PublicIPDetails{Path: path, LatencyMS: latency.Milliseconds()}
	var api ipAPIResponse
	if err := json.Unmarshal(body, &api); err != nil {
		out.Error = err.Error()
		return out
	}
	if api.Status != "" && api.Status != "success" {
		if api.Message == "" {
			api.Message = "public IP lookup failed"
		}
		out.Error = api.Message
		return out
	}
	out.OK = true
	out.IP = api.Query
	out.Country = api.Country
	out.Region = api.RegionName
	out.City = api.City
	out.ISP = api.ISP
	out.Org = api.Org
	out.ASN = publicIPASN(api.AS)
	out.ASName = api.ASName
	out.Timezone = api.Timezone
	return out
}

func publicIPASN(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func fetchHTTPViaLocalSOCKS(ctx context.Context, cfg Config, host string, port int, path string) ([]byte, error) {
	d := baseDialer(cfg)
	c, err := d.DialContext(ctx, "tcp", socksAddr(cfg))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		return nil, err
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		return nil, fmt.Errorf("SOCKS5 auth rejected: %v", reply)
	}
	if len(host) > 255 {
		return nil, errors.New("SOCKS5 host is too long")
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	if err := readSOCKS5ConnectReply(c); err != nil {
		return nil, err
	}
	httpReq := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: SSHCustom-Magisk/%s\r\nAccept: application/json\r\nConnection: close\r\n\r\n", path, host, Version)
	if _, err := c.Write([]byte(httpReq)); err != nil {
		return nil, err
	}
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("public IP lookup over tunnel returned %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64*1024))
}

func readSOCKS5ConnectReply(r io.Reader) error {
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 version %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("SOCKS5 connect failed code=%d", head[1])
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return err
		}
		skip = int(l[0])
	case 0x04:
		skip = 16
	default:
		return fmt.Errorf("unsupported SOCKS5 address type %d", head[3])
	}
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(skip)); err != nil {
			return err
		}
	}
	_, err := io.CopyN(io.Discard, r, 2)
	return err
}
